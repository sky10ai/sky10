package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	skyagent "github.com/sky10/sky10/pkg/agent"
	skyconfig "github.com/sky10/sky10/pkg/config"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
	"github.com/sky10/sky10/pkg/sandbox/comms"
	commsx402 "github.com/sky10/sky10/pkg/sandbox/comms/x402"
	"github.com/sky10/sky10/pkg/x402"
	"github.com/sky10/sky10/pkg/x402/discovery"
)

// installX402Endpoint mounts /comms/x402/ws on the daemon's RPC
// server, backed by an in-memory x402 service registry persisted to
// a JSON file under the sky10 root directory. Identity for incoming
// connections is resolved against the agent registry: the URL must
// carry an `agent` query parameter naming a registered agent.
//
// M1 wiring uses pkg/x402's StubSigner — calls that would actually
// charge fail with ErrSignerNotConfigured rather than charging an
// unconfigured wallet. Real OWS-backed signing follows once OWS
// exposes a sign-only command.
func installX402Endpoint(ctx context.Context, server *skyrpc.Server, agentRegistry *skyagent.Registry, logger *slog.Logger) error {
	if server == nil {
		return errors.New("x402: nil rpc server")
	}
	if agentRegistry == nil {
		return errors.New("x402: nil agent registry")
	}

	registryPath, err := x402RegistryPath()
	if err != nil {
		return fmt.Errorf("registry path: %w", err)
	}
	registry, err := x402.NewRegistry(x402.NewFileRegistryStore(registryPath), nil)
	if err != nil {
		return fmt.Errorf("new registry: %w", err)
	}

	if err := seedX402Registry(ctx, registry, logger); err != nil {
		// Seeding is best-effort: a network or overlay parse error
		// should not prevent the endpoint from coming up. Log and
		// continue with whatever the persisted registry already had.
		logger.Warn("x402 registry seed failed; continuing with persisted state", "error", err)
	}

	budget := x402.NewBudget(nil)
	transport := x402.NewTransport(x402.NewStubSigner("OWS x402 sign-only command not yet wired"))
	backend := x402.NewBackend(x402.BackendOptions{
		Registry:  registry,
		Transport: transport,
		Budget:    budget,
	})

	adapter := newX402Adapter(backend, budget, defaultX402BudgetConfig())
	resolver := newAgentIdentityResolver(agentRegistry)

	endpoint := commsx402.NewEndpoint(adapter, resolver, comms.WithLogger(logger))
	server.HandleHTTP("GET "+commsx402.EndpointPath, endpoint.Handler())
	return nil
}

// seedX402Registry runs one Refresh against the curated builtin
// source so the registry has at least the primitive set after a
// fresh daemon start. Future work adds periodic refresh and live
// agentic.market integration; until then the daemon falls back to
// the embedded primitive list on every startup.
func seedX402Registry(ctx context.Context, registry *x402.Registry, logger *slog.Logger) error {
	overlay, err := discovery.LoadOverlay()
	if err != nil {
		return fmt.Errorf("load overlay: %w", err)
	}
	source := discovery.NewStaticSource("builtin-primitives", discovery.BuiltinPrimitives())
	result, err := discovery.Refresh(ctx, registry, overlay, []discovery.Source{source}, logger)
	if err != nil {
		return fmt.Errorf("seed refresh: %w", err)
	}
	logger.Info("x402 registry seeded",
		"applied", len(result.Applied),
		"queued", len(result.Queued),
		"removed", len(result.Removed),
		"errors", len(result.Errors),
	)
	return nil
}

func x402RegistryPath() (string, error) {
	root, err := skyconfig.RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "x402", "registry.json"), nil
}

// defaultX402BudgetConfig is the per-agent default applied lazily on
// first sight in the adapter. Production budgets get configured via
// settings UI / RPC in a follow-up.
func defaultX402BudgetConfig() x402.BudgetConfig {
	return x402.BudgetConfig{
		PerCallMaxUSDC: "0.10",
		DailyCapUSDC:   "5.00",
	}
}

