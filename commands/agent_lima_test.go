package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sky10/sky10/pkg/config"
	skyfs "github.com/sky10/sky10/pkg/fs"
	skysandbox "github.com/sky10/sky10/pkg/sandbox"
)

func TestDefaultLimaSharedDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := defaultLimaSharedDir("bobs-burgers")
	if err != nil {
		t.Fatalf("defaultLimaSharedDir: %v", err)
	}

	want := filepath.Join(home, "Sky10", "Drives", "Agents", "bobs-burgers")
	if got != want {
		t.Fatalf("defaultLimaSharedDir = %q, want %q", got, want)
	}
}

func TestDefaultLimaStateDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv(config.EnvHome, root)

	got, err := defaultLimaStateDir("bobs-burgers")
	if err != nil {
		t.Fatalf("defaultLimaStateDir: %v", err)
	}

	want := filepath.Join(root, "sandboxes", "bobs-burgers", "state")
	if got != want {
		t.Fatalf("defaultLimaStateDir = %q, want %q", got, want)
	}
}

func TestEnsureLocalAgentHomeUsesAgentsDriveRoot(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	driveHome := t.TempDir()
	sharedDir := filepath.Join(driveHome, "Sky10", "Drives", "Agents", "devbox")
	if err := ensureLocalAgentHome("devbox", sharedDir); err != nil {
		t.Fatalf("ensureLocalAgentHome() error: %v", err)
	}

	cfgDir, err := config.Dir()
	if err != nil {
		t.Fatalf("config.Dir() error: %v", err)
	}
	manager := skyfs.NewDriveManager(nil, filepath.Join(cfgDir, "drives.json"))
	drives := manager.ListDrives()
	if len(drives) != 1 {
		t.Fatalf("drive count = %d, want 1", len(drives))
	}
	if drives[0].Name != agentDriveRootName {
		t.Fatalf("drive name = %q, want %q", drives[0].Name, agentDriveRootName)
	}
	if drives[0].Namespace != agentDriveRootName {
		t.Fatalf("drive namespace = %q, want %q", drives[0].Namespace, agentDriveRootName)
	}
	if drives[0].LocalPath != filepath.Join(driveHome, "Sky10", "Drives", "Agents") {
		t.Fatalf("drive path = %q, want root agent drive", drives[0].LocalPath)
	}
}

func TestEnsureLocalAgentHomeReplacesLegacyPerAgentDrive(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	driveHome := t.TempDir()
	sharedDir := filepath.Join(driveHome, "Sky10", "Drives", "Agents", "devbox")

	cfgDir, err := config.Dir()
	if err != nil {
		t.Fatalf("config.Dir() error: %v", err)
	}
	manager := skyfs.NewDriveManager(nil, filepath.Join(cfgDir, "drives.json"))
	legacyName := agentDriveNamePrefix + "devbox"
	if _, err := manager.CreateDrive(legacyName, sharedDir, legacyName); err != nil {
		t.Fatalf("CreateDrive(legacy) error: %v", err)
	}

	if err := ensureLocalAgentHome("devbox", sharedDir); err != nil {
		t.Fatalf("ensureLocalAgentHome() error: %v", err)
	}

	manager = skyfs.NewDriveManager(nil, filepath.Join(cfgDir, "drives.json"))
	drives := manager.ListDrives()
	if len(drives) != 1 {
		t.Fatalf("drive count = %d, want 1", len(drives))
	}
	if drives[0].Name != agentDriveRootName {
		t.Fatalf("drive name = %q, want %q", drives[0].Name, agentDriveRootName)
	}
	if drives[0].LocalPath != filepath.Join(driveHome, "Sky10", "Drives", "Agents") {
		t.Fatalf("drive path = %q, want root agent drive", drives[0].LocalPath)
	}
}

