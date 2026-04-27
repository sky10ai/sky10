package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/config"
)

func TestBuildSandboxSecretBindingsEnvAddsManagedBlock(t *testing.T) {
	t.Parallel()

	body := string(BuildSandboxSecretBindingsEnv([]byte("MANUAL_FLAG=1\n"), map[string]string{
		"ELEVENLABS_API_KEY": "elevenlabs-key",
		"OPENAI_API_KEY":     "openai-key",
	}))

	for _, want := range []string{
		"MANUAL_FLAG=1",
		managedSecretEnvBlockStart,
		"# Generated from sandbox secret bindings.",
		"ELEVENLABS_API_KEY=elevenlabs-key",
		"OPENAI_API_KEY=openai-key",
		managedSecretEnvBlockEnd,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("env body = %q, missing %q", body, want)
		}
	}
	if strings.Index(body, "ELEVENLABS_API_KEY") > strings.Index(body, "OPENAI_API_KEY") {
		t.Fatalf("env body should sort managed keys: %q", body)
	}
}

func TestBuildSandboxSecretBindingsEnvReplacesManagedBlock(t *testing.T) {
	t.Parallel()

	existing := []byte(strings.Join([]string{
		"MANUAL_FLAG=1",
		managedSecretEnvBlockStart,
		"OLD_KEY=old-value",
		managedSecretEnvBlockEnd,
		"TRAILING_FLAG=1",
		"",
	}, "\n"))

	body := string(BuildSandboxSecretBindingsEnv(existing, map[string]string{
		"NEW_KEY": "new-value",
	}))

	for _, want := range []string{
		"MANUAL_FLAG=1",
		"TRAILING_FLAG=1",
		"NEW_KEY=new-value",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("env body = %q, missing %q", body, want)
		}
	}
	if strings.Contains(body, "OLD_KEY=old-value") {
		t.Fatalf("env body kept stale managed value: %q", body)
	}
	if strings.Count(body, managedSecretEnvBlockStart) != 1 {
		t.Fatalf("env body should have one managed block: %q", body)
	}
}

func TestBuildSandboxSecretBindingsEnvRemovesManagedBlockWhenEmpty(t *testing.T) {
	t.Parallel()

	existing := []byte(strings.Join([]string{
		"MANUAL_FLAG=1",
		managedSecretEnvBlockStart,
		"ELEVENLABS_API_KEY=old",
		managedSecretEnvBlockEnd,
		"",
	}, "\n"))

	body := string(BuildSandboxSecretBindingsEnv(existing, nil))
	if body != "MANUAL_FLAG=1\n" {
		t.Fatalf("env body = %q, want manual-only env", body)
	}
}

func TestBuildSandboxSecretBindingsEnvQuotesUnsafeValues(t *testing.T) {
	t.Parallel()

	body := string(BuildSandboxSecretBindingsEnv(nil, map[string]string{
		"API_KEY": `value with "quotes" and \ slash`,
	}))

	if !strings.Contains(body, `API_KEY="value with \"quotes\" and \\ slash"`) {
		t.Fatalf("env body did not quote unsafe value correctly: %q", body)
	}
}

func TestNormalizeSecretBindingEnv(t *testing.T) {
	t.Parallel()

	valid, err := normalizeSecretBindingEnv(" ELEVENLABS_API_KEY ")
	if err != nil {
		t.Fatalf("normalizeSecretBindingEnv(valid) error: %v", err)
	}
	if valid != "ELEVENLABS_API_KEY" {
		t.Fatalf("normalizeSecretBindingEnv(valid) = %q, want ELEVENLABS_API_KEY", valid)
	}

	for _, value := range []string{"", "1BAD", "BAD-NAME", "BAD NAME", "BAD.NAME"} {
		if _, err := normalizeSecretBindingEnv(value); err == nil {
			t.Fatalf("normalizeSecretBindingEnv(%q) unexpectedly succeeded", value)
		}
	}
}

func TestNormalizeSecretBindingsSortsAndRejectsDuplicates(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	m.SetSecretLookup(func(ctx context.Context, idOrName string) ([]byte, error) {
		return []byte("value"), nil
	})

	got, err := m.normalizeSecretBindings(context.Background(), []SecretBinding{
		{Env: "Z_KEY", Secret: "z"},
		{Env: "A_KEY", Secret: "a"},
	}, "now")
	if err != nil {
		t.Fatalf("normalizeSecretBindings() error: %v", err)
	}
	if got[0].Env != "A_KEY" || got[1].Env != "Z_KEY" {
		t.Fatalf("normalizeSecretBindings() = %#v, want sorted by env", got)
	}
	if got[0].CreatedAt != "now" || got[0].UpdatedAt != "now" {
		t.Fatalf("normalizeSecretBindings() did not fill timestamps: %#v", got[0])
	}

	_, err = m.normalizeSecretBindings(context.Background(), []SecretBinding{
		{Env: "DUP_KEY", Secret: "one"},
		{Env: "DUP_KEY", Secret: "two"},
	}, "now")
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("normalizeSecretBindings(duplicate) error = %v, want duplicate error", err)
	}
}

