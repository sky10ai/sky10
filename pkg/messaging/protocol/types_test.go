package protocol

import (
	"encoding/json"
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

func TestConnectParamsCarriesResolvedCredential(t *testing.T) {
	t.Parallel()

	params := ConnectParams{
		Connection: messaging.Connection{
			ID:        "imap/work",
			AdapterID: "imap-smtp",
			Label:     "Work Mail",
		},
		Paths: RuntimePaths{
			RootDir:    "/tmp/sky10/messaging",
			SecretsDir: "/tmp/sky10/messaging/runtime/secrets",
		},
		Credential: &ResolvedCredential{
			Ref:         "secret://imap/work",
			AuthMethod:  messaging.AuthMethodBasic,
			ContentType: "application/json",
			Blob: BlobRef{
				ID:        "credential:c2VjcmV0",
				LocalPath: "/tmp/sky10/messaging/runtime/secrets/credential.bin",
				SizeBytes: 42,
			},
		},
	}

	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !json.Valid(raw) {
		t.Fatalf("marshaled json is invalid: %s", string(raw))
	}

	var decoded ConnectParams
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if decoded.Credential == nil {
		t.Fatal("decoded credential = nil, want populated credential")
	}
	if decoded.Credential.Ref != "secret://imap/work" {
		t.Fatalf("decoded credential ref = %q, want secret://imap/work", decoded.Credential.Ref)
	}
	if decoded.Paths.SecretsDir == "" {
		t.Fatal("decoded secrets dir = empty, want staged secret path")
	}
}
