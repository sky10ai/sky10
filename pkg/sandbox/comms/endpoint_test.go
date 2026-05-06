package comms

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func staticResolver(agentID, deviceID string) IdentityResolver {
	return func(*http.Request) (string, string, error) {
		return agentID, deviceID, nil
	}
}

func TestNewEndpointPanicsOnEmptyName(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for empty name")
		}
	}()
	NewEndpoint("", staticResolver("A-1", "D-1"))
}

func TestNewEndpointPanicsOnNilResolver(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil resolver")
		}
	}()
	NewEndpoint("test", nil)
}

func TestRegisterDuplicatePanics(t *testing.T) {
	t.Parallel()
	e := NewEndpoint("test", staticResolver("A-1", "D-1"))
	e.Register(validSpec())
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for duplicate Register")
		}
		if !strings.Contains(asString(r), "duplicate") {
			t.Fatalf("panic should mention duplicate, got %v", r)
		}
	}()
	e.Register(validSpec())
}

func TestRegisterAfterStartPanics(t *testing.T) {
	t.Parallel()
	e := NewEndpoint("test", staticResolver("A-1", "D-1"))
	e.Register(validSpec())
	_ = e.Handler() // freezes registry
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for Register after Handler")
		}
		if !strings.Contains(asString(r), "started serving") {
			t.Fatalf("panic should mention started, got %v", r)
		}
	}()
	other := validSpec()
	other.Name = "test.other"
	e.Register(other)
}

// integrationEndpoint stands up a minimal httptest server hosting one
// Endpoint with the given specs and resolver, returns the websocket
// URL and a cleanup func.
func integrationEndpoint(t *testing.T, specs []TypeSpec, resolver IdentityResolver, opts ...Option) (string, func()) {
	t.Helper()
	if resolver == nil {
		resolver = staticResolver("A-1", "D-1")
	}
	e := NewEndpoint("test", resolver, opts...)
	for _, s := range specs {
		e.Register(s)
	}
	srv := httptest.NewServer(e.Handler())
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	return url, srv.Close
}

