// Package transfer provides streaming io.Reader/io.Writer wrappers
// with progress callbacks and idle timeout detection.
package transfer

import (
	"io"
	"sync"
	"time"
)

// Progress reports the state of a transfer.
type Progress struct {
	Bytes int64 // bytes transferred so far
	Total int64 // total expected bytes (-1 if unknown)
}

// OnProgress is called during reads/writes with current progress.
type OnProgress func(Progress)

// Reader wraps an io.Reader with progress reporting and idle timeout.
// If no bytes are read for IdleTimeout, the read returns an error.
type Reader struct {
	r           io.Reader
	onProgress  OnProgress
	idleTimeout time.Duration

	mu       sync.Mutex
	bytes    int64
	total    int64
	lastRead time.Time
}

// NewReader wraps r with progress tracking.
// total is the expected byte count (-1 if unknown).
func NewReader(r io.Reader, total int64, onProgress OnProgress) *Reader {
	return &Reader{
		r:          r,
		onProgress: onProgress,
		total:      total,
		lastRead:   time.Now(),
	}
}

// SetIdleTimeout sets the max duration with no bytes before Read returns
// ErrIdleTimeout. Zero means no idle timeout (default).
func (r *Reader) SetIdleTimeout(d time.Duration) {
	r.idleTimeout = d
}

// Read implements io.Reader.
func (r *Reader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.mu.Lock()
		r.bytes += int64(n)
		r.lastRead = time.Now()
		bytes := r.bytes
		r.mu.Unlock()

		if r.onProgress != nil {
			r.onProgress(Progress{Bytes: bytes, Total: r.total})
		}
	}
	if err == nil && n == 0 && r.idleTimeout > 0 {
		r.mu.Lock()
		idle := time.Since(r.lastRead)
		r.mu.Unlock()
		if idle > r.idleTimeout {
			return 0, ErrIdleTimeout
		}
	}
	return n, err
}

// Bytes returns the total bytes transferred so far.
func (r *Reader) Bytes() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bytes
}

// Writer wraps an io.Writer with progress reporting.
type Writer struct {
	w          io.Writer
	onProgress OnProgress

	mu    sync.Mutex
	bytes int64
	total int64
}

// NewWriter wraps w with progress tracking.
// total is the expected byte count (-1 if unknown).
func NewWriter(w io.Writer, total int64, onProgress OnProgress) *Writer {
	return &Writer{
		w:          w,
		onProgress: onProgress,
		total:      total,
	}
}

// Write implements io.Writer.
func (w *Writer) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if n > 0 {
		w.mu.Lock()
		w.bytes += int64(n)
		bytes := w.bytes
		w.mu.Unlock()

		if w.onProgress != nil {
			w.onProgress(Progress{Bytes: bytes, Total: w.total})
		}
	}
	return n, err
}

// Bytes returns the total bytes written so far.
func (w *Writer) Bytes() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.bytes
}
