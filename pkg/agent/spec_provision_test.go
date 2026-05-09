package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/config"
	skysandbox "github.com/sky10/sky10/pkg/sandbox"
)

type fakeSandboxProvisioner struct {
	called bool
	params skysandbox.CreateParams
	record skysandbox.Record
	err    error
}

func (f *fakeSandboxProvisioner) Create(_ context.Context, params skysandbox.CreateParams) (*skysandbox.Record, error) {
	f.called = true
	f.params = params
	if f.err != nil {
		return nil, f.err
	}
	if f.record.Name == "" {
		f.record = skysandbox.Record{
			Name:      params.Name,
			Slug:      compileSlug(params.Name),
			Provider:  params.Provider,
			Template:  params.Template,
			Status:    "creating",
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		}
	}
	return &f.record, nil
}

func TestAgentSpecProvisionCreatesSandboxWithFilesAndSecretBindings(t *testing.T) {
	spec := BuildAgentSpec(canonicalMediaAccentPrompt, time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC))
	spec.Status = SpecStatusApproved
	spec.ApprovedAt = spec.UpdatedAt

	provisioner := &fakeSandboxProvisioner{}
	h := newTestRPCHandler(t, newTestRegistry(), nil)
	h.SetSandboxProvisioner(provisioner)

	params, err := json.Marshal(AgentSpecProvisionParams{Spec: &spec})
	if err != nil {
		t.Fatalf("Marshal provision params: %v", err)
	}
	raw, err, handled := h.Dispatch(context.Background(), "agent.spec.provision", params)
	if err != nil {
		t.Fatalf("Dispatch(agent.spec.provision) error: %v", err)
	}
	if !handled {
		t.Fatal("agent.spec.provision handled = false, want true")
	}
	result := raw.(*AgentSpecProvisionResult)
	if result.Sandbox == nil || result.Sandbox.Status != "creating" {
		t.Fatalf("sandbox result = %#v, want creating sandbox", result.Sandbox)
	}
	if !provisioner.called {
		t.Fatal("sandbox provisioner was not called")
	}
	if provisioner.params.Name != "media-accent-agent" ||
		provisioner.params.Provider != "lima" ||
		provisioner.params.Template != "openclaw-docker" {
		t.Fatalf("sandbox params = %#v, want media accent openclaw sandbox", provisioner.params)
	}
	if len(provisioner.params.SecretBindings) != 1 ||
		provisioner.params.SecretBindings[0].Env != "ELEVENLABS_API_KEY" ||
		provisioner.params.SecretBindings[0].Secret != "ELEVENLABS_API_KEY" {
		t.Fatalf("secret bindings = %#v, want ElevenLabs binding", provisioner.params.SecretBindings)
	}
	if !sandboxFilesContain(provisioner.params.Files, "compose.yaml") ||
		!sandboxFilesContain(provisioner.params.Files, "agent-manifest.json") ||
		!sandboxFilesContain(provisioner.params.Files, "workspace/AGENTS.md") {
		t.Fatalf("files = %#v, want compose.yaml, agent-manifest.json, and workspace/AGENTS.md", provisioner.params.Files)
	}
}

func TestAgentCreateApprovesAndProvisionsFromPrompt(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	store := NewSpecStore(nil)
	provisioner := &fakeSandboxProvisioner{}
	h := newTestRPCHandler(t, newTestRegistry(), nil)
	h.SetSpecStore(store)
	h.SetSandboxProvisioner(provisioner)

	params, err := json.Marshal(AgentCreateParams{Prompt: canonicalMediaAccentPrompt})
	if err != nil {
		t.Fatalf("Marshal create params: %v", err)
	}
	raw, err, handled := h.Dispatch(context.Background(), "agent.create", params)
	if err != nil {
		t.Fatalf("Dispatch(agent.create) error: %v", err)
	}
	if !handled {
		t.Fatal("agent.create handled = false, want true")
	}
	result := raw.(*AgentCreateResult)
	if result.Spec.Status != SpecStatusApproved || result.Spec.ApprovedAt == "" {
		t.Fatalf("created spec = %#v, want approved spec", result.Spec)
	}
	if result.Sandbox == nil || result.Sandbox.Status != "creating" {
		t.Fatalf("sandbox result = %#v, want creating sandbox", result.Sandbox)
	}
	if result.Compile == nil || result.Compile.Runtime.Template != "openclaw-docker" {
		t.Fatalf("compile result = %#v, want openclaw-docker runtime", result.Compile)
	}
	if !provisioner.called {
		t.Fatal("sandbox provisioner was not called")
	}

	list, err := store.List(context.Background(), AgentSpecListParams{Limit: 10})
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(list.Specs) != 1 || list.Specs[0].Status != SpecStatusApproved {
		t.Fatalf("stored specs = %#v, want one approved spec", list.Specs)
	}
}

func TestAgentCreateUsesProvidedNameForProvisionedSandbox(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	store := NewSpecStore(nil)
	provisioner := &fakeSandboxProvisioner{}
	h := newTestRPCHandler(t, newTestRegistry(), nil)
	h.SetSpecStore(store)
	h.SetSandboxProvisioner(provisioner)

	params, err := json.Marshal(AgentCreateParams{
		Prompt: canonicalMediaAccentPrompt,
		Name:   "British Accent Pro",
	})
	if err != nil {
		t.Fatalf("Marshal create params: %v", err)
	}
	raw, err, handled := h.Dispatch(context.Background(), "agent.create", params)
	if err != nil {
		t.Fatalf("Dispatch(agent.create) error: %v", err)
	}
	if !handled {
		t.Fatal("agent.create handled = false, want true")
	}
	result := raw.(*AgentCreateResult)
	if result.Spec.Name != "british-accent-pro" {
		t.Fatalf("created name = %q, want british-accent-pro", result.Spec.Name)
	}
	if provisioner.params.Name != "british-accent-pro" {
		t.Fatalf("sandbox name = %q, want british-accent-pro", provisioner.params.Name)
	}
}

func TestAgentSpecProvisionRejectsUnapprovedSpecs(t *testing.T) {
	spec := BuildAgentSpec(canonicalMediaAccentPrompt, time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC))
	provisioner := &fakeSandboxProvisioner{}
	h := newTestRPCHandler(t, newTestRegistry(), nil)
	h.SetSandboxProvisioner(provisioner)

	params, err := json.Marshal(AgentSpecProvisionParams{Spec: &spec})
	if err != nil {
		t.Fatalf("Marshal provision params: %v", err)
	}
	_, err, handled := h.Dispatch(context.Background(), "agent.spec.provision", params)
	if err == nil || !strings.Contains(err.Error(), "must be approved") {
		t.Fatalf("Dispatch(agent.spec.provision) error = %v, want approval error", err)
	}
	if !handled {
		t.Fatal("agent.spec.provision handled = false, want true")
	}
	if provisioner.called {
		t.Fatal("sandbox provisioner was called for an unapproved spec")
	}
}

func sandboxFilesContain(files []skysandbox.SharedFile, path string) bool {
	for _, file := range files {
		if file.Path == path {
			return true
		}
	}
	return false
}
