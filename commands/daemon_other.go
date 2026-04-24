//go:build !darwin && !linux && !windows

package commands

import "github.com/spf13/cobra"

func daemonInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install the daemon as a system service",
		RunE:  func(_ *cobra.Command, _ []string) error { return unsupportedPlatform() },
	}
}

func daemonUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the daemon system service",
		RunE:  func(_ *cobra.Command, _ []string) error { return unsupportedPlatform() },
	}
}

func daemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		RunE:  func(_ *cobra.Command, _ []string) error { return unsupportedPlatform() },
	}
}

func daemonRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon",
		RunE:  func(_ *cobra.Command, _ []string) error { return unsupportedPlatform() },
	}
}

// RestartDaemon is a no-op on unsupported platforms.
func RestartDaemon() error { return nil }

// StopMenu is a no-op on unsupported platforms.
func StopMenu() error { return nil }

// StartMenu is a no-op on unsupported platforms.
func StartMenu() error { return nil }

// RestartMenu is a no-op on unsupported platforms.
func RestartMenu() error { return nil }

func daemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon",
		RunE:  func(_ *cobra.Command, _ []string) error { return unsupportedPlatform() },
	}
}
