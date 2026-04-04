package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
)

const launchdLabel = "ai.sky10.daemon"

var plistTemplate = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.Binary}}</string>
        <string>serve</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/sky10/daemon.stdout.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/sky10/daemon.stderr.log</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>{{.Home}}/.bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
    </dict>
</dict>
</plist>
`))

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
}

func findBinary() string {
	if p, err := exec.LookPath("sky10"); err == nil {
		abs, _ := filepath.Abs(p)
		return abs
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".bin", "sky10")
}

func launchdTarget() string {
	return fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel)
}

func daemonInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install and start the daemon as a launchd agent",
		RunE: func(_ *cobra.Command, _ []string) error {
			path := plistPath()
			os.MkdirAll(filepath.Dir(path), 0755)

			f, err := os.Create(path)
			if err != nil {
				return fmt.Errorf("creating plist: %w", err)
			}

			home, _ := os.UserHomeDir()
			data := struct {
				Label  string
				Binary string
				Home   string
			}{
				Label:  launchdLabel,
				Binary: findBinary(),
				Home:   home,
			}
			if err := plistTemplate.Execute(f, data); err != nil {
				f.Close()
				return fmt.Errorf("writing plist: %w", err)
			}
			f.Close()

			// Unload first in case it's already loaded (ignore error).
			exec.Command("launchctl", "bootout", launchdTarget()).Run()

			out, err := exec.Command("launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), path).CombinedOutput()
			if err != nil {
				return fmt.Errorf("launchctl bootstrap: %s (%w)", strings.TrimSpace(string(out)), err)
			}

			fmt.Printf("Installed: %s\n", path)
			fmt.Println("Daemon will start now and on every login.")
			fmt.Println("Run 'sky10 ui open' to open the web UI.")
			return nil
		},
	}
}

func daemonUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the daemon launchd agent",
		RunE: func(_ *cobra.Command, _ []string) error {
			exec.Command("launchctl", "bootout", launchdTarget()).Run()

			path := plistPath()
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing plist: %w", err)
			}

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
			out, err := exec.Command("launchctl", "print", launchdTarget()).CombinedOutput()
			if err != nil {
				fmt.Println("Not installed (launchd agent not found)")
				return nil
			}

			for _, line := range strings.Split(string(out), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "state =") ||
					strings.HasPrefix(line, "pid =") ||
					strings.HasPrefix(line, "last exit code =") {
					fmt.Println(line)
				}
			}
			return nil
		},
	}
}

func daemonRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			out, err := exec.Command("launchctl", "kickstart", "-k", launchdTarget()).CombinedOutput()
			if err != nil {
				return fmt.Errorf("launchctl kickstart: %s (%w)", strings.TrimSpace(string(out)), err)
			}

			fmt.Println("Daemon restarted.")
			return nil
		},
	}
}

func daemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon (will restart on next login)",
		RunE: func(_ *cobra.Command, _ []string) error {
			out, err := exec.Command("launchctl", "kill", "SIGTERM", launchdTarget()).CombinedOutput()
			if err != nil {
				return fmt.Errorf("launchctl kill: %s (%w)", strings.TrimSpace(string(out)), err)
			}

			fmt.Println("Daemon stopped.")
			return nil
		},
	}
}
