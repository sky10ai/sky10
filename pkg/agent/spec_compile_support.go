package agent

import (
	"fmt"
	"regexp"
	"strings"
)

var specSlugPattern = regexp.MustCompile(`[a-z0-9]+`)

func sandboxActionSecretBindings(bindings []AgentCompiledSecretBinding) []map[string]string {
	result := make([]map[string]string, 0, len(bindings))
	for _, binding := range bindings {
		result = append(result, map[string]string{
			"env":    binding.Env,
			"secret": binding.Secret,
		})
	}
	return result
}

func sandboxActionFiles(spec AgentSpec, runtime AgentCompiledRuntime, bindings []AgentCompiledSecretBinding) []map[string]string {
	files, _, err := compileSpecFiles(spec, runtime, bindings)
	if err != nil {
		return nil
	}
	result := make([]map[string]string, 0, len(files))
	for _, file := range files {
		result = append(result, map[string]string{
			"path":    file.Path,
			"mode":    file.Mode,
			"content": file.Content,
		})
	}
	return result
}

func runtimeNeedsGeneratedCompose(runtime AgentCompiledRuntime) bool {
	return len(runtime.Packages) > 0 || len(runtime.Containers) > 0
}

func composeServiceName(name, harness string) string {
	serviceName := compileSlug(name)
	if serviceName == "" {
		serviceName = serviceNameForHarness(harness)
	}
	return serviceName
}

func composePackageArg(packages []string) string {
	parts := make([]string, 0, len(packages))
	for _, pkg := range packages {
		pkg = strings.TrimSpace(pkg)
		if pkg != "" {
			parts = append(parts, pkg)
		}
	}
	return strings.Join(parts, " ")
}

func normalizeRuntimePackages(packages []string) []string {
	normalized := make([]string, 0, len(packages))
	for _, pkg := range packages {
		pkg = strings.TrimSpace(pkg)
		if pkg != "" {
			normalized = append(normalized, pkg)
		}
	}
	return normalized
}

func registerActionTools(tools []AgentToolSpec) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(tools))
	for _, tool := range tools {
		capability := strings.TrimSpace(tool.Capability)
		if capability == "" {
			capability = tool.Name
		}
		result = append(result, map[string]interface{}{
			"name":               tool.Name,
			"capability":         capability,
			"audience":           tool.Audience,
			"scope":              tool.Scope,
			"input_schema":       normalizeJSONObject(tool.InputSchema),
			"output_schema":      normalizeJSONObject(tool.OutputSchema),
			"stream_schema":      normalizeJSONObject(tool.StreamSchema),
			"effects":            tool.Effects,
			"pricing":            tool.Pricing,
			"supports_cancel":    tool.SupportsCancel,
			"supports_streaming": tool.SupportsStreaming,
		})
	}
	return result
}

func compileTools(tools []AgentToolSpec) []AgentToolSpec {
	compiled := make([]AgentToolSpec, 0, len(tools))
	for _, tool := range tools {
		next := tool
		next.InputSchema = normalizeJSONObject(tool.InputSchema)
		next.OutputSchema = normalizeJSONObject(tool.OutputSchema)
		next.StreamSchema = normalizeJSONObject(tool.StreamSchema)
		next.Meta = normalizeJSONObject(tool.Meta)
		compiled = append(compiled, next)
	}
	return compiled
}

func normalizeJSONObject(value map[string]interface{}) map[string]interface{} {
	if value == nil {
		return nil
	}
	normalized, ok := normalizeJSONValue(value).(map[string]interface{})
	if !ok {
		return map[string]interface{}{}
	}
	return normalized
}

func normalizeJSONValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, nested := range v {
			out[key] = normalizeJSONValue(nested)
		}
		return out
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, nested := range v {
			out[fmt.Sprint(key)] = normalizeJSONValue(nested)
		}
		return out
	case []interface{}:
		out := make([]interface{}, 0, len(v))
		for _, nested := range v {
			out = append(out, normalizeJSONValue(nested))
		}
		return out
	default:
		return value
	}
}

func harnessFromTemplate(template string) string {
	value := strings.ToLower(template)
	switch {
	case strings.Contains(value, "codex"):
		return "codex"
	case strings.Contains(value, "dexter"):
		return "dexter"
	case strings.Contains(value, "openclaw"):
		return defaultAgentHarness
	default:
		return defaultAgentHarness
	}
}

func defaultTemplateForHarness(harness string) string {
	switch harness {
	case "codex":
		return "codex-docker"
	case "dexter":
		return "dexter-docker"
	case "", defaultAgentHarness:
		return defaultSandboxTemplate
	default:
		return ""
	}
}

func defaultImageForHarness(harness string) string {
	switch harness {
	case "dexter":
		return "oven/bun:1.1"
	default:
		return "ubuntu:24.04"
	}
}

func serviceNameForHarness(harness string) string {
	switch harness {
	case "codex":
		return "codex-worker"
	case "dexter":
		return "dexter-worker"
	case "openclaw":
		return "openclaw-worker"
	default:
		return "agent-worker"
	}
}

func compileSlug(value string) string {
	parts := specSlugPattern.FindAllString(strings.ToLower(value), -1)
	return strings.Join(parts, "-")
}

func quoteYAML(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	value = strings.ReplaceAll(value, "\n", "\\n")
	return "\"" + value + "\""
}
