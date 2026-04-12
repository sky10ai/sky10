package link

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

func TestSaveAndLoadRelayBootstrapPeers(t *testing.T) {
	t.Parallel()

	addr, err := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/4101/p2p/12D3KooWQJ9m1x5v6Lq3J1s4mP4h9j9bpt5yN4B8pJxWf1dP6W8M")
	if err != nil {
		t.Fatal(err)
	}
	info, err := peer.AddrInfoFromP2pAddr(addr)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "link-relays.json")
	if err := SaveRelayBootstrapPeers(path, []peer.AddrInfo{*info}); err != nil {
		t.Fatalf("SaveRelayBootstrapPeers: %v", err)
	}

	loaded, snapshot, err := LoadRelayBootstrapPeers(path)
	if err != nil {
		t.Fatalf("LoadRelayBootstrapPeers: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded peers = %d, want 1", len(loaded))
	}
	if loaded[0].ID != info.ID {
		t.Fatalf("loaded relay ID = %s, want %s", loaded[0].ID, info.ID)
	}
	if len(snapshot.PeerIDs) != 1 || snapshot.PeerIDs[0] != info.ID.String() {
		t.Fatalf("snapshot peer IDs = %v, want [%s]", snapshot.PeerIDs, info.ID)
	}
	if snapshot.UpdatedAt.IsZero() {
		t.Fatal("expected updated_at to be set")
	}
}

func TestRelayBootstrapPeersFromHostAddrs(t *testing.T) {
	t.Parallel()

	addrs := []ma.Multiaddr{
		ma.StringCast("/ip4/127.0.0.1/tcp/4101/p2p/12D3KooWQJ9m1x5v6Lq3J1s4mP4h9j9bpt5yN4B8pJxWf1dP6W8M/p2p-circuit"),
		ma.StringCast("/ip4/127.0.0.1/tcp/4101"),
	}
	relays := RelayBootstrapPeersFromHostAddrs(addrs)
	if len(relays) != 1 {
		t.Fatalf("relay peer count = %d, want 1", len(relays))
	}
	if relays[0].ID.String() != "12D3KooWQJ9m1x5v6Lq3J1s4mP4h9j9bpt5yN4B8pJxWf1dP6W8M" {
		t.Fatalf("relay peer id = %s", relays[0].ID)
	}
}

func TestLiveRelayHealthFromHost(t *testing.T) {
	t.Parallel()

	configured := []peer.AddrInfo{{
		ID: peer.ID("12D3KooWQJ9m1x5v6Lq3J1s4mP4h9j9bpt5yN4B8pJxWf1dP6W8M"),
	}}
	cachedAt := time.Now().UTC()
	health := LiveRelayHealthFromHost(
		[]ma.Multiaddr{
			ma.StringCast("/ip4/127.0.0.1/tcp/4101/p2p/12D3KooWQJ9m1x5v6Lq3J1s4mP4h9j9bpt5yN4B8pJxWf1dP6W8M/p2p-circuit"),
		},
		configured,
		RelayBootstrapSnapshot{
			PeerIDs:   []string{"12D3KooWQJ9m1x5v6Lq3J1s4mP4h9j9bpt5yN4B8pJxWf1dP6W8M"},
			UpdatedAt: cachedAt,
		},
	)

	if health.ConfiguredPeers != 1 {
		t.Fatalf("configured peers = %d, want 1", health.ConfiguredPeers)
	}
	if health.CachedPeers != 1 {
		t.Fatalf("cached peers = %d, want 1", health.CachedPeers)
	}
	if health.ActivePeers != 1 {
		t.Fatalf("active peers = %d, want 1", health.ActivePeers)
	}
	if health.CurrentPeerID == "" {
		t.Fatal("expected current peer ID")
	}
	if !reflect.DeepEqual(health.ActivePeerIDs, []string{"12D3KooWQJ9m1x5v6Lq3J1s4mP4h9j9bpt5yN4B8pJxWf1dP6W8M"}) {
		t.Fatalf("active peer IDs = %v", health.ActivePeerIDs)
	}
	if health.LastBootstrapAt == nil || !health.LastBootstrapAt.Equal(cachedAt) {
		t.Fatalf("last bootstrap at = %v, want %v", health.LastBootstrapAt, cachedAt)
	}
}
