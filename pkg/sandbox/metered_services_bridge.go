package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/sky10/sky10/pkg/sandbox/bridge"
	commsx402 "github.com/sky10/sky10/pkg/sandbox/comms/x402"
)

const meteredServicesBridgeReconnectDelay = 2 * time.Second

type MeteredServicesBridgeManager struct {
	backend commsx402.Backend
	logger  *slog.Logger

	mu      sync.Mutex
	entries map[string]*meteredServicesBridgeEntry
}

type meteredServicesBridgeEntry struct {
	cancel context.CancelFunc
	conn   *bridge.Conn
}

func NewMeteredServicesBridgeManager(backend commsx402.Backend, logger *slog.Logger) *MeteredServicesBridgeManager {
	return &MeteredServicesBridgeManager{
		backend: backend,
		logger:  componentLogger(logger),
		entries: make(map[string]*meteredServicesBridgeEntry),
	}
}

func (m *MeteredServicesBridgeManager) Connect(ctx context.Context, rec Record) error {
	if m == nil || m.backend == nil {
		return nil
	}
	if strings.TrimSpace(rec.Slug) == "" {
		return fmt.Errorf("sandbox bridge record has empty slug")
	}
	wsURL, err := meteredServicesBridgeURL(rec)
	if err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	entry := &meteredServicesBridgeEntry{cancel: cancel}

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

func (m *MeteredServicesBridgeManager) Close(slug string) {
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

func (m *MeteredServicesBridgeManager) dial(runCtx, dialCtx context.Context, rec Record, wsURL string, entry *meteredServicesBridgeEntry) (<-chan error, error) {
	conn, resp, err := bridge.Dial(dialCtx, wsURL, commsx402.NewBridgeHandler(m.backend, rec.Slug))
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", commsx402.EndpointPath, err)
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
		m.logger.Info("sandbox metered-services bridge connected", "sandbox", rec.Slug)
	}
	return done, nil
}

func (m *MeteredServicesBridgeManager) reconnectLoop(runCtx context.Context, rec Record, wsURL string, entry *meteredServicesBridgeEntry, done <-chan error) {
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
				m.logger.Warn("sandbox metered-services bridge disconnected", "sandbox", rec.Slug, "error", err)
			}
		}

		timer := time.NewTimer(meteredServicesBridgeReconnectDelay)
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
				m.logger.Warn("sandbox metered-services bridge reconnect failed", "sandbox", rec.Slug, "error", err)
			}
			continue
		}
	}
}

func (m *MeteredServicesBridgeManager) clearConn(slug string, entry *meteredServicesBridgeEntry, conn *bridge.Conn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if current := m.entries[slug]; current == entry && entry.conn == conn {
		entry.conn = nil
	}
}

func (m *MeteredServicesBridgeManager) removeEntry(slug string, entry *meteredServicesBridgeEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries[slug] == entry {
		delete(m.entries, slug)
	}
}

func meteredServicesBridgeURL(rec Record) (string, error) {
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
	u.Path = commsx402.EndpointPath
	q := u.Query()
	q.Set(commsx402.BridgeRoleQuery, commsx402.BridgeRoleHost)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
