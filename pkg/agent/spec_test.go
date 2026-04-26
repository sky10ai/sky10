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
	if tool.Name != "media.convert" {
		t.Fatalf("tool name = %q, want media.convert", tool.Name)
	}
	if tool.Capability != "media.convert" {
		t.Fatalf("tool capability = %q, want media.convert", tool.Capability)
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

	compileParams, err := json.Marshal(AgentSpecCompileParams{ID: result.Spec.ID})
	if err != nil {
		t.Fatalf("Marshal compile params: %v", err)
	}
	compileRaw, err, handled := h.Dispatch(context.Background(), "agent.spec.compile", compileParams)
	if err != nil {
		t.Fatalf("Dispatch(agent.spec.compile) error: %v", err)
	}
	if !handled {
		t.Fatal("agent.spec.compile handled = false, want true")
	}
	compiled := compileRaw.(*AgentSpecCompileResult)
	if compiled.Runtime.Template != "openclaw-docker" {
		t.Fatalf("compiled runtime = %#v, want openclaw-docker template", compiled.Runtime)
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

func TestCompileAgentSpecProducesMediaRuntimePreview(t *testing.T) {
	spec := loadSpecFixture(t, "media-accent-private.yaml")
	compiled, err := CompileAgentSpec(spec)
	if err != nil {
		t.Fatalf("CompileAgentSpec() error: %v", err)
	}

	if compiled.Runtime.Target != "sandbox" ||
		compiled.Runtime.Provider != "lima" ||
		compiled.Runtime.Template != "openclaw-docker" ||
		compiled.Runtime.Harness != "openclaw" {
		t.Fatalf("runtime = %#v, want sandbox/lima/openclaw-docker/openclaw", compiled.Runtime)
	}
	if len(compiled.SecretBindings) != 1 {
		t.Fatalf("secret bindings = %#v, want one binding", compiled.SecretBindings)
	}
	if compiled.SecretBindings[0].Env != "ELEVENLABS_API_KEY" ||
		compiled.SecretBindings[0].Secret != "voice-provider-api-key" {
		t.Fatalf("secret binding = %#v, want ElevenLabs env mapped to sky10 secret name", compiled.SecretBindings[0])
	}

	compose := compiledFileContent(t, compiled, "compose.yaml")
	for _, want := range []string{
		"image: \"ubuntu:24.04\"",
		"- ELEVENLABS_API_KEY=${ELEVENLABS_API_KEY}",
		"sky10.agent.harness=openclaw",
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("compose.yaml missing %q:\n%s", want, compose)
		}
	}

	env := compiledFileContent(t, compiled, ".env.example")
	if !strings.Contains(env, "ELEVENLABS_API_KEY=\n") {
		t.Fatalf(".env.example = %q, want ELEVENLABS_API_KEY placeholder", env)
	}
	if strings.Contains(env, "elevenlabs-key") {
		t.Fatalf(".env.example leaked secret payload: %q", env)
	}
}

func TestCompileAgentSpecFixturesProduceRuntimeArtifacts(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata", "specs"))
	if err != nil {
		t.Fatalf("ReadDir(testdata/specs) error: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			spec := loadSpecFixture(t, entry.Name())
			compiled, err := CompileAgentSpec(spec)
			if err != nil {
				t.Fatalf("CompileAgentSpec(%s) error: %v", entry.Name(), err)
			}
			for _, path := range []string{"agent-manifest.json", ".env.example", "README.md"} {
				if content := compiledFileContent(t, compiled, path); strings.TrimSpace(content) == "" {
					t.Fatalf("%s is empty", path)
				}
			}
			if spec.Runtime.Target == "sandbox" {
				compiledFileContent(t, compiled, "compose.yaml")
				if len(compiled.Actions) < 2 ||
					compiled.Actions[0].Method != "sandbox.create" ||
					compiled.Actions[1].Method != "agent.register" {
					t.Fatalf("actions = %#v, want sandbox.create, agent.register", compiled.Actions)
				}
				params := compiled.Actions[0].Params
				if _, ok := params["files"].([]map[string]string); !ok {
					t.Fatalf("sandbox.create params files = %#v, want generated shared files", params["files"])
				}
			}
			for _, file := range compiled.Files {
				if strings.Contains(file.Content, "sk_test_") || strings.Contains(file.Content, "elevenlabs-key") {
					t.Fatalf("%s appears to contain a secret payload: %q", file.Path, file.Content)
				}
			}
		})
	}
}

func TestCompileAgentSpecSelectsHarnessTemplates(t *testing.T) {
	cases := []struct {
		fixture string
		harness string
		image   string
	}{
		{fixture: "coding-codex-private.yaml", harness: "codex", image: "ubuntu:24.04"},
		{fixture: "financial-dexter-private.yaml", harness: "dexter", image: "oven/bun:1.1"},
	}

	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			compiled, err := CompileAgentSpec(loadSpecFixture(t, tc.fixture))
			if err != nil {
				t.Fatalf("CompileAgentSpec(%s) error: %v", tc.fixture, err)
			}
			if compiled.Runtime.Harness != tc.harness {
				t.Fatalf("harness = %q, want %q", compiled.Runtime.Harness, tc.harness)
			}
			compose := compiledFileContent(t, compiled, "compose.yaml")
			if !strings.Contains(compose, "image: "+quoteYAML(tc.image)) {
				t.Fatalf("compose.yaml = %q, want image %s", compose, tc.image)
			}
		})
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

func compiledFileContent(t *testing.T, compiled *AgentSpecCompileResult, path string) string {
	t.Helper()
	for _, file := range compiled.Files {
		if file.Path == path {
			if file.Mode != compiledFileMode {
				t.Fatalf("%s mode = %q, want %q", path, file.Mode, compiledFileMode)
			}
			return file.Content
		}
	}
	t.Fatalf("compiled file %s not found in %#v", path, compiled.Files)
	return ""
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
