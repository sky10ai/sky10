package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	skyapps "github.com/sky10/sky10/pkg/apps"
)

var openClawSharedAssetFiles = []string{
	templateOpenClawPluginPackage,
	templateOpenClawPluginManifest,
	templateOpenClawPluginIndex,
	templateOpenClawPluginMedia,
	templateOpenClawPluginClient,
}

var openClawDockerSharedAssetFiles = append(
	append([]string(nil), openClawSharedAssetFiles...),
	templateOpenClawDockerfile,
	templateOpenClawDockerEntrypoint,
)

var hermesSharedAssetFiles = []string{
	templateHermesBridgeAsset,
}

var hermesDockerSharedAssetFiles = append(
	append([]string(nil), hermesSharedAssetFiles...),
	templateHermesDockerfile,
	templateHermesDockerEntrypoint,
)

var defaultHermesBridgeSkills = []string{
	"code",
	"shell",
	"web-search",
	"file-ops",
}

var ubuntuLimaProgressPlan = []progressStep{
	{ID: "sandbox.prepare", Summary: "Preparing sandbox..."},
	{ID: "vm.start", Summary: "Booting device..."},
}

var openClawLimaProgressPlan = []progressStep{
	{ID: "sandbox.prepare", Summary: "Preparing sandbox..."},
	{ID: "vm.start", Summary: "Booting device..."},
	{ID: "guest.system.packages", Summary: "Installing system packages..."},
	{ID: "guest.node.install", Summary: "Installing Node.js..."},
	{ID: "guest.openclaw.install", Summary: "Installing OpenClaw..."},
	{ID: "guest.chromium.install", Summary: "Installing Chromium..."},
	{ID: "guest.caddy.install", Summary: "Installing Caddy..."},
	{ID: "guest.sky10.join", Summary: "Linking guest sky10 identity..."},
	{ID: "guest.sky10.start", Summary: "Starting guest sky10..."},
	{ID: "guest.openclaw.configure", Summary: "Configuring OpenClaw..."},
	{ID: "guest.openclaw.start", Summary: "Starting OpenClaw..."},
	{ID: "ready.openclaw.gateway", Summary: "Waiting for OpenClaw gateway..."},
	{ID: "ready.guest.sky10", Summary: "Waiting for guest sky10..."},
	{ID: "ready.guest.identity", Summary: "Confirming guest identity..."},
	{ID: "ready.guest.agent", Summary: "Waiting for agent registration..."},
	{ID: "ready.host.connect", Summary: "Connecting host to guest..."},
}

var hermesLimaProgressPlan = []progressStep{
	{ID: "sandbox.prepare", Summary: "Preparing sandbox..."},
	{ID: "vm.start", Summary: "Booting device..."},
	{ID: "guest.system.packages", Summary: "Installing system packages..."},
	{ID: "guest.hermes.install", Summary: "Installing Hermes..."},
	{ID: "guest.hermes.configure", Summary: "Configuring Hermes..."},
	{ID: "guest.sky10.join", Summary: "Linking guest sky10 identity..."},
	{ID: "guest.sky10.start", Summary: "Starting guest sky10..."},
	{ID: "guest.hermes.bridge.start", Summary: "Starting Hermes bridge..."},
	{ID: "ready.guest.hermes", Summary: "Waiting for Hermes CLI..."},
	{ID: "ready.guest.sky10", Summary: "Waiting for guest sky10..."},
	{ID: "ready.guest.identity", Summary: "Confirming guest identity..."},
	{ID: "ready.guest.agent", Summary: "Waiting for agent registration..."},
	{ID: "ready.host.connect", Summary: "Connecting host to guest..."},
}

var openClawDockerLimaProgressPlan = []progressStep{
	{ID: "sandbox.prepare", Summary: "Preparing sandbox..."},
	{ID: "vm.start", Summary: "Booting device..."},
	{ID: "guest.system.packages", Summary: "Installing Docker runtime..."},
	{ID: "guest.docker.configure", Summary: "Configuring Docker runtime..."},
	{ID: "guest.docker.build", Summary: "Building OpenClaw container..."},
	{ID: "guest.docker.start", Summary: "Starting OpenClaw containers..."},
	{ID: "ready.openclaw.gateway", Summary: "Waiting for OpenClaw gateway..."},
	{ID: "ready.guest.sky10", Summary: "Waiting for guest sky10..."},
	{ID: "ready.guest.identity", Summary: "Confirming guest identity..."},
	{ID: "ready.guest.agent", Summary: "Waiting for agent registration..."},
	{ID: "ready.host.connect", Summary: "Connecting host to guest..."},
}

