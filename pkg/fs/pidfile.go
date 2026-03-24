package fs

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DaemonPIDPath returns the path to the daemon PID file.
func DaemonPIDPath() string {
	return "/tmp/sky10/daemon.pid"
}

// WritePIDFile writes the current process ID to the PID file.
func WritePIDFile() error {
	os.MkdirAll("/tmp/sky10", 0755)
	return os.WriteFile(DaemonPIDPath(), []byte(strconv.Itoa(os.Getpid())), 0600)
}

// RemovePIDFile removes the PID file.
func RemovePIDFile() {
	os.Remove(DaemonPIDPath())
}

// KillExistingDaemon reads the PID file and kills the existing daemon
// if it's still running. Waits up to 3 seconds for it to exit.
func KillExistingDaemon() error {
	data, err := os.ReadFile(DaemonPIDPath())
	if err != nil {
		return nil // no PID file — nothing to kill
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		os.Remove(DaemonPIDPath())
		return nil // corrupt PID file
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		os.Remove(DaemonPIDPath())
		return nil // process not found
	}

	// Check if process is alive (signal 0 = check only)
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		os.Remove(DaemonPIDPath())
		return nil // already dead
	}

	// Kill it
	proc.Signal(syscall.SIGTERM)

	// Wait up to 3 seconds
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			os.Remove(DaemonPIDPath())
			return nil // exited
		}
	}

	// Force kill
	proc.Signal(syscall.SIGKILL)
	time.Sleep(100 * time.Millisecond)
	os.Remove(DaemonPIDPath())
	return fmt.Errorf("killed stale daemon (pid %d)", pid)
}
