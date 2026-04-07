package commands

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// KvCmd returns the `sky10 kv` command group.
func KvCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kv",
		Short: "Encrypted key-value store",
	}

	cmd.AddCommand(kvSetCmd())
	cmd.AddCommand(kvGetCmd())
	cmd.AddCommand(kvDeleteCmd())
	cmd.AddCommand(kvListCmd())
	cmd.AddCommand(kvSyncCmd())
	cmd.AddCommand(kvStatusCmd())

	return cmd
}

func kvSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a key-value pair",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := rpcCall("skykv.set", map[string]string{
				"key": args[0], "value": args[1],
			})
			if err != nil {
				return err
			}
			fmt.Printf("set %s\n", args[0])
			return nil
		},
	}
}

func kvGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a value by key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("skykv.get", map[string]string{"key": args[0]})
			if err != nil {
				return err
			}
			var r struct {
				Value string `json:"value"`
				Found bool   `json:"found"`
			}
			json.Unmarshal(result, &r)
			if !r.Found {
				return fmt.Errorf("key not found: %s", args[0])
			}
			fmt.Println(r.Value)
			return nil
		},
	}
}

func kvDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <key>",
		Short: "Delete a key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := rpcCall("skykv.delete", map[string]string{"key": args[0]})
			if err != nil {
				return err
			}
			fmt.Printf("deleted %s\n", args[0])
			return nil
		},
	}
}

func kvListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [prefix]",
		Short: "List keys",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix := ""
			if len(args) > 0 {
				prefix = args[0]
			}
			result, err := rpcCall("skykv.list", map[string]string{"prefix": prefix})
			if err != nil {
				return err
			}
			var r struct {
				Keys []string `json:"keys"`
			}
			json.Unmarshal(result, &r)
			if len(r.Keys) == 0 {
				fmt.Println("(no keys)")
				return nil
			}
			for _, k := range r.Keys {
				fmt.Println(k)
			}
			return nil
		},
	}
}

func kvSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Sync with remote devices",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := rpcCall("skykv.sync", nil)
			if err != nil {
				return err
			}
			fmt.Println("synced")
			return nil
		},
	}
}

func kvStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show KV store status",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("skykv.status", nil)
			if err != nil {
				return err
			}
			var r struct {
				Namespace     string `json:"namespace"`
				Keys          int    `json:"keys"`
				DeviceID      string `json:"device_id"`
				NSID          string `json:"nsid"`
				Ready         bool   `json:"ready"`
				PeerCount     int    `json:"peer_count"`
				ExpectedPeers int    `json:"expected_peers"`
				SyncState     string `json:"sync_state"`
				SyncMessage   string `json:"sync_message"`
			}
			json.Unmarshal(result, &r)
			fmt.Printf("namespace: %s\nkeys:      %d\ndevice:    %s\nready:     %v\nsync:      %s\npeers:     %d/%d\n", r.Namespace, r.Keys, r.DeviceID, r.Ready, r.SyncState, r.PeerCount, r.ExpectedPeers)
			if r.NSID != "" {
				fmt.Printf("nsid:      %s\n", r.NSID)
			}
			if r.SyncMessage != "" {
				fmt.Printf("message:   %s\n", r.SyncMessage)
			}

			if r.Keys > 0 {
				all, err := rpcCall("skykv.getAll", map[string]string{"prefix": ""})
				if err == nil {
					var ar struct {
						Entries map[string]string `json:"entries"`
					}
					json.Unmarshal(all, &ar)
					fmt.Println()
					for k, v := range ar.Entries {
						if len(v) > 60 {
							v = v[:60] + "…"
						}
						v = strings.ReplaceAll(v, "\n", "\\n")
						fmt.Printf("  %s = %s\n", k, v)
					}
				}
			}
			return nil
		},
	}
}
