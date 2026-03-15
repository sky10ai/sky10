// Command sky10 is the unified CLI for the sky10 ecosystem.
package main

import (
	"os"

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

	root.AddCommand(keyCmd())
	root.AddCommand(fsCmd())

	root.CompletionOptions.HiddenDefaultCmd = true

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
