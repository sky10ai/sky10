package sandbox

import "testing"

func TestMessengersBridgeURLUsesCanonicalPath(t *testing.T) {
	got, err := messengersBridgeURL(Record{
		ForwardedHost: "127.0.0.1",
		ForwardedPort: 39102,
	})
	if err != nil {
		t.Fatalf("messengersBridgeURL() error = %v", err)
	}
	want := "ws://127.0.0.1:39102/bridge/messengers/ws?bridge_role=host"
	if got != want {
		t.Fatalf("messengersBridgeURL() = %q, want %q", got, want)
	}
}
