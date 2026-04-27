// Package agentslack embeds the vendored stablyai/agent-slack bundles. The
// CLI bundle exposes agent-slack's full subcommand surface; the dump-
// credentials bundle is a sky10 hydrator that prints loadCredentials()
// output (with macOS Keychain values resolved) to stdout. Both run under
// sky10's managed bun.
//
// To bump the vendored version, run scripts/vendor-agent-slack.sh and
// update Version below.
package agentslack

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Version is the agent-slack release tag currently vendored.
const Version = "v0.8.5"

//go:embed v0.8.5
var assets embed.FS

const (
	cliBundleName  = "agent-slack.js"
	dumpBundleName = "dump-credentials.js"
)

// Bundles is the absolute on-disk location of the vendored bundles after
// materialization.
type Bundles struct {
	CLI  string
	Dump string
	Dir  string
}

// Materialize writes the embedded bundles into rootDir/<Version>/ and
// returns absolute paths to both. Existing files are left in place when
// their content already matches, so repeated calls are cheap.
func Materialize(rootDir string) (Bundles, error) {
	targetDir := filepath.Join(rootDir, Version)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return Bundles{}, fmt.Errorf("agent-slack: create %s: %w", targetDir, err)
	}
	embedRoot := Version
	entries, err := fs.ReadDir(assets, embedRoot)
	if err != nil {
		return Bundles{}, fmt.Errorf("agent-slack: read embedded %s: %w", embedRoot, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err := copyEmbedded(embedRoot, entry.Name(), targetDir); err != nil {
			return Bundles{}, err
		}
	}
	cli := filepath.Join(targetDir, cliBundleName)
	dump := filepath.Join(targetDir, dumpBundleName)
	for _, required := range []string{cli, dump} {
		if _, err := os.Stat(required); err != nil {
			return Bundles{}, fmt.Errorf("agent-slack: missing bundle %s: %w", required, err)
		}
	}
	return Bundles{CLI: cli, Dump: dump, Dir: targetDir}, nil
}

func copyEmbedded(embedDir, name, targetDir string) error {
	src, err := assets.Open(embedDir + "/" + name)
	if err != nil {
		return fmt.Errorf("agent-slack: open embedded %s: %w", name, err)
	}
	defer src.Close()
	wantBytes, err := io.ReadAll(src)
	if err != nil {
		return fmt.Errorf("agent-slack: read embedded %s: %w", name, err)
	}
	target := filepath.Join(targetDir, name)
	if existing, err := os.ReadFile(target); err == nil && sameContent(existing, wantBytes) {
		return nil
	}
	if err := os.WriteFile(target, wantBytes, 0o644); err != nil {
		return fmt.Errorf("agent-slack: write %s: %w", target, err)
	}
	return nil
}

func sameContent(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) < 1024 {
		return string(a) == string(b)
	}
	return sha256.Sum256(a) == sha256.Sum256(b)
}
