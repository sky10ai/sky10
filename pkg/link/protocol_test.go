package link

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestWriteReadMessageRoundTrip(t *testing.T) {
	t.Parallel()
	msg := &Message{
		ID:     "test-1",
		Method: "echo",
		Params: json.RawMessage(`{"hello":"world"}`),
	}

	var buf bytes.Buffer
	if err := WriteMessage(&buf, msg); err != nil {
		t.Fatal(err)
	}

	got, err := ReadMessage(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != msg.ID {
		t.Fatalf("ID: got %q, want %q", got.ID, msg.ID)
	}
	if got.Method != msg.Method {
		t.Fatalf("Method: got %q, want %q", got.Method, msg.Method)
	}
	if string(got.Params) != string(msg.Params) {
		t.Fatalf("Params: got %s, want %s", got.Params, msg.Params)
	}
}

func TestWriteReadMessageResponse(t *testing.T) {
	t.Parallel()
	msg := &Message{
		ID:     "resp-1",
		Result: json.RawMessage(`{"ok":true}`),
	}

	var buf bytes.Buffer
	if err := WriteMessage(&buf, msg); err != nil {
		t.Fatal(err)
	}

	got, err := ReadMessage(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != "" {
		t.Fatalf("expected empty method for response, got %q", got.Method)
	}
	if string(got.Result) != `{"ok":true}` {
		t.Fatalf("Result: got %s", got.Result)
	}
}

func TestWriteReadMessageError(t *testing.T) {
	t.Parallel()
	msg := &Message{
		ID:    "err-1",
		Error: &MessageError{Code: -32601, Message: "not found"},
	}

	var buf bytes.Buffer
	if err := WriteMessage(&buf, msg); err != nil {
		t.Fatal(err)
	}

	got, err := ReadMessage(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Error == nil {
		t.Fatal("expected error in response")
	}
	if got.Error.Message != "not found" {
		t.Fatalf("Error.Message: got %q", got.Error.Message)
	}
	if got.Error.Error() != "not found" {
		t.Fatalf("Error(): got %q", got.Error.Error())
	}
}

func TestWriteMessageTooLarge(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", MaxMessageSize+1)
	msg := &Message{
		ID:     "big",
		Result: json.RawMessage(`"` + big + `"`),
	}
	var buf bytes.Buffer
	err := WriteMessage(&buf, msg)
	if err == nil {
		t.Fatal("expected error for oversized message")
	}
}

func TestReadMessageTooLarge(t *testing.T) {
	t.Parallel()
	// Write a length prefix that exceeds MaxMessageSize.
	var buf bytes.Buffer
	lenBuf := []byte{0x01, 0x00, 0x00, 0x00} // 16MB
	buf.Write(lenBuf)
	_, err := ReadMessage(&buf)
	if err == nil {
		t.Fatal("expected error for oversized message")
	}
}

func TestReadMessageTruncated(t *testing.T) {
	t.Parallel()
	// Write valid length but truncate payload.
	var buf bytes.Buffer
	buf.Write([]byte{0x00, 0x00, 0x00, 0x10}) // 16 bytes
	buf.Write([]byte("short"))                // only 5 bytes
	_, err := ReadMessage(&buf)
	if err == nil {
		t.Fatal("expected error for truncated payload")
	}
}

func TestReadMessageEmptyReader(t *testing.T) {
	t.Parallel()
	_, err := ReadMessage(io.LimitReader(strings.NewReader(""), 0))
	if err == nil {
		t.Fatal("expected error for empty reader")
	}
}

func TestMultipleMessagesOnSameBuffer(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer

	msg1 := &Message{ID: "1", Method: "a"}
	msg2 := &Message{ID: "2", Method: "b"}
	msg3 := &Message{ID: "3", Method: "c"}

	WriteMessage(&buf, msg1)
	WriteMessage(&buf, msg2)
	WriteMessage(&buf, msg3)

	for _, want := range []string{"1", "2", "3"} {
		got, err := ReadMessage(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != want {
			t.Fatalf("got ID %q, want %q", got.ID, want)
		}
	}
}
