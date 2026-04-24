package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/config"
)

func TestGuestSky10RPCURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		address string
		want    string
	}{
		{
			name:    "ip address",
			address: "192.168.64.10",
			want:    "http://192.168.64.10:9101/rpc",
		},
		{
			name:    "http base",
			address: "http://192.168.64.10:9200",
			want:    "http://192.168.64.10:9200/rpc",
		},
		{
			name:    "https base with trailing slash",
			address: "https://guest.example.test/",
			want:    "https://guest.example.test/rpc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := guestSky10RPCURL(tt.address); got != tt.want {
				t.Fatalf("guestSky10RPCURL(%q) = %q, want %q", tt.address, got, tt.want)
			}
		})
	}
}

func TestHTTPPortFromAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		addr string
		want string
	}{
		{name: "bare port", addr: ":9101", want: "9101"},
		{name: "host port", addr: "127.0.0.1:9101", want: "9101"},
		{name: "ipv6 host port", addr: "[::1]:9101", want: "9101"},
		{name: "empty host from split host port", addr: ":9101", want: "9101"},
		{name: "missing port", addr: "127.0.0.1", want: ""},
		{name: "empty", addr: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := httpPortFromAddr(tt.addr); got != tt.want {
				t.Fatalf("httpPortFromAddr(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestLookupLimaInstanceIPv4FallsBackToRouteSource(t *testing.T) {
	t.Parallel()

	var scripts []string
	got, err := lookupLimaInstanceIPv4(context.Background(), func(ctx context.Context, bin string, args []string) ([]byte, error) {
		if bin != "/tmp/fake/limactl" {
			t.Fatalf("bin = %q, want /tmp/fake/limactl", bin)
		}
		if len(args) != 6 || args[0] != "shell" || args[1] != "devbox" || args[2] != "--" || args[3] != "bash" || args[4] != "-lc" {
			t.Fatalf("args = %v, want limactl shell bash invocation", args)
		}
		scripts = append(scripts, args[5])
		if strings.Contains(args[5], "route get 1.1.1.1") {
			return []byte("\n"), nil
		}
		if strings.Contains(args[5], "addr show scope global") {
			return []byte("192.168.64.33\n"), nil
		}
		return nil, fmt.Errorf("unexpected script: %s", args[5])
	}, "/tmp/fake/limactl", "devbox")
	if err != nil {
		t.Fatalf("lookupLimaInstanceIPv4() error: %v", err)
	}
	if got != "192.168.64.33" {
		t.Fatalf("lookupLimaInstanceIPv4() = %q, want 192.168.64.33", got)
	}
	if len(scripts) != 2 {
		t.Fatalf("lookup scripts = %d, want 2", len(scripts))
	}
}

func TestGuestSky10RPCAddressPrefersForwardedEndpoint(t *testing.T) {
	t.Parallel()

	got := guestSky10RPCAddress(Record{
		IPAddress:     "192.168.64.33",
		ForwardedHost: "127.0.0.1",
		ForwardedPort: 39101,
	})
	if got != "http://127.0.0.1:39101" {
		t.Fatalf("guestSky10RPCAddress() = %q, want forwarded URL", got)
	}

	got = guestSky10RPCAddress(Record{IPAddress: "192.168.64.33"})
	if got != "192.168.64.33" {
		t.Fatalf("guestSky10RPCAddress() = %q, want guest IP fallback", got)
	}
}

func TestCaptureGuestDeviceIdentityLooksUpIPAndPersistsGuest(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	rec := Record{
		Name:      "openclaw-m8",
		Slug:      "openclaw-m8",
		Provider:  providerLima,
		Template:  templateOpenClaw,
		Status:    "ready",
		VMStatus:  "Running",
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.records[rec.Slug] = rec
	m.outputCmd = func(ctx context.Context, bin string, args []string) ([]byte, error) {
		if len(args) == 6 && strings.Contains(args[5], "route get 1.1.1.1") {
			return []byte("192.168.64.44\n"), nil
		}
		return nil, fmt.Errorf("unexpected outputCmd args: %v", args)
	}
	m.guestRPC = func(ctx context.Context, address, method string, params interface{}, out interface{}) error {
		if address != "192.168.64.44" {
			t.Fatalf("guest RPC address = %q, want 192.168.64.44", address)
		}
		if method != "identity.show" {
			t.Fatalf("guest RPC method = %q, want identity.show", method)
		}
		body, err := json.Marshal(guestIdentity{
			Address:      "sky10-host",
			DeviceID:     "D-guest123",
			DevicePubKey: "ABCDEF1234567890",
			DeviceCount:  1,
		})
		if err != nil {
			t.Fatalf("marshal guest identity: %v", err)
		}
		return json.Unmarshal(body, out)
	}

	got := m.captureGuestDeviceIdentity(context.Background(), &rec, "/tmp/fake/limactl")
	if got == nil {
		t.Fatal("captureGuestDeviceIdentity() = nil, want record copy")
	}
	if got.IPAddress != "192.168.64.44" {
		t.Fatalf("captured ip address = %q, want 192.168.64.44", got.IPAddress)
	}
	if got.GuestDeviceID != "D-guest123" {
		t.Fatalf("captured guest device id = %q, want D-guest123", got.GuestDeviceID)
	}
	if got.GuestDevicePubKey != "abcdef1234567890" {
		t.Fatalf("captured guest device pubkey = %q, want abcdef1234567890", got.GuestDevicePubKey)
	}

	persisted, err := m.requireRecord("openclaw-m8")
	if err != nil {
		t.Fatalf("requireRecord() error: %v", err)
	}
	if persisted.IPAddress != "192.168.64.44" {
		t.Fatalf("persisted ip address = %q, want 192.168.64.44", persisted.IPAddress)
	}
	if persisted.GuestDevicePubKey != "abcdef1234567890" {
		t.Fatalf("persisted guest device pubkey = %q, want abcdef1234567890", persisted.GuestDevicePubKey)
	}
}
