package x402

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sky10/sky10/pkg/sandbox/comms"
)

func TestBudgetStatusReturnsBackendSnapshot(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{
		budgetResult: &BudgetSnapshot{
			PerCallMaxUSDC: "0.10",
			DailyCapUSDC:   "5.00",
			SpentTodayUSDC: "0.45",
		},
	}
	h := &handlers{backend: backend}
	resp, err := h.handleBudgetStatus(context.Background(), comms.Envelope{
		AgentID: "A-1",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var got BudgetSnapshot
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	if got.SpentTodayUSDC != "0.45" {
		t.Fatalf("spent_today = %q, want 0.45", got.SpentTodayUSDC)
	}
	if backend.budgetCalls[0] != "A-1" {
		t.Fatalf("backend got agentID %q, want A-1", backend.budgetCalls[0])
	}
}

func TestBudgetStatusRejectsMalformedPayload(t *testing.T) {
	t.Parallel()
	h := &handlers{backend: &fakeBackend{}}
	_, err := h.handleBudgetStatus(context.Background(), comms.Envelope{
		AgentID: "A-1",
		Payload: json.RawMessage("not json"),
	})
	if err == nil {
		t.Fatal("expected error for malformed payload")
	}
}

func TestBudgetStatusPropagatesBackendError(t *testing.T) {
	t.Parallel()
	h := &handlers{backend: &fakeBackend{budgetErr: errFakeBoom}}
	_, err := h.handleBudgetStatus(context.Background(), comms.Envelope{
		AgentID: "A-1",
	})
	if err == nil {
		t.Fatal("expected error from backend")
	}
}
