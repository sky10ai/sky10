package commands

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/config"
	skyfs "github.com/sky10/sky10/pkg/fs"
	skysandbox "github.com/sky10/sky10/pkg/sandbox"
	"github.com/spf13/cobra"
)

const (
	agentLimaTemplateName     = "openclaw-sky10"
	agentLimaTemplateYAML     = "openclaw-sky10.yaml"
	agentLimaDependencyScript = "openclaw-sky10.dependency.sh"
	agentLimaSystemScript     = "openclaw-sky10.system.sh"
	agentLimaUserScript       = "openclaw-sky10.user.sh"
	agentLimaHermesName       = "hermes-sky10"
	agentLimaHermesYAML       = "hermes-sky10.yaml"
	agentLimaHermesDep        = "hermes-sky10.dependency.sh"
	agentLimaHermesSys        = "hermes-sky10.system.sh"
	agentLimaHermesUser       = "hermes-sky10.user.sh"
	agentLimaHermesBridge     = "hermes-sky10-bridge.py"
	agentLimaHermesBridgeJSON = "bridge.json"
	agentLimaHostsScript      = "update-lima-hosts.sh"
	agentLimaPluginDir        = "openclaw-sky10-channel"
	agentLimaPluginPackage    = agentLimaPluginDir + "/package.json"
	agentLimaPluginManifest   = agentLimaPluginDir + "/openclaw.plugin.json"
	agentLimaPluginIndex      = agentLimaPluginDir + "/src/index.js"
	agentLimaPluginClient     = agentLimaPluginDir + "/src/sky10.js"
	agentLimaRemoteAssetBase  = "https://raw.githubusercontent.com/sky10ai/sky10/main/templates/lima/"
	sandboxProviderLima       = "lima"
	sandboxTemplateOpenClaw   = "openclaw"
	sandboxTemplateHermes     = "hermes"
	agentLimaJoinFile         = "join.json"
	templateNameToken         = "__SKY10_SANDBOX_NAME__"
	templateSharedToken       = "__SKY10_SHARED_DIR__"
	templateStateToken        = "__SKY10_STATE_DIR__"
	agentDriveRootName        = "Agents"
	agentDriveNamePrefix      = "agent-"
	openClawReadyTimeout      = 2 * time.Minute
	guestSky10ReadyURL        = "http://127.0.0.1:9101/health"
	openClawReadyURL          = "http://127.0.0.1:18789/health"
	defaultHermesHostRPCURL   = "http://127.0.0.1:9101/rpc"
)

var agentLimaAssetFiles = []string{
	agentLimaTemplateYAML,
	agentLimaDependencyScript,
	agentLimaSystemScript,
	agentLimaUserScript,
}

var agentLimaSharedPluginFiles = []string{
	agentLimaPluginPackage,
	agentLimaPluginManifest,
	agentLimaPluginIndex,
	agentLimaPluginClient,
}

var agentLimaHermesSharedFiles = []string{
	agentLimaHermesBridge,
}

var defaultHermesBridgeSkills = []string{
	"code",
	"shell",
	"web-search",
	"file-ops",
}

type limaTemplateSpec struct {
	templateID         string
	cacheDir           string
	mainAsset          string
	assets             []string
	sharedAssetFiles   []string
	includeHostsHelper bool
}

type hermesBridgeConfig struct {
	HostRPCURL   string   `json:"host_rpc_url"`
	AgentName    string   `json:"agent_name"`
	AgentKeyName string   `json:"agent_key_name,omitempty"`
	Skills       []string `json:"skills,omitempty"`
}

var sandboxNameWordPattern = regexp.MustCompile(`[a-z0-9]+`)

