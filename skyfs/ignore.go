package skyfs

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// DefaultIgnorePatterns are always ignored regardless of .skyfsignore.
var DefaultIgnorePatterns = []string{
	".git",
	".DS_Store",
	"Thumbs.db",
	"*.swp",
	"*~",
	"*.tmp",
	".skyfsignore",
}

// IgnoreMatcher determines if a path should be ignored during sync.
type IgnoreMatcher struct {
	patterns []ignorePattern
}

type ignorePattern struct {
	pattern string
	negated bool
	dirOnly bool
}

// NewIgnoreMatcher creates a matcher from default patterns plus any
// patterns in the .skyfsignore file at the given root.
func NewIgnoreMatcher(root string) *IgnoreMatcher {
	m := &IgnoreMatcher{}

	// Load defaults
	for _, p := range DefaultIgnorePatterns {
		m.patterns = append(m.patterns, ignorePattern{pattern: p})
	}

	// Load .skyfsignore if it exists
	ignorePath := filepath.Join(root, ".skyfsignore")
	if f, err := os.Open(ignorePath); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			p := ignorePattern{pattern: line}
			if strings.HasPrefix(line, "!") {
				p.negated = true
				p.pattern = line[1:]
			}
			if strings.HasSuffix(p.pattern, "/") {
				p.dirOnly = true
				p.pattern = strings.TrimSuffix(p.pattern, "/")
			}
			m.patterns = append(m.patterns, p)
		}
	}

	return m
}

// Matches returns true if the path should be ignored.
// Path should be relative and use forward slashes.
func (m *IgnoreMatcher) Matches(path string) bool {
	ignored := false
	name := filepath.Base(path)

	for _, p := range m.patterns {
		matched := false

		// Try matching against the full path and the basename
		if matchGlob(p.pattern, path) || matchGlob(p.pattern, name) {
			matched = true
		}

		// Also match against each path component
		if !matched {
			parts := strings.Split(path, "/")
			for _, part := range parts {
				if matchGlob(p.pattern, part) {
					matched = true
					break
				}
			}
		}

		if matched {
			if p.negated {
				ignored = false
			} else {
				ignored = true
			}
		}
	}

	return ignored
}

// IgnoreFunc returns a function suitable for SyncConfig.IgnoreFunc.
func (m *IgnoreMatcher) IgnoreFunc() func(string) bool {
	return m.Matches
}

// matchGlob performs simple glob matching supporting * and ?.
func matchGlob(pattern, name string) bool {
	matched, _ := filepath.Match(pattern, name)
	return matched
}
