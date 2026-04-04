package wallet

import (
	"context"
	"encoding/json"
	"testing"
)

func TestNewClient_NotInstalled(t *testing.T) {
	// Cannot use t.Parallel with t.Setenv.
	t.Setenv("PATH", "/nonexistent")
	c := NewClient()
	if c != nil {
		t.Fatal("expected nil client when ows is not on PATH")
	}
	if c.Available() {
		t.Fatal("nil client should not be available")
	}
}

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
		{"GetWallet", func() (err error) { _, err = c.GetWallet(ctx, "test"); return }},
		{"Address", func() (err error) { _, err = c.Address(ctx, "test"); return }},
		{"Balance", func() (err error) { _, err = c.Balance(ctx, "test"); return }},
		{"Pay", func() (err error) { _, err = c.Pay(ctx, "test", "https://example.com"); return }},
		{"Deposit", func() (err error) { _, err = c.Deposit(ctx, "test"); return }},
		{"Transfer", func() (err error) { _, err = c.Transfer(ctx, "test", "addr", "1.0", "SOL"); return }},
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

func TestRPCHandler_UnknownMethod(t *testing.T) {
	t.Parallel()
	h := NewRPCHandler(nil)

	// Non-wallet method should not be handled.
	_, _, handled := h.Dispatch(context.Background(), "skykv.get", nil)
	if handled {
		t.Error("non-wallet method should not be handled")
	}

	// Unknown wallet method should be handled with error.
	_, err, handled := h.Dispatch(context.Background(), "wallet.nonexistent", nil)
	if !handled {
		t.Error("wallet.nonexistent should be handled")
	}
	if err == nil {
		t.Error("wallet.nonexistent should return an error")
	}
}

func TestRPCHandler_StatusWhenNotInstalled(t *testing.T) {
	t.Parallel()
	h := NewRPCHandler(nil)
	result, err, handled := h.Dispatch(context.Background(), "wallet.status", nil)
	if !handled {
		t.Fatal("wallet.status should be handled")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, _ := json.Marshal(result)
	var status StatusResult
	if err := json.Unmarshal(b, &status); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if status.Installed {
		t.Error("expected installed=false")
	}
}

func TestRPCHandler_CreateRequiresName(t *testing.T) {
	t.Parallel()
	h := NewRPCHandler(nil)
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
	h := NewRPCHandler(&Client{bin: "ows"})
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
	h := NewRPCHandler(&Client{bin: "ows"})

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
	h := NewRPCHandler(&Client{bin: "ows"})

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
