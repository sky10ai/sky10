// Command sky10 is the unified CLI for the sky10 ecosystem.
package main

import (
	"embed"
	"os"

	"github.com/sky10/sky10/commands"
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

	root := &cobra.Command{
		Use:     "sky10",
		Short:   "Encrypted storage & agent coordination",
		Version: version + " (" + commit + ") built " + buildDate,
	}

	root.AddCommand(commands.ServeCmd())
	root.AddCommand(commands.KeyCmd())
	root.AddCommand(commands.FsCmd())
	root.AddCommand(commands.KvCmd())
	root.AddCommand(commands.LinkCmd())
	root.AddCommand(commands.UiCmd())

	root.CompletionOptions.HiddenDefaultCmd = true

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
