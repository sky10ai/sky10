package commands

import (
	"fmt"

	skyapps "github.com/sky10/sky10/pkg/apps"
	"github.com/spf13/cobra"
)

var (
	managedAppsList        = skyapps.List
	managedAppsLookup      = skyapps.Lookup
	managedAppsStatus      = skyapps.StatusFor
	managedAppsCheckLatest = skyapps.CheckLatest
	managedAppsUpgrade     = skyapps.Upgrade
	managedAppsUninstall   = skyapps.Uninstall
)

// AppsCmd returns the `sky10 apps` command group.
func AppsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apps",
		Short: "Manage optional helper apps",
	}
	cmd.AddCommand(appsListCmd())
	cmd.AddCommand(appsStatusCmd())
	cmd.AddCommand(appsInstallCmd())
	cmd.AddCommand(appsUpgradeCmd())
	cmd.AddCommand(appsUninstallCmd())
	return cmd
}

func appsListCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List known managed apps",
		RunE: func(_ *cobra.Command, _ []string) error {
			items := managedAppsList()
			if jsonOut {
				return printJSON(items)
			}
			for _, item := range items {
				fmt.Printf("%s\t%s\n", item.ID, item.Name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print machine-readable JSON")
	return cmd
}

func appsStatusCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status <app>",
		Short: "Show the status of a managed app",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			app, err := managedAppsLookup(args[0])
			if err != nil {
				return err
			}
			status, err := managedAppsStatus(app.ID)
			if err != nil {
				return err
			}
			release, err := managedAppsCheckLatest(app.ID)
			if err != nil {
				return err
			}

			view := struct {
				ID          skyapps.ID `json:"id"`
				Name        string     `json:"name"`
				Installed   bool       `json:"installed"`
				Managed     bool       `json:"managed"`
				ManagedPath string     `json:"managed_path,omitempty"`
				ActivePath  string     `json:"active_path,omitempty"`
				Version     string     `json:"version,omitempty"`
				Latest      string     `json:"latest,omitempty"`
				Available   bool       `json:"available"`
			}{
				ID:          status.ID,
				Name:        status.Name,
				Installed:   status.Installed,
				Managed:     status.Managed,
				ManagedPath: status.ManagedPath,
				ActivePath:  status.ActivePath,
				Version:     status.Version,
				Latest:      release.Latest,
				Available:   release.Available,
			}

			if jsonOut {
				return printJSON(view)
			}

			fmt.Printf("app:           %s\n", view.ID)
			fmt.Printf("name:          %s\n", view.Name)
			fmt.Printf("installed:     %t\n", view.Installed)
			fmt.Printf("managed:       %t\n", view.Managed)
			fmt.Printf("managed path:  %s\n", valueOrDash(view.ManagedPath))
			fmt.Printf("active path:   %s\n", valueOrDash(view.ActivePath))
			fmt.Printf("version:       %s\n", valueOrDash(view.Version))
			fmt.Printf("latest:        %s\n", valueOrDash(view.Latest))
			fmt.Printf("available:     %t\n", view.Available)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print machine-readable JSON")
	return cmd
}

func appsInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install <app>",
		Short: "Install a managed app",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runAppsUpgrade(args[0], "install")
		},
	}
}

func appsUpgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "upgrade <app>",
		Aliases: []string{"update"},
		Short:   "Upgrade a managed app to the latest release",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runAppsUpgrade(args[0], "upgrade")
		},
	}
}

func appsUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall <app>",
		Short: "Remove the managed binary for an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			app, err := managedAppsLookup(args[0])
			if err != nil {
				return err
			}
			result, err := managedAppsUninstall(app.ID)
			if err != nil {
				return err
			}
			if result.Removed {
				fmt.Printf("removed %s from %s\n", app.ID, result.Path)
				return nil
			}
			fmt.Printf("no managed %s binary at %s\n", app.ID, result.Path)
			return nil
		},
	}
}

func runAppsUpgrade(rawID, verb string) error {
	app, err := managedAppsLookup(rawID)
	if err != nil {
		return err
	}
	info, err := managedAppsUpgrade(app.ID, nil)
	if err != nil {
		return err
	}
	if !info.Available {
		fmt.Printf("%s already up to date (%s)\n", app.ID, valueOrDash(info.Current))
		return nil
	}
	switch verb {
	case "install":
		fmt.Printf("installed %s %s\n", app.ID, info.Latest)
	default:
		fmt.Printf("upgraded %s to %s\n", app.ID, info.Latest)
	}
	return nil
}

func valueOrDash(v string) string {
	if v == "" {
		return "-"
	}
	return v
}
