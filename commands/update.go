package commands

import (
	"fmt"

	"github.com/sky10/sky10/pkg/update"
	"github.com/spf13/cobra"
)

// Version is the raw version string (e.g. "v0.3.2"), set by main.
var Version string

// UpdateCmd returns the `sky10 update` command (aliased as `upgrade`).
func UpdateCmd() *cobra.Command {
	var checkOnly bool
	cmd := &cobra.Command{
		Use:     "update",
		Aliases: []string{"upgrade"},
		Short:   "Update sky10 to the latest version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("current: %s\n", Version)

			info, err := update.Check(Version)
			if err != nil {
				return err
			}

			if !info.Available {
				fmt.Println("already up to date")
				return nil
			}

			fmt.Printf("update available: %s -> %s\n", info.Current, info.Latest)

			if checkOnly {
				return nil
			}

			fmt.Println("downloading...")
			if err := update.Apply(info, nil); err != nil {
				return err
			}

			if err := update.ApplyMenu(info); err != nil {
				fmt.Printf("warning: could not update sky10-menu: %v\n", err)
			} else if info.MenuAssetURL != "" {
				fmt.Println("sky10-menu updated")
			}

			fmt.Printf("updated to %s\nrestart the daemon to use the new version\n", info.Latest)
			return nil
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "Check for updates without installing")
	return cmd
}