// newAgentIdentityResolver returns a comms.IdentityResolver that
// reads the `agent` query parameter and resolves it against the
// agent registry. Mirrors the chat websocket's path-based agent
// resolution but uses a query parameter so we can mount the comms
// endpoint at the package's own canonical path.
func newAgentIdentityResolver(reg *skyagent.Registry) comms.IdentityResolver {
	return func(r *http.Request) (string, string, error) {
		name := strings.TrimSpace(r.URL.Query().Get("agent"))
		if name == "" {
			return "", "", fmt.Errorf("%w: missing agent query parameter", comms.ErrUnauthenticated)
		}
		info := reg.Resolve(name)
		if info == nil {
			return "", "", fmt.Errorf("%w: agent %q not registered", comms.ErrUnauthenticated, name)
		}
		return info.ID, info.DeviceID, nil
	}
}

// x402Adapter implements pkg/sandbox/comms/x402.Backend by translating
// each method's payload between the comms-side wire shapes and the
// pkg/x402 native types. Lazy budget configuration: any agent that
// hits the endpoint without an explicit budget gets the daemon
// default applied on first call.
type x402Adapter struct {
	backend       *x402.Backend
	budget        *x402.Budget
	defaultBudget x402.BudgetConfig

	mu       sync.Mutex
	enrolled map[string]bool
}

func newX402Adapter(backend *x402.Backend, budget *x402.Budget, defaults x402.BudgetConfig) *x402Adapter {
	return &x402Adapter{
		backend:       backend,
		budget:        budget,
		defaultBudget: defaults,
		enrolled:      make(map[string]bool),
	}
}

// ListServices satisfies commsx402.Backend.
func (a *x402Adapter) ListServices(ctx context.Context, agentID string) ([]commsx402.ServiceListing, error) {
	listings, err := a.backend.ListServices(ctx, agentID)
	if err != nil {
		return nil, err
	}
	out := make([]commsx402.ServiceListing, 0, len(listings))
	for _, l := range listings {
		out = append(out, commsx402.ServiceListing{
			ID:          l.ID,
			DisplayName: l.DisplayName,
			Category:    l.Category,
			Tier:        string(l.Tier),
			PriceUSDC:   l.PriceUSDC,
			Hint:        l.Hint,
		})
	}
	return out, nil
}

// Call satisfies commsx402.Backend.
func (a *x402Adapter) Call(ctx context.Context, params commsx402.CallParams) (*commsx402.CallResult, error) {
	if err := a.ensureBudget(params.AgentID); err != nil {
		return nil, err
	}
	result, err := a.backend.Call(ctx, x402.CallParams{
		AgentID:      params.AgentID,
		ServiceID:    params.ServiceID,
		Path:         params.Path,
		Method:       params.Method,
		Headers:      params.Headers,
		Body:         []byte(params.Body),
		MaxPriceUSDC: params.MaxPriceUSDC,
		PaymentNonce: params.PaymentNonce,
	})
	if err != nil {
		return nil, err
	}
	out := &commsx402.CallResult{
		Status:  result.Status,
		Headers: result.Headers,
		Body:    result.Body,
	}
	if result.Receipt != nil {
		out.Receipt = &commsx402.Receipt{
			Tx:         result.Receipt.Tx,
			Network:    string(result.Receipt.Network),
			AmountUSDC: result.Receipt.AmountUSDC,
			SettledAt:  result.Receipt.Ts.UTC().Format(time.RFC3339Nano),
		}
	}
	return out, nil
}

// BudgetStatus satisfies commsx402.Backend.
func (a *x402Adapter) BudgetStatus(ctx context.Context, agentID string) (*commsx402.BudgetSnapshot, error) {
	if err := a.ensureBudget(agentID); err != nil {
		return nil, err
	}
	snap, err := a.backend.BudgetStatus(ctx, agentID)
	if err != nil {
		return nil, err
	}
	out := &commsx402.BudgetSnapshot{
		PerCallMaxUSDC: snap.PerCallMaxUSDC,
		DailyCapUSDC:   snap.DailyCapUSDC,
		SpentTodayUSDC: snap.SpentTodayUSDC,
	}
	for _, s := range snap.PerService {
		out.PerService = append(out.PerService, commsx402.PerServiceCap{
			ServiceID:      s.ServiceID,
			DailyCapUSDC:   s.DailyCapUSDC,
			SpentTodayUSDC: s.SpentTodayUSDC,
		})
	}
	return out, nil
}

func (a *x402Adapter) ensureBudget(agentID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.enrolled[agentID] {
		return nil
	}
	if err := a.budget.SetAgentBudget(agentID, a.defaultBudget); err != nil {
		return fmt.Errorf("apply default budget: %w", err)
	}
	a.enrolled[agentID] = true
	return nil
}
