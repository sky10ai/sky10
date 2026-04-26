package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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
		provisioner.params.SecretBindings[0].Secret != "voice-provider-api-key" {
		t.Fatalf("secret bindings = %#v, want ElevenLabs binding", provisioner.params.SecretBindings)
	}
	if !sandboxFilesContain(provisioner.params.Files, "compose.yaml") ||
		!sandboxFilesContain(provisioner.params.Files, "agent-manifest.json") {
		t.Fatalf("files = %#v, want compose.yaml and agent-manifest.json", provisioner.params.Files)
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
