package apps

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestRPCHandler_UnknownNamespaceIsIgnored(t *testing.T) {
	h := NewRPCHandler(nil)

	_, _, handled := h.Dispatch(context.Background(), "wallet.status", nil)
	if handled {
		t.Fatal("wallet.status should not be handled by apps RPC")
	}
}

func TestRPCHandler_StatusAndCheckUpdateDispatch(t *testing.T) {
	h := NewRPCHandler(nil)
	h.lookup = func(id string) (*AppInfo, error) {
		return &AppInfo{ID: AppLima, Name: "Lima"}, nil
	}
	h.status = func(id ID) (*Status, error) {
		return &Status{ID: id, Name: "Lima", Installed: true, Managed: true}, nil
	}
	h.check = func(id ID) (*ReleaseInfo, error) {
		return &ReleaseInfo{ID: id, Current: "v1.2.2", Latest: "v1.2.3", Available: true}, nil
	}

	statusRaw, err, handled := h.Dispatch(context.Background(), "apps.status", mustAppsJSON(t, map[string]string{"id": "lima"}))
	if err != nil {
		t.Fatalf("apps.status error: %v", err)
	}
	if !handled {
		t.Fatal("apps.status should be handled")
	}
	status, ok := statusRaw.(*Status)
	if !ok || status.ID != AppLima {
		t.Fatalf("apps.status result = %#v, want Lima status", statusRaw)
	}

	releaseRaw, err, handled := h.Dispatch(context.Background(), "apps.checkUpdate", mustAppsJSON(t, map[string]string{"id": "lima"}))
	if err != nil {
		t.Fatalf("apps.checkUpdate error: %v", err)
	}
	if !handled {
		t.Fatal("apps.checkUpdate should be handled")
	}
	release, ok := releaseRaw.(*ReleaseInfo)
	if !ok || release.ID != AppLima || !release.Available {
		t.Fatalf("apps.checkUpdate result = %#v, want available Lima release", releaseRaw)
	}
}

func TestRPCHandler_InstallDispatchEmitsProgressAndComplete(t *testing.T) {
	events := make(chan map[string]interface{}, 4)
	h := NewRPCHandler(func(event string, data interface{}) {
		payload, ok := data.(map[string]interface{})
		if !ok {
			switch v := data.(type) {
			case map[string]string:
				payload = make(map[string]interface{}, len(v)+1)
				for k, value := range v {
					payload[k] = value
				}
			default:
				payload = map[string]interface{}{}
			}
		}
		payload["event"] = event
		events <- payload
	})
	h.lookup = func(id string) (*AppInfo, error) {
		return &AppInfo{ID: AppLima, Name: "Lima"}, nil
	}
	h.upgrade = func(id ID, progress ProgressFunc) (*ReleaseInfo, error) {
		progress(7, 10)
		return &ReleaseInfo{ID: id, Latest: "v1.2.3", Available: true}, nil
	}

	result, err, handled := h.Dispatch(context.Background(), "apps.install", mustAppsJSON(t, map[string]string{"id": "lima"}))
	if err != nil {
		t.Fatalf("apps.install error: %v", err)
	}
	if !handled {
		t.Fatal("apps.install should be handled")
	}
	reply, ok := result.(map[string]string)
	if !ok {
		t.Fatalf("apps.install result has unexpected type %T", result)
	}
	if reply["status"] != "installing" || reply["id"] != "lima" {
		t.Fatalf("apps.install result = %#v, want installing lima", reply)
	}

	progress := waitForAppsEvent(t, events)
	if progress["event"] != "apps:install:progress" {
		t.Fatalf("first event = %#v, want install progress", progress)
	}
	if progress["id"] != AppLima {
		t.Fatalf("progress id = %#v, want %q", progress["id"], AppLima)
	}

	complete := waitForAppsEvent(t, events)
	if complete["event"] != "apps:install:complete" {
		t.Fatalf("second event = %#v, want install complete", complete)
	}
	if complete["id"] != "lima" || complete["version"] != "v1.2.3" {
		t.Fatalf("complete payload = %#v, want lima v1.2.3", complete)
	}
}

func TestRPCHandler_InstallRejectsConcurrentInstallForSameApp(t *testing.T) {
	release := make(chan struct{})
	h := NewRPCHandler(nil)
	h.lookup = func(id string) (*AppInfo, error) {
		return &AppInfo{ID: AppLima, Name: "Lima"}, nil
	}
	h.upgrade = func(id ID, progress ProgressFunc) (*ReleaseInfo, error) {
		<-release
		return &ReleaseInfo{ID: id, Latest: "v1.2.3", Available: true}, nil
	}

	if _, err, handled := h.Dispatch(context.Background(), "apps.install", mustAppsJSON(t, map[string]string{"id": "lima"})); err != nil || !handled {
		t.Fatalf("first apps.install err=%v handled=%t", err, handled)
	}
	if _, err, handled := h.Dispatch(context.Background(), "apps.install", mustAppsJSON(t, map[string]string{"id": "lima"})); !handled {
		t.Fatal("second apps.install should be handled")
	} else if err == nil {
		t.Fatal("second apps.install should fail while install is running")
	}

	close(release)
}

func mustAppsJSON(t *testing.T, value interface{}) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	return data
}

func waitForAppsEvent(t *testing.T, events <-chan map[string]interface{}) map[string]interface{} {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for apps event")
		return nil
	}
}
