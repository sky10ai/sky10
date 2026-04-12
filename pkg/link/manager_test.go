package link

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestManagerCoalescesTriggers(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := make(chan ConvergenceBatch, 4)
	manager := NewManager(nil, func(ctx context.Context, batch ConvergenceBatch) error {
		calls <- batch
		return nil
	},
		WithManagerDebounce(10*time.Millisecond),
		WithManagerRunTimeout(time.Second),
		WithManagerJitter(func(d time.Duration) time.Duration { return d }),
	)

	done := make(chan error, 1)
	go func() {
		done <- manager.Run(ctx)
	}()

	manager.Trigger("startup", "")
	manager.Trigger("address_change", "1")
	manager.Trigger("address_change", "1")

	select {
	case batch := <-calls:
		if len(batch.Reasons) != 2 {
			t.Fatalf("reason count = %d, want 2", len(batch.Reasons))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for convergence batch")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("manager did not stop")
	}
}

func TestManagerRetriesAfterFailure(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		mu    sync.Mutex
		calls []ConvergenceBatch
	)
	manager := NewManager(nil, func(ctx context.Context, batch ConvergenceBatch) error {
		mu.Lock()
		calls = append(calls, batch)
		callCount := len(calls)
		mu.Unlock()
		if callCount == 1 {
			return errors.New("boom")
		}
		return nil
	},
		WithManagerDebounce(5*time.Millisecond),
		WithManagerRetryBounds(10*time.Millisecond, 10*time.Millisecond),
		WithManagerRunTimeout(time.Second),
		WithManagerJitter(func(d time.Duration) time.Duration { return d }),
	)

	done := make(chan error, 1)
	go func() {
		done <- manager.Run(ctx)
	}()

	manager.Trigger("startup", "")

	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		count := len(calls)
		var second ConvergenceBatch
		if count >= 2 {
			second = calls[1]
		}
		mu.Unlock()
		if count >= 2 {
			if second.Retry != 1 {
				t.Fatalf("second retry = %d, want 1", second.Retry)
			}
			foundRetry := false
			for _, reason := range second.Reasons {
				if reason.Reason == internalReasonRetry {
					foundRetry = true
					break
				}
			}
			if !foundRetry {
				t.Fatal("expected retry reason in second batch")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for retry batch")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("manager did not stop")
	}
}

func TestManagerSafetySweep(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := make(chan ConvergenceBatch, 2)
	manager := NewManager(nil, func(ctx context.Context, batch ConvergenceBatch) error {
		calls <- batch
		return nil
	},
		WithManagerDebounce(5*time.Millisecond),
		WithManagerSafetySweep(10*time.Millisecond),
		WithManagerRunTimeout(time.Second),
		WithManagerJitter(func(d time.Duration) time.Duration { return d }),
	)

	done := make(chan error, 1)
	go func() {
		done <- manager.Run(ctx)
	}()

	select {
	case batch := <-calls:
		if !batch.SafetyRun {
			t.Fatalf("expected safety sweep batch, got %+v", batch)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for safety sweep")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("manager did not stop")
	}
}
