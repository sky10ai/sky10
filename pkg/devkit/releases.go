package devkit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
)

func resolveBunRelease(ctx context.Context, s spec, current, requestedVersion string) (*ReleaseInfo, error) {
	version := s.Normalize(requestedVersion)
	if version == "" {
		url := ghReleaseURL(s)
		if strings.TrimSpace(url) == "" {
			return nil, fmt.Errorf("missing %s release URL", s.ID)
		}
		release, err := ghReleaseClient.Latest(ctx, url)
		if err != nil {
			return nil, fmt.Errorf("fetching latest %s release: %w", s.Name, err)
		}
		version = s.Normalize(release.TagName)
	}

	assetBase, _ := bunReleasePlatform(runtime.GOOS, runtime.GOARCH)
	info := &ReleaseInfo{
		ID:        s.ID,
		Installed: current != "",
		Current:   current,
		Latest:    version,
		Available: current == "" || current != version,
	}
	if assetBase == "" || version == "" {
		return info, nil
	}
	info.AssetName = assetBase + ".zip"
	info.AssetURL = fmt.Sprintf("https://github.com/oven-sh/bun/releases/download/bun-%s/%s", version, info.AssetName)
	return info, nil
}

type goRelease struct {
	Version string   `json:"version"`
	Stable  bool     `json:"stable"`
	Files   []goFile `json:"files"`
}

type goFile struct {
	Filename string `json:"filename"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Kind     string `json:"kind"`
}

func resolveGoRelease(ctx context.Context, s spec, current, requestedVersion string) (*ReleaseInfo, error) {
	releases, err := fetchGoReleases(ctx)
	if err != nil {
		return nil, err
	}
	requested := s.Normalize(requestedVersion)
	for _, release := range releases {
		if requested != "" && s.Normalize(release.Version) != requested {
			continue
		}
		if requested == "" && !release.Stable {
			continue
		}
		file, ok := goArchiveForTarget(release.Files, runtime.GOOS, runtime.GOARCH)
		if !ok {
			if requested != "" {
				return &ReleaseInfo{
					ID:        s.ID,
					Installed: current != "",
					Current:   current,
					Latest:    s.Normalize(release.Version),
					Available: current == "" || current != s.Normalize(release.Version),
				}, nil
			}
			continue
		}
		version := s.Normalize(release.Version)
		return &ReleaseInfo{
			ID:        s.ID,
			Installed: current != "",
			Current:   current,
			Latest:    version,
			Available: current == "" || current != version,
			AssetName: file.Filename,
			AssetURL:  goDownloadBaseURL + file.Filename,
			SHA256:    file.SHA256,
		}, nil
	}
	if requested != "" {
		return nil, fmt.Errorf("go version %s not found in download manifest", requested)
	}
	return nil, fmt.Errorf("no stable go archive found for %s/%s", runtime.GOOS, runtime.GOARCH)
}

func fetchGoReleases(ctx context.Context) ([]goRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, goManifestURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("building go download manifest request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching go download manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("go download manifest returned %s", resp.Status)
	}
	var releases []goRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decoding go download manifest: %w", err)
	}
	return releases, nil
}

func goArchiveForTarget(files []goFile, goos, goarch string) (goFile, bool) {
	for _, file := range files {
		if file.Kind != "archive" {
			continue
		}
		if file.OS == goos && file.Arch == goarch {
			return file, true
		}
	}
	return goFile{}, false
}

func bunExecutableFor(goos, _ string) string {
	if strings.EqualFold(goos, "windows") {
		return "bun.exe"
	}
	return "bun"
}

func bunEntrySubpath(goos, goarch string) string {
	assetBase, executable := bunReleasePlatform(goos, goarch)
	if assetBase == "" || executable == "" {
		return ""
	}
	return filepathSlashJoin(assetBase, executable)
}

func bunReleasePlatform(goos, goarch string) (assetBase, executable string) {
	executable = bunExecutableFor(goos, goarch)
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
		switch strings.ToLower(strings.TrimSpace(goarch)) {
		case "arm64":
			return "bun-windows-aarch64", executable
		case "amd64":
			return "bun-windows-x64", executable
		}
	}
	return "", ""
}

func goExecutableFor(goos, _ string) string {
	if strings.EqualFold(goos, "windows") {
		return "go.exe"
	}
	return "go"
}

func goEntrySubpath(goos, goarch string) string {
	return filepathSlashJoin("go", "bin", goExecutableFor(goos, goarch))
}

func filepathSlashJoin(parts ...string) string {
	return strings.Join(parts, "/")
}
