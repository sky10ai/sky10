package agentjobs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	skyagent "github.com/sky10/sky10/pkg/agent"
	skysandbox "github.com/sky10/sky10/pkg/sandbox"
	"github.com/sky10/sky10/pkg/sandbox/bridge"
)

type fakeHostBackend struct {
	agentRef string
	update   skyagent.AgentJobStatusParams
}

func (f *fakeHostBackend) UpdateStatus(_ context.Context, agentRef string, params skyagent.AgentJobStatusParams) (*skyagent.AgentJobResult, error) {
	f.agentRef = agentRef
	f.update = params
	return &skyagent.AgentJobResult{Job: skyagent.AgentJob{
		JobID:     params.JobID,
		AgentName: agentRef,
		WorkState: params.WorkState,
	}}, nil
}

func (f *fakeHostBackend) Complete(context.Context, string, skyagent.AgentJobCompleteParams) (*skyagent.AgentJobResult, error) {
	return nil, nil
}

func (f *fakeHostBackend) Fail(context.Context, string, skyagent.AgentJobFailParams) (*skyagent.AgentJobResult, error) {
	return nil, nil
}

func TestForwardingBackendSendsJobUpdatesOverHostBridge(t *testing.T) {
	forwarder := NewForwardingBackend()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != EndpointPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, EndpointPath)
		}
		HandlerWithHostBridge(forwarder)(w, r)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostBackend := &fakeHostBackend{}
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + EndpointPath + "?" + BridgeRoleQuery + "=" + BridgeRoleHost
	hostConn, resp, err := bridge.Dial(ctx, wsURL, NewBridgeHandler(hostBackend, "custom-agent"))
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("host bridge dial: %v", err)
	}
	defer hostConn.Close(websocket.StatusNormalClosure, "")
	go func() { _ = hostConn.Run(ctx) }()

	waitForForwarderConnected(t, ctx, forwarder)
	result, err := forwarder.UpdateStatus(ctx, skyagent.AgentJobStatusParams{
		JobID:     "j_sandbox",
		WorkState: skyagent.JobWorkRunning,
		Message:   "Running agent.run",
	})
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if result.Job.JobID != "j_sandbox" || result.Job.WorkState != skyagent.JobWorkRunning {
		t.Fatalf("result = %#v, want running job", result.Job)
	}
	if hostBackend.agentRef != "custom-agent" || hostBackend.update.JobID != "j_sandbox" {
		t.Fatalf("host backend = ref %q update %#v, want custom-agent j_sandbox", hostBackend.agentRef, hostBackend.update)
	}
}

func TestBridgeURLUsesForwardedSky10Endpoint(t *testing.T) {
	got, err := BridgeURL(skysandbox.Record{
		ForwardedEndpoints: []skysandbox.ForwardedEndpoint{{
			Name:     skysandbox.ForwardedEndpointSky10,
			Host:     "127.0.0.1",
			HostPort: 39107,
		}},
	})
	if err != nil {
		t.Fatalf("BridgeURL: %v", err)
	}
	want := "ws://127.0.0.1:39107" + EndpointPath + "?" + BridgeRoleQuery + "=" + BridgeRoleHost
	if got != want {
		t.Fatalf("BridgeURL = %q, want %q", got, want)
	}
}

func waitForForwarderConnected(t *testing.T, ctx context.Context, forwarder *ForwardingBackend) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if forwarder.Connected() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for host bridge attachment")
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context done waiting for host bridge attachment: %v", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
