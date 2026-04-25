package apps

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func installArchiveRelease(s spec, version string, info *ReleaseInfo, onProgress ProgressFunc) error {
	dest, err := versionBinaryPath(s, version)
	if err != nil {
		return err
	}
	versionRoot := filepath.Dir(dest)
	for filepath.Base(versionRoot) != normalizeVersion(version) {
		parent := filepath.Dir(versionRoot)
		if parent == versionRoot {
			return fmt.Errorf("resolving version directory for %s", s.ID)
		}
		versionRoot = parent
	}

	if err := os.RemoveAll(versionRoot); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("resetting %s version directory: %w", s.ID, err)
	}
	if err := os.MkdirAll(versionRoot, 0o755); err != nil {
		return fmt.Errorf("creating %s version directory: %w", s.ID, err)
	}

	assetURLs := append([]string{info.AssetURL}, info.ExtraAssetURLs...)
	for i, assetURL := range assetURLs {
		if strings.TrimSpace(assetURL) == "" {
			return fmt.Errorf("missing %s archive URL", s.ID)
		}
		progress := onProgress
		if i > 0 {
			progress = nil
		}
		if err := downloadAndExtractArchive(assetURL, versionRoot, string(s.ID)+"-archive-*", "downloading "+string(s.ID), progress); err != nil {
			return err
		}
	}

	if _, err := os.Stat(dest); err != nil {
		return fmt.Errorf("%s archive did not produce %q: %w", s.ID, dest, err)
	}
	return nil
}

func downloadAndExtractArchive(url, destDir, pattern, action string, onProgress ProgressFunc) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: returned %d", action, resp.StatusCode)
	}

	src := io.Reader(resp.Body)
	if onProgress != nil {
		src = &progressReader{
			r:     resp.Body,
			total: resp.ContentLength,
			fn:    onProgress,
		}
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("creating archive destination: %w", err)
	}

	tmp, err := os.CreateTemp(destDir, pattern)
	if err != nil {
		return fmt.Errorf("creating archive temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return fmt.Errorf("writing archive: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing archive temp file: %w", err)
	}

	switch {
	case strings.HasSuffix(url, ".tar.gz"):
		return extractTarGz(tmpPath, destDir)
	case strings.HasSuffix(url, ".zip"):
		return extractZip(tmpPath, destDir)
	default:
		return fmt.Errorf("unsupported archive format for %q", url)
	}
}

func extractTarGz(path, destDir string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening archive: %w", err)
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("opening gzip archive: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading tar archive: %w", err)
		}
		if err := extractTarEntry(tr, hdr, destDir); err != nil {
			return err
		}
	}
}

func extractTarEntry(r io.Reader, hdr *tar.Header, destDir string) error {
	target, err := secureArchiveTarget(destDir, hdr.Name)
	if err != nil {
		return err
	}
	if target == "" {
		return nil
	}
	mode := os.FileMode(hdr.Mode)
	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, mode.Perm())
	case tar.TypeReg, tar.TypeRegA:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("creating archive parent directory: %w", err)
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
		if err != nil {
			return fmt.Errorf("creating archive file: %w", err)
		}
		if _, err := io.Copy(f, r); err != nil {
			f.Close()
			return fmt.Errorf("writing archive file: %w", err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("closing archive file: %w", err)
		}
	case tar.TypeSymlink:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("creating archive symlink parent directory: %w", err)
		}
		_ = os.Remove(target)
		if err := os.Symlink(hdr.Linkname, target); err != nil {
			return fmt.Errorf("creating archive symlink: %w", err)
		}
	}
	return nil
}

func extractZip(path, destDir string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("opening zip archive: %w", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		target, err := secureArchiveTarget(destDir, f.Name)
		if err != nil {
			return err
		}
		if target == "" {
			continue
		}
		mode := f.Mode()
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, mode.Perm()); err != nil {
				return fmt.Errorf("creating zip directory: %w", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("creating zip parent directory: %w", err)
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("opening zip file: %w", err)
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
		if err != nil {
			rc.Close()
			return fmt.Errorf("creating zip output file: %w", err)
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			return fmt.Errorf("writing zip output file: %w", err)
		}
		if err := out.Close(); err != nil {
			rc.Close()
			return fmt.Errorf("closing zip output file: %w", err)
		}
		if err := rc.Close(); err != nil {
			return fmt.Errorf("closing zip entry: %w", err)
		}
	}
	return nil
}

func secureArchiveTarget(destDir, name string) (string, error) {
	cleanName := filepath.Clean(filepath.FromSlash(name))
	if cleanName == "." || cleanName == string(filepath.Separator) {
		return "", nil
	}
	target := filepath.Join(destDir, cleanName)
	rel, err := filepath.Rel(destDir, target)
	if err != nil {
		return "", fmt.Errorf("resolving archive target: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry %q escapes destination", name)
	}
	return target, nil
}

func limaReleasePlatform(goos, goarch string) (osName, archName, archiveExt string) {
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "darwin":
		osName = "Darwin"
		archiveExt = "tar.gz"
	case "linux":
		osName = "Linux"
		archiveExt = "tar.gz"
	default:
		return "", "", ""
	}

	switch strings.ToLower(strings.TrimSpace(goarch)) {
	case "arm64":
		if osName == "Linux" {
			archName = "aarch64"
		} else {
			archName = "arm64"
		}
	case "amd64":
		archName = "x86_64"
	default:
		return "", "", ""
	}
	return osName, archName, archiveExt
}

func bunEntrySubpath(goos, goarch string) string {
	assetBase, executable := bunReleasePlatform(goos, goarch)
	if assetBase == "" || executable == "" {
		return ""
	}
	return filepath.Join(assetBase, executable)
}

func bunReleasePlatform(goos, goarch string) (assetBase, executable string) {
	executable = "bun"
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "darwin":
		switch strings.ToLower(strings.TrimSpace(goarch)) {
		case "arm64":
			return "bun-darwin-aarch64", executable
		case "amd64":
			return "bun-darwin-x64", executable
		}
	case "linux":
		switch strings.ToLower(strings.TrimSpace(goarch)) {
		case "arm64":
			return "bun-linux-aarch64", executable
		case "amd64":
			return "bun-linux-x64", executable
		}
	case "windows":
		executable = "bun.exe"
		switch strings.ToLower(strings.TrimSpace(goarch)) {
		case "arm64":
			return "bun-windows-aarch64", executable
		case "amd64":
			return "bun-windows-x64", executable
		}
	}
	return "", ""
}

func zeroboxReleaseAsset(goos, goarch string) string {
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "darwin":
		switch strings.ToLower(strings.TrimSpace(goarch)) {
		case "arm64":
			return "zerobox-aarch64-apple-darwin.tar.gz"
		case "amd64":
			return "zerobox-x86_64-apple-darwin.tar.gz"
		}
	case "linux":
		switch strings.ToLower(strings.TrimSpace(goarch)) {
		case "arm64":
			return "zerobox-aarch64-unknown-linux-gnu.tar.gz"
		case "amd64":
			return "zerobox-x86_64-unknown-linux-gnu.tar.gz"
		}
	}
	return ""
}
