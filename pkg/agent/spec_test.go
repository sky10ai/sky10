package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/config"
	"go.yaml.in/yaml/v2"
)

const canonicalMediaAccentPrompt = "make me an ai agent that can process media files to change the accent to british"

func TestBuildAgentSpecBuildsMediaAccentSpecAsFreePrivateTool(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	spec := BuildAgentSpec(canonicalMediaAccentPrompt, now)

	if spec.Spec != AgentSpecVersion {
		t.Fatalf("spec version = %q, want %q", spec.Spec, AgentSpecVersion)
	}
	if spec.Status != SpecStatusDraft {
		t.Fatalf("status = %q, want draft", spec.Status)
	}
	if spec.Name != "media-accent-agent" {
		t.Fatalf("name = %q, want media-accent-agent", spec.Name)
	}
	if spec.Runtime.Target != "sandbox" || spec.Runtime.Template != "openclaw-docker" {
		t.Fatalf("runtime = %#v, want sandbox openclaw-docker", spec.Runtime)
	}
	if len(spec.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(spec.Tools))
	}
	tool := spec.Tools[0]
	if tool.Name != "media.accent.convert" {
		t.Fatalf("tool name = %q, want media.accent.convert", tool.Name)
	}
	if tool.Audience != "private" || tool.Scope != "current" {
		t.Fatalf("tool exposure = %s/%s, want private/current", tool.Audience, tool.Scope)
	}
	if tool.Pricing.Model != "free" || spec.Commerce.Enabled {
		t.Fatalf("pricing = %#v commerce=%v, want free and disabled", tool.Pricing, spec.Commerce.Enabled)
	}
	if !containsString(tool.Effects, "file.read") || !containsString(tool.Effects, "file.write") {
		t.Fatalf("effects = %#v, want file read/write", tool.Effects)
	}
	if len(spec.Secrets) != 1 || spec.Secrets[0].Env != "ELEVENLABS_API_KEY" {
		t.Fatalf("secrets = %#v, want optional ElevenLabs binding", spec.Secrets)
	}
}

func TestBuildAgentSpecSelectsCodexHarness(t *testing.T) {
	spec := BuildAgentSpec("create a coding agent with codex", time.Now())

	if spec.Runtime.Template != "codex-docker" || spec.Runtime.Harness != "codex" {
		t.Fatalf("runtime = %#v, want codex-docker/codex", spec.Runtime)
	}
	if spec.Tools[0].Capability != "github.fix" {
		t.Fatalf("capability = %q, want github.fix", spec.Tools[0].Capability)
	}
}

func TestBuildAgentSpecSelectsDexterHarness(t *testing.T) {
	spec := BuildAgentSpec("I want a financial agent with Dexter", time.Now())

	if spec.Runtime.Template != "dexter-docker" || spec.Runtime.Harness != "dexter" {
		t.Fatalf("runtime = %#v, want dexter-docker/dexter", spec.Runtime)
	}
	if spec.Tools[0].Capability != "finance.research" {
		t.Fatalf("capability = %q, want finance.research", spec.Tools[0].Capability)
	}
	if !containsString(spec.Permissions, "market_data.read") {
		t.Fatalf("permissions = %#v, want market_data.read", spec.Permissions)
	}
}

func TestBuildAgentSpecCanRepresentOptionalCommerce(t *testing.T) {
	spec := BuildAgentSpec(canonicalMediaAccentPrompt+" and charge $2 per minute", time.Now())

	if !spec.Commerce.Enabled {
		t.Fatal("commerce enabled = false, want true when prompt asks to charge")
	}
	if spec.Tools[0].Pricing.Model != "variable" {
		t.Fatalf("pricing model = %q, want variable", spec.Tools[0].Pricing.Model)
	}
	if spec.Tools[0].Pricing.Rate != "2" {
		t.Fatalf("pricing rate = %q, want 2", spec.Tools[0].Pricing.Rate)
	}
	if !containsString(spec.Tools[0].Effects, "payment.charge") {
		t.Fatalf("effects = %#v, want payment.charge", spec.Tools[0].Effects)
	}
}

