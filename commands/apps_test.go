package commands

import (
	"strings"
	"testing"

	skyapps "github.com/sky10/sky10/pkg/apps"
)

func withAppsStubs(t *testing.T) {
	t.Helper()
	oldList := managedAppsList
	oldLookup := managedAppsLookup
	oldStatus := managedAppsStatus
	oldCheck := managedAppsCheckLatest
	oldUpgrade := managedAppsUpgrade
	oldUninstall := managedAppsUninstall
	t.Cleanup(func() {
		managedAppsList = oldList
		managedAppsLookup = oldLookup
		managedAppsStatus = oldStatus
		managedAppsCheckLatest = oldCheck
		managedAppsUpgrade = oldUpgrade
		managedAppsUninstall = oldUninstall
	})
}

func TestAppsUpgradeAliasUsesUpdateName(t *testing.T) {
	withAppsStubs(t)

	managedAppsLookup = func(id string) (*skyapps.AppInfo, error) {
		return &skyapps.AppInfo{ID: skyapps.AppOWS, Name: "Open Wallet Standard"}, nil
	}
	managedAppsUpgrade = func(id skyapps.ID, _ skyapps.ProgressFunc) (*skyapps.ReleaseInfo, error) {
		return &skyapps.ReleaseInfo{
			ID:        id,
			Current:   "v0.4.0",
			Latest:    "v0.5.0",
			Available: true,
		}, nil
	}

	out, err := captureStdout(t, func() error {
		cmd := AppsCmd()
		cmd.SetArgs([]string{"update", "ows"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "upgraded ows to v0.5.0") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestAppsInstallPrintsInstalledMessage(t *testing.T) {
	withAppsStubs(t)

	managedAppsLookup = func(id string) (*skyapps.AppInfo, error) {
		return &skyapps.AppInfo{ID: skyapps.AppOWS, Name: "Open Wallet Standard"}, nil
	}
	managedAppsUpgrade = func(id skyapps.ID, _ skyapps.ProgressFunc) (*skyapps.ReleaseInfo, error) {
		return &skyapps.ReleaseInfo{
			ID:        id,
			Current:   "",
			Latest:    "v0.5.0",
			Available: true,
		}, nil
	}

	out, err := captureStdout(t, func() error {
		cmd := AppsCmd()
		cmd.SetArgs([]string{"install", "ows"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "installed ows v0.5.0") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestAppsUpgradeReportsAlreadyCurrent(t *testing.T) {
	withAppsStubs(t)

	managedAppsLookup = func(id string) (*skyapps.AppInfo, error) {
		return &skyapps.AppInfo{ID: skyapps.AppOWS, Name: "Open Wallet Standard"}, nil
	}
	managedAppsUpgrade = func(id skyapps.ID, _ skyapps.ProgressFunc) (*skyapps.ReleaseInfo, error) {
		return &skyapps.ReleaseInfo{
			ID:        id,
			Current:   "v0.5.0",
			Latest:    "v0.5.0",
			Available: false,
		}, nil
	}

	out, err := captureStdout(t, func() error {
		cmd := AppsCmd()
		cmd.SetArgs([]string{"upgrade", "ows"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "ows already up to date (v0.5.0)") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}