func TestWalkUp(t *testing.T) {
	t.Parallel()

	base := filepath.Join(string(filepath.Separator), "tmp", "sky10", "nested")
	got := walkUp(base)
	if len(got) < 4 {
		t.Fatalf("walkUp(%q) returned too few directories: %v", base, got)
	}
	if got[0] != base {
		t.Fatalf("walkUp(%q) first dir = %q, want %q", base, got[0], base)
	}
	if got[len(got)-1] != string(filepath.Separator) {
		t.Fatalf("walkUp(%q) last dir = %q, want %q", base, got[len(got)-1], string(filepath.Separator))
	}
}

func TestHasLimaTemplateAssets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	spec, err := limaTemplateDefinition(sandboxTemplateOpenClaw)
	if err != nil {
		t.Fatalf("limaTemplateDefinition(openclaw): %v", err)
	}
	for _, name := range append(append([]string(nil), spec.assets...), agentLimaHostsScript) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}

	if !hasLimaTemplateAssets(dir, spec) {
		t.Fatal("hasLimaTemplateAssets() = false, want true")
	}
}

func TestValidateSandboxCreate(t *testing.T) {
	t.Parallel()

	if err := validateSandboxCreate(sandboxProviderLima, sandboxTemplateOpenClaw); err != nil {
		t.Fatalf("validateSandboxCreate(valid): %v", err)
	}
	if err := validateSandboxCreate(sandboxProviderLima, sandboxTemplateHermes); err != nil {
		t.Fatalf("validateSandboxCreate(hermes): %v", err)
	}
	if err := validateSandboxCreate("docker", sandboxTemplateOpenClaw); err == nil {
		t.Fatal("validateSandboxCreate(docker): want error")
	}
	if err := validateSandboxCreate(sandboxProviderLima, "claude"); err == nil {
		t.Fatal("validateSandboxCreate(unknown template): want error")
	}
}

func TestRenderLimaTemplate(t *testing.T) {
	t.Parallel()

	body := []byte(`name=__SKY10_SANDBOX_NAME__ path=__SKY10_SHARED_DIR__ state=__SKY10_STATE_DIR__`)
	got := string(renderLimaTemplate(body, "bobs-burgers", "/Users/bf/Sky10/Drives/Agents/bobs-burgers", "/Users/bf/.sky10/sandboxes/bobs-burgers/state"))

	if strings.Contains(got, templateNameToken) || strings.Contains(got, templateSharedToken) || strings.Contains(got, templateStateToken) {
		t.Fatalf("renderLimaTemplate() left placeholder tokens behind: %q", got)
	}
	if !strings.Contains(got, "bobs-burgers") {
		t.Fatalf("renderLimaTemplate() missing sandbox name: %q", got)
	}
	if !strings.Contains(got, "/Users/bf/Sky10/Drives/Agents/bobs-burgers") {
		t.Fatalf("renderLimaTemplate() missing shared dir: %q", got)
	}
	if !strings.Contains(got, "/Users/bf/.sky10/sandboxes/bobs-burgers/state") {
		t.Fatalf("renderLimaTemplate() missing state dir: %q", got)
	}
}

