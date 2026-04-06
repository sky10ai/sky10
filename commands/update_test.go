package commands

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	skyupdate "github.com/sky10/sky10/pkg/update"
)

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	runErr := fn()
	w.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy stdout: %v", err)
	}
	r.Close()
	return buf.String(), runErr
}

func withUpdateStubs(t *testing.T) {
	t.Helper()
	oldCheck := updateCheck
	oldDownload := updateDownload
	oldStatus := updateStatus
	oldInstall := updateInstall
	oldStopMenu := updateStopMenu
	oldStartMenu := updateStartMenu
	oldRestartDaemon := updateRestartDaemon
	oldWaitHTTPReady := updateWaitHTTPReady
	oldVersion := Version
	t.Cleanup(func() {
		updateCheck = oldCheck
		updateDownload = oldDownload
		updateStatus = oldStatus
		updateInstall = oldInstall
		updateStopMenu = oldStopMenu
		updateStartMenu = oldStartMenu
		updateRestartDaemon = oldRestartDaemon
		updateWaitHTTPReady = oldWaitHTTPReady
		Version = oldVersion
	})
}

func TestUpdateCmdDownloadsAndInstallsBeforeRestartingDaemon(t *testing.T) {
	withUpdateStubs(t)
	Version = "v0.37.1"

	var order []string
	ready := false

	updateStatus = func(current string) (*skyupdate.StagedStatus, error) {
		order = append(order, "status")
		return &skyupdate.StagedStatus{
			Current:    current,
			Ready:      ready,
			Latest:     "v0.38.0",
			CLIStaged:  ready,
			MenuStaged: ready,
		}, nil
	}
	updateCheck = func(current string) (*skyupdate.Info, error) {
		order = append(order, "check")
		return &skyupdate.Info{
			Current:       current,
			Latest:        "v0.38.0",
			Available:     true,
			CLIAvailable:  true,
			MenuAvailable: true,
		}, nil
	}
	updateDownload = func(info *skyupdate.Info) (*skyupdate.StagedRelease, error) {
		order = append(order, "download")
		ready = true
		return &skyupdate.StagedRelease{
			Current:    info.Current,
			Latest:     info.Latest,
			CLIStaged:  true,
			MenuStaged: true,
		}, nil
	}
	updateStopMenu = func() error {
		order = append(order, "stop-menu")
		return nil
	}
	updateInstall = func() (*skyupdate.StagedRelease, error) {
		order = append(order, "install")
		return &skyupdate.StagedRelease{
			Current:    "v0.37.1",
			Latest:     "v0.38.0",
			CLIStaged:  true,
			MenuStaged: true,
		}, nil
	}
	updateRestartDaemon = func() error {
		order = append(order, "restart-daemon")
		return nil
	}
	updateWaitHTTPReady = func() error {
		order = append(order, "wait-http")
		return nil
	}
	updateStartMenu = func() error {
		order = append(order, "start-menu")
		return nil
	}

	out, err := captureStdout(t, func() error {
		cmd := UpdateCmd()
		return cmd.RunE(cmd, nil)
	})
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}

	want := []string{"status", "check", "download", "status", "stop-menu", "install", "restart-daemon", "wait-http", "start-menu"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for _, needle := range []string{"downloading...", "staged v0.38.0 (cli and menu)", "sky10-menu updated", "daemon restarted", "updated to v0.38.0"} {
		if !strings.Contains(out, needle) {
			t.Fatalf("output missing %q:\n%s", needle, out)
		}
	}
}

func TestUpdateInstallMenuOnlyDoesNotRestartDaemon(t *testing.T) {
	withUpdateStubs(t)
	Version = "v0.38.0"

	var order []string
	updateStatus = func(current string) (*skyupdate.StagedStatus, error) {
		order = append(order, "status")
		return &skyupdate.StagedStatus{
			Current:    current,
			Ready:      true,
			Latest:     "v0.38.0",
			MenuStaged: true,
		}, nil
	}
	updateStopMenu = func() error {
		order = append(order, "stop-menu")
		return nil
	}
	updateInstall = func() (*skyupdate.StagedRelease, error) {
		order = append(order, "install")
		return &skyupdate.StagedRelease{
			Current:    "v0.38.0",
			Latest:     "v0.38.0",
			MenuStaged: true,
		}, nil
	}
	updateRestartDaemon = func() error {
		order = append(order, "restart-daemon")
		return nil
	}
	updateStartMenu = func() error {
		order = append(order, "start-menu")
		return nil
	}

	out, err := captureStdout(t, func() error {
		return updateInstallCmd().RunE(updateInstallCmd(), nil)
	})
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}

	want := []string{"status", "stop-menu", "install", "start-menu"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if strings.Contains(out, "daemon restarted") {
		t.Fatalf("unexpected daemon restart output:\n%s", out)
	}
	if !strings.Contains(out, "sky10-menu updated") {
		t.Fatalf("expected menu update output:\n%s", out)
	}
}

