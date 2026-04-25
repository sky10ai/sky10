package wallet

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sky10/sky10/pkg/config"
)

func TestTransactionLogPathUsesWalletDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	path, err := transactionLogPath("default")
	if err != nil {
		t.Fatalf("transactionLogPath() error: %v", err)
	}
	want := filepath.Join(home, "wallet", "wallets", "default", "transactions.jsonl")
	if path != want {
		t.Fatalf("transactionLogPath() = %q, want %q", path, want)
	}
}

func TestAppendAndListTransactions(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	first := testTransaction("tx-1", "send", "solana")
	second := testTransaction("tx-2", "fund", "base")

	if err := appendTransaction("default", first); err != nil {
		t.Fatalf("append first transaction: %v", err)
	}
	if err := appendTransaction("default", second); err != nil {
		t.Fatalf("append second transaction: %v", err)
	}

	entries, err := listTransactions("default", 32)
	if err != nil {
		t.Fatalf("listTransactions() error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].ID != "tx-2" || entries[1].ID != "tx-1" {
		t.Fatalf("entries order = [%q, %q], want newest first", entries[0].ID, entries[1].ID)
	}

	data, err := os.ReadFile(filepath.Join(home, "wallet", "wallets", "default", "transactions.jsonl"))
	if err != nil {
		t.Fatalf("read transaction log: %v", err)
	}
	lines := bytesSplitLines(data)
	if len(lines) != 2 {
		t.Fatalf("transaction log lines = %d, want 2", len(lines))
	}
}

func TestTransactionLogRejectsUnsafeWalletName(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	for _, name := range []string{"", " default", ".", "..", "../default", "nested/default", `nested\default`, "bad:name", "default.", "CON", "con.txt", "lpt1"} {
		if _, err := transactionLogPath(name); err == nil {
			t.Fatalf("transactionLogPath(%q) error = nil, want error", name)
		}
	}
}

func TestRPCHandler_TransactionListRequiresWallet(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	h := NewRPCHandler(nil, noopEmit)
	params, _ := json.Marshal(transactionListParams{})
	_, err, handled := h.Dispatch(context.Background(), "wallet.transactionList", params)
	if !handled {
		t.Fatal("wallet.transactionList should be handled")
	}
	if err == nil || err.Error() != "wallet is required" {
		t.Fatalf("error = %v, want %q", err, "wallet is required")
	}
}

func TestRPCHandler_TransactionAppendStoresEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	h := NewRPCHandler(nil, noopEmit)
	params, _ := json.Marshal(transactionAppendParams{
		Wallet: "default",
		Entry:  testTransaction("tx-rpc", "send", "solana"),
	})
	result, err, handled := h.Dispatch(context.Background(), "wallet.transactionAppend", params)
	if !handled {
		t.Fatal("wallet.transactionAppend should be handled")
	}
	if err != nil {
		t.Fatalf("wallet.transactionAppend error: %v", err)
	}

	status, ok := result.(map[string]string)
	if !ok || status["status"] != "stored" {
		t.Fatalf("result = %#v, want stored status", result)
	}

	entries, err := listTransactions("default", 32)
	if err != nil {
		t.Fatalf("listTransactions() error: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "tx-rpc" {
		t.Fatalf("entries = %#v, want tx-rpc", entries)
	}
}

func testTransaction(id, kind, chain string) TransactionEntry {
	return TransactionEntry{
		Amount:               "-1",
		Asset:                "USDC",
		Chain:                chain,
		Counterparty:         "counterparty",
		CounterpartySubtitle: "test transfer",
		CreatedAt:            "2026-04-25T12:00:00Z",
		ID:                   id,
		Kind:                 kind,
		Memo:                 "test memo",
		Status:               "Submitted",
	}
}
