//go:build !windows

package fs

import (
	"os"
	"syscall"
)

func processAlive(pid int, proc *os.Process) bool {
	_ = pid
	return proc.Signal(syscall.Signal(0)) == nil
}

func terminateProcess(proc *os.Process) error {
	return proc.Signal(syscall.SIGTERM)
}

func forceKillProcess(proc *os.Process) error {
	return proc.Signal(syscall.SIGKILL)
}
