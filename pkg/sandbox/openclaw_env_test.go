package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestResolveOpenClawProviderEnv(t *testing.T) {
	t.Parallel()

	values, err := ResolveOpenClawProviderEnv(context.Background(), func(ctx context.Context, idOrName string) ([]byte, error) {
		switch idOrName {
		case "anthropic":
			return []byte("anthropic-key\n"), nil
		case "OPENAI_API_KEY":
			return []byte("openai-key"), nil
		default:
			return nil, ErrProviderSecretNotFound
		}
	})
	if err != nil {
		t.Fatalf("ResolveOpenClawProviderEnv() error: %v", err)
	}
	if got := values["ANTHROPIC_API_KEY"]; got != "anthropic-key" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want anthropic-key", got)
	}
	if got := values["OPENAI_API_KEY"]; got != "openai-key" {
		t.Fatalf("OPENAI_API_KEY = %q, want openai-key", got)
	}
}

func TestResolveOpenClawProviderEnvPropagatesLookupErrors(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	_, err := ResolveOpenClawProviderEnv(context.Background(), func(ctx context.Context, idOrName string) ([]byte, error) {
		if idOrName == "OPENAI_API_KEY" {
			return nil, boom
		}
		return nil, ErrProviderSecretNotFound
	})
	if !errors.Is(err, boom) {
		t.Fatalf("ResolveOpenClawProviderEnv() error = %v, want boom", err)
	}
}

func TestBuildOpenClawSharedEnvWithoutExistingFile(t *testing.T) {
	t.Parallel()

	body := string(BuildOpenClawSharedEnv(nil, map[string]string{
		"OPENAI_API_KEY": "openai-key",
	}))
	if !strings.Contains(body, "ANTHROPIC_API_KEY=") {
		t.Fatalf("env body missing anthropic stub: %q", body)
	}
	if !strings.Contains(body, "OPENAI_API_KEY=openai-key") {
		t.Fatalf("env body missing resolved openai key: %q", body)
	}
}

func TestBuildOpenClawSharedEnvMergesResolvedValues(t *testing.T) {
	t.Parallel()

	existing := []byte(strings.Join([]string{
		"# keep me",
		"OPENAI_API_KEY=manual-openai",
		"CUSTOM_FLAG=1",
		"",
	}, "\n"))
	body := string(BuildOpenClawSharedEnv(existing, map[string]string{
		"ANTHROPIC_API_KEY": "anthropic-key",
		"OPENAI_API_KEY":    "host-openai",
	}))
	if !strings.Contains(body, "# keep me") {
		t.Fatalf("env body dropped existing comment: %q", body)
	}
	if !strings.Contains(body, "CUSTOM_FLAG=1") {
		t.Fatalf("env body dropped unrelated key: %q", body)
	}
	if !strings.Contains(body, "OPENAI_API_KEY=host-openai") {
		t.Fatalf("env body did not replace openai key: %q", body)
	}
	if !strings.Contains(body, "ANTHROPIC_API_KEY=anthropic-key") {
		t.Fatalf("env body did not append anthropic key: %q", body)
	}
}
