package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	skywallet "github.com/sky10/sky10/pkg/wallet"
	"github.com/sky10/sky10/pkg/x402/siwx"
)

func TestVeniceBalanceClientQueriesWalletScopedBalance(t *testing.T) {
	t.Parallel()

	const walletAddress = "0xdd12decbea4bd0bc414af635a3398f50fa291e45"
	var gotPath, gotSIWX string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotSIWX = r.Header.Get(siwx.HeaderName)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"walletAddress":"` + walletAddress + `","balanceUsd":4.9980359,"canConsume":true,"minimumTopUpUsd":5,"suggestedTopUpUsd":10}}`))
	}))
	defer server.Close()

	client := &VeniceBalanceClient{
		AddressResolver: fakeVeniceAddressResolver{address: walletAddress},
		Signer: func(walletName string) siwx.Signer {
			if walletName != "bovilus" {
				t.Fatalf("walletName = %q, want bovilus", walletName)
			}
			return fakePersonalSigner{}
		},
		HTTP: server.Client(),
		Now:  func() time.Time { return time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC) },
	}
	result, err := client.VeniceBalance(context.Background(), Connection{
		ID:      "venice-live",
		BaseURL: server.URL + "/api/v1",
		Auth: AuthConfig{
			Wallet:  "bovilus",
			Network: "base",
		},
	})
	if err != nil {
		t.Fatalf("VeniceBalance() error = %v", err)
	}
	if gotPath != "/api/v1/x402/balance/"+walletAddress {
		t.Fatalf("path = %q", gotPath)
	}
	if gotSIWX == "" {
		t.Fatal("missing SIWX header")
	}
	decoded, err := base64.StdEncoding.DecodeString(gotSIWX)
	if err != nil {
		t.Fatalf("decode SIWX header: %v", err)
	}
	var envelope struct {
		Address string `json:"address"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(decoded, &envelope); err != nil {
		t.Fatalf("decode SIWX envelope: %v", err)
	}
	if !strings.Contains(envelope.Message, "URI: "+server.URL+"/api/v1/x402/balance/"+walletAddress) {
		t.Fatalf("SIWX message URI = %q", envelope.Message)
	}
	if envelope.Address != walletAddress {
		t.Fatalf("SIWX address = %q", envelope.Address)
	}
	if result.BalanceUSD != "4.9980359" || !result.CanConsume {
		t.Fatalf("result = %+v", result)
	}
	if result.Wallet != "bovilus" || result.Network != "base" || result.WalletAddress != walletAddress {
		t.Fatalf("wallet scope = %+v", result)
	}
	if result.CheckedAt != "2026-05-09T12:00:00Z" {
		t.Fatalf("CheckedAt = %q", result.CheckedAt)
	}
}

func TestRPCVeniceBalanceUsesConfiguredConnection(t *testing.T) {
	t.Parallel()

	store := NewStore(filepath.Join(t.TempDir(), "connections.json"))
	if _, err := store.Upsert(context.Background(), Connection{
		ID:       "venice",
		Label:    "Venice",
		Provider: ProviderVenice,
		BaseURL:  DefaultVeniceBaseURL,
		Auth: AuthConfig{
			Method:  AuthMethodX402,
			Wallet:  "bovilus",
			Network: "base",
		},
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	var got Connection
	handler := NewRPCHandler(store, nil)
	handler.SetVeniceBalanceProvider(VeniceBalanceProviderFunc(func(_ context.Context, connection Connection) (*VeniceBalanceResult, error) {
		got = connection
		return &VeniceBalanceResult{
			Wallet:        connection.Auth.Wallet,
			Network:       connection.Auth.Network,
			WalletAddress: "0xabc",
			BalanceUSD:    "4.25",
			CanConsume:    true,
			CheckedAt:     "2026-05-09T12:00:00Z",
		}, nil
	}))

	raw, _ := json.Marshal(VeniceBalanceParams{ConnectionID: "venice"})
	result, err, handled := handler.Dispatch(context.Background(), "inference.veniceBalance", raw)
	if !handled {
		t.Fatal("veniceBalance not handled")
	}
	if err != nil {
		t.Fatalf("veniceBalance error = %v", err)
	}
	balance, ok := result.(*VeniceBalanceResult)
	if !ok {
		t.Fatalf("result type = %T, want *VeniceBalanceResult", result)
	}
	if got.ID != "venice" {
		t.Fatalf("provider saw connection = %+v", got)
	}
	if balance.ConnectionID != "venice" || balance.BalanceUSD != "4.25" {
		t.Fatalf("balance = %+v", balance)
	}
}

func TestVeniceBalanceClientLive(t *testing.T) {
	requireLiveVenice(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	walletClient := skywallet.NewClient()
	if walletClient == nil {
		t.Skip("ows binary not found via skywallet.NewClient")
	}
	walletName := strings.TrimSpace(os.Getenv("X402_LIVE_WALLET"))
	if walletName == "" {
		walletName = strings.TrimSpace(os.Getenv("OWS_WALLET"))
	}
	if walletName == "" {
		walletName = DefaultVeniceWallet
	}
	client := &VeniceBalanceClient{
		AddressResolver: walletClient,
		Signer: func(walletName string) siwx.Signer {
			return siwx.NewOWSSigner(walletClient, walletName)
		},
		HTTP: &http.Client{Timeout: 30 * time.Second},
	}
	result, err := client.VeniceBalance(ctx, Connection{
		ID:       "venice-live",
		Provider: ProviderVenice,
		BaseURL:  DefaultVeniceBaseURL,
		Auth: AuthConfig{
			Method:  AuthMethodX402,
			Wallet:  walletName,
			Network: "base",
		},
	})
	if err != nil {
		t.Fatalf("VeniceBalance() live error = %v", err)
	}
	if result.BalanceUSD == "" || result.WalletAddress == "" {
		t.Fatalf("live Venice balance missing fields: %+v", result)
	}
	t.Logf("Venice balance: wallet=%s address=%s balance_usd=%s can_consume=%v", result.Wallet, result.WalletAddress, result.BalanceUSD, result.CanConsume)
}

type fakeVeniceAddressResolver struct {
	address string
}

func (r fakeVeniceAddressResolver) AddressForChain(_ context.Context, walletName, chain string) (string, error) {
	if walletName != "bovilus" {
		return "", nil
	}
	if chain != "base" {
		return "", nil
	}
	return r.address, nil
}

type fakePersonalSigner struct{}

func (fakePersonalSigner) SignPersonalMessage(context.Context, string) (string, error) {
	return "0xabc", nil
}