func TestSpecStorePersistsLatestSpecSnapshots(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	var emitted []string
	store := NewSpecStore(func(event string, _ interface{}) {
		emitted = append(emitted, event)
	})
	create, err := store.Create(context.Background(), AgentSpecCreateParams{Prompt: canonicalMediaAccentPrompt})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	updated := create.Spec
	updated.Description = "Updated review copy."
	if _, err := store.Update(context.Background(), AgentSpecUpdateParams{Spec: updated}); err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	approved, err := store.Approve(context.Background(), updated.ID)
	if err != nil {
		t.Fatalf("Approve() error: %v", err)
	}
	if approved.Spec.Status != SpecStatusApproved || approved.Spec.ApprovedAt == "" {
		t.Fatalf("approved spec = %#v, want approved with timestamp", approved.Spec)
	}

	list, err := store.List(context.Background(), AgentSpecListParams{Limit: 10})
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(list.Specs) != 1 {
		t.Fatalf("spec count = %d, want 1", len(list.Specs))
	}
	if list.Specs[0].Description != "Updated review copy." || list.Specs[0].Status != SpecStatusApproved {
		t.Fatalf("latest spec = %#v, want updated approved snapshot", list.Specs[0])
	}
	if len(emitted) != 3 || emitted[0] != "agent.spec.changed" {
		t.Fatalf("emitted = %#v, want agent.spec.changed events", emitted)
	}

	path := filepath.Join(os.Getenv(config.EnvHome), "agents", "specs.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error: %v", path, err)
	}
	if got := len(splitNonEmptySpecLines(string(data))); got != 3 {
		t.Fatalf("spec log lines = %d, want 3", got)
	}
	if strings.Contains(string(data), "elevenlabs-key") {
		t.Fatalf("spec log persisted secret payload: %q", string(data))
	}
}