func TestNormalizeCreateSecretBindingsAddsAnthropicForAgentTemplates(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	m.SetSecretLookup(func(ctx context.Context, idOrName string) ([]byte, error) {
		if idOrName != "anthropic" {
			return nil, ErrProviderSecretNotFound
		}
		return []byte("anthropic-key"), nil
	})

	got, err := m.normalizeCreateSecretBindings(context.Background(), templateOpenClawDocker, "", nil, "2026-04-26T12:00:00Z")
	if err != nil {
		t.Fatalf("normalizeCreateSecretBindings() error: %v", err)
	}
	if len(got) != 1 ||
		got[0].Env != "ANTHROPIC_API_KEY" ||
		got[0].Secret != "anthropic" {
		t.Fatalf("bindings = %#v, want default Anthropic binding", got)
	}
}

func TestNormalizeCreateSecretBindingsPreservesExplicitAnthropicBinding(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	m.SetSecretLookup(func(ctx context.Context, idOrName string) ([]byte, error) {
		switch idOrName {
		case "owner-anthropic", "anthropic":
			return []byte("anthropic-key"), nil
		default:
			return nil, ErrProviderSecretNotFound
		}
	})

	got, err := m.normalizeCreateSecretBindings(context.Background(), templateOpenClaw, "", []SecretBinding{
		{Env: "ANTHROPIC_API_KEY", Secret: "owner-anthropic"},
	}, "2026-04-26T12:00:00Z")
	if err != nil {
		t.Fatalf("normalizeCreateSecretBindings() error: %v", err)
	}
	if len(got) != 1 || got[0].Secret != "owner-anthropic" {
		t.Fatalf("bindings = %#v, want explicit Anthropic binding preserved", got)
	}
}

