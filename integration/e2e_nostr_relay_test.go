//go:build integration

package integration

import (
	"net"
	"net/http"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/nbd-wtf/go-nostr"
)

type testNostrRelay struct {
	server   *http.Server
	listener net.Listener
	url      string

	upgrader websocket.Upgrader

	mu      sync.RWMutex
	events  []*nostr.Event
	clients map[*testNostrRelayClient]struct{}

	closeOnce sync.Once
}

type testNostrRelayClient struct {
	relay *testNostrRelay
	conn  *websocket.Conn

	mu   sync.Mutex
	subs map[string]nostr.Filters
}

func startTestNostrRelay(t *testing.T) *testNostrRelay {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test relay: %v", err)
	}

	relay := &testNostrRelay{
		listener: listener,
		url:      "ws://" + listener.Addr().String(),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
		},
		clients: make(map[*testNostrRelayClient]struct{}),
	}
	relay.server = &http.Server{Handler: http.HandlerFunc(relay.handleHTTP)}

	go func() {
		_ = relay.server.Serve(listener)
	}()

	t.Cleanup(func() {
		relay.Close()
	})
	return relay
}

func (r *testNostrRelay) URL() string {
	return r.url
}

func (r *testNostrRelay) Close() {
	r.closeOnce.Do(func() {
		if r.server != nil {
			_ = r.server.Close()
		}

		r.mu.Lock()
		clients := make([]*testNostrRelayClient, 0, len(r.clients))
		for client := range r.clients {
			clients = append(clients, client)
		}
		r.clients = map[*testNostrRelayClient]struct{}{}
		r.mu.Unlock()

		for _, client := range clients {
			_ = client.conn.Close()
		}
	})
}

func (r *testNostrRelay) handleHTTP(w http.ResponseWriter, req *http.Request) {
	conn, err := r.upgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}

	client := &testNostrRelayClient{
		relay: r,
		conn:  conn,
		subs:  make(map[string]nostr.Filters),
	}

	r.mu.Lock()
	r.clients[client] = struct{}{}
	r.mu.Unlock()

	go client.readLoop()
}

func (r *testNostrRelay) removeClient(client *testNostrRelayClient) {
	r.mu.Lock()
	delete(r.clients, client)
	r.mu.Unlock()
}

func (r *testNostrRelay) matchingEvents(filters nostr.Filters) []*nostr.Event {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*nostr.Event, 0, len(r.events))
	seen := make(map[string]struct{}, len(r.events))
	for i := len(r.events) - 1; i >= 0; i-- {
		event := r.events[i]
		if event == nil || !matchesAnyFilter(filters, event) {
			continue
		}
		if _, ok := seen[event.ID]; ok {
			continue
		}
		seen[event.ID] = struct{}{}
		out = append(out, cloneNostrEvent(event))
	}
	return out
}

func (r *testNostrRelay) storeEvent(event *nostr.Event) []*testNostrRelayClient {
	r.mu.Lock()
	r.events = append(r.events, cloneNostrEvent(event))
	clients := make([]*testNostrRelayClient, 0, len(r.clients))
	for client := range r.clients {
		clients = append(clients, client)
	}
	r.mu.Unlock()
	return clients
}

func (c *testNostrRelayClient) readLoop() {
	defer func() {
		c.relay.removeClient(c)
		_ = c.conn.Close()
	}()

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			return
		}

		envelope := nostr.ParseMessage(string(message))
		switch env := envelope.(type) {
		case *nostr.EventEnvelope:
			c.handleEvent(env)
		case *nostr.ReqEnvelope:
			c.handleReq(env)
		case *nostr.CloseEnvelope:
			c.handleClose(env)
		}
	}
}

func (c *testNostrRelayClient) handleEvent(env *nostr.EventEnvelope) {
	if env == nil {
		return
	}

	valid, err := env.Event.CheckSignature()
	if err != nil || !valid {
		_ = c.writeEnvelope(&nostr.OKEnvelope{
			EventID: env.Event.ID,
			OK:      false,
			Reason:  "invalid signature",
		})
		return
	}

	clients := c.relay.storeEvent(&env.Event)
	_ = c.writeEnvelope(&nostr.OKEnvelope{
		EventID: env.Event.ID,
		OK:      true,
		Reason:  "",
	})

	for _, client := range clients {
		client.deliverMatching(&env.Event)
	}
}

func (c *testNostrRelayClient) handleReq(env *nostr.ReqEnvelope) {
	if env == nil {
		return
	}

	c.mu.Lock()
	c.subs[env.SubscriptionID] = append(nostr.Filters(nil), env.Filters...)
	c.mu.Unlock()

	for _, event := range c.relay.matchingEvents(env.Filters) {
		subID := env.SubscriptionID
		_ = c.writeEnvelope(&nostr.EventEnvelope{
			SubscriptionID: &subID,
			Event:          *event,
		})
	}

	eose := nostr.EOSEEnvelope(env.SubscriptionID)
	_ = c.writeEnvelope(&eose)
}

func (c *testNostrRelayClient) handleClose(env *nostr.CloseEnvelope) {
	if env == nil {
		return
	}

	c.mu.Lock()
	delete(c.subs, string(*env))
	c.mu.Unlock()
}

func (c *testNostrRelayClient) deliverMatching(event *nostr.Event) {
	if event == nil {
		return
	}

	c.mu.Lock()
	subs := make(map[string]nostr.Filters, len(c.subs))
	for subID, filters := range c.subs {
		subs[subID] = append(nostr.Filters(nil), filters...)
	}
	c.mu.Unlock()

	for subID, filters := range subs {
		if !matchesAnyFilter(filters, event) {
			continue
		}
		subID := subID
		_ = c.writeEnvelope(&nostr.EventEnvelope{
			SubscriptionID: &subID,
			Event:          *cloneNostrEvent(event),
		})
	}
}

func (c *testNostrRelayClient) writeEnvelope(envelope nostr.Envelope) error {
	if envelope == nil {
		return nil
	}

	data, err := envelope.MarshalJSON()
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

func matchesAnyFilter(filters nostr.Filters, event *nostr.Event) bool {
	for _, filter := range filters {
		if filter.Matches(event) {
			return true
		}
	}
	return false
}

func cloneNostrEvent(event *nostr.Event) *nostr.Event {
	if event == nil {
		return nil
	}
	cp := *event
	cp.Tags = append(nostr.Tags(nil), event.Tags...)
	return &cp
}
