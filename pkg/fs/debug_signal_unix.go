//go:build !windows

package fs

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

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
