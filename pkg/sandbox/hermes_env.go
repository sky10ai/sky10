package sandbox

import (
	"bytes"
	"context"
	"strings"
)

var hermesProviderSecretSpecs = []providerSecretSpec{
	{
		envKey:     "ANTHROPIC_API_KEY",
		candidates: []string{"ANTHROPIC_API_KEY", "anthropic", "anthropic-api-key"},
	},
	{
		envKey:     "OPENAI_API_KEY",
		candidates: []string{"OPENAI_API_KEY", "openai", "openai-api-key"},
	},
	{
		envKey:     "OPENROUTER_API_KEY",
		candidates: []string{"OPENROUTER_API_KEY", "openrouter", "openrouter-api-key"},
	},
}

func ResolveHermesProviderEnv(ctx context.Context, lookup ProviderSecretLookup) (map[string]string, error) {
	return resolveProviderEnv(ctx, lookup, hermesProviderSecretSpecs)
}

func BuildHermesSharedEnv(existing []byte, resolved map[string]string) []byte {
	return buildSharedEnv(existing, resolved, hermesProviderSecretSpecs, []string{
		"# Optional provider keys for Hermes inside Lima.",
		"# Host secrets named ANTHROPIC_API_KEY/anthropic, OPENAI_API_KEY/openai, and OPENROUTER_API_KEY/openrouter merge in automatically when available.",
		"# Hermes reads ~/.hermes/.env, which is linked to this shared file.",
	})
}

func normalizeEnvFile(existing []byte) string {
	if len(bytes.TrimSpace(existing)) == 0 {
		return ""
	}
	text := strings.ReplaceAll(string(existing), "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}
