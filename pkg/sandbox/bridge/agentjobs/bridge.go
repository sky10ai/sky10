package agentjobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	skyagent "github.com/sky10/sky10/pkg/agent"
	"github.com/sky10/sky10/pkg/logging"
	skysandbox "github.com/sky10/sky10/pkg/sandbox"
	"github.com/sky10/sky10/pkg/sandbox/bridge"
)

const (
	EndpointPath = "/bridge/agent-jobs/ws"

	TypeUpdateStatus = "agent.job.updateStatus"
	TypeComplete     = "agent.job.complete"
	TypeFail         = "agent.job.fail"

	ErrCodeHostBridgeDisconnected = "host_bridge_disconnected"

	BridgeRoleQuery = "bridge_role"
	BridgeRoleHost  = "host"

	guestSky10Port       = 9101
	bridgeReconnectDelay = 2 * time.Second
)

// ForwardingBackend implements skyagent.JobForwarder inside a guest daemon.
// It sends job lifecycle updates over the host-opened sandbox bridge because
// host-created jobs do not exist in the guest daemon's local JobStore.
type ForwardingBackend struct {
	mu   sync.RWMutex
	conn *bridge.Conn
}

func NewForwardingBackend() *ForwardingBackend {
	return &ForwardingBackend{}
}

func HandlerWithHostBridge(forwarder *ForwardingBackend, opts ...bridge.Option) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get(BridgeRoleQuery) != BridgeRoleHost {
			http.NotFound(w, r)
			return
		}
		if forwarder == nil {
			http.Error(w, "agent-jobs bridge is not configured", http.StatusServiceUnavailable)
			return
		}
		conn, err := bridge.Accept(w, r, nil, opts...)
		if err != nil {
			return
		}
		forwarder.Attach(conn)
		defer forwarder.Detach(conn)
		defer conn.Close(websocket.StatusNormalClosure, "")
		_ = conn.Run(r.Context())
	}
}

func (b *ForwardingBackend) Attach(conn *bridge.Conn) {
	b.mu.Lock()
	old := b.conn
	b.conn = conn
	b.mu.Unlock()
	if old != nil && old != conn {
		_ = old.Close(websocket.StatusNormalClosure, "replaced")
	}
}

func (b *ForwardingBackend) Detach(conn *bridge.Conn) {
	b.mu.Lock()
	if b.conn == conn {
		b.conn = nil
	}
	b.mu.Unlock()
}

func (b *ForwardingBackend) Connected() bool {
	b.mu.RLock()
	ok := b.conn != nil
	b.mu.RUnlock()
	return ok
}

func (b *ForwardingBackend) UpdateStatus(ctx context.Context, params skyagent.AgentJobStatusParams) (*skyagent.AgentJobResult, error) {
	raw, err := b.call(ctx, TypeUpdateStatus, params)
	if err != nil {
		return nil, err
	}
	return decodeJobResult(raw)
}

func (b *ForwardingBackend) Complete(ctx context.Context, params skyagent.AgentJobCompleteParams) (*skyagent.AgentJobResult, error) {
	raw, err := b.call(ctx, TypeComplete, params)
	if err != nil {
		return nil, err
	}
	return decodeJobResult(raw)
}

func (b *ForwardingBackend) Fail(ctx context.Context, params skyagent.AgentJobFailParams) (*skyagent.AgentJobResult, error) {
	raw, err := b.call(ctx, TypeFail, params)
	if err != nil {
		return nil, err
	}
	return decodeJobResult(raw)
}

func (b *ForwardingBackend) call(ctx context.Context, typ string, payload any) (json.RawMessage, error) {
	conn := b.activeConn()
	if conn == nil {
		return nil, bridge.HandlerError(ErrCodeHostBridgeDisconnected, "agent-jobs host bridge is not connected")
	}
	raw, err := conn.Call(ctx, typ, payload)
	if err != nil {
		if errors.Is(err, bridge.ErrClosed) {
			b.Detach(conn)
		}
		return nil, err
	}
	return raw, nil
}

func (b *ForwardingBackend) activeConn() *bridge.Conn {
	b.mu.RLock()
	conn := b.conn
	b.mu.RUnlock()
	return conn
}

