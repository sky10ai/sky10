package llm

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	skyrpc "github.com/sky10/sky10/pkg/rpc"
	skysandbox "github.com/sky10/sky10/pkg/sandbox"
	bridgex402 "github.com/sky10/sky10/pkg/sandbox/bridge/x402"
	skywallet "github.com/sky10/sky10/pkg/wallet"
	skyx402 "github.com/sky10/sky10/pkg/x402"
	"github.com/sky10/sky10/pkg/x402/siwx"
)

const liveVeniceGateEnv = "SKY10_LLM_LIVE_VENICE"

func TestHostGuestVeniceBridgeStreamsChatAndResponses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store := NewStore(filepath.Join(t.TempDir(), "connections.json"))
	if _, err := store.Upsert(ctx, Connection{
		ID:           "bovilus-venice",
		Label:        "Bovilus Venice",
		Provider:     ProviderVenice,
		BaseURL:      DefaultVeniceBaseURL,
		DefaultModel: DefaultVeniceModel,
		Models:       []string{"anthropic/opus-4-7"},
		Auth: AuthConfig{
			Method:       AuthMethodX402,
			ServiceID:    DefaultVeniceX402Service,
			Network:      "base",
			MaxPriceUSDC: "0.020",
		},
	}); err != nil {
		t.Fatalf("save connection: %v", err)
	}

	forwarder := bridgex402.NewForwardingBackend()
	guestURL := startVeniceGuestDaemon(t, store, forwardingMeteredServiceBackend{backend: forwarder}, forwarder)

	hostBackend := &fakeHostMeteredBackend{}
	manager := skysandbox.NewMeteredServicesBridgeManager(hostBackend, nil)
	rec := bridgeRecordForDaemonURL(t, guestURL, "guest-agent")
	if err := manager.Connect(ctx, rec); err != nil {
		t.Fatalf("connect host bridge to guest daemon: %v", err)
	}
	t.Cleanup(func() { manager.Close(rec.Slug) })
	waitForForwarderConnected(t, ctx, forwarder)

	model := "bovilus-venice/anthropic/opus-4-7"
	assertDaemonModels(t, guestURL, "venice/anthropic/opus-4-7", model)

	temperature := 0.0
	chat := postStreamingChatCompletion(t, guestURL, ChatCompletionRequest{
		Model:         model,
		Stream:        true,
		StreamOptions: &ChatStreamOptions{IncludeUsage: true},
		MaxTokens:     liveChatCompletionMaxToken,
		Temperature:   &temperature,
		Messages: []ChatMessage{{
			Role:    "user",
			Content: liveStreamingPrompt,
		}},
	})
	assertSuccessfulDaemonStream(t, chat, "anthropic/opus-4-7", liveRequiredMarkers)
	if !chat.hasUsage() {
		t.Fatalf("chat stream missing usage chunk:\n%s", chat.raw)
	}

	responses := postStreamingResponses(t, guestURL, ResponsesRequest{
		Model:           model,
		Stream:          true,
		StreamOptions:   &ChatStreamOptions{IncludeUsage: true},
		MaxOutputTokens: liveChatCompletionMaxToken,
		Temperature:     &temperature,
		Input:           json.RawMessage(strconv.Quote(liveStreamingPrompt)),
	})
	assertSuccessfulDaemonResponsesStream(t, responses, "anthropic/opus-4-7", liveRequiredMarkers)
	if !responses.hasUsage() {
		t.Fatalf("responses stream missing usage:\n%s", responses.raw)
	}

	calls := hostBackend.calls()
	if len(calls) != 2 {
		t.Fatalf("host backend calls = %d, want 2", len(calls))
	}
	for _, call := range calls {
		if call.AgentID != "guest-agent" {
			t.Fatalf("call.AgentID = %q, want host-stamped guest-agent", call.AgentID)
		}
		if call.ServiceID != DefaultVeniceX402Service {
			t.Fatalf("call.ServiceID = %q", call.ServiceID)
		}
		if call.Path != "/api/v1/chat/completions" {
			t.Fatalf("call.Path = %q", call.Path)
		}
		if call.MaxPriceUSDC != "0.020" {
			t.Fatalf("call.MaxPriceUSDC = %q", call.MaxPriceUSDC)
		}
		var upstream ChatCompletionRequest
		if err := json.Unmarshal(call.Body, &upstream); err != nil {
			t.Fatalf("decode upstream call body: %v", err)
		}
		if upstream.Model != "anthropic/opus-4-7" {
			t.Fatalf("upstream.Model = %q", upstream.Model)
		}
		if upstream.Stream {
			t.Fatal("upstream.Stream = true, want buffered x402 call")
		}
	}
}

