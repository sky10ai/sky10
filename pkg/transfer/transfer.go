// Package transfer provides streaming io.Reader/io.Writer wrappers
// with progress tracking and idle timeout detection. Progress updates
// are non-blocking — the latest state is stored atomically and can be
// polled, or delivered via a non-blocking channel send.
//
// Read calls never block longer than the idle timeout. Each Read runs
// in a goroutine — if no bytes arrive within the timeout, the underlying
// reader is closed (if it implements io.Closer) and ErrIdleTimeout is
// returned. The goroutine is always cleaned up.
package transfer

import (
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// Progress reports the state of a transfer.
type Progress struct {
	Bytes int64 // bytes transferred so far
	Total int64 // total expected bytes (-1 if unknown)
}

// readResult is the result of a goroutine Read call.
type readResult struct {
	n   int
	err error
}

// Reader wraps an io.Reader with progress tracking and idle timeout.
//
// Two stall detection modes:
//   - Download mode (SetIdleTimeout only): detects when a single Read()
//     blocks for too long (data source stall). Each Read runs in a goroutine.
//   - Upload mode (SetIdleTimeout + OnStall): detects when the consumer stops
//     calling Read() (write-side stall). A background monitor watches for gaps
//     between Read() calls and fires the OnStall callback.
type Reader struct {
	r           io.Reader
	total       int64
	bytes       atomic.Int64
	lastRead    atomic.Int64 // unix nano
	idleTimeout time.Duration
	progress    chan Progress // non-blocking progress updates
	onStall     func()        // called when consumer stops reading
	stalled     atomic.Bool
	monitorOnce sync.Once
	doneOnce    sync.Once
	done        chan struct{} // closed when reader completes or Close called
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
// ErrIdleTimeout. If the underlying reader implements io.Closer, it is
// closed to unblock the stuck read and clean up the goroutine.
// Zero means no idle timeout (default).
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

// OnStall registers a function called when the consumer stops calling Read
// for longer than the idle timeout. Use for upload request bodies where the
// HTTP client stalls on a dead socket write. Typically pass a
// context.CancelFunc to abort the HTTP request. Must be called before Read.
func (r *Reader) OnStall(fn func()) {
	r.onStall = fn
}

// Stalled returns true if a consumer stall was detected.
func (r *Reader) Stalled() bool { return r.stalled.Load() }

// Close stops the stall monitor and closes the underlying reader
// if it implements io.Closer.
func (r *Reader) Close() error {
	r.signalDone()
	if c, ok := r.r.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// Read implements io.Reader.
//   - Upload mode (onStall set): uses readDirect + background monitor
//   - Download mode (onStall nil): each Read runs in a goroutine with timeout
func (r *Reader) Read(p []byte) (int, error) {
	if r.onStall != nil && r.idleTimeout > 0 {
		r.monitorOnce.Do(func() {
			r.done = make(chan struct{})
			go r.monitorStall()
		})
	}
	if r.idleTimeout > 0 && r.onStall == nil {
		return r.readWithTimeout(p)
	}
	return r.readDirect(p)
}

func (r *Reader) readDirect(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		bytes := r.bytes.Add(int64(n))
		r.lastRead.Store(time.Now().UnixNano())
		r.emit(bytes)
	}
	if err != nil {
		r.signalDone()
	}
	return n, err
}

func (r *Reader) readWithTimeout(p []byte) (int, error) {
	done := make(chan readResult, 1)
	go func() {
		n, err := r.r.Read(p)
		done <- readResult{n, err}
	}()

	select {
	case res := <-done:
		if res.n > 0 {
			bytes := r.bytes.Add(int64(res.n))
			r.lastRead.Store(time.Now().UnixNano())
			r.emit(bytes)
		}
		return res.n, res.err

	case <-time.After(r.idleTimeout):
		// Close the underlying reader to unblock the goroutine.
		if c, ok := r.r.(io.Closer); ok {
			c.Close()
		}
		<-done // wait for goroutine to exit
		r.signalDone()
		return 0, ErrIdleTimeout
	}
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

func (r *Reader) signalDone() {
	r.doneOnce.Do(func() {
		if r.done != nil {
			close(r.done)
		}
	})
}

func (r *Reader) monitorStall() {
	interval := r.idleTimeout / 4
	if interval < 50*time.Millisecond {
		interval = 50 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			last := r.lastRead.Load()
			gap := time.Duration(time.Now().UnixNano() - last)
			if gap > r.idleTimeout {
				r.stalled.Store(true)
				r.onStall()
				return
			}
		case <-r.done:
			return
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
