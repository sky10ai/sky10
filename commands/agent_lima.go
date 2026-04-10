package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	skyapps "github.com/sky10/sky10/pkg/apps"
	"github.com/sky10/sky10/pkg/config"
	"github.com/spf13/cobra"
)

const (
	agentLimaTemplateName    = "openclaw-sky10"
	agentLimaTemplateYAML    = "openclaw-sky10.yaml"
	agentLimaSystemScript    = "openclaw-sky10.system.sh"
	agentLimaUserScript      = "openclaw-sky10.user.sh"
	agentLimaHostsScript     = "update-lima-hosts.sh"
	agentLimaRemoteAssetBase = "https://raw.githubusercontent.com/sky10ai/sky10/main/templates/lima/"
	sandboxDomainSuffix      = ".sb.sky10.local"
	sandboxCertFile          = "sb.sky10.local.pem"
	sandboxCertKeyFile       = "sb.sky10.local-key.pem"
	sandboxProviderLima      = "lima"
	sandboxTemplateOpenClaw  = "openclaw"
	templateNameToken        = "__SKY10_SANDBOX_NAME__"
	templateSharedToken      = "__SKY10_SHARED_DIR__"
)

var agentLimaAssetFiles = []string{
	agentLimaTemplateYAML,
	agentLimaSystemScript,
	agentLimaUserScript,
}

var sandboxNameWordPattern = regexp.MustCompile(`[a-z0-9]+`)

var (
	sandboxManagedAppStatus  = skyapps.StatusFor
	sandboxManagedAppUpgrade = skyapps.Upgrade
)

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

			if runtime.GOOS != "darwin" {
				return fmt.Errorf("sky10 sandbox create --provider %s --template %s is macOS-only for now (the current template uses Lima vz)",
					sandboxProviderLima, sandboxTemplateOpenClaw)
			}

			health, err := loadDaemonHealth()
			if err != nil {
				return err
			}
			guestRPCURL, err := guestRPCURLFromHTTPAddr(health.HTTPAddr)
			if err != nil {
				return err
			}

			sharedDir, err := defaultLimaSharedDir(slug)
			if err != nil {
				return err
			}

			templatePath, hostsScript, err := materializeLimaAssets(cmd.Context(), slug, sharedDir)
			if err != nil {
				return err
			}
			if err := prepareLimaSharedDir(sharedDir, hostsScript); err != nil {
				return err
			}
			if err := ensureSandboxCertificate(cmd.Context(), cmd, sharedDir); err != nil {
				return err
			}

			limactl, err := ensureManagedAppPath(cmd, skyapps.AppLima)
			if err != nil {
				return err
			}

			startArgs := []string{
				"start",
				"--tty=false",
				"--name", slug,
				"--set", fmt.Sprintf(".param.sky10RPCURL = %q", guestRPCURL),
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
				if err := waitForAgentRegistration(cmd.Context(), slug, waitTimeout); err != nil {
					return err
				}
			}

			ipAddr, ipErr := lookupLimaInstanceIPv4(cmd.Context(), limactl, slug)
			httpURL := ""
			if ipErr == nil && ipAddr != "" && !limaTLSCertsPresent(sharedDir) {
				httpURL = fmt.Sprintf("http://%s:18790/chat?session=main", ipAddr)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "\nSandbox ready.\n")
			fmt.Fprintf(cmd.OutOrStdout(), "Name:       %s\n", displayName)
			fmt.Fprintf(cmd.OutOrStdout(), "Runtime ID: %s\n", slug)
			fmt.Fprintf(cmd.OutOrStdout(), "Provider:   %s\n", provider)
			fmt.Fprintf(cmd.OutOrStdout(), "Template:   %s\n", template)
			fmt.Fprintf(cmd.OutOrStdout(), "Shared dir: %s\n", sharedDir)
			fmt.Fprintf(cmd.OutOrStdout(), "Guest RPC:  %s\n", guestRPCURL)
			fmt.Fprintf(cmd.OutOrStdout(), "Network:    routed through the host sky10 daemon/private network\n")

			if httpURL != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "OpenClaw:   %s\n", httpURL)
			} else if limaTLSCertsPresent(sharedDir) {
				fmt.Fprintf(cmd.OutOrStdout(), "OpenClaw:   run %s and open %s\n",
					filepath.Join(sharedDir, agentLimaHostsScript), sandboxHTTPSURL(slug))
			} else if ipAddr != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "OpenClaw:   guest IP %s on port 18790 (TLS certs are enabled, so hostname mapping is recommended)\n", ipAddr)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "OpenClaw:   run 'limactl shell %s -- bash -lc \"ip -4 route get 1.1.1.1\"' to find the guest IP\n", slug)
			}

			if openUI {
				switch {
				case httpURL != "":
					if err := openBrowser(httpURL); err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not open browser: %v\n", err)
					}
				case limaTLSCertsPresent(sharedDir):
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: --open skipped because TLS hostname mapping still depends on %s\n", filepath.Join(sharedDir, agentLimaHostsScript))
				default:
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: --open skipped because the guest IP could not be resolved automatically\n")
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&provider, "provider", "", "Sandbox provider to use")
	cmd.Flags().StringVar(&template, "template", "", "Sandbox template/payload to install")
	cmd.Flags().StringVar(&model, "model", "", "Override the default OpenClaw model for this sandbox")
	cmd.Flags().DurationVar(&waitTimeout, "wait", 2*time.Minute, "How long to wait for the sandbox agent to register back to the host daemon")
	cmd.Flags().BoolVar(&openUI, "open", false, "Open the OpenClaw UI after the VM is ready when a direct URL is available")
	_ = cmd.MarkFlagRequired("provider")
	_ = cmd.MarkFlagRequired("template")
	return cmd
}

