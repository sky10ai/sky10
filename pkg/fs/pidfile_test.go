package fs

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sky10/sky10/pkg/config"
)

func TestDaemonSocketPathUsesShortFallbackForDeepRoots(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket path limit does not apply on windows")
	}

	deepRoot := filepath.Join(
		t.TempDir(),
		"very-long-instance-root-name",
		"another-long-segment",
		"yet-another-long-segment",
		"sky10-node-a",
	)
	t.Setenv(config.EnvHome, deepRoot)

	got := DaemonSocketPath()
	if len(got) >= maxUnixSocketPath {
		t.Fatalf("socket path length = %d, want < %d (%s)", len(got), maxUnixSocketPath, got)
	}
	if !strings.HasPrefix(got, shortSocketBaseDir()+string(filepath.Separator)) {
		t.Fatalf("socket path = %q, want prefix %q", got, shortSocketBaseDir()+string(filepath.Separator))
	}
	if !strings.HasSuffix(got, ".sock") {
		t.Fatalf("socket path = %q, want .sock suffix", got)
	}
}
