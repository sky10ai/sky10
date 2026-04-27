package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	managedSecretEnvBlockStart = "# sky10 managed secret bindings"
	managedSecretEnvBlockEnd   = "# end sky10 managed secret bindings"
)

var sandboxSecretEnvKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type SecretBinding struct {
	Env       string `json:"env"`
	Secret    string `json:"secret"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type SecretAttachParams struct {
	Name   string `json:"name,omitempty"`
	Slug   string `json:"slug,omitempty"`
	Env    string `json:"env"`
	Secret string `json:"secret"`
}

type SecretDetachParams struct {
	Name string `json:"name,omitempty"`
	Slug string `json:"slug,omitempty"`
	Env  string `json:"env"`
}

type SecretBindingsResult struct {
	Name     string          `json:"name"`
	Slug     string          `json:"slug"`
	Bindings []SecretBinding `json:"bindings"`
}

func (m *Manager) SecretBindings(ctx context.Context, name string) (*SecretBindingsResult, error) {
	rec, err := m.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	return &SecretBindingsResult{
		Name:     rec.Name,
		Slug:     rec.Slug,
		Bindings: append([]SecretBinding(nil), rec.SecretBindings...),
	}, nil
}

func (m *Manager) AttachSecret(ctx context.Context, name string, params SecretAttachParams) (*Record, error) {
	env, err := normalizeSecretBindingEnv(params.Env)
	if err != nil {
		return nil, err
	}
	secret := strings.TrimSpace(params.Secret)
	if secret == "" {
		return nil, fmt.Errorf("secret is required")
	}
	if err := m.ensureSecretResolvable(ctx, secret); err != nil {
		return nil, err
	}

	slug, err := m.resolveRecordSlug(name)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	rec, err := m.mutateStoredRecord(slug, func(rec *Record) (bool, error) {
		for i := range rec.SecretBindings {
			if rec.SecretBindings[i].Env != env {
				continue
			}
			if rec.SecretBindings[i].Secret == secret {
				return false, nil
			}
			rec.SecretBindings[i].Secret = secret
			rec.SecretBindings[i].UpdatedAt = now
			return true, nil
		}
		rec.SecretBindings = append(rec.SecretBindings, SecretBinding{
			Env:       env,
			Secret:    secret,
			CreatedAt: now,
			UpdatedAt: now,
		})
		sortSecretBindings(rec.SecretBindings)
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	if rec == nil {
		rec, err = m.Get(ctx, slug)
		if err != nil {
			return nil, err
		}
	}
	if err := m.materializeSecretBindings(ctx, *rec); err != nil {
		return nil, err
	}
	m.emitState(*rec)
	return rec, nil
}

func (m *Manager) DetachSecret(ctx context.Context, name string, params SecretDetachParams) (*Record, error) {
	env, err := normalizeSecretBindingEnv(params.Env)
	if err != nil {
		return nil, err
	}
	slug, err := m.resolveRecordSlug(name)
	if err != nil {
		return nil, err
	}

	rec, err := m.mutateStoredRecord(slug, func(rec *Record) (bool, error) {
		next := rec.SecretBindings[:0]
		changed := false
		for _, binding := range rec.SecretBindings {
			if binding.Env == env {
				changed = true
				continue
			}
			next = append(next, binding)
		}
		if !changed {
			return false, nil
		}
		rec.SecretBindings = next
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	if rec == nil {
		rec, err = m.Get(ctx, slug)
		if err != nil {
			return nil, err
		}
	}
	if err := m.materializeSecretBindings(ctx, *rec); err != nil {
		return nil, err
	}
	m.emitState(*rec)
	return rec, nil
}

func (m *Manager) SyncSecrets(ctx context.Context, name string) (*SecretBindingsResult, error) {
	rec, err := m.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := m.materializeSecretBindings(ctx, *rec); err != nil {
		return nil, err
	}
	return &SecretBindingsResult{
		Name:     rec.Name,
		Slug:     rec.Slug,
		Bindings: append([]SecretBinding(nil), rec.SecretBindings...),
	}, nil
}

func (m *Manager) resolveRecordSlug(name string) (string, error) {
	key, err := normalizeLookup(name)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	slug, ok := m.findRecordKeyLocked(key)
	if !ok {
		return "", fmt.Errorf("sandbox %q not found", key)
	}
	return slug, nil
}

func (m *Manager) ensureSecretResolvable(ctx context.Context, secret string) error {
	if m.secretLookup == nil {
		return fmt.Errorf("sandbox secret lookup is not configured")
	}
	if _, err := m.secretLookup(ctx, secret); err != nil {
		return fmt.Errorf("resolve secret %q: %w", secret, err)
	}
	return nil
}

func (m *Manager) materializeSecretBindings(ctx context.Context, rec Record) error {
	values := map[string]string{}
	for _, binding := range rec.SecretBindings {
		env, err := normalizeSecretBindingEnv(binding.Env)
		if err != nil {
			return err
		}
		secret := strings.TrimSpace(binding.Secret)
		if secret == "" {
			return fmt.Errorf("secret binding %s is missing a secret", env)
		}
		if m.secretLookup == nil {
			return fmt.Errorf("sandbox secret lookup is not configured")
		}
		payload, err := m.secretLookup(ctx, secret)
		if err != nil {
			return fmt.Errorf("resolve secret %q for %s: %w", secret, env, err)
		}
		values[env] = strings.TrimRight(string(payload), "\r\n")
	}

	stateDir := m.sandboxStateDir(rec.Slug)
	envPath := filepath.Join(stateDir, ".env")
	existing, err := os.ReadFile(envPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("reading sandbox env file: %w", err)
		}
		if len(values) == 0 {
			return nil
		}
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("creating sandbox state directory: %w", err)
	}
	if err := os.WriteFile(envPath, BuildSandboxSecretBindingsEnv(existing, values), 0o600); err != nil {
		return fmt.Errorf("writing sandbox env file: %w", err)
	}
	return nil
}

func BuildSandboxSecretBindingsEnv(existing []byte, values map[string]string) []byte {
	text := normalizeEnvFile(existing)
	lines := []string{}
	if text != "" {
		lines = stripManagedSecretEnvBlock(strings.Split(text, "\n"))
	}
	lines = trimTrailingEmptyLines(lines)

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines,
			managedSecretEnvBlockStart,
			"# Generated from sandbox secret bindings. Values may be overwritten by sky10.",
		)
		for _, key := range keys {
			lines = append(lines, formatEnvAssignment(key, values[key]))
		}
		lines = append(lines, managedSecretEnvBlockEnd)
	}

	if len(lines) == 0 {
		return []byte{}
	}
	lines = append(lines, "")
	return []byte(strings.Join(lines, "\n"))
}

func stripManagedSecretEnvBlock(lines []string) []string {
	out := make([]string, 0, len(lines))
	inBlock := false
	for _, line := range lines {
		switch strings.TrimSpace(line) {
		case managedSecretEnvBlockStart:
			inBlock = true
			continue
		case managedSecretEnvBlockEnd:
			inBlock = false
			continue
		}
		if inBlock {
			continue
		}
		out = append(out, line)
	}
	return out
}

func trimTrailingEmptyLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func normalizeSecretBindingEnv(value string) (string, error) {
	env := strings.TrimSpace(value)
	if env == "" {
		return "", fmt.Errorf("env is required")
	}
	if !sandboxSecretEnvKeyPattern.MatchString(env) {
		return "", fmt.Errorf("env %q is not a valid environment variable name", env)
	}
	return env, nil
}

func sortSecretBindings(bindings []SecretBinding) {
	sort.Slice(bindings, func(i, j int) bool {
		return bindings[i].Env < bindings[j].Env
	})
}

func (m *Manager) normalizeSecretBindings(ctx context.Context, bindings []SecretBinding, now string) ([]SecretBinding, error) {
	if len(bindings) == 0 {
		return nil, nil
	}

	seen := map[string]bool{}
	normalized := make([]SecretBinding, 0, len(bindings))
	for _, binding := range bindings {
		env, err := normalizeSecretBindingEnv(binding.Env)
		if err != nil {
			return nil, err
		}
		if seen[env] {
			return nil, fmt.Errorf("duplicate secret binding env %q", env)
		}
		seen[env] = true

		secret := strings.TrimSpace(binding.Secret)
		if secret == "" {
			return nil, fmt.Errorf("secret is required for %s", env)
		}
		if err := m.ensureSecretResolvable(ctx, secret); err != nil {
			return nil, err
		}

		createdAt := strings.TrimSpace(binding.CreatedAt)
		if createdAt == "" {
			createdAt = now
		}
		updatedAt := strings.TrimSpace(binding.UpdatedAt)
		if updatedAt == "" {
			updatedAt = now
		}
		normalized = append(normalized, SecretBinding{
			Env:       env,
			Secret:    secret,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		})
	}
	sortSecretBindings(normalized)
	return normalized, nil
}

func (m *Manager) normalizeCreateSecretBindings(ctx context.Context, template, model string, bindings []SecretBinding, now string) ([]SecretBinding, error) {
	normalized, err := m.normalizeSecretBindings(ctx, bindings, now)
	if err != nil {
		return nil, err
	}
	return m.withDefaultProviderSecretBindings(ctx, template, model, normalized, now), nil
}

func (m *Manager) withDefaultProviderSecretBindings(ctx context.Context, template, model string, bindings []SecretBinding, now string) []SecretBinding {
	if !isOpenClawTemplate(template) && !isHermesTemplate(template) {
		return bindings
	}
	if !modelUsesAnthropic(model) {
		return bindings
	}
	return m.withDefaultProviderSecretBinding(ctx, bindings, now, anthropicProviderSecretSpec)
}

func (m *Manager) withDefaultProviderSecretBinding(ctx context.Context, bindings []SecretBinding, now string, spec providerSecretSpec) []SecretBinding {
	if m.secretLookup == nil {
		return bindings
	}
	env := strings.TrimSpace(spec.envKey)
	if env == "" {
		return bindings
	}
	for _, binding := range bindings {
		if binding.Env == env {
			return bindings
		}
	}
	secret, ok := m.findDefaultProviderSecret(ctx, spec)
	if !ok {
		return bindings
	}
	next := append([]SecretBinding(nil), bindings...)
	next = append(next, SecretBinding{
		Env:       env,
		Secret:    secret,
		CreatedAt: now,
		UpdatedAt: now,
	})
	sortSecretBindings(next)
	return next
}

func (m *Manager) findDefaultProviderSecret(ctx context.Context, spec providerSecretSpec) (string, bool) {
	for _, candidate := range spec.candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, err := m.secretLookup(ctx, candidate); err == nil {
			return candidate, true
		}
	}
	return "", false
}

func modelUsesAnthropic(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return model == "" || strings.HasPrefix(model, "anthropic/") || strings.HasPrefix(model, "claude-")
}