func dialClient(t *testing.T, ctx context.Context, url string) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{})
	if err != nil {
		t.Fatalf("dial err = %v", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	return conn
}

func TestEndpointRoundTripIdentityIsBusStamped(t *testing.T) {
	t.Parallel()
	type echoPayload struct {
		Saw string `json:"saw"`
	}
	spec := validSpec()
	spec.Handler = func(_ context.Context, env Envelope) (json.RawMessage, error) {
		// The handler reads identity from the bus-stamped Envelope
		// fields, never from the payload. The payload-supplied
		// "agent_id":"A-impostor" must NOT appear here.
		out, _ := json.Marshal(echoPayload{Saw: env.AgentID})
		return out, nil
	}
	url, closeFn := integrationEndpoint(t, []TypeSpec{spec}, staticResolver("A-trusted", "D-1"))
	defer closeFn()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := dialClient(t, ctx, url)
	defer c.Close(websocket.StatusNormalClosure, "")

	// Wire envelope claims to be from a different agent. Plumbing must
	// drop that and stamp the resolver's value.
	if err := wsjson.Write(ctx, c, map[string]any{
		"type":       "test.echo",
		"agent_id":   "A-impostor",
		"device_id":  "D-impostor",
		"request_id": "r1",
		"nonce":      "n1",
		"payload":    map[string]string{"hello": "world"},
	}); err != nil {
		t.Fatal(err)
	}

	var resp responseEnvelope
	if err := wsjson.Read(ctx, c, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.RequestID != "r1" {
		t.Fatalf("response RequestID = %q, want r1", resp.RequestID)
	}
	var saw echoPayload
	if err := json.Unmarshal(resp.Payload, &saw); err != nil {
		t.Fatalf("unmarshal saw payload: %v", err)
	}
	if saw.Saw != "A-trusted" {
		t.Fatalf("handler saw agent_id %q, want A-trusted; payload-supplied identity must be dropped", saw.Saw)
	}
}

func TestEndpointTypeUnregistered(t *testing.T) {
	t.Parallel()
	url, closeFn := integrationEndpoint(t, []TypeSpec{validSpec()}, nil)
	defer closeFn()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := dialClient(t, ctx, url)
	defer c.Close(websocket.StatusNormalClosure, "")

	if err := wsjson.Write(ctx, c, map[string]any{
		"type":       "test.unknown",
		"request_id": "r1",
		"nonce":      "n1",
	}); err != nil {
		t.Fatal(err)
	}
	var resp responseEnvelope
	if err := wsjson.Read(ctx, c, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodeTypeUnregistered {
		t.Fatalf("response = %+v, want type_unregistered error", resp)
	}
}

func TestEndpointPayloadTooLarge(t *testing.T) {
	t.Parallel()
	spec := validSpec()
	spec.MaxPayloadSize = 16
	url, closeFn := integrationEndpoint(t, []TypeSpec{spec}, nil)
	defer closeFn()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := dialClient(t, ctx, url)
	defer c.Close(websocket.StatusNormalClosure, "")

	bigPayload := strings.Repeat("x", 64)
	if err := wsjson.Write(ctx, c, map[string]any{
		"type":       "test.echo",
		"request_id": "r1",
		"nonce":      "n1",
		"payload":    bigPayload,
	}); err != nil {
		t.Fatal(err)
	}
	var resp responseEnvelope
	if err := wsjson.Read(ctx, c, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodePayloadTooLarge {
		t.Fatalf("response = %+v, want payload_too_large", resp)
	}
}

func TestEndpointReplayRejected(t *testing.T) {
	t.Parallel()
	url, closeFn := integrationEndpoint(t, []TypeSpec{validSpec()}, nil)
	defer closeFn()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := dialClient(t, ctx, url)
	defer c.Close(websocket.StatusNormalClosure, "")

	send := func(requestID string) {
		if err := wsjson.Write(ctx, c, map[string]any{
			"type":       "test.echo",
			"request_id": requestID,
			"nonce":      "samenonce",
			"payload":    map[string]string{},
		}); err != nil {
			t.Fatal(err)
		}
	}
	send("r1")
	var ok responseEnvelope
	if err := wsjson.Read(ctx, c, &ok); err != nil {
		t.Fatal(err)
	}
	if ok.Error != nil {
		t.Fatalf("first send error: %+v", ok.Error)
	}
	send("r2")
	var replayed responseEnvelope
	if err := wsjson.Read(ctx, c, &replayed); err != nil {
		t.Fatal(err)
	}
	if replayed.Error == nil || replayed.Error.Code != ErrCodeReplay {
		t.Fatalf("response = %+v, want replay", replayed)
	}
}

func TestEndpointRateLimited(t *testing.T) {
	t.Parallel()
	spec := validSpec()
	spec.RateLimit = RateLimit{PerAgent: 1, Burst: 1, Window: time.Hour}
	url, closeFn := integrationEndpoint(t, []TypeSpec{spec}, nil)
	defer closeFn()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := dialClient(t, ctx, url)
	defer c.Close(websocket.StatusNormalClosure, "")

	send := func(nonce string) responseEnvelope {
		if err := wsjson.Write(ctx, c, map[string]any{
			"type":       "test.echo",
			"request_id": nonce,
			"nonce":      nonce,
			"payload":    map[string]string{},
		}); err != nil {
			t.Fatal(err)
		}
		var resp responseEnvelope
		if err := wsjson.Read(ctx, c, &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if r := send("n1"); r.Error != nil {
		t.Fatalf("first call error: %+v", r.Error)
	}
	r := send("n2")
	if r.Error == nil || r.Error.Code != ErrCodeRateLimited {
		t.Fatalf("response = %+v, want rate_limited", r)
	}
}

func TestEndpointHandlerError(t *testing.T) {
	t.Parallel()
	spec := validSpec()
	spec.Handler = func(context.Context, Envelope) (json.RawMessage, error) {
		return nil, errors.New("handler boom")
	}
	url, closeFn := integrationEndpoint(t, []TypeSpec{spec}, nil)
	defer closeFn()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := dialClient(t, ctx, url)
	defer c.Close(websocket.StatusNormalClosure, "")

	if err := wsjson.Write(ctx, c, map[string]any{
		"type":       "test.echo",
		"request_id": "r1",
		"nonce":      "n1",
		"payload":    map[string]string{},
	}); err != nil {
		t.Fatal(err)
	}
	var resp responseEnvelope
	if err := wsjson.Read(ctx, c, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodeHandlerError {
		t.Fatalf("response = %+v, want handler_error", resp)
	}
	if !strings.Contains(resp.Error.Message, "handler boom") {
		t.Fatalf("error message = %q, want to contain handler boom", resp.Error.Message)
	}
}

func TestEndpointPushNoResponse(t *testing.T) {
	t.Parallel()
	called := make(chan struct{}, 1)
	spec := validSpec()
	spec.Direction = DirectionPush
	spec.Handler = func(context.Context, Envelope) (json.RawMessage, error) {
		called <- struct{}{}
		return nil, nil
	}
	url, closeFn := integrationEndpoint(t, []TypeSpec{spec}, nil)
	defer closeFn()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := dialClient(t, ctx, url)
	defer c.Close(websocket.StatusNormalClosure, "")

	if err := wsjson.Write(ctx, c, map[string]any{
		"type":       "test.echo",
		"request_id": "r1",
		"nonce":      "n1",
		"payload":    map[string]string{},
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-called:
	case <-ctx.Done():
		t.Fatal("handler not called")
	}

	// No response should arrive on a push. Read with a short deadline
	// and expect timeout.
	readCtx, readCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer readCancel()
	var resp responseEnvelope
	err := wsjson.Read(readCtx, c, &resp)
	if err == nil {
		t.Fatalf("push direction should not produce a response, got %+v", resp)
	}
}

func TestEndpointAuditCapturesAcceptedAndRejected(t *testing.T) {
	t.Parallel()
	rec := &recordingAudit{}
	url, closeFn := integrationEndpoint(t, []TypeSpec{validSpec()}, nil, WithAuditWriter(rec))
	defer closeFn()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := dialClient(t, ctx, url)
	defer c.Close(websocket.StatusNormalClosure, "")

	// Accepted call.
	if err := wsjson.Write(ctx, c, map[string]any{
		"type":       "test.echo",
		"request_id": "r1",
		"nonce":      "n1",
		"payload":    map[string]string{"a": "b"},
	}); err != nil {
		t.Fatal(err)
	}
	var resp responseEnvelope
	_ = wsjson.Read(ctx, c, &resp)

	// Rejected call (replayed nonce).
	if err := wsjson.Write(ctx, c, map[string]any{
		"type":       "test.echo",
		"request_id": "r2",
		"nonce":      "n1",
		"payload":    map[string]string{"a": "b"},
	}); err != nil {
		t.Fatal(err)
	}
	_ = wsjson.Read(ctx, c, &resp)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.lines) != 2 {
		t.Fatalf("expected 2 audit lines, got %d", len(rec.lines))
	}
	if rec.lines[0].Decision != "accepted" {
		t.Fatalf("first decision = %q, want accepted", rec.lines[0].Decision)
	}
	if !strings.HasPrefix(rec.lines[0].PayloadHash, "sha256:") {
		t.Fatalf("AuditFull should populate PayloadHash, got %q", rec.lines[0].PayloadHash)
	}
	if rec.lines[1].Decision != ErrCodeReplay {
		t.Fatalf("second decision = %q, want replay", rec.lines[1].Decision)
	}
}

type recordingAudit struct {
	mu    sync.Mutex
	lines []AuditLine
}

func (r *recordingAudit) WriteAudit(line AuditLine) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case error:
		return t.Error()
	default:
		return ""
	}
}
