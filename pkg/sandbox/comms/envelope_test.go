package comms

import (
	"encoding/json"
	"testing"
)

// TestWireEnvelopeDropsPayloadIdentity is the core security test for
// the structural protection: a payload-supplied agent_id or device_id
// at the top level of the wire JSON cannot survive the deserialization
// because the wireEnvelope struct does not have those fields. This
// test guards against accidental field additions that would undermine
// the whole identity-injection guarantee.
func TestWireEnvelopeDropsPayloadIdentity(t *testing.T) {
	t.Parallel()
	wireJSON := []byte(`{
		"type": "test.echo",
		"agent_id": "A-impostor",
		"device_id": "D-impostor",
		"request_id": "r1",
		"nonce": "n1",
		"payload": {"hello": "world"}
	}`)
	var w wireEnvelope
	if err := json.Unmarshal(wireJSON, &w); err != nil {
		t.Fatalf("unmarshal err = %v", err)
	}
	if w.Type != "test.echo" {
		t.Fatalf("Type = %q, want test.echo", w.Type)
	}
	if w.RequestID != "r1" {
		t.Fatalf("RequestID = %q, want r1", w.RequestID)
	}
	if w.Nonce != "n1" {
		t.Fatalf("Nonce = %q, want n1", w.Nonce)
	}

	// Re-encode and confirm no agent_id or device_id leak through. This
	// catches the failure mode where someone adds those fields back
	// later with a json tag that happens to deserialize correctly.
	out, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal err = %v", err)
	}
	var roundTrip map[string]json.RawMessage
	if err := json.Unmarshal(out, &roundTrip); err != nil {
		t.Fatalf("re-unmarshal err = %v", err)
	}
	if _, ok := roundTrip["agent_id"]; ok {
		t.Fatal("wireEnvelope must not carry agent_id field — payload-supplied identity must be dropped")
	}
	if _, ok := roundTrip["device_id"]; ok {
		t.Fatal("wireEnvelope must not carry device_id field — payload-supplied identity must be dropped")
	}
}

func TestWireEnvelopeAcceptsMissingOptionalFields(t *testing.T) {
	t.Parallel()
	wireJSON := []byte(`{"type": "test.echo", "nonce": "n1"}`)
	var w wireEnvelope
	if err := json.Unmarshal(wireJSON, &w); err != nil {
		t.Fatalf("unmarshal err = %v", err)
	}
	if w.RequestID != "" {
		t.Fatalf("RequestID = %q, want empty", w.RequestID)
	}
	if w.Timestamp != "" {
		t.Fatalf("Timestamp = %q, want empty", w.Timestamp)
	}
	if len(w.Payload) != 0 {
		t.Fatalf("Payload = %q, want empty", w.Payload)
	}
}
