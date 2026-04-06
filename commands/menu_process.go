package commands

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func killAllMenuProcesses() error {
	out, err := exec.Command("pkill", "-x", "sky10-menu").CombinedOutput()
	if err == nil {
		return nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return nil
	}
	return fmt.Errorf("pkill sky10-menu: %s (%w)", strings.TrimSpace(string(out)), err)
}
