package external

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/messaging"
	messagingruntime "github.com/sky10/sky10/pkg/messaging/runtime"
)

func TestLoadManifestAndAdapterMetadata(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manifest := testManifest()
	path := writeManifest(t, dir, manifest)

	got, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	if got.ID != manifest.ID {
		t.Fatalf("manifest id = %q, want %q", got.ID, manifest.ID)
	}

	adapter := got.Adapter()
	if adapter.ID != "slack" {
		t.Fatalf("adapter id = %q, want slack", adapter.ID)
	}
	if adapter.DisplayName != "Slack" {
		t.Fatalf("adapter display name = %q, want Slack", adapter.DisplayName)
	}
	if len(adapter.AuthMethods) != 1 || adapter.AuthMethods[0] != messaging.AuthMethodBotToken {
		t.Fatalf("adapter auth methods = %#v, want bot_token", adapter.AuthMethods)
	}
	if !adapter.Capabilities.SearchConversations {
		t.Fatal("adapter search conversations = false, want true")
	}
	if len(got.Settings) != 1 || got.Settings[0].Key != "bot_token" {
		t.Fatalf("manifest settings = %#v, want bot_token setting", got.Settings)
	}
	if len(got.Actions) != 1 || got.Actions[0].Kind != ActionKindConnect {
		t.Fatalf("manifest actions = %#v, want connect action", got.Actions)
	}
}

func TestResolveBunProcessSpec(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeEntry(t, dir, "dist/adapter.js", "console.log('fixture');\n")
	manifest := testManifest()
	manifest.Env = map[string]string{
		"ZETA":    "last",
		"ALPHA":   "first",
		"BAD=KEY": "ignored",
		" ":       "ignored",
	}
	manifestPath := writeManifest(t, dir, manifest)

	spec, gotManifest, err := ResolveProcessSpec(manifestPath, ResolveOptions{
		BunPath:  "/managed/bin/bun",
		ExtraEnv: []string{"EXTRA=1"},
	})
	if err != nil {
		t.Fatalf("ResolveProcessSpec() error = %v", err)
	}
	if gotManifest.ID != manifest.ID {
		t.Fatalf("resolved manifest id = %q, want %q", gotManifest.ID, manifest.ID)
	}
	if spec.Path != "/managed/bin/bun" {
		t.Fatalf("spec path = %q, want /managed/bin/bun", spec.Path)
	}
	if len(spec.Args) != 1 {
		t.Fatalf("spec args len = %d, want 1", len(spec.Args))
	}

	wantDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("filepath.Abs() error = %v", err)
	}
	if spec.Dir != wantDir {
		t.Fatalf("spec dir = %q, want %q", spec.Dir, wantDir)
	}
	wantEntry := filepath.Join(wantDir, "dist", "adapter.js")
	if spec.Args[0] != wantEntry {
		t.Fatalf("spec entry = %q, want %q", spec.Args[0], wantEntry)
	}

	wantEnv := []string{
		"EXTRA=1",
		"ALPHA=first",
		"ZETA=last",
		"SKY10_MESSAGING_ADAPTER_ID=slack",
		"SKY10_MESSAGING_ADAPTER_BUNDLE_DIR=" + wantDir,
	}
	if !reflect.DeepEqual(spec.Env, wantEnv) {
		t.Fatalf("spec env = %#v, want %#v", spec.Env, wantEnv)
	}
}

func TestValidateRequiresExplicitSandbox(t *testing.T) {
	t.Parallel()

	manifest := testManifest()
	manifest.Sandbox = SandboxSpec{}
	err := manifest.Validate()
	if err == nil || !strings.Contains(err.Error(), "sandbox.mode") {
		t.Fatalf("Validate() error = %v, want sandbox.mode requirement", err)
	}
}

func TestResolveRejectsEscapingEntry(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manifest := testManifest()
	manifest.Entry = "../adapter.js"
	_, err := manifest.ProcessSpec(dir, ResolveOptions{})
	if err == nil || !strings.Contains(err.Error(), "escapes bundle directory") {
		t.Fatalf("ProcessSpec() error = %v, want escapes bundle directory error", err)
	}
}

func TestResolveRejectsBackslashEntry(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manifest := testManifest()
	manifest.Entry = `dist\adapter.js`
	_, err := manifest.ProcessSpec(dir, ResolveOptions{})
	if err == nil || !strings.Contains(err.Error(), "slash-separated") {
		t.Fatalf("ProcessSpec() error = %v, want slash-separated path error", err)
	}
}

func TestResolveRejectsZeroboxUntilWired(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeEntry(t, dir, "dist/adapter.js", "console.log('fixture');\n")
	manifest := testManifest()
	manifest.Sandbox = SandboxSpec{Mode: SandboxZerobox}

	_, err := manifest.ProcessSpec(dir, ResolveOptions{ZeroboxPath: "/managed/bin/zerobox"})
	if err == nil || !strings.Contains(err.Error(), "zerobox adapter sandbox launch is not wired yet") {
		t.Fatalf("ProcessSpec() error = %v, want zerobox not wired error", err)
	}
}

