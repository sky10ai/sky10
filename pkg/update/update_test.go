package update

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}

	info, err := Check("dev")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if info.Latest == "" {
		t.Error("latest version is empty")
	}
	if !info.Available {
		t.Error("expected update available for dev version")
	}
	if info.Current != "dev" {
		t.Errorf("current = %q, want %q", info.Current, "dev")
	}
}

// Regression: bare http.Get without User-Agent header causes GitHub API
// to return 403 on many Linux hosts / cloud IPs.
func TestCheckSendsUserAgent(t *testing.T) {
	asset := fmt.Sprintf("sky10-%s-%s", runtime.GOOS, runtime.GOARCH)
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name": "v1.0.0",
			"assets": []map[string]string{
				{"name": asset, "browser_download_url": "https://example.com/" + asset},
			},
		})
	}))
	defer srv.Close()

	origCheck := checkURL
	checkURL = srv.URL
	defer func() { checkURL = origCheck }()

	_, err := Check("v1.0.0")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if gotUA == "" || gotUA == "Go-http-client/1.1" || gotUA == "Go-http-client/2.0" {
		t.Errorf("expected custom User-Agent, got %q", gotUA)
	}
}

func TestCheckUpToDate(t *testing.T) {
	asset := fmt.Sprintf("sky10-%s-%s", runtime.GOOS, runtime.GOARCH)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name": "v1.0.0",
			"assets": []map[string]string{
				{"name": asset, "browser_download_url": "https://example.com/" + asset},
			},
		})
	}))
	defer srv.Close()

	origCheck := checkURL
	checkURL = srv.URL
	defer func() { checkURL = origCheck }()

	info, err := Check("v1.0.0")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if info.Available {
		t.Error("expected no update when versions match")
	}
}

func TestCheckUpdateAvailable(t *testing.T) {
	asset := fmt.Sprintf("sky10-%s-%s", runtime.GOOS, runtime.GOARCH)
	menuAsset := fmt.Sprintf("sky10-menu-%s-%s", runtime.GOOS, runtime.GOARCH)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name": "v2.0.0",
			"assets": []map[string]string{
				{"name": asset, "browser_download_url": "https://example.com/" + asset},
				{"name": menuAsset, "browser_download_url": "https://example.com/" + menuAsset},
			},
		})
	}))
	defer srv.Close()

	origCheck := checkURL
	checkURL = srv.URL
	defer func() { checkURL = origCheck }()

	info, err := Check("v1.0.0")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !info.Available {
		t.Error("expected update available")
	}
	if info.Latest != "v2.0.0" {
		t.Errorf("latest = %q, want %q", info.Latest, "v2.0.0")
	}
	if info.AssetURL == "" {
		t.Error("expected asset URL for current platform")
	}
	if info.MenuAssetURL == "" {
		t.Error("expected menu asset URL for current platform")
	}
}

func TestProgressReader(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 1024)
	var calls []int64
	pr := &progressReader{
		r:     bytes.NewReader(data),
		total: int64(len(data)),
		fn: func(downloaded, total int64) {
			calls = append(calls, downloaded)
		},
	}

	buf := make([]byte, 256)
	for {
		_, err := pr.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		// Advance past throttle window so every read triggers a callback.
		pr.lastEmit = time.Time{}
	}

	if len(calls) == 0 {
		t.Fatal("expected progress callbacks")
	}
	// Last callback should report all bytes read.
	if last := calls[len(calls)-1]; last != int64(len(data)) {
		t.Errorf("final progress = %d, want %d", last, len(data))
	}
}

func TestRPCHandlerDispatch(t *testing.T) {
	noop := func(string, interface{}) {}
	h := NewRPCHandler("v1.0.0", noop)

	// Non-system methods should not be handled.
	_, _, ok := h.Dispatch(context.Background(), "skyfs.list", nil)
	if ok {
		t.Error("expected system handler to not handle skyfs.list")
	}

	// Unknown system method should return error.
	_, err, ok := h.Dispatch(context.Background(), "system.unknown", nil)
	if !ok {
		t.Error("expected handled=true for system.unknown")
	}
	if err == nil {
		t.Error("expected error for unknown method")
	}
}

func TestRPCUpdateConcurrentGuard(t *testing.T) {
	// Mock a slow GitHub API so the update stays in-flight.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name": "v2.0.0",
			"assets":   []map[string]string{},
		})
	}))
	defer srv.Close()

	origCheck := checkURL
	checkURL = srv.URL
	defer func() { checkURL = origCheck }()

	var events []string
	var mu sync.Mutex
	emit := func(event string, _ interface{}) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}

	h := NewRPCHandler("v1.0.0", emit)

	// First call should succeed (starts async update).
	result, err, ok := h.Dispatch(context.Background(), "system.update", nil)
	if !ok || err != nil {
		t.Fatalf("first update: ok=%v, err=%v", ok, err)
	}
	m, _ := result.(map[string]string)
	if m["status"] != "checking" {
		t.Errorf("expected status=checking, got %v", result)
	}

	// Give the goroutine time to start the Check call.
	time.Sleep(50 * time.Millisecond)

	// Second call while first is in progress should be rejected.
	_, err, ok = h.Dispatch(context.Background(), "system.update", nil)
	if !ok {
		t.Error("expected handled=true")
	}
	if err == nil {
		t.Error("expected error for concurrent update")
	}
}

func TestRPCRestartDispatch(t *testing.T) {
	noop := func(string, interface{}) {}
	h := NewRPCHandler("v1.0.0", noop)
	h.restartDelay = 0

	called := make(chan struct{}, 1)
	h.SetRestartHandler(func() error {
		called <- struct{}{}
		return nil
	})

	result, err, ok := h.Dispatch(context.Background(), "system.restart", nil)
	if !ok || err != nil {
		t.Fatalf("restart dispatch: ok=%v err=%v", ok, err)
	}

	status, _ := result.(map[string]string)
	if status["status"] != "restarting" {
		t.Fatalf("status = %v, want restarting", result)
	}

	select {
	case <-called:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("restart handler was not called")
	}
}

func TestPeriodicCheckEmitsEvent(t *testing.T) {
	asset := fmt.Sprintf("sky10-%s-%s", runtime.GOOS, runtime.GOARCH)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name": "v2.0.0",
			"assets": []map[string]string{
				{"name": asset, "browser_download_url": "https://example.com/" + asset},
			},
		})
	}))
	defer srv.Close()

	origCheck := checkURL
	checkURL = srv.URL
	defer func() { checkURL = origCheck }()

	ctx, cancel := context.WithCancel(context.Background())
	var emitted []string
	var mu sync.Mutex
	emit := func(event string, _ interface{}) {
		mu.Lock()
		emitted = append(emitted, event)
		mu.Unlock()
		// Cancel after first emit so PeriodicCheck returns.
		cancel()
	}

	PeriodicCheck(ctx, "v1.0.0", emit)

	mu.Lock()
	defer mu.Unlock()
	if len(emitted) == 0 {
		t.Fatal("expected update:available event")
	}
	if emitted[0] != "update:available" {
		t.Errorf("event = %q, want %q", emitted[0], "update:available")
	}
}
