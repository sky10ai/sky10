package commands

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// IdCmd returns the `sky10 id` command group.
func IdCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "id",
		Short: "Identity management",
	}
	cmd.AddCommand(idShowCmd())
	cmd.AddCommand(idDevicesCmd())
	return cmd
}

func idShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show identity and device info",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("identity.show", nil)
			if err != nil {
				return err
			}
			var info struct {
				Address       string `json:"address"`
				DeviceAddress string `json:"device_address"`
				DeviceCount   int    `json:"device_count"`
			}
			if err := json.Unmarshal(result, &info); err != nil {
				return err
			}
			fmt.Printf("identity:  %s\n", info.Address)
			fmt.Printf("device:    %s\n", info.DeviceAddress)
			fmt.Printf("devices:   %d\n", info.DeviceCount)
			return nil
		},
	}
}

func idDevicesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "devices",
		Short: "List authorized devices",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("identity.devices", nil)
			if err != nil {
				return err
			}
			var info struct {
				Identity string `json:"identity"`
				Devices  []struct {
					Name    string `json:"name"`
					AddedAt string `json:"added_at"`
					Current bool   `json:"current"`
				} `json:"devices"`
			}
			if err := json.Unmarshal(result, &info); err != nil {
				return err
			}
			fmt.Printf("Identity: %s\n\n", info.Identity)
			for _, d := range info.Devices {
				marker := "  "
				if d.Current {
					marker = "* "
				}
				fmt.Printf("%s%s (added %s)\n", marker, d.Name, d.AddedAt[:10])
			}
			return nil
		},
	}
}