func sandboxCreateCmd() *cobra.Command {
	var provider string
	var template string
	var model string
	var waitTimeout time.Duration
	var openUI bool

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create or start a named sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			displayName := strings.TrimSpace(args[0])
			if displayName == "" {
				return fmt.Errorf("sandbox name must not be empty")
			}
			slug := slugifySandboxName(displayName)
			if slug == "" {
				return fmt.Errorf("sandbox name must include letters or numbers")
			}

			provider = strings.TrimSpace(strings.ToLower(provider))
			template = strings.TrimSpace(strings.ToLower(template))
			if err := validateSandboxCreate(provider, template); err != nil {
				return err
			}
			spec, err := limaTemplateDefinition(template)
			if err != nil {
				return err
			}

			if runtime.GOOS != "darwin" {
				return fmt.Errorf("sky10 sandbox create --provider %s --template %s is macOS-only for now (the current template uses Lima vz)",
					sandboxProviderLima, template)
			}

			params := skysandbox.CreateParams{
				Name:     displayName,
				Provider: provider,
				Template: template,
				Model:    strings.TrimSpace(model),
			}
			if rec, ok, err := ensureSandboxViaDaemon(cmd.Context(), params); err != nil {
				return err
			} else if ok {
				if waitTimeout > 0 {
					rec, err = waitForSandboxReadyViaDaemon(cmd.Context(), rec.Slug, waitTimeout)
					if err != nil {
						return err
					}
				}
				printSandboxSummary(cmd, *rec)
				maybeOpenSandboxUI(cmd, *rec, openUI)
				return nil
			}

			sharedDir, err := defaultLimaSharedDir(slug)
			if err != nil {
				return err
			}
			stateDir, err := defaultLimaStateDir(slug)
			if err != nil {
				return err
			}
			if err := ensureLocalAgentHome(slug, sharedDir); err != nil {
				return err
			}

			templatePath, hostsScript, err := materializeLimaAssets(cmd.Context(), slug, sharedDir, stateDir, spec)
			if err != nil {
				return err
			}
			sharedAssets, err := loadLimaSharedAssets(cmd.Context(), spec)
			if err != nil {
				return err
			}
			resolvedEnv := map[string]string{}
			var hermesBridge *hermesBridgeConfig
			switch template {
			case sandboxTemplateOpenClaw:
				resolvedEnv, err = resolveOpenClawProviderEnvFromDaemon(cmd.Context())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not resolve host secrets for sandbox env: %v\n", err)
				}
			case sandboxTemplateHermes:
				resolvedEnv, err = resolveHermesProviderEnvFromDaemon(cmd.Context())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not resolve host secrets for sandbox env: %v\n", err)
				}
				hermesBridge = buildHermesBridgeConfig(displayName, slug, "")
			}
			if err := prepareLimaSharedDir(template, sharedDir, stateDir, hostsScript, sharedAssets, resolvedEnv, hermesBridge, skysandbox.AgentMindSeed{
				DisplayName: displayName,
				Slug:        slug,
				Template:    template,
				Model:       model,
			}); err != nil {
				return err
			}

			limactl, err := requireLimaOnPath()
			if err != nil {
				return err
			}

			startArgs := []string{
				"start",
				"--tty=false",
				"--name", slug,
			}
			if strings.TrimSpace(model) != "" {
				startArgs = append(startArgs, "--set", fmt.Sprintf(".param.model = %q", strings.TrimSpace(model)))
			}
			startArgs = append(startArgs, templatePath)

			startCmd := exec.CommandContext(cmd.Context(), limactl, startArgs...)
			startCmd.Stdin = os.Stdin
			startCmd.Stdout = cmd.OutOrStdout()
			startCmd.Stderr = cmd.ErrOrStderr()
			if err := startCmd.Run(); err != nil {
				return fmt.Errorf("starting Lima instance %q: %w", slug, err)
			}

			if waitTimeout > 0 {
				if err := waitForTemplateReady(cmd.Context(), limactl, slug, template, waitTimeout); err != nil {
					return err
				}
			}

			ipAddr, _ := lookupLimaInstanceIPv4(cmd.Context(), limactl, slug)
			rec := skysandbox.Record{
				Name:      displayName,
				Slug:      slug,
				Provider:  provider,
				Template:  template,
				Model:     strings.TrimSpace(model),
				Status:    "ready",
				SharedDir: sharedDir,
				IPAddress: ipAddr,
				Shell:     localSandboxShellCommand(template, slug),
			}
			printSandboxSummary(cmd, rec)
			maybeOpenSandboxUI(cmd, rec, openUI)

			return nil
		},
	}

	cmd.Flags().StringVar(&provider, "provider", "", "Sandbox provider to use")
	cmd.Flags().StringVar(&template, "template", "", "Sandbox template/payload to install")
	cmd.Flags().StringVar(&model, "model", "", "Override the default model for supported sandbox templates")
	cmd.Flags().DurationVar(&waitTimeout, "wait", openClawReadyTimeout, "How long to wait for the sandbox bootstrap to finish after provisioning")
	cmd.Flags().BoolVar(&openUI, "open", false, "Open the sandbox web UI after the VM is ready when a direct URL is available")
	_ = cmd.MarkFlagRequired("provider")
	_ = cmd.MarkFlagRequired("template")
	return cmd
}

