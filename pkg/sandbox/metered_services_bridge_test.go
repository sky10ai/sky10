package sandbox

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	commsx402 "github.com/sky10/sky10/pkg/sandbox/comms/x402"
)

func TestMeteredServicesBridgeManagerConnectsToGuestEndpoint(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	forwarder := commsx402.NewForwardingBackend()
	mux := http.NewServeMux()
	localEndpoint := commsx402.NewEndpoint(forwarder, func(*http.Request) (string, string, error) {
		return "A-guest", "D-guest", nil
	})
	mux.HandleFunc("GET "+commsx402.EndpointPath, commsx402.HandlerWithHostBridge(localEndpoint.Handler(), forwarder))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rec := bridgeTestRecord(t, srv.URL)
	manager := NewMeteredServicesBridgeManager(&testMeteredBackend{}, nil)
	if err := manager.Connect(ctx, rec); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	waitForBridgeConnected(t, ctx, forwarder, true)

	manager.Close(rec.Slug)
	waitForBridgeConnected(t, ctx, forwarder, false)
}

func bridgeTestRecord(t *testing.T, rawURL string) Record {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return Record{
		Name:          "Travel Agent",
		Slug:          "travel-agent",
		Provider:      providerLima,
		Template:      templateOpenClaw,
		ForwardedHost: host,
		ForwardedPort: port,
	}
}

func waitForBridgeConnected(t *testing.T, ctx context.Context, forwarder *commsx402.ForwardingBackend, want bool) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if forwarder.Connected() == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("bridge connected did not become %v: %v", want, ctx.Err())
		case <-ticker.C:
		}
	}
}

type testMeteredBackend struct{}

func (testMeteredBackend) ListServices(context.Context, string) ([]commsx402.ServiceListing, error) {
	return []commsx402.ServiceListing{{ID: "travel.search", Tier: "primitive"}}, nil
}

func (testMeteredBackend) BudgetStatus(context.Context, string) (*commsx402.BudgetSnapshot, error) {
	return &commsx402.BudgetSnapshot{PerCallMaxUSDC: "0.10"}, nil
}

func (testMeteredBackend) Call(context.Context, commsx402.CallParams) (*commsx402.CallResult, error) {
	return &commsx402.CallResult{Status: 200, Body: json.RawMessage(`{"ok":true}`)}, nil
}

func TestMeteredServicesBridgeURLUsesCanonicalPath(t *testing.T) {
	t.Parallel()
	got, err := meteredServicesBridgeURL(Record{
		ForwardedHost: "127.0.0.1",
		ForwardedPort: 39101,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "ws://127.0.0.1:39101" + commsx402.EndpointPath + "?" + commsx402.BridgeRoleQuery + "=" + commsx402.BridgeRoleHost
	if got != want {
		t.Fatalf("meteredServicesBridgeURL() = %q, want %q", got, want)
	}
}