func validateSandboxCreate(provider, template string) error {
	switch {
	case provider == "":
		return fmt.Errorf("provider is required")
	case template == "":
		return fmt.Errorf("template is required")
	case provider != sandboxProviderLima:
		return fmt.Errorf("unsupported sandbox provider %q (supported: %s)", provider, sandboxProviderLima)
	case template != sandboxTemplateOpenClaw:
		return fmt.Errorf("unsupported sandbox template %q (supported: %s)", template, sandboxTemplateOpenClaw)
	default:
		return nil
	}
}

func loadDaemonHealth() (*daemonHealth, error) {
	raw, err := rpcCall("skyfs.health", nil)
	if err != nil {
		return nil, err
	}
	var health daemonHealth
	if err := json.Unmarshal(raw, &health); err != nil {
		return nil, fmt.Errorf("parsing daemon health response: %w", err)
	}
	if strings.TrimSpace(health.HTTPAddr) == "" {
		return nil, fmt.Errorf("daemon HTTP server is not running (start with 'sky10 serve')")
	}
	return &health, nil
}

type daemonHealth struct {
	HTTPAddr string `json:"http_addr"`
}

func guestRPCURLFromHTTPAddr(httpAddr string) (string, error) {
	port, err := extractPortSuffix(httpAddr)
	if err != nil {
		return "", fmt.Errorf("parsing daemon http_addr %q: %w", httpAddr, err)
	}
	return "http://host.lima.internal" + port, nil
}

func extractPortSuffix(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", fmt.Errorf("empty address")
	}
	if strings.HasPrefix(addr, ":") {
		if len(addr) == 1 {
			return "", fmt.Errorf("missing port")
		}
		return addr, nil
	}
	_, port, err := net.SplitHostPort(addr)
	if err == nil && port != "" {
		return ":" + port, nil
	}
	if idx := strings.LastIndex(addr, ":"); idx >= 0 && idx+1 < len(addr) {
		return addr[idx:], nil
	}
	return "", fmt.Errorf("missing port")
}

func defaultLimaSharedDir(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory: %w", err)
	}
	return filepath.Join(home, "sky10", "sandboxes", name), nil
}

func sandboxHostname(name string) string {
	return name + sandboxDomainSuffix
}

func sandboxHTTPSURL(name string) string {
	return "https://" + sandboxHostname(name) + ":18790/chat?session=main"
}

func slugifySandboxName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	parts := sandboxNameWordPattern.FindAllString(name, -1)
	return strings.Join(parts, "-")
}

