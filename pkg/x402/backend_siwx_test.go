package x402

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/x402/siwx"
)

// fakeSiwxSigner returns a deterministic signature so tests can
// match the exact header bytes a Builder produces. The signature
// itself is bogus from a verifier's perspective; we only assert
// shape.
type fakeSiwxSigner struct {
	mu     sync.Mutex
	calls  atomic.Int64
	gotMsg string
	sig    string
}

func (f *fakeSiwxSigner) SignPersonalMessage(_ context.Context, message string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls.Add(1)
	f.gotMsg = message
	return f.sig, nil
}

// siwxFakeServer captures whatever request hits it and returns 200
// with a JSON body. Lets the test inspect the X-Sign-In-With-X header
// that Backend.Call attached.
type siwxFakeServer struct {
	mu          sync.Mutex
	gotHeaders  http.Header
	requestPath string
}

func (s *siwxFakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.gotHeaders = r.Header.Clone()
	s.requestPath = r.URL.Path
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// TestBackendCallAttachesSIWXHeaderWhenManifestRequiresIt is the
// load-bearing integration test for the Venice flow: a manifest
// with SIWXDomain set causes Backend.Call to build and attach an
// X-Sign-In-With-X header on every outgoing request, regardless of
// whether the upstream returns 402.
func TestBackendCallAttachesSIWXHeaderWhenManifestRequiresIt(t *testing.T) {
	t.Parallel()
	signer := &fakeSiwxSigner{sig: "0x" + strings.Repeat("ab", 65)}
	clock := func() time.Time {
		return time.Date(2026, 5, 6, 11, 0, 0, 0, time.UTC)
	}

	fake := &siwxFakeServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	registry, err := NewRegistry(NewMemoryRegistryStore(), clock)
	if err != nil {
		t.Fatal(err)
	}
	manifest := ServiceManifest{
		ID:           "venice",
		DisplayName:  "Venice",
		Endpoint:     srv.URL,
		Networks:     []Network{NetworkBase},
		MaxPriceUSDC: "0.005",
		SIWXDomain:   "api.venice.ai",
	}
	if err := registry.AddManifest(manifest); err != nil {
		t.Fatal(err)
	}
	if err := registry.Approve("A-1", "venice", "0.005"); err != nil {
		t.Fatal(err)
	}
	budget := NewBudget(clock, nil)
	if err := budget.SetAgentBudget("A-1", BudgetConfig{
		PerCallMaxUSDC: "0.10",
		DailyCapUSDC:   "5.00",
	}); err != nil {
		t.Fatal(err)
	}

	transport := NewTransport(NewFakeSigner("0xdD12DEcbea4bd0Bc414af635a3398f50FA291e45"))
	backend := NewBackend(BackendOptions{
		Registry:  registry,
		Transport: transport,
		Budget:    budget,
		Clock:     clock,
		SIWX: &SIWXContext{
			WalletAddress: "0xdD12DEcbea4bd0Bc414af635a3398f50FA291e45",
			Signer:        signer,
		},
	})

	resp, err := backend.Call(context.Background(), CallParams{
		AgentID:      "A-1",
		ServiceID:    "venice",
		Path:         "/api/v1/chat/completions",
		Method:       "POST",
		Headers:      map[string]string{"Content-Type": "application/json"},
		Body:         []byte(`{"model":"x","messages":[]}`),
		MaxPriceUSDC: "0.005",
	})
	if err != nil {
		t.Fatalf("Backend.Call: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("status = %d", resp.Status)
	}

	// Confirm the request carried an X-Sign-In-With-X header that
	// decodes to a Venice-shaped envelope.
	header := fake.gotHeaders.Get(siwx.HeaderName)
	if header == "" {
		t.Fatalf("server saw no %s header (got %v)", siwx.HeaderName, fake.gotHeaders)
	}
	rawJSON, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal(rawJSON, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env["address"] != "0xdD12DEcbea4bd0Bc414af635a3398f50FA291e45" {
		t.Fatalf("envelope address = %v", env["address"])
	}
	if env["chainId"] != float64(8453) {
		t.Fatalf("envelope chainId = %v", env["chainId"])
	}
	msg := env["message"].(string)
	if !strings.Contains(msg, "api.venice.ai wants you to sign in") {
		t.Fatalf("message missing SIWE preamble: %s", msg)
	}
	if !strings.Contains(msg, "URI: "+srv.URL+"/api/v1/chat/completions") {
		t.Fatalf("message missing URI line: %s", msg)
	}

	// Caller's headers were preserved alongside the injected SIWX
	// header (mergeHeaders correctness).
	if got := fake.gotHeaders.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q (caller's header dropped)", got)
	}

	// Signer fired exactly once — Backend.Call doesn't double-sign.
	if signer.calls.Load() != 1 {
		t.Fatalf("signer.calls = %d, want 1", signer.calls.Load())
	}
}

// TestBackendCallSIWXManifestWithoutSignerErrorsClean asserts that
// manifest-required SIWX with no SIWXContext wired into the Backend
// fails with a clear error rather than silently dropping the header.
func TestBackendCallSIWXManifestWithoutSignerErrorsClean(t *testing.T) {
	t.Parallel()
	clock := func() time.Time { return time.Now() }
	registry, err := NewRegistry(NewMemoryRegistryStore(), clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.AddManifest(ServiceManifest{
		ID:           "venice",
		DisplayName:  "Venice",
		Endpoint:     "http://example",
		Networks:     []Network{NetworkBase},
		MaxPriceUSDC: "0.005",
		SIWXDomain:   "api.venice.ai",
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Approve("A-1", "venice", "0.005"); err != nil {
		t.Fatal(err)
	}
	budget := NewBudget(clock, nil)
	_ = budget.SetAgentBudget("A-1", BudgetConfig{PerCallMaxUSDC: "0.10", DailyCapUSDC: "1.00"})

	backend := NewBackend(BackendOptions{
		Registry:  registry,
		Transport: NewTransport(NewFakeSigner("0x0")),
		Budget:    budget,
		Clock:     clock,
		// No SIWX context wired.
	})
	_, err = backend.Call(context.Background(), CallParams{
		AgentID:      "A-1",
		ServiceID:    "venice",
		Path:         "/x",
		Method:       "GET",
		MaxPriceUSDC: "0.005",
	})
	if err == nil || !strings.Contains(err.Error(), "requires SIWX") {
		t.Fatalf("err = %v, want SIWX-required error", err)
	}
}
