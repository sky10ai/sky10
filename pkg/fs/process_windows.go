//go:build windows

package fs

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func processAlive(pid int, proc *os.Process) bool {
	_ = proc
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER) && !errors.Is(err, windows.ERROR_NOT_FOUND)
	}
	defer windows.CloseHandle(handle)

	status, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return true
	}
	return status == uint32(windows.WAIT_TIMEOUT)
}

func terminateProcess(proc *os.Process) error {
	return proc.Kill()
}

func forceKillProcess(proc *os.Process) error {
	return proc.Kill()
}
