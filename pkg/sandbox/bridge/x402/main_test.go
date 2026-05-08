package x402

import (
	"context"
	"errors"
	"sync"
)

// fakeBackend is the test substitute for the host-side x402 logic.
// Each method records the calls it received and returns whatever the
// test set ahead of time.
type fakeBackend struct {
	mu sync.Mutex

	listResult []ServiceListing
	listErr    error
	listCalls  []string

	callResult *CallResult
	callErr    error
	callCalls  []CallParams

	budgetResult *BudgetSnapshot
	budgetErr    error
	budgetCalls  []string
}

func (b *fakeBackend) ListServices(_ context.Context, agentID string) ([]ServiceListing, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.listCalls = append(b.listCalls, agentID)
	if b.listErr != nil {
		return nil, b.listErr
	}
	return b.listResult, nil
}

func (b *fakeBackend) Call(_ context.Context, params CallParams) (*CallResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.callCalls = append(b.callCalls, params)
	if b.callErr != nil {
		return nil, b.callErr
	}
	return b.callResult, nil
}

func (b *fakeBackend) BudgetStatus(_ context.Context, agentID string) (*BudgetSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.budgetCalls = append(b.budgetCalls, agentID)
	if b.budgetErr != nil {
		return nil, b.budgetErr
	}
	return b.budgetResult, nil
}

var errFakeBoom = errors.New("fake backend boom")
