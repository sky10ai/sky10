package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runtimebundles "github.com/sky10/sky10/external/runtimebundles"
	skyapps "github.com/sky10/sky10/pkg/apps"
	"github.com/sky10/sky10/pkg/config"
	skyfs "github.com/sky10/sky10/pkg/fs"
	skysandbox "github.com/sky10/sky10/pkg/sandbox"
)

func TestResolveLimaRuntimeInstallsManagedCopy(t *testing.T) {
	origStatusFor := commandAppStatusFor
	origUpgrade := commandAppUpgrade
	origManagedPath := commandAppManagedPath
	t.Cleanup(func() {
		commandAppStatusFor = origStatusFor
		commandAppUpgrade = origUpgrade
		commandAppManagedPath = origManagedPath
	})

	statusCalls := 0
	commandAppStatusFor = func(id skyapps.ID) (*skyapps.Status, error) {
		statusCalls++
		if id != skyapps.AppLima {
			t.Fatalf("StatusFor() id = %q, want %q", id, skyapps.AppLima)
		}
		if statusCalls == 1 {
			return &skyapps.Status{}, nil
		}
		return &skyapps.Status{
			Managed:    true,
			ActivePath: "/Users/test/.sky10/bin/limactl",
		}, nil
	}

	upgrades := 0
	commandAppUpgrade = func(id skyapps.ID, _ skyapps.ProgressFunc) (*skyapps.ReleaseInfo, error) {
		upgrades++
		if id != skyapps.AppLima {
			t.Fatalf("Upgrade() id = %q, want %q", id, skyapps.AppLima)
		}
		return &skyapps.ReleaseInfo{ID: id, Latest: "v1.2.3"}, nil
	}
	commandAppManagedPath = func(id skyapps.ID) (string, error) {
		if id != skyapps.AppLima {
			t.Fatalf("ManagedPath() id = %q, want %q", id, skyapps.AppLima)
		}
		return "/Users/test/.sky10/bin/limactl", nil
	}

	got, err := resolveLimaRuntime()
	if err != nil {
		t.Fatalf("resolveLimaRuntime() error = %v", err)
	}
	if got != "/Users/test/.sky10/bin/limactl" {
		t.Fatalf("resolveLimaRuntime() = %q, want managed limactl", got)
	}
	if upgrades != 1 {
		t.Fatalf("upgrade count = %d, want 1", upgrades)
	}
}

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
	if err := validateSandboxCreate(sandboxProviderLima, sandboxTemplateOpenClawDocker); err != nil {
		t.Fatalf("validateSandboxCreate(openclaw-docker): %v", err)
	}
	if err := validateSandboxCreate(sandboxProviderLima, sandboxTemplateHermes); err != nil {
		t.Fatalf("validateSandboxCreate(hermes): %v", err)
	}
	if err := validateSandboxCreate(sandboxProviderLima, sandboxTemplateHermesDocker); err != nil {
		t.Fatalf("validateSandboxCreate(hermes-docker): %v", err)
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

	body := []byte(`name=__SKY10_SANDBOX_NAME__ path=__SKY10_SHARED_DIR__ state=__SKY10_STATE_DIR__ port=__SKY10_GUEST_FORWARD_PORT__ gateway=__SKY10_OPENCLAW_GATEWAY_FORWARD_PORT__`)
	got := string(renderLimaTemplate(body, "bobs-burgers", "/Users/bf/Sky10/Drives/Agents/bobs-burgers", "/Users/bf/.sky10/sandboxes/bobs-burgers/state", 39123))

	if strings.Contains(got, templateNameToken) || strings.Contains(got, templateSharedToken) || strings.Contains(got, templateStateToken) || strings.Contains(got, templateForwardedGuestPortToken) || strings.Contains(got, templateOpenClawGatewayPortToken) {
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
	if !strings.Contains(got, "39123") {
		t.Fatalf("renderLimaTemplate() missing forwarded port: %q", got)
	}
	if !strings.Contains(got, "39124") {
		t.Fatalf("renderLimaTemplate() missing OpenClaw gateway forwarded port: %q", got)
	}
}

func TestLocalManagedLimaTemplatesForwardGuestEndpoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		template            string
		wantOpenClawGateway bool
	}{
		{template: sandboxTemplateOpenClaw, wantOpenClawGateway: true},
		{template: sandboxTemplateOpenClawDocker, wantOpenClawGateway: true},
		{template: sandboxTemplateHermes},
		{template: sandboxTemplateHermesDocker},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.template, func(t *testing.T) {
			t.Parallel()

			spec, err := limaTemplateDefinition(tc.template)
			if err != nil {
				t.Fatalf("limaTemplateDefinition(%q): %v", tc.template, err)
			}
			dir, err := findLocalLimaTemplateDir(spec)
			if err != nil {
				t.Fatalf("findLocalLimaTemplateDir(%q): %v", tc.template, err)
			}
			body, err := os.ReadFile(filepath.Join(dir, spec.mainAsset))
			if err != nil {
				t.Fatalf("ReadFile(%q): %v", spec.mainAsset, err)
			}
			text := string(body)
			assertLocalManagedLimaTemplateForwardsGuestSky10(t, text)
			if tc.wantOpenClawGateway {
				assertLocalManagedLimaTemplateForwardsOpenClawGateway(t, text)
			} else if strings.Contains(text, templateOpenClawGatewayPortToken) {
				t.Fatalf("managed Lima template unexpectedly forwards OpenClaw gateway")
			}
		})
	}
}

