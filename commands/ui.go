package commands

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
)

// UiCmd returns the "ui" command tree.
func UiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Web UI commands",
	}
	cmd.AddCommand(uiOpenCmd())
	return cmd
}

func uiOpenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "open",
		Short: "Open the web UI in the default browser",
		RunE: func(_ *cobra.Command, _ []string) error {
			raw, err := rpcCall("skyfs.health", nil)
			if err != nil {
				return err
			}

			var health struct {
				HTTPAddr string `json:"http_addr"`
			}
			if err := json.Unmarshal(raw, &health); err != nil {
				return fmt.Errorf("parsing health response: %w", err)
			}
			if health.HTTPAddr == "" {
				return fmt.Errorf("daemon HTTP server is not running (start with 'sky10 serve')")
			}

			url, err := localhostHTTPURL(health.HTTPAddr)
			if err != nil {
				return err
			}

			fmt.Println(url)
			return openBrowser(url)
		},
	}
}

func openBrowser(url string) error {
	name, args, err := browserCommand(runtime.GOOS, url)
	if err != nil {
		return err
	}
	return exec.Command(name, args...).Start()
}

func browserCommand(goos, url string) (string, []string, error) {
	switch goos {
	case "darwin":
		return "open", []string{url}, nil
	case "linux":
		return "xdg-open", []string{url}, nil
	case "windows":
		return "cmd", []string{"/c", "start", "", url}, nil
	default:
		return "", nil, fmt.Errorf("unsupported platform %s — open %s manually", goos, url)
	}
}