func TestBunManifestFixtureDescribe(t *testing.T) {
	bunPath := os.Getenv("SKY10_TEST_BUN")
	if bunPath == "" {
		var err error
		bunPath, err = exec.LookPath("bun")
		if err != nil {
			t.Skip("bun not found on PATH; set SKY10_TEST_BUN to run the fixture")
		}
	}

	dir := t.TempDir()
	writeEntry(t, dir, "dist/adapter.js", bunFixtureAdapter)
	manifestPath := writeManifest(t, dir, testManifest())

	spec, _, err := ResolveProcessSpec(manifestPath, ResolveOptions{BunPath: bunPath})
	if err != nil {
		t.Fatalf("ResolveProcessSpec() error = %v", err)
	}

	client, err := messagingruntime.StartAdapter(context.Background(), spec, nil)
	if err != nil {
		t.Fatalf("StartAdapter() error = %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("client.Close() error = %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	describe, err := client.Describe(ctx)
	if err != nil {
		t.Fatalf("Describe() error = %v; stderr=%s", err, client.Stderr())
	}
	if describe.Adapter.ID != "slack" {
		t.Fatalf("describe adapter id = %q, want slack", describe.Adapter.ID)
	}
	if !describe.Adapter.Capabilities.SearchConversations {
		t.Fatal("describe search conversations = false, want true")
	}
}

func testManifest() Manifest {
	return Manifest{
		ID:          "slack",
		DisplayName: "Slack",
		Version:     "0.1.0",
		Description: "Slack adapter fixture",
		AuthMethods: []messaging.AuthMethod{
			messaging.AuthMethodBotToken,
		},
		Capabilities: messaging.Capabilities{
			ReceiveMessages:     true,
			SendMessages:        true,
			ListConversations:   true,
			SearchConversations: true,
		},
		Settings: []Setting{{
			Key:         "bot_token",
			Label:       "Bot token",
			Kind:        SettingKindSecret,
			Target:      SettingTargetCredential,
			Required:    true,
			Description: "Slack bot token.",
			Placeholder: "xoxb-...",
			Secret:      true,
		}},
		Actions: []Action{{
			ID:      "connect",
			Label:   "Connect Slack",
			Kind:    ActionKindConnect,
			Primary: true,
		}},
		Runtime: RuntimeSpec{
			Type:    RuntimeBun,
			Version: "^1.3",
		},
		Entry:   "dist/adapter.js",
		Sandbox: SandboxSpec{Mode: SandboxNone},
	}
}

func writeManifest(t *testing.T, dir string, manifest Manifest) string {
	t.Helper()

	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	path := filepath.Join(dir, "adapter.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

func writeEntry(t *testing.T, dir, relPath, content string) {
	t.Helper()

	path := filepath.Join(dir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create entry dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
}

const bunFixtureAdapter = `
let buffer = Buffer.alloc(0);

process.stdin.on("data", (chunk) => {
  buffer = Buffer.concat([buffer, chunk]);
  for (;;) {
    const headerEnd = buffer.indexOf("\r\n\r\n");
    if (headerEnd < 0) return;

    const header = buffer.subarray(0, headerEnd).toString("utf8");
    const match = /Content-Length:\s*(\d+)/i.exec(header);
    if (!match) throw new Error("missing Content-Length");

    const length = Number(match[1]);
    const bodyStart = headerEnd + 4;
    const bodyEnd = bodyStart + length;
    if (buffer.length < bodyEnd) return;

    const request = JSON.parse(buffer.subarray(bodyStart, bodyEnd).toString("utf8"));
    buffer = buffer.subarray(bodyEnd);
    handle(request);
  }
});

function write(response) {
  const body = JSON.stringify(response);
  process.stdout.write(` + "`" + `Content-Length: ${Buffer.byteLength(body)}\r\n\r\n${body}` + "`" + `);
}

function handle(request) {
  switch (request.method) {
    case "messaging.adapter.describe":
      write({
        jsonrpc: "2.0",
        id: request.id,
        result: {
          protocol: {
            name: "sky10.messaging.adapter",
            version: "v1alpha1",
            compatible_versions: ["v1alpha1"],
            transport: "stdio-jsonrpc"
          },
          adapter: {
            id: process.env.SKY10_MESSAGING_ADAPTER_ID || "slack",
            display_name: "Slack",
            version: "0.1.0",
            description: "Slack adapter fixture",
            auth_methods: ["bot_token"],
            capabilities: {
              receive_messages: true,
              send_messages: true,
              list_conversations: true,
              search_conversations: true
            }
          }
        }
      });
      return;
    case "messaging.adapter.health":
      write({
        jsonrpc: "2.0",
        id: request.id,
        result: {
          health: {
            ok: true,
            status: "connected",
            message: "fixture"
          }
        }
      });
      return;
    default:
      write({
        jsonrpc: "2.0",
        id: request.id,
        error: {
          code: -32601,
          message: "method not found"
        }
      });
  }
}
`
