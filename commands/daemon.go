package commands

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// DaemonCmd returns the "daemon" command tree.
func DaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the sky10 background daemon",
	}
	cmd.AddCommand(daemonInstallCmd())
	cmd.AddCommand(daemonUninstallCmd())
	cmd.AddCommand(daemonStatusCmd())
	cmd.AddCommand(daemonRestartCmd())
	cmd.AddCommand(daemonStopCmd())
	return cmd
}

func unsupportedPlatform() error {
	return fmt.Errorf("daemon management not supported on %s (use macOS launchd or Linux systemd)", runtime.GOOS)
}