func decodeJobResult(raw json.RawMessage) (*skyagent.AgentJobResult, error) {
	var result skyagent.AgentJobResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

type HostBackend interface {
	UpdateStatus(ctx context.Context, agentRef string, params skyagent.AgentJobStatusParams) (*skyagent.AgentJobResult, error)
	Complete(ctx context.Context, agentRef string, params skyagent.AgentJobCompleteParams) (*skyagent.AgentJobResult, error)
	Fail(ctx context.Context, agentRef string, params skyagent.AgentJobFailParams) (*skyagent.AgentJobResult, error)
}

func NewBridgeHandler(backend HostBackend, agentRef string) bridge.Handler {
	agentRef = strings.TrimSpace(agentRef)
	return func(ctx context.Context, req bridge.Request) (json.RawMessage, error) {
		if backend == nil {
			return nil, bridge.HandlerError("backend_unavailable", "agent-jobs backend is not configured")
		}
		switch req.Type {
		case TypeUpdateStatus:
			var params skyagent.AgentJobStatusParams
			if err := json.Unmarshal(req.Payload, &params); err != nil {
				return nil, bridge.HandlerError("invalid_payload", err.Error())
			}
			if strings.TrimSpace(params.JobID) == "" {
				return nil, bridge.HandlerError("invalid_payload", "job_id is required")
			}
			result, err := backend.UpdateStatus(ctx, agentRef, params)
			return marshalJobResult(result, err)
		case TypeComplete:
			var params skyagent.AgentJobCompleteParams
			if err := json.Unmarshal(req.Payload, &params); err != nil {
				return nil, bridge.HandlerError("invalid_payload", err.Error())
			}
			if strings.TrimSpace(params.JobID) == "" {
				return nil, bridge.HandlerError("invalid_payload", "job_id is required")
			}
			result, err := backend.Complete(ctx, agentRef, params)
			return marshalJobResult(result, err)
		case TypeFail:
			var params skyagent.AgentJobFailParams
			if err := json.Unmarshal(req.Payload, &params); err != nil {
				return nil, bridge.HandlerError("invalid_payload", err.Error())
			}
			if strings.TrimSpace(params.JobID) == "" {
				return nil, bridge.HandlerError("invalid_payload", "job_id is required")
			}
			result, err := backend.Fail(ctx, agentRef, params)
			return marshalJobResult(result, err)
		default:
			return nil, bridge.HandlerError("type_unregistered", "unregistered agent-jobs bridge type")
		}
	}
}

func marshalJobResult(result *skyagent.AgentJobResult, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal agent job result: %w", err)
	}
	return raw, nil
}

type BridgeManager struct {
	backend HostBackend
	logger  *slog.Logger

	mu      sync.Mutex
	entries map[string]*bridgeEntry
}

type bridgeEntry struct {
	cancel context.CancelFunc
	conn   *bridge.Conn
}

func NewBridgeManager(backend HostBackend, logger *slog.Logger) *BridgeManager {
	return &BridgeManager{
		backend: backend,
		logger:  logging.WithComponent(logger, "sandbox.agent_jobs_bridge"),
		entries: make(map[string]*bridgeEntry),
	}
}

func (m *BridgeManager) Connect(ctx context.Context, rec skysandbox.Record) error {
	if m == nil || m.backend == nil {
		return nil
	}
	if strings.TrimSpace(rec.Slug) == "" {
		return fmt.Errorf("sandbox bridge record has empty slug")
	}
	wsURL, err := BridgeURL(rec)
	if err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	entry := &bridgeEntry{cancel: cancel}

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

func (m *BridgeManager) Close(slug string) {
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

func (m *BridgeManager) dial(runCtx, dialCtx context.Context, rec skysandbox.Record, wsURL string, entry *bridgeEntry) (<-chan error, error) {
	conn, resp, err := bridge.Dial(dialCtx, wsURL, NewBridgeHandler(m.backend, rec.Slug))
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", EndpointPath, err)
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
		m.logger.Info("sandbox agent-jobs bridge connected", "sandbox", rec.Slug)
	}
	return done, nil
}

func (m *BridgeManager) reconnectLoop(runCtx context.Context, rec skysandbox.Record, wsURL string, entry *bridgeEntry, done <-chan error) {
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
				m.logger.Warn("sandbox agent-jobs bridge disconnected", "sandbox", rec.Slug, "error", err)
			}
		}

		timer := time.NewTimer(bridgeReconnectDelay)
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
				m.logger.Warn("sandbox agent-jobs bridge reconnect failed", "sandbox", rec.Slug, "error", err)
			}
			continue
		}
	}
}

func (m *BridgeManager) clearConn(slug string, entry *bridgeEntry, conn *bridge.Conn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if current := m.entries[slug]; current == entry && entry.conn == conn {
		entry.conn = nil
	}
}

func (m *BridgeManager) removeEntry(slug string, entry *bridgeEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries[slug] == entry {
		delete(m.entries, slug)
	}
}

func BridgeURL(rec skysandbox.Record) (string, error) {
	base := strings.TrimSpace(guestSky10BaseURL(rec))
	if base == "" {
		return "", fmt.Errorf("guest sky10 endpoint unavailable")
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
	u.Path = EndpointPath
	q := u.Query()
	q.Set(BridgeRoleQuery, BridgeRoleHost)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func guestSky10BaseURL(rec skysandbox.Record) string {
	for _, endpoint := range rec.ForwardedEndpoints {
		if endpoint.Name != skysandbox.ForwardedEndpointSky10 || endpoint.HostPort <= 0 {
			continue
		}
		host := strings.TrimSpace(endpoint.Host)
		if host == "" {
			host = "127.0.0.1"
		}
		return "http://" + net.JoinHostPort(host, strconv.Itoa(endpoint.HostPort))
	}
	if rec.ForwardedPort > 0 {
		host := strings.TrimSpace(rec.ForwardedHost)
		if host == "" {
			host = "127.0.0.1"
		}
		return "http://" + net.JoinHostPort(host, strconv.Itoa(rec.ForwardedPort))
	}
	if ip := strings.TrimSpace(rec.IPAddress); ip != "" {
		return "http://" + net.JoinHostPort(ip, strconv.Itoa(guestSky10Port))
	}
	return ""
}
