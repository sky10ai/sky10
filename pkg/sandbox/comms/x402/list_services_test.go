package x402

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sky10/sky10/pkg/sandbox/comms"
)

func TestListServicesReturnsBackendResult(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{
		listResult: []ServiceListing{
			{ID: "perplexity", DisplayName: "Perplexity", Tier: "primitive", PriceUSDC: "0.005"},
			{ID: "deepgram", DisplayName: "Deepgram", Tier: "primitive", PriceUSDC: "0.003", Category: "audio"},
		},
	}
	h := &handlers{backend: backend}
	resp, err := h.handleListServices(context.Background(), comms.Envelope{
		AgentID: "A-1",
		Payload: nil,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var got listServicesResult
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	if len(got.Services) != 2 {
		t.Fatalf("services count = %d, want 2", len(got.Services))
	}
	if backend.listCalls[0] != "A-1" {
		t.Fatalf("backend got agentID %q, want A-1", backend.listCalls[0])
	}
}

func TestListServicesFiltersByCategory(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{
		listResult: []ServiceListing{
			{ID: "perplexity", Category: "search", Tier: "primitive"},
			{ID: "deepgram", Category: "audio", Tier: "primitive"},
		},
	}
	h := &handlers{backend: backend}
	payload, _ := json.Marshal(listServicesParams{Category: "audio"})
	resp, err := h.handleListServices(context.Background(), comms.Envelope{
		AgentID: "A-1",
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var got listServicesResult
	_ = json.Unmarshal(resp, &got)
	if len(got.Services) != 1 || got.Services[0].ID != "deepgram" {
		t.Fatalf("filtered services = %+v, want [deepgram]", got.Services)
	}
}

func TestListServicesRejectsMalformedPayload(t *testing.T) {
	t.Parallel()
	h := &handlers{backend: &fakeBackend{}}
	_, err := h.handleListServices(context.Background(), comms.Envelope{
		AgentID: "A-1",
		Payload: json.RawMessage("not json"),
	})
	if err == nil {
		t.Fatal("expected error for malformed payload")
	}
}

func TestListServicesPropagatesBackendError(t *testing.T) {
	t.Parallel()
	h := &handlers{backend: &fakeBackend{listErr: errFakeBoom}}
	_, err := h.handleListServices(context.Background(), comms.Envelope{
		AgentID: "A-1",
	})
	if err == nil {
		t.Fatal("expected error from backend")
	}
}
