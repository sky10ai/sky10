//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	skyagent "github.com/sky10/sky10/pkg/agent"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
	skysandbox "github.com/sky10/sky10/pkg/sandbox"
)

func TestIntegrationTwoProcessSandboxBridgeAgentJobs(t *testing.T) {
	bin := buildSky10Binary(t)
	base := t.TempDir()

	hostHome := filepath.Join(base, "host")
	guestHome := filepath.Join(base, "guest")
	hostPort := freePort(t)
	guestPort := freePort(t)
	hostBaseURL := fmt.Sprintf("http://127.0.0.1:%d", hostPort)
	guestBaseURL := fmt.Sprintf("http://127.0.0.1:%d", guestPort)

	host := startProcessNode(t, bin, "host", hostHome, "--http-port", strconv.Itoa(hostPort))
	waitForKVReady(t, bin, host.home)
	waitForProcessHTTPHealth(t, hostBaseURL)

	status := waitForLinkStatus(t, bin, host.home, 1)
	bootstrapAddr := status.ListenAddr[0] + "/p2p/" + status.PeerID
	invite := inviteCode(t, runCLI(t, bin, host.home, "invite"))
	runCLI(t, bin, guestHome, "join", "--role", "sandbox", invite)

	guest := startProcessNodeEnv(t, []string{"SKY10_SANDBOX_GUEST=1"}, bin, "guest", guestHome,
		"--http-port", strconv.Itoa(guestPort),
		"--link-bootstrap", bootstrapAddr,
	)
	waitForKVReady(t, bin, guest.home)
	waitForProcessHTTPHealth(t, guestBaseURL)

	guestIdentity := identityInfo(t, guest.home)
	registered := rpcCall[skyagent.RegisterResult](t, guest.home, "agent.register", skyagent.RegisterParams{
		Name:    "sandbox-agent",
		KeyName: "sandbox-agent",
		Tools: []skyagent.AgentToolSpec{{
			Name:        "travel.search",
			Capability:  "travel.search",
			Description: "Search travel providers through sandbox-declared services.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"query": map[string]interface{}{"type": "string"}},
			},
		}},
	})
	if registered.AgentID == "" {
		t.Fatal("guest agent registration returned empty agent id")
	}

	host.cancel()
	_ = host.cmd.Wait()

	now := time.Now().UTC().Format(time.RFC3339)
	writeSandboxBridgeState(t, hostHome, skysandbox.Record{
		Name:          "sandbox-agent",
		Slug:          "sandbox-agent",
		Provider:      "declared",
		Template:      "hermes",
		Status:        "ready",
		VMStatus:      "Running",
		SharedDir:     filepath.Join(base, "shared", "sandbox-agent"),
		ForwardedHost: "127.0.0.1",
		ForwardedPort: guestPort,
		ForwardedEndpoints: []skysandbox.ForwardedEndpoint{{
			Name:      skysandbox.ForwardedEndpointSky10,
			Host:      "127.0.0.1",
			HostPort:  guestPort,
			GuestHost: "127.0.0.1",
			GuestPort: 9101,
			Protocol:  "tcp",
		}},
		GuestDeviceID: guestIdentity.DeviceID,
		CreatedAt:     now,
		UpdatedAt:     now,
	})

	host = startProcessNode(t, bin, "host-restart", hostHome, "--http-port", strconv.Itoa(hostPort))
	waitForKVReady(t, bin, host.home)
	waitForProcessHTTPHealth(t, hostBaseURL)

	visible := waitForSandboxAgentVisible(t, host.home, registered.AgentID, "sandbox-agent")
	if visible.DeviceID != guestIdentity.DeviceID {
		t.Fatalf("host sees sandbox agent device %q, want guest device %q", visible.DeviceID, guestIdentity.DeviceID)
	}
	waitForGuestAgentJobsBridgeConnected(t, guest.home)

	done := startFakeGuestAgent(t, guestBaseURL, registered.AgentID, "sandbox-agent")
	call := rpcCall[skyagent.AgentCallResultEnvelope](t, host.home, "agent.call", map[string]any{
		"agent":           registered.AgentID,
		"tool":            "travel.search",
		"idempotency_key": "integration-sandbox-bridge-agent-jobs",
		"input": map[string]any{
			"query": "SFO to JFK hotel weekend package",
		},
	})
	if call.Type != skyagent.AgentCallAccepted {
		t.Fatalf("agent.call type = %q, want %q", call.Type, skyagent.AgentCallAccepted)
	}
	if call.JobID == "" {
		t.Fatal("agent.call returned empty job id")
	}
	if call.Delivery == nil {
		t.Fatal("agent.call returned nil delivery metadata")
	}
	if call.Delivery.Scope != skyagent.DeliveryScopeSandbox || call.Delivery.LiveTransport != "sandbox_bridge" || call.Delivery.DurableUsed {
		t.Fatalf("agent.call delivery = %+v, want live sandbox bridge without durable fallback", *call.Delivery)
	}

	completed := waitForAgentJobState(t, host.home, call.JobID, skyagent.JobWorkCompleted)
	if completed.Job.StatusMessage != "guest completed through sandbox bridge" {
		t.Fatalf("completed status message = %q", completed.Job.StatusMessage)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("fake guest agent failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fake guest agent did not observe and complete the host job")
	}
}