func materializeLimaAssets(ctx context.Context, sandboxName, sharedDir string) (string, []byte, error) {
	root, err := config.RootDir()
	if err != nil {
		return "", nil, err
	}
	destDir := filepath.Join(root, "lima", "templates", agentLimaTemplateName)
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return "", nil, fmt.Errorf("creating Lima template cache %q: %w", destDir, err)
	}
	templatePath := filepath.Join(destDir, sandboxName+"-"+agentLimaTemplateYAML)

	if localDir, err := findLocalLimaTemplateDir(); err == nil {
		for _, assetName := range append(append([]string(nil), agentLimaAssetFiles...), agentLimaHostsScript) {
			src := filepath.Join(localDir, assetName)
			dst := filepath.Join(destDir, assetName)
			mode := os.FileMode(0o644)
			if strings.HasSuffix(assetName, ".sh") {
				mode = 0o755
			}
			if assetName == agentLimaTemplateYAML {
				if err := copyAndRenderTemplate(src, templatePath, mode, sandboxName, sharedDir); err != nil {
					return "", nil, err
				}
				continue
			}
			if err := copyFile(src, dst, mode); err != nil {
				return "", nil, err
			}
		}
		helper, err := os.ReadFile(filepath.Join(destDir, agentLimaHostsScript))
		if err != nil {
			return "", nil, fmt.Errorf("reading copied Lima hosts helper: %w", err)
		}
		return templatePath, helper, nil
	}

	for _, assetName := range append(append([]string(nil), agentLimaAssetFiles...), agentLimaHostsScript) {
		body, err := downloadLimaAsset(ctx, assetName)
		if err != nil {
			return "", nil, err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(assetName, ".sh") {
			mode = 0o755
		}
		dst := filepath.Join(destDir, assetName)
		if assetName == agentLimaTemplateYAML {
			dst = templatePath
			body = renderLimaTemplate(body, sandboxName, sharedDir)
		}
		if err := os.WriteFile(dst, body, mode); err != nil {
			return "", nil, fmt.Errorf("writing Lima asset %q: %w", assetName, err)
		}
	}

	helper, err := os.ReadFile(filepath.Join(destDir, agentLimaHostsScript))
	if err != nil {
		return "", nil, fmt.Errorf("reading downloaded Lima hosts helper: %w", err)
	}
	return templatePath, helper, nil
}

func copyAndRenderTemplate(src, dst string, mode os.FileMode, name, sharedDir string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading Lima asset %q: %w", src, err)
	}
	if err := os.WriteFile(dst, renderLimaTemplate(data, name, sharedDir), mode); err != nil {
		return fmt.Errorf("writing Lima asset %q: %w", dst, err)
	}
	return nil
}

func renderLimaTemplate(body []byte, name, sharedDir string) []byte {
	rendered := strings.ReplaceAll(string(body), templateNameToken, name)
	rendered = strings.ReplaceAll(rendered, templateSharedToken, sharedDir)
	return []byte(rendered)
}

