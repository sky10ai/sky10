package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const defaultSharedFileMode = "0644"

type SharedFile struct {
	Path    string `json:"path"`
	Mode    string `json:"mode,omitempty"`
	Content string `json:"content"`
}

func normalizeSharedFiles(files []SharedFile) ([]SharedFile, error) {
	normalized := make([]SharedFile, 0, len(files))
	seen := map[string]struct{}{}
	for _, file := range files {
		path, err := normalizeSharedFilePath(file.Path)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[path]; exists {
			return nil, fmt.Errorf("duplicate shared file path %q", path)
		}
		seen[path] = struct{}{}
		mode, err := normalizeSharedFileMode(file.Mode)
		if err != nil {
			return nil, fmt.Errorf("shared file %q: %w", path, err)
		}
		normalized = append(normalized, SharedFile{
			Path:    path,
			Mode:    mode,
			Content: file.Content,
		})
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		return normalized[i].Path < normalized[j].Path
	})
	return normalized, nil
}

func writeSharedFiles(sharedDir string, files []SharedFile) error {
	if len(files) == 0 {
		return nil
	}
	normalized, err := normalizeSharedFiles(files)
	if err != nil {
		return err
	}
	root := filepath.Clean(sharedDir)
	for _, file := range normalized {
		target := filepath.Join(root, filepath.FromSlash(file.Path))
		if !pathWithin(root, target) {
			return fmt.Errorf("shared file path %q escapes shared directory", file.Path)
		}
		mode, err := parseSharedFileMode(file.Mode)
		if err != nil {
			return fmt.Errorf("shared file %q: %w", file.Path, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("creating shared file directory %q: %w", file.Path, err)
		}
		if err := os.WriteFile(target, []byte(file.Content), mode); err != nil {
			return fmt.Errorf("writing shared file %q: %w", file.Path, err)
		}
	}
	return nil
}

func normalizeSharedFilePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("shared file path is required")
	}
	clean := filepath.Clean(filepath.FromSlash(value))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("shared file path %q must be relative and stay inside the shared directory", value)
	}
	return filepath.ToSlash(clean), nil
}

func normalizeSharedFileMode(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultSharedFileMode, nil
	}
	mode, err := parseSharedFileMode(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%04o", mode.Perm()), nil
}

func parseSharedFileMode(value string) (os.FileMode, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = defaultSharedFileMode
	}
	raw, err := strconv.ParseUint(value, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("mode must be an octal file mode: %w", err)
	}
	mode := os.FileMode(raw)
	if mode&^0o777 != 0 {
		return 0, fmt.Errorf("mode must only include permission bits")
	}
	if mode == 0 {
		return 0, fmt.Errorf("mode must not be 0000")
	}
	return mode, nil
}

func pathWithin(root, target string) bool {
	rel, err := filepath.Rel(root, filepath.Clean(target))
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
