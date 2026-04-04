package commands

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// InviteCmd returns the top-level `sky10 invite` command.
func InviteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "invite",
		Short: "Generate an invite code for another device to join",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("skyfs.invite", nil)
			if err != nil {
				return err
			}
			var r struct{ Code string }
			if err := json.Unmarshal(result, &r); err != nil {
				return err
			}
			fmt.Println("\nShare this invite code with the other device:")
			fmt.Println(r.Code)
			fmt.Println("\nThe other device runs: sky10 join <code>")
			return nil
		},
	}
}
