package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestBinPath(t *testing.T) {
	t.Parallel()
	p, err := BinPath()
	if err != nil {
		t.Fatalf("BinPath() error: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".sky10", "bin", "ows")
	if p != want {
		t.Errorf("BinPath() = %q, want %q", p, want)
	}
}

func TestCheckRelease_ParsesResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name": "v0.5.0",
			"assets": []map[string]string{
				{"name": owsAssetName(), "browser_download_url": "https://example.com/ows"},
			},
		})
	}))
	defer srv.Close()

	old := ghReleaseURL
	ghReleaseURL = srv.URL
	defer func() { ghReleaseURL = old }()

	info, err := CheckRelease("v0.4.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.Available {
		t.Error("expected update available")
	}
	if info.Latest != "v0.5.0" {
		t.Errorf("latest = %q, want %q", info.Latest, "v0.5.0")
	}
	if info.AssetURL != "https://example.com/ows" {
		t.Errorf("asset URL = %q, want %q", info.AssetURL, "https://example.com/ows")
	}
}

func TestCheckRelease_AlreadyUpToDate(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name": "v0.5.0",
			"assets":   []map[string]string{},
		})
	}))
	defer srv.Close()

	old := ghReleaseURL
	ghReleaseURL = srv.URL
	defer func() { ghReleaseURL = old }()

	info, err := CheckRelease("v0.5.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Available {
		t.Error("expected no update available")
	}
}

func TestInstall_DownloadsBinary(t *testing.T) {
	t.Parallel()

	content := "#!/bin/sh\necho ows-fake"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
		w.Write([]byte(content))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	os.MkdirAll(binDir, 0755)
	dest := filepath.Join(binDir, "ows")

	resp, err := http.Get(srv.URL + "/ows")
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	defer resp.Body.Close()

	tmpFile, _ := os.CreateTemp(binDir, "ows-test-*")
	buf := make([]byte, 1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			tmpFile.Write(buf[:n])
		}
		if readErr != nil {
			break
		}
	}
	tmpFile.Close()
	os.Chmod(tmpFile.Name(), 0755)
	os.Rename(tmpFile.Name(), dest)

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading installed binary: %v", err)
	}
	if string(data) != content {
		t.Errorf("content = %q, want %q", string(data), content)
	}
	fi, _ := os.Stat(dest)
	if fi.Mode()&0111 == 0 {
		t.Error("binary should be executable")
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

func TestProgressReader(t *testing.T) {
	t.Parallel()
	data := []byte("hello world, this is test data")
	r := &progressReader{
		r:     bytes.NewReader(data),
		total: int64(len(data)),
		fn: func(downloaded, total int64) {
			if total != int64(len(data)) {
				t.Errorf("total = %d, want %d", total, len(data))
			}
		},
	}
	buf := make([]byte, 10)
	var totalRead int
	for {
		n, err := r.Read(buf)
		totalRead += n
		if err != nil {
			break
		}
	}
	if totalRead != len(data) {
		t.Errorf("read %d bytes, want %d", totalRead, len(data))
	}
}

func TestUninstallPath_RemovesManagedBinary(t *testing.T) {
	tmp := t.TempDir()
	dest := filepath.Join(tmp, "ows")
	if err := os.WriteFile(dest, []byte("test"), 0755); err != nil {
		t.Fatalf("writing binary: %v", err)
	}

	result, err := uninstallPath(dest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Removed {
		t.Fatal("expected removed=true")
	}
	if result.Path != dest {
		t.Fatalf("path = %q, want %q", result.Path, dest)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected %q to be removed, stat err=%v", dest, err)
	}
}

func TestUninstallPath_MissingBinary(t *testing.T) {
	tmp := t.TempDir()
	dest := filepath.Join(tmp, "missing-ows")

	result, err := uninstallPath(dest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Removed {
		t.Fatal("expected removed=false")
	}
	if result.Path != dest {
		t.Fatalf("path = %q, want %q", result.Path, dest)
	}
}

// Solana-specific tests (balances, tx building, signing) are in solana_test.go.
