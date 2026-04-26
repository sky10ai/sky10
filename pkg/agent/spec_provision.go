package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	skysandbox "github.com/sky10/sky10/pkg/sandbox"
)

type AgentSpecProvisionParams struct {
	ID   string     `json:"id,omitempty"`
	Spec *AgentSpec `json:"spec,omitempty"`
}

type AgentSpecProvisionResult struct {
	Spec    AgentSpec               `json:"spec"`
	Compile *AgentSpecCompileResult `json:"compile"`
	Sandbox *skysandbox.Record      `json:"sandbox,omitempty"`
}

func (h *RPCHandler) rpcSpecProvision(ctx context.Context, params json.RawMessage) (interface{}, error) {
	if h.sandbox == nil {
		return nil, fmt.Errorf("sandbox provisioner is not configured")
	}
	spec, err := h.resolveSpecForRuntimeAction(ctx, params)
	if err != nil {
		return nil, err
	}
	if spec.Status != SpecStatusApproved {
		return nil, fmt.Errorf("agent spec %q must be approved before provisioning", spec.ID)
	}

	compiled, err := CompileAgentSpec(spec)
	if err != nil {
		return nil, err
	}
	if compiled.Runtime.Target != "sandbox" {
		return nil, fmt.Errorf("agent spec %q targets %q; only sandbox provisioning is supported", spec.ID, compiled.Runtime.Target)
	}

	rec, err := h.sandbox.Create(ctx, skysandbox.CreateParams{
		Name:           compiled.Runtime.Name,
		Provider:       compiled.Runtime.Provider,
		Template:       compiled.Runtime.Template,
		SecretBindings: sandboxSecretBindings(compiled.SecretBindings),
		Files:          sandboxSharedFiles(compiled.Files),
	})
	if err != nil {
		return nil, err
	}
	return &AgentSpecProvisionResult{
		Spec:    spec,
		Compile: compiled,
		Sandbox: rec,
	}, nil
}

func (h *RPCHandler) resolveSpecForRuntimeAction(ctx context.Context, params json.RawMessage) (AgentSpec, error) {
	var p AgentSpecProvisionParams
	if len(params) > 0 && string(params) != "null" {
		if err := json.Unmarshal(params, &p); err != nil {
			return AgentSpec{}, fmt.Errorf("invalid params: %w", err)
		}
	}
	if p.Spec != nil {
		return *p.Spec, nil
	}

	store, err := h.requireSpecStore()
	if err != nil {
		return AgentSpec{}, err
	}
	result, err := store.Get(ctx, p.ID)
	if err != nil {
		return AgentSpec{}, err
	}
	return result.Spec, nil
}

func sandboxSecretBindings(bindings []AgentCompiledSecretBinding) []skysandbox.SecretBinding {
	result := make([]skysandbox.SecretBinding, 0, len(bindings))
	for _, binding := range bindings {
		result = append(result, skysandbox.SecretBinding{
			Env:    binding.Env,
			Secret: binding.Secret,
		})
	}
	return result
}

func sandboxSharedFiles(files []AgentCompiledFile) []skysandbox.SharedFile {
	result := make([]skysandbox.SharedFile, 0, len(files))
	for _, file := range files {
		result = append(result, skysandbox.SharedFile{
			Path:    strings.TrimSpace(file.Path),
			Mode:    strings.TrimSpace(file.Mode),
			Content: file.Content,
		})
	}
	return result
}
