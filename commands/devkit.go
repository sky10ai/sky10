package commands

import (
	"fmt"

	skydevkit "github.com/sky10/sky10/pkg/devkit"
	"github.com/spf13/cobra"
)

var (
	managedDevkitList        = skydevkit.List
	managedDevkitLookup      = skydevkit.Lookup
	managedDevkitStatus      = skydevkit.StatusFor
	managedDevkitCheckLatest = skydevkit.CheckLatest
	managedDevkitUpgrade     = skydevkit.Upgrade
	managedDevkitUninstall   = skydevkit.Uninstall
)

// DevkitCmd returns the `sky10 devkit` command group.
func DevkitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "devkit",
		Short: "Manage repair and build toolchains",
	}
	cmd.AddCommand(devkitListCmd())
	cmd.AddCommand(devkitStatusCmd())
	cmd.AddCommand(devkitInstallCmd())
	cmd.AddCommand(devkitUpgradeCmd())
	cmd.AddCommand(devkitUninstallCmd())
	return cmd
}

func devkitListCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List known managed devkit tools",
		RunE: func(_ *cobra.Command, _ []string) error {
			items := managedDevkitList()
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

func devkitStatusCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status <tool>",
		Short: "Show the status of a managed devkit tool",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			tool, err := managedDevkitLookup(args[0])
			if err != nil {
				return err
			}
			status, err := managedDevkitStatus(tool.ID)
			if err != nil {
				return err
			}
			release, err := managedDevkitCheckLatest(tool.ID)
			if err != nil {
				return err
			}
			view := struct {
				ID          skydevkit.ID `json:"id"`
				Name        string       `json:"name"`
				Installed   bool         `json:"installed"`
				Managed     bool         `json:"managed"`
				ManagedPath string       `json:"managed_path,omitempty"`
				ActivePath  string       `json:"active_path,omitempty"`
				Version     string       `json:"version,omitempty"`
				Latest      string       `json:"latest,omitempty"`
				Available   bool         `json:"available"`
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
			fmt.Printf("tool:          %s\n", view.ID)
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

func devkitInstallCmd() *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:   "install <tool>",
		Short: "Install a managed devkit tool",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runDevkitUpgrade(args[0], "install", version)
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "Install a specific version")
	return cmd
}

func devkitUpgradeCmd() *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:     "upgrade <tool>",
		Aliases: []string{"update"},
		Short:   "Upgrade a managed devkit tool",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runDevkitUpgrade(args[0], "upgrade", version)
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "Install a specific version")
	return cmd
}

func devkitUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall <tool>",
		Short: "Remove a managed devkit tool",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			tool, err := managedDevkitLookup(args[0])
			if err != nil {
				return err
			}
			result, err := managedDevkitUninstall(tool.ID)
			if err != nil {
				return err
			}
			if result.Removed {
				fmt.Printf("removed %s from %s\n", tool.ID, result.Path)
				return nil
			}
			fmt.Printf("no managed %s tool at %s\n", tool.ID, result.Path)
			return nil
		},
	}
}

func runDevkitUpgrade(rawID, verb, version string) error {
	tool, err := managedDevkitLookup(rawID)
	if err != nil {
		return err
	}
	info, err := managedDevkitUpgrade(tool.ID, version, nil)
	if err != nil {
		return err
	}
	if !info.Available {
		fmt.Printf("%s already up to date (%s)\n", tool.ID, valueOrDash(info.Current))
		return nil
	}
	switch verb {
	case "install":
		fmt.Printf("installed %s %s\n", tool.ID, info.Latest)
	default:
		fmt.Printf("upgraded %s to %s\n", tool.ID, info.Latest)
	}
	return nil
}
