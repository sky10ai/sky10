package x402

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// x402TestServer is a minimal x402-compliant server used in tests.
// First request without X-PAYMENT returns 402 with a PaymentChallenge
// body; second request with a valid header returns 200 with an
// X-PAYMENT-RESPONSE header.
type x402TestServer struct {
	mu          sync.Mutex
	calls       atomic.Int64
	challenges  []PaymentChallenge
	gotPayments []PaymentHeader
	bodies      [][]byte
	respondWith json.RawMessage
}

func newX402TestServer(respond json.RawMessage) *x402TestServer {
	return &x402TestServer{respondWith: respond}
}

func (s *x402TestServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.calls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodies = append(s.bodies, body)

	paymentValue := r.Header.Get(HeaderPayment)
	if paymentValue == "" {
		challenge := PaymentChallenge{
			Version:   "1",
			Network:   NetworkBase,
			Currency:  CurrencyUSDC,
			Amount:    "0.005",
			Recipient: "0xrecipient",
			Nonce:     "challenge-nonce-1",
			ExpiresAt: time.Now().Add(time.Minute),
		}
		s.challenges = append(s.challenges, challenge)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_ = json.NewEncoder(w).Encode(challenge)
		return
	}

	header, err := DecodePaymentHeader(paymentValue)
	if err != nil {
		http.Error(w, "bad payment header", http.StatusBadRequest)
		return
	}
	s.gotPayments = append(s.gotPayments, header)
	if header.Signature == "" {
		http.Error(w, "unsigned payment", http.StatusPaymentRequired)
		return
	}

	receipt := PaymentReceipt{
		Tx:         "0xdeadbeef",
		Network:    header.Network,
		AmountUSDC: header.Amount,
		SettledAt:  time.Now().UTC(),
	}
	receiptJSON, _ := json.Marshal(receipt)
	w.Header().Set(HeaderPaymentResponse, string(receiptJSON))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if s.respondWith != nil {
		_, _ = w.Write(s.respondWith)
	}
}

func mustParseAddr(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	if !strings.HasPrefix(srv.URL, "http") {
		t.Fatalf("unexpected URL: %s", srv.URL)
	}
	return srv.URL
}

func TestTransportCallSignsAndRetries(t *testing.T) {
	t.Parallel()
	fake := newX402TestServer(json.RawMessage(`{"answer":"42"}`))
	srv := httptest.NewServer(fake)
	defer srv.Close()

	tx := NewTransport(NewFakeSigner())
	resp, err := tx.Call(context.Background(), CallRequest{
		Method: "POST",
		URL:    mustParseAddr(t, srv) + "/search",
		Body:   []byte(`{"q":"hi"}`),
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("status = %d, want 200", resp.Status)
	}
	if string(resp.Body) != `{"answer":"42"}` {
		t.Fatalf("body = %q", resp.Body)
	}
	if resp.Receipt == nil || resp.Receipt.Tx != "0xdeadbeef" {
		t.Fatalf("receipt = %+v, want tx 0xdeadbeef", resp.Receipt)
	}
	if got := fake.calls.Load(); got != 2 {
		t.Fatalf("server saw %d calls, want 2 (initial 402 + retry)", got)
	}
	if len(fake.gotPayments) != 1 {
		t.Fatalf("payments seen = %d, want 1", len(fake.gotPayments))
	}
	pay := fake.gotPayments[0]
	if pay.Signature != "fake-sig:challenge-nonce-1" {
		t.Fatalf("signature = %q, want fake-sig:challenge-nonce-1", pay.Signature)
	}
}

func TestTransportCallReturnsImmediateSuccess(t *testing.T) {
	t.Parallel()
	// Server that returns 200 directly — no challenge, no retry.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	tx := NewTransport(NewFakeSigner())
	resp, err := tx.Call(context.Background(), CallRequest{Method: "GET", URL: srv.URL + "/free"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Status != 200 || resp.Receipt != nil {
		t.Fatalf("expected free 200 without receipt, got %+v", resp)
	}
}

func TestTransportCallReturnsErrPaymentNotAccepted(t *testing.T) {
	t.Parallel()
	// Server that returns 402 even after a signed retry.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_ = json.NewEncoder(w).Encode(PaymentChallenge{
			Network: NetworkBase, Currency: CurrencyUSDC, Amount: "0.005",
			Recipient: "0xr", Nonce: "n1",
		})
	}))
	defer srv.Close()

	tx := NewTransport(NewFakeSigner())
	_, err := tx.Call(context.Background(), CallRequest{URL: srv.URL})
	if !errors.Is(err, ErrPaymentNotAccepted) {
		t.Fatalf("err = %v, want ErrPaymentNotAccepted", err)
	}
}

func TestTransportCallReturnsSignerErr(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_ = json.NewEncoder(w).Encode(PaymentChallenge{
			Network: NetworkBase, Currency: CurrencyUSDC, Amount: "0.005",
			Recipient: "0xr", Nonce: "n1",
		})
	}))
	defer srv.Close()

	tx := NewTransport(NewStubSigner("OWS not installed"))
	_, err := tx.Call(context.Background(), CallRequest{URL: srv.URL})
	if !errors.Is(err, ErrSignerNotConfigured) {
		t.Fatalf("err = %v, want ErrSignerNotConfigured", err)
	}
}