func TestSpecStoreUpdateDoesNotApproveDraft(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	store := NewSpecStore(nil)
	create, err := store.Create(context.Background(), AgentSpecCreateParams{Prompt: canonicalMediaAccentPrompt})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	next := create.Spec
	next.Status = SpecStatusApproved
	next.ApprovedAt = time.Now().UTC().Format(time.RFC3339Nano)

	if _, err := store.Update(context.Background(), AgentSpecUpdateParams{Spec: next}); err == nil {
		t.Fatal("Update() error = nil, want status escalation rejected")
	}

	got, err := store.Get(context.Background(), create.Spec.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.Spec.Status != SpecStatusDraft || got.Spec.ApprovedAt != "" {
		t.Fatalf("stored spec = %#v, want unchanged draft lifecycle", got.Spec)
	}
}

func TestSpecStoreUpdateRejectsApprovedDraft(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	store := NewSpecStore(nil)
	create, err := store.Create(context.Background(), AgentSpecCreateParams{Prompt: canonicalMediaAccentPrompt})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	approved, err := store.Approve(context.Background(), create.Spec.ID)
	if err != nil {
		t.Fatalf("Approve() error: %v", err)
	}
	next := approved.Spec
	next.Description = "late edit"

	if _, err := store.Update(context.Background(), AgentSpecUpdateParams{Spec: next}); err == nil {
		t.Fatal("Update() error = nil, want approved draft rejected")
	}
}

func TestAgentSpecRPCDispatch(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	h.SetSpecStore(NewSpecStore(nil))

	params, err := json.Marshal(AgentSpecCreateParams{Prompt: canonicalMediaAccentPrompt})
	if err != nil {
		t.Fatalf("Marshal spec params: %v", err)
	}
	raw, err, handled := h.Dispatch(context.Background(), "agent.spec.create", params)
	if err != nil {
		t.Fatalf("Dispatch(agent.spec.create) error: %v", err)
	}
	if !handled {
		t.Fatal("agent.spec.create handled = false, want true")
	}
	result := raw.(*AgentSpecResult)
	if result.Spec.ID == "" {
		t.Fatal("spec ID is empty")
	}

	listRaw, err, handled := h.Dispatch(context.Background(), "agent.spec.list", nil)
	if err != nil {
		t.Fatalf("Dispatch(agent.spec.list) error: %v", err)
	}
	if !handled {
		t.Fatal("agent.spec.list handled = false, want true")
	}
	list := listRaw.(*AgentSpecListResult)
	if len(list.Specs) != 1 || list.Specs[0].ID != result.Spec.ID {
		t.Fatalf("spec list = %#v, want created spec", list.Specs)
	}

	action, err := json.Marshal(AgentSpecActionParams{ID: result.Spec.ID})
	if err != nil {
		t.Fatalf("Marshal action params: %v", err)
	}
	approvedRaw, err, _ := h.Dispatch(context.Background(), "agent.spec.approve", action)
	if err != nil {
		t.Fatalf("Dispatch(agent.spec.approve) error: %v", err)
	}
	approved := approvedRaw.(*AgentSpecResult)
	if approved.Spec.Status != SpecStatusApproved {
		t.Fatalf("approved status = %q, want approved", approved.Spec.Status)
	}
}

func TestAgentSpecFixturesAreVersionedAndValid(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata", "specs"))
	if err != nil {
		t.Fatalf("ReadDir(testdata/specs) error: %v", err)
	}
	if len(entries) < 15 {
		t.Fatalf("fixture count = %d, want at least 15", len(entries))
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			path := filepath.Join("testdata", "specs", entry.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile(%s) error: %v", path, err)
			}
			firstLine := strings.SplitN(string(raw), "\n", 2)[0]
			if firstLine != "spec: 0.1.0" {
				t.Fatalf("first line = %q, want spec: 0.1.0", firstLine)
			}

			var spec AgentSpec
			if err := yaml.UnmarshalStrict(raw, &spec); err != nil {
				t.Fatalf("UnmarshalStrict(%s) error: %v", path, err)
			}
			if err := validateAgentSpec(spec); err != nil {
				t.Fatalf("validateAgentSpec(%s) error: %v", path, err)
			}
		})
	}
}

func TestGeneratedSpecsMatchCanonicalFixtures(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		prompt string
	}{
		{name: "media-accent-private.yaml", prompt: canonicalMediaAccentPrompt},
		{name: "coding-codex-private.yaml", prompt: "create a coding agent with codex"},
		{name: "financial-dexter-private.yaml", prompt: "I want a financial agent with Dexter"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := loadSpecFixture(t, tc.name)
			got := BuildAgentSpec(tc.prompt, now)
			got.ID = want.ID
			got.Meta = want.Meta

			if got.Spec != want.Spec ||
				got.Name != want.Name ||
				got.Description != want.Description ||
				got.Runtime.Template != want.Runtime.Template ||
				got.Runtime.Harness != want.Runtime.Harness ||
				got.Tools[0].Name != want.Tools[0].Name ||
				got.Tools[0].Capability != want.Tools[0].Capability ||
				got.Tools[0].Pricing.Model != want.Tools[0].Pricing.Model {
				t.Fatalf("generated spec = %#v\nwant fixture = %#v", got, want)
			}
		})
	}
}

func loadSpecFixture(t *testing.T, name string) AgentSpec {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "specs", name))
	if err != nil {
		t.Fatalf("ReadFile(%s) error: %v", name, err)
	}
	var spec AgentSpec
	if err := yaml.UnmarshalStrict(raw, &spec); err != nil {
		t.Fatalf("UnmarshalStrict(%s) error: %v", name, err)
	}
	return spec
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func splitNonEmptySpecLines(value string) []string {
	var lines []string
	for _, line := range strings.Split(value, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
