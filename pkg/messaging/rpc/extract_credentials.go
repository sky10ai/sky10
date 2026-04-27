package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	agentslack "github.com/sky10/sky10/external/agent-slack"
	"github.com/sky10/sky10/pkg/messaging"
	messagingbroker "github.com/sky10/sky10/pkg/messaging/broker"
	messagingexternal "github.com/sky10/sky10/pkg/messaging/external"
	skysecrets "github.com/sky10/sky10/pkg/secrets"
)

const (
	extractActionPrefix     = "import-"
	extractActionTimeout    = 60 * time.Second
	extractDumpTimeout      = 15 * time.Second
	slackSessionTokenField  = "slack_session_token"
	slackSessionCookieField = "slack_session_cookie"
)

// agentSlackCredentials mirrors the JSON shape printed by our vendored
// dump-credentials.js helper, which forwards agent-slack's loadCredentials()
// output (with macOS Keychain values resolved).
type agentSlackCredentials struct {
	Version             int                   `json:"version"`
	UpdatedAt           string                `json:"updated_at,omitempty"`
	DefaultWorkspaceURL string                `json:"default_workspace_url,omitempty"`
	Workspaces          []agentSlackWorkspace `json:"workspaces"`
}

type agentSlackWorkspace struct {
	WorkspaceURL  string                  `json:"workspace_url"`
	WorkspaceName string                  `json:"workspace_name,omitempty"`
	TeamID        string                  `json:"team_id,omitempty"`
	TeamDomain    string                  `json:"team_domain,omitempty"`
	Auth          agentSlackWorkspaceAuth `json:"auth"`
}

type agentSlackWorkspaceAuth struct {
	AuthType   string `json:"auth_type"`
	Token      string `json:"token,omitempty"`
	XoxcToken  string `json:"xoxc_token,omitempty"`
	XoxdCookie string `json:"xoxd_cookie,omitempty"`
}

// runExtractCredentials handles the extract_credentials action by invoking
// the vendored agent-slack bundle, asking it to import a session from one of
// its supported sources, then reading the hydrated credentials back through
// our companion dump bundle and storing them as the connection's secret.
func (h *Handler) runExtractCredentials(
	ctx context.Context,
	info messagingexternal.AdapterInfo,
	action messagingexternal.Action,
	p runAdapterActionParams,
) (runAdapterActionResult, error) {
	if info.Adapter.ID != "slack" {
		return runAdapterActionResult{}, fmt.Errorf("extract_credentials is only wired for the slack adapter (got %q)", info.Adapter.ID)
	}
	source := strings.TrimPrefix(action.ID, extractActionPrefix)
	if source == action.ID || strings.TrimSpace(source) == "" {
		return runAdapterActionResult{}, fmt.Errorf("extract action %q must use id %q where <source> is desktop|chrome|firefox|brave", action.ID, extractActionPrefix+"<source>")
	}
	if h.bunPath == nil {
		return runAdapterActionResult{}, fmt.Errorf("managed bun path is not configured")
	}
	bun := strings.TrimSpace(h.bunPath())
	if bun == "" {
		return runAdapterActionResult{}, fmt.Errorf("managed bun is not installed; install via `sky10 apps install bun`")
	}
	if strings.TrimSpace(h.helperRootDir) == "" {
		return runAdapterActionResult{}, fmt.Errorf("messaging helper root dir is not configured")
	}
	if h.secretWriter == nil {
		return runAdapterActionResult{}, fmt.Errorf("messaging secret writer is not configured")
	}
	if strings.TrimSpace(string(p.ConnectionID)) == "" {
		return runAdapterActionResult{}, fmt.Errorf("connection_id is required")
	}

	bundles, err := agentslack.Materialize(h.helperRootDir + "/agent-slack")
	if err != nil {
		return runAdapterActionResult{}, fmt.Errorf("materialize agent-slack: %w", err)
	}

	if err := runBunCommand(ctx, bun, bundles.CLI, []string{"auth", "import-" + source}, extractActionTimeout); err != nil {
		return runAdapterActionResult{}, fmt.Errorf("agent-slack import-%s: %w", source, err)
	}

	creds, err := dumpAgentSlackCredentials(ctx, bun, bundles.Dump)
	if err != nil {
		return runAdapterActionResult{}, fmt.Errorf("read agent-slack credentials: %w", err)
	}

	workspace, err := pickBrowserWorkspace(creds)
	if err != nil {
		return runAdapterActionResult{}, err
	}

	connection := messaging.Connection{
		ID:        p.ConnectionID,
		AdapterID: info.Adapter.ID,
		Label:     strings.TrimSpace(p.Label),
		Status:    messaging.ConnectionStatusAuthRequired,
		Metadata:  make(map[string]string),
	}
	if existing, ok := h.store.GetConnection(p.ConnectionID); ok {
		if existing.AdapterID != info.Adapter.ID {
			return runAdapterActionResult{}, fmt.Errorf("connection %q already uses adapter %q", p.ConnectionID, existing.AdapterID)
		}
		connection = cloneRPCConnection(existing)
		if connection.Metadata == nil {
			connection.Metadata = make(map[string]string)
		}
	}
	if strings.TrimSpace(connection.Label) == "" {
		connection.Label = strings.TrimSpace(workspace.WorkspaceName)
	}
	if strings.TrimSpace(connection.Label) == "" {
		connection.Label = info.Adapter.DisplayName
	}
	if workspace.TeamID != "" {
		connection.Metadata["slack_team_id"] = workspace.TeamID
	}
	if workspace.WorkspaceURL != "" {
		connection.Metadata["slack_workspace_url"] = workspace.WorkspaceURL
	}
	if workspace.WorkspaceName != "" && connection.Metadata["slack_workspace_name"] == "" {
		connection.Metadata["slack_workspace_name"] = workspace.WorkspaceName
	}

	credentialValues := map[string]string{
		slackSessionTokenField:  workspace.Auth.XoxcToken,
		slackSessionCookieField: workspace.Auth.XoxdCookie,
	}
	scope := strings.TrimSpace(p.SecretScope)
	if scope == "" {
		scope = skysecrets.ScopeCurrent
	}
	ref, err := h.storeAdapterCredential(ctx, info.Adapter.ID, connection.ID, credentialValues, scope)
	if err != nil {
		return runAdapterActionResult{}, err
	}
	connection.Auth.Method = messaging.AuthMethodSession
	connection.Auth.CredentialRef = ref
	connection.Auth.ExternalAccount = workspace.WorkspaceURL
	connection.Status = messaging.ConnectionStatusConnecting

	if err := connection.Validate(); err != nil {
		return runAdapterActionResult{}, err
	}

	if h.processResolver == nil {
		return runAdapterActionResult{}, fmt.Errorf("messaging process resolver is not configured")
	}
	process, err := h.processResolver(string(connection.AdapterID))
	if err != nil {
		return runAdapterActionResult{}, err
	}
	if err := h.broker.UpsertConnection(ctx, messagingbroker.RegisterConnectionParams{
		Connection: connection,
		Process:    process,
	}); err != nil {
		return runAdapterActionResult{}, err
	}

	result := runAdapterActionResult{
		ActionID:      action.ID,
		ActionKind:    action.Kind,
		Connection:    connection,
		CredentialRef: ref,
	}

	connectResult, err := h.broker.ConnectConnection(ctx, connection.ID)
	if err != nil {
		// The connection is persisted; surface the validation error so the
		// user can re-paste or re-extract without losing the import.
		if stored, ok := h.store.GetConnection(connection.ID); ok {
			result.Connection = stored
		}
		return result, fmt.Errorf("validate extracted credentials: %w", err)
	}
	result.Connection = connectResult.Connection
	result.Connect = &connectResult
	return result, nil
}

