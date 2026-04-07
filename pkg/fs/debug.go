package fs

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"
)

// DumpGoroutines writes all goroutine stacks to the logger and raw stderr.
// This keeps live RPC logs in memory while letting the service manager capture
// full multiline stack dumps for post-mortem analysis.
func DumpGoroutines(logger *slog.Logger) {
	buf := make([]byte, 64*1024*1024) // 64MB — enough for any stack
	n := runtime.Stack(buf, true)     // true = all goroutines
	stack := string(buf[:n])

	logger.Warn("=== GOROUTINE DUMP ===", "goroutines", runtime.NumGoroutine())
	fmt.Fprintf(os.Stderr, "\n=== GOROUTINE DUMP %s (%d goroutines) ===\n%s\n=== END DUMP ===\n\n",
		time.Now().Format(time.RFC3339), runtime.NumGoroutine(), stack)
}

// HandleDumpSignal listens for SIGUSR1 and dumps all goroutine stacks.
// Call this once from the daemon startup.
func HandleDumpSignal(logger *slog.Logger) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	go func() {
		for range ch {
			logger.Info("received SIGUSR1, dumping goroutines")
			DumpGoroutines(logger)
		}
	}()
}

// Watchdog monitors worker goroutines and auto-dumps if any appear stuck.
// Each worker should call heartbeat() periodically. If no heartbeat arrives
// within the timeout, the watchdog dumps all goroutines to the log.
type Watchdog struct {
	mu      sync.Mutex
	logger  *slog.Logger
	timeout time.Duration
	workers map[string]*workerState
	dumped  bool // only dump once per freeze
}

type workerState struct {
	lastBeat time.Time
	isActive bool // false = idle/waiting (don't alert)
}

// NewWatchdog creates a watchdog with the given timeout.
func NewWatchdog(logger *slog.Logger, timeout time.Duration) *Watchdog {
	return &Watchdog{
		logger:  logger,
		timeout: timeout,
		workers: make(map[string]*workerState),
	}
}

// Register adds a worker to be monitored.
func (w *Watchdog) Register(name string) {
	w.workers[name] = &workerState{lastBeat: time.Now(), isActive: true}
}

// Heartbeat records that a worker is alive.
func (w *Watchdog) Heartbeat(name string) {
	w.mu.Lock()
	if ws, ok := w.workers[name]; ok {
		ws.lastBeat = time.Now()
		ws.isActive = true
		w.dumped = false // reset dump flag when workers recover
	}
	w.mu.Unlock()
}

// Run checks worker health periodically until context is cancelled.
func (w *Watchdog) Run(ctx context.Context) {
	ticker := time.NewTicker(w.timeout / 4)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.check()
		}
	}
}

func (w *Watchdog) check() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dumped {
		return
	}
	now := time.Now()
	for name, ws := range w.workers {
		if !ws.isActive {
			continue
		}
		gap := now.Sub(ws.lastBeat)
		if gap > w.timeout {
			w.logger.Error("watchdog: worker appears stuck",
				"worker", name,
				"last_heartbeat_ago", gap.Round(time.Second).String(),
				"timeout", w.timeout.String())
			DumpGoroutines(w.logger)
			w.dumped = true
			return
		}
	}
}
