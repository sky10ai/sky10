package fs

// SyncConfig configures a sync drive.
type SyncConfig struct {
	LocalRoot        string
	PollInterval     int      // seconds, 0 = no polling (manual sync)
	Namespaces       []string // empty = sync all
	Prefixes         []string // empty = sync all
	ExcludePrefixes  []string
	ConflictStrategy string // "lww" (default), "keep-both"
	IgnoreFunc       func(string) bool
}
