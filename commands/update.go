package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sky10/sky10/pkg/update"
	"github.com/spf13/cobra"
)

// Version is the raw version string (e.g. "v0.3.2"), set by main.
var Version string

var (
	updateCheck         = update.CheckExplicit
	updateDownload      = func(info *update.Info) (*update.StagedRelease, error) { return update.Stage(info, nil) }
	updateStatus        = update.Status
	updateInstall       = update.InstallStaged
	updateStopMenu      = StopMenu
	updateStartMenu     = StartMenu
	updateRestartDaemon = RestartDaemon
	updateWaitHTTPReady = waitForDaemonHTTPReady
)

// UpdateCmd returns the `sky10 update` command (aliased as `upgrade`).
func UpdateCmd() *cobra.Command {
	var checkOnly bool

	cmd := &cobra.Command{
		Use:     "update",
		Aliases: []string{"upgrade"},
		Short:   "Check, download, and install sky10 updates",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if checkOnly {
				return runUpdateCheck(false)
			}
			return runUpdateApply()
		},
	}

	cmd.Flags().BoolVar(&checkOnly, "check", false, "Check for updates without installing")
	cmd.AddCommand(updateCheckCmd())
	cmd.AddCommand(updateDownloadCmd())
	cmd.AddCommand(updateInstallCmd())
	cmd.AddCommand(updateStatusCmd())
	return cmd
}

func updateCheckCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check whether a new sky10 release is available",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runUpdateCheck(jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print machine-readable JSON")
	return cmd
}

func updateDownloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "download",
		Short: "Download and stage the latest sky10 release without installing it",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Printf("current: %s\n", Version)

			info, err := updateCheck(Version)
			if err != nil {
				return err
			}
			if !info.Available {
				fmt.Println("already up to date")
				return nil
			}

			fmt.Println(describeAvailable(info))
			fmt.Println("downloading...")

			staged, err := updateDownload(info)
			if err != nil {
				return err
			}
			if staged == nil {
				fmt.Println("already up to date")
				return nil
			}

			fmt.Printf("staged %s (%s)\n", staged.Latest, describeStagedComponents(staged.CLIStaged, staged.MenuStaged))
			return nil
		},
	}
}

func updateInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install the staged sky10 release and restart affected processes",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runUpdateInstall()
		},
	}
}

func updateStatusCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show whether a downloaded sky10 update is staged locally",
		RunE: func(_ *cobra.Command, _ []string) error {
			status, err := updateStatus(Version)
			if err != nil {
				return err
			}
			if jsonOut {
				return printJSON(status)
			}
			fmt.Printf("current: %s\n", status.Current)
			if !status.Ready {
				fmt.Println("no staged update")
				return nil
			}
			fmt.Printf("staged: %s (%s)\n", status.Latest, describeStagedComponents(status.CLIStaged, status.MenuStaged))
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print machine-readable JSON")
	return cmd
}

func runUpdateCheck(jsonOut bool) error {
	info, err := updateCheck(Version)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(info)
	}

	fmt.Printf("current: %s\n", Version)
	if !info.Available {
		fmt.Println("already up to date")
		return nil
	}
	fmt.Println(describeAvailable(info))
	return nil
}

func runUpdateApply() error {
	fmt.Printf("current: %s\n", Version)

	status, err := updateStatus(Version)
	if err != nil {
		return err
	}
	if status.Ready {
		fmt.Printf("installing staged %s (%s)\n", status.Latest, describeStagedComponents(status.CLIStaged, status.MenuStaged))
		return runUpdateInstall()
	}

	info, err := updateCheck(Version)
	if err != nil {
		return err
	}
	if !info.Available {
		fmt.Println("already up to date")
		return nil
	}

	fmt.Println(describeAvailable(info))
	fmt.Println("downloading...")
	staged, err := updateDownload(info)
	if err != nil {
		return err
	}
	if staged == nil {
		fmt.Println("already up to date")
		return nil
	}
	fmt.Printf("staged %s (%s)\n", staged.Latest, describeStagedComponents(staged.CLIStaged, staged.MenuStaged))
	return runUpdateInstall()
}

func runUpdateInstall() (runErr error) {
	status, err := updateStatus(Version)
	if err != nil {
		return err
	}
	if !status.Ready {
		fmt.Println("no staged update")
		return nil
	}

	startMenuOnReturn := false
	if err := updateStopMenu(); err != nil {
		fmt.Printf("warning: could not stop sky10-menu: %v\n", err)
	} else {
		startMenuOnReturn = true
	}
	defer func() {
		if !startMenuOnReturn {
			return
		}
		if err := updateStartMenu(); err != nil {
			fmt.Printf("warning: could not start sky10-menu: %v\n", err)
		}
	}()

	staged, err := updateInstall()
	if err != nil {
		if errors.Is(err, update.ErrNoStagedUpdate) {
			fmt.Println("no staged update")
			return nil
		}
		return err
	}

	if staged.MenuStaged {
		fmt.Println("sky10-menu updated")
	}

	if staged.CLIStaged {
		if err := updateRestartDaemon(); err != nil {
			fmt.Printf("warning: could not restart daemon: %v\n", err)
			fmt.Println("restart the daemon manually to use the new version")
			startMenuOnReturn = false
		} else {
			fmt.Println("daemon restarted")
			if err := updateWaitHTTPReady(); err != nil {
				fmt.Printf("warning: daemon HTTP server is not ready yet: %v\n", err)
				fmt.Println("start sky10-menu manually once the daemon is healthy")
				startMenuOnReturn = false
			}
		}
	}

	fmt.Printf("updated to %s\n", staged.Latest)
	return nil
}

func describeAvailable(info *update.Info) string {
	switch {
	case info.CLIAvailable && info.MenuAvailable:
		return fmt.Sprintf("update available: %s -> %s (cli and menu)", info.Current, info.Latest)
	case info.CLIAvailable:
		return fmt.Sprintf("update available: %s -> %s", info.Current, info.Latest)
	case info.MenuAvailable:
		return fmt.Sprintf("update available: %s (menu only)", info.Latest)
	default:
		return "already up to date"
	}
}

func describeStagedComponents(cliStaged, menuStaged bool) string {
	switch {
	case cliStaged && menuStaged:
		return "cli and menu"
	case cliStaged:
		return "cli"
	case menuStaged:
		return "menu"
	default:
		return "nothing"
	}
}

func printJSON(v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func waitForDaemonHTTPReady() error {
	deadline := time.Now().Add(10 * time.Second)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	var lastErr error

	for time.Now().Before(deadline) {
		raw, err := rpcCall("skyfs.health", nil)
		if err != nil {
			lastErr = err
			time.Sleep(200 * time.Millisecond)
			continue
		}

		var health struct {
			HTTPAddr string `json:"http_addr"`
		}
		if err := json.Unmarshal(raw, &health); err != nil {
			lastErr = fmt.Errorf("parse health response: %w", err)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if health.HTTPAddr == "" {
			lastErr = fmt.Errorf("daemon reported no HTTP address")
			time.Sleep(200 * time.Millisecond)
			continue
		}

		baseURL, err := loopbackHTTPURL(health.HTTPAddr)
		if err != nil {
			lastErr = err
			time.Sleep(200 * time.Millisecond)
			continue
		}

		resp, err := client.Get(baseURL + "/health")
		if err == nil && resp != nil && resp.StatusCode == http.StatusOK {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return nil
		}
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("GET /health returned %d", resp.StatusCode)
		} else {
			lastErr = err
		}

		time.Sleep(200 * time.Millisecond)
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("timed out waiting for daemon HTTP server")
	}
	return lastErr
}
