// Command sky10 is the unified CLI for the sky10 ecosystem.
package main

import (
	"os"

	"github.com/sky10/sky10/commands"
	"github.com/spf13/cobra"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	root := &cobra.Command{
		Use:     "sky10",
		Short:   "Encrypted storage & agent coordination",
		Version: version + " (" + commit + ") built " + buildDate,
	}

	root.AddCommand(commands.ServeCmd())
	root.AddCommand(commands.KeyCmd())
	root.AddCommand(commands.FsCmd())
	root.AddCommand(commands.KvCmd())

	root.CompletionOptions.HiddenDefaultCmd = true

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
