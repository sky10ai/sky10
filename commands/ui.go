package commands

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

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

			// http_addr comes as "[::]:9101" or ":9101" — extract port.
			port := health.HTTPAddr
			if i := strings.LastIndex(port, ":"); i >= 0 {
				port = port[i:]
			}

			url := "http://localhost" + port
			fmt.Println(url)
			return openBrowser(url)
		},
	}
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	default:
		return fmt.Errorf("unsupported platform %s — open %s manually", runtime.GOOS, url)
	}
}
