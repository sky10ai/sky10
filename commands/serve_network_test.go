package commands

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/sky10/sky10/pkg/config"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/link"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
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
		cfg, snapshot, err := resolvedLinkConfig(
			&config.Config{},
			[]string{"/ip4/127.0.0.1/tcp/0", "/ip4/127.0.0.1/tcp/0"},
			[]string{bootstrap},
			nil,
			false,
			"",
		)
		if err != nil {
			t.Fatalf("resolvedLinkConfig: %v", err)
		}
		if snapshot.UpdatedAt != (link.RelayBootstrapSnapshot{}).UpdatedAt {
			t.Fatalf("unexpected relay bootstrap snapshot: %+v", snapshot)
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
		cfg, _, err := resolvedLinkConfig(&config.Config{}, nil, nil, nil, true, "")
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

	t.Run("configured live relay peers", func(t *testing.T) {
		cfg, _, err := resolvedLinkConfig(
			&config.Config{LinkRelays: []string{bootstrap}},
			nil,
			nil,
			nil,
			false,
			"",
		)
		if err != nil {
			t.Fatalf("resolvedLinkConfig: %v", err)
		}
		if len(cfg.RelayPeers) != 1 {
			t.Fatalf("relay peers = %d, want 1", len(cfg.RelayPeers))
		}
		if cfg.RelayPeers[0].ID != peerID {
			t.Fatalf("relay peer id = %s, want %s", cfg.RelayPeers[0].ID, peerID)
		}
	})

	t.Run("cached live relay peers", func(t *testing.T) {
		cachePath := fmt.Sprintf("%s/link-relays.json", t.TempDir())
		info := mustPeerInfo(bootstrap)
		if err := link.SaveRelayBootstrapPeers(cachePath, []peer.AddrInfo{*info}); err != nil {
			t.Fatalf("SaveRelayBootstrapPeers: %v", err)
		}
		cfg, snapshot, err := resolvedLinkConfig(&config.Config{}, nil, nil, nil, false, cachePath)
		if err != nil {
			t.Fatalf("resolvedLinkConfig: %v", err)
		}
		if len(cfg.RelayPeers) != 1 {
			t.Fatalf("relay peers = %d, want 1", len(cfg.RelayPeers))
		}
		if snapshot.UpdatedAt.IsZero() {
			t.Fatal("expected cached relay snapshot updated_at")
		}
	})
}

func TestResolvedManagedLiveRelays(t *testing.T) {
	t.Run("override wins", func(t *testing.T) {
		got := resolvedManagedLiveRelays(&config.Config{LinkRelays: []string{"cfg-relay"}}, []string{" cli-relay ", "cli-relay"})
		want := []string{"cli-relay"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("managed relays = %v, want %v", got, want)
		}
	})

	t.Run("config fallback", func(t *testing.T) {
		got := resolvedManagedLiveRelays(&config.Config{LinkRelays: []string{" relay-a ", "relay-a", "relay-b"}}, nil)
		want := []string{"relay-a", "relay-b"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("managed relays = %v, want %v", got, want)
		}
	})

	t.Run("none", func(t *testing.T) {
		got := resolvedManagedLiveRelays(&config.Config{}, nil)
		if len(got) != 0 {
			t.Fatalf("managed relays = %v, want none", got)
		}
	})
}

func TestManagedLiveRelayWarning(t *testing.T) {
	relayPeer := *mustPeerInfo("/ip4/127.0.0.1/tcp/4101/p2p/12D3KooWQJ9m1x5v6Lq3J1s4mP4h9j9bpt5yN4B8pJxWf1dP6W8M")

	t.Run("configured relays suppress warning", func(t *testing.T) {
		if got := managedLiveRelayWarning([]string{"/ip4/127.0.0.1/tcp/4101/p2p/12D3KooWQJ9m1x5v6Lq3J1s4mP4h9j9bpt5yN4B8pJxWf1dP6W8M"}, []peer.AddrInfo{relayPeer}); got != "" {
			t.Fatalf("warning = %q, want none", got)
		}
	})

	t.Run("cached only warns", func(t *testing.T) {
		got := managedLiveRelayWarning(nil, []peer.AddrInfo{relayPeer})
		if got != "starting without managed live relays; using cached relay bootstrap only" {
			t.Fatalf("warning = %q", got)
		}
	})

	t.Run("missing relays warns", func(t *testing.T) {
		got := managedLiveRelayWarning(nil, nil)
		if got != "starting without managed live relays; direct transport has no degraded-but-live relay fallback" {
			t.Fatalf("warning = %q", got)
		}
	})
}

func TestServeCmdAllowNoLinkRelayFlagHidden(t *testing.T) {
	cmd := ServeCmd()
	flag := cmd.Flags().Lookup("allow-no-link-relay")
	if flag == nil {
		t.Fatal("allow-no-link-relay flag not found")
	}
	if !flag.Hidden {
		t.Fatal("allow-no-link-relay should be hidden")
	}
}

func TestServeCmdHTTPBindFlagDefaultsToLoopback(t *testing.T) {
	cmd := ServeCmd()
	flag := cmd.Flags().Lookup("http-bind")
	if flag == nil {
		t.Fatal("http-bind flag not found")
	}
	if flag.DefValue != skyrpc.DefaultHTTPBindAddress {
		t.Fatalf("http-bind default = %q, want %q", flag.DefValue, skyrpc.DefaultHTTPBindAddress)
	}
	if got := flag.Value.String(); got != skyrpc.DefaultHTTPBindAddress {
		t.Fatalf("http-bind value = %q, want %q", got, skyrpc.DefaultHTTPBindAddress)
	}
}

func mustPeerInfo(raw string) *peer.AddrInfo {
	addr, err := ma.NewMultiaddr(raw)
	if err != nil {
		panic(err)
	}
	info, err := peer.AddrInfoFromP2pAddr(addr)
	if err != nil {
		panic(err)
	}
	return info
}
