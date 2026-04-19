package device

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

func TestRegisterUsesExplicitDeviceID(t *testing.T) {
	t.Parallel()

	reg := &Registry{
		collector: localCollector{
			client:   testHTTPClient(`{"query":"203.0.113.8","city":"Austin","regionName":"Texas","country":"United States"}`),
			hostname: func() (string, error) { return "test-host", nil },
			goos:     "linux",
		},
		now: func() time.Time {
			return time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
		},
	}

	backend := s3adapter.NewMemory()
	ctx := context.Background()

	deviceID := "q6tvmewavwezlzjq"
	hexPubkey := "d2d9bcbbac7645f14809268473a77e81f55f4b29384d38803603f8ff4fae64c1"

	if err := reg.Register(ctx, backend, deviceID, hexPubkey, "test-device", "v0.30.1"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	wantKey := "devices/" + deviceID + ".json"
	keys, _ := backend.List(ctx, "devices/")
	if len(keys) != 1 {
		t.Fatalf("expected 1 device file, got %d: %v", len(keys), keys)
	}
	if keys[0] != wantKey {
		t.Errorf("device stored at %q, want %q", keys[0], wantKey)
	}

	dev, err := reg.read(ctx, backend, wantKey)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if dev.PubKey != hexPubkey {
		t.Errorf("PubKey = %q, want %q", dev.PubKey, hexPubkey)
	}
	if dev.ID != deviceID {
		t.Errorf("ID = %q, want %q", dev.ID, deviceID)
	}
	if dev.Platform != "Linux" {
		t.Errorf("Platform = %q, want Linux", dev.Platform)
	}
	if dev.Location != "Austin, Texas, United States" {
		t.Errorf("Location = %q, want Austin, Texas, United States", dev.Location)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func testHTTPClient(body string) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}
}
