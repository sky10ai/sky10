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
	oldApply := updateApply
	oldApplyMenu := updateApplyMenu
	oldStopMenu := updateStopMenu
	oldStartMenu := updateStartMenu
	oldRestartDaemon := updateRestartDaemon
	oldVersion := Version
	t.Cleanup(func() {
		updateCheck = oldCheck
		updateApply = oldApply
		updateApplyMenu = oldApplyMenu
		updateStopMenu = oldStopMenu
		updateStartMenu = oldStartMenu
		updateRestartDaemon = oldRestartDaemon
		Version = oldVersion
	})
}

func TestUpdateCmdRestartsMenuAfterDaemonRestart(t *testing.T) {
	withUpdateStubs(t)
	Version = "v0.37.1"

	var order []string
	updateCheck = func(current string) (*skyupdate.Info, error) {
		order = append(order, "check")
		return &skyupdate.Info{
			Current:   current,
			Latest:    "v0.38.0",
			Available: true,
		}, nil
	}
	updateApply = func(info *skyupdate.Info) error {
		order = append(order, "apply-cli")
		return nil
	}
	updateApplyMenu = func(info *skyupdate.Info) (bool, error) {
		order = append(order, "apply-menu")
		return true, nil
	}
	updateStopMenu = func() error {
		order = append(order, "stop-menu")
		return nil
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
		return UpdateCmd().RunE(UpdateCmd(), nil)
	})
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}

	want := []string{"stop-menu", "check", "apply-cli", "apply-menu", "restart-daemon", "start-menu"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for _, needle := range []string{"sky10-menu updated", "daemon restarted", "updated to v0.38.0"} {
		if !strings.Contains(out, needle) {
			t.Fatalf("output missing %q:\n%s", needle, out)
		}
	}
}

func TestUpdateCmdDoesNotStartMenuWhenDaemonRestartFails(t *testing.T) {
	withUpdateStubs(t)
	Version = "v0.37.1"

	var order []string
	updateCheck = func(current string) (*skyupdate.Info, error) {
		order = append(order, "check")
		return &skyupdate.Info{Current: current, Latest: "v0.38.0", Available: true}, nil
	}
	updateApply = func(info *skyupdate.Info) error {
		order = append(order, "apply-cli")
		return nil
	}
	updateApplyMenu = func(info *skyupdate.Info) (bool, error) {
		order = append(order, "apply-menu")
		return false, nil
	}
	updateStopMenu = func() error {
		order = append(order, "stop-menu")
		return nil
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
		return UpdateCmd().RunE(UpdateCmd(), nil)
	})
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}

	want := []string{"stop-menu", "check", "apply-cli", "apply-menu", "restart-daemon"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if strings.Contains(out, "start sky10-menu") {
		t.Fatalf("unexpected start-menu warning in output:\n%s", out)
	}
	if !strings.Contains(out, "restart the daemon manually to use the new version") {
		t.Fatalf("expected daemon restart warning in output:\n%s", out)
	}
}

func TestUpdateCmdRestartsMenuWhenOnlyMenuChanges(t *testing.T) {
	withUpdateStubs(t)
	Version = "v0.38.0"

	var order []string
	updateCheck = func(current string) (*skyupdate.Info, error) {
		order = append(order, "check")
		return &skyupdate.Info{Current: current, Latest: current, Available: false}, nil
	}
	updateApplyMenu = func(info *skyupdate.Info) (bool, error) {
		order = append(order, "apply-menu")
		return true, nil
	}
	updateStopMenu = func() error {
		order = append(order, "stop-menu")
		return nil
	}
	updateStartMenu = func() error {
		order = append(order, "start-menu")
		return nil
	}
	updateRestartDaemon = func() error {
		order = append(order, "restart-daemon")
		return nil
	}

	out, err := captureStdout(t, func() error {
		return UpdateCmd().RunE(UpdateCmd(), nil)
	})
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}

	want := []string{"stop-menu", "check", "apply-menu", "start-menu"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if !strings.Contains(out, "sky10-menu updated") {
		t.Fatalf("expected menu update output:\n%s", out)
	}
}

func TestUpdateCmdRestartsMenuWhenAlreadyUpToDate(t *testing.T) {
	withUpdateStubs(t)
	Version = "v0.38.1"

	var order []string
	updateCheck = func(current string) (*skyupdate.Info, error) {
		order = append(order, "check")
		return &skyupdate.Info{Current: current, Latest: current, Available: false}, nil
	}
	updateApplyMenu = func(info *skyupdate.Info) (bool, error) {
		order = append(order, "apply-menu")
		return false, nil
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
		return UpdateCmd().RunE(UpdateCmd(), nil)
	})
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}

	want := []string{"stop-menu", "check", "apply-menu", "start-menu"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if !strings.Contains(out, "already up to date") {
		t.Fatalf("expected already up to date output:\n%s", out)
	}
}

func TestUpdateCmdCheckOnlyDoesNotTouchMenu(t *testing.T) {
	withUpdateStubs(t)
	Version = "v0.38.0"

	var order []string
	updateCheck = func(current string) (*skyupdate.Info, error) {
		order = append(order, "check")
		return &skyupdate.Info{Current: current, Latest: "v0.38.1", Available: true}, nil
	}
	updateStopMenu = func() error {
		order = append(order, "stop-menu")
		return nil
	}
	updateStartMenu = func() error {
		order = append(order, "start-menu")
		return nil
	}
	cmd := UpdateCmd()
	if err := cmd.Flags().Set("check", "true"); err != nil {
		t.Fatalf("Flags().Set: %v", err)
	}

	out, err := captureStdout(t, func() error {
		return cmd.RunE(cmd, nil)
	})
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}

	want := []string{"check"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if !strings.Contains(out, "update available: v0.38.0 -> v0.38.1") {
		t.Fatalf("expected update available output:\n%s", out)
	}
}

func TestUpdateCmdRestartsMenuIfUpdateCheckFails(t *testing.T) {
	withUpdateStubs(t)
	Version = "v0.38.0"

	var order []string
	updateStopMenu = func() error {
		order = append(order, "stop-menu")
		return nil
	}
	updateCheck = func(current string) (*skyupdate.Info, error) {
		order = append(order, "check")
		return nil, fmt.Errorf("boom")
	}
	updateStartMenu = func() error {
		order = append(order, "start-menu")
		return nil
	}

	_, err := captureStdout(t, func() error {
		return UpdateCmd().RunE(UpdateCmd(), nil)
	})
	if err == nil {
		t.Fatal("expected RunE error")
	}

	want := []string{"stop-menu", "check", "start-menu"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}
