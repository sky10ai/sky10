package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func FsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fs",
		Short: "Encrypted file storage",
	}

	cmd.AddCommand(fsInitCmd())
	cmd.AddCommand(fsPutCmd())
	cmd.AddCommand(fsListCmd())
	cmd.AddCommand(fsInfoCmd())
	cmd.AddCommand(fsSyncCmd())
	cmd.AddCommand(fsCompactCmd())
	cmd.AddCommand(fsGCCmd())
	cmd.AddCommand(fsVersionsCmd())
	cmd.AddCommand(fsResetCmd())
	cmd.AddCommand(fsDriveCmd())
	cmd.AddCommand(fsInviteCmd())
	cmd.AddCommand(fsJoinCmd())
	cmd.AddCommand(fsApproveCmd())
	cmd.AddCommand(fsHealthCmd())

	return cmd
}

// --- All commands below talk to the running daemon via RPC ---

func fsPutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "put <file>",
		Short: "Encrypt and store a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			remotePath, _ := cmd.Flags().GetString("as")
			if remotePath == "" {
				remotePath = filepath.Base(args[0])
			}
			localPath, _ := filepath.Abs(args[0])
			result, err := rpcCall("skyfs.put", map[string]string{
				"path": remotePath, "local_path": localPath,
			})
			if err != nil {
				return err
			}
			var r struct{ Size int64 }
			json.Unmarshal(result, &r)
			fmt.Printf("stored %s (%s)\n", remotePath, formatSize(r.Size))
			return nil
		},
	}
	cmd.Flags().String("as", "", "Remote path")
	return cmd
}

func fsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls [prefix]",
		Short: "List stored files",
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix := ""
			if len(args) > 0 {
				prefix = args[0]
			}
			result, err := rpcCall("skyfs.list", map[string]string{"prefix": prefix})
			if err != nil {
				return err
			}
			var r struct {
				Files []struct {
					Path     string `json:"path"`
					Size     int64  `json:"size"`
					Modified string `json:"modified"`
				} `json:"files"`
			}
			json.Unmarshal(result, &r)
			if len(r.Files) == 0 {
				fmt.Println("no files found")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintf(w, "PATH\tSIZE\tMODIFIED\n")
			for _, f := range r.Files {
				fmt.Fprintf(w, "%s\t%s\t%s\n", f.Path, formatSize(f.Size), f.Modified)
			}
			w.Flush()
			return nil
		},
	}
}

func fsInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show configuration and stats",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("skyfs.info", nil)
			if err != nil {
				return err
			}
			var r struct {
				ID         string   `json:"id"`
				FileCount  int      `json:"file_count"`
				TotalSize  int64    `json:"total_size"`
				Namespaces []string `json:"namespaces"`
			}
			json.Unmarshal(result, &r)
			fmt.Printf("Identity:   %s\nFiles:      %d\nTotal size: %s\n",
				r.ID, r.FileCount, formatSize(r.TotalSize))
			if len(r.Namespaces) > 0 {
				fmt.Printf("Namespaces: %v\n", r.Namespaces)
			}
			return nil
		},
	}
}

func fsSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync <directory>",
		Short: "Sync a directory via the daemon",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, _ := filepath.Abs(args[0])
			poll, _ := cmd.Flags().GetInt("poll")
			_, err := rpcCall("skyfs.syncStart", map[string]interface{}{
				"dir": dir, "poll_seconds": poll,
			})
			if err != nil {
				return err
			}
			fmt.Printf("syncing %s (poll every %ds)\n", dir, poll)
			return nil
		},
	}
	cmd.Flags().Int("poll", 30, "Poll interval in seconds")
	return cmd
}

func fsCompactCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "compact",
		Short: "Compact ops log (no-op in snapshot-exchange)",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("skyfs.compact", nil)
			if err != nil {
				return err
			}
			var r map[string]string
			json.Unmarshal(result, &r)
			fmt.Println(r["status"])
			return nil
		},
	}
}

func fsGCCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Garbage collect orphaned blobs",
		RunE: func(cmd *cobra.Command, args []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			_, err := rpcCall("skyfs.gc", map[string]bool{"dry_run": dryRun})
			if err != nil {
				return err
			}
			fmt.Println("gc complete")
			return nil
		},
	}
	cmd.Flags().Bool("dry-run", false, "Show what would be deleted")
	return cmd
}

func fsVersionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "versions <path>",
		Short: "Show file version history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("skyfs.versions", map[string]string{"path": args[0]})
			if err != nil {
				return err
			}
			var r struct {
				Versions []struct {
					Timestamp string `json:"timestamp"`
					Device    string `json:"device"`
					Size      int64  `json:"size"`
					Checksum  string `json:"checksum"`
				} `json:"versions"`
			}
			json.Unmarshal(result, &r)
			if len(r.Versions) == 0 {
				fmt.Println("no versions found")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintf(w, "TIMESTAMP\tDEVICE\tSIZE\tCHECKSUM\n")
			for _, v := range r.Versions {
				cksum := v.Checksum
				if len(cksum) > 12 {
					cksum = cksum[:12]
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", v.Timestamp, v.Device, formatSize(v.Size), cksum)
			}
			w.Flush()
			return nil
		},
	}
}

func fsResetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "Delete all ops and snapshots from S3 + local state",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("skyfs.reset", nil)
			if err != nil {
				return err
			}
			var r map[string]interface{}
			json.Unmarshal(result, &r)
			fmt.Printf("Deleted %v S3 objects, %v local state files\n",
				r["s3_deleted"], r["local_deleted"])
			return nil
		},
	}
}

func fsHealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Show daemon health",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("skyfs.health", nil)
			if err != nil {
				return err
			}
			var r map[string]interface{}
			json.Unmarshal(result, &r)
			for _, k := range []string{"status", "version", "uptime", "drives", "drives_running", "outbox_pending", "last_activity_ago"} {
				if v, ok := r[k]; ok {
					fmt.Printf("%-20s %v\n", k+":", v)
				}
			}
			return nil
		},
	}
}

func fsDriveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "drive",
		Short: "Manage sync drives",
	}
	cmd.AddCommand(fsDriveCreateCmd())
	cmd.AddCommand(fsDriveListCmd())
	cmd.AddCommand(fsDriveRemoveCmd())
	return cmd
}

func fsDriveCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <name> <path>",
		Short: "Create a new sync drive",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ns, _ := cmd.Flags().GetString("namespace")
			if ns == "" {
				ns = args[0]
			}
			path, _ := filepath.Abs(args[1])
			result, err := rpcCall("skyfs.driveCreate", map[string]string{
				"name": args[0], "path": path, "namespace": ns,
			})
			if err != nil {
				return err
			}
			var r struct {
				Name      string `json:"name"`
				LocalPath string `json:"local_path"`
				Namespace string `json:"namespace"`
			}
			json.Unmarshal(result, &r)
			fmt.Printf("Created drive %q → %s (namespace: %s)\n", r.Name, r.LocalPath, r.Namespace)
			return nil
		},
	}
	cmd.Flags().String("namespace", "", "Remote namespace (default: drive name)")
	return cmd
}

func fsDriveListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all drives",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("skyfs.driveList", nil)
			if err != nil {
				return err
			}
			var r struct {
				Drives []struct {
					Name      string `json:"name"`
					LocalPath string `json:"local_path"`
					Namespace string `json:"namespace"`
					Running   bool   `json:"running"`
					Files     int    `json:"snapshot_files"`
				} `json:"drives"`
			}
			json.Unmarshal(result, &r)
			if len(r.Drives) == 0 {
				fmt.Println("No drives. Create one with: sky10 fs drive create <name> <path>")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintf(w, "NAME\tPATH\tNAMESPACE\tFILES\tRUNNING\n")
			for _, d := range r.Drives {
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%v\n", d.Name, d.LocalPath, d.Namespace, d.Files, d.Running)
			}
			w.Flush()
			return nil
		},
	}
}

func fsDriveRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <id>",
		Short: "Remove a drive",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := rpcCall("skyfs.driveRemove", map[string]string{"id": args[0]})
			if err != nil {
				return err
			}
			fmt.Printf("Removed drive %q\n", args[0])
			return nil
		},
	}
}

func fsInviteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "invite",
		Short: "Generate an invite code",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("skyfs.invite", nil)
			if err != nil {
				return err
			}
			var r struct{ Code string }
			json.Unmarshal(result, &r)
			fmt.Println("\nShare this invite code with the other device:")
			fmt.Println(r.Code)
			fmt.Println("\nThe other device runs: sky10 fs join <code>")
			return nil
		},
	}
}

func fsApproveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "approve",
		Short: "Approve a pending join request",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("skyfs.approve", nil)
			if err != nil {
				return err
			}
			var r struct{ Approved int }
			json.Unmarshal(result, &r)
			if r.Approved == 0 {
				fmt.Println("No pending join requests found.")
			} else {
				fmt.Printf("Approved %d device(s)\n", r.Approved)
			}
			return nil
		},
	}
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
