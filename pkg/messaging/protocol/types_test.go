package protocol

import (
	"errors"
	"testing"

	"github.com/sky10/sky10/pkg/messaging"
)

func TestCurrentProtocolValid(t *testing.T) {
	t.Parallel()

	info := CurrentProtocol()
	if err := info.Validate(); err != nil {
		t.Fatalf("CurrentProtocol().Validate() error = %v", err)
	}
	if info.Name != Name {
		t.Fatalf("CurrentProtocol().Name = %q, want %q", info.Name, Name)
	}
	if info.Version != Version {
		t.Fatalf("CurrentProtocol().Version = %q, want %q", info.Version, Version)
	}
}

func TestNotSupportedError(t *testing.T) {
	t.Parallel()

	err := NotSupported(MethodPoll, "")
	if !errors.Is(err, err) {
		t.Fatal("expected not supported error to satisfy errors.Is with itself")
	}
	if err.Code != ProtocolErrorNotSupported {
		t.Fatalf("code = %q, want %q", err.Code, ProtocolErrorNotSupported)
	}
	if err.Method != MethodPoll {
		t.Fatalf("method = %q, want %q", err.Method, MethodPoll)
	}
}

func TestDescribeResultUsesValidMessagingAdapter(t *testing.T) {
	t.Parallel()

	result := DescribeResult{
		Protocol: CurrentProtocol(),
		Adapter: messaging.Adapter{
			ID:          "slack",
			DisplayName: "Slack",
			AuthMethods: []messaging.AuthMethod{messaging.AuthMethodOAuth2},
		},
	}
	if err := result.Protocol.Validate(); err != nil {
		t.Fatalf("protocol validate: %v", err)
	}
	if err := result.Adapter.Validate(); err != nil {
		t.Fatalf("adapter validate: %v", err)
	}
}