func runBunCommand(ctx context.Context, bun, script string, args []string, timeout time.Duration) error {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bun, append([]string{script}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		stderrText := strings.TrimSpace(stderr.String())
		if stderrText == "" {
			stderrText = strings.TrimSpace(stdout.String())
		}
		if stderrText == "" {
			return err
		}
		return fmt.Errorf("%s: %w", stderrText, err)
	}
	return nil
}

func dumpAgentSlackCredentials(ctx context.Context, bun, script string) (agentSlackCredentials, error) {
	cctx, cancel := context.WithTimeout(ctx, extractDumpTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bun, script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		stderrText := strings.TrimSpace(stderr.String())
		if stderrText == "" {
			return agentSlackCredentials{}, err
		}
		return agentSlackCredentials{}, fmt.Errorf("%s: %w", stderrText, err)
	}
	var creds agentSlackCredentials
	if err := json.Unmarshal(stdout.Bytes(), &creds); err != nil {
		return agentSlackCredentials{}, fmt.Errorf("decode dump output: %w", err)
	}
	return creds, nil
}

func pickBrowserWorkspace(creds agentSlackCredentials) (agentSlackWorkspace, error) {
	if len(creds.Workspaces) == 0 {
		return agentSlackWorkspace{}, fmt.Errorf("agent-slack found no workspaces; sign in to Slack in the chosen browser/desktop and try again")
	}
	defaults := strings.TrimSpace(creds.DefaultWorkspaceURL)
	for _, workspace := range creds.Workspaces {
		if defaults == "" || workspace.WorkspaceURL != defaults {
			continue
		}
		if workspace.Auth.AuthType == "browser" && workspace.Auth.XoxcToken != "" && workspace.Auth.XoxdCookie != "" {
			return workspace, nil
		}
	}
	for _, workspace := range creds.Workspaces {
		if workspace.Auth.AuthType == "browser" && workspace.Auth.XoxcToken != "" && workspace.Auth.XoxdCookie != "" {
			return workspace, nil
		}
	}
	return agentSlackWorkspace{}, fmt.Errorf("agent-slack returned no browser-session workspaces (xoxc/xoxd); use the manual paste fields instead")
}
