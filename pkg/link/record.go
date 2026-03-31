package link

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/peer"
)

// AgentRecord is the data published to the DHT. Other agents resolve this
// to learn capabilities and connectivity info.
type AgentRecord struct {
	Address      string       `json:"address"`
	PeerID       string       `json:"peer_id"`
	Capabilities []Capability `json:"capabilities"`
	Multiaddrs   []string     `json:"multiaddrs"`
	Version      string       `json:"version"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

// dhtKey returns the DHT key for an agent record.
func dhtKey(id peer.ID) string {
	return "/skylink/agent/" + id.String()
}

// initDHT initializes the Kademlia DHT on the node. Only called in
// network mode.
func (n *Node) initDHT(ctx context.Context) error {
	d, err := dht.New(ctx, n.host, dht.Mode(dht.ModeAutoServer))
	if err != nil {
		return fmt.Errorf("creating DHT: %w", err)
	}
	if err := d.Bootstrap(ctx); err != nil {
		return fmt.Errorf("bootstrapping DHT: %w", err)
	}
	n.dht = d
	return nil
}

// PublishRecord publishes the agent's record to the DHT.
func (n *Node) PublishRecord(ctx context.Context) error {
	if n.dht == nil {
		return fmt.Errorf("DHT not initialized (network mode required)")
	}

	addrs := make([]string, 0, len(n.host.Addrs()))
	for _, a := range n.host.Addrs() {
		addrs = append(addrs, a.String())
	}

	var caps []Capability
	if n.registry != nil {
		caps = n.registry.Capabilities()
	}

	rec := AgentRecord{
		Address:      n.Address(),
		PeerID:       n.peerID.String(),
		Capabilities: caps,
		Multiaddrs:   addrs,
		Version:      n.version,
		UpdatedAt:    time.Now().UTC(),
	}

	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshaling agent record: %w", err)
	}

	return n.dht.PutValue(ctx, dhtKey(n.peerID), data)
}

// ResolveRecord resolves another agent's record from the DHT by sky10 address.
func (n *Node) ResolveRecord(ctx context.Context, address string) (*AgentRecord, error) {
	if n.dht == nil {
		return nil, fmt.Errorf("DHT not initialized (network mode required)")
	}

	pid, err := PeerIDFromAddress(address)
	if err != nil {
		return nil, err
	}

	return n.resolveByPeerID(ctx, pid)
}

// resolveByPeerID resolves an agent record by peer ID.
func (n *Node) resolveByPeerID(ctx context.Context, id peer.ID) (*AgentRecord, error) {
	data, err := n.dht.GetValue(ctx, dhtKey(id))
	if err != nil {
		return nil, fmt.Errorf("resolving agent record: %w", err)
	}

	var rec AgentRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("unmarshaling agent record: %w", err)
	}
	return &rec, nil
}
