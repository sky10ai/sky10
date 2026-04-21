package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestManagedAdapterRestartsAfterCrash(t *testing.T) {
	t.Parallel()

	countFile := filepath.Join(t.TempDir(), "launch-count.txt")
	adapter, err := StartManagedAdapter(context.Background(), ManagedAdapterSpec{
		Key: "helper",
		Process: ProcessSpec{
			Path: helperProcessExecutableForTests(),
			Args: []string{"-test.run=TestHelperMessagingAdapterProcess", "--"},
			Env: []string{
				"GO_WANT_HELPER_MESSAGING_ADAPTER=1",
				"SKY10_MESSAGING_HELPER_MODE=exit-once-then-serve",
				"SKY10_MESSAGING_HELPER_COUNT_FILE=" + countFile,
			},
		},
		HealthCheckInterval: 25 * time.Millisecond,
		RestartBackoffBase:  10 * time.Millisecond,
		RestartBackoffMax:   20 * time.Millisecond,
		StartupTimeout:      2 * time.Second,
	})
	if err != nil {
		t.Fatalf("StartManagedAdapter() error = %v", err)
	}
	defer func() {
		if err := adapter.Close(); err != nil {
			t.Fatalf("adapter.Close() error = %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := adapter.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady() error = %v", err)
	}

	state := adapter.Snapshot()
	if state.LaunchCount < 2 {
		t.Fatalf("launch count = %d, want at least 2 after crash restart", state.LaunchCount)
	}
	if state.RestartCount < 1 {
		t.Fatalf("restart count = %d, want at least 1", state.RestartCount)
	}
	if !state.Running {
		t.Fatal("adapter not running after restart")
	}
}

func TestManagedAdapterHealthPolling(t *testing.T) {
	t.Parallel()

	adapter, err := StartManagedAdapter(context.Background(), ManagedAdapterSpec{
		Key: "health",
		Process: ProcessSpec{
			Path: helperProcessExecutableForTests(),
			Args: []string{"-test.run=TestHelperMessagingAdapterProcess", "--"},
			Env:  []string{"GO_WANT_HELPER_MESSAGING_ADAPTER=1"},
		},
		HealthCheckInterval: 20 * time.Millisecond,
		StartupTimeout:      2 * time.Second,
	})
	if err != nil {
		t.Fatalf("StartManagedAdapter() error = %v", err)
	}
	defer func() {
		if err := adapter.Close(); err != nil {
			t.Fatalf("adapter.Close() error = %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := adapter.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		state := adapter.Snapshot()
		if state.LastHealth != nil && state.LastHealth.OK {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("health was not observed: %+v", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestManagerAddGetListAndRemove(t *testing.T) {
	t.Parallel()

	manager := NewManager(context.Background())
	defer func() {
		if err := manager.Close(); err != nil {
			t.Fatalf("manager.Close() error = %v", err)
		}
	}()

	adapter, err := manager.Add(ManagedAdapterSpec{
		Key: "helper",
		Process: ProcessSpec{
			Path: helperProcessExecutableForTests(),
			Args: []string{"-test.run=TestHelperMessagingAdapterProcess", "--"},
			Env:  []string{"GO_WANT_HELPER_MESSAGING_ADAPTER=1"},
		},
		StartupTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("manager.Add() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := adapter.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady() error = %v", err)
	}

	got, ok := manager.Get("helper")
	if !ok || got == nil {
		t.Fatal("manager.Get(helper) = missing, want adapter")
	}
	list := manager.List()
	if len(list) != 1 || list[0].Key != "helper" {
		t.Fatalf("manager.List() = %+v, want helper entry", list)
	}
	if err := manager.Remove("helper"); err != nil {
		t.Fatalf("manager.Remove() error = %v", err)
	}
	if _, ok := manager.Get("helper"); ok {
		t.Fatal("manager.Get(helper) still present after remove")
	}
}

func TestManagedAdapterCurrentUnavailable(t *testing.T) {
	t.Parallel()

	adapter, err := StartManagedAdapter(context.Background(), ManagedAdapterSpec{
		Key: "broken",
		Process: ProcessSpec{
			Path: helperProcessExecutableForTests(),
			Args: []string{"-test.run=TestHelperMessagingAdapterProcess", "--"},
			Env:  []string{"GO_WANT_HELPER_MESSAGING_ADAPTER=1", "SKY10_MESSAGING_HELPER_MODE=exit-immediately"},
		},
		RestartBackoffBase: 100 * time.Millisecond,
		RestartBackoffMax:  100 * time.Millisecond,
		StartupTimeout:     100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("StartManagedAdapter() error = %v", err)
	}
	defer func() {
		_ = adapter.Close()
	}()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := adapter.Current(); err != nil {
			if errors.Is(err, ErrAdapterUnavailable) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Current() never reported adapter unavailable")
}

func TestRestartBackoff(t *testing.T) {
	t.Parallel()

	got := restartBackoff(10*time.Millisecond, 80*time.Millisecond, 4)
	if got != 80*time.Millisecond {
		t.Fatalf("restartBackoff(..., failures=4) = %v, want 80ms", got)
	}
}
