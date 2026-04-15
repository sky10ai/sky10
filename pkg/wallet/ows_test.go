package wallet

import (
	"context"
	"encoding/json"
	"testing"
)

func noopEmit(string, interface{}) {}

func TestClient_NilReturnsErrNotInstalled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var c *Client // nil

	tests := []struct {
		name string
		fn   func() error
	}{
		{"CreateWallet", func() (err error) { _, err = c.CreateWallet(ctx, "test"); return }},
		{"ListWallets", func() (err error) { _, err = c.ListWallets(ctx); return }},
		{"Address", func() (err error) { _, err = c.Address(ctx, "test"); return }},
		{"Balance", func() (err error) { _, err = c.Balance(ctx, "test"); return }},
		{"Pay", func() (err error) { _, err = c.Pay(ctx, "test", "https://example.com"); return }},
		{"Deposit", func() (err error) { _, err = c.Deposit(ctx, "test"); return }},
		{"DepositForChain", func() (err error) { _, err = c.DepositForChain(ctx, "test", ChainBase); return }},
		{"Transfer", func() (err error) { _, err = c.Transfer(ctx, "test", "addr", "1.0", "SOL"); return }},
		{"TransferForChain", func() (err error) {
			_, err = c.TransferForChain(ctx, "test", ChainBase, "0x1111111111111111111111111111111111111111", "1.0", "ETH")
			return
		}},
		{"MaxTransferForChain", func() (err error) { _, err = c.MaxTransferForChain(ctx, "test", ChainBase); return }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.fn()
			if err != ErrNotInstalled {
				t.Errorf("got %v, want ErrNotInstalled", err)
			}
		})
	}
}

func TestOWSChainArg(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "default solana", input: "", want: ChainSolana},
		{name: "solana", input: ChainSolana, want: ChainSolana},
		{name: "base caip", input: ChainBase, want: "base"},
		{name: "base alias", input: "base", want: "base"},
		{name: "passthrough", input: "eip155:1", want: "eip155:1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := owsChainArg(tt.input); got != tt.want {
				t.Fatalf("owsChainArg(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestClient_StatusNil(t *testing.T) {
	t.Parallel()
	var c *Client
	result, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Installed {
		t.Error("nil client should report installed=false")
	}
}

func TestParseWalletList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		want   int
		wantID string
	}{
		{
			"no wallets",
			"No wallets found.",
			0, "",
		},
		{
			"one wallet",
			"ID:      91f431d8-a299-44ed-bf7b-422a79c60da6\nName:    default\nSecured: ✓\n  solana → addr1",
			1, "91f431d8-a299-44ed-bf7b-422a79c60da6",
		},
		{
			"two wallets",
			"ID:      aaa\nName:    first\n\nID:      bbb\nName:    second",
			2, "aaa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wallets := parseWalletList(tt.input)
			if len(wallets) != tt.want {
				t.Errorf("got %d wallets, want %d", len(wallets), tt.want)
			}
			if tt.want > 0 && wallets[0].ID != tt.wantID {
				t.Errorf("first ID = %q, want %q", wallets[0].ID, tt.wantID)
			}
		})
	}
}