var hermesDockerLimaProgressPlan = []progressStep{
	{ID: "sandbox.prepare", Summary: "Preparing sandbox..."},
	{ID: "vm.start", Summary: "Booting device..."},
	{ID: "guest.system.packages", Summary: "Installing Docker runtime..."},
	{ID: "guest.docker.configure", Summary: "Configuring Docker runtime..."},
	{ID: "guest.docker.build", Summary: "Building Hermes container..."},
	{ID: "guest.docker.start", Summary: "Starting Hermes containers..."},
	{ID: "ready.guest.hermes", Summary: "Waiting for Hermes CLI..."},
	{ID: "ready.guest.sky10", Summary: "Waiting for guest sky10..."},
	{ID: "ready.guest.identity", Summary: "Confirming guest identity..."},
	{ID: "ready.guest.agent", Summary: "Waiting for agent registration..."},
	{ID: "ready.host.connect", Summary: "Connecting host to guest..."},
}

type templateDefinition struct {
	mainAsset string
	assets    []string
}

type guestAgentWaiter func(context.Context, func(context.Context, string, []string) ([]byte, error), string, string, time.Duration) error

type templateReadyCheck struct {
	ID           string
	BeginSummary string
	EndSummary   string
	Wait         func(context.Context, *Manager, string, string) error
}

type templateSpec struct {
	family                string
	definition            templateDefinition
	progressPlan          []progressStep
	supportsModelOverride bool
	macOSOnly             bool
	shellCommand          func(limactl, slug string) string
	prepareSharedDir      func(context.Context, *Manager, Record, string) error
	readyChecks           []templateReadyCheck
	waitForGuestAgent     guestAgentWaiter
}

var sandboxTemplateOrder = []string{
	templateUbuntu,
	templateOpenClaw,
	templateOpenClawDocker,
	templateHermes,
	templateHermesDocker,
}

var sandboxTemplateSpecs = map[string]templateSpec{
	templateUbuntu: {
		definition: templateDefinition{
			mainAsset: templateUbuntuAsset,
			assets:    []string{templateUbuntuAsset},
		},
		progressPlan:     ubuntuLimaProgressPlan,
		prepareSharedDir: prepareBasicTemplateSharedDir,
	},
	templateOpenClaw: {
		family: templateOpenClaw,
		definition: templateDefinition{
			mainAsset: templateOpenClawYAML,
			assets: []string{
				templateOpenClawYAML,
				templateOpenClawDep,
				templateOpenClawSys,
				templateOpenClawUser,
			},
		},
		progressPlan:          openClawLimaProgressPlan,
		supportsModelOverride: true,
		macOSOnly:             true,
		prepareSharedDir:      prepareOpenClawTemplateSharedDir(openClawSharedAssetFiles),
		readyChecks: []templateReadyCheck{
			{
				ID:           "ready.openclaw.gateway",
				BeginSummary: "Waiting for OpenClaw gateway...",
				EndSummary:   "OpenClaw gateway is ready.",
				Wait:         waitForOpenClawGatewayReadyCheck,
			},
		},
		waitForGuestAgent: waitForGuestOpenClawAgent,
	},
	templateOpenClawDocker: {
		family: templateOpenClaw,
		definition: templateDefinition{
			mainAsset: templateOpenClawDockerYAML,
			assets: []string{
				templateOpenClawDockerYAML,
				templateOpenClawDockerDep,
				templateOpenClawDockerSys,
				templateOpenClawDockerUser,
			},
		},
		progressPlan:          openClawDockerLimaProgressPlan,
		supportsModelOverride: true,
		macOSOnly:             true,
		prepareSharedDir:      prepareOpenClawTemplateSharedDir(openClawDockerSharedAssetFiles),
		readyChecks: []templateReadyCheck{
			{
				ID:           "ready.openclaw.gateway",
				BeginSummary: "Waiting for OpenClaw gateway...",
				EndSummary:   "OpenClaw gateway is ready.",
				Wait:         waitForOpenClawGatewayReadyCheck,
			},
		},
		waitForGuestAgent: waitForGuestOpenClawAgent,
	},
	templateHermes: {
		family: templateHermes,
		definition: templateDefinition{
			mainAsset: templateHermesYAML,
			assets: []string{
				templateHermesYAML,
				templateHermesDep,
				templateHermesSys,
				templateHermesUser,
			},
		},
		progressPlan:          hermesLimaProgressPlan,
		supportsModelOverride: true,
		macOSOnly:             true,
		shellCommand:          hermesTemplateShellCommand,
		prepareSharedDir:      prepareHermesTemplateSharedDir(hermesSharedAssetFiles),
		readyChecks: []templateReadyCheck{
			{
				ID:           "ready.guest.hermes",
				BeginSummary: "Waiting for Hermes CLI...",
				EndSummary:   "Hermes CLI is ready.",
				Wait:         waitForHermesCLIReadyCheck,
			},
		},
		waitForGuestAgent: waitForGuestHermesAgent,
	},
	templateHermesDocker: {
		family: templateHermes,
		definition: templateDefinition{
			mainAsset: templateHermesDockerYAML,
			assets: []string{
				templateHermesDockerYAML,
				templateHermesDockerDep,
				templateHermesDockerSys,
				templateHermesDockerUser,
			},
		},
		progressPlan:          hermesDockerLimaProgressPlan,
		supportsModelOverride: true,
		macOSOnly:             true,
		shellCommand:          hermesTemplateShellCommand,
		prepareSharedDir:      prepareHermesTemplateSharedDir(hermesDockerSharedAssetFiles),
		readyChecks: []templateReadyCheck{
			{
				ID:           "ready.guest.hermes",
				BeginSummary: "Waiting for Hermes CLI...",
				EndSummary:   "Hermes CLI is ready.",
				Wait:         waitForHermesCLIReadyCheck,
			},
		},
		waitForGuestAgent: waitForGuestHermesAgent,
	},
}

