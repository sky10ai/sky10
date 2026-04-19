package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type messageSubscriber struct {
	ch     chan Message
	filter func(Message) bool
}

// MessageHub fans out session-scoped agent messages to in-process consumers
// such as the dedicated guest chat WebSocket bridge.
type MessageHub struct {
	mu   sync.RWMutex
	next uint64
	subs map[uint64]*messageSubscriber
}

// NewMessageHub creates an empty message hub.
func NewMessageHub() *MessageHub {
	return &MessageHub{
		subs: make(map[uint64]*messageSubscriber),
	}
}

// Publish broadcasts one message to interested subscribers.
func (h *MessageHub) Publish(msg Message) {
	if h == nil {
		return
	}

	h.mu.RLock()
	subs := make([]*messageSubscriber, 0, len(h.subs))
	for _, sub := range h.subs {
		subs = append(subs, sub)
	}
	h.mu.RUnlock()

	for _, sub := range subs {
		if sub == nil {
			continue
		}
		if sub.filter != nil && !sub.filter(msg) {
			continue
		}
		select {
		case sub.ch <- msg:
			continue
		default:
		}
		t := time.NewTimer(200 * time.Millisecond)
		select {
		case sub.ch <- msg:
		case <-t.C:
		}
		t.Stop()
	}
}

// Subscribe returns a live message channel until ctx is cancelled.
func (h *MessageHub) Subscribe(ctx context.Context, filter func(Message) bool) <-chan Message {
	ch := make(chan Message, 32)
	if h == nil {
		close(ch)
		return ch
	}

	id := atomic.AddUint64(&h.next, 1)
	h.mu.Lock()
	h.subs[id] = &messageSubscriber{ch: ch, filter: filter}
	h.mu.Unlock()

	go func() {
		<-ctx.Done()
		h.mu.Lock()
		delete(h.subs, id)
		h.mu.Unlock()
		close(ch)
	}()

	return ch
}
