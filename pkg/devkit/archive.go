package devkit

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

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
