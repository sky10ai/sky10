package fs

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

var windowsReservedNames = map[string]bool{
	"CON":  true,
	"PRN":  true,
	"AUX":  true,
	"NUL":  true,
	"COM1": true,
	"COM2": true,
	"COM3": true,
	"COM4": true,
	"COM5": true,
	"COM6": true,
	"COM7": true,
	"COM8": true,
	"COM9": true,
	"LPT1": true,
	"LPT2": true,
	"LPT3": true,
	"LPT4": true,
	"LPT5": true,
	"LPT6": true,
	"LPT7": true,
	"LPT8": true,
	"LPT9": true,
}

// NormalizeLogicalPath converts user-facing FS paths into the canonical
// slash-separated logical form used by metadata and RPC payloads.
func NormalizeLogicalPath(raw string) (string, error) {
	return normalizeLogicalPath(raw, true)
}

func canonicalLogicalPath(raw string) (string, error) {
	return normalizeLogicalPath(raw, false)
}

func normalizeLogicalPath(raw string, convertBackslashes bool) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("path is required")
	}

	path := raw
	if convertBackslashes {
		path = strings.ReplaceAll(path, "\\", "/")
	} else if strings.Contains(path, `\`) {
		return "", fmt.Errorf("path %q is not canonical", raw)
	}

	if path == "." {
		return "", fmt.Errorf("path must not be root")
	}
	if strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("path %q must be relative", raw)
	}
	if len(path) >= 2 && path[1] == ':' && isASCIIAlpha(path[0]) {
		return "", fmt.Errorf("path %q must not include a drive prefix", raw)
	}

	parts := strings.Split(path, "/")
	for _, part := range parts {
		switch part {
		case "":
			return "", fmt.Errorf("path %q contains an empty segment", raw)
		case ".", "..":
			return "", fmt.Errorf("path %q contains invalid segment %q", raw, part)
		}
	}

	return strings.Join(parts, "/"), nil
}

// LocalPathToLogical converts an on-disk path beneath root into the canonical
// slash-separated logical form used by the FS engine.
func LocalPathToLogical(root, local string) (string, error) {
	rel, err := filepath.Rel(root, local)
	if err != nil {
		return "", fmt.Errorf("relative path: %w", err)
	}
	return canonicalLogicalPath(filepath.ToSlash(rel))
}

// LogicalPathToLocal maps a canonical logical path onto the local filesystem
// beneath root.
func LogicalPathToLocal(root, logical string) (string, error) {
	canonical, err := canonicalLogicalPath(logical)
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		if err := ValidateWindowsLogicalPath(canonical); err != nil {
			return "", err
		}
	}
	return filepath.Join(root, filepath.FromSlash(canonical)), nil
}

// ValidateWindowsLogicalPath reports whether a logical path can be safely
// materialized on Windows without relying on implicit OS rewrites.
func ValidateWindowsLogicalPath(logical string) error {
	canonical, err := NormalizeLogicalPath(logical)
	if err != nil {
		return err
	}
	for _, segment := range strings.Split(canonical, "/") {
		if err := validateWindowsPathSegment(segment); err != nil {
			return fmt.Errorf("windows path %q is invalid: %w", canonical, err)
		}
	}
	return nil
}

func validateWindowsPathSegment(segment string) error {
	if segment == "" {
		return fmt.Errorf("empty segment")
	}
	if strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") {
		return fmt.Errorf("segment %q has forbidden trailing dot/space", segment)
	}
	if strings.ContainsRune(segment, ':') {
		return fmt.Errorf("segment %q contains ':'", segment)
	}

	for _, r := range segment {
		if r < 32 {
			return fmt.Errorf("segment %q contains control characters", segment)
		}
		switch r {
		case '<', '>', '"', '|', '?', '*':
			return fmt.Errorf("segment %q contains forbidden character %q", segment, r)
		}
	}

	trimmed := strings.TrimRight(segment, " .")
	base := trimmed
	if idx := strings.IndexByte(base, '.'); idx >= 0 {
		base = base[:idx]
	}
	if windowsReservedNames[strings.ToUpper(base)] {
		return fmt.Errorf("segment %q uses reserved name %q", segment, base)
	}

	return nil
}

func isASCIIAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
