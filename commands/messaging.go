package commands

import (
	"context"
	"os"

	messagingadapters "github.com/sky10/sky10/pkg/messengers/adapters"
	"github.com/spf13/cobra"
)

// MessagingCmd returns the hidden adapter self-exec command surface used for
// built-in messaging adapters such as `sky10 messaging imap-smtp`.
func MessagingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "messaging",
		Short:  "Run internal messaging adapter commands",
		Hidden: true,
	}
	for _, definition := range messagingadapters.Builtins() {
		cmd.AddCommand(messagingAdapterCmd(definition))
	}
	return cmd
}

func messagingAdapterCmd(definition messagingadapters.Definition) *cobra.Command {
	return &cobra.Command{
		Use:           definition.Name,
		Short:         definition.Summary,
		Hidden:        true,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return definition.Serve(ctx, os.Stdin, os.Stdout, os.Stderr)
		},
	}
}
