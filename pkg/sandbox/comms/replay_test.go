package comms

import (
	"errors"
	"testing"
	"time"
)

func TestReplayStoreAcceptsFreshNonce(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	r := NewReplayStore(2*time.Minute, 10*time.Minute, func() time.Time { return now })
	if err := r.Check("agent-A", "test.echo", "n1", now, time.Minute); err != nil {
		t.Fatalf("first Check err = %v, want nil", err)
	}
}

func TestReplayStoreRejectsRepeat(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	clock := now
	r := NewReplayStore(2*time.Minute, 10*time.Minute, func() time.Time { return clock })

	if err := r.Check("agent-A", "test.echo", "n1", clock, time.Minute); err != nil {
		t.Fatalf("first Check err = %v", err)
	}
	if err := r.Check("agent-A", "test.echo", "n1", clock, time.Minute); !errors.Is(err, ErrReplay) {
		t.Fatalf("repeat Check err = %v, want ErrReplay", err)
	}
}

func TestReplayStoreAllowsAfterWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	clock := now
	r := NewReplayStore(2*time.Minute, 10*time.Minute, func() time.Time { return clock })

	if err := r.Check("agent-A", "test.echo", "n1", clock, time.Minute); err != nil {
		t.Fatalf("first Check err = %v", err)
	}
	clock = clock.Add(time.Minute + time.Second)
	if err := r.Check("agent-A", "test.echo", "n1", clock, time.Minute); err != nil {
		t.Fatalf("post-window Check err = %v, want nil", err)
	}
}

func TestReplayStoreScopesByAgentAndType(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	r := NewReplayStore(2*time.Minute, 10*time.Minute, func() time.Time { return now })
	if err := r.Check("agent-A", "test.echo", "n1", now, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := r.Check("agent-B", "test.echo", "n1", now, time.Minute); err != nil {
		t.Fatalf("different agent same nonce err = %v, want nil", err)
	}
	if err := r.Check("agent-A", "test.other", "n1", now, time.Minute); err != nil {
		t.Fatalf("different type same nonce err = %v, want nil", err)
	}
}

func TestReplayStoreRejectsTimestampOutOfRange(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	r := NewReplayStore(2*time.Minute, 10*time.Minute, func() time.Time { return now })

	tooOld := now.Add(-3 * time.Minute)
	if err := r.Check("agent-A", "test.echo", "n1", tooOld, time.Minute); !errors.Is(err, ErrTimestampOutOfRange) {
		t.Fatalf("too-old ts err = %v, want ErrTimestampOutOfRange", err)
	}
	tooNew := now.Add(3 * time.Minute)
	if err := r.Check("agent-A", "test.echo", "n2", tooNew, time.Minute); !errors.Is(err, ErrTimestampOutOfRange) {
		t.Fatalf("too-new ts err = %v, want ErrTimestampOutOfRange", err)
	}
}

func TestReplayStoreSweepsOldEntries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	clock := now
	r := NewReplayStore(2*time.Minute, time.Minute, func() time.Time { return clock })

	if err := r.Check("agent-A", "test.echo", "n1", clock, time.Minute); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(2 * time.Minute)
	// Triggers a sweep that should drop n1 from the map.
	if err := r.Check("agent-A", "test.echo", "n2", clock, time.Minute); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.seen["agent-A\x00test.echo\x00n1"]; ok {
		t.Fatalf("n1 should have been swept")
	}
}
