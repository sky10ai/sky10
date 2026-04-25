package update

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func withRPCStubs(t *testing.T) {
	t.Helper()
	oldCheckPassive := rpcCheckPassive
	oldCheckAction := rpcCheckAction
	oldApply := rpcApply
	oldApplyMenu := rpcApplyMenu
	oldStage := rpcStage
	oldStatus := rpcStatus
	oldInstall := rpcInstallStaged
	t.Cleanup(func() {
		rpcCheckPassive = oldCheckPassive
		rpcCheckAction = oldCheckAction
		rpcApply = oldApply
		rpcApplyMenu = oldApplyMenu
		rpcStage = oldStage
		rpcStatus = oldStatus
		rpcInstallStaged = oldInstall
	})
}

func TestCheckDevBuildSkipsUpdate(t *testing.T) {
	info, err := Check("dev")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if info.Current != "dev" {
		t.Errorf("current = %q, want %q", info.Current, "dev")
	}
	if info.Latest != "" {
		t.Errorf("latest = %q, want empty", info.Latest)
	}
	if info.Available || info.CLIAvailable || info.MenuAvailable {
		t.Fatalf("expected no update info for dev build: %#v", info)
	}
}

func TestCheckExplicitDevBuildChecksLatestRelease(t *testing.T) {
	asset := cliAssetName(runtime.GOOS, runtime.GOARCH)
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

	info, err := CheckExplicit("dev")
	if err != nil {
		t.Fatalf("CheckExplicit: %v", err)
	}
	if info.Current != "dev" {
		t.Fatalf("current = %q, want %q", info.Current, "dev")
	}
	if info.Latest != "v2.0.0" {
		t.Fatalf("latest = %q, want %q", info.Latest, "v2.0.0")
	}
	if !info.Available || !info.CLIAvailable {
		t.Fatalf("expected update available for explicit dev check: %#v", info)
	}
	if info.AssetURL == "" {
		t.Fatal("expected asset URL for explicit dev check")
	}
}