func TestHostGuestVeniceLiveBridgeStreamsChat(t *testing.T) {
	requireLiveVenice(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	hostBackend, walletAddress := newLiveVeniceBridgeBackend(t, ctx)
	if !liveVeniceCanConsume(t, ctx, hostBackend, walletAddress) {
		t.Skip("Venice wallet balance cannot consume; skipping chat smoke to avoid paid top-up path")
	}

	store := NewStore(filepath.Join(t.TempDir(), "connections.json"))
	if _, err := store.Upsert(ctx, Connection{
		ID:           "venice-live",
		Label:        "Venice Live",
		Provider:     ProviderVenice,
		BaseURL:      DefaultVeniceBaseURL,
		DefaultModel: DefaultVeniceModel,
		Auth: AuthConfig{
			Method:       AuthMethodX402,
			ServiceID:    DefaultVeniceX402Service,
			Network:      "base",
			MaxPriceUSDC: "0.020",
		},
	}); err != nil {
		t.Fatalf("save connection: %v", err)
	}

	forwarder := bridgex402.NewForwardingBackend()
	guestURL := startVeniceGuestDaemon(t, store, forwardingMeteredServiceBackend{backend: forwarder}, forwarder)
	manager := skysandbox.NewMeteredServicesBridgeManager(hostBackend, nil)
	rec := bridgeRecordForDaemonURL(t, guestURL, "guest-agent")
	if err := manager.Connect(ctx, rec); err != nil {
		t.Fatalf("connect host bridge to guest daemon: %v", err)
	}
	t.Cleanup(func() { manager.Close(rec.Slug) })
	waitForForwarderConnected(t, ctx, forwarder)

	temperature := 0.0
	result := postStreamingChatCompletion(t, guestURL, ChatCompletionRequest{
		Model:         "venice-live/" + DefaultVeniceModel,
		Stream:        true,
		StreamOptions: &ChatStreamOptions{IncludeUsage: true},
		MaxTokens:     180,
		Temperature:   &temperature,
		Messages: []ChatMessage{{
			Role: "user",
			Content: `Return exactly six numbered lines.
Each line must include venice-smoke and describe one part of a host to guest streaming test.
No preamble and no markdown fence.`,
		}},
	})
	if !result.done {
		t.Fatalf("live Venice stream missing DONE:\n%s", result.raw)
	}
	text := strings.TrimSpace(result.text())
	if text == "" {
		t.Fatalf("live Venice stream returned no text:\n%s", result.raw)
	}
	if !strings.Contains(strings.ToLower(text), "venice-smoke") {
		t.Fatalf("live Venice response missing smoke marker:\n%s", text)
	}
	if result.contentChunkCount() < 2 {
		t.Fatalf("live Venice stream returned %d content chunks, want at least 2:\n%s", result.contentChunkCount(), result.raw)
	}
	if !result.hasUsage() {
		t.Fatalf("live Venice stream missing usage chunk:\n%s", result.raw)
	}
}

type forwardingMeteredServiceBackend struct {
	backend bridgex402.Backend
}

func (b forwardingMeteredServiceBackend) CallMeteredService(ctx context.Context, params MeteredServiceCallParams) (*MeteredServiceCallResult, error) {
	result, err := b.backend.Call(ctx, bridgex402.CallParams{
		AgentID:      params.AgentID,
		ServiceID:    params.ServiceID,
		Path:         params.Path,
		Method:       params.Method,
		Headers:      params.Headers,
		Body:         params.Body,
		MaxPriceUSDC: params.MaxPriceUSDC,
		PaymentNonce: params.PaymentNonce,
	})
	if err != nil {
		return nil, err
	}
	return &MeteredServiceCallResult{
		Status:  result.Status,
		Headers: result.Headers,
		Body:    []byte(result.Body),
	}, nil
}

func startVeniceGuestDaemon(t *testing.T, store *Store, meteredBackend MeteredServiceBackend, forwarder *bridgex402.ForwardingBackend) string {
	t.Helper()

	server := skyrpc.NewServer(filepath.Join(t.TempDir(), "guest.sock"), "test", nil)
	endpoint := bridgex402.NewEndpoint(forwarder, func(*http.Request) (string, string, error) {
		return "guest-runtime-agent", "guest-device", nil
	})
	server.HandleHTTP("GET "+bridgex402.EndpointPath, bridgex402.HandlerWithHostBridge(endpoint.Handler(), forwarder))

	handler := NewHostHTTPHandler(HostHTTPOptions{
		Backend: NewConnectionChatBackend(store, ConnectionChatBackendOptions{
			MeteredService: meteredBackend,
			MeteredAgentID: "guest-runtime-agent",
		}),
		ModelLister: StoreModelLister(store),
	})
	server.HandleHTTP("/v1/health", handler.HandleHealth)
	server.HandleHTTP("/v1/models", handler.HandleModels)
	server.HandleHTTP("/v1/chat/completions", handler.HandleChatCompletions)
	server.HandleHTTP("/v1/responses", handler.HandleResponses)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ServeHTTP(ctx, 0)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for server.HTTPAddr() == "" {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("timed out waiting for guest daemon HTTP server")
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("guest daemon HTTP server returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for guest daemon HTTP shutdown")
		}
	})

	return "http://" + server.HTTPAddr()
}

