package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var ErrProviderSecretNotFound = errors.New("provider secret not found")

type ProviderSecretLookup func(ctx context.Context, idOrName string) ([]byte, error)

type providerSecretSpec struct {
	envKey     string
	candidates []string
}

var anthropicProviderSecretSpec = providerSecretSpec{
	envKey:     "ANTHROPIC_API_KEY",
	candidates: []string{"ANTHROPIC_API_KEY", "anthropic", "anthropic-api-key"},
}

var openClawProviderSecretSpecs = []providerSecretSpec{
	anthropicProviderSecretSpec,
	{
		envKey:     "OPENAI_API_KEY",
		candidates: []string{"OPENAI_API_KEY", "openai", "openai-api-key"},
	},
}

var envAssignmentPattern = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)=`)

func ResolveOpenClawProviderEnv(ctx context.Context, lookup ProviderSecretLookup) (map[string]string, error) {
	return resolveProviderEnv(ctx, lookup, openClawProviderSecretSpecs)
}

func resolveProviderEnv(ctx context.Context, lookup ProviderSecretLookup, specs []providerSecretSpec) (map[string]string, error) {
	if lookup == nil {
		return map[string]string{}, nil
	}

	values := make(map[string]string, len(specs))
	for _, spec := range specs {
		value, err := resolveProviderEnvValue(ctx, lookup, spec.candidates)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", spec.envKey, err)
		}
		if value != "" {
			values[spec.envKey] = value
		}
	}
	return values, nil
}

func resolveProviderEnvValue(ctx context.Context, lookup ProviderSecretLookup, candidates []string) (string, error) {
	for _, candidate := range candidates {
		payload, err := lookup(ctx, candidate)
		if err == nil {
			return strings.TrimRight(string(payload), "\r\n"), nil
		}
		if errors.Is(err, ErrProviderSecretNotFound) {
			continue
		}
		return "", err
	}
	return "", nil
}

func BuildOpenClawSharedEnv(existing []byte, resolved map[string]string) []byte {
	return buildSharedEnv(existing, resolved, openClawProviderSecretSpecs, []string{
		"# Optional provider keys for OpenClaw inside Lima.",
		"# ANTHROPIC_API_KEY is normally projected through sandbox secret bindings.",
		"# Other host provider secrets merge here when available.",
	})
}

func buildSharedEnv(existing []byte, resolved map[string]string, specs []providerSecretSpec, header []string) []byte {
	if len(resolved) == 0 {
		resolved = map[string]string{}
	}

	if len(bytes.TrimSpace(existing)) == 0 {
		lines := append([]string(nil), header...)
		for _, spec := range specs {
			lines = append(lines, formatEnvAssignment(spec.envKey, resolved[spec.envKey]))
		}
		lines = append(lines, "")
		return []byte(strings.Join(lines, "\n"))
	}

	text := normalizeEnvFile(existing)
	lines := strings.Split(text, "\n")
	seen := map[string]bool{}

	for i, line := range lines {
		match := envAssignmentPattern.FindStringSubmatch(line)
		if len(match) != 2 {
			continue
		}
		key := match[1]
		if !isProviderEnvKey(key, specs) {
			continue
		}
		seen[key] = true
		if value, ok := resolved[key]; ok {
			lines[i] = formatEnvAssignment(key, value)
		}
	}

	for _, spec := range specs {
		if seen[spec.envKey] {
			continue
		}
		if value, ok := resolved[spec.envKey]; ok {
			lines = append(lines, formatEnvAssignment(spec.envKey, value))
		}
	}

	if len(lines) == 0 || lines[len(lines)-1] != "" {
		lines = append(lines, "")
	}
	return []byte(strings.Join(lines, "\n"))
}

func isProviderEnvKey(key string, specs []providerSecretSpec) bool {
	for _, spec := range specs {
		if spec.envKey == key {
			return true
		}
	}
	return false
}

func formatEnvAssignment(key, value string) string {
	if value == "" {
		return key + "="
	}
	if strings.ContainsAny(value, " \t\r\n#\"'\\") {
		escaped := strings.NewReplacer(
			"\\", "\\\\",
			`"`, `\"`,
			"\n", `\n`,
			"\r", `\r`,
		).Replace(value)
		return fmt.Sprintf(`%s="%s"`, key, escaped)
	}
	return key + "=" + value
}
