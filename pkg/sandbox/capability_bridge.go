package sandbox

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/sky10/sky10/pkg/sandbox/bridge"
)

const sandboxBridgeReconnectDelay = 2 * time.Second

type sandboxBridgeDialer func(context.Context, Record, string) (*bridge.Conn, *http.Response, error)

type sandboxBridgeManager struct {
	name      string
	bridgeURL func(Record) (string, error)
	dialConn  sandboxBridgeDialer
	logger    *slog.Logger

	mu      sync.Mutex
	entries map[string]*sandboxBridgeEntry
}

type sandboxBridgeEntry struct {
	cancel context.CancelFunc
	conn   *bridge.Conn
}

func newSandboxBridgeManager(name string, logger *slog.Logger, bridgeURL func(Record) (string, error), dialConn sandboxBridgeDialer) *sandboxBridgeManager {
	return &sandboxBridgeManager{
		name:      strings.TrimSpace(name),
		bridgeURL: bridgeURL,
		dialConn:  dialConn,
		logger:    componentLogger(logger),
		entries:   make(map[string]*sandboxBridgeEntry),
	}
}

func (m *sandboxBridgeManager) Connect(ctx context.Context, rec Record) error {
	if m == nil || m.bridgeURL == nil || m.dialConn == nil {
		return nil
	}
	if strings.TrimSpace(rec.Slug) == "" {
		return fmt.Errorf("sandbox bridge record has empty slug")
	}
	wsURL, err := m.bridgeURL(rec)
	if err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	entry := &sandboxBridgeEntry{cancel: cancel}

	m.mu.Lock()
	if old := m.entries[rec.Slug]; old != nil {
		m.mu.Unlock()
		cancel()
		return nil
	}
	m.entries[rec.Slug] = entry
	m.mu.Unlock()

	done, err := m.dial(runCtx, ctx, rec, wsURL, entry)
	if err != nil {
		cancel()
		m.removeEntry(rec.Slug, entry)
		return err
	}
	go m.reconnectLoop(runCtx, rec, wsURL, entry, done)
	return nil
}

func (m *sandboxBridgeManager) Close(slug string) {
	slug = strings.TrimSpace(slug)
	if slug == "" || m == nil {
		return
	}
	m.mu.Lock()
	entry := m.entries[slug]
	delete(m.entries, slug)
	m.mu.Unlock()
	if entry == nil {
		return
	}
	entry.cancel()
	if entry.conn != nil {
		_ = entry.conn.Close(websocket.StatusNormalClosure, "closed")
	}
}

func (m *sandboxBridgeManager) dial(runCtx, dialCtx context.Context, rec Record, wsURL string, entry *sandboxBridgeEntry) (<-chan error, error) {
	conn, resp, err := m.dialConn(dialCtx, rec, wsURL)
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("dial %s bridge: %w", m.label(), err)
	}

	m.mu.Lock()
	if current := m.entries[rec.Slug]; current != entry {
		m.mu.Unlock()
		_ = conn.Close(websocket.StatusNormalClosure, "replaced")
		return nil, bridge.ErrClosed
	}
	entry.conn = conn
	m.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		err := conn.Run(runCtx)
		m.clearConn(rec.Slug, entry, conn)
		done <- err
		close(done)
	}()

	if m.logger != nil {
		m.logger.Info("sandbox bridge connected", "capability", m.label(), "sandbox", rec.Slug)
	}
	return done, nil
}

func (m *sandboxBridgeManager) reconnectLoop(runCtx context.Context, rec Record, wsURL string, entry *sandboxBridgeEntry, done <-chan error) {
	for {
		select {
		case <-runCtx.Done():
			return
		case err, ok := <-done:
			if !ok {
				err = bridge.ErrClosed
			}
			if runCtx.Err() != nil {
				return
			}
			if m.logger != nil {
				m.logger.Warn("sandbox bridge disconnected", "capability", m.label(), "sandbox", rec.Slug, "error", err)
			}
		}

		timer := time.NewTimer(sandboxBridgeReconnectDelay)
		select {
		case <-runCtx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		var err error
		done, err = m.dial(runCtx, runCtx, rec, wsURL, entry)
		if err != nil {
			if m.logger != nil {
				m.logger.Warn("sandbox bridge reconnect failed", "capability", m.label(), "sandbox", rec.Slug, "error", err)
			}
			continue
		}
	}
}

func (m *sandboxBridgeManager) clearConn(slug string, entry *sandboxBridgeEntry, conn *bridge.Conn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if current := m.entries[slug]; current == entry && entry.conn == conn {
		entry.conn = nil
	}
}

func (m *sandboxBridgeManager) removeEntry(slug string, entry *sandboxBridgeEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries[slug] == entry {
		delete(m.entries, slug)
	}
}

func (m *sandboxBridgeManager) label() string {
	if m == nil || strings.TrimSpace(m.name) == "" {
		return "unknown"
	}
	return m.name
}

func sandboxCapabilityBridgeURL(rec Record, endpointPath, roleQuery, role string) (string, error) {
	base := strings.TrimSpace(guestSky10RPCAddress(rec))
	if base == "" {
		return "", fmt.Errorf("guest sky10 endpoint unavailable")
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "http://" + net.JoinHostPort(base, strconv.Itoa(guestSky10Port))
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse guest sky10 endpoint: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported guest sky10 endpoint scheme %q", u.Scheme)
	}
	u.Path = endpointPath
	q := u.Query()
	q.Set(roleQuery, role)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