// Regression: bare http.Get without User-Agent header causes GitHub API
// to return 403 on many Linux hosts / cloud IPs.
func TestCheckSendsUserAgent(t *testing.T) {
	asset := cliAssetName(runtime.GOOS, runtime.GOARCH)
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
	asset := cliAssetName(runtime.GOOS, runtime.GOARCH)
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
	asset := cliAssetName(runtime.GOOS, runtime.GOARCH)
	menuAsset := menuAssetName(runtime.GOOS, runtime.GOARCH)
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

func TestRPCSystemHealthNotClaimed(t *testing.T) {
	h := NewRPCHandler("v1.0.0", func(string, interface{}) {})

	result, err, ok := h.Dispatch(context.Background(), "system.health", nil)
	if ok {
		t.Fatal("system.health should be handled by the daemon health handler")
	}
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if result != nil {
		t.Fatalf("result = %#v, want nil", result)
	}
}

func TestRPCUpdateStatusDispatch(t *testing.T) {
	withRPCStubs(t)

	rpcStatus = func(current string) (*StagedStatus, error) {
		return &StagedStatus{
			Current:    current,
			Ready:      true,
			Latest:     "v2.0.0",
			CLIStaged:  true,
			MenuStaged: true,
		}, nil
	}

	h := NewRPCHandler("v1.0.0", func(string, interface{}) {})
	result, err, ok := h.Dispatch(context.Background(), "system.update.status", nil)
	if !ok || err != nil {
		t.Fatalf("updateStatus dispatch: ok=%v err=%v", ok, err)
	}

	status, _ := result.(*StagedStatus)
	if status == nil {
		t.Fatalf("expected *StagedStatus, got %T", result)
	}
	if !status.Ready || status.Latest != "v2.0.0" {
		t.Fatalf("status = %#v", status)
	}
}

func TestRPCDownloadUpdateStagesRelease(t *testing.T) {
	withRPCStubs(t)

	rpcCheckAction = func(current string) (*Info, error) {
		return &Info{
			Current:       current,
			Latest:        "v2.0.0",
			Available:     true,
			CLIAvailable:  true,
			MenuAvailable: true,
		}, nil
	}
	rpcStage = func(info *Info, onProgress ProgressFunc) (*StagedRelease, error) {
		onProgress(5, 10)
		return &StagedRelease{
			Current:    info.Current,
			Latest:     info.Latest,
			CLIStaged:  true,
			MenuStaged: true,
		}, nil
	}

	progressSeen := make(chan struct{}, 1)
	completeSeen := make(chan interface{}, 1)
	emit := func(event string, data interface{}) {
		switch event {
		case "update:download:progress":
			select {
			case progressSeen <- struct{}{}:
			default:
			}
		case "update:download:complete":
			select {
			case completeSeen <- data:
			default:
			}
		}
	}

	h := NewRPCHandler("v1.0.0", emit)
	result, err, ok := h.Dispatch(context.Background(), "system.update.download", nil)
	if !ok || err != nil {
		t.Fatalf("downloadUpdate dispatch: ok=%v err=%v", ok, err)
	}

	status, _ := result.(map[string]string)
	if status["status"] != "downloading" {
		t.Fatalf("status = %v, want downloading", result)
	}

	select {
	case <-progressSeen:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected update:download:progress event")
	}

	select {
	case data := <-completeSeen:
		staged, _ := data.(*StagedRelease)
		if staged == nil || staged.Latest != "v2.0.0" {
			t.Fatalf("complete data = %#v", data)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected update:download:complete event")
	}
}

func TestRPCDownloadUpdateAllowsDevBuild(t *testing.T) {
	withRPCStubs(t)

	asset := cliAssetName(runtime.GOOS, runtime.GOARCH)
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

	rpcStage = func(info *Info, onProgress ProgressFunc) (*StagedRelease, error) {
		return &StagedRelease{
			Current:   info.Current,
			Latest:    info.Latest,
			CLIStaged: true,
		}, nil
	}

	completeSeen := make(chan interface{}, 1)
	emit := func(event string, data interface{}) {
		if event != "update:download:complete" {
			return
		}
		select {
		case completeSeen <- data:
		default:
		}
	}

	h := NewRPCHandler("dev", emit)
	result, err, ok := h.Dispatch(context.Background(), "system.update.download", nil)
	if !ok || err != nil {
		t.Fatalf("downloadUpdate dispatch: ok=%v err=%v", ok, err)
	}

	status, _ := result.(map[string]string)
	if status["status"] != "downloading" {
		t.Fatalf("status = %v, want downloading", result)
	}

	select {
	case data := <-completeSeen:
		staged, _ := data.(*StagedRelease)
		if staged == nil || staged.Current != "dev" || staged.Latest != "v2.0.0" {
			t.Fatalf("complete data = %#v", data)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected update:download:complete event")
	}
}

func TestRPCInstallUpdateSchedulesRestart(t *testing.T) {
	withRPCStubs(t)

	rpcInstallStaged = func() (*StagedRelease, error) {
		return &StagedRelease{
			Current:    "v1.0.0",
			Latest:     "v2.0.0",
			CLIStaged:  true,
			MenuStaged: true,
		}, nil
	}

	restarted := make(chan struct{}, 1)
	installSeen := make(chan map[string]any, 1)
	emit := func(event string, data interface{}) {
		if event != "update:install:complete" {
			return
		}
		payload, _ := data.(map[string]any)
		select {
		case installSeen <- payload:
		default:
		}
	}

	h := NewRPCHandler("v1.0.0", emit)
	h.restartDelay = 0
	h.SetRestartHandler(func() error {
		restarted <- struct{}{}
		return nil
	})

	result, err, ok := h.Dispatch(context.Background(), "system.update.install", nil)
	if !ok || err != nil {
		t.Fatalf("installUpdate dispatch: ok=%v err=%v", ok, err)
	}

	payload, _ := result.(map[string]any)
	if payload["status"] != "restarting" {
		t.Fatalf("status = %v, want restarting", result)
	}
	if payload["restarting"] != true {
		t.Fatalf("restarting = %v, want true", payload["restarting"])
	}

	select {
	case event := <-installSeen:
		if event["latest"] != "v2.0.0" {
			t.Fatalf("install event = %#v", event)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected update:install:complete event")
	}

	select {
	case <-restarted:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("restart handler was not called")
	}
}

func TestPeriodicCheckEmitsEvent(t *testing.T) {
	asset := cliAssetName(runtime.GOOS, runtime.GOARCH)
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

func TestPeriodicCheckSkipsDevBuild(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	origCheck := checkURL
	checkURL = srv.URL
	defer func() { checkURL = origCheck }()

	emitted := make(chan struct{}, 1)
	PeriodicCheck(context.Background(), "dev", func(string, interface{}) {
		emitted <- struct{}{}
	})

	select {
	case <-emitted:
		t.Fatal("did not expect update event for dev build")
	default:
	}
	if hits.Load() != 0 {
		t.Fatalf("expected no network calls for dev build, got %d", hits.Load())
	}
}
