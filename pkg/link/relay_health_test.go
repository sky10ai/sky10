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

func TestNostrRelayTrackerTracksLiveSubscriptions(t *testing.T) {
	t.Parallel()

	tracker := NewNostrRelayTracker([]string{
		"wss://one.example",
		"wss://two.example",
		"wss://three.example",
	})
	tracker.RecordSubscriptionConnect("sky10-private:alice", "wss://one.example")
	tracker.RecordSubscriptionEvent("sky10-private:alice", "wss://one.example")
	tracker.RecordSubscriptionConnect("sky10-private:alice", "wss://two.example")
	tracker.RecordSubscriptionDisconnect("sky10-private:alice", "wss://two.example", errors.New("closed"))

	snapshot := tracker.CoordinationSnapshot()
	if len(snapshot.Subscriptions) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(snapshot.Subscriptions))
	}
	subscription := snapshot.Subscriptions[0]
	if subscription.Label != "sky10-private:alice" {
		t.Fatalf("subscription label = %q", subscription.Label)
	}
	if subscription.ActiveRelays != 1 {
		t.Fatalf("active relays = %d, want 1", subscription.ActiveRelays)
	}
	if subscription.RequiredRelays != 2 {
		t.Fatalf("required relays = %d, want 2", subscription.RequiredRelays)
	}
	if subscription.LastEventAt == nil {
		t.Fatal("expected last event timestamp")
	}
	if subscription.LastDisconnectAt == nil {
		t.Fatal("expected last disconnect timestamp")
	}
	if subscription.LastError == "" {
		t.Fatal("expected subscription last error")
	}

	relays := tracker.Snapshot()
	if len(relays) != 3 {
		t.Fatalf("relay count = %d, want 3", len(relays))
	}
	if relays[0].ActiveSubscriptions == 0 && relays[1].ActiveSubscriptions == 0 && relays[2].ActiveSubscriptions == 0 {
		t.Fatal("expected at least one active subscription on relay snapshot")
	}
}

func TestNostrRelayTrackerAdaptivePollIntervalFollowsSubscriptionHealth(t *testing.T) {
	t.Parallel()

	tracker := NewNostrRelayTracker([]string{
		"wss://one.example",
		"wss://two.example",
		"wss://three.example",
	})
	healthy := 75 * time.Second
	degraded := 30 * time.Second
	down := 15 * time.Second
	label := "mailbox:alice"

	if got := tracker.AdaptivePollInterval(label, healthy, degraded, down); got != down {
		t.Fatalf("initial poll interval = %s, want %s", got, down)
	}

	tracker.RecordSubscriptionConnect(label, "wss://one.example")
	if got := tracker.AdaptivePollInterval(label, healthy, degraded, down); got != degraded {
		t.Fatalf("degraded poll interval = %s, want %s", got, degraded)
	}

	tracker.RecordSubscriptionConnect(label, "wss://two.example")
	if got := tracker.AdaptivePollInterval(label, healthy, degraded, down); got != healthy {
		t.Fatalf("healthy poll interval = %s, want %s", got, healthy)
	}

	tracker.RecordSubscriptionDisconnect(label, "wss://one.example", errors.New("closed"))
	tracker.RecordSubscriptionDisconnect(label, "wss://two.example", errors.New("closed"))
	if got := tracker.AdaptivePollInterval(label, healthy, degraded, down); got != down {
		t.Fatalf("down poll interval = %s, want %s", got, down)
	}
}