func assertLocalManagedLimaTemplateForwardsGuestSky10(t *testing.T, text string) {
	t.Helper()

	for _, want := range []string{
		"portForwards:",
		"- lima: user-v2",
		`guestIP: "127.0.0.1"`,
		"guestPort: 9101",
		`hostIP: "127.0.0.1"`,
		"hostPort: __SKY10_GUEST_FORWARD_PORT__",
		"proto: tcp",
		"guestIP: \"127.0.0.1\"\n  guestPortRange: [1, 65535]",
		"guestIP: \"0.0.0.0\"\n  guestPortRange: [1, 65535]",
		"proto: any",
		"ignore: true",
		"http://127.0.0.1:__SKY10_GUEST_FORWARD_PORT__",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("managed Lima template missing %q", want)
		}
	}
	for _, disallowed := range []string{
		"vzNAT: true",
		"http://<guest-ip>",
		"ip -4 addr show dev lima0",
	} {
		if strings.Contains(text, disallowed) {
			t.Fatalf("managed Lima template still contains %q", disallowed)
		}
	}
}

func assertLocalManagedLimaTemplateForwardsOpenClawGateway(t *testing.T, text string) {
	t.Helper()

	for _, want := range []string{
		"guestPort: 18789",
		"hostPort: __SKY10_OPENCLAW_GATEWAY_FORWARD_PORT__",
		"http://127.0.0.1:__SKY10_OPENCLAW_GATEWAY_FORWARD_PORT__",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("managed Lima template missing %q", want)
		}
	}
}

