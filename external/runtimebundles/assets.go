package runtimebundles

import (
	"context"
	"embed"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	RemoteBase               = "https://raw.githubusercontent.com/sky10ai/sky10/main/external/runtimebundles/"
	OpenClawDir              = "openclaw"
	OpenClawSky10PluginDir   = OpenClawDir + "/sky10-openclaw"
	OpenClawDockerDir        = OpenClawDir + "/docker"
	repoRuntimeBundlesSubdir = "external/runtimebundles"
)

//go:embed openclaw
var assets embed.FS

func IsAsset(name string) bool {
	clean, err := cleanAssetName(name)
	return err == nil && strings.HasPrefix(clean, OpenClawDir+"/")
}

func ReadAsset(name string) ([]byte, error) {
	clean, err := cleanAssetName(name)
	if err != nil {
		return nil, err
	}
	body, err := assets.ReadFile(clean)
	if err != nil {
		return nil, fmt.Errorf("reading bundled runtime bundle asset %q: %w", clean, err)
	}
	return body, nil
}

func LoadAsset(ctx context.Context, name string) ([]byte, error) {
	clean, err := cleanAssetName(name)
	if err != nil {
		return nil, err
	}
	if local, err := FindLocalAsset(clean); err == nil {
		body, err := os.ReadFile(local)
		if err != nil {
			return nil, fmt.Errorf("reading local runtime bundle asset %q: %w", clean, err)
		}
		return body, nil
	}
	if body, err := assets.ReadFile(clean); err == nil {
		return body, nil
	}
	return downloadAsset(ctx, clean)
}

func FindLocalAsset(name string) (string, error) {
	clean, err := cleanAssetName(name)
	if err != nil {
		return "", err
	}

	candidates := make([]string, 0, 2)
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}

	for _, start := range candidates {
		for _, dir := range walkUp(start) {
			assetPath := filepath.Join(dir, filepath.FromSlash(repoRuntimeBundlesSubdir), filepath.FromSlash(clean))
			if info, err := os.Stat(assetPath); err == nil && !info.IsDir() {
				return assetPath, nil
			}
		}
	}
	return "", fmt.Errorf("runtime bundle asset %q not found locally", clean)
}

func cleanAssetName(name string) (string, error) {
	raw := strings.TrimSpace(name)
	if raw == "" || path.IsAbs(raw) || strings.Contains(raw, "\\") {
		return "", fmt.Errorf("invalid runtime bundle asset name %q", name)
	}
	for _, part := range strings.Split(raw, "/") {
		if part == ".." {
			return "", fmt.Errorf("invalid runtime bundle asset name %q", name)
		}
	}
	clean := path.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("invalid runtime bundle asset name %q", name)
	}
	return clean, nil
}

func walkUp(start string) []string {
	start = filepath.Clean(start)
	var dirs []string
	for {
		dirs = append(dirs, start)
		parent := filepath.Dir(start)
		if parent == start {
			break
		}
		start = parent
	}
	return dirs
}

func downloadAsset(ctx context.Context, name string) ([]byte, error) {
	url := RemoteBase + name
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("building runtime bundle asset request for %q: %w", name, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading runtime bundle asset %q: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading runtime bundle asset %q: unexpected HTTP %d", name, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading runtime bundle asset %q: %w", name, err)
	}
	return body, nil
}
