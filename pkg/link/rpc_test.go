package link

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRPCDispatchPrefix(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	h := NewRPCHandler(n, nil)

	_, _, handled := h.Dispatch(context.Background(), "skyfs.list", nil)
	if handled {
		t.Fatal("should not handle skyfs.* methods")
	}

	_, _, handled = h.Dispatch(context.Background(), "skykv.get", nil)
	if handled {
		t.Fatal("should not handle skykv.* methods")
	}
}

func TestRPCStatus(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	startTestNode(t, n)
	h := NewRPCHandler(n, nil)

	result, err, handled := h.Dispatch(context.Background(), "skylink.status", nil)
	if !handled {
		t.Fatal("should handle skylink.status")
	}
	if err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(result)
	var status statusResult
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatal(err)
	}
	if status.PeerID == "" {
		t.Fatal("expected non-empty peer_id")
	}
	if status.Address == "" {
		t.Fatal("expected non-empty address")
	}
	if status.Mode != "private" {
		t.Fatalf("expected mode 'private', got %q", status.Mode)
	}
	if len(status.Addrs) == 0 {
		t.Fatal("expected at least one listen address")
	}
}

func TestRPCPeers(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	startTestNode(t, n)
	h := NewRPCHandler(n, nil)

	result, err, handled := h.Dispatch(context.Background(), "skylink.peers", nil)
	if !handled {
		t.Fatal("should handle skylink.peers")
	}
	if err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(result)
	var peers peersResult
	if err := json.Unmarshal(data, &peers); err != nil {
		t.Fatal(err)
	}
	if peers.Count != 0 {
		t.Fatalf("expected 0 peers, got %d", peers.Count)
	}
}

func TestRPCNetcheck(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	startTestNode(t, n)

	server := startTestSTUNServer(t, 0, nil)
	h := NewRPCHandler(n, nil, WithSTUNServers([]string{server}))

	result, err, handled := h.Dispatch(context.Background(), "skylink.netcheck", nil)
	if !handled {
		t.Fatal("should handle skylink.netcheck")
	}
	if err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(result)
	var status NetcheckResult
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatal(err)
	}
	if !status.UDP {
		t.Fatal("expected UDP reachability")
	}
	if status.PublicAddr == "" {
		t.Fatal("expected public_addr")
	}
}

func TestRPCUnknownMethod(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	h := NewRPCHandler(n, nil)

	_, err, handled := h.Dispatch(context.Background(), "skylink.bogus", nil)
	if !handled {
		t.Fatal("should handle skylink.* even if unknown")
	}
	if err == nil {
		t.Fatal("expected error for unknown method")
	}
}
