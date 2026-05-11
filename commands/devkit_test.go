package commands

import (
	"strings"
	"testing"

	skydevkit "github.com/sky10/sky10/pkg/devkit"
)

func withDevkitStubs(t *testing.T) {
	t.Helper()
	oldList := managedDevkitList
	oldLookup := managedDevkitLookup
	oldStatus := managedDevkitStatus
	oldCheck := managedDevkitCheckLatest
	oldUpgrade := managedDevkitUpgrade
	oldUninstall := managedDevkitUninstall
	t.Cleanup(func() {
		managedDevkitList = oldList
		managedDevkitLookup = oldLookup
		managedDevkitStatus = oldStatus
		managedDevkitCheckLatest = oldCheck
		managedDevkitUpgrade = oldUpgrade
		managedDevkitUninstall = oldUninstall
	})
}

func TestDevkitInstallPassesRequestedVersion(t *testing.T) {
	withDevkitStubs(t)

	managedDevkitLookup = func(id string) (*skydevkit.ToolInfo, error) {
		return &skydevkit.ToolInfo{ID: skydevkit.ToolGo, Name: "Go SDK"}, nil
	}
	var gotVersion string
	managedDevkitUpgrade = func(id skydevkit.ID, version string, _ skydevkit.ProgressFunc) (*skydevkit.ReleaseInfo, error) {
		gotVersion = version
		return &skydevkit.ReleaseInfo{
			ID:        id,
			Current:   "",
			Latest:    "go1.26.3",
			Available: true,
		}, nil
	}

	out, err := captureStdout(t, func() error {
		cmd := DevkitCmd()
		cmd.SetArgs([]string{"install", "go", "--version", "1.26.3"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotVersion != "1.26.3" {
		t.Fatalf("requested version = %q, want 1.26.3", gotVersion)
	}
	if !strings.Contains(out, "installed go go1.26.3") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestDevkitUpgradeReportsAlreadyCurrent(t *testing.T) {
	withDevkitStubs(t)

	managedDevkitLookup = func(id string) (*skydevkit.ToolInfo, error) {
		return &skydevkit.ToolInfo{ID: skydevkit.ToolBun, Name: "Bun"}, nil
	}
	managedDevkitUpgrade = func(id skydevkit.ID, _ string, _ skydevkit.ProgressFunc) (*skydevkit.ReleaseInfo, error) {
		return &skydevkit.ReleaseInfo{
			ID:        id,
			Current:   "v1.2.3",
			Latest:    "v1.2.3",
			Available: false,
		}, nil
	}

	out, err := captureStdout(t, func() error {
		cmd := DevkitCmd()
		cmd.SetArgs([]string{"upgrade", "bun"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "bun already up to date (v1.2.3)") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}
