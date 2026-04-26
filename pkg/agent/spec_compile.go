package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	compiledFileMode = "0644"
)

type AgentSpecCompileParams struct {
	ID   string     `json:"id,omitempty"`
	Spec *AgentSpec `json:"spec,omitempty"`
}

type AgentSpecCompileResult struct {
	Spec           AgentSpec                    `json:"spec"`
	Runtime        AgentCompiledRuntime         `json:"runtime"`
	Files          []AgentCompiledFile          `json:"files"`
	SecretBindings []AgentCompiledSecretBinding `json:"secret_bindings,omitempty"`
	Actions        []AgentProvisionAction       `json:"actions"`
	Warnings       []string                     `json:"warnings,omitempty"`
}

type AgentCompiledRuntime struct {
	Name       string               `json:"name"`
	Slug       string               `json:"slug"`
	Target     string               `json:"target"`
	Provider   string               `json:"provider,omitempty"`
	Template   string               `json:"template,omitempty"`
	Harness    string               `json:"harness,omitempty"`
	Containers []AgentContainerSpec `json:"containers,omitempty"`
}

type AgentCompiledFile struct {
	Path    string `json:"path"`
	Mode    string `json:"mode"`
	Content string `json:"content"`
}

type AgentCompiledSecretBinding struct {
	Env         string `json:"env"`
	Secret      string `json:"secret"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

type AgentProvisionAction struct {
	Method  string                 `json:"method"`
	Summary string                 `json:"summary"`
	Params  map[string]interface{} `json:"params,omitempty"`
}

func CompileAgentSpec(spec AgentSpec) (*AgentSpecCompileResult, error) {
	if err := validateAgentSpec(spec); err != nil {
		return nil, err
	}

	runtime := compileRuntime(spec)
	secretBindings := compileSecretBindings(spec.Secrets)
	files, warnings, err := compileSpecFiles(spec, runtime, secretBindings)
	if err != nil {
		return nil, err
	}
	actions := compileProvisionActions(spec, runtime, secretBindings)

	return &AgentSpecCompileResult{
		Spec:           spec,
		Runtime:        runtime,
		Files:          files,
		SecretBindings: secretBindings,
		Actions:        actions,
		Warnings:       warnings,
	}, nil
}

func compileRuntime(spec AgentSpec) AgentCompiledRuntime {
	runtime := spec.Runtime
	harness := strings.TrimSpace(runtime.Harness)
	if harness == "" {
		harness = harnessFromTemplate(runtime.Template)
	}
	provider := strings.TrimSpace(runtime.Provider)
	if provider == "" && runtime.Target == "sandbox" {
		provider = "lima"
	}
	template := strings.TrimSpace(runtime.Template)
	if template == "" && runtime.Target == "sandbox" {
		template = defaultTemplateForHarness(harness)
	}
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		name = spec.ID
	}
	slug := compileSlug(name)
	if slug == "" {
		slug = compileSlug(spec.ID)
	}
	if slug == "" {
		slug = "agent"
	}

	containers := append([]AgentContainerSpec(nil), runtime.Containers...)
	if runtime.Target == "sandbox" && len(containers) == 0 {
		containers = []AgentContainerSpec{{
			Name:     serviceNameForHarness(harness),
			Image:    defaultImageForHarness(harness),
			Packages: append([]string(nil), runtime.Packages...),
		}}
	}
	for i := range containers {
		containers[i].Name = strings.TrimSpace(containers[i].Name)
		if containers[i].Name == "" {
			containers[i].Name = serviceNameForHarness(harness)
		}
		if strings.TrimSpace(containers[i].Image) == "" {
			containers[i].Image = defaultImageForHarness(harness)
		}
	}

	return AgentCompiledRuntime{
		Name:       name,
		Slug:       slug,
		Target:     runtime.Target,
		Provider:   provider,
		Template:   template,
		Harness:    harness,
		Containers: containers,
	}
}

func compileSecretBindings(secrets []AgentSecretSpec) []AgentCompiledSecretBinding {
	bindings := make([]AgentCompiledSecretBinding, 0, len(secrets))
	for _, secret := range secrets {
		env := strings.TrimSpace(secret.Env)
		name := strings.TrimSpace(secret.Name)
		if env == "" || name == "" {
			continue
		}
		bindings = append(bindings, AgentCompiledSecretBinding{
			Env:         env,
			Secret:      name,
			Required:    secret.Required,
			Description: strings.TrimSpace(secret.Description),
		})
	}
	sort.SliceStable(bindings, func(i, j int) bool {
		return bindings[i].Env < bindings[j].Env
	})
	return bindings
}

func compileSpecFiles(spec AgentSpec, runtime AgentCompiledRuntime, secretBindings []AgentCompiledSecretBinding) ([]AgentCompiledFile, []string, error) {
	var warnings []string
	files := []AgentCompiledFile{
		{Path: "agent-manifest.json", Mode: compiledFileMode},
		{Path: ".env.example", Mode: compiledFileMode, Content: compileEnvExample(spec, secretBindings)},
		{Path: "README.md", Mode: compiledFileMode, Content: compileREADME(spec, runtime, secretBindings)},
	}

	manifest, err := compileManifestJSON(spec, runtime, secretBindings)
	if err != nil {
		return nil, nil, err
	}
	files[0].Content = manifest

	if runtime.Target == "sandbox" {
		files = append(files, AgentCompiledFile{
			Path:    "compose.yaml",
			Mode:    compiledFileMode,
			Content: compileComposeYAML(spec, runtime, secretBindings),
		})
		if runtime.Template == "" {
			warnings = append(warnings, "runtime.template is empty; provisioning will need an explicit sandbox template")
		}
	}

	sort.SliceStable(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files, warnings, nil
}

func compileManifestJSON(spec AgentSpec, runtime AgentCompiledRuntime, secretBindings []AgentCompiledSecretBinding) (string, error) {
	manifest := struct {
		Spec           string                       `json:"spec"`
		ID             string                       `json:"id"`
		Name           string                       `json:"name"`
		Description    string                       `json:"description"`
		Runtime        AgentCompiledRuntime         `json:"runtime"`
		Fulfillment    AgentFulfillment             `json:"fulfillment"`
		Tools          []AgentToolSpec              `json:"tools"`
		Inputs         []AgentIOSpec                `json:"inputs"`
		Outputs        []AgentIOSpec                `json:"outputs"`
		SecretBindings []AgentCompiledSecretBinding `json:"secret_bindings,omitempty"`
		Permissions    []string                     `json:"permissions"`
		Commerce       AgentCommerceSpec            `json:"commerce"`
		JobPolicy      AgentJobPolicy               `json:"job_policy"`
		PublishPolicy  AgentPublishPolicy           `json:"publish_policy"`
		Prompt         string                       `json:"prompt"`
	}{
		Spec:           spec.Spec,
		ID:             spec.ID,
		Name:           spec.Name,
		Description:    spec.Description,
		Runtime:        runtime,
		Fulfillment:    spec.Fulfillment,
		Tools:          compileTools(spec.Tools),
		Inputs:         spec.Inputs,
		Outputs:        spec.Outputs,
		SecretBindings: secretBindings,
		Permissions:    spec.Permissions,
		Commerce:       spec.Commerce,
		JobPolicy:      spec.JobPolicy,
		PublishPolicy:  spec.PublishPolicy,
		Prompt:         spec.Prompt,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal compiled agent manifest: %w", err)
	}
	return string(data) + "\n", nil
}

func compileEnvExample(spec AgentSpec, secretBindings []AgentCompiledSecretBinding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# sky10 secret bindings for %s\n", spec.Name)
	b.WriteString("# Values are projected from the sky10 secret store at runtime.\n")
	if len(secretBindings) == 0 {
		b.WriteString("# No secret bindings required by this spec.\n")
		return b.String()
	}
	for _, binding := range secretBindings {
		if binding.Description != "" {
			fmt.Fprintf(&b, "# %s\n", binding.Description)
		}
		fmt.Fprintf(&b, "%s=\n", binding.Env)
	}
	return b.String()
}

func compileREADME(spec AgentSpec, runtime AgentCompiledRuntime, secretBindings []AgentCompiledSecretBinding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", spec.Name)
	if spec.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", spec.Description)
	}
	fmt.Fprintf(&b, "Spec: `%s`\n", spec.Spec)
	fmt.Fprintf(&b, "Runtime: `%s`", runtime.Target)
	if runtime.Provider != "" {
		fmt.Fprintf(&b, " via `%s`", runtime.Provider)
	}
	if runtime.Template != "" {
		fmt.Fprintf(&b, " using `%s`", runtime.Template)
	}
	if runtime.Harness != "" {
		fmt.Fprintf(&b, " with `%s`", runtime.Harness)
	}
	b.WriteString("\n\n")

	b.WriteString("## Tools\n\n")
	for _, tool := range spec.Tools {
		capability := strings.TrimSpace(tool.Capability)
		if capability == "" {
			capability = tool.Name
		}
		fmt.Fprintf(&b, "- `%s` (%s): %s\n", tool.Name, capability, tool.Description)
	}

	if len(spec.Inputs) > 0 {
		b.WriteString("\n## Inputs\n\n")
		for _, input := range spec.Inputs {
			required := "optional"
			if input.Required {
				required = "required"
			}
			fmt.Fprintf(&b, "- `%s` (%s): %s\n", input.Kind, required, input.Description)
		}
	}

	if len(spec.Outputs) > 0 {
		b.WriteString("\n## Outputs\n\n")
		for _, output := range spec.Outputs {
			required := "optional"
			if output.Required {
				required = "required"
			}
			fmt.Fprintf(&b, "- `%s` (%s): %s\n", output.Kind, required, output.Description)
		}
	}

	b.WriteString("\n## Secret bindings\n\n")
	if len(secretBindings) == 0 {
		b.WriteString("- None\n")
	} else {
		for _, binding := range secretBindings {
			required := "optional"
			if binding.Required {
				required = "required"
			}
			fmt.Fprintf(&b, "- `%s` from sky10 secret `%s` (%s)\n", binding.Env, binding.Secret, required)
		}
	}
	return b.String()
}

func compileComposeYAML(spec AgentSpec, runtime AgentCompiledRuntime, secretBindings []AgentCompiledSecretBinding) string {
	var b strings.Builder
	b.WriteString("services:\n")
	for _, container := range runtime.Containers {
		serviceName := compileSlug(container.Name)
		if serviceName == "" {
			serviceName = serviceNameForHarness(runtime.Harness)
		}
		fmt.Fprintf(&b, "  %s:\n", serviceName)
		fmt.Fprintf(&b, "    image: %s\n", quoteYAML(container.Image))
		b.WriteString("    working_dir: /workspace\n")
		b.WriteString("    command: [\"sh\", \"-lc\", \"sleep infinity\"]\n")
		b.WriteString("    env_file:\n")
		b.WriteString("      - .env\n")
		if len(secretBindings) > 0 {
			b.WriteString("    environment:\n")
			for _, binding := range secretBindings {
				fmt.Fprintf(&b, "      - %s=${%s}\n", binding.Env, binding.Env)
			}
		}
		b.WriteString("    volumes:\n")
		b.WriteString("      - ./agent:/workspace/agent\n")
		b.WriteString("      - ./input:/workspace/input\n")
		b.WriteString("      - ./output:/workspace/output\n")
		b.WriteString("    labels:\n")
		fmt.Fprintf(&b, "      - %s\n", quoteYAML("sky10.agent.spec_id="+spec.ID))
		fmt.Fprintf(&b, "      - %s\n", quoteYAML("sky10.agent.name="+spec.Name))
		if runtime.Harness != "" {
			fmt.Fprintf(&b, "      - %s\n", quoteYAML("sky10.agent.harness="+runtime.Harness))
		}
		if len(container.Packages) > 0 {
			b.WriteString("    x-sky10-packages:\n")
			for _, pkg := range container.Packages {
				fmt.Fprintf(&b, "      - %s\n", quoteYAML(pkg))
			}
		}
	}
	return b.String()
}

func compileProvisionActions(spec AgentSpec, runtime AgentCompiledRuntime, secretBindings []AgentCompiledSecretBinding) []AgentProvisionAction {
	actions := []AgentProvisionAction{}
	if runtime.Target == "sandbox" {
		params := map[string]interface{}{
			"name":     runtime.Name,
			"provider": runtime.Provider,
			"template": runtime.Template,
			"files":    sandboxActionFiles(spec, runtime, secretBindings),
		}
		if len(secretBindings) > 0 {
			params["secret_bindings"] = sandboxActionSecretBindings(secretBindings)
		}
		actions = append(actions, AgentProvisionAction{
			Method:  "sandbox.create",
			Summary: "Create and boot the VM template instance with generated files and declared sky10 secrets in place before startup.",
			Params:  params,
		})
	}

	actions = append(actions, AgentProvisionAction{
		Method:  "agent.register",
		Summary: "Register the compiled callable tools once the harness is reachable.",
		Params: map[string]interface{}{
			"name":     spec.Name,
			"key_name": runtime.Slug,
			"tools":    registerActionTools(spec.Tools),
			"runtime": map[string]interface{}{
				"target":   runtime.Target,
				"provider": runtime.Provider,
				"template": runtime.Template,
				"harness":  runtime.Harness,
			},
		},
	})
	return actions
}
