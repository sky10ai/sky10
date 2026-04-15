package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestResolveHermesProviderEnv(t *testing.T) {
	t.Parallel()

	values, err := ResolveHermesProviderEnv(context.Background(), func(ctx context.Context, idOrName string) ([]byte, error) {
		switch idOrName {
		case "anthropic":
			return []byte("anthropic-key\n"), nil
		case "OPENAI_API_KEY":
			return []byte("openai-key"), nil
		case "openrouter":
			return []byte("openrouter-key"), nil
		default:
			return nil, ErrProviderSecretNotFound
		}
	})
	if err != nil {
		t.Fatalf("ResolveHermesProviderEnv() error: %v", err)
	}
	if got := values["ANTHROPIC_API_KEY"]; got != "anthropic-key" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want anthropic-key", got)
	}
	if got := values["OPENAI_API_KEY"]; got != "openai-key" {
		t.Fatalf("OPENAI_API_KEY = %q, want openai-key", got)
	}
	if got := values["OPENROUTER_API_KEY"]; got != "openrouter-key" {
		t.Fatalf("OPENROUTER_API_KEY = %q, want openrouter-key", got)
	}
}

func TestResolveHermesProviderEnvPropagatesLookupErrors(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	_, err := ResolveHermesProviderEnv(context.Background(), func(ctx context.Context, idOrName string) ([]byte, error) {
		if idOrName == "OPENROUTER_API_KEY" {
			return nil, boom
		}
		return nil, ErrProviderSecretNotFound
	})
	if !errors.Is(err, boom) {
		t.Fatalf("ResolveHermesProviderEnv() error = %v, want boom", err)
	}
}

func TestBuildHermesSharedEnvWithoutExistingFile(t *testing.T) {
	t.Parallel()

	body := string(BuildHermesSharedEnv(nil, map[string]string{
		"ANTHROPIC_API_KEY": "anthropic-key",
	}))
	if !strings.Contains(body, "OPENAI_API_KEY=") {
		t.Fatalf("env body missing openai stub: %q", body)
	}
	if !strings.Contains(body, "ANTHROPIC_API_KEY=anthropic-key") {
		t.Fatalf("env body missing resolved anthropic key: %q", body)
	}
	if !strings.Contains(body, "OPENROUTER_API_KEY=") {
		t.Fatalf("env body missing openrouter stub: %q", body)
	}
}

func TestBuildHermesSharedEnvMergesResolvedValues(t *testing.T) {
	t.Parallel()

	existing := []byte(strings.Join([]string{
		"# keep me",
		"ANTHROPIC_API_KEY=manual-anthropic",
		"CUSTOM_FLAG=1",
		"",
	}, "\n"))
	body := string(BuildHermesSharedEnv(existing, map[string]string{
		"ANTHROPIC_API_KEY":  "host-anthropic",
		"OPENROUTER_API_KEY": "host-openrouter",
	}))
	if !strings.Contains(body, "# keep me") {
		t.Fatalf("env body dropped existing comment: %q", body)
	}
	if !strings.Contains(body, "CUSTOM_FLAG=1") {
		t.Fatalf("env body dropped unrelated key: %q", body)
	}
	if !strings.Contains(body, "ANTHROPIC_API_KEY=host-anthropic") {
		t.Fatalf("env body did not replace anthropic key: %q", body)
	}
	if !strings.Contains(body, "OPENROUTER_API_KEY=host-openrouter") {
		t.Fatalf("env body did not append openrouter key: %q", body)
	}
}
