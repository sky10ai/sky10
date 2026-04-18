package kv

import "strings"

const (
	internalSlashPrefix = "_sys/"
)

// IsInternalKey reports whether a key uses a system-managed prefix.
// Internal keys are hidden from generic KV browsing by default, but this is
// only a UX boundary.
func IsInternalKey(key string) bool {
	return strings.HasPrefix(key, internalSlashPrefix)
}

func filterVisibleKeys(keys []string, includeInternal bool) []string {
	if includeInternal {
		return keys
	}
	filtered := make([]string, 0, len(keys))
	for _, key := range keys {
		if IsInternalKey(key) {
			continue
		}
		filtered = append(filtered, key)
	}
	return filtered
}

func filterVisibleEntries(entries map[string][]byte, includeInternal bool) map[string][]byte {
	if includeInternal {
		return entries
	}
	filtered := make(map[string][]byte, len(entries))
	for key, value := range entries {
		if IsInternalKey(key) {
			continue
		}
		filtered[key] = value
	}
	return filtered
}

func visibleKeyCount(snap *Snapshot) int {
	if snap == nil {
		return 0
	}
	count := 0
	for key := range snap.entries {
		if IsInternalKey(key) {
			continue
		}
		count++
	}
	return count
}
