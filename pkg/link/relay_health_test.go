package link

import (
	"errors"
	"testing"
	"time"
)

func TestNostrRelayTrackerOrdersHealthyRelaysFirst(t *testing.T) {
	t.Parallel()

	tracker := NewNostrRelayTracker([]string{
		"wss://slow.example",
		"wss://healthy.example",
		"wss://failing.example",
	})

	tracker.Record("wss://slow.example", 180*time.Millisecond, nil)
	tracker.Record("wss://healthy.example", 25*time.Millisecond, nil)
	tracker.Record("wss://failing.example", 0, errors.New("timeout"))

	got := tracker.Ordered(nil)
	want := []string{
		"wss://healthy.example",
		"wss://slow.example",
		"wss://failing.example",
	}
	if len(got) != len(want) {
		t.Fatalf("ordered count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ordered[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNostrRelayTrackerSnapshotIncludesLatencyAndErrors(t *testing.T) {
	t.Parallel()

	tracker := NewNostrRelayTracker([]string{"wss://relay.example"})
	tracker.Record("wss://relay.example", 42*time.Millisecond, nil)
	tracker.Record("wss://relay.example", 0, errors.New("connect failed"))

	snapshot := tracker.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshot))
	}
	if snapshot[0].URL != "wss://relay.example" {
		t.Fatalf("snapshot relay = %q", snapshot[0].URL)
	}
	if snapshot[0].Successes != 1 {
		t.Fatalf("successes = %d, want 1", snapshot[0].Successes)
	}
	if snapshot[0].Failures != 1 {
		t.Fatalf("failures = %d, want 1", snapshot[0].Failures)
	}
	if snapshot[0].AverageLatencyMS != 42 {
		t.Fatalf("average latency = %d, want 42", snapshot[0].AverageLatencyMS)
	}
	if snapshot[0].LastError == "" {
		t.Fatal("expected last error")
	}
	if snapshot[0].LastFailureAt == nil {
		t.Fatal("expected last failure timestamp")
	}
}

func TestNostrRelayTrackerCoordinationSnapshotTracksDegradedPublish(t *testing.T) {
	t.Parallel()

	tracker := NewNostrRelayTracker([]string{
		"wss://one.example",
		"wss://two.example",
		"wss://three.example",
	})
	outcome := tracker.RecordPublishOutcome("presence", 3, 1, DefaultNostrPublishQuorum(3))
	if !outcome.Degraded {
		t.Fatal("expected degraded publish outcome")
	}

	snapshot := tracker.CoordinationSnapshot()
	if snapshot.ConfiguredRelays != 3 {
		t.Fatalf("configured relays = %d, want 3", snapshot.ConfiguredRelays)
	}
	if snapshot.LastPublish.Operation != "presence" {
		t.Fatalf("last publish operation = %q", snapshot.LastPublish.Operation)
	}
	if snapshot.LastPublish.Quorum != 2 {
		t.Fatalf("publish quorum = %d, want 2", snapshot.LastPublish.Quorum)
	}
	if !snapshot.LastPublish.Degraded {
		t.Fatal("expected degraded coordination snapshot")
	}
}
