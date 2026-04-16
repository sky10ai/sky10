package venice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	skywallet "github.com/sky10/sky10/pkg/wallet"
)

type fakeWallet struct {
	mu               sync.Mutex
	wallets          []skywallet.Wallet
	addressCalls     []string
	signMessageCalls []string
	signTypedCalls   []string
	address          string
}

func (f *fakeWallet) ListWallets(_ context.Context) ([]skywallet.Wallet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wallets == nil {
		return []skywallet.Wallet{{Name: "default", ID: "wallet-1"}}, nil
	}
	return append([]skywallet.Wallet(nil), f.wallets...), nil
}

func (f *fakeWallet) AddressForChain(_ context.Context, _ string, chain string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addressCalls = append(f.addressCalls, chain)
	return f.address, nil
}

func (f *fakeWallet) SignMessage(_ context.Context, _ string, chain, message string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signMessageCalls = append(f.signMessageCalls, chain+"|"+message)
	return "abc123", nil
}

func (f *fakeWallet) SignTypedData(_ context.Context, _ string, chain, typedData string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signTypedCalls = append(f.signTypedCalls, chain+"|"+typedData)
	return "def456", nil
}

func TestProxyChatAutoTopUpAndRetry(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	chatRequests := 0
	topUpPayments := 0
	var latestChatBody string
	var latestAuthHeader string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/chat/completions":
			mu.Lock()
			chatRequests++
			body := readAllString(t, r)
			latestChatBody = body
			latestAuthHeader = r.Header.Get(authHeaderName)
			paid := topUpPayments > 0
			mu.Unlock()

			if latestAuthHeader == "" {
				http.Error(w, "missing auth", http.StatusUnauthorized)
				return
			}
			if !paid {
				w.WriteHeader(http.StatusPaymentRequired)
				_, _ = w.Write([]byte(`{"error":"insufficient balance"}`))
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(balanceHeaderName, "9.75")
			_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
		case "/api/v1/x402/top-up":
			if r.Header.Get(paymentHeaderName) == "" {
				w.Header().Set(paymentRequiredHeader, encodeRequirementsHeader(t, topUpRequirements{
					X402Version: 1,
					Accepts: []topUpAccept{{
						Network: "base",
						Asset:   baseUSDCAddress,
						Amount:  "1000000",
						PayTo:   "0x1111111111111111111111111111111111111111",
					}},
				}))
				w.WriteHeader(http.StatusPaymentRequired)
				return
			}

			mu.Lock()
			topUpPayments++
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"newBalance":10}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	wallet := &fakeWallet{address: "0x9999999999999999999999999999999999999999"}
	proxy := newTestProxy(t, wallet, upstream.URL)

	req := httptest.NewRequest(http.MethodPost, "/venice/v1/chat/completions", strings.NewReader(`{"model":"llama-3.3-70b","messages":[{"role":"user","content":"ping"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	proxy.HandleAPI(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"content":"ok"`) {
		t.Fatalf("unexpected body: %s", rr.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if chatRequests != 2 {
		t.Fatalf("chat requests = %d, want 2", chatRequests)
	}
	if topUpPayments != 1 {
		t.Fatalf("top-up payments = %d, want 1", topUpPayments)
	}
	if latestChatBody == "" || !strings.Contains(latestChatBody, `"model":"llama-3.3-70b"`) {
		t.Fatalf("chat body was not forwarded: %q", latestChatBody)
	}
	if latestAuthHeader == "" {
		t.Fatal("missing auth header on upstream request")
	}

	wallet.mu.Lock()
	defer wallet.mu.Unlock()
	if len(wallet.signMessageCalls) != 2 {
		t.Fatalf("signMessage calls = %d, want 2", len(wallet.signMessageCalls))
	}
	if len(wallet.signTypedCalls) != 1 {
		t.Fatalf("signTypedData calls = %d, want 1", len(wallet.signTypedCalls))
	}
	if !strings.Contains(wallet.signTypedCalls[0], `"TransferWithAuthorization"`) {
		t.Fatalf("typed data was not constructed correctly: %s", wallet.signTypedCalls[0])
	}
}

func TestProxyModelsPassThrough(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get(authHeaderName) == "" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"venice-kimi","object":"model","created":1,"owned_by":"venice"}]}`))
	}))
	defer upstream.Close()

	wallet := &fakeWallet{address: "0x9999999999999999999999999999999999999999"}
	proxy := newTestProxy(t, wallet, upstream.URL)

	req := httptest.NewRequest(http.MethodGet, "/venice/v1/models", nil)
	rr := httptest.NewRecorder()

	proxy.HandleAPI(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"venice-kimi"`) {
		t.Fatalf("unexpected body: %s", rr.Body.String())
	}

	wallet.mu.Lock()
	defer wallet.mu.Unlock()
	if len(wallet.signMessageCalls) != 1 {
		t.Fatalf("signMessage calls = %d, want 1", len(wallet.signMessageCalls))
	}
	if len(wallet.signTypedCalls) != 0 {
		t.Fatalf("signTypedData calls = %d, want 0", len(wallet.signTypedCalls))
	}
}

