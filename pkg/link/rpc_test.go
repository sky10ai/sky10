package link

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestRPCDispatchPrefix(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	h := NewRPCHandler(n, nil)

	_, _, handled := h.Dispatch(context.Background(), "skyfs.list", nil)
	if handled {
		t.Fatal("should not handle skyfs.* methods")
	}

	_, _, handled = h.Dispatch(context.Background(), "skykv.get", nil)
	if handled {
		t.Fatal("should not handle skykv.* methods")
	}
}

func TestRPCStatus(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	startTestNode(t, n)
	server := startTestSTUNServer(t, 0, nil)
	tracker := NewRuntimeHealthTracker()
	tracker.RecordReachability("public")
	tracker.RecordAddressUpdate(1)
	tracker.RecordPublish("dht", nil)
	tracker.RecordMailbox("handed_off", "queued", "item-123")

	h := NewRPCHandler(
		n,
		nil,
		WithSTUNServers([]string{server}),
		WithRuntimeHealthTracker(tracker),
		WithMailboxHealthProvider(func() MailboxHealth {
			now := time.Now().UTC()
			return MailboxHealth{
				Queued:              2,
				HandedOff:           1,
				PendingPrivate:      1,
				PendingSky10Network: 1,
				LastHandoffAt:       &now,
			}
		}),
	)

	result, err, handled := h.Dispatch(context.Background(), "skylink.status", nil)
	if !handled {
		t.Fatal("should handle skylink.status")
	}
	if err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(result)
	var status statusResult
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatal(err)
	}
	if status.PeerID == "" {
		t.Fatal("expected non-empty peer_id")
	}
	if status.Address == "" {
		t.Fatal("expected non-empty address")
	}
	if status.Mode != "private" {
		t.Fatalf("expected mode 'private', got %q", status.Mode)
	}
	if len(status.Addrs) == 0 {
		t.Fatal("expected at least one listen address")
	}
	if status.PrivatePeers != 0 {
		t.Fatalf("expected 0 private peers, got %d", status.PrivatePeers)
	}
	if !status.Health.Netcheck.UDP {
		t.Fatal("expected cached netcheck UDP reachability")
	}
	if status.Health.PreferredTransport != "quic" {
		t.Fatalf("preferred transport = %q, want quic", status.Health.PreferredTransport)
	}
	if status.Health.Reachability != "public" {
		t.Fatalf("reachability = %q, want public", status.Health.Reachability)
	}
	if status.Health.Mailbox.HandedOff != 1 {
		t.Fatalf("mailbox handed_off = %d, want 1", status.Health.Mailbox.HandedOff)
	}
	if len(status.Health.Events) == 0 {
		t.Fatal("expected recent health events")
	}
}

func TestRPCPeers(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	startTestNode(t, n)
	h := NewRPCHandler(n, nil)

	result, err, handled := h.Dispatch(context.Background(), "skylink.peers", nil)
	if !handled {
		t.Fatal("should handle skylink.peers")
	}
	if err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(result)
	var peers peersResult
	if err := json.Unmarshal(data, &peers); err != nil {
		t.Fatal(err)
	}
	if peers.Count != 0 {
		t.Fatalf("expected 0 peers, got %d", peers.Count)
	}
}

func TestRPCNetcheck(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	startTestNode(t, n)

	server := startTestSTUNServer(t, 0, nil)
	h := NewRPCHandler(n, nil, WithSTUNServers([]string{server}))

	result, err, handled := h.Dispatch(context.Background(), "skylink.netcheck", nil)
	if !handled {
		t.Fatal("should handle skylink.netcheck")
	}
	if err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(result)
	var status NetcheckResult
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatal(err)
	}
	if !status.UDP {
		t.Fatal("expected UDP reachability")
	}
	if status.PublicAddr == "" {
		t.Fatal("expected public_addr")
	}
}

func TestRPCUnknownMethod(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	h := NewRPCHandler(n, nil)

	_, err, handled := h.Dispatch(context.Background(), "skylink.bogus", nil)
	if !handled {
		t.Fatal("should handle skylink.* even if unknown")
	}
	if err == nil {
		t.Fatal("expected error for unknown method")
	}
}
