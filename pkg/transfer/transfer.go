// Package transfer provides streaming io.Reader/io.Writer wrappers
// with progress tracking and idle timeout detection. Progress updates
// are non-blocking — the latest state is stored atomically and can be
// polled, or delivered via a non-blocking channel send.
package transfer

import (
	"io"
	"sync/atomic"
	"time"
)

// Progress reports the state of a transfer.
type Progress struct {
	Bytes int64 // bytes transferred so far
	Total int64 // total expected bytes (-1 if unknown)
}

// Reader wraps an io.Reader with progress tracking and idle timeout.
type Reader struct {
	r           io.Reader
	total       int64
	bytes       atomic.Int64
	lastRead    atomic.Int64 // unix nano
	idleTimeout time.Duration
	progress    chan Progress // non-blocking progress updates
}

// NewReader wraps r with progress tracking.
// total is the expected byte count (-1 if unknown).
func NewReader(r io.Reader, total int64) *Reader {
	tr := &Reader{
		r:     r,
		total: total,
	}
	tr.lastRead.Store(time.Now().UnixNano())
	return tr
}

// SetIdleTimeout sets the max duration with no bytes before Read returns
// ErrIdleTimeout. Zero means no idle timeout (default).
func (r *Reader) SetIdleTimeout(d time.Duration) {
	r.idleTimeout = d
}

// Progress returns a channel that receives non-blocking progress updates.
// The channel has a buffer of 1 — slow consumers get the latest state,
// intermediate updates are dropped. Must be called before reading starts.
func (r *Reader) Progress() <-chan Progress {
	r.progress = make(chan Progress, 1)
	return r.progress
}

// Read implements io.Reader.
func (r *Reader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		bytes := r.bytes.Add(int64(n))
		r.lastRead.Store(time.Now().UnixNano())
		r.emit(bytes)
	}
	if err == nil && n == 0 && r.idleTimeout > 0 {
		last := time.Unix(0, r.lastRead.Load())
		if time.Since(last) > r.idleTimeout {
			return 0, ErrIdleTimeout
		}
	}
	return n, err
}

// Bytes returns the total bytes transferred so far.
func (r *Reader) Bytes() int64 { return r.bytes.Load() }

func (r *Reader) emit(bytes int64) {
	if r.progress == nil {
		return
	}
	p := Progress{Bytes: bytes, Total: r.total}
	select {
	case r.progress <- p:
	default:
		// Drop intermediate update — channel has latest or consumer is slow.
		// Drain and replace so the channel always has the most recent value.
		select {
		case <-r.progress:
		default:
		}
		select {
		case r.progress <- p:
		default:
		}
	}
}

// Writer wraps an io.Writer with progress tracking.
type Writer struct {
	w        io.Writer
	total    int64
	bytes    atomic.Int64
	progress chan Progress
}

// NewWriter wraps w with progress tracking.
// total is the expected byte count (-1 if unknown).
func NewWriter(w io.Writer, total int64) *Writer {
	return &Writer{w: w, total: total}
}

// Progress returns a channel that receives non-blocking progress updates.
func (w *Writer) Progress() <-chan Progress {
	w.progress = make(chan Progress, 1)
	return w.progress
}

// Write implements io.Writer.
func (w *Writer) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if n > 0 {
		bytes := w.bytes.Add(int64(n))
		w.emit(bytes)
	}
	return n, err
}

// Bytes returns the total bytes written so far.
func (w *Writer) Bytes() int64 { return w.bytes.Load() }

func (w *Writer) emit(bytes int64) {
	if w.progress == nil {
		return
	}
	p := Progress{Bytes: bytes, Total: w.total}
	select {
	case w.progress <- p:
	default:
		select {
		case <-w.progress:
		default:
		}
		select {
		case w.progress <- p:
		default:
		}
	}
}
