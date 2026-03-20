package transfer

import (
	"bytes"
	"io"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReaderProgress(t *testing.T) {
	data := strings.Repeat("x", 1000)
	var lastProgress Progress
	var callCount atomic.Int64

	r := NewReader(strings.NewReader(data), int64(len(data)), func(p Progress) {
		lastProgress = p
		callCount.Add(1)
	})

	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(buf) != 1000 {
		t.Fatalf("read %d bytes, want 1000", len(buf))
	}
	if lastProgress.Bytes != 1000 {
		t.Errorf("progress bytes = %d, want 1000", lastProgress.Bytes)
	}
	if lastProgress.Total != 1000 {
		t.Errorf("progress total = %d, want 1000", lastProgress.Total)
	}
	if callCount.Load() == 0 {
		t.Error("progress callback never called")
	}
	if r.Bytes() != 1000 {
		t.Errorf("Bytes() = %d, want 1000", r.Bytes())
	}
}

func TestReaderUnknownTotal(t *testing.T) {
	var lastProgress Progress
	r := NewReader(strings.NewReader("hello"), -1, func(p Progress) {
		lastProgress = p
	})
	io.ReadAll(r)
	if lastProgress.Total != -1 {
		t.Errorf("total = %d, want -1", lastProgress.Total)
	}
	if lastProgress.Bytes != 5 {
		t.Errorf("bytes = %d, want 5", lastProgress.Bytes)
	}
}

func TestReaderNilCallback(t *testing.T) {
	r := NewReader(strings.NewReader("hello"), 5, nil)
	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Errorf("got %q, want hello", string(buf))
	}
}

func TestWriterProgress(t *testing.T) {
	var buf bytes.Buffer
	var lastProgress Progress
	var callCount atomic.Int64

	w := NewWriter(&buf, 500, func(p Progress) {
		lastProgress = p
		callCount.Add(1)
	})

	data := bytes.Repeat([]byte("y"), 500)
	n, err := w.Write(data)
	if err != nil {
		t.Fatal(err)
	}
	if n != 500 {
		t.Fatalf("wrote %d bytes, want 500", n)
	}
	if lastProgress.Bytes != 500 {
		t.Errorf("progress bytes = %d, want 500", lastProgress.Bytes)
	}
	if lastProgress.Total != 500 {
		t.Errorf("progress total = %d, want 500", lastProgress.Total)
	}
	if callCount.Load() == 0 {
		t.Error("progress callback never called")
	}
	if w.Bytes() != 500 {
		t.Errorf("Bytes() = %d, want 500", w.Bytes())
	}
}

func TestWriterMultipleWrites(t *testing.T) {
	var buf bytes.Buffer
	var progress []int64

	w := NewWriter(&buf, 30, func(p Progress) {
		progress = append(progress, p.Bytes)
	})

	w.Write([]byte("0123456789"))
	w.Write([]byte("0123456789"))
	w.Write([]byte("0123456789"))

	if len(progress) != 3 {
		t.Fatalf("got %d callbacks, want 3", len(progress))
	}
	if progress[0] != 10 || progress[1] != 20 || progress[2] != 30 {
		t.Errorf("progress = %v, want [10 20 30]", progress)
	}
}