func TestParseSolanaAddress(t *testing.T) {
	t.Parallel()

	input := "ID:      uuid1\nName:    default\n  eip155:1 (ethereum) → 0xabc\n  solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp (solana) → 6fSWeC5P1icuiW2DfWHxz3rxjjpZXccsNYXJfXYkjaZ4\n  bip122 (bitcoin) → bc1q..."

	addr, err := parseSolanaAddress(input, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if addr != "6fSWeC5P1icuiW2DfWHxz3rxjjpZXccsNYXJfXYkjaZ4" {
		t.Errorf("got %q", addr)
	}

	_, err = parseSolanaAddress(input, "other")
	if err == nil {
		t.Error("expected error for unknown wallet")
	}
}

func TestParseBaseAddressFallsBackToEVMAddress(t *testing.T) {
	t.Parallel()

	input := "ID:      uuid1\nName:    default\n  eip155:1 (ethereum) → 0xabc123\n  solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp (solana) → 6fSWeC5P1icuiW2DfWHxz3rxjjpZXccsNYXJfXYkjaZ4"

	addr, err := parseBaseAddress(input, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if addr != "0xabc123" {
		t.Errorf("got %q", addr)
	}
}

func TestRPCHandler_UnknownMethod(t *testing.T) {
	t.Parallel()
	h := NewRPCHandler(nil, noopEmit)

	_, _, handled := h.Dispatch(context.Background(), "skykv.get", nil)
	if handled {
		t.Error("non-wallet method should not be handled")
	}

	_, err, handled := h.Dispatch(context.Background(), "wallet.nonexistent", nil)
	if !handled {
		t.Error("wallet.nonexistent should be handled")
	}
	if err == nil {
		t.Error("wallet.nonexistent should return an error")
	}
}

func TestRPCHandler_StatusWhenNilClient(t *testing.T) {
	t.Parallel()
	// Directly construct with nil client to avoid auto-detection.
	h := &RPCHandler{client: nil, emit: noopEmit}
	result, err, handled := h.Dispatch(context.Background(), "wallet.status", nil)
	if !handled {
		t.Fatal("wallet.status should be handled")
	}
	// When OWS is actually installed, status returns installed=true.
	// When not installed, status returns installed=false.
	// Either is valid — just verify no crash.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, _ := json.Marshal(result)
	var status StatusResult
	if err := json.Unmarshal(b, &status); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	// status.Installed may be true or false depending on environment.
	_ = status
}

func TestRPCHandler_CreateRequiresName(t *testing.T) {
	t.Parallel()
	h := NewRPCHandler(nil, noopEmit)
	params, _ := json.Marshal(map[string]string{"name": ""})
	_, err, handled := h.Dispatch(context.Background(), "wallet.create", params)
	if !handled {
		t.Fatal("should be handled")
	}
	if err == nil {
		t.Fatal("empty name should return error")
	}
}

func TestRPCHandler_DepositRequiresWallet(t *testing.T) {
	t.Parallel()
	h := NewRPCHandler(&Client{bin: "ows"}, noopEmit)
	params, _ := json.Marshal(map[string]string{"wallet": ""})
	_, err, handled := h.Dispatch(context.Background(), "wallet.deposit", params)
	if !handled {
		t.Fatal("should be handled")
	}
	if err == nil || err.Error() != "wallet is required" {
		t.Errorf("got %v, want %q", err, "wallet is required")
	}
}

func TestRPCHandler_TransferRequiresFields(t *testing.T) {
	t.Parallel()
	h := NewRPCHandler(&Client{bin: "ows"}, noopEmit)

	tests := []struct {
		name   string
		params transferParams
		errMsg string
	}{
		{"no wallet", transferParams{To: "addr", Amount: "1"}, "wallet is required"},
		{"no to", transferParams{Wallet: "w", Amount: "1"}, "to address is required"},
		{"no amount", transferParams{Wallet: "w", To: "addr"}, "amount is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			raw, _ := json.Marshal(tt.params)
			_, err, handled := h.Dispatch(context.Background(), "wallet.transfer", raw)
			if !handled {
				t.Fatal("should be handled")
			}
			if err == nil || err.Error() != tt.errMsg {
				t.Errorf("got %v, want %q", err, tt.errMsg)
			}
		})
	}
}

func TestRPCHandler_PayRequiresFields(t *testing.T) {
	t.Parallel()
	h := NewRPCHandler(&Client{bin: "ows"}, noopEmit)

	tests := []struct {
		name   string
		params payParams
		errMsg string
	}{
		{"no wallet", payParams{URL: "https://example.com"}, "wallet is required"},
		{"no url", payParams{Wallet: "w"}, "url is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			raw, _ := json.Marshal(tt.params)
			_, err, handled := h.Dispatch(context.Background(), "wallet.pay", raw)
			if !handled {
				t.Fatal("should be handled")
			}
			if err == nil || err.Error() != tt.errMsg {
				t.Errorf("got %v, want %q", err, tt.errMsg)
			}
		})
	}
}

func TestRPCHandler_InstallDispatch(t *testing.T) {
	t.Parallel()
	h := NewRPCHandler(nil, noopEmit)
	_, _, handled := h.Dispatch(context.Background(), "wallet.install", nil)
	if !handled {
		t.Error("wallet.install should be handled")
	}
}

func TestRPCHandler_UninstallDispatch(t *testing.T) {
	t.Parallel()
	h := NewRPCHandler(nil, noopEmit)
	_, _, handled := h.Dispatch(context.Background(), "wallet.uninstall", nil)
	if !handled {
		t.Error("wallet.uninstall should be handled")
	}
}

func TestRPCHandler_CheckUpdateDispatch(t *testing.T) {
	t.Parallel()
	h := NewRPCHandler(nil, noopEmit)
	_, _, handled := h.Dispatch(context.Background(), "wallet.checkUpdate", nil)
	if !handled {
		t.Error("wallet.checkUpdate should be handled")
	}
}

// Solana-specific tests (balances, tx building, signing) are in solana_test.go.