func sandboxProgressPlan(provider, template string) []progressStep {
	if provider != providerLima {
		return nil
	}
	spec, ok := sandboxTemplateSpecs[normalizeSandboxTemplate(template)]
	if !ok {
		return nil
	}
	return spec.progressPlan
}

func sandboxTemplateDefinition(template string) (templateDefinition, error) {
	spec, err := lookupSandboxTemplateSpec(template)
	if err != nil {
		return templateDefinition{}, err
	}
	return spec.definition, nil
}

func isOpenClawTemplate(template string) bool {
	spec, ok := sandboxTemplateSpecs[normalizeSandboxTemplate(template)]
	return ok && spec.family == templateOpenClaw
}

func isHermesTemplate(template string) bool {
	spec, ok := sandboxTemplateSpecs[normalizeSandboxTemplate(template)]
	return ok && spec.family == templateHermes
}

func defaultShellCommand(slug, template string) string {
	limactl := "limactl"
	if status, err := sandboxAppStatusFor(skyapps.AppLima); err == nil && status != nil && status.Managed {
		if managedPath, pathErr := sandboxAppManagedPath(skyapps.AppLima); pathErr == nil && strings.TrimSpace(managedPath) != "" {
			limactl = shellQuote(managedPath)
		}
	}

	spec, ok := sandboxTemplateSpecs[normalizeSandboxTemplate(template)]
	if ok && spec.shellCommand != nil {
		return spec.shellCommand(limactl, slug)
	}
	return fmt.Sprintf("%s shell %s", limactl, slug)
}

func (m *Manager) finishReady(ctx context.Context, name, limactl string) error {
	rec, err := m.requireRecord(name)
	if err != nil {
		return err
	}

	spec, err := lookupSandboxTemplateSpec(rec.Template)
	if err != nil {
		return err
	}
	for _, step := range spec.readyChecks {
		step := step
		if err := m.runReadyProgressStep(name, step.ID, step.BeginSummary, step.EndSummary, func() error {
			return step.Wait(ctx, m, limactl, name)
		}); err != nil {
			return err
		}
	}
	if spec.waitForGuestAgent != nil {
		if err := m.finishGuestReadyFlow(ctx, name, limactl, *rec, spec.waitForGuestAgent); err != nil {
			return err
		}
	}

	ipAddr, err := lookupLimaInstanceIPv4(ctx, m.outputCmd, limactl, name)
	if err != nil {
		m.logger.Debug("sandbox ip lookup failed", "name", name, "error", err)
	}
	if ipAddr != "" {
		if err := m.updateIPAddress(name, ipAddr); err != nil {
			return err
		}
	}
	if err := m.updateVMStatus(name, "Running"); err != nil {
		return err
	}
	return m.updateStatus(name, "ready", "")
}