func ensureSandboxViaDaemon(ctx context.Context, params skysandbox.CreateParams) (*skysandbox.Record, bool, error) {
	raw, err := rpcCall("sandbox.ensure", params)
	if err != nil {
		if sandboxDaemonUnavailable(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var rec skysandbox.Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, false, fmt.Errorf("parsing sandbox.ensure response: %w", err)
	}
	return &rec, true, nil
}

func waitForSandboxReadyViaDaemon(ctx context.Context, slug string, timeout time.Duration) (*skysandbox.Record, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		rec, err := getSandboxViaDaemon(slug)
		if err != nil {
			return nil, err
		}
		switch rec.Status {
		case "ready":
			return rec, nil
		case "error":
			if strings.TrimSpace(rec.LastError) != "" {
				return nil, fmt.Errorf("sandbox %q failed: %s", rec.Name, rec.LastError)
			}
			return nil, fmt.Errorf("sandbox %q failed", rec.Name)
		}

		select {
		case <-waitCtx.Done():
			return nil, fmt.Errorf("timed out waiting for sandbox %q to become ready", slug)
		case <-ticker.C:
		}
	}
}

func getSandboxViaDaemon(slug string) (*skysandbox.Record, error) {
	raw, err := rpcCall("sandbox.get", skysandbox.NamedParams{Slug: slug})
	if err != nil {
		return nil, err
	}
	var rec skysandbox.Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("parsing sandbox.get response: %w", err)
	}
	return &rec, nil
}

func sandboxDaemonUnavailable(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "daemon not running")
}

func printSandboxSummary(cmd *cobra.Command, rec skysandbox.Record) {
	fmt.Fprintf(cmd.OutOrStdout(), "\nSandbox ready.\n")
	fmt.Fprintf(cmd.OutOrStdout(), "Name:       %s\n", rec.Name)
	fmt.Fprintf(cmd.OutOrStdout(), "Runtime ID: %s\n", rec.Slug)
	fmt.Fprintf(cmd.OutOrStdout(), "Provider:   %s\n", rec.Provider)
	fmt.Fprintf(cmd.OutOrStdout(), "Template:   %s\n", rec.Template)
	fmt.Fprintf(cmd.OutOrStdout(), "Agent home: %s\n", rec.SharedDir)
	if stateDir, err := defaultLimaStateDir(rec.Slug); err == nil {
		fmt.Fprintf(cmd.OutOrStdout(), "State dir:  %s\n", stateDir)
	}

	switch rec.Template {
	case sandboxTemplateHermes:
		fmt.Fprintf(cmd.OutOrStdout(), "Hermes:     installed inside the guest with host chat routed through guest sky10 and a local CLI/TUI via `hermes` and `hermes-shared`\n")
		fmt.Fprintf(cmd.OutOrStdout(), "Launch:     limactl shell %s -- bash -lc 'hermes-shared'\n", rec.Slug)
	default:
		sky10URL, openClawURL := sandboxURLs(rec)
		fmt.Fprintf(cmd.OutOrStdout(), "Guest sky10: installed inside the guest and serving on http://127.0.0.1:9101\n")
		fmt.Fprintf(cmd.OutOrStdout(), "OpenClaw:    installed inside the guest with Chromium, Xvfb, a local gateway on http://127.0.0.1:18789, and a bundled sky10 channel plugin\n")

		if sky10URL != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "sky10 UI:   %s\n", sky10URL)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "sky10 UI:   run 'limactl shell %s -- bash -lc \"ip -4 addr show dev lima0\"' to find the host-reachable guest IP\n", rec.Slug)
		}
		if openClawURL != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "OpenClaw UI:%s %s\n", strings.Repeat(" ", 2), openClawURL)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "OpenClaw UI: run 'limactl shell %s -- bash -lc \"ip -4 addr show dev lima0\"' to find the host-reachable guest IP\n", rec.Slug)
		}
	}
}

