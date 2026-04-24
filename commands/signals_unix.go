//go:build !windows

package commands

import (
	"os"
	"syscall"
)

func daemonShutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