func findLocalLimaTemplateDir() (string, error) {
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
			if hasLimaTemplateAssets(templateDir) {
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

func hasLimaTemplateAssets(dir string) bool {
	for _, name := range append(append([]string(nil), agentLimaAssetFiles...), agentLimaHostsScript) {
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

func prepareLimaSharedDir(sharedDir string, hostsScript []byte) error {
	if err := os.MkdirAll(sharedDir, 0o700); err != nil {
		return fmt.Errorf("creating shared Lima directory %q: %w", sharedDir, err)
	}

	envPath := filepath.Join(sharedDir, ".env")
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		stub := strings.Join([]string{
			"# Optional provider keys for OpenClaw inside Lima.",
			"# Fill these in before using the agent for real requests.",
			"ANTHROPIC_API_KEY=",
			"OPENAI_API_KEY=",
			"",
		}, "\n")
		if err := os.WriteFile(envPath, []byte(stub), 0o600); err != nil {
			return fmt.Errorf("writing shared env file %q: %w", envPath, err)
		}
	} else if err != nil {
		return fmt.Errorf("checking shared env file %q: %w", envPath, err)
	}

	helperPath := filepath.Join(sharedDir, agentLimaHostsScript)
	if len(hostsScript) > 0 {
		if err := os.WriteFile(helperPath, hostsScript, 0o755); err != nil {
			return fmt.Errorf("writing Lima hosts helper %q: %w", helperPath, err)
		}
	}

	return nil
}

func waitForAgentRegistration(ctx context.Context, name string, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastErr error
	for {
		registered, err := agentRegistered(name)
		if err == nil && registered {
			return nil
		}
		if err != nil {
			lastErr = err
		}

		select {
		case <-waitCtx.Done():
			if lastErr != nil {
				return fmt.Errorf("timed out waiting for agent %q to register: %w", name, lastErr)
			}
			return fmt.Errorf("timed out waiting for agent %q to register", name)
		case <-ticker.C:
		}
	}
}

func agentRegistered(name string) (bool, error) {
	raw, err := rpcCall("agent.list", nil)
	if err != nil {
		return false, err
	}
	var resp struct {
		Agents []struct {
			Name string `json:"name"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return false, fmt.Errorf("parsing agent list: %w", err)
	}
	for _, agent := range resp.Agents {
		if agent.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func lookupLimaInstanceIPv4(ctx context.Context, limactl, name string) (string, error) {
	cmd := exec.CommandContext(ctx, limactl, "shell", name, "--", "bash", "-lc",
		`ip -4 route get 1.1.1.1 | awk '{for (i = 1; i <= NF; i++) if ($i == "src") {print $(i + 1); exit}}'`)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("querying guest IP: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func limaTLSCertsPresent(sharedDir string) bool {
	if sharedDir == "" {
		return false
	}
	cert := filepath.Join(sharedDir, "certs", sandboxCertFile)
	key := filepath.Join(sharedDir, "certs", sandboxCertKeyFile)
	return localFileExists(cert) && localFileExists(key)
}

func localFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ensureManagedAppPath(cmd *cobra.Command, id skyapps.ID) (string, error) {
	status, err := sandboxManagedAppStatus(id)
	if err != nil {
		return "", err
	}
	if status.ActivePath != "" {
		return status.ActivePath, nil
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "Installing %s via sky10 app management...\n", id)
	if _, err := sandboxManagedAppUpgrade(id, nil); err != nil {
		return "", fmt.Errorf("installing %s: %w", id, err)
	}

	status, err = sandboxManagedAppStatus(id)
	if err != nil {
		return "", err
	}
	if status.ActivePath == "" {
		return "", fmt.Errorf("%s installed but no active binary was found", id)
	}
	return status.ActivePath, nil
}

func ensureSandboxCertificate(ctx context.Context, cmd *cobra.Command, sharedDir string) error {
	if limaTLSCertsPresent(sharedDir) {
		return nil
	}

	mkcert, err := ensureManagedAppPath(cmd, skyapps.AppMkcert)
	if err != nil {
		return err
	}

	certsDir := filepath.Join(sharedDir, "certs")
	if err := os.MkdirAll(certsDir, 0o700); err != nil {
		return fmt.Errorf("creating certs directory: %w", err)
	}

	installCmd := exec.CommandContext(ctx, mkcert, "-install")
	installCmd.Stdin = os.Stdin
	installCmd.Stdout = cmd.OutOrStdout()
	installCmd.Stderr = cmd.ErrOrStderr()
	if err := installCmd.Run(); err != nil {
		return fmt.Errorf("initializing mkcert trust store: %w", err)
	}

	certPath := filepath.Join(certsDir, sandboxCertFile)
	keyPath := filepath.Join(certsDir, sandboxCertKeyFile)
	certCmd := exec.CommandContext(ctx, mkcert,
		"-cert-file", certPath,
		"-key-file", keyPath,
		"sb.sky10.local",
		"*.sb.sky10.local",
	)
	certCmd.Stdin = os.Stdin
	certCmd.Stdout = cmd.OutOrStdout()
	certCmd.Stderr = cmd.ErrOrStderr()
	if err := certCmd.Run(); err != nil {
		return fmt.Errorf("generating sandbox TLS certificate: %w", err)
	}
	return nil
}
