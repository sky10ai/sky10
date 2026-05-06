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
// (decimal `maxAmountRequired`) and the receipt on X-PAYMENT-RESPONSE
// as plain JSON. In v2 mode (Version = X402ProtocolV2) it emits the
// challenge on the Payment-Required response header (base64 JSON,
// integer `amount`) and the receipt on Payment-Response (base64 JSON
// with `transaction` field).
type x402TestServer struct {
	mu          sync.Mutex
	calls       atomic.Int64
	gotPayments []rawPayment
	bodies      [][]byte
	respondWith json.RawMessage
	// Version controls which x402 wire shape the fake emits. Zero
	// means v1 (legacy body-based challenge).
	Version int
}

// rawPayment captures the parsed top-level JSON of an X-PAYMENT
// envelope so tests can assert which version's keys are present.
type rawPayment struct {
	Top   map[string]json.RawMessage
	Inner ExactSchemePayload
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
		s.writeChallenge(w)
		return
	}
	rp, err := decodeRawPayment(paymentValue)
	if err != nil {
		http.Error(w, "bad payment header: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.gotPayments = append(s.gotPayments, rp)

	if s.Version == X402ProtocolV2 {
		body, _ := json.Marshal(map[string]any{
			"success":     true,
			"transaction": "0xdeadbeef",
			"network":     "eip155:8453",
			"payer":       rp.Inner.Authorization.From,
		})
		w.Header().Set(HeaderPaymentResponseV2, base64.StdEncoding.EncodeToString(body))
	} else {
		receipt, _ := json.Marshal(PaymentReceipt{
			Tx:         "0xdeadbeef",
			Network:    NetworkBase,
			AmountUSDC: "0.005",
			SettledAt:  time.Now().UTC(),
		})
		w.Header().Set(HeaderPaymentResponse, string(receipt))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if s.respondWith != nil {
		_, _ = w.Write(s.respondWith)
	}
}

// writeChallenge emits the 402 challenge in whatever shape the
// configured Version dictates. The sample requirement is the same
// canonical USDC-on-Base challenge for both modes; only the wire
// fields differ.
func (s *x402TestServer) writeChallenge(w http.ResponseWriter) {
	if s.Version == X402ProtocolV2 {
		challenge, _ := json.Marshal(map[string]any{
			"x402Version": 2,
			"resource": map[string]any{
				"url":         "http://test/contents",
				"description": "test",
				"mimeType":    "application/json",
			},
			"accepts": []map[string]any{{
				"scheme":            "exact",
				"network":           "eip155:8453",
				"amount":            "5000",
				"payTo":             "0x000000000000000000000000000000000000beef",
				"asset":             "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
				"maxTimeoutSeconds": 60,
				"extra":             map[string]any{"name": "USD Coin", "version": "2"},
			}},
		})
		w.Header().Set(HeaderPaymentRequiredV2, base64.StdEncoding.EncodeToString(challenge))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"payment required"}`))
		return
	}
	body, _ := json.Marshal(map[string]any{
		"x402Version": 1,
		"accepts": []map[string]any{{
			"scheme":            "exact",
			"network":           "base",
			"maxAmountRequired": "0.005",
			"payTo":             "0x000000000000000000000000000000000000beef",
			"asset":             "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
			"maxTimeoutSeconds": 60,
			"extra":             map[string]any{"name": "USD Coin", "version": "2"},
		}},
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired)
	_, _ = w.Write(body)
}

// decodeRawPayment parses an X-PAYMENT base64-JSON envelope into a
// generic top-level map (so tests can assert which keys are present)
// plus the inner ExactSchemePayload (so tests can assert the
// signature shape).
func decodeRawPayment(b64 string) (rawPayment, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return rawPayment{}, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return rawPayment{}, err
	}
	rp := rawPayment{Top: top}
	if inner, ok := top["payload"]; ok {
		_ = json.Unmarshal(inner, &rp.Inner)
	}
	return rp, nil
}

func mustParseAddr(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	if !strings.HasPrefix(srv.URL, "http") {
		t.Fatalf("unexpected URL: %s", srv.URL)
	}
	return srv.URL
}

// --- v1 round-trip ----------------------------------------------------------

func TestTransportCallV1RoundtripAssertsWireShape(t *testing.T) {
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

	// v1 envelope: top-level x402Version + scheme + network + payload.
	// No `accepted` or `resource` keys.
	assertJSONField(t, pay.Top, "x402Version", float64(X402ProtocolV1))
	assertJSONField(t, pay.Top, "scheme", "exact")
	assertJSONField(t, pay.Top, "network", "base")
	if _, ok := pay.Top["accepted"]; ok {
		t.Fatalf("v1 envelope should not contain `accepted`")
	}
	if _, ok := pay.Top["resource"]; ok {
		t.Fatalf("v1 envelope should not contain `resource`")
	}
	if !strings.HasPrefix(pay.Inner.Signature, "fake-sig:") {
		t.Fatalf("signature = %q, want fake-sig prefix", pay.Inner.Signature)
	}
	// canonical AmountMicros = "5000" (0.005 USDC) flows into the
	// authorization's `value`.
	if pay.Inner.Authorization.Value != "5000" {
		t.Fatalf("inner authorization value = %q, want 5000", pay.Inner.Authorization.Value)
	}
}

// --- v2 round-trip ----------------------------------------------------------

func TestTransportCallV2RoundtripAssertsWireShape(t *testing.T) {
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

	// v2 envelope: top-level x402Version + accepted + resource +
	// payload. NO top-level scheme/network — those live inside
	// `accepted`.
	assertJSONField(t, pay.Top, "x402Version", float64(X402ProtocolV2))
	if _, ok := pay.Top["scheme"]; ok {
		t.Fatalf("v2 envelope should NOT contain top-level `scheme`")
	}
	if _, ok := pay.Top["network"]; ok {
		t.Fatalf("v2 envelope should NOT contain top-level `network`")
	}
	if _, ok := pay.Top["accepted"]; !ok {
		t.Fatalf("v2 envelope missing `accepted`")
	}
	if _, ok := pay.Top["resource"]; !ok {
		t.Fatalf("v2 envelope missing `resource`")
	}

	var accepted map[string]any
	if err := json.Unmarshal(pay.Top["accepted"], &accepted); err != nil {
		t.Fatalf("decode accepted: %v", err)
	}
	if accepted["amount"] != "5000" {
		t.Fatalf("accepted.amount = %v, want 5000 (integer base units)", accepted["amount"])
	}
	if accepted["network"] != "eip155:8453" {
		t.Fatalf("accepted.network = %v, want eip155:8453", accepted["network"])
	}

	var resource Resource
	if err := json.Unmarshal(pay.Top["resource"], &resource); err != nil {
		t.Fatalf("decode resource: %v", err)
	}
	if resource.URL != "http://test/contents" {
		t.Fatalf("resource.URL = %q", resource.URL)
	}
}

// --- shared edges -----------------------------------------------------------

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
	body, _ := json.Marshal(map[string]any{
		"x402Version": 1,
		"accepts": []map[string]any{{
			"scheme":            "exact",
			"network":           "base",
			"maxAmountRequired": "0.005",
			"payTo":             "0x000000000000000000000000000000000000beef",
			"asset":             "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
			"maxTimeoutSeconds": 60,
			"extra":             map[string]any{"name": "USD Coin", "version": "2"},
		}},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write(body)
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
	body, _ := json.Marshal(map[string]any{
		"x402Version": 1,
		"accepts": []map[string]any{{
			"scheme":            "exact",
			"network":           "base",
			"maxAmountRequired": "0.005",
			"payTo":             "0x000000000000000000000000000000000000beef",
			"asset":             "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
			"maxTimeoutSeconds": 60,
			"extra":             map[string]any{"name": "USD Coin", "version": "2"},
		}},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write(body)
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
	body, _ := json.Marshal(map[string]any{
		"x402Version": 1,
		"accepts": []map[string]any{{
			"scheme":            "lightning",
			"network":           "btc",
			"maxAmountRequired": "0.001",
			"payTo":             "lnbc...",
			"asset":             "btc",
		}},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	tx := NewTransport(NewFakeSigner("0x0"))
	_, err := tx.Call(context.Background(), CallRequest{URL: srv.URL})
	if !errors.Is(err, ErrNoCompatibleRequirements) {
		t.Fatalf("err = %v, want ErrNoCompatibleRequirements", err)
	}
}

func assertJSONField(t *testing.T, top map[string]json.RawMessage, key string, want any) {
	t.Helper()
	raw, ok := top[key]
	if !ok {
		t.Fatalf("missing key %q in envelope", key)
	}
	var got any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode %s: %v", key, err)
	}
	if got != want {
		t.Fatalf("envelope[%q] = %v, want %v", key, got, want)
	}
}
