//go:build windows

package fs

import "log/slog"

// HandleDumpSignal is a no-op on Windows. The Unix SIGUSR1 dump path does not
// exist there, so watchdog-based dumps remain the supported debug mechanism.
func HandleDumpSignal(logger *slog.Logger) {
	_ = logger
}