func TestNormalizeCreateSecretBindingsSkipsAnthropicForNonAgentTemplates(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	m.SetSecretLookup(func(ctx context.Context, idOrName string) ([]byte, error) {
		return []byte("anthropic-key"), nil
	})

	got, err := m.normalizeCreateSecretBindings(context.Background(), templateUbuntu, "", nil, "2026-04-26T12:00:00Z")
	if err != nil {
		t.Fatalf("normalizeCreateSecretBindings() error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("bindings = %#v, want no default binding for ubuntu template", got)
	}
}

func TestNormalizeCreateSecretBindingsSkipsAnthropicForNonAnthropicModelOverride(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	m.SetSecretLookup(func(ctx context.Context, idOrName string) ([]byte, error) {
		return []byte("anthropic-key"), nil
	})

	got, err := m.normalizeCreateSecretBindings(context.Background(), templateOpenClaw, "openai/gpt-5.5", nil, "2026-04-26T12:00:00Z")
	if err != nil {
		t.Fatalf("normalizeCreateSecretBindings() error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("bindings = %#v, want no default Anthropic binding for non-Anthropic model", got)
	}
}

func TestResolveSharedEnvOmitsSecretBoundKeys(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	got := m.resolveSharedEnv(context.Background(), Record{
		Slug: "openclaw-agent",
		SecretBindings: []SecretBinding{
			{Env: "ANTHROPIC_API_KEY", Secret: "anthropic"},
		},
	}, func(context.Context) (map[string]string, error) {
		return map[string]string{
			"ANTHROPIC_API_KEY": "legacy-anthropic",
			"OPENAI_API_KEY":    "openai-key",
		}, nil
	})

	if _, ok := got["ANTHROPIC_API_KEY"]; ok {
		t.Fatalf("resolved env = %#v, want secret-bound Anthropic omitted", got)
	}
	if got["OPENAI_API_KEY"] != "openai-key" {
		t.Fatalf("resolved env = %#v, want unbound OpenAI retained", got)
	}
}

func TestPrepareTemplateSharedDirMaterializesInitialSecretBindings(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	m.SetSecretLookup(func(ctx context.Context, idOrName string) ([]byte, error) {
		if idOrName != "elevenlabs" {
			return nil, ErrProviderSecretNotFound
		}
		return []byte("elevenlabs-key"), nil
	})

	sharedDir := filepath.Join(t.TempDir(), "shared")
	rec := Record{
		Name:      "media-agent",
		Slug:      "media-agent",
		Provider:  providerLima,
		Template:  templateUbuntu,
		Status:    "creating",
		SharedDir: sharedDir,
		SecretBindings: []SecretBinding{
			{Env: "ELEVENLABS_API_KEY", Secret: "elevenlabs"},
		},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if err := m.prepareTemplateSharedDir(context.Background(), rec); err != nil {
		t.Fatalf("prepareTemplateSharedDir() error: %v", err)
	}
	envText := readSandboxEnv(t, m, rec.Slug)
	if !strings.Contains(envText, "ELEVENLABS_API_KEY=elevenlabs-key") {
		t.Fatalf(".env = %q, want initial projected secret", envText)
	}
}

func TestMaterializeSecretBindingsSkipsMissingEnvWhenEmpty(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	rec := Record{
		Name: "plain-agent",
		Slug: "plain-agent",
	}
	if err := m.materializeSecretBindings(context.Background(), rec); err != nil {
		t.Fatalf("materializeSecretBindings() error: %v", err)
	}

	envPath := filepath.Join(m.sandboxStateDir(rec.Slug), ".env")
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Fatalf("Stat(.env) error = %v, want missing file", err)
	}
}

func TestAttachSecretUpdatesExistingBinding(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	m.SetSecretLookup(func(ctx context.Context, idOrName string) ([]byte, error) {
		switch idOrName {
		case "old-secret":
			return []byte("old-value"), nil
		case "new-secret":
			return []byte("new-value"), nil
		default:
			return nil, ErrProviderSecretNotFound
		}
	})
	addReadySandboxRecord(t, m, "vm-agent")

	if _, err := m.AttachSecret(context.Background(), "vm-agent", SecretAttachParams{
		Env:    "SERVICE_API_KEY",
		Secret: "old-secret",
	}); err != nil {
		t.Fatalf("AttachSecret(old) error: %v", err)
	}
	updated, err := m.AttachSecret(context.Background(), "vm-agent", SecretAttachParams{
		Env:    "SERVICE_API_KEY",
		Secret: "new-secret",
	})
	if err != nil {
		t.Fatalf("AttachSecret(new) error: %v", err)
	}
	if len(updated.SecretBindings) != 1 {
		t.Fatalf("secret bindings len = %d, want 1", len(updated.SecretBindings))
	}
	if got := updated.SecretBindings[0].Secret; got != "new-secret" {
		t.Fatalf("binding secret = %q, want new-secret", got)
	}

	envText := readSandboxEnv(t, m, "vm-agent")
	if !strings.Contains(envText, "SERVICE_API_KEY=new-value") {
		t.Fatalf(".env = %q, want new projected value", envText)
	}
	if strings.Contains(envText, "old-value") {
		t.Fatalf(".env kept old projected value: %q", envText)
	}
}

func TestAttachSecretRejectsMissingSecret(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	m.SetSecretLookup(func(ctx context.Context, idOrName string) ([]byte, error) {
		return nil, ErrProviderSecretNotFound
	})
	addReadySandboxRecord(t, m, "vm-agent")

	_, err = m.AttachSecret(context.Background(), "vm-agent", SecretAttachParams{
		Env:    "SERVICE_API_KEY",
		Secret: "missing-secret",
	})
	if err == nil {
		t.Fatal("AttachSecret() unexpectedly succeeded for missing secret")
	}
	if !strings.Contains(err.Error(), "missing-secret") {
		t.Fatalf("AttachSecret() error = %v, want missing secret context", err)
	}
}

func TestSyncSecretsRefreshesProjectedValues(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	currentValue := "first-value"
	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	m.SetSecretLookup(func(ctx context.Context, idOrName string) ([]byte, error) {
		if idOrName != "service-secret" {
			return nil, ErrProviderSecretNotFound
		}
		return []byte(currentValue), nil
	})
	addReadySandboxRecord(t, m, "vm-agent")

	if _, err := m.AttachSecret(context.Background(), "vm-agent", SecretAttachParams{
		Env:    "SERVICE_API_KEY",
		Secret: "service-secret",
	}); err != nil {
		t.Fatalf("AttachSecret() error: %v", err)
	}

	currentValue = "second-value"
	if _, err := m.SyncSecrets(context.Background(), "vm-agent"); err != nil {
		t.Fatalf("SyncSecrets() error: %v", err)
	}

	envText := readSandboxEnv(t, m, "vm-agent")
	if !strings.Contains(envText, "SERVICE_API_KEY=second-value") {
		t.Fatalf(".env = %q, want refreshed projected value", envText)
	}
	if strings.Contains(envText, "first-value") {
		t.Fatalf(".env kept stale projected value: %q", envText)
	}
}

func addReadySandboxRecord(t *testing.T, m *Manager, slug string) {
	t.Helper()

	now := time.Now().UTC().Format(time.RFC3339)
	rec := Record{
		Name:      slug,
		Slug:      slug,
		Provider:  providerLima,
		Template:  templateUbuntu,
		Status:    "ready",
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[slug] = rec
	if err := m.saveLocked(); err != nil {
		t.Fatalf("saveLocked() error: %v", err)
	}
}

func readSandboxEnv(t *testing.T, m *Manager, slug string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(m.sandboxStateDir(slug), ".env"))
	if err != nil {
		t.Fatalf("ReadFile(.env) error: %v", err)
	}
	return string(body)
}
