package fs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/config"
)

// RuntimeDir returns the directory for daemon state (socket, log, PID file).
// It preserves the legacy /tmp/sky10 default unless SKY10_HOME or
// SKY10_RUNTIME_DIR overrides are set.
func RuntimeDir() string {
	return config.RuntimeDir()
}

// DaemonPIDPath returns the path to the daemon PID file.
func DaemonPIDPath() string {
	return filepath.Join(RuntimeDir(), "daemon.pid")
}

// DaemonSocketPath returns the default path for the RPC Unix socket.
func DaemonSocketPath() string {
	path := filepath.Join(RuntimeDir(), "sky10.sock")
	if runtime.GOOS == "windows" || len(path) < maxUnixSocketPath {
		return path
	}
	return filepath.Join(shortSocketBaseDir(), "sky10-"+shortSocketSuffix(RuntimeDir())+".sock")
}

const maxUnixSocketPath = 100

func shortSocketBaseDir() string {
	if runtime.GOOS == "windows" {
		return os.TempDir()
	}
	return "/tmp"
}

func shortSocketSuffix(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}

// WritePIDFile writes the current process ID to the PID file.
func WritePIDFile() error {
	os.MkdirAll(RuntimeDir(), 0755)
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

	if !processAlive(pid, proc) {
		os.Remove(DaemonPIDPath())
		return nil // already dead
	}

	if err := terminateProcess(proc); err != nil && processAlive(pid, proc) {
		return fmt.Errorf("terminating stale daemon (pid %d): %w", pid, err)
	}

	// Wait up to 3 seconds
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if !processAlive(pid, proc) {
			os.Remove(DaemonPIDPath())
			return nil // exited
		}
	}

	if err := forceKillProcess(proc); err != nil && processAlive(pid, proc) {
		return fmt.Errorf("killing stale daemon (pid %d): %w", pid, err)
	}
	time.Sleep(100 * time.Millisecond)
	os.Remove(DaemonPIDPath())
	return fmt.Errorf("killed stale daemon (pid %d)", pid)
}
