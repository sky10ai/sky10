package link

import (
	"context"
	"fmt"
	"sync"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
)

// topicHandle tracks a joined GossipSub topic and its subscription.
type topicHandle struct {
	topic  *pubsub.Topic
	sub    *pubsub.Subscription
	cancel context.CancelFunc
}

// PubSub manages GossipSub topics for encrypted channels.
// This is the NETWORK layer — sync notifications use direct streams
// (NotifyOwn) and never touch GossipSub.
type PubSub struct {
	ps      *pubsub.PubSub
	localID peer.ID
	logger  interface{ Debug(string, ...any) }

	mu     sync.RWMutex
	topics map[string]*topicHandle
}

// newPubSub initializes GossipSub on the node's host.
func newPubSub(ctx context.Context, n *Node) (*PubSub, error) {
	ps, err := pubsub.NewGossipSub(ctx, n.host)
	if err != nil {
		return nil, fmt.Errorf("creating gossipsub: %w", err)
	}
	return &PubSub{
		ps:      ps,
		localID: n.peerID,
		logger:  n.logger,
		topics:  make(map[string]*topicHandle),
	}, nil
}

// Subscribe joins a GossipSub topic and calls handler for each message.
// The handler receives the sender's peer ID and the raw message data.
func (p *PubSub) Subscribe(ctx context.Context, topicName string, handler func(from peer.ID, data []byte)) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.topics[topicName]; ok {
		return fmt.Errorf("already subscribed to %q", topicName)
	}

	topic, err := p.ps.Join(topicName)
	if err != nil {
		return fmt.Errorf("joining topic %q: %w", topicName, err)
	}

	sub, err := topic.Subscribe()
	if err != nil {
		topic.Close()
		return fmt.Errorf("subscribing to %q: %w", topicName, err)
	}

	subCtx, cancel := context.WithCancel(ctx)
	p.topics[topicName] = &topicHandle{topic: topic, sub: sub, cancel: cancel}

	go p.readLoop(subCtx, sub, handler)

	return nil
}

// Publish sends data to all subscribers of a topic.
func (p *PubSub) Publish(ctx context.Context, topicName string, data []byte) error {
	p.mu.RLock()
	th, ok := p.topics[topicName]
	p.mu.RUnlock()

	if !ok {
		// Join the topic for publishing even if not subscribed.
		topic, err := p.ps.Join(topicName)
		if err != nil {
			return fmt.Errorf("joining topic %q: %w", topicName, err)
		}
		p.mu.Lock()
		th = &topicHandle{topic: topic}
		p.topics[topicName] = th
		p.mu.Unlock()
	}

	return th.topic.Publish(ctx, data)
}

// Unsubscribe leaves a topic.
func (p *PubSub) Unsubscribe(topicName string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	th, ok := p.topics[topicName]
	if !ok {
		return
	}
	if th.cancel != nil {
		th.cancel()
	}
	if th.sub != nil {
		th.sub.Cancel()
	}
	th.topic.Close()
	delete(p.topics, topicName)
}

// Subscriptions returns the names of all active subscriptions.
func (p *PubSub) Subscriptions() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, 0, len(p.topics))
	for name, th := range p.topics {
		if th.sub != nil {
			out = append(out, name)
		}
	}
	return out
}

// Close shuts down all subscriptions.
func (p *PubSub) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for name, th := range p.topics {
		if th.cancel != nil {
			th.cancel()
		}
		if th.sub != nil {
			th.sub.Cancel()
		}
		th.topic.Close()
		delete(p.topics, name)
	}
}

func (p *PubSub) readLoop(ctx context.Context, sub *pubsub.Subscription, handler func(peer.ID, []byte)) {
	for {
		msg, err := sub.Next(ctx)
		if err != nil {
			return // context cancelled or subscription closed
		}
		// Skip our own messages.
		if msg.GetFrom() == p.localID {
			continue
		}
		handler(msg.GetFrom(), msg.Data)
	}
}
