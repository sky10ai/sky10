package link

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	skykey "github.com/sky10/sky10/pkg/key"
)

func connectNodes(t *testing.T, n1, n2 *Node) {
	t.Helper()
	info := addrInfo(t, n2)
	if err := n1.Host().Connect(context.Background(), info); err != nil {
		t.Fatalf("connect: %v", err)
	}
}

func TestCreateChannel(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	startTestNode(t, n)

	cm := n.ChannelManager()
	ch, err := cm.CreateChannel(context.Background(), "general")
	if err != nil {
		t.Fatal(err)
	}
	if ch.ID == "" {
		t.Fatal("expected non-empty channel ID")
	}
	if ch.Name != "general" {
		t.Fatalf("expected name 'general', got %q", ch.Name)
	}
	if len(ch.Members) != 1 || ch.Members[0] != n.Address() {
		t.Fatalf("expected self as only member, got %v", ch.Members)
	}
}

func TestChannelSendReceive(t *testing.T) {
	t.Parallel()
	n1 := generateTestNode(t)
	n2 := generateTestNode(t)
	startTestNode(t, n1)
	startTestNode(t, n2)
	connectNodes(t, n1, n2)

	// n1 creates a channel.
	cm1 := n1.ChannelManager()
	ch, err := cm1.CreateChannel(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	// n1 invites n2.
	wrapped, err := cm1.InviteToChannel(ch.ID, n2.Address())
	if err != nil {
		t.Fatal(err)
	}

	// n2 joins the channel.
	cm2 := n2.ChannelManager()
	_, err = cm2.JoinChannel(context.Background(), ch.ID, wrapped)
	if err != nil {
		t.Fatal(err)
	}

	// n2 registers a message handler.
	var received atomic.Value
	done := make(chan struct{})
	cm2.OnChannelMessage(ch.ID, func(from string, data []byte) {
		received.Store(string(data))
		close(done)
	})

	// GossipSub mesh formation needs 2-3 heartbeats (1s each).
	time.Sleep(3 * time.Second)

	// n1 sends a message.
	msg, _ := json.Marshal("hello from n1")
	if err := cm1.SendToChannel(context.Background(), ch.ID, msg); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for channel message")
	}

	got := received.Load().(string)
	if got != `"hello from n1"` {
		t.Fatalf("got %q, want %q", got, `"hello from n1"`)
	}
}

func TestChannelNonMemberCantDecrypt(t *testing.T) {
	t.Parallel()
	n1 := generateTestNode(t)
	n2 := generateTestNode(t)
	n3 := generateTestNode(t)
	startTestNode(t, n1)
	startTestNode(t, n2)
	startTestNode(t, n3)
	connectNodes(t, n1, n2)
	connectNodes(t, n1, n3)
	connectNodes(t, n2, n3)

	// n1 creates a channel, invites n2 only.
	cm1 := n1.ChannelManager()
	ch, err := cm1.CreateChannel(context.Background(), "private")
	if err != nil {
		t.Fatal(err)
	}

	wrapped, err := cm1.InviteToChannel(ch.ID, n2.Address())
	if err != nil {
		t.Fatal(err)
	}

	cm2 := n2.ChannelManager()
	_, err = cm2.JoinChannel(context.Background(), ch.ID, wrapped)
	if err != nil {
		t.Fatal(err)
	}

	// n3 tries to join with a WRONG key (generate a random one).
	fakeKey, _ := skykey.GenerateSymmetricKey()
	fakeWrapped, _ := skykey.WrapKey(fakeKey, n3.identity.PublicKey)
	cm3 := n3.ChannelManager()
	_, err = cm3.JoinChannel(context.Background(), ch.ID, fakeWrapped)
	if err != nil {
		t.Fatal(err) // Join itself works (key unwraps fine), but decryption will fail
	}

	// n3 handler should never fire (wrong key → decrypt fails).
	n3received := make(chan struct{}, 1)
	cm3.OnChannelMessage(ch.ID, func(from string, data []byte) {
		n3received <- struct{}{}
	})

	// n2 handler should fire.
	n2received := make(chan string, 1)
	cm2.OnChannelMessage(ch.ID, func(from string, data []byte) {
		n2received <- string(data)
	})

	// GossipSub mesh formation needs 2-3 heartbeats.
	time.Sleep(3 * time.Second)

	msg, _ := json.Marshal("secret")
	cm1.SendToChannel(context.Background(), ch.ID, msg)

	select {
	case <-n2received:
		// Good — n2 got the message.
	case <-time.After(5 * time.Second):
		t.Fatal("n2 didn't receive message")
	}

	// n3 should NOT have received anything.
	select {
	case <-n3received:
		t.Fatal("n3 should not have decrypted the message")
	case <-time.After(500 * time.Millisecond):
		// Good — n3 didn't get it.
	}
}

func TestChannelInviteNotFound(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	startTestNode(t, n)

	cm := n.ChannelManager()
	_, err := cm.InviteToChannel("nonexistent", n.Address())
	if err == nil {
		t.Fatal("expected error for nonexistent channel")
	}
}

func TestChannelSendNotFound(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	startTestNode(t, n)

	cm := n.ChannelManager()
	err := cm.SendToChannel(context.Background(), "nonexistent", []byte("hi"))
	if err == nil {
		t.Fatal("expected error for nonexistent channel")
	}
}

func TestChannelsList(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	startTestNode(t, n)

	cm := n.ChannelManager()
	cm.CreateChannel(context.Background(), "a")
	cm.CreateChannel(context.Background(), "b")

	channels := cm.Channels()
	if len(channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(channels))
	}
}
