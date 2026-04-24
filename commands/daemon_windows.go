//go:build windows

package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	skyfs "github.com/sky10/sky10/pkg/fs"
	"golang.org/x/sys/windows/registry"

	"github.com/spf13/cobra"
)

const windowsRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func windowsInstallDir() string {
	if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
		return filepath.Join(local, "sky10", "bin")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "AppData", "Local", "sky10", "bin")
}

func findWindowsBinary() string {
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			return resolved
		}
		return exe
	}
	if p, err := exec.LookPath("sky10.exe"); err == nil {
		return p
	}
	if p, err := exec.LookPath("sky10"); err == nil {
		return p
	}
	return filepath.Join(windowsInstallDir(), "sky10.exe")
}

func findMenuBinaryWindows() string {
	if p, err := exec.LookPath("sky10-menu.exe"); err == nil {
		return p
	}
	if p, err := exec.LookPath("sky10-menu"); err == nil {
		return p
	}
	return filepath.Join(windowsInstallDir(), "sky10-menu.exe")
}

func windowsCommandLine(path string, args ...string) string {
	parts := []string{windowsQuoteArg(path)}
	for _, arg := range args {
		parts = append(parts, windowsQuoteArg(arg))
	}
	return strings.Join(parts, " ")
}

func windowsQuoteArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\"") {
		return arg
	}
	return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
}

func setWindowsRunValue(name, command string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, windowsRunKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("opening Windows Run key: %w", err)
	}
	defer key.Close()
	if err := key.SetStringValue(name, command); err != nil {
		return fmt.Errorf("setting Windows Run value %q: %w", name, err)
	}
	return nil
}

func deleteWindowsRunValue(name string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, windowsRunKey, registry.SET_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return fmt.Errorf("opening Windows Run key: %w", err)
	}
	defer key.Close()
	if err := key.DeleteValue(name); err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("deleting Windows Run value %q: %w", name, err)
	}
	return nil
}

func startWindowsProcess(path string, args ...string) error {
	cmd := exec.Command(path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}

func daemonInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install and start the daemon for this Windows user",
		RunE: func(_ *cobra.Command, _ []string) error {
			binary := findWindowsBinary()
			if err := setWindowsRunValue("sky10-daemon", windowsCommandLine(binary, "serve")); err != nil {
				return err
			}
			if err := RestartDaemon(); err != nil {
				return err
			}

			menuBin := findMenuBinaryWindows()
			if _, err := os.Stat(menuBin); err == nil {
				if err := setWindowsRunValue("sky10-menu", windowsCommandLine(menuBin)); err != nil {
					return err
				}
				if err := StartMenu(); err != nil {
					return err
				}
			}

			fmt.Println("Daemon will start now and on every login.")
			if _, err := os.Stat(menuBin); err == nil {
				fmt.Println("Menu app will start now and on every login.")
			}
			fmt.Println("Run 'sky10 ui open' to open the web UI.")
			return nil
		},
	}
}

func daemonUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the daemon from Windows user startup",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := deleteWindowsRunValue("sky10-daemon"); err != nil {
				return err
			}
			if err := deleteWindowsRunValue("sky10-menu"); err != nil {
				return err
			}
			_ = StopMenu()
			_ = skyfs.KillExistingDaemon()
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
			client := &http.Client{Timeout: 2 * time.Second}
			resp, err := client.Get("http://127.0.0.1:9101/health")
			if err != nil {
				fmt.Println("Not running (HTTP health check failed)")
				return nil
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				fmt.Printf("Not healthy (HTTP status %d)\n", resp.StatusCode)
				return nil
			}
			var health struct {
				Version string `json:"version"`
				Status  string `json:"status"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
				return fmt.Errorf("parsing health response: %w", err)
			}
			fmt.Printf("Running (%s", health.Status)
			if health.Version != "" {
				fmt.Printf(", version %s", health.Version)
			}
			fmt.Println(")")
			return nil
		},
	}
}

func daemonRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := RestartDaemon(); err != nil {
				return err
			}
			fmt.Println("Daemon restarted.")
			return nil
		},
	}
}

// RestartDaemon restarts the Windows per-user daemon process.
func RestartDaemon() error {
	_ = skyfs.KillExistingDaemon()
	binary := findWindowsBinary()
	if _, err := os.Stat(binary); err != nil {
		return fmt.Errorf("finding sky10 binary %q: %w", binary, err)
	}
	return startWindowsProcess(binary, "serve")
}

// StopMenu kills every running sky10-menu process for this user.
func StopMenu() error {
	out, err := exec.Command("taskkill", "/IM", "sky10-menu.exe", "/F").CombinedOutput()
	if err == nil {
		return nil
	}
	text := strings.ToLower(string(out))
	if strings.Contains(text, "not found") || strings.Contains(text, "not running") {
		return nil
	}
	return fmt.Errorf("taskkill sky10-menu.exe: %s (%w)", strings.TrimSpace(string(out)), err)
}

// StartMenu starts the sky10-menu process from the user install path if it exists.
func StartMenu() error {
	menuBin := findMenuBinaryWindows()
	if _, err := os.Stat(menuBin); os.IsNotExist(err) {
		return nil
	}
	return startWindowsProcess(menuBin)
}

// RestartMenu kills and restarts the sky10-menu process if it exists.
func RestartMenu() error {
	if err := StopMenu(); err != nil {
		return err
	}
	return StartMenu()
}

func daemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			_ = skyfs.KillExistingDaemon()
			fmt.Println("Daemon stopped.")
			return nil
		},
	}
}
