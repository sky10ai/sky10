package skyfs

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestProgressReader(t *testing.T) {
	t.Parallel()

	data := strings.Repeat("hello", 1000)
	var lastTransferred int64
	var callCount int

	pr := NewProgressReader(
		strings.NewReader(data),
		int64(len(data)),
		func(transferred, total int64) {
			lastTransferred = transferred
			callCount++
		},
	)

	buf, err := io.ReadAll(pr)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if string(buf) != data {
		t.Error("data mismatch")
	}
	if lastTransferred != int64(len(data)) {
		t.Errorf("lastTransferred = %d, want %d", lastTransferred, len(data))
	}
	if callCount == 0 {
		t.Error("progress callback never called")
	}
}

func TestProgressWriter(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	var lastTransferred int64

	pw := NewProgressWriter(
		&buf,
		5000,
		func(transferred, total int64) {
			lastTransferred = transferred
		},
	)

	data := bytes.Repeat([]byte("x"), 5000)
	n, err := pw.Write(data)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 5000 {
		t.Errorf("wrote %d, want 5000", n)
	}
	if lastTransferred != 5000 {
		t.Errorf("lastTransferred = %d, want 5000", lastTransferred)
	}
}