func bridgeRecordForDaemonURL(t *testing.T, rawURL, slug string) skysandbox.Record {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return skysandbox.Record{
		Name:          slug,
		Slug:          slug,
		Provider:      "lima",
		Template:      "openclaw",
		ForwardedHost: host,
		ForwardedPort: port,
	}
}

func waitForForwarderConnected(t *testing.T, ctx context.Context, forwarder *bridgex402.ForwardingBackend) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if forwarder.Connected() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("metered-services bridge did not connect: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

type fakeHostMeteredBackend struct {
	mu      sync.Mutex
	records []bridgex402.CallParams
}

func (b *fakeHostMeteredBackend) ListServices(context.Context, string) ([]bridgex402.ServiceListing, error) {
	return []bridgex402.ServiceListing{{
		ID:          DefaultVeniceX402Service,
		DisplayName: "Venice",
		Tier:        "primitive",
		PriceUSDC:   "0.020",
	}}, nil
}

func (b *fakeHostMeteredBackend) BudgetStatus(context.Context, string) (*bridgex402.BudgetSnapshot, error) {
	return &bridgex402.BudgetSnapshot{PerCallMaxUSDC: "0.020", DailyCapUSDC: "1.00"}, nil
}

func (b *fakeHostMeteredBackend) Call(_ context.Context, params bridgex402.CallParams) (*bridgex402.CallResult, error) {
	b.mu.Lock()
	copied := params
	copied.Body = append([]byte(nil), params.Body...)
	b.records = append(b.records, copied)
	b.mu.Unlock()

	var req ChatCompletionRequest
	_ = json.Unmarshal(params.Body, &req)
	body, err := json.Marshal(ChatCompletionResponse{
		ID:      "chatcmpl-venice-bridge",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []ChatChoice{{
			Index: 0,
			Message: ChatMessage{
				Role:    "assistant",
				Content: veniceBridgeSmokeResponse(),
			},
			FinishReason: "stop",
		}},
		Usage: &ChatUsage{PromptTokens: 16, CompletionTokens: 128, TotalTokens: 144},
	})
	if err != nil {
		return nil, err
	}
	return &bridgex402.CallResult{
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    body,
	}, nil
}

func (b *fakeHostMeteredBackend) calls() []bridgex402.CallParams {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]bridgex402.CallParams, len(b.records))
	copy(out, b.records)
	return out
}

func veniceBridgeSmokeResponse() string {
	return strings.Join([]string{
		"1. alpha-stream frames the request through the guest endpoint and preserves chat messages.",
		"2. beta-stream confirms response headers stay compatible with event-stream clients.",
		"3. gamma-stream checks event decoding across the metered bridge boundary.",
		"4. delta-stream verifies token forwarding remains ordered after the host x402 call.",
		"5. epsilon-stream covers cancellation-aware plumbing without leaking payment credentials.",
		"6. zeta-stream proves provider auth stays in the host-controlled x402 layer.",
		"7. eta-stream validates model routing for Venice models containing provider slashes.",
		"8. theta-stream says validation complete for the two-daemon smoke path.",
	}, "\n")
}

func requireLiveVenice(t *testing.T) {
	t.Helper()
	loadLiveLLMEnv(t)
	if os.Getenv(liveVeniceGateEnv) != "1" {
		t.Skipf("set %s=1 to run live Venice host/guest smoke; this consumes Venice credits", liveVeniceGateEnv)
	}
}

