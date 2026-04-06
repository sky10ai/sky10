package commands

import (
	"fmt"

	"github.com/sky10/sky10/pkg/update"
	"github.com/spf13/cobra"
)

// Version is the raw version string (e.g. "v0.3.2"), set by main.
var Version string

var (
	updateCheck         = update.Check
	updateApply         = func(info *update.Info) error { return update.Apply(info, nil) }
	updateApplyMenu     = update.ApplyMenu
	updateStopMenu      = StopMenu
	updateStartMenu     = StartMenu
	updateRestartDaemon = RestartDaemon
)

// UpdateCmd returns the `sky10 update` command (aliased as `upgrade`).
func UpdateCmd() *cobra.Command {
	var checkOnly bool
	cmd := &cobra.Command{
		Use:     "update",
		Aliases: []string{"upgrade"},
		Short:   "Update sky10 to the latest version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("current: %s\n", Version)

			info, err := updateCheck(Version)
			if err != nil {
				return err
			}

			if !info.Available {
				// CLI is current, but the menu binary may still
				// need updating (e.g. menu assets arrived after
				// the CLI was already updated).
				menuUpdated, err := updateApplyMenu(info)
				if err != nil {
					fmt.Printf("warning: could not update sky10-menu: %v\n", err)
				}
				if menuUpdated {
					fmt.Println("sky10-menu updated")
					if err := updateStopMenu(); err != nil {
						fmt.Printf("warning: could not stop sky10-menu: %v\n", err)
					}
					if err := updateStartMenu(); err != nil {
						fmt.Printf("warning: could not start sky10-menu: %v\n", err)
					}
				} else {
					fmt.Println("already up to date")
				}
				return nil
			}

			fmt.Printf("update available: %s -> %s\n", info.Current, info.Latest)

			if checkOnly {
				return nil
			}

			fmt.Println("downloading...")
			if err := updateApply(info); err != nil {
				return err
			}

			menuUpdated, err := updateApplyMenu(info)
			if err != nil {
				fmt.Printf("warning: could not update sky10-menu: %v\n", err)
			} else if menuUpdated {
				fmt.Println("sky10-menu updated")
			}

			if err := updateStopMenu(); err != nil {
				fmt.Printf("warning: could not stop sky10-menu: %v\n", err)
			}

			if err := updateRestartDaemon(); err != nil {
				fmt.Printf("warning: could not restart daemon: %v\n", err)
				fmt.Println("restart the daemon manually to use the new version")
			} else {
				fmt.Println("daemon restarted")
				if err := updateStartMenu(); err != nil {
					fmt.Printf("warning: could not start sky10-menu: %v\n", err)
				}
			}

			fmt.Printf("updated to %s\n", info.Latest)
			return nil
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "Check for updates without installing")
	return cmd
}
