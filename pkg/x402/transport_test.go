package x402

import (
	"context"
	"encoding/base64"
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
// In v1 mode (default) it emits the challenge in the response body
// and the receipt on X-PAYMENT-RESPONSE. In v2 mode (set Version =
// X402ProtocolV2) it emits the challenge on the Payment-Required
// response header (base64 JSON) with `amount` instead of
// `maxAmountRequired`, and the receipt on Payment-Response (also
// base64 JSON).
type x402TestServer struct {
	mu           sync.Mutex
	calls        atomic.Int64
	gotPayments  []PaymentPayload
	bodies       [][]byte
	respondWith  json.RawMessage
	requirements PaymentRequirements
	// Version controls which x402 wire shape the fake emits. Zero
	// means v1 (legacy body-based challenge).
	Version int
}

func newX402TestServer(respond json.RawMessage) *x402TestServer {
	return &x402TestServer{
		respondWith:  respond,
		requirements: sampleRequirement(),
	}
}

func (s *x402TestServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.calls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodies = append(s.bodies, body)

	paymentValue := r.Header.Get(HeaderPayment)
	if paymentValue == "" {
		s.writeChallenge(w)
		return
	}

	payload, err := DecodePaymentPayload(paymentValue)
	if err != nil {
		http.Error(w, "bad payment header", http.StatusBadRequest)
		return
	}
	s.gotPayments = append(s.gotPayments, payload)

	receipt := PaymentReceipt{
		Tx:         "0xdeadbeef",
		Network:    Network(payload.Network),
		AmountUSDC: s.requirements.MaxAmount(),
		SettledAt:  time.Now().UTC(),
	}
	receiptJSON, _ := json.Marshal(receipt)
	if s.Version == X402ProtocolV2 {
		w.Header().Set(HeaderPaymentResponseV2, base64.StdEncoding.EncodeToString(receiptJSON))
	} else {
		w.Header().Set(HeaderPaymentResponse, string(receiptJSON))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if s.respondWith != nil {
		_, _ = w.Write(s.respondWith)
	}
}

// writeChallenge emits the 402 challenge in whatever shape the
// configured Version dictates.
func (s *x402TestServer) writeChallenge(w http.ResponseWriter) {
	if s.Version == X402ProtocolV2 {
		// v2: challenge in Payment-Required header, body is the
		// short error blob real services tend to emit.
		req := s.requirements
		// v2 quotes amount as integer base units (micro-USDC), not
		// decimal USDC. If the test's sample populated the v1
		// decimal field, convert: "0.005" → "5000".
		if req.Amount == "" && req.MaxAmountRequired != "" {
			micros, err := parseUSDC(req.MaxAmountRequired)
			if err != nil {
				http.Error(w, "v2 amount conversion: "+err.Error(), http.StatusInternalServerError)
				return
			}
			req.Amount = micros.String()
			req.MaxAmountRequired = ""
		}
		challenge := PaymentChallenge{
			X402Version: X402ProtocolV2,
			Accepts:     []PaymentRequirements{req},
		}
		raw, _ := json.Marshal(challenge)
		w.Header().Set(HeaderPaymentRequiredV2, base64.StdEncoding.EncodeToString(raw))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"payment required"}`))
		return
	}
	challenge := PaymentChallenge{
		X402Version: X402ProtocolV1,
		Accepts:     []PaymentRequirements{s.requirements},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired)
	_ = json.NewEncoder(w).Encode(challenge)
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

	tx := NewTransport(NewFakeSigner("0x0000000000000000000000000000000000000abc"))
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
	if pay.X402Version != X402ProtocolV1 {
		t.Fatalf("payload x402Version = %d, want %d (v1 challenge should round-trip)", pay.X402Version, X402ProtocolV1)
	}
	if pay.Scheme != "exact" || pay.Network != "base" {
		t.Fatalf("scheme/network = %s/%s", pay.Scheme, pay.Network)
	}
	var exact ExactSchemePayload
	if err := json.Unmarshal(pay.Payload, &exact); err != nil {
		t.Fatalf("decode inner: %v", err)
	}
	if !strings.HasPrefix(exact.Signature, "fake-sig:") {
		t.Fatalf("signature = %q, want fake-sig prefix", exact.Signature)
	}
}

func TestTransportCallSignsAndRetriesV2(t *testing.T) {
	t.Parallel()
	fake := newX402TestServer(json.RawMessage(`{"answer":"42"}`))
	fake.Version = X402ProtocolV2
	srv := httptest.NewServer(fake)
	defer srv.Close()

	tx := NewTransport(NewFakeSigner("0x0000000000000000000000000000000000000abc"))
	resp, err := tx.Call(context.Background(), CallRequest{
		Method: "POST",
		URL:    mustParseAddr(t, srv) + "/contents",
		Body:   []byte(`{"q":"hi"}`),
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("status = %d, want 200", resp.Status)
	}
	if resp.Receipt == nil || resp.Receipt.Tx != "0xdeadbeef" {
		t.Fatalf("receipt = %+v, want tx 0xdeadbeef from base64-encoded Payment-Response", resp.Receipt)
	}
	if len(fake.gotPayments) != 1 {
		t.Fatalf("payments seen = %d, want 1", len(fake.gotPayments))
	}
	pay := fake.gotPayments[0]
	if pay.X402Version != X402ProtocolV2 {
		t.Fatalf("payload x402Version = %d, want %d (v2 challenge should round-trip)", pay.X402Version, X402ProtocolV2)
	}
}

func TestTransportCallReturnsImmediateSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	tx := NewTransport(NewFakeSigner("0x0"))
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_ = json.NewEncoder(w).Encode(PaymentChallenge{
			X402Version: X402ProtocolVersion,
			Accepts:     []PaymentRequirements{sampleRequirement()},
		})
	}))
	defer srv.Close()

	tx := NewTransport(NewFakeSigner("0x0"))
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
			X402Version: X402ProtocolVersion,
			Accepts:     []PaymentRequirements{sampleRequirement()},
		})
	}))
	defer srv.Close()

	tx := NewTransport(NewStubSigner("OWS not installed"))
	_, err := tx.Call(context.Background(), CallRequest{URL: srv.URL})
	if !errors.Is(err, ErrSignerNotConfigured) {
		t.Fatalf("err = %v, want ErrSignerNotConfigured", err)
	}
}

func TestTransportCallReturnsNoCompatibleRequirements(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_ = json.NewEncoder(w).Encode(PaymentChallenge{
			X402Version: X402ProtocolVersion,
			Accepts: []PaymentRequirements{
				{Scheme: "lightning", Network: "btc", MaxAmountRequired: "1", PayTo: "lnbc...", Asset: "btc"},
			},
		})
	}))
	defer srv.Close()

	tx := NewTransport(NewFakeSigner("0x0"))
	_, err := tx.Call(context.Background(), CallRequest{URL: srv.URL})
	if !errors.Is(err, ErrNoCompatibleRequirements) {
		t.Fatalf("err = %v, want ErrNoCompatibleRequirements", err)
	}
}
