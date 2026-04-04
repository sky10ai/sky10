package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/link"
)

func makeTestNode(t *testing.T) *link.Node {
	t.Helper()
	k, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	n, err := link.NewFromKey(k, link.Config{Mode: link.Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func startNode(t *testing.T, n *link.Node) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- n.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for n.Host() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n.Host() == nil {
		cancel()
		t.Fatal("node did not start")
	}
	t.Cleanup(func() {
		cancel()
		<-errCh
	})
}

func connectNodes(t *testing.T, a, b *link.Node) {
	t.Helper()
	info := b.Host().Peerstore().PeerInfo(b.PeerID())
	info.Addrs = b.Host().Addrs()
	if err := a.Host().Connect(context.Background(), info); err != nil {
		t.Fatalf("connecting nodes: %v", err)
	}
}

func TestRouterSendLocal(t *testing.T) {
	t.Parallel()

	reg := NewRegistry("D-device01", "host1", nil)
	reg.Register(RegisterParams{Name: "coder"}, "A-agent00100000000")

	var emitted []Message
	emit := func(event string, data interface{}) {
		if msg, ok := data.(Message); ok {
			emitted = append(emitted, msg)
		}
	}

	node := makeTestNode(t)
	router := NewRouter(reg, node, emit, "D-device01", nil)

	msg := Message{
		ID:        "msg-1",
		SessionID: "session-1",
		To:        "coder",
		Type:      "text",
		Content:   json.RawMessage(`{"text":"hello"}`),
	}
	result, err := router.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("local send: %v", err)
	}
	m := result.(map[string]string)
	if m["status"] != "sent" {
		t.Errorf("status = %s, want sent", m["status"])
	}
	if len(emitted) != 1 {
		t.Fatalf("emitted %d messages, want 1", len(emitted))
	}
	if emitted[0].To != "coder" {
		t.Errorf("emitted to = %s, want coder", emitted[0].To)
	}
}

func TestRouterSendRemote(t *testing.T) {
	t.Parallel()

	// Node A: sender, no local agents.
	nodeA := makeTestNode(t)
	regA := NewRegistry("D-deviceAA", "hostA", nil)

	// Node B: receiver, has agent.
	nodeB := makeTestNode(t)
	regB := NewRegistry("D-deviceBB", "hostB", nil)
	regB.Register(RegisterParams{Name: "researcher"}, "A-remote0100000000")

	var receivedOnB []Message
	emitB := func(event string, data interface{}) {
		if msg, ok := data.(Message); ok {
			receivedOnB = append(receivedOnB, msg)
		}
	}
	RegisterLinkHandlers(nodeB, regB, emitB)

	startNode(t, nodeA)
	startNode(t, nodeB)
	connectNodes(t, nodeA, nodeB)

	routerA := NewRouter(regA, nodeA, nil, "D-deviceAA", nil)
	routerA.cachePeer("D-deviceBB", nodeB.PeerID())

	msg := Message{
		ID:        "msg-remote",
		SessionID: "session-1",
		To:        "researcher",
		DeviceID:  "D-deviceBB",
		Type:      "text",
		Content:   json.RawMessage(`{"text":"search this"}`),
	}
	result, err := routerA.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("remote send: %v", err)
	}
	m := result.(map[string]string)
	if m["status"] != "sent" {
		t.Errorf("status = %s, want sent", m["status"])
	}
	if len(receivedOnB) != 1 {
		t.Fatalf("node B received %d messages, want 1", len(receivedOnB))
	}
	if receivedOnB[0].To != "researcher" {
		t.Errorf("received to = %s, want researcher", receivedOnB[0].To)
	}
}

func TestRouterListAggregation(t *testing.T) {
	t.Parallel()

	nodeA := makeTestNode(t)
	regA := NewRegistry("D-deviceAA", "hostA", nil)
	regA.Register(RegisterParams{Name: "coder", Capabilities: []string{"code"}}, "A-localA0100000000")

	nodeB := makeTestNode(t)
	regB := NewRegistry("D-deviceBB", "hostB", nil)
	regB.Register(RegisterParams{Name: "researcher", Capabilities: []string{"research"}}, "A-remoteB100000000")

	RegisterLinkHandlers(nodeB, regB, nil)

	startNode(t, nodeA)
	startNode(t, nodeB)
	connectNodes(t, nodeA, nodeB)

	routerA := NewRouter(regA, nodeA, nil, "D-deviceAA", nil)

	agents := routerA.List(context.Background())
	if len(agents) != 2 {
		t.Fatalf("List() returned %d agents, want 2", len(agents))
	}

	names := map[string]bool{}
	for _, a := range agents {
		names[a.Name] = true
	}
	if !names["coder"] || !names["researcher"] {
		t.Fatalf("expected coder and researcher, got %v", names)
	}
}

func TestRouterDiscover(t *testing.T) {
	t.Parallel()

	nodeA := makeTestNode(t)
	regA := NewRegistry("D-deviceAA", "hostA", nil)
	regA.Register(RegisterParams{Name: "coder", Capabilities: []string{"code"}}, "A-localA0100000000")

	nodeB := makeTestNode(t)
	regB := NewRegistry("D-deviceBB", "hostB", nil)
	regB.Register(RegisterParams{Name: "researcher", Capabilities: []string{"research"}}, "A-remoteB100000000")

	RegisterLinkHandlers(nodeB, regB, nil)
	startNode(t, nodeA)
	startNode(t, nodeB)
	connectNodes(t, nodeA, nodeB)

	routerA := NewRouter(regA, nodeA, nil, "D-deviceAA", nil)

	found := routerA.Discover(context.Background(), "code")
	if len(found) != 1 || found[0].Name != "coder" {
		t.Fatalf("Discover(code) = %v, want [coder]", found)
	}

	found = routerA.Discover(context.Background(), "research")
	if len(found) != 1 || found[0].Name != "researcher" {
		t.Fatalf("Discover(research) = %v, want [researcher]", found)
	}

	found = routerA.Discover(context.Background(), "missing")
	if len(found) != 0 {
		t.Fatalf("Discover(missing) = %v, want []", found)
	}
}

func TestRouterSendUnknownDevice(t *testing.T) {
	t.Parallel()

	node := makeTestNode(t)
	reg := NewRegistry("D-device01", "host1", nil)
	router := NewRouter(reg, node, nil, "D-device01", nil)

	msg := Message{
		ID:       "msg-1",
		To:       "missing",
		DeviceID: "D-unknown0",
	}
	_, err := router.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error for unknown device")
	}
}

func TestRouterListPopulatesPeerCache(t *testing.T) {
	t.Parallel()

	nodeA := makeTestNode(t)
	regA := NewRegistry("D-deviceAA", "hostA", nil)

	nodeB := makeTestNode(t)
	regB := NewRegistry("D-deviceBB", "hostB", nil)
	regB.Register(RegisterParams{Name: "researcher"}, "A-remoteB100000000")

	RegisterLinkHandlers(nodeB, regB, nil)
	startNode(t, nodeA)
	startNode(t, nodeB)
	connectNodes(t, nodeA, nodeB)

	routerA := NewRouter(regA, nodeA, nil, "D-deviceAA", nil)

	_, ok := routerA.lookupPeer("D-deviceBB")
	if ok {
		t.Fatal("peer cache should be empty before List")
	}

	routerA.List(context.Background())

	pid, ok := routerA.lookupPeer("D-deviceBB")
	if !ok {
		t.Fatal("peer cache not populated after List")
	}
	if pid != nodeB.PeerID() {
		t.Fatalf("cached peer = %s, want %s", pid, nodeB.PeerID())
	}
}
