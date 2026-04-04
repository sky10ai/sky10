package commands

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"

	"github.com/spf13/cobra"
)

const systemdUnit = "sky10"

func systemdUnitContent() string {
	binary, err := exec.LookPath("sky10")
	if err != nil {
		home, _ := os.UserHomeDir()
		binary = home + "/.bin/sky10"
	}

	u, _ := user.Current()
	username := "root"
	home := "/root"
	if u != nil {
		username = u.Username
		home = u.HomeDir
	}

	return fmt.Sprintf(`[Unit]
Description=sky10 daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s serve
Restart=on-failure
RestartSec=5
User=%s
Environment=HOME=%s
StandardOutput=append:/tmp/sky10/daemon.log
StandardError=append:/tmp/sky10/daemon.log

[Install]
WantedBy=multi-user.target
`, binary, username, home)
}

func sudo(args ...string) (string, error) {
	if os.Getuid() == 0 {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	out, err := exec.Command("sudo", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func daemonInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install and start the daemon as a systemd service",
		RunE: func(_ *cobra.Command, _ []string) error {
			if _, err := exec.LookPath("systemctl"); err != nil {
				return fmt.Errorf("systemctl not found — systemd is required on Linux")
			}

			os.MkdirAll("/tmp/sky10", 0755)

			unitPath := "/etc/systemd/system/" + systemdUnit + ".service"
			tmp, err := os.CreateTemp("", "sky10-unit-*")
			if err != nil {
				return err
			}
			tmp.WriteString(systemdUnitContent())
			tmp.Close()

			if out, err := sudo("cp", tmp.Name(), unitPath); err != nil {
				os.Remove(tmp.Name())
				return fmt.Errorf("writing unit file: %s (%w)", out, err)
			}
			os.Remove(tmp.Name())

			if out, err := sudo("systemctl", "daemon-reload"); err != nil {
				return fmt.Errorf("daemon-reload: %s (%w)", out, err)
			}
			if out, err := sudo("systemctl", "enable", systemdUnit); err != nil {
				return fmt.Errorf("enable: %s (%w)", out, err)
			}
			if out, err := sudo("systemctl", "restart", systemdUnit); err != nil {
				return fmt.Errorf("restart: %s (%w)", out, err)
			}

			fmt.Printf("Installed: %s\n", unitPath)
			fmt.Println("Daemon started and enabled on boot.")
			fmt.Println("Run 'sky10 ui open' to open the web UI.")
			return nil
		},
	}
}

func daemonUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the daemon systemd service",
		RunE: func(_ *cobra.Command, _ []string) error {
			sudo("systemctl", "stop", systemdUnit)
			sudo("systemctl", "disable", systemdUnit)

			unitPath := "/etc/systemd/system/" + systemdUnit + ".service"
			sudo("rm", "-f", unitPath)
			sudo("systemctl", "daemon-reload")

			fmt.Println("Daemon uninstalled.")
			return nil
		},
	}
}

func daemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		RunE: func(_ *cobra.Command, _ []string) error {
			out, err := exec.Command("systemctl", "status", systemdUnit, "--no-pager", "-l").CombinedOutput()
			if err != nil && len(out) == 0 {
				fmt.Println("Not installed (systemd unit not found)")
				return nil
			}
			fmt.Print(string(out))
			return nil
		},
	}
}

func daemonRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			out, err := sudo("systemctl", "restart", systemdUnit)
			if err != nil {
				return fmt.Errorf("systemctl restart: %s (%w)", out, err)
			}

			fmt.Println("Daemon restarted.")
			return nil
		},
	}
}

func daemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			out, err := sudo("systemctl", "stop", systemdUnit)
			if err != nil {
				return fmt.Errorf("systemctl stop: %s (%w)", out, err)
			}

			fmt.Println("Daemon stopped.")
			return nil
		},
	}
}