func maybeOpenSandboxUI(cmd *cobra.Command, rec skysandbox.Record, openUI bool) {
	if !openUI {
		return
	}
	if rec.Template == sandboxTemplateHermes {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: --open is not supported for the Hermes template yet\n")
		return
	}
	_, openClawURL := sandboxURLs(rec)
	if openClawURL == "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: --open skipped because the guest IP could not be resolved automatically\n")
		return
	}
	if err := openBrowser(openClawURL); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not open browser: %v\n", err)
	}
}

func sandboxURLs(rec skysandbox.Record) (string, string) {
	if rec.Template != sandboxTemplateOpenClaw {
		return "", ""
	}
	if strings.TrimSpace(rec.IPAddress) == "" {
		return "", ""
	}
	return fmt.Sprintf("http://%s:9101", rec.IPAddress), fmt.Sprintf("http://%s:18790/chat?session=main", rec.IPAddress)
}

func localSandboxShellCommand(template, slug string) string {
	if template == sandboxTemplateHermes {
		return fmt.Sprintf("limactl shell %s -- bash -lc 'hermes-shared'", slug)
	}
	return fmt.Sprintf("limactl shell %s", slug)
}

func validateSandboxCreate(provider, template string) error {
	switch {
	case provider == "":
		return fmt.Errorf("provider is required")
	case template == "":
		return fmt.Errorf("template is required")
	case provider != sandboxProviderLima:
		return fmt.Errorf("unsupported sandbox provider %q (supported: %s)", provider, sandboxProviderLima)
	case template != sandboxTemplateOpenClaw && template != sandboxTemplateHermes:
		return fmt.Errorf("unsupported sandbox template %q (supported: %s, %s)", template, sandboxTemplateOpenClaw, sandboxTemplateHermes)
	default:
		return nil
	}
}

func limaTemplateDefinition(template string) (limaTemplateSpec, error) {
	switch template {
	case sandboxTemplateOpenClaw:
		return limaTemplateSpec{
			templateID:         template,
			cacheDir:           agentLimaTemplateName,
			mainAsset:          agentLimaTemplateYAML,
			assets:             append([]string(nil), agentLimaAssetFiles...),
			sharedAssetFiles:   append([]string(nil), agentLimaSharedPluginFiles...),
			includeHostsHelper: true,
		}, nil
	case sandboxTemplateHermes:
		return limaTemplateSpec{
			templateID:       template,
			cacheDir:         agentLimaHermesName,
			mainAsset:        agentLimaHermesYAML,
			sharedAssetFiles: append([]string(nil), agentLimaHermesSharedFiles...),
			assets: []string{
				agentLimaHermesYAML,
				agentLimaHermesDep,
				agentLimaHermesSys,
				agentLimaHermesUser,
			},
		}, nil
	default:
		return limaTemplateSpec{}, fmt.Errorf("unsupported sandbox template %q", template)
	}
}

func defaultLimaSharedDir(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory: %w", err)
	}
	return filepath.Join(home, "Sky10", "Drives", "Agents", name), nil
}

func defaultLimaStateDir(name string) (string, error) {
	root, err := config.RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "sandboxes", name, "state"), nil
}

