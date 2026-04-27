package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareOpenClawSharedDirDockerAssets(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := prepareOpenClawSharedDir(sharedDir, stateDir, nil, map[string][]byte{
		runtimeBundleOpenClawPluginManifest:   []byte(`{"id":"sky10"}` + "\n"),
		runtimeBundleOpenClawDockerfile:       []byte("FROM ubuntu:24.04\n"),
		runtimeBundleOpenClawDockerEntrypoint: []byte("#!/bin/sh\n"),
	}, map[string]string{
		"OPENAI_API_KEY": "openai-key",
	}, nil, AgentProfileSeed{
		DisplayName: "OpenClaw Docker",
		Slug:        "openclaw-docker",
		Template:    templateOpenClawDocker,
	}); err != nil {
		t.Fatalf("prepareOpenClawSharedDir(openclaw-docker) error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(stateDir, "plugins", templateOpenClawPluginManifest)); err != nil {
		t.Fatalf("Stat(plugin manifest) error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "runtime", "openclaw", "Dockerfile")); err != nil {
		t.Fatalf("Stat(runtime Dockerfile) error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "runtime", "openclaw", "entrypoint.sh")); err != nil {
		t.Fatalf("Stat(runtime entrypoint) error: %v", err)
	}
}

func TestPrepareHermesSharedDir(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := prepareHermesSharedDir(sharedDir, stateDir, map[string]string{
		"ANTHROPIC_API_KEY": "anthropic-key",
	}, map[string][]byte{
		runtimeBundleHermesBridgeAsset: []byte("#!/usr/bin/env python3\nprint('ok')\n"),
	}, &hermesBridgeConfig{
		Sky10RPCURL:  guestSky10LocalRPCURL,
		AgentName:    "Hermes Agent",
		AgentKeyName: "hermes-agent",
		Skills:       []string{"code", "shell"},
	}, &IdentityInvite{
		HostIdentity: "sky10-host",
		Code:         "invite-code",
	}, AgentProfileSeed{
		DisplayName: "Hermes Agent",
		Slug:        "hermes-agent",
		Template:    templateHermes,
	}); err != nil {
		t.Fatalf("prepareHermesSharedDir() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(sharedDir, "workspace")); err != nil {
		t.Fatalf("Stat(agent workspace) error: %v", err)
	}
	assertSymlinkTarget(t, filepath.Join(sharedDir, "workspace", "AGENTS.md"), filepath.Join("..", "AGENTS.md"))

	envData, err := os.ReadFile(filepath.Join(stateDir, ".env"))
	if err != nil {
		t.Fatalf("ReadFile(.env) error: %v", err)
	}
	text := string(envData)
	if !strings.Contains(text, "Optional provider keys for Hermes") {
		t.Fatalf(".env = %q, want Hermes comment header", text)
	}
	if !strings.Contains(text, "ANTHROPIC_API_KEY=anthropic-key") {
		t.Fatalf(".env = %q, want resolved anthropic key", text)
	}
	configData, err := os.ReadFile(filepath.Join(stateDir, templateHermesBridgeConfig))
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
	inviteData, err := os.ReadFile(filepath.Join(stateDir, templateOpenClawInviteFile))
	if err != nil {
		t.Fatalf("ReadFile(invite payload) error: %v", err)
	}
	if !strings.Contains(string(inviteData), `"host_identity":"sky10-host"`) {
		t.Fatalf("invite payload = %q, want host identity", string(inviteData))
	}
	if strings.Contains(string(inviteData), "host_rpc_url") {
		t.Fatalf("invite payload should not include host_rpc_url: %q", string(inviteData))
	}
	if strings.Contains(string(inviteData), "sandbox_slug") {
		t.Fatalf("invite payload should not include sandbox_slug: %q", string(inviteData))
	}
	bridgePath := filepath.Join(stateDir, templateHermesBridgeAsset)
	if info, err := os.Stat(bridgePath); err != nil {
		t.Fatalf("Stat(bridge asset) error: %v", err)
	} else if info.Mode()&0o111 == 0 {
		t.Fatalf("bridge asset mode = %v, want executable", info.Mode())
	}
}

func TestPrepareHermesSharedDirDockerAssets(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := prepareHermesSharedDir(sharedDir, stateDir, map[string]string{
		"ANTHROPIC_API_KEY": "anthropic-key",
	}, map[string][]byte{
		runtimeBundleHermesBridgeAsset:      []byte("#!/usr/bin/env python3\nprint('ok')\n"),
		runtimeBundleHermesDockerfile:       []byte("FROM ubuntu:24.04\n"),
		runtimeBundleHermesDockerEntrypoint: []byte("#!/bin/sh\n"),
	}, &hermesBridgeConfig{
		Sky10RPCURL:  guestSky10LocalRPCURL,
		AgentName:    "Hermes Docker",
		AgentKeyName: "hermes-docker",
	}, nil, AgentProfileSeed{
		DisplayName: "Hermes Docker",
		Slug:        "hermes-docker",
		Template:    templateHermesDocker,
	}); err != nil {
		t.Fatalf("prepareHermesSharedDir(hermes-docker) error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(stateDir, templateHermesBridgeAsset)); err != nil {
		t.Fatalf("Stat(bridge asset) error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "runtime", "hermes", "Dockerfile")); err != nil {
		t.Fatalf("Stat(runtime Dockerfile) error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "runtime", "hermes", "entrypoint.sh")); err != nil {
		t.Fatalf("Stat(runtime entrypoint) error: %v", err)
	}
}

func TestBundledOpenClawDockerUserScriptPersistsGuestSky10State(t *testing.T) {
	t.Parallel()

	body, err := readBundledTemplateAsset(templateOpenClawDockerUser)
	if err != nil {
		t.Fatalf("readBundledTemplateAsset(%q) error: %v", templateOpenClawDockerUser, err)
	}

	script := string(body)
	if !strings.Contains(script, `mkdir -p "${SANDBOX_STATE_DIR}/sky10-home"`) {
		t.Fatalf("bundled OpenClaw Docker user script missing guest sky10 state dir: %q", script)
	}
	if !strings.Contains(script, `- /sandbox-state/sky10-home:/root/.sky10`) {
		t.Fatalf("bundled OpenClaw Docker user script missing guest sky10 volume mount: %q", script)
	}
	for _, want := range []string{
		`SPEC_COMPOSE_FILE="/shared/compose.yaml"`,
		`COMPOSE_FILES+=(-f "${SPEC_COMPOSE_FILE}")`,
		`docker compose "${COMPOSE_FILES[@]}"`,
		`env_file:`,
		`- /sandbox-state/.env`,
		"else\n      status=$?",
		`for attempt in 1 2 3 4 5; do`,
		`retry_command "OpenClaw runtime image pull" docker_pull "${image}"`,
		`emit_progress begin guest.docker.pull "Pulling Docker runtime images..."`,
		`retry_command "Docker Compose build" docker_compose build`,
		`retry_command "Docker Compose up" docker_compose up -d --remove-orphans`,
		`docker_compose build`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("bundled OpenClaw Docker user script missing %q: %q", want, script)
		}
	}
}

func TestBundledOpenClawDockerfileSupportsSpecPackageLayer(t *testing.T) {
	t.Parallel()

	body, err := readBundledRuntimeBundleAsset(runtimeBundleOpenClawDockerfile)
	if err != nil {
		t.Fatalf("readBundledRuntimeBundleAsset(%q) error: %v", runtimeBundleOpenClawDockerfile, err)
	}

	dockerfile := string(body)
	for _, want := range []string{
		`ARG SKY10_OPENCLAW_RUNTIME_IMAGE=ghcr.io/sky10ai/sky10-openclaw-runtime:2026.4.24-ubuntu24.04`,
		`FROM ${SKY10_OPENCLAW_RUNTIME_IMAGE}`,
		`ARG SKY10_AGENT_PACKAGES=""`,
		`apt-get -o Acquire::ForceIPv4=true -o Acquire::Retries=5 install -y ${SKY10_AGENT_PACKAGES}`,
	} {
		if !strings.Contains(dockerfile, want) {
			t.Fatalf("bundled OpenClaw Dockerfile missing %q: %q", want, dockerfile)
		}
	}
}

func TestBundledHermesDockerfileUsesGHCRRuntimeImage(t *testing.T) {
	t.Parallel()

	body, err := readBundledRuntimeBundleAsset(runtimeBundleHermesDockerfile)
	if err != nil {
		t.Fatalf("readBundledRuntimeBundleAsset(%q) error: %v", runtimeBundleHermesDockerfile, err)
	}

	dockerfile := string(body)
	for _, want := range []string{
		`ARG SKY10_HERMES_RUNTIME_IMAGE=ghcr.io/sky10ai/sky10-hermes-runtime:v2026.4.23-ubuntu24.04`,
		`FROM ${SKY10_HERMES_RUNTIME_IMAGE}`,
	} {
		if !strings.Contains(dockerfile, want) {
			t.Fatalf("bundled Hermes Dockerfile missing %q: %q", want, dockerfile)
		}
	}
}

func TestBundledHermesDockerUserScriptPersistsGuestSky10State(t *testing.T) {
	t.Parallel()

	body, err := readBundledTemplateAsset(templateHermesDockerUser)
	if err != nil {
		t.Fatalf("readBundledTemplateAsset(%q) error: %v", templateHermesDockerUser, err)
	}

	script := string(body)
	if !strings.Contains(script, `mkdir -p "${SANDBOX_STATE_DIR}/sky10-home"`) {
		t.Fatalf("bundled Hermes Docker user script missing guest sky10 state dir: %q", script)
	}
	if !strings.Contains(script, `- /sandbox-state/sky10-home:/root/.sky10`) {
		t.Fatalf("bundled Hermes Docker user script missing guest sky10 volume mount: %q", script)
	}
	for _, want := range []string{
		"else\n      status=$?",
		`for attempt in 1 2 3 4 5; do`,
		`retry_command "Hermes runtime image pull" docker_pull "${image}"`,
		`emit_progress begin guest.docker.pull "Pulling Docker runtime images..."`,
		`retry_command "Docker Compose build" docker_compose -f "${COMPOSE_FILE}" build hermes`,
		`retry_command "Docker Compose up" docker_compose -f "${COMPOSE_FILE}" up -d --remove-orphans`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("bundled Hermes Docker user script missing %q: %q", want, script)
		}
	}
}

func TestBundledOpenClawDockerRuntimeEntrypointUsesIsolatedSky10Runtime(t *testing.T) {
	t.Parallel()

	body, err := readBundledRuntimeBundleAsset(runtimeBundleOpenClawDockerEntrypoint)
	if err != nil {
		t.Fatalf("readBundledRuntimeBundleAsset(%q) error: %v", runtimeBundleOpenClawDockerEntrypoint, err)
	}

	assertBundledDockerRuntimeEntrypointUsesIsolatedSky10Runtime(t, string(body))
}

func TestLoadSandboxAssetsLoadsOpenClawRuntimeBundle(t *testing.T) {
	t.Parallel()

	assets, err := loadSandboxAssets(context.Background(), []string{
		runtimeBundleOpenClawPluginManifest,
		runtimeBundleOpenClawDockerEntrypoint,
	})
	if err != nil {
		t.Fatalf("loadSandboxAssets() error: %v", err)
	}
	manifestBody, ok := assets[runtimeBundleOpenClawPluginManifest]
	if !ok {
		t.Fatalf("loadSandboxAssets() missing %q", runtimeBundleOpenClawPluginManifest)
	}
	if !strings.Contains(string(manifestBody), `"channels"`) {
		t.Fatalf("runtime bundle plugin manifest missing channels: %q", string(manifestBody))
	}
	entrypointBody, ok := assets[runtimeBundleOpenClawDockerEntrypoint]
	if !ok {
		t.Fatalf("loadSandboxAssets() missing %q", runtimeBundleOpenClawDockerEntrypoint)
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
	if strings.Contains(string(entrypointBody), `rm -rf "${runtime_root}"`) {
		t.Fatalf("runtime bundle entrypoint should not delete preseeded managed runtime deps: %q", string(entrypointBody))
	}
	if !strings.Contains(string(entrypointBody), `! -name managed-runtime-deps`) {
		t.Fatalf("runtime bundle entrypoint should preserve preseeded managed runtime deps: %q", string(entrypointBody))
	}
	if !strings.Contains(string(entrypointBody), `rm -f "/tmp/.X${DISPLAY#:}-lock" "/tmp/.X11-unix/X${DISPLAY#:}"`) {
		t.Fatalf("runtime bundle entrypoint should clear stale Xvfb display locks before restart: %q", string(entrypointBody))
	}
	if !strings.Contains(string(entrypointBody), `cat /tmp/xvfb.log >&2`) {
		t.Fatalf("runtime bundle entrypoint should surface Xvfb startup failures: %q", string(entrypointBody))
	}
}

func TestLoadSandboxAssetsLoadsHermesRuntimeBundle(t *testing.T) {
	t.Parallel()

	assets, err := loadSandboxAssets(context.Background(), []string{
		runtimeBundleHermesBridgeAsset,
		runtimeBundleHermesDockerEntrypoint,
	})
	if err != nil {
		t.Fatalf("loadSandboxAssets() error: %v", err)
	}
	bridgeBody, ok := assets[runtimeBundleHermesBridgeAsset]
	if !ok {
		t.Fatalf("loadSandboxAssets() missing %q", runtimeBundleHermesBridgeAsset)
	}
	if !strings.Contains(string(bridgeBody), `"agent.register"`) {
		t.Fatalf("runtime bundle Hermes bridge missing registration call: %q", string(bridgeBody))
	}
	entrypointBody, ok := assets[runtimeBundleHermesDockerEntrypoint]
	if !ok {
		t.Fatalf("loadSandboxAssets() missing %q", runtimeBundleHermesDockerEntrypoint)
	}
	if !strings.Contains(string(entrypointBody), `source_env_file "${BRIDGE_ENV}"`) {
		t.Fatalf("runtime bundle Hermes entrypoint missing bridge env load: %q", string(entrypointBody))
	}
}

func TestBundledHermesDockerRuntimeEntrypointUsesIsolatedSky10Runtime(t *testing.T) {
	t.Parallel()

	body, err := readBundledRuntimeBundleAsset(runtimeBundleHermesDockerEntrypoint)
	if err != nil {
		t.Fatalf("readBundledRuntimeBundleAsset(%q) error: %v", runtimeBundleHermesDockerEntrypoint, err)
	}

	assertBundledDockerRuntimeEntrypointUsesIsolatedSky10Runtime(t, string(body))
}

func assertBundledDockerRuntimeEntrypointUsesIsolatedSky10Runtime(t *testing.T, script string) {
	t.Helper()

	if strings.Contains(script, "set -eux") {
		t.Fatalf("bundled Docker runtime entrypoint enables default xtrace: %q", script)
	}
	if !strings.Contains(script, `SKY10_DOCKER_DEBUG`) {
		t.Fatalf("bundled Docker runtime entrypoint missing opt-in debug xtrace guard: %q", script)
	}
	for _, forbidden := range []string{
		"sandbox.reconnectGuest",
		"host_rpc_url",
		"SKY10_RECONNECT_HELPER",
		"sky10-managed-reconnect",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("bundled Docker runtime entrypoint contains removed guest callback marker %q: %q", forbidden, script)
		}
	}
	for _, want := range []string{
		`export SKY10_HOME="${HOME}/.sky10"`,
		`export SKY10_RUNTIME_DIR="/run/sky10"`,
		`mkdir -p "${SKY10_RUNTIME_DIR}"`,
		`rm -f "${SKY10_RUNTIME_DIR}/daemon.pid" "${SKY10_RUNTIME_DIR}/sky10.sock"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("bundled Docker runtime entrypoint missing %q: %q", want, script)
		}
	}
}

func TestBundledHermesDockerRuntimeEntrypointDoesNotTraceSecrets(t *testing.T) {
	t.Parallel()

	body, err := readBundledRuntimeBundleAsset(runtimeBundleHermesDockerEntrypoint)
	if err != nil {
		t.Fatalf("readBundledRuntimeBundleAsset(%q) error: %v", runtimeBundleHermesDockerEntrypoint, err)
	}

	script := string(body)
	for _, want := range []string{
		"source_env_file()",
		`source_env_file "${HERMES_HOME}/.env"`,
		`source_env_file "${BRIDGE_ENV}"`,
		"set +x",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("bundled Hermes Docker runtime entrypoint missing %q: %q", want, script)
		}
	}
}
