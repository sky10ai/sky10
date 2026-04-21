package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
)

func TestProcessHostDescribeAndHealth(t *testing.T) {
	t.Parallel()

	host := startHelperProcessHost(t, nil)
	defer func() {
		if err := host.Close(); err != nil {
			t.Fatalf("host.Close() error = %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var describe protocol.DescribeResult
	if err := host.Call(ctx, string(protocol.MethodDescribe), protocol.DescribeParams{
		BrokerProtocol: protocol.CurrentProtocol(),
	}, &describe); err != nil {
		t.Fatalf("Describe call error = %v", err)
	}
	if describe.Adapter.ID != "test-adapter" {
		t.Fatalf("adapter id = %q, want test-adapter", describe.Adapter.ID)
	}

	var health protocol.HealthResult
	if err := host.Call(ctx, string(protocol.MethodHealth), protocol.HealthParams{}, &health); err != nil {
		t.Fatalf("Health call error = %v", err)
	}
	if !health.Health.OK {
		t.Fatal("health.OK = false, want true")
	}

	if logs := host.Stderr(); logs == "" {
		t.Fatal("expected stderr logs from helper process")
	}
}

func TestProcessHostNotifications(t *testing.T) {
	t.Parallel()

	notifyCh := make(chan string, 1)
	host := startHelperProcessHost(t, func(method string, _ json.RawMessage) {
		select {
		case notifyCh <- method:
		default:
		}
	})
	defer func() {
		if err := host.Close(); err != nil {
			t.Fatalf("host.Close() error = %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var health protocol.HealthResult
	if err := host.Call(ctx, string(protocol.MethodHealth), protocol.HealthParams{}, &health); err != nil {
		t.Fatalf("Health call error = %v", err)
	}

	select {
	case method := <-notifyCh:
		if method != "messaging.adapter.event" {
			t.Fatalf("notification method = %q, want messaging.adapter.event", method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for helper notification")
	}
}

func TestProcessHostWaitUnexpectedExit(t *testing.T) {
	t.Parallel()

	exe := helperProcessExecutable(t)
	host, err := StartProcess(context.Background(), ProcessSpec{
		Path: exe,
		Args: []string{"-test.run=TestHelperMessagingAdapterProcess", "--"},
		Env:  []string{"GO_WANT_HELPER_MESSAGING_ADAPTER=1", "SKY10_MESSAGING_HELPER_MODE=exit-immediately"},
	}, nil)
	if err != nil {
		t.Fatalf("StartProcess() error = %v", err)
	}

	if err := host.Wait(); err == nil {
		t.Fatal("Wait() error = nil, want process failure")
	}
}

func TestHelperMessagingAdapterProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_MESSAGING_ADAPTER") != "1" {
		return
	}
	mode := os.Getenv("SKY10_MESSAGING_HELPER_MODE")
	if mode == "exit-immediately" {
		fmt.Fprintln(os.Stderr, "helper exiting immediately")
		os.Exit(23)
	}
	if err := runHelperMessagingAdapter(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}

func startHelperProcessHost(t *testing.T, notify NotificationHandler) *ProcessHost {
	t.Helper()

	exe := helperProcessExecutable(t)
	host, err := StartProcess(context.Background(), ProcessSpec{
		Path: exe,
		Args: []string{"-test.run=TestHelperMessagingAdapterProcess", "--"},
		Env:  []string{"GO_WANT_HELPER_MESSAGING_ADAPTER=1"},
	}, notify)
	if err != nil {
		t.Fatalf("StartProcess() error = %v", err)
	}
	return host
}

func helperProcessExecutable(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	return exe
}

func runHelperMessagingAdapter() error {
	fmt.Fprintln(os.Stderr, "helper adapter starting")

	dec := NewDecoder(os.Stdin)
	enc := NewEncoder(os.Stdout)
	for {
		var req Request
		if err := dec.Read(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		_ = enc.Write(map[string]any{
			"jsonrpc": jsonRPCVersion,
			"method":  "messaging.adapter.event",
			"params": map[string]any{
				"type": string(messaging.EventTypeMessageReceived),
			},
		})

		switch req.Method {
		case string(protocol.MethodDescribe):
			if err := enc.Write(Response{
				JSONRPC: jsonRPCVersion,
				ID:      req.ID,
				Result: mustJSON(protocol.DescribeResult{
					Protocol: protocol.CurrentProtocol(),
					Adapter: messaging.Adapter{
						ID:          "test-adapter",
						DisplayName: "Test Adapter",
						AuthMethods: []messaging.AuthMethod{messaging.AuthMethodOAuth2},
					},
				}),
			}); err != nil {
				return err
			}
		case string(protocol.MethodHealth):
			if err := enc.Write(Response{
				JSONRPC: jsonRPCVersion,
				ID:      req.ID,
				Result: mustJSON(protocol.HealthResult{
					Health: protocol.HealthStatus{
						OK:      true,
						Status:  messaging.ConnectionStatusConnected,
						Message: "ok",
					},
				}),
			}); err != nil {
				return err
			}
		default:
			if err := enc.Write(Response{
				JSONRPC: jsonRPCVersion,
				ID:      req.ID,
				Error: &ResponseError{
					Code:    -32601,
					Message: "method not found",
				},
			}); err != nil {
				return err
			}
		}
	}
}

func mustJSON(v any) json.RawMessage {
	body, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return body
}
