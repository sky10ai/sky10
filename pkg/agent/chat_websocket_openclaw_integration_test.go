package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestChatWebSocketOpenClawBridgeEventShape(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	host := startChatWebSocketTestServer(t, ctx, "host-openclaw-stream", false)
	guest := startChatWebSocketTestServer(t, ctx, "guest-openclaw-stream", true)
	if host.port == guest.port {
		t.Fatalf("host and guest ports must differ: both = %d", host.port)
	}

	waitForHTTPHealth(t, host.baseURL)
	waitForHTTPHealth(t, guest.baseURL)

	postRPC(t, guest.baseURL, "agent.register", RegisterParams{
		Name:   "openclaw",
		Skills: []string{"code"},
	}, nil)

	sessionAConn := dialChatSession(t, guest.baseURL, "openclaw", "session-a")
	defer sessionAConn.Close(websocket.StatusNormalClosure, "")
	sessionBConn := dialChatSession(t, guest.baseURL, "openclaw", "session-b")
	defer sessionBConn.Close(websocket.StatusNormalClosure, "")

	readyA := readReadyEvent(t, sessionAConn)
	if readyA.SessionID != "session-a" {
		t.Fatalf("session A ready session_id = %q, want session-a", readyA.SessionID)
	}
	readyB := readReadyEvent(t, sessionBConn)
	if readyB.SessionID != "session-b" {
		t.Fatalf("session B ready session_id = %q, want session-b", readyB.SessionID)
	}

	sendChatTextRequest(t, sessionAConn, "req-openclaw", "hello")

	postRPC(t, guest.baseURL, "agent.send", SendParams{
		To:        guest.registry.DeviceID(),
		DeviceID:  guest.registry.DeviceID(),
		SessionID: "session-a",
		Type:      "delta",
		Content:   json.RawMessage(`{"text":"hel","stream_id":"stream-openclaw","client_request_id":"req-openclaw"}`),
	}, nil)

	deltaOne, deltaOneContent := readHermesStreamEvent(t, sessionAConn, "delta")
	if deltaOne.Payload.MessageType != "delta" {
		t.Fatalf("first delta message_type = %q, want delta", deltaOne.Payload.MessageType)
	}
	if deltaOneContent.Text != "hel" {
		t.Fatalf("first delta text = %q, want hel", deltaOneContent.Text)
	}
	if deltaOneContent.StreamID != "stream-openclaw" {
		t.Fatalf("first delta stream_id = %q, want stream-openclaw", deltaOneContent.StreamID)
	}
	if deltaOneContent.ClientRequestID != "req-openclaw" {
		t.Fatalf("first delta client_request_id = %q, want req-openclaw", deltaOneContent.ClientRequestID)
	}

	postRPC(t, guest.baseURL, "agent.send", SendParams{
		To:        guest.registry.DeviceID(),
		DeviceID:  guest.registry.DeviceID(),
		SessionID: "session-a",
		Type:      "delta",
		Content:   json.RawMessage(`{"text":"lo","stream_id":"stream-openclaw","client_request_id":"req-openclaw"}`),
	}, nil)

	_, deltaTwoContent := readHermesStreamEvent(t, sessionAConn, "delta")
	if deltaTwoContent.Text != "lo" {
		t.Fatalf("second delta text = %q, want lo", deltaTwoContent.Text)
	}
	if deltaTwoContent.StreamID != deltaOneContent.StreamID {
		t.Fatalf("second delta stream_id = %q, want %q", deltaTwoContent.StreamID, deltaOneContent.StreamID)
	}
	if deltaTwoContent.ClientRequestID != deltaOneContent.ClientRequestID {
		t.Fatalf("second delta client_request_id = %q, want %q", deltaTwoContent.ClientRequestID, deltaOneContent.ClientRequestID)
	}

	postRPC(t, guest.baseURL, "agent.send", SendParams{
		To:        guest.registry.DeviceID(),
		DeviceID:  guest.registry.DeviceID(),
		SessionID: "session-a",
		Type:      "text",
		Content:   json.RawMessage(`{"text":"hello","stream_id":"stream-openclaw","client_request_id":"req-openclaw"}`),
	}, nil)

	messageEvent, messageContent := readHermesStreamEvent(t, sessionAConn, "message")
	if messageEvent.Payload.MessageType != "text" {
		t.Fatalf("final message type = %q, want text", messageEvent.Payload.MessageType)
	}
	if messageContent.Text != "hello" {
		t.Fatalf("final message text = %q, want hello", messageContent.Text)
	}
	if messageContent.StreamID != deltaOneContent.StreamID {
		t.Fatalf("final message stream_id = %q, want %q", messageContent.StreamID, deltaOneContent.StreamID)
	}
	if messageContent.ClientRequestID != deltaOneContent.ClientRequestID {
		t.Fatalf("final message client_request_id = %q, want %q", messageContent.ClientRequestID, deltaOneContent.ClientRequestID)
	}

	assertNoStreamEvent(t, sessionBConn, 250*time.Millisecond)
}
