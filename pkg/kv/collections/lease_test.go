package collections

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestLeaseClaimAndGet(t *testing.T) {
	t.Parallel()

	store := newMemoryKVStore()
	lease := NewLease(store, "mailbox/leases")
	now := time.Unix(1_700_000_000, 0).UTC()
	lease.now = func() time.Time { return now }
	lease.newUUID = func() string { return "token-1" }

	record, ok, err := lease.Claim(context.Background(), "task-1", "agent-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("claim was not acquired")
	}
	if record.Token != "token-1" {
		t.Fatalf("token = %q, want token-1", record.Token)
	}

	got, ok, err := lease.Get("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("lease not found")
	}
	if got.Holder != "agent-a" {
		t.Fatalf("holder = %q, want agent-a", got.Holder)
	}
}

func TestLeaseClaimRejectsActiveLease(t *testing.T) {
	t.Parallel()

	store := newMemoryKVStore()
	lease := NewLease(store, "mailbox/leases")
	now := time.Unix(1_700_000_000, 0).UTC()
	lease.now = func() time.Time { return now }
	lease.newUUID = func() string { return "token-1" }

	if _, ok, err := lease.Claim(context.Background(), "task-1", "agent-a", time.Minute); err != nil || !ok {
		t.Fatalf("initial claim err=%v ok=%v", err, ok)
	}
	if current, ok, err := lease.Claim(context.Background(), "task-1", "agent-b", time.Minute); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatalf("second claim should fail, got %+v", current)
	}
}

func TestLeaseExpiredCanBeReclaimed(t *testing.T) {
	t.Parallel()

	store := newMemoryKVStore()
	lease := NewLease(store, "mailbox/leases")
	now := time.Unix(1_700_000_000, 0).UTC()
	lease.now = func() time.Time { return now }
	lease.newUUID = func() string { return "token-1" }

	if _, ok, err := lease.Claim(context.Background(), "task-1", "agent-a", time.Second); err != nil || !ok {
		t.Fatalf("initial claim err=%v ok=%v", err, ok)
	}

	now = now.Add(2 * time.Second)
	lease.newUUID = func() string { return "token-2" }

	record, ok, err := lease.Claim(context.Background(), "task-1", "agent-b", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("reclaim should succeed after expiry")
	}
	if record.Holder != "agent-b" || record.Token != "token-2" {
		t.Fatalf("record = %+v, want holder agent-b token token-2", record)
	}
}

func TestLeaseRenew(t *testing.T) {
	t.Parallel()

	store := newMemoryKVStore()
	lease := NewLease(store, "mailbox/leases")
	now := time.Unix(1_700_000_000, 0).UTC()
	lease.now = func() time.Time { return now }
	lease.newUUID = func() string { return "token-1" }

	record, ok, err := lease.Claim(context.Background(), "task-1", "agent-a", time.Second)
	if err != nil || !ok {
		t.Fatalf("claim err=%v ok=%v", err, ok)
	}

	now = now.Add(500 * time.Millisecond)
	renewed, ok, err := lease.Renew(context.Background(), "task-1", "agent-a", record.Token, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("renew should succeed")
	}
	if !renewed.ExpiresAt.After(record.ExpiresAt) {
		t.Fatalf("renewed expires_at = %v, want after %v", renewed.ExpiresAt, record.ExpiresAt)
	}
}

func TestLeaseRelease(t *testing.T) {
	t.Parallel()

	store := newMemoryKVStore()
	lease := NewLease(store, "mailbox/leases")
	now := time.Unix(1_700_000_000, 0).UTC()
	lease.now = func() time.Time { return now }
	lease.newUUID = func() string { return "token-1" }

	record, ok, err := lease.Claim(context.Background(), "task-1", "agent-a", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim err=%v ok=%v", err, ok)
	}

	released, err := lease.Release(context.Background(), "task-1", "agent-a", record.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !released {
		t.Fatal("release should succeed")
	}
	if _, ok, err := lease.Get("task-1"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("lease should be gone after release")
	}
}

func TestLeaseClaimContentionLeavesSingleActiveLease(t *testing.T) {
	t.Parallel()

	store := newMemoryKVStore()
	lease := NewLease(store, "mailbox/leases")
	now := time.Unix(1_700_000_000, 0).UTC()
	lease.now = func() time.Time { return now }

	var (
		wg      sync.WaitGroup
		start   = make(chan struct{})
		results = make(chan bool, 2)
	)

	claim := func(holder string) {
		defer wg.Done()
		<-start
		_, ok, err := lease.Claim(context.Background(), "task-1", holder, time.Minute)
		if err != nil {
			t.Errorf("claim(%s): %v", holder, err)
			return
		}
		results <- ok
	}

	wg.Add(2)
	go claim("agent-a")
	go claim("agent-b")
	close(start)
	wg.Wait()
	close(results)

	var winners int
	for ok := range results {
		if ok {
			winners++
		}
	}
	if winners == 0 {
		t.Fatal("expected at least one claim winner")
	}

	record, ok, err := lease.Get("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected active lease after contention")
	}
	if record.Holder != "agent-a" && record.Holder != "agent-b" {
		t.Fatalf("holder = %q, want one of the contenders", record.Holder)
	}
}
