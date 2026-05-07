package llm

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreUpsertNormalizesVeniceX402Connection(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "connections.json"))
	store.now = func() time.Time { return time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC) }

	got, err := store.Upsert(context.Background(), Connection{
		ID:       "venice",
		Provider: "Venice",
		BaseURL:  "https://api.venice.ai/api/v1/",
		Auth: AuthConfig{
			Method: "x402",
		},
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if got.Label != DefaultVeniceLabel {
		t.Fatalf("Label = %q, want %q", got.Label, DefaultVeniceLabel)
	}
	if got.BaseURL != DefaultVeniceBaseURL {
		t.Fatalf("BaseURL = %q, want %q", got.BaseURL, DefaultVeniceBaseURL)
	}
	if got.DefaultModel != "" {
		t.Fatalf("DefaultModel = %q, want empty connection default", got.DefaultModel)
	}
	if got.Auth.Method != AuthMethodX402 {
		t.Fatalf("Auth.Method = %q, want %q", got.Auth.Method, AuthMethodX402)
	}
	if got.Auth.ServiceID != DefaultVeniceX402Service {
		t.Fatalf("Auth.ServiceID = %q, want %q", got.Auth.ServiceID, DefaultVeniceX402Service)
	}
	if got.Auth.MaxPriceUSDC != DefaultVeniceMaxPriceUSDC {
		t.Fatalf("Auth.MaxPriceUSDC = %q, want %q", got.Auth.MaxPriceUSDC, DefaultVeniceMaxPriceUSDC)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not set: %+v", got)
	}
}

func TestStorePersistsConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inference", "connections.json")
	store := NewStore(path)
	if _, err := store.Upsert(context.Background(), Connection{
		ID:       "venice",
		Label:    "Venice",
		Provider: ProviderVenice,
		BaseURL:  DefaultVeniceBaseURL,
		Auth:     AuthConfig{Method: AuthMethodX402},
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	reopened := NewStore(path)
	connections, err := reopened.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(connections) != 1 {
		t.Fatalf("len(connections) = %d, want 1", len(connections))
	}
	if connections[0].ID != "venice" {
		t.Fatalf("connection id = %q, want venice", connections[0].ID)
	}
}

func TestStoreUpsertNormalizesOpenAIConnection(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "connections.json"))
	got, err := store.Upsert(context.Background(), Connection{
		ID:       "openai",
		Provider: ProviderOpenAI,
		Auth:     AuthConfig{Method: AuthMethodAPIKey},
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if got.Label != DefaultOpenAILabel {
		t.Fatalf("Label = %q, want %q", got.Label, DefaultOpenAILabel)
	}
	if got.BaseURL != DefaultOpenAIBaseURL {
		t.Fatalf("BaseURL = %q, want %q", got.BaseURL, DefaultOpenAIBaseURL)
	}
	if got.DefaultModel != "" {
		t.Fatalf("DefaultModel = %q, want empty connection default", got.DefaultModel)
	}
	if got.Auth.APIKeyEnv != DefaultOpenAIAPIKeyEnv {
		t.Fatalf("APIKeyEnv = %q, want %q", got.Auth.APIKeyEnv, DefaultOpenAIAPIKeyEnv)
	}
}

func TestStoreUpsertNormalizesAnthropicConnection(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "connections.json"))
	got, err := store.Upsert(context.Background(), Connection{
		ID:       "anthropic",
		Provider: ProviderAnthropic,
		Auth:     AuthConfig{Method: AuthMethodAPIKey},
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if got.Label != DefaultAnthropicLabel {
		t.Fatalf("Label = %q, want %q", got.Label, DefaultAnthropicLabel)
	}
	if got.BaseURL != DefaultAnthropicBaseURL {
		t.Fatalf("BaseURL = %q, want %q", got.BaseURL, DefaultAnthropicBaseURL)
	}
	if got.DefaultModel != "" {
		t.Fatalf("DefaultModel = %q, want empty connection default", got.DefaultModel)
	}
	if got.Auth.APIKeyEnv != DefaultAnthropicAPIKeyEnv {
		t.Fatalf("APIKeyEnv = %q, want %q", got.Auth.APIKeyEnv, DefaultAnthropicAPIKeyEnv)
	}
	if got.Auth.APIVersion != DefaultAnthropicAPIVersion {
		t.Fatalf("APIVersion = %q, want %q", got.Auth.APIVersion, DefaultAnthropicAPIVersion)
	}
}

func TestStoreRejectsUnsupportedProvider(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "connections.json"))
	_, err := store.Upsert(context.Background(), Connection{
		ID:       "unknown",
		Label:    "Unknown",
		Provider: "unknown",
		BaseURL:  "https://example.com",
		Auth:     AuthConfig{Method: "api_key"},
	})
	if err == nil {
		t.Fatal("Upsert() error = nil, want unsupported provider error")
	}
}
