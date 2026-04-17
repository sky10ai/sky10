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
	var pattern string
	var dryRun bool
	var includeInternal bool

	cmd := &cobra.Command{
		Use:   "delete <key>",
		Short: "Delete a key or keys matching a pattern",
		Example: strings.TrimSpace(`
sky10 kv delete session/token
sky10 kv delete --pattern 'session/*'
sky10 kv delete --pattern 'session/*' --dry-run
`),
		Args: func(cmd *cobra.Command, args []string) error {
			if pattern != "" {
				if len(args) != 0 {
					return fmt.Errorf("delete with --pattern does not take a key argument")
				}
				return nil
			}
			if dryRun {
				return fmt.Errorf("--dry-run requires --pattern")
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if pattern == "" {
				_, err := rpcCall("skykv.delete", map[string]string{"key": args[0]})
				if err != nil {
					return err
				}
				fmt.Printf("deleted %s\n", args[0])
				return nil
			}

			result, err := rpcCall("skykv.deleteMatching", map[string]interface{}{
				"pattern":          pattern,
				"dry_run":          dryRun,
				"include_internal": includeInternal,
			})
			if err != nil {
				return err
			}
			var r struct {
				Keys   []string `json:"keys"`
				Count  int      `json:"count"`
				DryRun bool     `json:"dry_run"`
			}
			json.Unmarshal(result, &r)
			if r.Count == 0 {
				fmt.Printf("no keys matched %q\n", pattern)
				return nil
			}

			action := "deleted"
			if r.DryRun {
				action = "would delete"
			}
			for _, key := range r.Keys {
				fmt.Printf("%s %s\n", action, key)
			}
			fmt.Printf("%s %d key(s) matching %q\n", action, r.Count, pattern)
			return nil
		},
	}
	cmd.Flags().StringVar(&pattern, "pattern", "", "Delete all keys matching this pattern ('*' matches any sequence, including '/', '?' matches one character)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show matching keys without deleting them")
	cmd.Flags().BoolVar(&includeInternal, "internal", false, "include reserved _sys/* keys when matching patterns")
	return cmd
}

func kvListCmd() *cobra.Command {
	var includeInternal bool
	cmd := &cobra.Command{
		Use:   "list [prefix]",
		Short: "List keys",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix := ""
			if len(args) > 0 {
				prefix = args[0]
			}
			result, err := rpcCall("skykv.list", map[string]interface{}{
				"prefix":           prefix,
				"include_internal": includeInternal,
			})
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
	cmd.Flags().BoolVar(&includeInternal, "internal", false, "include reserved _sys/* keys")
	return cmd
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
	var includeInternal bool
	cmd := &cobra.Command{
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
				all, err := rpcCall("skykv.getAll", map[string]interface{}{
					"prefix":           "",
					"include_internal": includeInternal,
				})
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
	cmd.Flags().BoolVar(&includeInternal, "internal", false, "include reserved _sys/* keys in the entry dump")
	return cmd
}
