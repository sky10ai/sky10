//go:build windows

package commands

import "os"

func daemonShutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
