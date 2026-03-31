package link

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestRegistryRegister(t *testing.T) {
	t.Parallel()
	r := NewRegistry(nil)
	r.Register(Capability{Name: "echo", Description: "echo test"}, handlePing)

	caps := r.Capabilities()
	found := false
	for _, c := range caps {
		if c.Name == "echo" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected 'echo' in capabilities")
	}
}

func TestRegistryBuiltinPing(t *testing.T) {
	t.Parallel()
	r := NewRegistry(nil)
	caps := r.Capabilities()
	found := false
	for _, c := range caps {
		if c.Name == "ping" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected 'ping' built-in capability")
	}
}

func TestTwoNodePing(t *testing.T) {
	t.Parallel()
	n1 := generateTestNode(t)
	n2 := generateTestNode(t)
	startTestNode(t, n1)
	startTestNode(t, n2)

	// Connect n1 → n2.
	info := addrInfo(t, n2)
	if err := n1.Host().Connect(context.Background(), info); err != nil {
		t.Fatal(err)
	}

	// n1 calls ping on n2.
	result, err := n1.Call(context.Background(), n2.PeerID(), "ping", nil)
	if err != nil {
		t.Fatal(err)
	}

	var resp map[string]bool
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp["pong"] {
		t.Fatalf("expected pong=true, got %v", resp)
	}
}

func TestTwoNodeEcho(t *testing.T) {
	t.Parallel()
	n1 := generateTestNode(t)
	n2 := generateTestNode(t)
	startTestNode(t, n1)
	startTestNode(t, n2)

	// Register echo capability on n2.
	n2.RegisterCapability(
		Capability{Name: "echo", Description: "echo params back"},
		func(ctx context.Context, req *PeerRequest) (interface{}, error) {
			return json.RawMessage(req.Params), nil
		},
	)

	// Connect.
	info := addrInfo(t, n2)
	if err := n1.Host().Connect(context.Background(), info); err != nil {
		t.Fatal(err)
	}

	// Call echo.
	params := map[string]string{"message": "hello skylink"}
	result, err := n1.Call(context.Background(), n2.PeerID(), "echo", params)
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]string
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	if got["message"] != "hello skylink" {
		t.Fatalf("got %v, want 'hello skylink'", got)
	}
}

func TestTwoNodeUnknownMethod(t *testing.T) {
	t.Parallel()
	n1 := generateTestNode(t)
	n2 := generateTestNode(t)
	startTestNode(t, n1)
	startTestNode(t, n2)

	info := addrInfo(t, n2)
	if err := n1.Host().Connect(context.Background(), info); err != nil {
		t.Fatal(err)
	}

	_, err := n1.Call(context.Background(), n2.PeerID(), "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown method")
	}
}

func TestCallPeerAddress(t *testing.T) {
	t.Parallel()
	n1 := generateTestNode(t)
	n2 := generateTestNode(t)
	startTestNode(t, n1)
	startTestNode(t, n2)

	// Register a handler that returns the caller's address.
	n2.RegisterCapability(
		Capability{Name: "whoami"},
		func(ctx context.Context, req *PeerRequest) (interface{}, error) {
			return map[string]string{"address": req.Address}, nil
		},
	)

	info := addrInfo(t, n2)
	if err := n1.Host().Connect(context.Background(), info); err != nil {
		t.Fatal(err)
	}

	result, err := n1.Call(context.Background(), n2.PeerID(), "whoami", nil)
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]string
	json.Unmarshal(result, &got)
	if got["address"] != n1.Address() {
		t.Fatalf("got address %q, want %q", got["address"], n1.Address())
	}
}

func TestCallTimeout(t *testing.T) {
	t.Parallel()
	n1 := generateTestNode(t)
	n2 := generateTestNode(t)
	startTestNode(t, n1)
	startTestNode(t, n2)

	// Register a slow handler.
	n2.RegisterCapability(
		Capability{Name: "slow"},
		func(ctx context.Context, req *PeerRequest) (interface{}, error) {
			time.Sleep(5 * time.Second)
			return nil, nil
		},
	)

	info := addrInfo(t, n2)
	if err := n1.Host().Connect(context.Background(), info); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := n1.Call(ctx, n2.PeerID(), "slow", nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestCallNodeNotRunning(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	_, err := n.Call(context.Background(), "fake-peer", "ping", nil)
	if err == nil {
		t.Fatal("expected error when node not running")
	}
}
