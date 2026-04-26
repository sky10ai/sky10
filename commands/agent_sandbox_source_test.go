package commands

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	skyagent "github.com/sky10/sky10/pkg/agent"
	skysandbox "github.com/sky10/sky10/pkg/sandbox"
)

type fakeSandboxAgentLister struct {
	result *skysandbox.ListResult
	err    error
}

func (f fakeSandboxAgentLister) List(context.Context) (*skysandbox.ListResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func TestSandboxAgentSourceListsAgentsFromForwardedSky10Endpoint(t *testing.T) {
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rpc" {
			t.Fatalf("path = %q, want /rpc", r.URL.Path)
		}
		var req struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotMethod = req.Method
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]interface{}{
				"agents": []skyagent.AgentInfo{{
					ID:       "A-clawdock",
					Name:     "clawdock",
					DeviceID: "D-guest",
					Skills:   []string{"code"},
				}, {
					ID:       "A-remote",
					Name:     "remote",
					DeviceID: "D-other",
					Skills:   []string{"code"},
				}},
			},
		})
	}))
	defer srv.Close()

	host, port := hostPortFromTestURL(t, srv.URL)
	source := newSandboxAgentSource(fakeSandboxAgentLister{
		result: &skysandbox.ListResult{Sandboxes: []skysandbox.Record{{
			Name:          "clawdock",
			Slug:          "clawdock",
			Status:        "ready",
			VMStatus:      "Running",
			GuestDeviceID: "D-guest",
			ForwardedEndpoints: []skysandbox.ForwardedEndpoint{{
				Name:     skysandbox.ForwardedEndpointSky10,
				Host:     host,
				HostPort: port,
			}},
		}}},
	}, nil)

	agents := source.ListAgents(context.Background())
	if gotMethod != "agent.list" {
		t.Fatalf("method = %q, want agent.list", gotMethod)
	}
	if len(agents) != 1 {
		t.Fatalf("agents length = %d, want 1", len(agents))
	}
	if agents[0].Name != "clawdock" || agents[0].DeviceID != "D-guest" {
		t.Fatalf("agent = %#v, want clawdock on D-guest", agents[0])
	}
	if agents[0].DeviceName != "lima-clawdock" {
		t.Fatalf("device name = %q, want lima-clawdock", agents[0].DeviceName)
	}

	target, ok := source.Resolve(context.Background(), "A-clawdock")
	if !ok {
		t.Fatal("Resolve(A-clawdock) = false, want true")
	}
	if target.BaseURL != srv.URL {
		t.Fatalf("target base URL = %q, want %q", target.BaseURL, srv.URL)
	}
}

func TestSandboxAgentSourceListsManifestToolsBeforeGuestAgentIsReady(t *testing.T) {
	source := newSandboxAgentSource(fakeSandboxAgentLister{
		result: &skysandbox.ListResult{Sandboxes: []skysandbox.Record{{
			Name:     "media-accent-agent",
			Slug:     "media-accent-agent",
			Status:   "creating",
			VMStatus: "",
			Files: []skysandbox.SharedFile{{
				Path: "agent-manifest.json",
				Content: `{
  "id": "aspec_media",
  "name": "media-accent-agent",
  "tools": [{
    "name": "media.convert",
    "capability": "media.convert",
    "description": "Convert media accent.",
    "audience": "private",
    "scope": "current",
    "input_schema": {"type":"object"},
    "output_schema": {"type":"object"},
    "availability": {"status":"available"},
    "fulfillment": {"mode":"autonomous"},
    "pricing": {"model":"free"},
    "supports_cancel": true,
    "supports_streaming": true
  }]
}`,
			}},
		}}},
	}, nil)

	agents := source.ListAgents(context.Background())
	if len(agents) != 1 {
		t.Fatalf("agents length = %d, want 1", len(agents))
	}
	if agents[0].Name != "media-accent-agent" {
		t.Fatalf("agent name = %q, want media-accent-agent", agents[0].Name)
	}
	if len(agents[0].Tools) != 1 || agents[0].Tools[0].Name != "media.convert" {
		t.Fatalf("tools = %#v, want media accent tool", agents[0].Tools)
	}
	if len(agents[0].Skills) != 1 || agents[0].Skills[0] != "media.convert" {
		t.Fatalf("skills = %#v, want capability compatibility from manifest tool", agents[0].Skills)
	}
	if _, ok := source.Resolve(context.Background(), "media-accent-agent"); ok {
		t.Fatal("Resolve(media-accent-agent) = true before guest endpoint is reachable, want false")
	}
}

func TestSandboxSky10BaseURLFallsBackToForwardedPort(t *testing.T) {
	baseURL, ok := sandboxSky10BaseURL(skysandbox.Record{
		ForwardedHost: "127.0.0.1",
		ForwardedPort: 39101,
	})
	if !ok {
		t.Fatal("sandboxSky10BaseURL ok = false, want true")
	}
	if baseURL != "http://127.0.0.1:39101" {
		t.Fatalf("base URL = %q, want http://127.0.0.1:39101", baseURL)
	}
}

func hostPortFromTestURL(t *testing.T, raw string) (string, int) {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	host, portRaw, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return host, port
}
