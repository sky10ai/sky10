package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
)

func TestAdapterClientDescribeHealthAndConnect(t *testing.T) {
	t.Parallel()

	client := startHelperAdapterClient(t, "")
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("client.Close() error = %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	describe, err := client.Describe(ctx)
	if err != nil {
		t.Fatalf("Describe() error = %v", err)
	}
	if describe.Adapter.ID != "test-adapter" {
		t.Fatalf("adapter id = %q, want test-adapter", describe.Adapter.ID)
	}

	health, err := client.Health(ctx, protocol.HealthParams{})
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if !health.Health.OK {
		t.Fatal("health ok = false, want true")
	}

	connect, err := client.Connect(ctx, protocol.ConnectParams{
		Connection: messaging.Connection{
			ID:        "slack/work",
			AdapterID: "slack",
			Label:     "Slack Work",
		},
		Paths: protocol.RuntimePaths{RootDir: "/tmp/sky10-test"},
	})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if connect.Status != messaging.ConnectionStatusConnected {
		t.Fatalf("connect status = %q, want %q", connect.Status, messaging.ConnectionStatusConnected)
	}
	if len(connect.Identities) != 1 {
		t.Fatalf("connect identities = %d, want 1", len(connect.Identities))
	}
}

func TestAdapterClientNotificationHandler(t *testing.T) {
	t.Parallel()

	notifyCh := make(chan string, 1)
	client, err := StartAdapter(context.Background(), helperAdapterSpec(""), func(method string, params json.RawMessage) {
		select {
		case notifyCh <- method + ":" + string(params):
		default:
		}
	})
	if err != nil {
		t.Fatalf("StartAdapter() error = %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("client.Close() error = %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.Health(ctx, protocol.HealthParams{}); err != nil {
		t.Fatalf("Health() error = %v", err)
	}

	select {
	case raw := <-notifyCh:
		if !strings.HasPrefix(raw, "messaging.adapter.event:") {
			t.Fatalf("notification = %q, want messaging.adapter.event prefix", raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for adapter notification")
	}
}

func TestAdapterClientDescribeRejectsProtocolMismatch(t *testing.T) {
	t.Parallel()

	client := startHelperAdapterClient(t, "protocol-mismatch")
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("client.Close() error = %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.Describe(ctx)
	if err == nil || !strings.Contains(err.Error(), "protocol version mismatch") {
		t.Fatalf("Describe() error = %v, want protocol version mismatch", err)
	}
}

func TestValidateProtocolCompatibility(t *testing.T) {
	t.Parallel()

	local := protocol.CurrentProtocol()
	remote := protocol.ProtocolInfo{
		Name:               protocol.Name,
		Version:            "v1beta9",
		CompatibleVersions: []string{protocol.Version},
	}
	if err := ValidateProtocolCompatibility(local, remote); err != nil {
		t.Fatalf("ValidateProtocolCompatibility() error = %v", err)
	}
}

func startHelperAdapterClient(t *testing.T, mode string) *AdapterClient {
	t.Helper()

	client, err := StartAdapter(context.Background(), helperAdapterSpec(mode), nil)
	if err != nil {
		t.Fatalf("StartAdapter() error = %v", err)
	}
	return client
}

func helperAdapterSpec(mode string) ProcessSpec {
	spec := ProcessSpec{
		Path: helperProcessExecutableForTests(),
		Args: []string{"-test.run=TestHelperMessagingAdapterProcess", "--"},
		Env:  []string{"GO_WANT_HELPER_MESSAGING_ADAPTER=1"},
	}
	if mode != "" {
		spec.Env = append(spec.Env, "SKY10_MESSAGING_HELPER_MODE="+mode)
	}
	return spec
}