func slugifySandboxName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	parts := sandboxNameWordPattern.FindAllString(name, -1)
	return strings.Join(parts, "-")
}

func materializeLimaAssets(ctx context.Context, sandboxName, sharedDir, stateDir string, spec limaTemplateSpec) (string, []byte, error) {
	root, err := config.RootDir()
	if err != nil {
		return "", nil, err
	}
	destDir := filepath.Join(root, "lima", "templates", spec.cacheDir)
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return "", nil, fmt.Errorf("creating Lima template cache %q: %w", destDir, err)
	}
	templatePath := filepath.Join(destDir, sandboxName+"-"+spec.mainAsset)
	assetNames := append([]string(nil), spec.assets...)
	if spec.includeHostsHelper {
		assetNames = append(assetNames, agentLimaHostsScript)
	}

	if localDir, err := findLocalLimaTemplateDir(spec); err == nil {
		for _, assetName := range assetNames {
			src := filepath.Join(localDir, assetName)
			dst := filepath.Join(destDir, assetName)
			mode := os.FileMode(0o644)
			if strings.HasSuffix(assetName, ".sh") {
				mode = 0o755
			}
			if assetName == spec.mainAsset {
				if err := copyAndRenderTemplate(src, templatePath, mode, sandboxName, sharedDir, stateDir); err != nil {
					return "", nil, err
				}
				continue
			}
			if err := copyFile(src, dst, mode); err != nil {
				return "", nil, err
			}
		}
		if !spec.includeHostsHelper {
			return templatePath, nil, nil
		}
		helper, err := os.ReadFile(filepath.Join(destDir, agentLimaHostsScript))
		if err != nil {
			return "", nil, fmt.Errorf("reading copied Lima hosts helper: %w", err)
		}
		return templatePath, helper, nil
	}

	for _, assetName := range assetNames {
		body, err := downloadLimaAsset(ctx, assetName)
		if err != nil {
			return "", nil, err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(assetName, ".sh") {
			mode = 0o755
		}
		dst := filepath.Join(destDir, assetName)
		if assetName == spec.mainAsset {
			dst = templatePath
			body = renderLimaTemplate(body, sandboxName, sharedDir, stateDir)
		}
		if err := os.WriteFile(dst, body, mode); err != nil {
			return "", nil, fmt.Errorf("writing Lima asset %q: %w", assetName, err)
		}
	}

	if !spec.includeHostsHelper {
		return templatePath, nil, nil
	}
	helper, err := os.ReadFile(filepath.Join(destDir, agentLimaHostsScript))
	if err != nil {
		return "", nil, fmt.Errorf("reading downloaded Lima hosts helper: %w", err)
	}
	return templatePath, helper, nil
}

func copyAndRenderTemplate(src, dst string, mode os.FileMode, name, sharedDir, stateDir string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading Lima asset %q: %w", src, err)
	}
	if err := os.WriteFile(dst, renderLimaTemplate(data, name, sharedDir, stateDir), mode); err != nil {
		return fmt.Errorf("writing Lima asset %q: %w", dst, err)
	}
	return nil
}

func renderLimaTemplate(body []byte, name, sharedDir, stateDir string) []byte {
	rendered := strings.ReplaceAll(string(body), templateNameToken, name)
	rendered = strings.ReplaceAll(rendered, templateSharedToken, sharedDir)
	rendered = strings.ReplaceAll(rendered, templateStateToken, stateDir)
	return []byte(rendered)
}

func findLocalLimaTemplateDir(spec limaTemplateSpec) (string, error) {
	candidates := make([]string, 0, 2)
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}

	for _, start := range candidates {
		for _, dir := range walkUp(start) {
			templateDir := filepath.Join(dir, "templates", "lima")
			if hasLimaTemplateAssets(templateDir, spec) {
				return templateDir, nil
			}
		}
	}
	return "", fmt.Errorf("local Lima template not found")
}

