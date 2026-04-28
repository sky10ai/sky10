package x402

import (
	"path/filepath"
	"testing"
	"time"
)

func TestFileReceiptStoreRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "x402", "receipts.jsonl")
	store := NewFileReceiptStore(path)

	r1 := Receipt{
		Ts:         time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC),
		AgentID:    "A-1",
		ServiceID:  "perplexity",
		Path:       "/search",
		Tx:         "0xabc",
		Network:    NetworkBase,
		AmountUSDC: "0.005",
	}
	r2 := r1
	r2.Tx = "0xdef"
	r2.AmountUSDC = "0.003"

	if err := store.Append(r1); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(r2); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("len(loaded) = %d, want 2", len(loaded))
	}
	if loaded[0].Tx != "0xabc" || loaded[1].Tx != "0xdef" {
		t.Fatalf("ordering: got %+v / %+v", loaded[0], loaded[1])
	}
}

func TestFileReceiptStoreLoadMissingFileReturnsEmpty(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "absent.jsonl")
	got, err := NewFileReceiptStore(path).Load()
	if err != nil {
		t.Fatalf("Load missing file err = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Fatalf("Load missing file = %d entries, want 0", len(got))
	}
}

func TestFileReceiptStoreSkipsMalformedLines(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "x402", "receipts.jsonl")
	store := NewFileReceiptStore(path)
	if err := store.Append(Receipt{
		AgentID: "A-1", ServiceID: "perplexity", AmountUSDC: "0.005",
	}); err != nil {
		t.Fatal(err)
	}
	// Manually append a malformed line and then a real one to make
	// sure Load is forgiving.
	if err := appendRaw(path, "this is not json\n"); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(Receipt{
		AgentID: "A-1", ServiceID: "deepgram", AmountUSDC: "0.003",
	}); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("len(loaded) = %d, want 2 (malformed line should be skipped)", len(loaded))
	}
}

func TestBudgetLoadsFromStoreOnConstruction(t *testing.T) {
	t.Parallel()
	store := NewMemoryReceiptStore()
	prior := Receipt{
		Ts:         time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC),
		AgentID:    "A-1",
		ServiceID:  "perplexity",
		AmountUSDC: "0.005",
	}
	if err := store.Append(prior); err != nil {
		t.Fatal(err)
	}

	b := NewBudget(nil, store)
	all := b.AllReceipts()
	if len(all) != 1 {
		t.Fatalf("len(all) = %d, want 1 (loaded from store)", len(all))
	}
	if all[0].Tx != "" || all[0].ServiceID != "perplexity" {
		t.Fatalf("loaded receipt mismatch: %+v", all[0])
	}
}

func TestBudgetChargePersistsToStore(t *testing.T) {
	t.Parallel()
	store := NewMemoryReceiptStore()
	b := NewBudget(nil, store)
	if err := b.SetAgentBudget("A-1", BudgetConfig{PerCallMaxUSDC: "0.10", DailyCapUSDC: "5.00"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Charge(Receipt{AgentID: "A-1", ServiceID: "perplexity", AmountUSDC: "0.005"}); err != nil {
		t.Fatal(err)
	}

	persisted, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 || persisted[0].ServiceID != "perplexity" {
		t.Fatalf("persisted = %+v, want one perplexity charge", persisted)
	}
}

// appendRaw is a tiny helper used by malformed-line test; lives here
// so the test file is self-contained without importing extra
// packages.
func appendRaw(path, line string) error {
	store := NewFileReceiptStore(path)
	store.mu.Lock()
	defer store.mu.Unlock()
	f, err := openAppend(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
}
