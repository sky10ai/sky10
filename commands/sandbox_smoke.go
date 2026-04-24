package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	skyagent "github.com/sky10/sky10/pkg/agent"
	skysandbox "github.com/sky10/sky10/pkg/sandbox"
	"github.com/spf13/cobra"
)

type sandboxEndpointProbe struct {
	Name       string
	URL        string
	OK         bool
	StatusCode int
	Duration   time.Duration
	Error      string
}

type sandboxSmokeHealth struct {
	Agent     skyagent.AgentInfo
	Sandbox   skysandbox.Record
	Endpoints []sandboxEndpointProbe
}

func sandboxSmokeCmd() *cobra.Command {
	var baseURL string
	var message string
	var timeout time.Duration
	var readyTimeout time.Duration
	var healthTimeout time.Duration
	var concurrency int
	var skipHealth bool

	cmd := &cobra.Command{
		Use:   "smoke [agent-name-or-id...]",
		Short: "Smoke test sandbox agent chat websockets",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			resolvedBaseURL := strings.TrimSpace(baseURL)
			if resolvedBaseURL == "" {
				var err error
				resolvedBaseURL, err = daemonHTTPBaseURL()
				if err != nil {
					return err
				}
			}

			agents, err := listAgentsViaDaemon()
			if err != nil {
				return err
			}
			selected, err := selectSmokeAgents(agents, args)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Daemon HTTP: %s\n", resolvedBaseURL)
			fmt.Fprintf(cmd.OutOrStdout(), "Agents: %d\n", len(selected))

			if !skipHealth {
				health, err := sandboxSmokeHealthForAgents(ctx, selected, healthTimeout)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: sandbox endpoint health unavailable: %v\n", err)
				} else {
					printSandboxSmokeHealth(cmd.OutOrStdout(), health)
				}
			}

			report, err := skyagent.RunChatSmoke(ctx, skyagent.SmokeOptions{
				BaseURL:      resolvedBaseURL,
				Agents:       selected,
				Message:      message,
				Timeout:      timeout,
				ReadyTimeout: readyTimeout,
				Concurrency:  concurrency,
			})
			if err != nil {
				return err
			}
			printAgentSmokeReport(cmd.OutOrStdout(), report)
			if !report.OK() {
				return fmt.Errorf("agent smoke failed: %d/%d failed", report.FailureCount(), len(report.Results))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&baseURL, "base-url", "", "Daemon HTTP base URL (defaults to skyfs.health http_addr)")
	cmd.Flags().StringVar(&message, "message", skyagent.DefaultSmokeMessage, "Chat message sent to each agent")
	cmd.Flags().DurationVar(&timeout, "timeout", skyagent.DefaultSmokeTimeout, "Per-agent response timeout")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", skyagent.DefaultSmokeReadyTimeout, "Per-agent websocket ready timeout")
	cmd.Flags().DurationVar(&healthTimeout, "health-timeout", 2*time.Second, "Per-endpoint sandbox health probe timeout")
	cmd.Flags().IntVar(&concurrency, "concurrency", skyagent.DefaultSmokeConcurrency, "Maximum agents to smoke test in parallel")
	cmd.Flags().BoolVar(&skipHealth, "skip-health", false, "Skip sandbox forwarded endpoint health probes")
	return cmd
}

func daemonHTTPBaseURL() (string, error) {
	raw, err := rpcCall("skyfs.health", nil)
	if err != nil {
		return "", err
	}
	var health struct {
		HTTPAddr string `json:"http_addr"`
	}
	if err := json.Unmarshal(raw, &health); err != nil {
		return "", fmt.Errorf("parsing health response: %w", err)
	}
	if strings.TrimSpace(health.HTTPAddr) == "" {
		return "", fmt.Errorf("daemon HTTP server is not running (start with 'sky10 serve')")
	}
	baseURL, err := loopbackHTTPURL(health.HTTPAddr)
	if err != nil {
		return "", err
	}
	return baseURL, nil
}