func newLiveVeniceBridgeBackend(t *testing.T, ctx context.Context) (*liveVeniceBridgeBackend, string) {
	t.Helper()
	client := skywallet.NewClient()
	if client == nil {
		t.Skip("ows binary not found via skywallet.NewClient")
	}
	walletName := strings.TrimSpace(os.Getenv("X402_LIVE_WALLET"))
	if walletName == "" {
		walletName = strings.TrimSpace(os.Getenv("OWS_WALLET"))
	}
	if walletName == "" {
		walletName = "default"
	}
	signer := skyx402.NewOWSSigner(client, walletName)
	if signer == nil {
		t.Skip("x402 OWS signer unavailable")
	}
	walletAddress, err := signer.AddressForChain(ctx, walletName, string(skyx402.NetworkBase))
	if err != nil {
		t.Fatalf("resolve Base wallet address: %v", err)
	}

	clock := time.Now
	registry, err := skyx402.NewRegistry(skyx402.NewMemoryRegistryStore(), clock)
	if err != nil {
		t.Fatal(err)
	}
	manifest := skyx402.ServiceManifest{
		ID:           DefaultVeniceX402Service,
		DisplayName:  "Venice AI",
		Endpoint:     "https://api.venice.ai",
		Networks:     []skyx402.Network{skyx402.NetworkBase},
		MaxPriceUSDC: "0.020",
		UpdatedAt:    clock().UTC(),
		SIWXDomain:   "api.venice.ai",
	}
	if err := registry.AddManifest(manifest); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetUserEnabled(manifest.ID, "0.020"); err != nil {
		t.Fatal(err)
	}
	budget := skyx402.NewBudget(clock, skyx402.NewMemoryReceiptStore())
	if err := budget.SetAgentBudget("guest-agent", skyx402.BudgetConfig{
		PerCallMaxUSDC: "0.020",
		DailyCapUSDC:   "1.000",
	}); err != nil {
		t.Fatal(err)
	}
	transport := skyx402.NewTransport(signer)
	transport.HTTP = &http.Client{Timeout: 90 * time.Second}
	backend := skyx402.NewBackend(skyx402.BackendOptions{
		Registry:  registry,
		Transport: transport,
		Budget:    budget,
		Clock:     clock,
		SIWX: &skyx402.SIWXContext{
			WalletAddress: walletAddress,
			Signer:        siwx.NewOWSSigner(client, walletName),
		},
	})
	return &liveVeniceBridgeBackend{backend: backend, budget: budget}, walletAddress
}

func liveVeniceCanConsume(t *testing.T, ctx context.Context, backend *liveVeniceBridgeBackend, walletAddress string) bool {
	t.Helper()
	result, err := backend.Call(ctx, bridgex402.CallParams{
		AgentID:      "guest-agent",
		ServiceID:    DefaultVeniceX402Service,
		Path:         "/api/v1/x402/balance/" + walletAddress,
		Method:       http.MethodGet,
		MaxPriceUSDC: "0.020",
		PaymentNonce: "venice-balance-" + strconv.FormatInt(time.Now().UnixNano(), 10),
	})
	if err != nil {
		t.Fatalf("Venice balance preflight: %v", err)
	}
	if result.Status != http.StatusOK {
		t.Fatalf("Venice balance status = %d, body = %s", result.Status, result.Body)
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			CanConsume bool `json:"canConsume"`
		} `json:"data"`
	}
	if err := json.Unmarshal(result.Body, &payload); err != nil {
		t.Fatalf("decode Venice balance: %v", err)
	}
	return payload.Success && payload.Data.CanConsume
}

type liveVeniceBridgeBackend struct {
	backend *skyx402.Backend
	budget  *skyx402.Budget
}

func (b *liveVeniceBridgeBackend) ListServices(context.Context, string) ([]bridgex402.ServiceListing, error) {
	return []bridgex402.ServiceListing{{
		ID:          DefaultVeniceX402Service,
		DisplayName: "Venice",
		Tier:        "primitive",
		PriceUSDC:   "0.020",
	}}, nil
}

func (b *liveVeniceBridgeBackend) BudgetStatus(ctx context.Context, agentID string) (*bridgex402.BudgetSnapshot, error) {
	snap, err := b.backend.BudgetStatus(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return &bridgex402.BudgetSnapshot{
		PerCallMaxUSDC: snap.PerCallMaxUSDC,
		DailyCapUSDC:   snap.DailyCapUSDC,
		SpentTodayUSDC: snap.SpentTodayUSDC,
	}, nil
}

func (b *liveVeniceBridgeBackend) Call(ctx context.Context, params bridgex402.CallParams) (*bridgex402.CallResult, error) {
	result, err := b.backend.Call(ctx, skyx402.CallParams{
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
	return &bridgex402.CallResult{
		Status:  result.Status,
		Headers: result.Headers,
		Body:    result.Body,
	}, nil
}
