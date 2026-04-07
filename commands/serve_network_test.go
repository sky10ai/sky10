package commands

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/sky10/sky10/pkg/config"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/link"
)

func TestResolvedRelays(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		got := resolvedRelays(&config.Config{}, nil, false)
		if !reflect.DeepEqual(got, config.DefaultNostrRelays) {
			t.Fatalf("relays = %v, want %v", got, config.DefaultNostrRelays)
		}
	})

	t.Run("override", func(t *testing.T) {
		got := resolvedRelays(&config.Config{}, []string{" wss://one ", "wss://one", "wss://two"}, false)
		want := []string{"wss://one", "wss://two"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("relays = %v, want %v", got, want)
		}
	})

	t.Run("no default", func(t *testing.T) {
		got := resolvedRelays(&config.Config{}, nil, true)
		if len(got) != 0 {
			t.Fatalf("relays = %v, want none", got)
		}
	})
}

func TestResolvedLinkConfig(t *testing.T) {
	key, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	peerID, err := link.PeerIDFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := fmt.Sprintf("/ip4/127.0.0.1/tcp/4101/p2p/%s", peerID.String())

	t.Run("custom bootstrap and listen", func(t *testing.T) {
		cfg, err := resolvedLinkConfig(
			[]string{"/ip4/127.0.0.1/tcp/0", "/ip4/127.0.0.1/tcp/0"},
			[]string{bootstrap},
			false,
		)
		if err != nil {
			t.Fatalf("resolvedLinkConfig: %v", err)
		}
		if got, want := cfg.ListenAddrs, []string{"/ip4/127.0.0.1/tcp/0"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("listen = %v, want %v", got, want)
		}
		if len(cfg.BootstrapPeers) != 1 {
			t.Fatalf("bootstrap peers = %d, want 1", len(cfg.BootstrapPeers))
		}
		if cfg.BootstrapPeers[0].ID != peerID {
			t.Fatalf("bootstrap peer id = %s, want %s", cfg.BootstrapPeers[0].ID, peerID)
		}
	})

	t.Run("no default bootstrap", func(t *testing.T) {
		cfg, err := resolvedLinkConfig(nil, nil, true)
		if err != nil {
			t.Fatalf("resolvedLinkConfig: %v", err)
		}
		if cfg.BootstrapPeers == nil {
			t.Fatal("bootstrap peers should be an explicit empty slice")
		}
		if len(cfg.BootstrapPeers) != 0 {
			t.Fatalf("bootstrap peers = %d, want 0", len(cfg.BootstrapPeers))
		}
	})
}
