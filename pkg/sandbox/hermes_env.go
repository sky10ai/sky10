package sandbox

import (
	"bytes"
	"strings"
)

func BuildHermesSharedEnv(existing []byte) []byte {
	text := normalizeEnvFile(existing)
	if strings.TrimSpace(text) == "" {
		return []byte(strings.Join([]string{
			"# Optional provider keys for Hermes inside Lima.",
			"# Hermes reads ~/.hermes/.env, which is linked to this shared file.",
			"# Add the providers you plan to use, then start Hermes with `hermes-shared` inside the guest.",
			"",
			"# OPENAI_API_KEY=",
			"# ANTHROPIC_API_KEY=",
			"# OPENROUTER_API_KEY=",
			"",
		}, "\n"))
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return []byte(text)
}

func normalizeEnvFile(existing []byte) string {
	if len(bytes.TrimSpace(existing)) == 0 {
		return ""
	}
	text := strings.ReplaceAll(string(existing), "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}
