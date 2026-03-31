package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/sky10/sky10/pkg/config"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/kv"
	"github.com/spf13/cobra"
)

// KvCmd returns the `sky10 kv` command group.
func KvCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kv",
		Short: "Encrypted key-value store",
	}

	cmd.PersistentFlags().String("namespace", "default", "KV namespace")

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
			store, err := openKVStore(cmd)
			if err != nil {
				return err
			}
			ctx := context.Background()
			if err := store.Set(ctx, args[0], []byte(args[1])); err != nil {
				return err
			}
			if err := store.SyncOnce(ctx); err != nil {
				return fmt.Errorf("sync: %w", err)
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
			store, err := openKVStore(cmd)
			if err != nil {
				return err
			}
			ctx := context.Background()
			// Poll first to get latest remote state
			store.SyncOnce(ctx)

			val, ok := store.Get(args[0])
			if !ok {
				return fmt.Errorf("key not found: %s", args[0])
			}
			fmt.Println(string(val))
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
			store, err := openKVStore(cmd)
			if err != nil {
				return err
			}
			ctx := context.Background()
			if err := store.Delete(ctx, args[0]); err != nil {
				return err
			}
			if err := store.SyncOnce(ctx); err != nil {
				return fmt.Errorf("sync: %w", err)
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
			store, err := openKVStore(cmd)
			if err != nil {
				return err
			}
			ctx := context.Background()
			store.SyncOnce(ctx)

			prefix := ""
			if len(args) > 0 {
				prefix = args[0]
			}
			keys := store.List(prefix)
			if len(keys) == 0 {
				fmt.Println("(no keys)")
				return nil
			}
			for _, k := range keys {
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
			store, err := openKVStore(cmd)
			if err != nil {
				return err
			}
			if err := store.SyncOnce(context.Background()); err != nil {
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
			store, err := openKVStore(cmd)
			if err != nil {
				return err
			}
			store.SyncOnce(context.Background())

			snap, err := store.Snapshot()
			if err != nil {
				return err
			}
			fmt.Printf("namespace: %s\n", nsFromCmd(cmd))
			fmt.Printf("keys:      %d\n", snap.Len())

			keys := snap.Keys()
			if len(keys) > 0 {
				fmt.Println()
				for _, k := range keys {
					vi, _ := snap.Lookup(k)
					val := string(vi.Value)
					if len(val) > 60 {
						val = val[:60] + "…"
					}
					val = strings.ReplaceAll(val, "\n", "\\n")
					fmt.Printf("  %s = %s\n", k, val)
				}
			}
			return nil
		},
	}
}

func openKVStore(cmd *cobra.Command) (*kv.Store, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	id, err := skykey.Load(cfg.IdentityFile)
	if err != nil {
		return nil, fmt.Errorf("loading identity: %w", err)
	}
	backend, err := makeBackend(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	ns := nsFromCmd(cmd)
	store := kv.New(backend, id, kv.Config{Namespace: ns}, nil)
	return store, nil
}

func nsFromCmd(cmd *cobra.Command) string {
	ns, _ := cmd.Flags().GetString("namespace")
	if ns == "" {
		ns = "default"
	}
	return ns
}