func TestPrepareLimaSharedDir(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	pluginAssets := map[string][]byte{
		agentLimaPluginManifest: []byte(`{"id":"sky10"}` + "\n"),
		agentLimaPluginIndex:    []byte("export default function register() {}\n"),
	}
	if err := prepareLimaSharedDir(sandboxTemplateOpenClaw, sharedDir, stateDir, []byte("#!/bin/sh\n"), pluginAssets, map[string]string{
		"OPENAI_API_KEY": "openai-key",
	}, nil, skysandbox.AgentProfileSeed{
		DisplayName: "OpenClaw Dev",
		Slug:        "devbox",
		Template:    sandboxTemplateOpenClaw,
	}); err != nil {
		t.Fatalf("prepareLimaSharedDir() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(sharedDir, "workspace")); err != nil {
		t.Fatalf("Stat(agent workspace) error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, agentLimaHostsScript)); err != nil {
		t.Fatalf("Stat(hosts helper) error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, ".env")); err != nil {
		t.Fatalf("Stat(.env) error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, ".env"))
	if err != nil {
		t.Fatalf("ReadFile(.env) error: %v", err)
	}
	if !strings.Contains(string(data), "OPENAI_API_KEY=openai-key") {
		t.Fatalf(".env = %q, want resolved openai key", string(data))
	}
	if _, err := os.Stat(filepath.Join(stateDir, "plugins", agentLimaPluginManifest)); err != nil {
		t.Fatalf("Stat(plugin manifest) error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sharedDir, "sky10.md")); err != nil {
		t.Fatalf("Stat(sky10.md) error: %v", err)
	}
	if got, err := os.Readlink(filepath.Join(sharedDir, "workspace", "SOUL.md")); err != nil {
		t.Fatalf("Readlink(workspace/SOUL.md) error: %v", err)
	} else if got != filepath.Join("..", "SOUL.md") {
		t.Fatalf("workspace/SOUL.md -> %q, want ../SOUL.md", got)
	}
}

func TestPrepareLimaSharedDirHermes(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := prepareLimaSharedDir(sandboxTemplateHermes, sharedDir, stateDir, nil, map[string][]byte{
		agentLimaHermesBridge: []byte("#!/usr/bin/env python3\nprint('ok')\n"),
	}, map[string]string{
		"ANTHROPIC_API_KEY": "anthropic-key",
	}, &hermesBridgeConfig{
		HostRPCURL:   "http://host.lima.internal:9101/rpc",
		AgentName:    "Hermes Agent",
		AgentKeyName: "hermes-agent",
		Skills:       []string{"code", "shell"},
	}, skysandbox.AgentProfileSeed{
		DisplayName: "Hermes Agent",
		Slug:        "hermes-agent",
		Template:    sandboxTemplateHermes,
	}); err != nil {
		t.Fatalf("prepareLimaSharedDir(hermes) error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(stateDir, ".env"))
	if err != nil {
		t.Fatalf("ReadFile(.env) error: %v", err)
	}
	if !strings.Contains(string(data), "Optional provider keys for Hermes") {
		t.Fatalf(".env = %q, want Hermes header", string(data))
	}
	if got, err := os.Readlink(filepath.Join(sharedDir, "workspace", "AGENTS.md")); err != nil {
		t.Fatalf("Readlink(workspace/AGENTS.md) error: %v", err)
	} else if got != filepath.Join("..", "AGENTS.md") {
		t.Fatalf("workspace/AGENTS.md -> %q, want ../AGENTS.md", got)
	}
	if !strings.Contains(string(data), "ANTHROPIC_API_KEY=anthropic-key") {
		t.Fatalf(".env = %q, want resolved anthropic key", string(data))
	}
	configData, err := os.ReadFile(filepath.Join(stateDir, agentLimaHermesBridgeJSON))
	if err != nil {
		t.Fatalf("ReadFile(bridge config) error: %v", err)
	}
	if !strings.Contains(string(configData), `"agent_name":"Hermes Agent"`) {
		t.Fatalf("bridge config = %q, want agent name", string(configData))
	}
	bridgePath := filepath.Join(stateDir, agentLimaHermesBridge)
	if info, err := os.Stat(bridgePath); err != nil {
		t.Fatalf("Stat(bridge asset) error: %v", err)
	} else if info.Mode()&0o111 == 0 {
		t.Fatalf("bridge asset mode = %v, want executable", info.Mode())
	}
}

func TestOpenClawUserScriptLoadsOpenClawEnvFile(t *testing.T) {
	t.Parallel()

	spec, err := limaTemplateDefinition(sandboxTemplateOpenClaw)
	if err != nil {
		t.Fatalf("limaTemplateDefinition(openclaw): %v", err)
	}
	dir, err := findLocalLimaTemplateDir(spec)
	if err != nil {
		t.Fatalf("findLocalLimaTemplateDir() error: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, agentLimaUserScript))
	if err != nil {
		t.Fatalf("ReadFile(user script) error: %v", err)
	}
	if !strings.Contains(string(body), "EnvironmentFile=-%h/.openclaw/.env") {
		t.Fatalf("user script missing systemd env file import: %q", string(body))
	}
	if !strings.Contains(string(body), `SKY10_INVITE_PATH="/sandbox-state/join.json"`) {
		t.Fatalf("user script missing sandbox invite path: %q", string(body))
	}
	if strings.Contains(string(body), "sky10 join --role sandbox") {
		t.Fatalf("user script should not join the host identity during boot: %q", string(body))
	}
	if !strings.Contains(string(body), "cat > \"${UNIT_DIR}/sky10.service\" <<EOF") {
		t.Fatalf("user script missing guest sky10 systemd unit: %q", string(body))
	}
	if !strings.Contains(string(body), "ExecStartPost=%h/.bin/sky10-managed-reconnect") {
		t.Fatalf("user script missing guest sky10 reconnect hook: %q", string(body))
	}
	if !strings.Contains(string(body), "systemctl --user enable sky10.service") {
		t.Fatalf("user script missing guest sky10 systemd enable: %q", string(body))
	}
	if !strings.Contains(string(body), "install_guest_reconnect_helper") {
		t.Fatalf("user script missing guest reconnect helper install: %q", string(body))
	}
	if !strings.Contains(string(body), `"method": "sandbox.reconnectGuest"`) {
		t.Fatalf("user script missing sandbox reconnect guest callback: %q", string(body))
	}
	if !strings.Contains(string(body), `payload.get("host_rpc_url")`) {
		t.Fatalf("user script missing host rpc url parsing: %q", string(body))
	}
	if strings.Contains(string(body), "nohup sky10 serve") {
		t.Fatalf("user script should not rely on nohup sky10 serve fallback: %q", string(body))
	}
	if !strings.Contains(string(body), "bootstrap_local_cli_pairing") {
		t.Fatalf("user script missing CLI pairing bootstrap: %q", string(body))
	}
	if strings.Contains(string(body), "IDENTITY.md") {
		t.Fatalf("user script should not seed identity files into the shared workspace: %q", string(body))
	}
	if !strings.Contains(string(body), `PLUGIN_DIR="/sandbox-state/plugins/openclaw-sky10-channel"`) {
		t.Fatalf("user script missing sandbox-state plugin dir: %q", string(body))
	}
	if !strings.Contains(string(body), `WORKSPACE_DIR="/shared/workspace"`) {
		t.Fatalf("user script missing shared workspace dir: %q", string(body))
	}
	if !strings.Contains(string(body), `"skills": ["code", "shell", "browser", "web-search", "file-ops"]`) {
		t.Fatalf("user script missing browser skill registration: %q", string(body))
	}
	if !strings.Contains(string(body), `defaults["workspace"] = "/shared/workspace"`) {
		t.Fatalf("user script missing shared workspace config: %q", string(body))
	}
	if !strings.Contains(string(body), `sky10_channel["defaultAccount"] = "default"`) {
		t.Fatalf("user script missing sky10 default account config: %q", string(body))
	}
	if !strings.Contains(string(body), `sky10_channel["healthMonitor"] = {"enabled": False}`) {
		t.Fatalf("user script missing sky10 health monitor config: %q", string(body))
	}
	if !strings.Contains(string(body), `sky10_accounts["default"] = {`) {
		t.Fatalf("user script missing sky10 default account entry: %q", string(body))
	}
	if !strings.Contains(string(body), `browser["ssrfPolicy"] = {"dangerouslyAllowPrivateNetwork": True}`) {
		t.Fatalf("user script missing relaxed browser SSRF policy: %q", string(body))
	}
}

func TestOpenClawDependencyScriptPersistsRouteMetrics(t *testing.T) {
	t.Parallel()

	spec, err := limaTemplateDefinition(sandboxTemplateOpenClaw)
	if err != nil {
		t.Fatalf("limaTemplateDefinition(openclaw): %v", err)
	}
	dir, err := findLocalLimaTemplateDir(spec)
	if err != nil {
		t.Fatalf("findLocalLimaTemplateDir() error: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, agentLimaDependencyScript))
	if err != nil {
		t.Fatalf("ReadFile(dependency script) error: %v", err)
	}
	script := string(body)
	if !strings.Contains(script, "99-openclaw-route-metrics.yaml") {
		t.Fatalf("dependency script missing netplan route override: %q", script)
	}
	if !strings.Contains(script, "route-metric: 100") || !strings.Contains(script, "route-metric: 200") {
		t.Fatalf("dependency script missing persistent route metrics: %q", script)
	}
	if !strings.Contains(script, "netplan apply") {
		t.Fatalf("dependency script missing netplan apply: %q", script)
	}
}

func TestOpenClawSystemScriptPinsOpenClawVersion(t *testing.T) {
	t.Parallel()

	spec, err := limaTemplateDefinition(sandboxTemplateOpenClaw)
	if err != nil {
		t.Fatalf("limaTemplateDefinition(openclaw): %v", err)
	}
	dir, err := findLocalLimaTemplateDir(spec)
	if err != nil {
		t.Fatalf("findLocalLimaTemplateDir() error: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, agentLimaSystemScript))
	if err != nil {
		t.Fatalf("ReadFile(system script) error: %v", err)
	}
	script := string(body)
	if !strings.Contains(script, `OPENCLAW_VERSION=2026.4.14`) {
		t.Fatalf("system script missing pinned openclaw version: %q", script)
	}
	if !strings.Contains(script, `npm install -g "openclaw@${OPENCLAW_VERSION}"`) {
		t.Fatalf("system script missing pinned openclaw install command: %q", script)
	}
	if !strings.Contains(script, `openclaw-system-v2`) {
		t.Fatalf("system script missing bumped sentinel version: %q", script)
	}
	if strings.Contains(script, `openclaw@latest`) {
		t.Fatalf("system script should not install latest openclaw: %q", script)
	}
}

func TestOpenClawPluginDefaultsAdvertiseBrowserSkill(t *testing.T) {
	t.Parallel()

	spec, err := limaTemplateDefinition(sandboxTemplateOpenClaw)
	if err != nil {
		t.Fatalf("limaTemplateDefinition(openclaw): %v", err)
	}
	dir, err := findLocalLimaTemplateDir(spec)
	if err != nil {
		t.Fatalf("findLocalLimaTemplateDir() error: %v", err)
	}

	manifestBody, err := os.ReadFile(filepath.Join(dir, agentLimaPluginManifest))
	if err != nil {
		t.Fatalf("ReadFile(plugin manifest) error: %v", err)
	}
	if !strings.Contains(string(manifestBody), `["code", "shell", "browser", "web-search", "file-ops"]`) {
		t.Fatalf("plugin manifest missing browser skill default: %q", string(manifestBody))
	}
	if !strings.Contains(string(manifestBody), `"channels"`) || !strings.Contains(string(manifestBody), `"sky10"`) {
		t.Fatalf("plugin manifest missing sky10 channel declaration: %q", string(manifestBody))
	}

	indexBody, err := os.ReadFile(filepath.Join(dir, agentLimaPluginIndex))
	if err != nil {
		t.Fatalf("ReadFile(plugin index) error: %v", err)
	}
	if !strings.Contains(string(indexBody), `["code", "shell", "browser", "web-search", "file-ops"]`) {
		t.Fatalf("plugin index missing browser skill default: %q", string(indexBody))
	}
	if !strings.Contains(string(indexBody), `createChatChannelPlugin`) {
		t.Fatalf("plugin index missing OpenClaw chat channel registration: %q", string(indexBody))
	}
	if !strings.Contains(string(indexBody), `createChannelReplyPipeline`) {
		t.Fatalf("plugin index missing channel reply pipeline: %q", string(indexBody))
	}
	if !strings.Contains(string(indexBody), `api.registerChannel({ plugin: sky10ChannelPlugin })`) {
		t.Fatalf("plugin index missing channel registration: %q", string(indexBody))
	}
	if !strings.Contains(string(indexBody), `fs.openSync(claimPathFor(msgId), "wx")`) {
		t.Fatalf("plugin index missing cross-process claim guard: %q", string(indexBody))
	}
	if !strings.Contains(string(indexBody), `Symbol.for("sky10.openclaw.bridge")`) {
		t.Fatalf("plugin index missing global bridge singleton: %q", string(indexBody))
	}
	if strings.Contains(string(indexBody), `/v1/responses`) {
		t.Fatalf("plugin index should not self-call the gateway responses API: %q", string(indexBody))
	}
}

func TestOpenClawBridgeAssetStreamsReplies(t *testing.T) {
	t.Parallel()

	spec, err := limaTemplateDefinition(sandboxTemplateOpenClaw)
	if err != nil {
		t.Fatalf("limaTemplateDefinition(openclaw): %v", err)
	}
	dir, err := findLocalLimaTemplateDir(spec)
	if err != nil {
		t.Fatalf("findLocalLimaTemplateDir() error: %v", err)
	}

	indexBody, err := os.ReadFile(filepath.Join(dir, agentLimaPluginIndex))
	if err != nil {
		t.Fatalf("ReadFile(plugin index) error: %v", err)
	}
	indexScript := string(indexBody)
	if !strings.Contains(indexScript, "createChannelReplyPipeline") {
		t.Fatalf("plugin index missing reply pipeline creation: %q", indexScript)
	}
	if !strings.Contains(indexScript, "dispatchReplyWithBufferedBlockDispatcher") {
		t.Fatalf("plugin index missing buffered block dispatcher: %q", indexScript)
	}
	if !strings.Contains(indexScript, "state.client.sendDelta(") {
		t.Fatalf("plugin index missing delta send path: %q", indexScript)
	}
	if !strings.Contains(indexScript, "onPartialReply: async (payload)") {
		t.Fatalf("plugin index missing partial reply stream hook: %q", indexScript)
	}
	if !strings.Contains(indexScript, "resolveIncrementalReplyText") {
		t.Fatalf("plugin index missing incremental reply helper: %q", indexScript)
	}
	if !strings.Contains(indexScript, "state.client.sendContent(") {
		t.Fatalf("plugin index missing final content send path: %q", indexScript)
	}
	if !strings.Contains(indexScript, "stream_id: streamId") {
		t.Fatalf("plugin index missing stream_id propagation: %q", indexScript)
	}
	if !strings.Contains(indexScript, "extractClientRequestID") {
		t.Fatalf("plugin index missing client_request_id propagation helper: %q", indexScript)
	}

	clientBody, err := os.ReadFile(filepath.Join(dir, agentLimaPluginClient))
	if err != nil {
		t.Fatalf("ReadFile(plugin client) error: %v", err)
	}
	clientScript := string(clientBody)
	if !strings.Contains(clientScript, "async sendContent(") {
		t.Fatalf("plugin client missing sendContent helper: %q", clientScript)
	}
	if !strings.Contains(clientScript, "async sendDelta(") {
		t.Fatalf("plugin client missing sendDelta helper: %q", clientScript)
	}
	if !strings.Contains(clientScript, "stream_id: streamId") {
		t.Fatalf("plugin client missing stream_id propagation: %q", clientScript)
	}
}

func TestHermesUserScriptInstallsHelper(t *testing.T) {
	t.Parallel()

	spec, err := limaTemplateDefinition(sandboxTemplateHermes)
	if err != nil {
		t.Fatalf("limaTemplateDefinition(hermes): %v", err)
	}
	dir, err := findLocalLimaTemplateDir(spec)
	if err != nil {
		t.Fatalf("findLocalLimaTemplateDir() error: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, agentLimaHermesUser))
	if err != nil {
		t.Fatalf("ReadFile(user script) error: %v", err)
	}
	script := string(body)
	if !strings.Contains(script, "hermes-shared") {
		t.Fatalf("user script missing helper command: %q", script)
	}
	if !strings.Contains(script, "hermes config set terminal.cwd /shared/workspace") {
		t.Fatalf("user script missing shared cwd config: %q", script)
	}
	if !strings.Contains(script, "hermes config set model \"${HERMES_MODEL}\"") {
		t.Fatalf("user script missing upstream model config command: %q", script)
	}
	if !strings.Contains(script, "ANTHROPIC_API_KEY/anthropic") {
		t.Fatalf("user script missing host-secret merge note: %q", script)
	}
	if !strings.Contains(script, "hermes-sky10-bridge.py") {
		t.Fatalf("user script missing bundled bridge asset install: %q", script)
	}
	if !strings.Contains(script, "sky10-hermes-gateway.service") {
		t.Fatalf("user script missing gateway service unit: %q", script)
	}
	if !strings.Contains(script, "sky10-hermes-bridge.service") {
		t.Fatalf("user script missing bridge service unit: %q", script)
	}
	if !strings.Contains(script, "sky10-managed-reconnect") {
		t.Fatalf("user script missing guest reconnect helper: %q", script)
	}
	if !strings.Contains(script, `mkdir -p "${HOME}/.bin"`) {
		t.Fatalf("user script missing ~/.bin bootstrap dir creation: %q", script)
	}
	if !strings.Contains(script, "sky10.service") {
		t.Fatalf("user script missing guest sky10 service unit: %q", script)
	}
	if strings.Contains(script, "sky10 join --role sandbox") {
		t.Fatalf("user script should not join the host identity during boot: %q", script)
	}
	if !strings.Contains(script, "hermes gateway run") {
		t.Fatalf("user script missing gateway foreground command: %q", script)
	}
	if !strings.Contains(script, "API_SERVER_ENABLED=true") {
		t.Fatalf("user script missing API server env bootstrap: %q", script)
	}
	if !strings.Contains(script, `Environment=MESSAGING_CWD=/shared/workspace`) {
		t.Fatalf("user script missing messaging cwd override: %q", script)
	}
	if !strings.Contains(script, "/sandbox-state/bridge.json") {
		t.Fatalf("user script missing bridge config path: %q", script)
	}
	if !strings.Contains(script, `link_agent_file "${SHARED_DIR}/SOUL.md" "${HERMES_HOME}/SOUL.md"`) {
		t.Fatalf("user script missing SOUL.md root link: %q", script)
	}
	if !strings.Contains(script, `link_agent_file "${SHARED_DIR}/MEMORY.md" "${HERMES_HOME}/memories/MEMORY.md"`) {
		t.Fatalf("user script missing MEMORY.md root link: %q", script)
	}
	if strings.Contains(script, "HERMES.md") {
		t.Fatalf("user script should not seed welcome docs into the shared workspace: %q", script)
	}
	if !strings.Contains(script, "merge_guest_env_into_shared") {
		t.Fatalf("user script missing guest-env merge helper: %q", script)
	}
	if !strings.Contains(script, "shared_agent_file_is_seed") {
		t.Fatalf("user script missing seeded-profile detection helper: %q", script)
	}
	if !strings.Contains(script, `preserve_guest_agent_path "${source}" "${target}"`) {
		t.Fatalf("user script missing guest profile preservation before relink: %q", script)
	}
	if !strings.Contains(script, "guest-profile-backup") {
		t.Fatalf("user script missing guest profile backup path: %q", script)
	}
	if !strings.Contains(script, ".env.example") {
		t.Fatalf("user script missing Hermes example env comparison: %q", script)
	}
	if !strings.Contains(script, `ln -sfn "${SANDBOX_STATE_DIR}/.env" "${HERMES_HOME}/.env"`) {
		t.Fatalf("user script missing sandbox env symlink: %q", script)
	}
	if got := strings.Count(script, "link_hermes_env"); got < 3 {
		t.Fatalf("user script should relink shared env after Hermes config writes, count=%d: %q", got, script)
	}
	if strings.Contains(script, `cp "${HERMES_HOME}/.env" "${SHARED_DIR}/.env"`) {
		t.Fatalf("user script should not clobber shared env with guest env: %q", script)
	}
}

func TestHermesBridgeAssetSubscribesToSky10Events(t *testing.T) {
	t.Parallel()

	spec, err := limaTemplateDefinition(sandboxTemplateHermes)
	if err != nil {
		t.Fatalf("limaTemplateDefinition(hermes): %v", err)
	}
	dir, err := findLocalLimaTemplateDir(spec)
	if err != nil {
		t.Fatalf("findLocalLimaTemplateDir() error: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, agentLimaHermesBridge))
	if err != nil {
		t.Fatalf("ReadFile(bridge asset) error: %v", err)
	}
	script := string(body)
	if !strings.Contains(script, `"agent.register"`) {
		t.Fatalf("bridge asset missing sky10 registration call: %q", script)
	}
	if !strings.Contains(script, "/rpc/events") {
		t.Fatalf("bridge asset missing SSE subscription: %q", script)
	}
	if !strings.Contains(script, "/responses") {
		t.Fatalf("bridge asset missing Hermes Responses API call: %q", script)
	}
	if !strings.Contains(script, "/chat/completions") {
		t.Fatalf("bridge asset missing chat completions fallback: %q", script)
	}
	if !strings.Contains(script, "def stream(self, session_id: str, content: Any, on_delta") {
		t.Fatalf("bridge asset missing Hermes streaming entrypoint: %q", script)
	}
	if !strings.Contains(script, "self.sky10.send_delta(") {
		t.Fatalf("bridge asset missing delta send path: %q", script)
	}
	if !strings.Contains(script, "self.sky10.send_done(") {
		t.Fatalf("bridge asset missing done send path: %q", script)
	}
	if !strings.Contains(script, `"stream_id": stream_id`) {
		t.Fatalf("bridge asset missing stream_id propagation: %q", script)
	}
	if !strings.Contains(script, "build_inbound_body") {
		t.Fatalf("bridge asset missing structured inbound content builder: %q", script)
	}
	if !strings.Contains(script, "stage_base64_part") {
		t.Fatalf("bridge asset missing attachment staging helper: %q", script)
	}
	if !strings.Contains(script, "sky10-hermes-media") {
		t.Fatalf("bridge asset missing guest-local media staging root: %q", script)
	}
	if !strings.Contains(script, "build_outbound_content") {
		t.Fatalf("bridge asset missing outbound content builder: %q", script)
	}
	if !strings.Contains(script, `trimmed.startswith("MEDIA:")`) {
		t.Fatalf("bridge asset missing MEDIA artifact extraction: %q", script)
	}
	if !strings.Contains(script, "media_part_from_file") {
		t.Fatalf("bridge asset missing local artifact file encoding: %q", script)
	}
	if !strings.Contains(script, "def warm_up(self) -> None:") {
		t.Fatalf("bridge asset missing Hermes warm-up path: %q", script)
	}
	if !strings.Contains(script, "HERMES_BRIDGE_SKIP_WARMUP") {
		t.Fatalf("bridge asset missing warm-up test escape hatch: %q", script)
	}
}