func TestUpdateCheckSubcommandDoesNotTouchMenu(t *testing.T) {
	withUpdateStubs(t)
	Version = "v0.38.0"

	var order []string
	updateCheck = func(current string) (*skyupdate.Info, error) {
		order = append(order, "check")
		return &skyupdate.Info{
			Current:      current,
			Latest:       "v0.38.1",
			Available:    true,
			CLIAvailable: true,
		}, nil
	}
	updateStopMenu = func() error {
		order = append(order, "stop-menu")
		return nil
	}
	updateStartMenu = func() error {
		order = append(order, "start-menu")
		return nil
	}

	out, err := captureStdout(t, func() error {
		return updateCheckCmd().RunE(updateCheckCmd(), nil)
	})
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}

	want := []string{"check"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if !strings.Contains(out, "update available: v0.38.0 -> v0.38.1") {
		t.Fatalf("expected update output:\n%s", out)
	}
}

func TestUpdateDownloadSubcommandDoesNotTouchMenu(t *testing.T) {
	withUpdateStubs(t)
	Version = "v0.38.0"

	var order []string
	updateCheck = func(current string) (*skyupdate.Info, error) {
		order = append(order, "check")
		return &skyupdate.Info{
			Current:       current,
			Latest:        "v0.38.1",
			Available:     true,
			MenuAvailable: true,
		}, nil
	}
	updateDownload = func(info *skyupdate.Info) (*skyupdate.StagedRelease, error) {
		order = append(order, "download")
		return &skyupdate.StagedRelease{
			Current:    info.Current,
			Latest:     info.Latest,
			MenuStaged: true,
		}, nil
	}
	updateStopMenu = func() error {
		order = append(order, "stop-menu")
		return nil
	}

	out, err := captureStdout(t, func() error {
		return updateDownloadCmd().RunE(updateDownloadCmd(), nil)
	})
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}

	want := []string{"check", "download"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if !strings.Contains(out, "staged v0.38.1 (menu)") {
		t.Fatalf("expected staged output:\n%s", out)
	}
}

func TestUpdateInstallDoesNotStartMenuWhenDaemonRestartFails(t *testing.T) {
	withUpdateStubs(t)
	Version = "v0.37.1"

	var order []string
	updateStatus = func(current string) (*skyupdate.StagedStatus, error) {
		order = append(order, "status")
		return &skyupdate.StagedStatus{
			Current:    current,
			Ready:      true,
			Latest:     "v0.38.0",
			CLIStaged:  true,
			MenuStaged: true,
		}, nil
	}
	updateStopMenu = func() error {
		order = append(order, "stop-menu")
		return nil
	}
	updateInstall = func() (*skyupdate.StagedRelease, error) {
		order = append(order, "install")
		return &skyupdate.StagedRelease{
			Current:    "v0.37.1",
			Latest:     "v0.38.0",
			CLIStaged:  true,
			MenuStaged: true,
		}, nil
	}
	updateRestartDaemon = func() error {
		order = append(order, "restart-daemon")
		return fmt.Errorf("boom")
	}
	updateStartMenu = func() error {
		order = append(order, "start-menu")
		return nil
	}

	out, err := captureStdout(t, func() error {
		return updateInstallCmd().RunE(updateInstallCmd(), nil)
	})
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}

	want := []string{"status", "stop-menu", "install", "restart-daemon"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if strings.Contains(out, "could not start sky10-menu") {
		t.Fatalf("unexpected start-menu warning in output:\n%s", out)
	}
	if !strings.Contains(out, "restart the daemon manually to use the new version") {
		t.Fatalf("expected daemon restart warning:\n%s", out)
	}
}

func TestUpdateStatusJSON(t *testing.T) {
	withUpdateStubs(t)
	Version = "v0.38.0"

	updateStatus = func(current string) (*skyupdate.StagedStatus, error) {
		return &skyupdate.StagedStatus{
			Current:    current,
			Ready:      true,
			Latest:     "v0.38.1",
			MenuStaged: true,
		}, nil
	}

	cmd := updateStatusCmd()
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatalf("Flags().Set: %v", err)
	}

	out, err := captureStdout(t, func() error {
		return cmd.RunE(cmd, nil)
	})
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(out, `"ready": true`) || !strings.Contains(out, `"latest": "v0.38.1"`) {
		t.Fatalf("expected JSON status output:\n%s", out)
	}
}
