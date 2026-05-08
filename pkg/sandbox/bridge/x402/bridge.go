package x402

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"github.com/sky10/sky10/pkg/sandbox/bridge"
)

const (
	TypeListServices = "x402.list_services"
	TypeServiceCall  = "x402.service_call"
	TypeBudgetStatus = "x402.budget_status"

	ErrCodeHostBridgeDisconnected = "host_bridge_disconnected"

	BridgeRoleQuery = "bridge_role"
	BridgeRoleHost  = "host"
)

// ForwardingBackend implements Backend inside the guest. The bridge
// endpoint still validates and stamps local runtime calls; this backend only
// forwards already-validated x402 requests over the host-opened bridge socket.
type ForwardingBackend struct {
	mu   sync.RWMutex
	conn *bridge.Conn
}

func NewForwardingBackend() *ForwardingBackend {
	return &ForwardingBackend{}
}

// HandlerWithHostBridge wraps the normal agent-facing bridge handler with the
// host-upstream attachment path. Normal runtime callers use the same endpoint
// without bridge_role=host; the host daemon dials the same capability endpoint
// with bridge_role=host and holds that socket as the forwarding upstream.
func HandlerWithHostBridge(local http.HandlerFunc, forwarder *ForwardingBackend, opts ...bridge.Option) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get(BridgeRoleQuery) != BridgeRoleHost {
			local(w, r)
			return
		}
		if forwarder == nil {
			http.Error(w, "metered-services bridge is not configured", http.StatusServiceUnavailable)
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
		_ = old.Close(1000, "replaced")
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

func (b *ForwardingBackend) ListServices(ctx context.Context, _ string) ([]ServiceListing, error) {
	raw, err := b.call(ctx, TypeListServices, nil)
	if err != nil {
		return nil, err
	}
	var result listServicesResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result.Services, nil
}

func (b *ForwardingBackend) BudgetStatus(ctx context.Context, _ string) (*BudgetSnapshot, error) {
	raw, err := b.call(ctx, TypeBudgetStatus, nil)
	if err != nil {
		return nil, err
	}
	var snapshot BudgetSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (b *ForwardingBackend) Call(ctx context.Context, params CallParams) (*CallResult, error) {
	raw, err := b.call(ctx, TypeServiceCall, serviceCallParams{
		ServiceID:    params.ServiceID,
		Path:         params.Path,
		Method:       params.Method,
		Body:         params.Body,
		Headers:      params.Headers,
		MaxPriceUSDC: params.MaxPriceUSDC,
		PaymentNonce: params.PaymentNonce,
	})
	if err != nil {
		return nil, err
	}
	var result CallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (b *ForwardingBackend) call(ctx context.Context, typ string, payload any) (json.RawMessage, error) {
	conn := b.activeConn()
	if conn == nil {
		return nil, bridge.HandlerError(ErrCodeHostBridgeDisconnected, "metered-services host bridge is not connected")
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
	defer b.mu.RUnlock()
	return b.conn
}

// PreferForwardingBackend uses the guest forwarding backend when a host
// upstream is attached and otherwise falls back to a local backend. This lets
// host daemons keep serving the same x402 endpoint locally while guest daemons
// switch to the host-owned bridge as soon as the host connects.
type PreferForwardingBackend struct {
	Forwarder *ForwardingBackend
	Local     Backend
}

func (b PreferForwardingBackend) ListServices(ctx context.Context, agentID string) ([]ServiceListing, error) {
	return b.backend().ListServices(ctx, agentID)
}

func (b PreferForwardingBackend) BudgetStatus(ctx context.Context, agentID string) (*BudgetSnapshot, error) {
	return b.backend().BudgetStatus(ctx, agentID)
}

func (b PreferForwardingBackend) Call(ctx context.Context, params CallParams) (*CallResult, error) {
	return b.backend().Call(ctx, params)
}

func (b PreferForwardingBackend) backend() Backend {
	if b.Forwarder != nil && b.Forwarder.Connected() {
		return b.Forwarder
	}
	return b.Local
}

// NewBridgeHandler returns the host-side handler for requests forwarded over a
// host-owned sandbox bridge connection. agentID is trusted host state derived
// from the sandbox record or host agent registry; request payload identity is
// ignored.
func NewBridgeHandler(backend Backend, agentID string) bridge.Handler {
	trustedAgentID := strings.TrimSpace(agentID)
	return func(ctx context.Context, req bridge.Request) (json.RawMessage, error) {
		if backend == nil {
			return nil, bridge.HandlerError("backend_unavailable", "metered-services backend is not configured")
		}
		if trustedAgentID == "" {
			return nil, bridge.HandlerError("agent_unavailable", "trusted agent identity is not configured")
		}
		switch req.Type {
		case TypeListServices:
			services, err := backend.ListServices(ctx, trustedAgentID)
			if err != nil {
				return nil, err
			}
			return json.Marshal(listServicesResult{Services: services})
		case TypeBudgetStatus:
			snapshot, err := backend.BudgetStatus(ctx, trustedAgentID)
			if err != nil {
				return nil, err
			}
			return json.Marshal(snapshot)
		case TypeServiceCall:
			params, err := parseServiceCallParams(req.Payload)
			if err != nil {
				return nil, bridge.HandlerError("invalid_payload", err.Error())
			}
			if err := validateServiceCallParams(params); err != nil {
				return nil, bridge.HandlerError("invalid_payload", err.Error())
			}
			result, err := backend.Call(ctx, CallParams{
				AgentID:      trustedAgentID,
				ServiceID:    params.ServiceID,
				Path:         params.Path,
				Method:       params.Method,
				Body:         params.Body,
				Headers:      params.Headers,
				MaxPriceUSDC: params.MaxPriceUSDC,
				PaymentNonce: params.PaymentNonce,
			})
			if err != nil {
				return nil, err
			}
			return json.Marshal(result)
		default:
			return nil, bridge.HandlerError("type_unregistered", "unregistered metered-services bridge type")
		}
	}
}
