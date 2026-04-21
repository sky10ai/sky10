package runtime

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// Encoder writes Content-Length framed JSON messages.
type Encoder struct {
	mu sync.Mutex
	w  io.Writer
}

// NewEncoder creates a framed message encoder.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w: w}
}

// Write marshals v as JSON and writes one framed message.
func (e *Encoder) Write(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal frame body: %w", err)
	}
	return e.WriteMessage(body)
}

// WriteMessage writes one already-encoded JSON message.
func (e *Encoder) WriteMessage(body []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := fmt.Fprintf(e.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if _, err := e.w.Write(body); err != nil {
		return fmt.Errorf("write frame body: %w", err)
	}
	return nil
}

// Decoder reads Content-Length framed JSON messages.
type Decoder struct {
	r *bufio.Reader
}

// NewDecoder creates a framed message decoder.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: bufio.NewReader(r)}
}

// ReadMessage reads one framed JSON payload.
func (d *Decoder) ReadMessage() ([]byte, error) {
	contentLength := -1
	for {
		line, err := d.r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("invalid frame header %q", line)
		}
		if !strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			continue
		}
		length, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("parse Content-Length %q: %w", value, err)
		}
		if length < 0 {
			return nil, fmt.Errorf("invalid Content-Length %d", length)
		}
		contentLength = length
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("frame missing Content-Length")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(d.r, body); err != nil {
		return nil, fmt.Errorf("read frame body: %w", err)
	}
	return body, nil
}

// Read reads and unmarshals one framed JSON payload into v.
func (d *Decoder) Read(v any) error {
	body, err := d.ReadMessage()
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("unmarshal frame body: %w", err)
	}
	return nil
}
