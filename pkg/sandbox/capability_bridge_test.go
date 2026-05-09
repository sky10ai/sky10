package sandbox

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/sandbox/bridge"
)

func TestSandboxBridgeReconnectLoopRetriesAfterFailedRedial(t *testing.T) {
	attempts := make(chan struct{}, 3)
	m := newSandboxBridgeManager(
		"messengers",
		nil,
		func(Record) (string, error) { return "ws://127.0.0.1:39101/bridge/messengers/ws", nil },
		func(context.Context, Record, string) (*bridge.Conn, *http.Response, error) {
			attempts <- struct{}{}
			return nil, nil, errors.New("connection refused")
		},
	)
	m.reconnectDelay = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := Record{Slug: "custom-agent"}
	entry := &sandboxBridgeEntry{cancel: cancel}
	m.entries[rec.Slug] = entry

	done := make(chan error)
	close(done)
	go m.reconnectLoop(ctx, rec, "ws://127.0.0.1:39101/bridge/messengers/ws", entry, done)

	for i := 0; i < 2; i++ {
		select {
		case <-attempts:
		case <-time.After(time.Second):
			t.Fatalf("reconnect attempts = %d, want at least 2", i)
		}
	}
}
