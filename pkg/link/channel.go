package link

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	skykey "github.com/sky10/sky10/pkg/key"
)

// Channel is an encrypted pub/sub topic with membership.
type Channel struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Members []string `json:"members"` // sky10q... addresses
	key     []byte   // AES-256 symmetric key (not serialized)
}

// channelMessage is the plaintext envelope inside encrypted channel data.
type channelMessage struct {
	From string          `json:"from"` // sky10q... address of sender
	Ts   int64           `json:"ts"`   // unix timestamp
	Data json.RawMessage `json:"data"` // application payload
}

// ChannelManager manages encrypted channels on a node.
type ChannelManager struct {
	node   *Node
	pubsub *PubSub

	mu       sync.RWMutex
	channels map[string]*Channel
	handlers map[string]func(from string, data []byte)
}

// newChannelManager creates a channel manager.
func newChannelManager(node *Node, ps *PubSub) *ChannelManager {
	return &ChannelManager{
		node:     node,
		pubsub:   ps,
		channels: make(map[string]*Channel),
		handlers: make(map[string]func(from string, data []byte)),
	}
}

// CreateChannel creates a new encrypted channel with a fresh key.
func (cm *ChannelManager) CreateChannel(ctx context.Context, name string) (*Channel, error) {
	id, err := randomID()
	if err != nil {
		return nil, err
	}

	key, err := skykey.GenerateSymmetricKey()
	if err != nil {
		return nil, fmt.Errorf("generating channel key: %w", err)
	}

	ch := &Channel{
		ID:      id,
		Name:    name,
		Members: []string{cm.node.Address()},
		key:     key,
	}

	cm.mu.Lock()
	cm.channels[id] = ch
	cm.mu.Unlock()

	// Subscribe to the GossipSub topic for this channel.
	if err := cm.subscribeChannel(ctx, ch); err != nil {
		return nil, err
	}

	return ch, nil
}

// JoinChannel joins an existing channel using a wrapped key.
func (cm *ChannelManager) JoinChannel(ctx context.Context, id string, wrappedKey []byte) (*Channel, error) {
	key, err := skykey.UnwrapKey(wrappedKey, cm.node.identity.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("unwrapping channel key: %w", err)
	}

	ch := &Channel{
		ID:  id,
		key: key,
	}

	cm.mu.Lock()
	cm.channels[id] = ch
	cm.mu.Unlock()

	if err := cm.subscribeChannel(ctx, ch); err != nil {
		return nil, err
	}

	return ch, nil
}

// SendToChannel encrypts and publishes a message to a channel.
func (cm *ChannelManager) SendToChannel(ctx context.Context, channelID string, data []byte) error {
	cm.mu.RLock()
	ch, ok := cm.channels[channelID]
	cm.mu.RUnlock()
	if !ok {
		return fmt.Errorf("channel %q not found", channelID)
	}

	msg := channelMessage{
		From: cm.node.Address(),
		Ts:   time.Now().Unix(),
		Data: data,
	}
	plain, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}

	encrypted, err := skykey.Encrypt(plain, ch.key)
	if err != nil {
		return fmt.Errorf("encrypting message: %w", err)
	}

	return cm.pubsub.Publish(ctx, channelTopic(channelID), encrypted)
}

// InviteToChannel wraps the channel key for a new member's public key.
func (cm *ChannelManager) InviteToChannel(channelID string, memberAddr string) ([]byte, error) {
	cm.mu.RLock()
	ch, ok := cm.channels[channelID]
	cm.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("channel %q not found", channelID)
	}

	memberKey, err := skykey.ParseAddress(memberAddr)
	if err != nil {
		return nil, fmt.Errorf("parsing member address: %w", err)
	}

	wrapped, err := skykey.WrapKey(ch.key, memberKey.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("wrapping key: %w", err)
	}

	cm.mu.Lock()
	ch.Members = append(ch.Members, memberAddr)
	cm.mu.Unlock()

	return wrapped, nil
}

// OnChannelMessage registers a handler for incoming channel messages.
func (cm *ChannelManager) OnChannelMessage(channelID string, handler func(from string, data []byte)) {
	cm.mu.Lock()
	cm.handlers[channelID] = handler
	cm.mu.Unlock()
}

// GetChannel returns a channel by ID.
func (cm *ChannelManager) GetChannel(id string) (*Channel, bool) {
	cm.mu.RLock()
	ch, ok := cm.channels[id]
	cm.mu.RUnlock()
	return ch, ok
}

// Channels returns all joined channels.
func (cm *ChannelManager) Channels() []*Channel {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	out := make([]*Channel, 0, len(cm.channels))
	for _, ch := range cm.channels {
		out = append(out, ch)
	}
	return out
}

func (cm *ChannelManager) subscribeChannel(ctx context.Context, ch *Channel) error {
	return cm.pubsub.Subscribe(ctx, channelTopic(ch.ID), func(from peer.ID, data []byte) {
		plain, err := skykey.Decrypt(data, ch.key)
		if err != nil {
			// Not a member or corrupted — silently drop.
			return
		}

		var msg channelMessage
		if err := json.Unmarshal(plain, &msg); err != nil {
			return
		}

		cm.mu.RLock()
		handler := cm.handlers[ch.ID]
		cm.mu.RUnlock()

		if handler != nil {
			handler(msg.From, msg.Data)
		}
	})
}

// channelTopic returns the GossipSub topic name for a channel.
// Uses the channel ID directly — it's already a random hex string.
func channelTopic(channelID string) string {
	return "skylink/ch/" + channelID
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
