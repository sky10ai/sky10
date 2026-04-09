package commands

import "github.com/spf13/cobra"

// SandboxCmd returns the top-level `sky10 sandbox` command group.
func SandboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Sandboxed agent environments",
	}
	cmd.AddCommand(sandboxCreateCmd())
	return cmd
}
