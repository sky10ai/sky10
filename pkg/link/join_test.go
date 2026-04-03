package link

import (
	"testing"
)

func TestP2PInviteRoundtrip(t *testing.T) {
	t.Parallel()

	code, err := CreateP2PInvite(
		"sky10qvx2mz9abc123",
		[]string{"wss://relay.damus.io", "wss://nos.lol"},
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