type sandboxBridgeStateFile struct {
	Sandboxes []skysandbox.Record `json:"sandboxes"`
}

func writeSandboxBridgeState(t *testing.T, home string, records ...skysandbox.Record) {
	t.Helper()

	sandboxesDir := filepath.Join(home, "sandboxes")
	if err := os.MkdirAll(sandboxesDir, 0o755); err != nil {
		t.Fatalf("mkdir sandboxes dir: %v", err)
	}
	data, err := json.MarshalIndent(sandboxBridgeStateFile{Sandboxes: records}, "", "  ")
	if err != nil {
		t.Fatalf("marshal sandbox state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sandboxesDir, "state.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write sandbox state: %v", err)
	}
}

func waitForProcessHTTPHealth(t *testing.T, baseURL string) {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		res, err := client.Get(strings.TrimRight(baseURL, "/") + "/health")
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("HTTP server at %s did not become healthy", baseURL)
}

func waitForSandboxAgentVisible(t *testing.T, hostHome, agentID, agentName string) skyagent.AgentInfo {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	var last []skyagent.AgentInfo
	for time.Now().Before(deadline) {
		listed := rpcCall[struct {
			Agents []skyagent.AgentInfo `json:"agents"`
		}](t, hostHome, "agent.list", nil)
		last = listed.Agents
		for _, agent := range listed.Agents {
			if agent.ID == agentID || agent.Name == agentName {
				return agent
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("host did not see sandbox agent id=%q name=%q; last=%+v", agentID, agentName, last)
	return skyagent.AgentInfo{}
}

func waitForGuestAgentJobsBridgeConnected(t *testing.T, guestHome string) {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = rpcCallInto(guestHome, "agent.job.updateStatus", skyagent.AgentJobStatusParams{
			JobID:     "j_integration_bridge_probe",
			WorkState: skyagent.JobWorkRunning,
			Message:   "bridge probe",
		}, nil)
		if lastErr != nil && strings.Contains(lastErr.Error(), "not found") {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("guest agent-jobs bridge did not connect; last_err=%v", lastErr)
}

func waitForAgentJobState(t *testing.T, home, jobID, want string) skyagent.AgentJobResult {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	var last skyagent.AgentJobResult
	for time.Now().Before(deadline) {
		last = rpcCall[skyagent.AgentJobResult](t, home, "agent.job.get", skyagent.AgentJobGetParams{JobID: jobID})
		if last.Job.WorkState == want {
			return last
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("agent job %s state = %q, want %q; last=%+v", jobID, last.Job.WorkState, want, last.Job)
	return skyagent.AgentJobResult{}
}

func startFakeGuestAgent(t *testing.T, guestBaseURL, agentID, agentName string) <-chan error {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	t.Cleanup(cancel)

	go func() {
		done <- runFakeGuestAgent(ctx, guestBaseURL, agentID, agentName, ready)
	}()

	select {
	case <-ready:
		return done
	case err := <-done:
		t.Fatalf("fake guest agent exited before subscribing: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("fake guest agent did not subscribe to guest events")
	}
	return done
}

func runFakeGuestAgent(ctx context.Context, guestBaseURL, agentID, agentName string, ready chan<- struct{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(guestBaseURL, "/")+"/rpc/events", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("subscribe guest events: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	close(ready)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var eventName, data string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			handled, err := handleFakeGuestEvent(ctx, guestBaseURL, agentID, agentName, eventName, data)
			if err != nil {
				return err
			}
			if handled {
				return nil
			}
			eventName, data = "", ""
			continue
		}
		if value, ok := strings.CutPrefix(line, "event:"); ok {
			eventName = strings.TrimSpace(value)
			continue
		}
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			if data != "" {
				data += "\n"
			}
			data += strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return ctx.Err()
}

func handleFakeGuestEvent(ctx context.Context, guestBaseURL, agentID, agentName, eventName, data string) (bool, error) {
	if eventName != "agent.message" || strings.TrimSpace(data) == "" {
		return false, nil
	}
	var env struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(data), &env); err != nil {
		return false, fmt.Errorf("decode guest event envelope: %w", err)
	}
	if env.Event != "agent.message" {
		return false, nil
	}
	var msg skyagent.Message
	if err := json.Unmarshal(env.Data, &msg); err != nil {
		return false, fmt.Errorf("decode guest agent message: %w", err)
	}
	if msg.To != agentID && msg.To != agentName {
		return false, nil
	}
	if msg.Type != "tool_call" {
		return false, fmt.Errorf("guest agent received message type %q, want tool_call", msg.Type)
	}

	var call skyagent.AgentToolCallMessage
	if err := json.Unmarshal(msg.Content, &call); err != nil {
		return false, fmt.Errorf("decode tool call: %w", err)
	}
	if call.JobID == "" {
		return false, fmt.Errorf("tool call missing job id")
	}
	if call.Tool != "travel.search" {
		return false, fmt.Errorf("tool call tool = %q, want travel.search", call.Tool)
	}

	if err := httpRPCCall(ctx, guestBaseURL, "agent.job.updateStatus", skyagent.AgentJobStatusParams{
		JobID:     call.JobID,
		WorkState: skyagent.JobWorkRunning,
		Message:   "guest running through sandbox bridge",
	}, nil); err != nil {
		return false, fmt.Errorf("guest update status: %w", err)
	}
	output := json.RawMessage(`{"summary":"sandbox bridge completed host-owned job"}`)
	if err := httpRPCCall(ctx, guestBaseURL, "agent.job.complete", skyagent.AgentJobCompleteParams{
		JobID:   call.JobID,
		Output:  output,
		Message: "guest completed through sandbox bridge",
	}, nil); err != nil {
		return false, fmt.Errorf("guest complete job: %w", err)
	}
	return true, nil
}

func httpRPCCall(ctx context.Context, baseURL, method string, params any, out any) error {
	var rawParams json.RawMessage
	if params != nil {
		body, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal %s params: %w", method, err)
		}
		rawParams = body
	}
	body, err := json.Marshal(skyrpc.Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  rawParams,
		ID:      1,
	})
	if err != nil {
		return fmt.Errorf("marshal %s request: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/rpc", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("post %s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s HTTP %d: %s", method, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var rpcResp struct {
		Result json.RawMessage `json:"result,omitempty"`
		Error  *skyrpc.Error   `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return fmt.Errorf("decode %s response: %w", method, err)
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("%s", rpcResp.Error.Message)
	}
	if out != nil && len(rpcResp.Result) > 0 {
		if err := json.Unmarshal(rpcResp.Result, out); err != nil {
			return fmt.Errorf("unmarshal %s result: %w", method, err)
		}
	}
	return nil
}
