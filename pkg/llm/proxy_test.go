package llm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubBackend struct {
	readyErr     error
	forwardResp  *http.Response
	forwardErr   error
	topUpAmount  string
	topUpErr     error
	lastPath     string
	lastRawQuery string
	lastMethod   string
	lastBody     []byte
	lastHeaders  http.Header
	topUpCalls   int
	forwardCalls int
}

func (b *stubBackend) Ready() error {
	return b.readyErr
}

func (b *stubBackend) Forward(_ context.Context, path, rawQuery, method string, headers http.Header, body []byte) (*http.Response, error) {
	b.forwardCalls++
	b.lastPath = path
	b.lastRawQuery = rawQuery
	b.lastMethod = method
	b.lastHeaders = headers
	b.lastBody = append([]byte(nil), body...)
	if b.forwardErr != nil {
		return nil, b.forwardErr
	}
	if b.forwardResp != nil {
		return b.forwardResp, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
	}, nil
}

func (b *stubBackend) TopUp(_ context.Context, amountUSD string) (string, error) {
	b.topUpCalls++
	b.topUpAmount = amountUSD
	if b.topUpErr != nil {
		return "", b.topUpErr
	}
	if strings.TrimSpace(amountUSD) == "" {
		return "10", nil
	}
	return amountUSD, nil
}

func TestProxyForwardsRelativePath(t *testing.T) {
	t.Parallel()

	backend := &stubBackend{
		forwardResp: &http.Response{
			StatusCode: http.StatusCreated,
			Header: http.Header{
				"Content-Type":      {"application/json"},
				"X-Upstream-Header": {"present"},
				"Connection":        {"close"},
			},
			Body: io.NopCloser(strings.NewReader(`{"id":"ok"}`)),
		},
	}
	proxy, err := NewProxy(Config{PathPrefix: "/llm/v1"}, backend, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/llm/v1/chat/completions?stream=true", bytes.NewBufferString(`{"model":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "should-be-forwarded-by-generic-proxy")
	rr := httptest.NewRecorder()

	proxy.HandleAPI(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if backend.forwardCalls != 1 {
		t.Fatalf("forwardCalls = %d, want 1", backend.forwardCalls)
	}
	if backend.lastPath != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions", backend.lastPath)
	}
	if backend.lastRawQuery != "stream=true" {
		t.Fatalf("rawQuery = %q, want stream=true", backend.lastRawQuery)
	}
	if backend.lastMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", backend.lastMethod)
	}
	if string(backend.lastBody) != `{"model":"x"}` {
		t.Fatalf("body = %q", string(backend.lastBody))
	}
	if got := rr.Header().Get("X-Upstream-Header"); got != "present" {
		t.Fatalf("X-Upstream-Header = %q, want present", got)
	}
	if got := rr.Header().Get("Connection"); got != "" {
		t.Fatalf("Connection header leaked from upstream: %q", got)
	}
}

func TestProxyManualTopUpRoute(t *testing.T) {
	t.Parallel()

	backend := &stubBackend{}
	proxy, err := NewProxy(Config{PathPrefix: "/llm/v1"}, backend, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/llm/v1/x402/top-up", strings.NewReader(`{"amountUsd":"12.5"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	proxy.HandleAPI(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if backend.topUpCalls != 1 {
		t.Fatalf("topUpCalls = %d, want 1", backend.topUpCalls)
	}
	if backend.topUpAmount != "12.5" {
		t.Fatalf("topUpAmount = %q, want 12.5", backend.topUpAmount)
	}
	if !strings.Contains(rr.Body.String(), `"amountUsd":"12.5"`) {
		t.Fatalf("unexpected body: %s", rr.Body.String())
	}
}

func TestProxyReturnsServiceUnavailableWhenBackendNotReady(t *testing.T) {
	t.Parallel()

	proxy, err := NewProxy(Config{PathPrefix: "/llm/v1"}, &stubBackend{readyErr: fmt.Errorf("wallet unavailable")}, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/llm/v1/models", nil)
	rr := httptest.NewRecorder()

	proxy.HandleAPI(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "wallet unavailable") {
		t.Fatalf("unexpected body: %s", rr.Body.String())
	}
}