func TestPrepareLimaSharedDir(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	pluginAssets := map[string][]byte{
		agentLimaPluginManifestAsset: []byte(`{"id":"sky10"}` + "\n"),
		agentLimaPluginIndexAsset:    []byte("export default function register() {}\n"),
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
		agentLimaHermesBridgeAsset: []byte("#!/usr/bin/env python3\nprint('ok')\n"),
	}, map[string]string{
		"ANTHROPIC_API_KEY": "anthropic-key",
	}, &hermesBridgeConfig{
		Sky10RPCURL:  "http://127.0.0.1:9101/rpc",
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
	if !strings.Contains(string(configData), `"sky10_rpc_url":"http://127.0.0.1:9101/rpc"`) {
		t.Fatalf("bridge config = %q, want guest sky10 rpc url", string(configData))
	}
	if strings.Contains(string(configData), "host_rpc_url") {
		t.Fatalf("bridge config should not include host_rpc_url: %q", string(configData))
	}
	bridgePath := filepath.Join(stateDir, agentLimaHermesBridge)
	if info, err := os.Stat(bridgePath); err != nil {
		t.Fatalf("Stat(bridge asset) error: %v", err)
	} else if info.Mode()&0o111 == 0 {
		t.Fatalf("bridge asset mode = %v, want executable", info.Mode())
	}
}

func TestPrepareLimaSharedDirDockerRuntimeAssets(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := prepareLimaSharedDir(sandboxTemplateOpenClawDocker, sharedDir, stateDir, []byte("#!/bin/sh\n"), map[string][]byte{
		agentLimaPluginManifestAsset:           []byte(`{"id":"sky10"}` + "\n"),
		agentLimaOpenClawDockerfileAsset:       []byte("FROM ubuntu:24.04\n"),
		agentLimaOpenClawDockerEntrypointAsset: []byte("#!/bin/sh\n"),
	}, map[string]string{
		"OPENAI_API_KEY": "openai-key",
	}, nil, skysandbox.AgentProfileSeed{
		DisplayName: "OpenClaw Docker",
		Slug:        "openclaw-docker",
		Template:    sandboxTemplateOpenClawDocker,
	}); err != nil {
		t.Fatalf("prepareLimaSharedDir(openclaw-docker) error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(stateDir, "plugins", agentLimaPluginManifest)); err != nil {
		t.Fatalf("Stat(plugin manifest) error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "runtime", "openclaw", "Dockerfile")); err != nil {
		t.Fatalf("Stat(runtime Dockerfile) error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "runtime", "openclaw", "entrypoint.sh")); err != nil {
		t.Fatalf("Stat(runtime entrypoint) error: %v", err)
	}
}

func TestLoadLimaSharedAssetsLoadsOpenClawRuntimeBundle(t *testing.T) {
	t.Parallel()

	spec, err := limaTemplateDefinition(sandboxTemplateOpenClawDocker)
	if err != nil {
		t.Fatalf("limaTemplateDefinition(openclaw-docker): %v", err)
	}
	assets, err := loadLimaSharedAssets(context.Background(), spec)
	if err != nil {
		t.Fatalf("loadLimaSharedAssets() error: %v", err)
	}
	manifestBody, ok := assets[agentLimaPluginManifestAsset]
	if !ok {
		t.Fatalf("loadLimaSharedAssets() missing %q", agentLimaPluginManifestAsset)
	}
	if !strings.Contains(string(manifestBody), `"channels"`) {
		t.Fatalf("runtime bundle plugin manifest missing channels: %q", string(manifestBody))
	}
	entrypointBody, ok := assets[agentLimaOpenClawDockerEntrypointAsset]
	if !ok {
		t.Fatalf("loadLimaSharedAssets() missing %q", agentLimaOpenClawDockerEntrypointAsset)
	}
	if !strings.Contains(string(entrypointBody), `PLUGIN_DIR="${SANDBOX_STATE_DIR}/plugins/openclaw-sky10-channel"`) {
		t.Fatalf("runtime bundle entrypoint missing plugin target path: %q", string(entrypointBody))
	}
	if !strings.Contains(string(entrypointBody), `entries.pop("acpx", None)`) {
		t.Fatalf("runtime bundle entrypoint should remove stale acpx config in managed runtime: %q", string(entrypointBody))
	}
	if !strings.Contains(string(entrypointBody), `plugins["allow"] = ["sky10", "anthropic", "browser"]`) {
		t.Fatalf("runtime bundle entrypoint should restrict bundled OpenClaw plugins in managed runtime: %q", string(entrypointBody))
	}
	if !strings.Contains(string(entrypointBody), `plugins.setdefault("slots", {})["memory"] = "none"`) {
		t.Fatalf("runtime bundle entrypoint should disable bundled memory plugin in managed runtime: %q", string(entrypointBody))
	}
	if !strings.Contains(string(entrypointBody), `OPENCLAW_BUNDLED_PLUGINS_DIR`) {
		t.Fatalf("runtime bundle entrypoint should use managed bundled OpenClaw plugin tree: %q", string(entrypointBody))
	}
	if !strings.Contains(string(entrypointBody), `speech-core memory-core image-generation-core media-understanding-core video-generation-core`) {
		t.Fatalf("runtime bundle entrypoint should expose OpenClaw public-surface plugins in managed runtime: %q", string(entrypointBody))
	}
	if !strings.Contains(string(entrypointBody), `prime_managed_openclaw_runtime_deps`) {
		t.Fatalf("runtime bundle entrypoint should seed managed OpenClaw runtime deps: %q", string(entrypointBody))
	}
}

func TestLoadLimaSharedAssetsLoadsHermesRuntimeBundle(t *testing.T) {
	t.Parallel()

	spec, err := limaTemplateDefinition(sandboxTemplateHermesDocker)
	if err != nil {
		t.Fatalf("limaTemplateDefinition(hermes-docker): %v", err)
	}
	assets, err := loadLimaSharedAssets(context.Background(), spec)
	if err != nil {
		t.Fatalf("loadLimaSharedAssets() error: %v", err)
	}
	bridgeBody, ok := assets[agentLimaHermesBridgeAsset]
	if !ok {
		t.Fatalf("loadLimaSharedAssets() missing %q", agentLimaHermesBridgeAsset)
	}
	if !strings.Contains(string(bridgeBody), `"agent.register"`) {
		t.Fatalf("runtime bundle Hermes bridge missing registration call: %q", string(bridgeBody))
	}
	entrypointBody, ok := assets[agentLimaHermesDockerEntrypointAsset]
	if !ok {
		t.Fatalf("loadLimaSharedAssets() missing %q", agentLimaHermesDockerEntrypointAsset)
	}
	if !strings.Contains(string(entrypointBody), `source_env_file "${BRIDGE_ENV}"`) {
		t.Fatalf("runtime bundle Hermes entrypoint missing bridge env load: %q", string(entrypointBody))
	}
}

func TestOpenClawDockerUserScriptPersistsGuestSky10State(t *testing.T) {
	t.Parallel()

	spec, err := limaTemplateDefinition(sandboxTemplateOpenClawDocker)
	if err != nil {
		t.Fatalf("limaTemplateDefinition(openclaw-docker): %v", err)
	}
	dir, err := findLocalLimaTemplateDir(spec)
	if err != nil {
		t.Fatalf("findLocalLimaTemplateDir() error: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, agentLimaOpenClawDockerUser))
	if err != nil {
		t.Fatalf("ReadFile(docker user script) error: %v", err)
	}

	script := string(body)
	if !strings.Contains(script, `mkdir -p "${SANDBOX_STATE_DIR}/sky10-home"`) {
		t.Fatalf("docker user script missing guest sky10 state dir: %q", script)
	}
	if !strings.Contains(script, `- /sandbox-state/sky10-home:/root/.sky10`) {
		t.Fatalf("docker user script missing guest sky10 volume mount: %q", script)
	}
}

func TestHermesDockerUserScriptPersistsGuestSky10State(t *testing.T) {
	t.Parallel()

	spec, err := limaTemplateDefinition(sandboxTemplateHermesDocker)
	if err != nil {
		t.Fatalf("limaTemplateDefinition(hermes-docker): %v", err)
	}
	dir, err := findLocalLimaTemplateDir(spec)
	if err != nil {
		t.Fatalf("findLocalLimaTemplateDir() error: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, agentLimaHermesDockerUser))
	if err != nil {
		t.Fatalf("ReadFile(docker user script) error: %v", err)
	}

	script := string(body)
	if !strings.Contains(script, `mkdir -p "${SANDBOX_STATE_DIR}/sky10-home"`) {
		t.Fatalf("docker user script missing guest sky10 state dir: %q", script)
	}
	if !strings.Contains(script, `- /sandbox-state/sky10-home:/root/.sky10`) {
		t.Fatalf("docker user script missing guest sky10 volume mount: %q", script)
	}
}

func TestOpenClawDockerRuntimeEntrypointUsesIsolatedSky10Runtime(t *testing.T) {
	t.Parallel()

	body, err := runtimebundles.ReadAsset(agentLimaOpenClawDockerEntrypointAsset)
	if err != nil {
		t.Fatalf("runtimebundles.ReadAsset(%q) error: %v", agentLimaOpenClawDockerEntrypointAsset, err)
	}

	assertDockerRuntimeEntrypointUsesIsolatedSky10Runtime(t, string(body))
}

func TestHermesDockerRuntimeEntrypointUsesIsolatedSky10Runtime(t *testing.T) {
	t.Parallel()

	body, err := runtimebundles.ReadAsset(agentLimaHermesDockerEntrypointAsset)
	if err != nil {
		t.Fatalf("runtimebundles.ReadAsset(%q) error: %v", agentLimaHermesDockerEntrypointAsset, err)
	}

	assertDockerRuntimeEntrypointUsesIsolatedSky10Runtime(t, string(body))
}

func assertDockerRuntimeEntrypointUsesIsolatedSky10Runtime(t *testing.T, script string) {
	t.Helper()

	if strings.Contains(script, "set -eux") {
		t.Fatalf("docker runtime entrypoint enables default xtrace: %q", script)
	}
	if !strings.Contains(script, `SKY10_DOCKER_DEBUG`) {
		t.Fatalf("docker runtime entrypoint missing opt-in debug xtrace guard: %q", script)
	}
	for _, forbidden := range []string{
		"sandbox.reconnectGuest",
		"host_rpc_url",
		"SKY10_RECONNECT_HELPER",
		"sky10-managed-reconnect",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("docker runtime entrypoint contains removed guest callback marker %q: %q", forbidden, script)
		}
	}
	for _, want := range []string{
		`export SKY10_HOME="${HOME}/.sky10"`,
		`export SKY10_RUNTIME_DIR="/run/sky10"`,
		`mkdir -p "${SKY10_RUNTIME_DIR}"`,
		`rm -f "${SKY10_RUNTIME_DIR}/daemon.pid" "${SKY10_RUNTIME_DIR}/sky10.sock"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("docker runtime entrypoint missing %q: %q", want, script)
		}
	}
}

func TestHermesDockerRuntimeEntrypointDoesNotTraceSecrets(t *testing.T) {
	t.Parallel()

	body, err := runtimebundles.ReadAsset(agentLimaHermesDockerEntrypointAsset)
	if err != nil {
		t.Fatalf("runtimebundles.ReadAsset(%q) error: %v", agentLimaHermesDockerEntrypointAsset, err)
	}

	script := string(body)
	for _, want := range []string{
		"source_env_file()",
		`source_env_file "${HERMES_HOME}/.env"`,
		`source_env_file "${BRIDGE_ENV}"`,
		"set +x",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("Hermes Docker runtime entrypoint missing %q: %q", want, script)
		}
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
	if strings.Contains(string(body), "ExecStartPost=%h/.bin/sky10-managed-reconnect") {
		t.Fatalf("user script should not install guest-to-host reconnect hook: %q", string(body))
	}
	if !strings.Contains(string(body), "systemctl --user enable sky10.service") {
		t.Fatalf("user script missing guest sky10 systemd enable: %q", string(body))
	}
	if strings.Contains(string(body), "install_guest_reconnect_helper") {
		t.Fatalf("user script should not install guest reconnect helper: %q", string(body))
	}
	if strings.Contains(string(body), `"method": "sandbox.reconnectGuest"`) {
		t.Fatalf("user script should not call sandbox.reconnectGuest: %q", string(body))
	}
	if strings.Contains(string(body), `payload.get("host_rpc_url")`) {
		t.Fatalf("user script should not parse host_rpc_url: %q", string(body))
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
	if !strings.Contains(string(body), `entries.pop("acpx", None)`) {
		t.Fatalf("user script should remove stale acpx config in managed runtime: %q", string(body))
	}
	if !strings.Contains(string(body), `plugins["allow"] = ["sky10", "anthropic", "browser"]`) {
		t.Fatalf("user script should restrict bundled OpenClaw plugins in managed runtime: %q", string(body))
	}
	if !strings.Contains(string(body), `plugins.setdefault("slots", {})["memory"] = "none"`) {
		t.Fatalf("user script should disable bundled memory plugin in managed runtime: %q", string(body))
	}
	if !strings.Contains(string(body), `OPENCLAW_BUNDLED_PLUGINS_DIR`) || !strings.Contains(string(body), `OPENCLAW_NO_RESPAWN=1`) {
		t.Fatalf("user script should wrap OpenClaw gateway with managed runtime environment: %q", string(body))
	}
	if !strings.Contains(string(body), `prime_managed_openclaw_runtime_deps`) {
		t.Fatalf("user script should seed managed OpenClaw runtime deps: %q", string(body))
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
	if !strings.Contains(script, `OPENCLAW_VERSION=2026.4.24`) {
		t.Fatalf("system script missing pinned openclaw version: %q", script)
	}
	if !strings.Contains(script, `npm install -g "openclaw@${OPENCLAW_VERSION}"`) {
		t.Fatalf("system script missing pinned openclaw install command: %q", script)
	}
	if !strings.Contains(script, "configure_managed_openclaw_bundled_plugins") {
		t.Fatalf("system script missing managed OpenClaw bundled plugin tree setup: %q", script)
	}
	if !strings.Contains(script, "managed-runtime-deps") {
		t.Fatalf("system script missing managed OpenClaw runtime dependency seed: %q", script)
	}
	if !strings.Contains(script, `speech-core memory-core image-generation-core media-understanding-core video-generation-core`) {
		t.Fatalf("system script missing managed OpenClaw public-surface plugin tree copy: %q", script)
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

	manifestBody, err := runtimebundles.ReadAsset(agentLimaPluginManifestAsset)
	if err != nil {
		t.Fatalf("runtimebundles.ReadAsset(%q) error: %v", agentLimaPluginManifestAsset, err)
	}
	if !strings.Contains(string(manifestBody), `["code", "shell", "browser", "web-search", "file-ops"]`) {
		t.Fatalf("plugin manifest missing browser skill default: %q", string(manifestBody))
	}
	if !strings.Contains(string(manifestBody), `"channels"`) || !strings.Contains(string(manifestBody), `"sky10"`) {
		t.Fatalf("plugin manifest missing sky10 channel declaration: %q", string(manifestBody))
	}

	indexBody, err := runtimebundles.ReadAsset(agentLimaPluginIndexAsset)
	if err != nil {
		t.Fatalf("runtimebundles.ReadAsset(%q) error: %v", agentLimaPluginIndexAsset, err)
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

	indexBody, err := runtimebundles.ReadAsset(agentLimaPluginIndexAsset)
	if err != nil {
		t.Fatalf("runtimebundles.ReadAsset(%q) error: %v", agentLimaPluginIndexAsset, err)
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

	clientBody, err := runtimebundles.ReadAsset(agentLimaPluginClientAsset)
	if err != nil {
		t.Fatalf("runtimebundles.ReadAsset(%q) error: %v", agentLimaPluginClientAsset, err)
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
	if !strings.Contains(script, "HERMES_RELEASE_REF=v2026.4.23") {
		t.Fatalf("user script missing pinned Hermes release ref: %q", script)
	}
	if !strings.Contains(script, `--branch "${HERMES_RELEASE_REF}"`) {
		t.Fatalf("user script missing pinned Hermes install branch: %q", script)
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
	if strings.Contains(script, "sky10-managed-reconnect") {
		t.Fatalf("user script should not install guest reconnect helper: %q", script)
	}
	if !strings.Contains(script, `mkdir -p "${HOME}/.bin"`) {
		t.Fatalf("user script missing ~/.bin bootstrap dir creation: %q", script)
	}
	if !strings.Contains(script, "sky10.service") {
		t.Fatalf("user script missing guest sky10 service unit: %q", script)
	}
	if !strings.Contains(script, "hermes config set auxiliary.vision.provider main") {
		t.Fatalf("user script missing auxiliary vision config: %q", script)
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

	body, err := runtimebundles.ReadAsset(agentLimaHermesBridgeAsset)
	if err != nil {
		t.Fatalf("runtimebundles.ReadAsset(%q) error: %v", agentLimaHermesBridgeAsset, err)
	}
	script := string(body)
	if !strings.Contains(script, `"agent.register"`) {
		t.Fatalf("bridge asset missing sky10 registration call: %q", script)
	}
	if !strings.Contains(script, "/rpc/events") {
		t.Fatalf("bridge asset missing SSE subscription: %q", script)
	}
	if strings.Contains(script, "host_rpc_url") {
		t.Fatalf("bridge asset should not accept legacy host_rpc_url config: %q", script)
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