func (m *Manager) prepareTemplateSharedDir(ctx context.Context, rec Record) error {
	stateDir := m.sandboxStateDir(rec.Slug)
	if err := m.ensureAgentHome(ctx, rec.Slug, rec.SharedDir); err != nil {
		return err
	}

	spec, err := lookupSandboxTemplateSpec(rec.Template)
	if err != nil {
		return err
	}
	return spec.prepareSharedDir(ctx, m, rec, stateDir)
}

func buildHermesBridgeConfig(rec Record) *hermesBridgeConfig {
	config := &hermesBridgeConfig{
		Sky10RPCURL:  guestSky10LocalRPCURL,
		AgentName:    strings.TrimSpace(rec.Name),
		AgentKeyName: strings.TrimSpace(rec.Slug),
		Skills:       append([]string(nil), defaultHermesBridgeSkills...),
	}
	if config.AgentName == "" {
		config.AgentName = strings.TrimSpace(rec.Slug)
	}
	if config.AgentKeyName == "" {
		config.AgentKeyName = strings.TrimSpace(rec.Name)
	}
	return config
}

func prepareBasicTemplateSharedDir(_ context.Context, _ *Manager, rec Record, stateDir string) error {
	if err := os.MkdirAll(rec.SharedDir, 0o755); err != nil {
		return fmt.Errorf("creating shared directory: %w", err)
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("creating sandbox state directory: %w", err)
	}
	return nil
}

func prepareOpenClawTemplateSharedDir(assetNames []string) func(context.Context, *Manager, Record, string) error {
	sharedAssetNames := append([]string(nil), assetNames...)
	return func(ctx context.Context, m *Manager, rec Record, stateDir string) error {
		hostsHelper, err := loadSandboxAsset(ctx, templateHostsHelper)
		if err != nil {
			return err
		}
		pluginAssets, err := loadSandboxAssets(ctx, sharedAssetNames)
		if err != nil {
			return err
		}

		return prepareOpenClawSharedDir(
			rec.SharedDir,
			stateDir,
			hostsHelper,
			pluginAssets,
			m.resolveSharedEnv(ctx, rec, m.resolveOpenClawSharedEnv),
			m.resolveIdentityInvite(ctx, rec, "sandbox"),
			agentProfileSeed(rec),
		)
	}
}

func prepareHermesTemplateSharedDir(assetNames []string) func(context.Context, *Manager, Record, string) error {
	sharedAssetNames := append([]string(nil), assetNames...)
	return func(ctx context.Context, m *Manager, rec Record, stateDir string) error {
		sharedAssets, err := loadSandboxAssets(ctx, sharedAssetNames)
		if err != nil {
			return err
		}

		return prepareHermesSharedDir(
			rec.SharedDir,
			stateDir,
			m.resolveSharedEnv(ctx, rec, m.resolveHermesSharedEnv),
			sharedAssets,
			buildHermesBridgeConfig(rec),
			m.resolveIdentityInvite(ctx, rec, "hermes sandbox"),
			agentProfileSeed(rec),
		)
	}
}

func agentProfileSeed(rec Record) AgentProfileSeed {
	return AgentProfileSeed{
		DisplayName: rec.Name,
		Slug:        rec.Slug,
		Template:    rec.Template,
		Model:       rec.Model,
	}
}

func (m *Manager) resolveSharedEnv(ctx context.Context, rec Record, resolver func(context.Context) (map[string]string, error)) map[string]string {
	if resolver == nil {
		return map[string]string{}
	}

	values, err := resolver(ctx)
	if err != nil {
		m.logger.Warn("failed to resolve host secrets for sandbox env", "sandbox", rec.Slug, "error", err)
		return map[string]string{}
	}
	return values
}

func (m *Manager) resolveIdentityInvite(ctx context.Context, rec Record, subject string) *IdentityInvite {
	if m.issueIdentityInvite == nil {
		return nil
	}

	value, err := m.issueIdentityInvite(ctx)
	if err != nil {
		m.logger.Warn(fmt.Sprintf("failed to issue host invite for %s bootstrap", subject), "sandbox", rec.Slug, "error", err)
		return nil
	}
	return value
}

