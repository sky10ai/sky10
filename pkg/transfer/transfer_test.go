package transfer

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestReaderProgress(t *testing.T) {
	data := strings.Repeat("x", 1000)
	r := NewReader(strings.NewReader(data), 1000)
	ch := r.Progress()

	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(buf) != 1000 {
		t.Fatalf("read %d bytes, want 1000", len(buf))
	}
	if r.Bytes() != 1000 {
		t.Errorf("Bytes() = %d, want 1000", r.Bytes())
	}

	// Drain channel — last value should be the final progress
	var last Progress
	for {
		select {
		case p := <-ch:
			last = p
		default:
			goto done
		}
	}
done:
	if last.Bytes != 1000 {
		t.Errorf("last progress bytes = %d, want 1000", last.Bytes)
	}
	if last.Total != 1000 {
		t.Errorf("last progress total = %d, want 1000", last.Total)
	}
}

func TestReaderUnknownTotal(t *testing.T) {
	r := NewReader(strings.NewReader("hello"), -1)
	io.ReadAll(r)
	if r.Bytes() != 5 {
		t.Errorf("Bytes() = %d, want 5", r.Bytes())
	}
}

func TestReaderNoProgressChannel(t *testing.T) {
	// No Progress() call — should not panic
	r := NewReader(strings.NewReader("hello"), 5)
	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Errorf("got %q, want hello", string(buf))
	}
}

func TestReaderNonBlocking(t *testing.T) {
	// Progress channel is never read — Read must not block
	r := NewReader(strings.NewReader(strings.Repeat("x", 10000)), 10000)
	_ = r.Progress() // create channel but never consume

	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(buf) != 10000 {
		t.Fatalf("read %d bytes, want 10000", len(buf))
	}
}

func TestWriterProgress(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, 500)
	ch := w.Progress()

	data := bytes.Repeat([]byte("y"), 500)
	n, err := w.Write(data)
	if err != nil {
		t.Fatal(err)
	}
	if n != 500 {
		t.Fatalf("wrote %d bytes, want 500", n)
	}
	if w.Bytes() != 500 {
		t.Errorf("Bytes() = %d, want 500", w.Bytes())
	}

	select {
	case p := <-ch:
		if p.Bytes != 500 {
			t.Errorf("progress bytes = %d, want 500", p.Bytes)
		}
	default:
		t.Error("no progress received")
	}
}

func TestWriterNonBlocking(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, 10000)
	_ = w.Progress()

	data := bytes.Repeat([]byte("z"), 10000)
	n, err := w.Write(data)
	if err != nil {
		t.Fatal(err)
	}
	if n != 10000 {
		t.Fatalf("wrote %d, want 10000", n)
	}
}

func TestWriterMultipleWrites(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, 30)

	w.Write([]byte("0123456789"))
	w.Write([]byte("0123456789"))
	w.Write([]byte("0123456789"))

	if w.Bytes() != 30 {
		t.Errorf("Bytes() = %d, want 30", w.Bytes())
	}
}
