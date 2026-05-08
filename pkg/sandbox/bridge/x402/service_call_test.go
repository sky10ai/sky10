package x402

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sky10/sky10/pkg/sandbox/bridge"
)

func TestServiceCallSuccess(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{
		callResult: &CallResult{
			Status:  200,
			Body:    json.RawMessage(`{"hello":"world"}`),
			Receipt: &Receipt{Tx: "0xabc", Network: "base", AmountUSDC: "0.005", SettledAt: "2026-04-27T12:00:00Z"},
		},
	}
	h := &handlers{backend: backend}
	payload, _ := json.Marshal(serviceCallParams{
		ServiceID:    "perplexity",
		Path:         "/search",
		Method:       "POST",
		Body:         json.RawMessage(`{"q":"hello"}`),
		MaxPriceUSDC: "0.005",
		PaymentNonce: "n1",
	})
	resp, err := h.handleServiceCall(context.Background(), bridge.Envelope{
		AgentID: "A-1",
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var got CallResult
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	if got.Status != 200 {
		t.Fatalf("status = %d, want 200", got.Status)
	}
	if got.Receipt == nil || got.Receipt.Tx != "0xabc" {
		t.Fatalf("receipt = %+v, want tx 0xabc", got.Receipt)
	}
	if backend.callCalls[0].AgentID != "A-1" {
		t.Fatalf("backend got agentID %q, want A-1", backend.callCalls[0].AgentID)
	}
	if backend.callCalls[0].PaymentNonce != "n1" {
		t.Fatalf("backend got payment_nonce %q, want n1", backend.callCalls[0].PaymentNonce)
	}
}

func TestServiceCallRejectsMissingFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		params  serviceCallParams
		wantSub string
	}{
		{"missing service_id", serviceCallParams{Path: "/x", MaxPriceUSDC: "0.001", PaymentNonce: "n1"}, "service_id"},
		{"missing path", serviceCallParams{ServiceID: "svc", MaxPriceUSDC: "0.001", PaymentNonce: "n1"}, "path"},
		{"path without slash", serviceCallParams{ServiceID: "svc", Path: "search", MaxPriceUSDC: "0.001", PaymentNonce: "n1"}, "path must start with /"},
		{"missing max_price", serviceCallParams{ServiceID: "svc", Path: "/x", PaymentNonce: "n1"}, "max_price_usdc"},
		{"missing nonce", serviceCallParams{ServiceID: "svc", Path: "/x", MaxPriceUSDC: "0.001"}, "payment_nonce"},
		{"bad method", serviceCallParams{ServiceID: "svc", Path: "/x", Method: "TRACE", MaxPriceUSDC: "0.001", PaymentNonce: "n1"}, "method"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := &handlers{backend: &fakeBackend{}}
			payload, _ := json.Marshal(tc.params)
			_, err := h.handleServiceCall(context.Background(), bridge.Envelope{
				AgentID: "A-1",
				Payload: payload,
			})
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestServiceCallRejectsEmptyPayload(t *testing.T) {
	t.Parallel()
	h := &handlers{backend: &fakeBackend{}}
	_, err := h.handleServiceCall(context.Background(), bridge.Envelope{
		AgentID: "A-1",
	})
	if err == nil {
		t.Fatal("expected error for empty payload")
	}
}

func TestServiceCallPropagatesBackendError(t *testing.T) {
	t.Parallel()
	h := &handlers{backend: &fakeBackend{callErr: errFakeBoom}}
	payload, _ := json.Marshal(serviceCallParams{
		ServiceID:    "svc",
		Path:         "/x",
		MaxPriceUSDC: "0.001",
		PaymentNonce: "n1",
	})
	_, err := h.handleServiceCall(context.Background(), bridge.Envelope{
		AgentID: "A-1",
		Payload: payload,
	})
	if err == nil {
		t.Fatal("expected error from backend")
	}
}

func TestServiceCallBackendNeverSeesPayloadAgentID(t *testing.T) {
	t.Parallel()
	// Even if a malicious payload tries to smuggle agent_id through
	// the body, the handler ignores anything outside the typed
	// serviceCallParams fields and the Backend gets the bus-stamped
	// AgentID from the Envelope. This is the integration version of
	// the structural identity protection.
	backend := &fakeBackend{callResult: &CallResult{Status: 200}}
	h := &handlers{backend: backend}
	rawPayload := []byte(`{
		"service_id": "svc",
		"path": "/x",
		"max_price_usdc": "0.001",
		"payment_nonce": "n1",
		"agent_id": "A-impostor"
	}`)
	if _, err := h.handleServiceCall(context.Background(), bridge.Envelope{
		AgentID: "A-trusted",
		Payload: rawPayload,
	}); err != nil {
		t.Fatalf("err = %v", err)
	}
	got := backend.callCalls[0].AgentID
	if got != "A-trusted" {
		t.Fatalf("backend got AgentID %q, want A-trusted; payload-supplied identity must be ignored", got)
	}
}
