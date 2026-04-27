package comms

import (
	"testing"
	"time"
)

func TestQuotaStoreAllowsBurst(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	q := NewQuotaStore(func() time.Time { return now })
	limit := RateLimit{PerAgent: 10, Burst: 3, Window: time.Second}

	for i := 0; i < 3; i++ {
		if !q.Check("agent-A", "test.echo", limit) {
			t.Fatalf("burst call %d denied", i)
		}
	}
}

func TestQuotaStoreRejectsAfterBurst(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	q := NewQuotaStore(func() time.Time { return now })
	limit := RateLimit{PerAgent: 10, Burst: 3, Window: time.Second}

	for i := 0; i < 3; i++ {
		if !q.Check("agent-A", "test.echo", limit) {
			t.Fatalf("setup call %d denied", i)
		}
	}
	if q.Check("agent-A", "test.echo", limit) {
		t.Fatal("4th call should have been rejected")
	}
}

func TestQuotaStoreRefillsOverTime(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	clock := now
	q := NewQuotaStore(func() time.Time { return clock })
	limit := RateLimit{PerAgent: 10, Burst: 3, Window: time.Second}

	for i := 0; i < 3; i++ {
		if !q.Check("agent-A", "test.echo", limit) {
			t.Fatalf("setup call %d denied", i)
		}
	}
	if q.Check("agent-A", "test.echo", limit) {
		t.Fatal("burst exhausted, expected rejection")
	}
	// 10 tokens / 1 second = 10 tokens/sec; advance 200ms = 2 tokens.
	clock = clock.Add(200 * time.Millisecond)
	if !q.Check("agent-A", "test.echo", limit) {
		t.Fatal("first refilled token denied")
	}
	if !q.Check("agent-A", "test.echo", limit) {
		t.Fatal("second refilled token denied")
	}
	if q.Check("agent-A", "test.echo", limit) {
		t.Fatal("third call after refill should have been rejected")
	}
}

func TestQuotaStoreScopesByAgentAndType(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	q := NewQuotaStore(func() time.Time { return now })
	limit := RateLimit{PerAgent: 10, Burst: 1, Window: time.Second}

	if !q.Check("agent-A", "test.echo", limit) {
		t.Fatal("agent-A first call denied")
	}
	if q.Check("agent-A", "test.echo", limit) {
		t.Fatal("agent-A burst exhausted but second call allowed")
	}
	if !q.Check("agent-B", "test.echo", limit) {
		t.Fatal("agent-B should have its own bucket")
	}
	if !q.Check("agent-A", "test.other", limit) {
		t.Fatal("different type should have its own bucket")
	}
}