func walkUp(start string) []string {
	start = filepath.Clean(start)
	var dirs []string
	for {
		dirs = append(dirs, start)
		parent := filepath.Dir(start)
		if parent == start {
			break
		}
		start = parent
	}
	return dirs
}

func hasLimaTemplateAssets(dir string, spec limaTemplateSpec) bool {
	names := append([]string(nil), spec.assets...)
	if spec.includeHostsHelper {
		names = append(names, agentLimaHostsScript)
	}
	for _, name := range names {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

func copyFile(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading Lima asset %q: %w", src, err)
	}
	if err := os.WriteFile(dst, data, mode); err != nil {
		return fmt.Errorf("writing Lima asset %q: %w", dst, err)
	}
	return nil
}

func downloadLimaAsset(ctx context.Context, name string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, agentLimaRemoteAssetBase+name, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("building Lima asset request for %q: %w", name, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading Lima asset %q: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading Lima asset %q: unexpected HTTP %d", name, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading Lima asset %q: %w", name, err)
	}
	return body, nil
}

func prepareLimaSharedDir(template, sharedDir, stateDir string, hostsScript []byte, sharedAssets map[string][]byte, resolvedEnv map[string]string, hermesBridge *hermesBridgeConfig, seed skysandbox.AgentMindSeed) error {
	if err := skysandbox.EnsureAgentMindLayout(sharedDir, seed); err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("creating sandbox state directory %q: %w", stateDir, err)
	}

	envPath := filepath.Join(stateDir, ".env")
	existingEnv, err := os.ReadFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("checking sandbox env file %q: %w", envPath, err)
	}
	switch template {
	case sandboxTemplateOpenClaw:
		if err := os.WriteFile(envPath, skysandbox.BuildOpenClawSharedEnv(existingEnv, resolvedEnv), 0o600); err != nil {
			return fmt.Errorf("writing sandbox env file %q: %w", envPath, err)
		}
	case sandboxTemplateHermes:
		if err := os.WriteFile(envPath, skysandbox.BuildHermesSharedEnv(existingEnv, resolvedEnv), 0o600); err != nil {
			return fmt.Errorf("writing sandbox env file %q: %w", envPath, err)
		}
		if hermesBridge != nil {
			body, err := json.Marshal(hermesBridge)
			if err != nil {
				return fmt.Errorf("marshaling hermes bridge config: %w", err)
			}
			configPath := filepath.Join(stateDir, agentLimaHermesBridgeJSON)
			if err := os.WriteFile(configPath, append(body, '\n'), 0o600); err != nil {
				return fmt.Errorf("writing hermes bridge config %q: %w", configPath, err)
			}
		}
	}

	helperPath := filepath.Join(stateDir, agentLimaHostsScript)
	if len(hostsScript) > 0 {
		if err := os.WriteFile(helperPath, hostsScript, 0o755); err != nil {
			return fmt.Errorf("writing Lima hosts helper %q: %w", helperPath, err)
		}
	}

	for relPath, body := range sharedAssets {
		targetPath := filepath.Join(stateDir, relPath)
		if template == sandboxTemplateOpenClaw {
			targetPath = filepath.Join(stateDir, "plugins", relPath)
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("creating bundled plugin dir %q: %w", filepath.Dir(targetPath), err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(relPath, ".py") || strings.HasSuffix(relPath, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(targetPath, body, mode); err != nil {
			return fmt.Errorf("writing bundled plugin asset %q: %w", targetPath, err)
		}
	}

	return nil
}

func ensureLocalAgentHome(slug, sharedDir string) error {
	if err := skysandbox.EnsureAgentHomeLayout(sharedDir); err != nil {
		return err
	}

	cfgDir, err := config.Dir()
	if err != nil {
		return fmt.Errorf("resolving drive config directory: %w", err)
	}
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		return fmt.Errorf("creating drive config directory: %w", err)
	}

	manager := skyfs.NewDriveManager(nil, filepath.Join(cfgDir, "drives.json"))
	cleanPath := filepath.Clean(sharedDir)
	driveRoot := filepath.Clean(filepath.Dir(cleanPath))
	legacyIDs := make([]string, 0)
	rootReady := false
	for _, drive := range manager.ListDrives() {
		driveName := strings.TrimSpace(drive.Name)
		drivePath := filepath.Clean(strings.TrimSpace(drive.LocalPath))
		switch {
		case drivePath == driveRoot:
			if driveName != agentDriveRootName {
				return fmt.Errorf("drive %q already exists with path %q; expected drive %q", driveName, drive.LocalPath, agentDriveRootName)
			}
			rootReady = true
		case driveName == agentDriveRootName:
			return fmt.Errorf("drive %q already exists with path %q", agentDriveRootName, drive.LocalPath)
		case strings.HasPrefix(driveName, agentDriveNamePrefix) && filepath.Clean(filepath.Dir(drivePath)) == driveRoot:
			legacyIDs = append(legacyIDs, drive.ID)
		}
	}
	for _, id := range legacyIDs {
		if err := manager.RemoveDrive(id); err != nil {
			return fmt.Errorf("removing legacy agent drive %q: %w", id, err)
		}
	}
	if rootReady {
		return nil
	}

	if _, err := manager.CreateDrive(agentDriveRootName, driveRoot, agentDriveRootName); err != nil {
		return fmt.Errorf("creating drive %q for agent home %q: %w", agentDriveRootName, slug, err)
	}
	return nil
}

func waitForOpenClawReady(ctx context.Context, limactl, name string, timeout time.Duration) error {
	if err := waitForGuestHTTPHealth(ctx, limactl, name, openClawReadyURL, "OpenClaw", timeout); err != nil {
		return err
	}
	if err := waitForGuestHTTPHealth(ctx, limactl, name, guestSky10ReadyURL, "guest-local sky10", timeout); err != nil {
		return err
	}
	return waitForGuestCommand(
		ctx,
		limactl,
		name,
		fmt.Sprintf(`curl -fsS http://127.0.0.1:9101/rpc -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","method":"agent.list","params":{},"id":1}' | grep -F '"name":"%s"' >/dev/null`, name),
		"guest OpenClaw agent registration",
		timeout,
	)
}

func waitForHermesReady(ctx context.Context, limactl, name string, timeout time.Duration) error {
	return waitForGuestCommand(
		ctx,
		limactl,
		name,
		`export PATH="$HOME/.local/bin:$HOME/.cargo/bin:$PATH"; command -v hermes >/dev/null`,
		"Hermes CLI",
		timeout,
	)
}

func waitForTemplateReady(ctx context.Context, limactl, name, template string, timeout time.Duration) error {
	switch template {
	case sandboxTemplateOpenClaw:
		return waitForOpenClawReady(ctx, limactl, name, timeout)
	case sandboxTemplateHermes:
		return waitForHermesReady(ctx, limactl, name, timeout)
	default:
		return nil
	}
}

func waitForGuestHTTPHealth(ctx context.Context, limactl, name, url, label string, timeout time.Duration) error {
	return waitForGuestCommand(ctx, limactl, name, fmt.Sprintf("curl -fsS %s >/dev/null", url), label, timeout)
}

func waitForGuestCommand(ctx context.Context, limactl, name, script, label string, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastErr error
	for {
		cmd := exec.CommandContext(waitCtx, limactl, "shell", name, "--", "bash", "-lc", script)
		if err := cmd.Run(); err == nil {
			return nil
		} else {
			lastErr = err
		}

		select {
		case <-waitCtx.Done():
			if lastErr != nil {
				return fmt.Errorf("timed out waiting for %s in sandbox %q: %w", label, name, lastErr)
			}
			return fmt.Errorf("timed out waiting for %s in sandbox %q", label, name)
		case <-ticker.C:
		}
	}
}

func loadLimaSharedAssets(ctx context.Context, spec limaTemplateSpec) (map[string][]byte, error) {
	assets := make(map[string][]byte, len(spec.sharedAssetFiles))
	if len(spec.sharedAssetFiles) == 0 {
		return assets, nil
	}
	if localDir, err := findLocalLimaTemplateDir(spec); err == nil {
		for _, assetName := range spec.sharedAssetFiles {
			body, err := os.ReadFile(filepath.Join(localDir, assetName))
			if err != nil {
				return nil, fmt.Errorf("reading local Lima shared asset %q: %w", assetName, err)
			}
			assets[assetName] = body
		}
		return assets, nil
	}

	for _, assetName := range spec.sharedAssetFiles {
		body, err := downloadLimaAsset(ctx, assetName)
		if err != nil {
			return nil, err
		}
		assets[assetName] = body
	}
	return assets, nil
}

func resolveOpenClawProviderEnvFromDaemon(ctx context.Context) (map[string]string, error) {
	return skysandbox.ResolveOpenClawProviderEnv(ctx, providerSecretLookupFromDaemon())
}

func resolveHermesProviderEnvFromDaemon(ctx context.Context) (map[string]string, error) {
	return skysandbox.ResolveHermesProviderEnv(ctx, providerSecretLookupFromDaemon())
}

func buildHermesBridgeConfig(agentName, agentKeyName, hostRPCURL string) *hermesBridgeConfig {
	config := &hermesBridgeConfig{
		HostRPCURL:   strings.TrimSpace(hostRPCURL),
		AgentName:    strings.TrimSpace(agentName),
		AgentKeyName: strings.TrimSpace(agentKeyName),
		Skills:       append([]string(nil), defaultHermesBridgeSkills...),
	}
	if config.HostRPCURL == "" {
		config.HostRPCURL = defaultHermesHostRPCURL
	}
	if config.AgentName == "" {
		config.AgentName = strings.TrimSpace(agentKeyName)
	}
	if config.AgentKeyName == "" {
		config.AgentKeyName = strings.TrimSpace(agentName)
	}
	return config
}

func providerSecretLookupFromDaemon() skysandbox.ProviderSecretLookup {
	return func(_ context.Context, idOrName string) ([]byte, error) {
		raw, err := rpcCall("secrets.get", map[string]string{"id_or_name": idOrName})
		if err != nil {
			switch {
			case strings.Contains(err.Error(), "daemon not running"):
				return nil, skysandbox.ErrProviderSecretNotFound
			case strings.Contains(strings.ToLower(err.Error()), "secret not found"):
				return nil, skysandbox.ErrProviderSecretNotFound
			default:
				return nil, err
			}
		}

		var secret struct {
			Payload string `json:"payload"`
		}
		if err := json.Unmarshal(raw, &secret); err != nil {
			return nil, fmt.Errorf("parsing secret %q: %w", idOrName, err)
		}
		payload, err := base64.StdEncoding.DecodeString(secret.Payload)
		if err != nil {
			return nil, fmt.Errorf("decoding secret %q: %w", idOrName, err)
		}
		return payload, nil
	}
}

func lookupLimaInstanceIPv4(ctx context.Context, limactl, name string) (string, error) {
	scripts := []string{
		`ip -4 addr show dev lima0 | awk '/inet / {sub(/\/.*/, "", $2); print $2; exit}'`,
		`ip -4 route get 1.1.1.1 | awk '{for (i = 1; i <= NF; i++) if ($i == "src") {print $(i + 1); exit}}'`,
	}
	var lastErr error
	for _, script := range scripts {
		cmd := exec.CommandContext(ctx, limactl, "shell", name, "--", "bash", "-lc", script)
		out, err := cmd.Output()
		if err != nil {
			lastErr = err
			continue
		}
		if ip := strings.TrimSpace(string(out)); ip != "" {
			return ip, nil
		}
	}
	if lastErr != nil {
		return "", fmt.Errorf("querying guest IP: %w", lastErr)
	}
	return "", nil
}

func requireLimaOnPath() (string, error) {
	limactl, err := exec.LookPath("limactl")
	if err == nil {
		return limactl, nil
	}
	return "", fmt.Errorf("limactl not found on PATH; managed Lima installs are not used by sandbox flows yet")
}
