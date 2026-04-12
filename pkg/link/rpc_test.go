package link

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
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
		WithRelayHealthProvider(func() []NostrRelayHealth {
			return []NostrRelayHealth{{
				URL:                 "wss://relay.example",
				Successes:           3,
				Failures:            1,
				AverageLatencyMS:    28,
				ActiveSubscriptions: 1,
			}}
		}),
		WithNostrCoordinationProvider(func() NostrCoordinationHealth {
			now := time.Now().UTC()
			return NostrCoordinationHealth{
				ConfiguredRelays: 3,
				LastPublish: NostrPublishOutcome{
					Operation: "presence",
					Attempts:  3,
					Successes: 1,
					Quorum:    2,
					Degraded:  true,
					At:        &now,
				},
				Subscriptions: []NostrSubscriptionHealth{{
					Label:          "sky10-private:test",
					ActiveRelays:   1,
					RequiredRelays: 2,
					LastEventAt:    &now,
				}},
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
	if len(status.Health.Relays) != 1 {
		t.Fatalf("relay count = %d, want 1", len(status.Health.Relays))
	}
	if status.Health.Relays[0].URL != "wss://relay.example" {
		t.Fatalf("relay url = %q", status.Health.Relays[0].URL)
	}
	if status.Health.Relays[0].ActiveSubscriptions != 1 {
		t.Fatalf("relay active subscriptions = %d, want 1", status.Health.Relays[0].ActiveSubscriptions)
	}
	if status.Health.CoordinationDegradedReason != "nostr_publish_quorum" {
		t.Fatalf("coordination degraded reason = %q", status.Health.CoordinationDegradedReason)
	}
	if status.Health.Nostr.LastPublish.Quorum != 2 {
		t.Fatalf("publish quorum = %d, want 2", status.Health.Nostr.LastPublish.Quorum)
	}
	if len(status.Health.Nostr.Subscriptions) != 1 {
		t.Fatalf("subscription count = %d, want 1", len(status.Health.Nostr.Subscriptions))
	}
	if len(status.Health.Events) == 0 {
		t.Fatal("expected recent health events")
	}
}

func TestCoordinationDegradedReasonTracksSubscriptionQuorum(t *testing.T) {
	t.Parallel()

	health := NostrCoordinationHealth{
		ConfiguredRelays: 3,
		Subscriptions: []NostrSubscriptionHealth{{
			Label:          "mailbox:test",
			ActiveRelays:   1,
			RequiredRelays: 2,
		}},
	}
	if got := coordinationDegradedReason(health); got != "nostr_subscription_quorum" {
		t.Fatalf("coordination degraded reason = %q, want nostr_subscription_quorum", got)
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

func TestRPCResolveIncludesPathScores(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bundleA := generateTestBundle(t, "nodeA")
	nodeA, err := New(bundleA, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	go nodeA.Run(ctx)
	waitForHost(t, nodeA)

	membershipA, err := nodeA.CurrentMembershipRecord()
	if err != nil {
		t.Fatalf("membership record A: %v", err)
	}
	presenceA, err := nodeA.CurrentPresenceRecord(0)
	if err != nil {
		t.Fatalf("presence record A: %v", err)
	}
	pid := nodeA.PeerID().String()
	presenceA.Multiaddrs = []string{
		"/ip4/203.0.113.10/udp/4101/quic-v1/p2p/" + pid,
		"/ip4/203.0.113.10/tcp/4101/p2p/" + pid,
	}

	bundleB := generateTestBundle(t, "nodeB")
	nodeB, err := New(bundleB, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(nodeB)
	resolver.nostr = &staticDiscovery{
		membership: membershipA,
		presences:  []*PresenceRecord{presenceA},
	}
	resolver.netcheck = func(context.Context, []string) NetcheckResult {
		return NetcheckResult{
			UDP:        true,
			PublicAddr: "203.0.113.99:55000",
		}
	}
	tcpAddr, err := ma.NewMultiaddr("/ip4/203.0.113.10/tcp/4101")
	if err != nil {
		t.Fatalf("NewMultiaddr(tcp): %v", err)
	}
	resolver.paths.RecordSuccess(nodeA.PeerID(), "nostr", &peer.AddrInfo{
		ID:    nodeA.PeerID(),
		Addrs: []ma.Multiaddr{tcpAddr},
	})

	h := NewRPCHandler(nodeB, resolver)
	params, _ := json.Marshal(resolveParams{Address: bundleA.Address()})
	result, err, handled := h.Dispatch(ctx, "skylink.resolve", params)
	if !handled {
		t.Fatal("should handle skylink.resolve")
	}
	if err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(result)
	var response struct {
		Peers []struct {
			PreferredTransport   string      `json:"preferred_transport"`
			LastSuccessTransport string      `json:"last_success_transport"`
			LastSuccessSource    string      `json:"last_success_source"`
			LastSuccessAddr      string      `json:"last_success_addr"`
			AddrScores           []AddrScore `json:"addr_scores"`
		} `json:"peers"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Peers) != 1 {
		t.Fatalf("peer count = %d, want 1", len(response.Peers))
	}
	if response.Peers[0].PreferredTransport != "direct_tcp" {
		t.Fatalf("preferred transport = %q, want direct_tcp", response.Peers[0].PreferredTransport)
	}
	if response.Peers[0].LastSuccessTransport != "direct_tcp" {
		t.Fatalf("last success transport = %q, want direct_tcp", response.Peers[0].LastSuccessTransport)
	}
	if response.Peers[0].LastSuccessSource != "nostr" {
		t.Fatalf("last success source = %q, want nostr", response.Peers[0].LastSuccessSource)
	}
	if response.Peers[0].LastSuccessAddr == "" {
		t.Fatal("expected last success addr")
	}
	if len(response.Peers[0].AddrScores) == 0 {
		t.Fatal("expected addr scores")
	}
	if response.Peers[0].AddrScores[0].Transport != "direct_tcp" {
		t.Fatalf("top addr score transport = %q, want direct_tcp", response.Peers[0].AddrScores[0].Transport)
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