func TestBuildSIWEMessage(t *testing.T) {
	t.Parallel()

	issuedAt := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	expiresAt := issuedAt.Add(5 * time.Minute)
	got := buildSIWEMessage("api.venice.ai", "0xabc", "https://api.venice.ai/api/v1/models", "deadbeefcafebabe", issuedAt, expiresAt, baseChainID)

	if !strings.Contains(got, "api.venice.ai wants you to sign in with your Ethereum account:") {
		t.Fatalf("message missing domain prelude: %q", got)
	}
	if !strings.Contains(got, "\n\nSign in to Venice AI\n\nURI: https://api.venice.ai/api/v1/models") {
		t.Fatalf("message missing statement/URI block: %q", got)
	}
	if !strings.Contains(got, "Expiration Time: 2026-04-16T12:05:00.000Z") {
		t.Fatalf("message missing expiration time: %q", got)
	}
}

func TestResolveWalletNamePrefersDefault(t *testing.T) {
	t.Parallel()

	proxy := newTestProxy(t, &fakeWallet{
		wallets: []skywallet.Wallet{
			{Name: "travel", ID: "1"},
			{Name: "default", ID: "2"},
		},
		address: "0x9999999999999999999999999999999999999999",
	}, "https://api.venice.ai")

	got, err := proxy.resolveWalletName(context.Background())
	if err != nil {
		t.Fatalf("resolveWalletName: %v", err)
	}
	if got != "default" {
		t.Fatalf("got %q, want default", got)
	}
}

func TestResolveWalletNameUsesOnlyWallet(t *testing.T) {
	t.Parallel()

	proxy := newTestProxy(t, &fakeWallet{
		wallets: []skywallet.Wallet{{Name: "solo", ID: "1"}},
		address: "0x9999999999999999999999999999999999999999",
	}, "https://api.venice.ai")

	got, err := proxy.resolveWalletName(context.Background())
	if err != nil {
		t.Fatalf("resolveWalletName: %v", err)
	}
	if got != "solo" {
		t.Fatalf("got %q, want solo", got)
	}
}

func TestResolveWalletNameErrorsOnAmbiguousWallets(t *testing.T) {
	t.Parallel()

	proxy := newTestProxy(t, &fakeWallet{
		wallets: []skywallet.Wallet{
			{Name: "alpha", ID: "1"},
			{Name: "beta", ID: "2"},
		},
		address: "0x9999999999999999999999999999999999999999",
	}, "https://api.venice.ai")

	_, err := proxy.resolveWalletName(context.Background())
	if err == nil || !strings.Contains(err.Error(), "multiple OWS wallets found") {
		t.Fatalf("err = %v, want multiple-wallets error", err)
	}
}

func newTestProxy(t *testing.T, wallet *fakeWallet, apiURL string) *Proxy {
	t.Helper()

	proxy, err := NewProxy(Config{
		APIURL:   apiURL,
		Wallet:   "",
		TopUpUSD: "10",
	}, wallet, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	proxy.now = func() time.Time {
		return time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	}
	return proxy
}

func encodeRequirementsHeader(t *testing.T, requirements topUpRequirements) string {
	t.Helper()
	raw, err := json.Marshal(requirements)
	if err != nil {
		t.Fatalf("marshal requirements: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func readAllString(t *testing.T, r *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}
