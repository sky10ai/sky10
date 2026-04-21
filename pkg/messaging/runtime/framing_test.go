package runtime

import (
	"bytes"
	"strings"
	"testing"
)

func TestEncoderDecoderRoundTrip(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	dec := NewDecoder(&buf)

	payload := map[string]any{"method": "messaging.adapter.describe", "id": 1}
	if err := enc.Write(payload); err != nil {
		t.Fatalf("enc.Write() error = %v", err)
	}

	var got map[string]any
	if err := dec.Read(&got); err != nil {
		t.Fatalf("dec.Read() error = %v", err)
	}
	if got["method"] != payload["method"] {
		t.Fatalf("method = %v, want %v", got["method"], payload["method"])
	}
}

func TestDecoderRejectsMissingContentLength(t *testing.T) {
	t.Parallel()

	dec := NewDecoder(strings.NewReader("\r\n{}"))
	if err := dec.Read(&map[string]any{}); err == nil || !strings.Contains(err.Error(), "Content-Length") {
		t.Fatalf("dec.Read() error = %v, want missing Content-Length", err)
	}
}
