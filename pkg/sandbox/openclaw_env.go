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

var openClawProviderSecretSpecs = []providerSecretSpec{
	{
		envKey:     "ANTHROPIC_API_KEY",
		candidates: []string{"ANTHROPIC_API_KEY", "anthropic", "anthropic-api-key"},
	},
	{
		envKey:     "OPENAI_API_KEY",
		candidates: []string{"OPENAI_API_KEY", "openai", "openai-api-key"},
	},
}

var envAssignmentPattern = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)=`)

func ResolveOpenClawProviderEnv(ctx context.Context, lookup ProviderSecretLookup) (map[string]string, error) {
	if lookup == nil {
		return map[string]string{}, nil
	}

	values := make(map[string]string, len(openClawProviderSecretSpecs))
	for _, spec := range openClawProviderSecretSpecs {
		value, err := resolveOpenClawProviderEnvValue(ctx, lookup, spec.candidates)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", spec.envKey, err)
		}
		if value != "" {
			values[spec.envKey] = value
		}
	}
	return values, nil
}

func resolveOpenClawProviderEnvValue(ctx context.Context, lookup ProviderSecretLookup, candidates []string) (string, error) {
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
	if len(resolved) == 0 {
		resolved = map[string]string{}
	}

	if len(bytes.TrimSpace(existing)) == 0 {
		lines := []string{
			"# Optional provider keys for OpenClaw inside Lima.",
			"# Host secrets named OPENAI_API_KEY/openai and ANTHROPIC_API_KEY/anthropic merge in automatically when available.",
		}
		for _, spec := range openClawProviderSecretSpecs {
			lines = append(lines, formatEnvAssignment(spec.envKey, resolved[spec.envKey]))
		}
		lines = append(lines, "")
		return []byte(strings.Join(lines, "\n"))
	}

	text := strings.ReplaceAll(string(existing), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	seen := map[string]bool{}

	for i, line := range lines {
		match := envAssignmentPattern.FindStringSubmatch(line)
		if len(match) != 2 {
			continue
		}
		key := match[1]
		if !isOpenClawProviderEnvKey(key) {
			continue
		}
		seen[key] = true
		if value, ok := resolved[key]; ok {
			lines[i] = formatEnvAssignment(key, value)
		}
	}

	for _, spec := range openClawProviderSecretSpecs {
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

func isOpenClawProviderEnvKey(key string) bool {
	for _, spec := range openClawProviderSecretSpecs {
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
