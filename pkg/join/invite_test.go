package join

import (
	"testing"
	"time"
)

func TestP2PInviteRoundtrip(t *testing.T) {
	t.Parallel()

	code, err := CreateP2PInviteWithBootstrap(
		"sky10qvx2mz9abc123",
		[]string{"wss://relay.damus.io", "wss://nos.lol"},
		"12D3KooWExamplePeerID",
		[]string{"/ip4/203.0.113.10/udp/4242/quic-v1/p2p/12D3KooWExamplePeerID"},
	)
	if err != nil {
		t.Fatal(err)
	}

	if code[:len(p2pInvitePrefix)] != p2pInvitePrefix {
		t.Errorf("code should start with %q, got %q", p2pInvitePrefix, code[:10])
	}

	invite, err := DecodeP2PInvite(code)
	if err != nil {
		t.Fatal(err)
	}
	if invite.Address != "sky10qvx2mz9abc123" {
		t.Errorf("address = %q, want sky10qvx2mz9abc123", invite.Address)
	}
	if len(invite.NostrRelays) != 2 {
		t.Errorf("relays = %v, want 2", invite.NostrRelays)
	}
	if invite.InviteID == "" {
		t.Error("invite ID should not be empty")
	}
	if invite.PeerID != "12D3KooWExamplePeerID" {
		t.Errorf("peer_id = %q", invite.PeerID)
	}
	if len(invite.Multiaddrs) != 1 {
		t.Fatalf("multiaddrs = %v, want 1", invite.Multiaddrs)
	}
	if invite.IssuedAt.IsZero() {
		t.Fatal("issued_at should not be zero")
	}
	if time.Since(invite.IssuedAt) > time.Minute {
		t.Fatalf("issued_at too old: %s", invite.IssuedAt)
	}
}

func TestDecodeP2PInviteInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code string
	}{
		{"empty", ""},
		{"wrong prefix", "sky10invite_abc"},
		{"bad base64", p2pInvitePrefix + "!!!"},
		{"bad json", p2pInvitePrefix + "dGVzdA"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeP2PInvite(tt.code)
			if err == nil {
				t.Error("expected error")
			}
		})
	}
}
