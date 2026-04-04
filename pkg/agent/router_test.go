package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func testAgentServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params,omitempty"`
			ID     int64           `json:"id"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		resp := map[string]interface{}{"jsonrpc": "2.0", "id": req.ID}
		switch req.Method {
		case "ping":
			resp["result"] = map[string]string{"status": "ok"}
		case "search":
			resp["result"] = map[string]string{"answer": "42"}
		default:
			resp["error"] = map[string]interface{}{"code": -32601, "message": "unknown: " + req.Method}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRouterLocalCall(t *testing.T) {
	t.Parallel()

	agent := testAgentServer(t)
	reg := NewRegistry("D-device01", "host1", nil)
	reg.Register(RegisterParams{
		Name:     "coder",
		Endpoint: agent.URL,
		Methods:  []MethodSpec{{Name: "search"}},
	}, "A-agent001")

	node := makeTestNode(t)
	router := NewRouter(reg, NewCaller(), node, "D-device01", nil)

	result, err := router.Call(context.Background(), CallParams{
		Agent:  "coder",
		Method: "search",
	})
	if err != nil {
		t.Fatalf("local call: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("call error: %s", result.Error)
	}
	if result.Result == nil {
		t.Fatal("result is nil")
	}
}

func TestRouterLocalCallSelfDeviceID(t *testing.T) {
	t.Parallel()

	agent := testAgentServer(t)
	reg := NewRegistry("D-device01", "host1", nil)
	reg.Register(RegisterParams{
		Name:     "coder",
		Endpoint: agent.URL,
	}, "A-agent001")

	node := makeTestNode(t)
	router := NewRouter(reg, NewCaller(), node, "D-device01", nil)

	// Explicit device_id matching self should still route locally.
	result, err := router.Call(context.Background(), CallParams{
		Agent:    "coder",
		DeviceID: "D-device01",
		Method:   "search",
	})
	if err != nil {
		t.Fatalf("local call with self device_id: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("call error: %s", result.Error)
	}
}

func TestRouterRemoteCall(t *testing.T) {
	t.Parallel()

	// Node A: no local agents, will call remote.
	nodeA := makeTestNode(t)
	regA := NewRegistry("D-deviceAA", "hostA", nil)

	// Node B: has a local agent.
	nodeB := makeTestNode(t)
	regB := NewRegistry("D-deviceBB", "hostB", nil)
	agentSrv := testAgentServer(t)
	regB.Register(RegisterParams{
		Name:         "researcher",
		Endpoint:     agentSrv.URL,
		Capabilities: []string{"research"},
		Methods:      []MethodSpec{{Name: "search"}},
	}, "A-remote01")

	// Register link handlers on node B so it can serve agent.call.
	callerB := NewCaller()
	RegisterLinkHandlers(nodeB, regB, callerB)

	startNode(t, nodeA)
	startNode(t, nodeB)
	connectNodes(t, nodeA, nodeB)

	// Set up router on A, manually cache the peer mapping.
	routerA := NewRouter(regA, NewCaller(), nodeA, "D-deviceAA", nil)
	routerA.cachePeer("D-deviceBB", nodeB.PeerID())

	result, err := routerA.Call(context.Background(), CallParams{
		Agent:    "researcher",
		DeviceID: "D-deviceBB",
		Method:   "search",
	})
	if err != nil {
		t.Fatalf("remote call: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("call error: %s", result.Error)
	}

	var got map[string]string
	json.Unmarshal(result.Result, &got)
	if got["answer"] != "42" {
		t.Fatalf("got %v, want answer=42", got)
	}
}

func TestRouterListAggregation(t *testing.T) {
	t.Parallel()

	// Node A: one local agent.
	nodeA := makeTestNode(t)
	regA := NewRegistry("D-deviceAA", "hostA", nil)
	agentA := testAgentServer(t)
	regA.Register(RegisterParams{
		Name:         "coder",
		Endpoint:     agentA.URL,
		Capabilities: []string{"code"},
	}, "A-localA01")

	// Node B: one local agent.
	nodeB := makeTestNode(t)
	regB := NewRegistry("D-deviceBB", "hostB", nil)
	agentB := testAgentServer(t)
	regB.Register(RegisterParams{
		Name:         "researcher",
		Endpoint:     agentB.URL,
		Capabilities: []string{"research"},
	}, "A-remoteB1")

	RegisterLinkHandlers(nodeB, regB, NewCaller())

	startNode(t, nodeA)
	startNode(t, nodeB)
	connectNodes(t, nodeA, nodeB)

	routerA := NewRouter(regA, NewCaller(), nodeA, "D-deviceAA", nil)

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
	agentA := testAgentServer(t)
	regA.Register(RegisterParams{
		Name:         "coder",
		Endpoint:     agentA.URL,
		Capabilities: []string{"code"},
	}, "A-localA01")

	nodeB := makeTestNode(t)
	regB := NewRegistry("D-deviceBB", "hostB", nil)
	agentB := testAgentServer(t)
	regB.Register(RegisterParams{
		Name:         "researcher",
		Endpoint:     agentB.URL,
		Capabilities: []string{"research"},
	}, "A-remoteB1")

	RegisterLinkHandlers(nodeB, regB, NewCaller())
	startNode(t, nodeA)
	startNode(t, nodeB)
	connectNodes(t, nodeA, nodeB)

	routerA := NewRouter(regA, NewCaller(), nodeA, "D-deviceAA", nil)

	// Discover "code" → only coder.
	found := routerA.Discover(context.Background(), "code")
	if len(found) != 1 || found[0].Name != "coder" {
		t.Fatalf("Discover(code) = %v, want [coder]", found)
	}

	// Discover "research" → only researcher.
	found = routerA.Discover(context.Background(), "research")
	if len(found) != 1 || found[0].Name != "researcher" {
		t.Fatalf("Discover(research) = %v, want [researcher]", found)
	}

	// Discover "missing" → empty.
	found = routerA.Discover(context.Background(), "missing")
	if len(found) != 0 {
		t.Fatalf("Discover(missing) = %v, want []", found)
	}
}

func TestRouterRemoteCallUnknownDevice(t *testing.T) {
	t.Parallel()

	node := makeTestNode(t)
	reg := NewRegistry("D-device01", "host1", nil)
	router := NewRouter(reg, NewCaller(), node, "D-device01", nil)

	_, err := router.Call(context.Background(), CallParams{
		Agent:    "missing",
		DeviceID: "D-unknown0",
		Method:   "search",
	})
	if err == nil {
		t.Fatal("expected error for unknown device")
	}
}

func TestRouterRemoteCallAgentNotFound(t *testing.T) {
	t.Parallel()

	nodeA := makeTestNode(t)
	regA := NewRegistry("D-deviceAA", "hostA", nil)

	nodeB := makeTestNode(t)
	regB := NewRegistry("D-deviceBB", "hostB", nil)
	// Node B has no agents registered.
	RegisterLinkHandlers(nodeB, regB, NewCaller())

	startNode(t, nodeA)
	startNode(t, nodeB)
	connectNodes(t, nodeA, nodeB)

	routerA := NewRouter(regA, NewCaller(), nodeA, "D-deviceAA", nil)
	routerA.cachePeer("D-deviceBB", nodeB.PeerID())

	result, err := routerA.Call(context.Background(), CallParams{
		Agent:    "missing",
		DeviceID: "D-deviceBB",
		Method:   "search",
	})
	if err == nil && result != nil && result.Error == "" {
		fmt.Printf("result: %+v\n", result)
		t.Fatal("expected error for missing agent on remote device")
	}
}

func TestRouterListPopulatesPeerCache(t *testing.T) {
	t.Parallel()

	nodeA := makeTestNode(t)
	regA := NewRegistry("D-deviceAA", "hostA", nil)

	nodeB := makeTestNode(t)
	regB := NewRegistry("D-deviceBB", "hostB", nil)
	agentB := testAgentServer(t)
	regB.Register(RegisterParams{
		Name:     "researcher",
		Endpoint: agentB.URL,
	}, "A-remoteB1")

	RegisterLinkHandlers(nodeB, regB, NewCaller())
	startNode(t, nodeA)
	startNode(t, nodeB)
	connectNodes(t, nodeA, nodeB)

	routerA := NewRouter(regA, NewCaller(), nodeA, "D-deviceAA", nil)

	// Before List, peer cache is empty.
	_, ok := routerA.lookupPeer("D-deviceBB")
	if ok {
		t.Fatal("peer cache should be empty before List")
	}

	// List populates the cache.
	routerA.List(context.Background())

	pid, ok := routerA.lookupPeer("D-deviceBB")
	if !ok {
		t.Fatal("peer cache not populated after List")
	}
	if pid != nodeB.PeerID() {
		t.Fatalf("cached peer = %s, want %s", pid, nodeB.PeerID())
	}
}