func listAgentsViaDaemon() ([]skyagent.AgentInfo, error) {
	raw, err := rpcCall("agent.list", nil)
	if err != nil {
		return nil, err
	}
	var listed struct {
		Agents []skyagent.AgentInfo `json:"agents"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, fmt.Errorf("parsing agent.list response: %w", err)
	}
	return listed.Agents, nil
}

func selectSmokeAgents(agents []skyagent.AgentInfo, filters []string) ([]skyagent.AgentInfo, error) {
	if len(filters) == 0 {
		if len(agents) == 0 {
			return nil, fmt.Errorf("no registered agents")
		}
		return agents, nil
	}

	selected := make([]skyagent.AgentInfo, 0, len(filters))
	for _, filter := range filters {
		filter = strings.TrimSpace(filter)
		if filter == "" {
			continue
		}
		var matched *skyagent.AgentInfo
		for i := range agents {
			if agents[i].ID == filter || agents[i].Name == filter {
				cp := agents[i]
				matched = &cp
				break
			}
		}
		if matched == nil {
			return nil, fmt.Errorf("agent %q not found", filter)
		}
		selected = append(selected, *matched)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no agents selected")
	}
	return selected, nil
}

func sandboxSmokeHealthForAgents(ctx context.Context, agents []skyagent.AgentInfo, timeout time.Duration) ([]sandboxSmokeHealth, error) {
	raw, err := rpcCall("sandbox.list", nil)
	if err != nil {
		return nil, err
	}
	var listed skysandbox.ListResult
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, fmt.Errorf("parsing sandbox.list response: %w", err)
	}

	sandboxByGuestDevice := make(map[string]skysandbox.Record, len(listed.Sandboxes))
	for _, rec := range listed.Sandboxes {
		if strings.TrimSpace(rec.GuestDeviceID) != "" {
			sandboxByGuestDevice[rec.GuestDeviceID] = rec
		}
	}

	results := make([]sandboxSmokeHealth, 0, len(agents))
	for _, agent := range agents {
		rec, ok := sandboxByGuestDevice[agent.DeviceID]
		if !ok {
			continue
		}
		results = append(results, sandboxSmokeHealth{
			Agent:     agent,
			Sandbox:   rec,
			Endpoints: probeSandboxEndpoints(ctx, rec, timeout),
		})
	}
	return results, nil
}

func probeSandboxEndpoints(ctx context.Context, rec skysandbox.Record, timeout time.Duration) []sandboxEndpointProbe {
	endpoints := rec.ForwardedEndpoints
	if len(endpoints) == 0 && rec.ForwardedPort > 0 {
		endpoints = []skysandbox.ForwardedEndpoint{{
			Name:     skysandbox.ForwardedEndpointSky10,
			Host:     rec.ForwardedHost,
			HostPort: rec.ForwardedPort,
		}}
	}
	probes := make([]sandboxEndpointProbe, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if endpoint.HostPort <= 0 {
			continue
		}
		host := strings.TrimSpace(endpoint.Host)
		if host == "" {
			host = "127.0.0.1"
		}
		probes = append(probes, probeSandboxEndpoint(ctx, endpoint.Name, host, endpoint.HostPort, timeout))
	}
	return probes
}

func probeSandboxEndpoint(ctx context.Context, name, host string, port int, timeout time.Duration) sandboxEndpointProbe {
	probe := sandboxEndpointProbe{
		Name: name,
		URL:  "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/health",
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	client := &http.Client{Timeout: timeout}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, probe.URL, nil)
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	start := time.Now()
	resp, err := client.Do(req)
	probe.Duration = time.Since(start)
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	probe.StatusCode = resp.StatusCode
	probe.OK = resp.StatusCode >= 200 && resp.StatusCode < 300
	if !probe.OK {
		probe.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return probe
}

func printSandboxSmokeHealth(out io.Writer, health []sandboxSmokeHealth) {
	if len(health) == 0 {
		fmt.Fprintln(out, "Sandbox endpoints: none matched registered agent devices")
		return
	}
	fmt.Fprintln(out, "Sandbox endpoints:")
	for _, item := range health {
		fmt.Fprintf(out, "  %s -> %s status=%s vm=%s guest=%s\n",
			agentSmokeLabel(item.Agent),
			item.Sandbox.Slug,
			emptyDash(item.Sandbox.Status),
			emptyDash(item.Sandbox.VMStatus),
			emptyDash(item.Sandbox.GuestDeviceID),
		)
		for _, endpoint := range item.Endpoints {
			status := "ok"
			if !endpoint.OK {
				status = "fail"
			}
			fmt.Fprintf(out, "    %s %s %s %s",
				emptyDash(endpoint.Name),
				status,
				formatSmokeDuration(endpoint.Duration),
				endpoint.URL,
			)
			if endpoint.Error != "" {
				fmt.Fprintf(out, " error=%s", endpoint.Error)
			}
			fmt.Fprintln(out)
		}
	}
}

func printAgentSmokeReport(out io.Writer, report skyagent.SmokeReport) {
	fmt.Fprintln(out, "Agent chat smoke:")
	for _, result := range report.Results {
		if result.OK {
			fmt.Fprintf(out, "  %s ok ready=%s ack=%s first=%s(%s) final=%s(%s)",
				agentSmokeLabel(result.Agent),
				formatSmokeDuration(result.ReadyLatency),
				formatSmokeDuration(result.AckLatency),
				emptyDash(result.FirstEvent),
				formatSmokeDuration(result.FirstEventLatency),
				emptyDash(result.FinalEvent),
				formatSmokeDuration(result.FinalEventLatency),
			)
			if result.ResponseSnippet != "" {
				fmt.Fprintf(out, " reply=%q", result.ResponseSnippet)
			}
			fmt.Fprintln(out)
			continue
		}

		fmt.Fprintf(out, "  %s fail stage=%s elapsed=%s",
			agentSmokeLabel(result.Agent),
			emptyDash(result.Stage),
			formatSmokeDuration(result.Elapsed),
		)
		if result.Error != "" {
			fmt.Fprintf(out, " error=%s", result.Error)
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintf(out, "Summary: %d/%d ok in %s\n",
		len(report.Results)-report.FailureCount(),
		len(report.Results),
		formatSmokeDuration(report.Duration),
	)
}

func agentSmokeLabel(agent skyagent.AgentInfo) string {
	name := strings.TrimSpace(agent.Name)
	if name == "" {
		name = strings.TrimSpace(agent.ID)
	}
	device := strings.TrimSpace(agent.DeviceID)
	if device == "" {
		return name
	}
	return fmt.Sprintf("%s [%s]", name, device)
}

func formatSmokeDuration(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	if d < time.Millisecond {
		return "<1ms"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Round(time.Millisecond).Milliseconds())
	}
	return d.Round(100 * time.Millisecond).String()
}

func emptyDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return strings.TrimSpace(v)
}
