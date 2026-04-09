// Command sky10 is the unified CLI for the sky10 ecosystem.
package main

import (
	"embed"
	"os"

	"github.com/sky10/sky10/commands"
	"github.com/sky10/sky10/pkg/config"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
	"github.com/spf13/cobra"
)

//go:embed all:web/dist
var webDist embed.FS

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	skyrpc.WebDist = webDist
	commands.Version = version
	var homeOverride string

	root := &cobra.Command{
		Use:     "sky10",
		Short:   "Encrypted storage & agent coordination",
		Version: version + " (" + commit + ") built " + buildDate,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if homeOverride != "" {
				return os.Setenv(config.EnvHome, homeOverride)
			}
			return nil
		},
	}

	root.AddCommand(commands.ServeCmd())
	root.AddCommand(commands.InviteCmd())
	root.AddCommand(commands.JoinCmd())
	root.AddCommand(commands.KeyCmd())
	root.AddCommand(commands.FsCmd())
	root.AddCommand(commands.KvCmd())
	root.AddCommand(commands.LinkCmd())
	root.AddCommand(commands.IdCmd())
	root.AddCommand(commands.UiCmd())
	root.AddCommand(commands.AppsCmd())
	root.AddCommand(commands.UpdateCmd())
	root.AddCommand(commands.DaemonCmd())

	root.CompletionOptions.HiddenDefaultCmd = true
	root.PersistentFlags().StringVar(&homeOverride, "home", "", "Root directory for local sky10 state")

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