func prepareOpenClawSharedDir(sharedDir, stateDir string, hostsHelper []byte, pluginAssets map[string][]byte, resolvedEnv map[string]string, invite *IdentityInvite, seed AgentProfileSeed) error {
	if err := EnsureAgentProfileLayout(sharedDir, seed); err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("creating sandbox state directory: %w", err)
	}

	envPath := filepath.Join(stateDir, ".env")
	existingEnv, err := os.ReadFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("checking sandbox env file: %w", err)
	}
	if err := os.WriteFile(envPath, BuildOpenClawSharedEnv(existingEnv, resolvedEnv), 0o600); err != nil {
		return fmt.Errorf("writing sandbox env file: %w", err)
	}

	if invite != nil && strings.TrimSpace(invite.Code) != "" {
		if err := writeSandboxJoinPayload(stateDir, invite); err != nil {
			return err
		}
	}

	if len(hostsHelper) > 0 {
		helperPath := filepath.Join(stateDir, templateHostsHelper)
		if err := os.WriteFile(helperPath, hostsHelper, 0o755); err != nil {
			return fmt.Errorf("writing hosts helper: %w", err)
		}
	}

	for relPath, body := range pluginAssets {
		targetPath := sandboxSharedAssetTargetPath(stateDir, relPath)
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("creating bundled plugin dir: %w", err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(relPath, ".py") || strings.HasSuffix(relPath, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(targetPath, body, mode); err != nil {
			return fmt.Errorf("writing bundled plugin asset %q: %w", relPath, err)
		}
	}

	return nil
}

func prepareHermesSharedDir(sharedDir, stateDir string, resolvedEnv map[string]string, sharedAssets map[string][]byte, bridgeConfig *hermesBridgeConfig, invite *IdentityInvite, seed AgentProfileSeed) error {
	if err := EnsureAgentProfileLayout(sharedDir, seed); err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("creating sandbox state directory: %w", err)
	}

	envPath := filepath.Join(stateDir, ".env")
	existingEnv, err := os.ReadFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("checking sandbox env file: %w", err)
	}
	if err := os.WriteFile(envPath, BuildHermesSharedEnv(existingEnv, resolvedEnv), 0o600); err != nil {
		return fmt.Errorf("writing sandbox env file: %w", err)
	}

	if bridgeConfig != nil {
		body, err := json.Marshal(bridgeConfig)
		if err != nil {
			return fmt.Errorf("marshaling hermes bridge config: %w", err)
		}
		configPath := filepath.Join(stateDir, templateHermesBridgeConfig)
		if err := os.WriteFile(configPath, append(body, '\n'), 0o600); err != nil {
			return fmt.Errorf("writing hermes bridge config: %w", err)
		}
	}

	if invite != nil && strings.TrimSpace(invite.Code) != "" {
		if err := writeSandboxJoinPayload(stateDir, invite); err != nil {
			return err
		}
	}

	for relPath, body := range sharedAssets {
		targetPath := sandboxSharedAssetTargetPath(stateDir, relPath)
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("creating hermes shared asset dir: %w", err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(relPath, ".py") || strings.HasSuffix(relPath, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(targetPath, body, mode); err != nil {
			return fmt.Errorf("writing hermes shared asset %q: %w", relPath, err)
		}
	}

	return nil
}

func sandboxSharedAssetTargetPath(stateDir, relPath string) string {
	switch {
	case strings.HasPrefix(relPath, templateOpenClawPluginDir+"/"):
		return filepath.Join(stateDir, "plugins", relPath)
	case strings.HasPrefix(relPath, templateOpenClawDockerRuntimeDir+"/"):
		return filepath.Join(stateDir, "runtime", "openclaw", strings.TrimPrefix(relPath, templateOpenClawDockerRuntimeDir+"/"))
	case strings.HasPrefix(relPath, templateHermesDockerRuntimeDir+"/"):
		return filepath.Join(stateDir, "runtime", "hermes", strings.TrimPrefix(relPath, templateHermesDockerRuntimeDir+"/"))
	default:
		return filepath.Join(stateDir, relPath)
	}
}

func writeSandboxJoinPayload(stateDir string, invite *IdentityInvite) error {
	payload := openClawJoinPayload{
		HostIdentity: strings.TrimSpace(invite.HostIdentity),
		Code:         strings.TrimSpace(invite.Code),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling sandbox join payload: %w", err)
	}
	invitePath := filepath.Join(stateDir, templateOpenClawInviteFile)
	if err := os.WriteFile(invitePath, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing sandbox join payload: %w", err)
	}
	return nil
}

func waitForOpenClawGatewayReadyCheck(ctx context.Context, m *Manager, limactl, name string) error {
	return waitForOpenClawGateway(ctx, m.outputCmd, limactl, name, openClawReadyTimeout)
}

func waitForHermesCLIReadyCheck(ctx context.Context, m *Manager, limactl, name string) error {
	return waitForGuestHermesCLI(ctx, m.outputCmd, limactl, name, openClawReadyTimeout)
}

func waitForOpenClawGateway(
	ctx context.Context,
	outputCmd func(context.Context, string, []string) ([]byte, error),
	limactl, name string,
	timeout time.Duration,
) error {
	return waitForGuestHTTPHealth(ctx, outputCmd, limactl, name, openClawReadyURL, "OpenClaw gateway", timeout)
}

func waitForGuestSky10(
	ctx context.Context,
	outputCmd func(context.Context, string, []string) ([]byte, error),
	limactl, name string,
	timeout time.Duration,
) error {
	return waitForGuestHTTPHealth(ctx, outputCmd, limactl, name, guestSky10ReadyURL, "guest sky10", timeout)
}

func waitForGuestOpenClawAgent(
	ctx context.Context,
	outputCmd func(context.Context, string, []string) ([]byte, error),
	limactl, name string,
	timeout time.Duration,
) error {
	return waitForGuestCommand(
		ctx,
		outputCmd,
		limactl,
		name,
		fmt.Sprintf(`curl -fsS http://127.0.0.1:9101/rpc -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","method":"agent.list","params":{},"id":1}' | grep -F '"name":"%s"' >/dev/null`, name),
		"guest OpenClaw agent registration",
		timeout,
	)
}

func waitForGuestHermesAgent(
	ctx context.Context,
	outputCmd func(context.Context, string, []string) ([]byte, error),
	limactl, name string,
	timeout time.Duration,
) error {
	return waitForGuestCommand(
		ctx,
		outputCmd,
		limactl,
		name,
		fmt.Sprintf(`curl -fsS http://127.0.0.1:9101/rpc -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","method":"agent.list","params":{},"id":1}' | grep -F '"name":"%s"' >/dev/null`, name),
		"guest Hermes agent registration",
		timeout,
	)
}

func waitForGuestHermesCLI(
	ctx context.Context,
	outputCmd func(context.Context, string, []string) ([]byte, error),
	limactl, name string,
	timeout time.Duration,
) error {
	return waitForGuestCommand(
		ctx,
		outputCmd,
		limactl,
		name,
		`export PATH="$HOME/.local/bin:$HOME/.cargo/bin:$PATH"; command -v hermes >/dev/null 2>&1 || command -v hermes-shared >/dev/null 2>&1`,
		"Hermes CLI",
		timeout,
	)
}

func waitForGuestHTTPHealth(
	ctx context.Context,
	outputCmd func(context.Context, string, []string) ([]byte, error),
	limactl, name, url, label string,
	timeout time.Duration,
) error {
	return waitForGuestCommand(
		ctx,
		outputCmd,
		limactl,
		name,
		fmt.Sprintf("curl -fsS %s >/dev/null", url),
		label,
		timeout,
	)
}

func waitForGuestCommand(
	ctx context.Context,
	outputCmd func(context.Context, string, []string) ([]byte, error),
	limactl, name, script, label string,
	timeout time.Duration,
) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastErr error
	for {
		_, err := outputCmd(waitCtx, limactl, []string{
			"shell",
			name,
			"--",
			"bash",
			"-lc",
			script,
		})
		if err == nil {
			return nil
		}
		lastErr = err

		select {
		case <-waitCtx.Done():
			if lastErr != nil {
				return fmt.Errorf("waiting for %s: %w", label, lastErr)
			}
			return fmt.Errorf("timed out waiting for %s", label)
		case <-ticker.C:
		}
	}
}

func lookupSandboxTemplateSpec(template string) (templateSpec, error) {
	template = normalizeSandboxTemplate(template)
	spec, ok := sandboxTemplateSpecs[template]
	if ok {
		return spec, nil
	}
	return templateSpec{}, fmt.Errorf("unsupported sandbox template %q (supported: %s)", template, strings.Join(sandboxTemplateOrder, ", "))
}

func normalizeSandboxTemplate(template string) string {
	return strings.ToLower(strings.TrimSpace(template))
}

func hermesTemplateShellCommand(limactl, slug string) string {
	return fmt.Sprintf("%s shell %s -- bash -lc 'hermes-shared'", limactl, slug)
}
