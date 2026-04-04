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

const (
	launchdLabel     = "ai.sky10.daemon"
	launchdMenuLabel = "ai.sky10.menu"
)

var plistTemplate = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.Binary}}</string>{{range .Args}}
        <string>{{.}}</string>{{end}}
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>{{.LogOut}}</string>
    <key>StandardErrorPath</key>
    <string>{{.LogErr}}</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>{{.Home}}/.bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
    </dict>
</dict>
</plist>
`))

type plistData struct {
	Label  string
	Binary string
	Args   []string
	Home   string
	LogOut string
	LogErr string
}

func launchdPlistPath(label string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist")
}

func plistPath() string {
	return launchdPlistPath(launchdLabel)
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

func launchdMenuTarget() string {
	return fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdMenuLabel)
}

func findMenuBinary() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".bin", "sky10-menu")
}

func installMenuAgent() {
	menuBin := findMenuBinary()
	if _, err := os.Stat(menuBin); err != nil {
		return
	}

	home, _ := os.UserHomeDir()
	path := launchdPlistPath(launchdMenuLabel)
	os.MkdirAll(filepath.Dir(path), 0755)

	f, err := os.Create(path)
	if err != nil {
		return
	}
	data := plistData{
		Label:  launchdMenuLabel,
		Binary: menuBin,
		Home:   home,
		LogOut: "/tmp/sky10/menu.stdout.log",
		LogErr: "/tmp/sky10/menu.stderr.log",
	}
	if err := plistTemplate.Execute(f, data); err != nil {
		f.Close()
		return
	}
	f.Close()

	exec.Command("launchctl", "bootout", launchdMenuTarget()).Run()
	gui := fmt.Sprintf("gui/%d", os.Getuid())
	if out, err := exec.Command("launchctl", "bootstrap", gui, path).CombinedOutput(); err != nil {
		fmt.Printf("Warning: could not start sky10-menu: %s\n", strings.TrimSpace(string(out)))
		return
	}
	fmt.Printf("Installed: %s\n", path)
	fmt.Println("Menu bar app will start now and on every login.")
}

func uninstallMenuAgent() {
	exec.Command("launchctl", "bootout", launchdMenuTarget()).Run()
	path := launchdPlistPath(launchdMenuLabel)
	os.Remove(path)
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
			data := plistData{
				Label:  launchdLabel,
				Binary: findBinary(),
				Args:   []string{"serve"},
				Home:   home,
				LogOut: "/tmp/sky10/daemon.stdout.log",
				LogErr: "/tmp/sky10/daemon.stderr.log",
			}
			if err := plistTemplate.Execute(f, data); err != nil {
				f.Close()
				return fmt.Errorf("writing plist: %w", err)
			}
			f.Close()

			// Unload first in case it's already loaded (ignore error).
			exec.Command("launchctl", "bootout", launchdTarget()).Run()

			gui := fmt.Sprintf("gui/%d", os.Getuid())
			out, err := exec.Command("launchctl", "bootstrap", gui, path).CombinedOutput()
			if err != nil {
				return fmt.Errorf("launchctl bootstrap: %s (%w)", strings.TrimSpace(string(out)), err)
			}

			fmt.Printf("Installed: %s\n", path)
			fmt.Println("Daemon will start now and on every login.")
			fmt.Println("Run 'sky10 ui open' to open the web UI.")

			installMenuAgent()
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

			uninstallMenuAgent()

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
