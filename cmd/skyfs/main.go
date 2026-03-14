// Command skyfs is the CLI for encrypted file storage.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/sky10/sky10/internal/config"
	s3backend "github.com/sky10/sky10/skyadapter/s3"
	"github.com/sky10/sky10/skyfs"
)

// Set by -ldflags at build time. See Makefile.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "version", "--version":
		fmt.Printf("skyfs %s (%s) built %s\n", version, commit, buildDate)
		return
	case "init":
		err = cmdInit(os.Args[2:])
	case "put":
		err = cmdPut(os.Args[2:])
	case "get":
		err = cmdGet(os.Args[2:])
	case "ls":
		err = cmdList(os.Args[2:])
	case "rm":
		err = cmdRemove(os.Args[2:])
	case "info":
		err = cmdInfo(os.Args[2:])
	case "compact":
		err = cmdCompact(os.Args[2:])
	case "gc":
		err = cmdGC(os.Args[2:])
	case "help", "--help", "-h":
		printUsage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	bucket := fs.String("bucket", "", "S3 bucket name (required)")
	region := fs.String("region", "us-east-1", "S3 region")
	endpoint := fs.String("endpoint", "", "custom S3 endpoint (for B2/R2/MinIO)")
	pathStyle := fs.Bool("path-style", false, "use path-style S3 addressing")
	fs.Parse(args)

	if *bucket == "" {
		return fmt.Errorf("--bucket is required")
	}

	id, err := skyfs.GenerateIdentity()
	if err != nil {
		return fmt.Errorf("generating identity: %w", err)
	}

	idPath, err := config.DefaultIdentityPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(idPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	if err := skyfs.SaveIdentity(id, idPath); err != nil {
		return fmt.Errorf("saving identity: %w", err)
	}

	cfg := &config.Config{
		Bucket:         *bucket,
		Region:         *region,
		Endpoint:       *endpoint,
		ForcePathStyle: *pathStyle,
		IdentityFile:   idPath,
	}
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Printf("Initialized skyfs\n")
	fmt.Printf("  Identity: %s\n", id.ID())
	fmt.Printf("  Bucket:   %s\n", cfg.Bucket)
	fmt.Printf("  Config:   %s\n", dir)
	return nil
}

func cmdPut(args []string) error {
	fs := flag.NewFlagSet("put", flag.ExitOnError)
	remotePath := fs.String("as", "", "remote path (default: filename)")
	fs.Parse(args)

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: skyfs put <file> [--as <remote-path>]")
	}

	localPath := fs.Arg(0)

	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", localPath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", localPath, err)
	}

	remote := *remotePath
	if remote == "" {
		remote = filepath.Base(localPath)
	}

	ctx := context.Background()
	store, err := openStore(ctx)
	if err != nil {
		return err
	}

	if err := store.Put(ctx, remote, f); err != nil {
		return fmt.Errorf("storing %s: %w", remote, err)
	}

	fmt.Printf("stored %s (%s)\n", remote, formatSize(info.Size()))
	return nil
}

func cmdGet(args []string) error {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	outPath := fs.String("out", "", "output path (default: filename in current dir)")
	fs.Parse(args)

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: skyfs get <path> [--out <file>]")
	}

	remotePath := fs.Arg(0)

	out := *outPath
	if out == "" {
		out = filepath.Base(remotePath)
	}

	ctx := context.Background()
	store, err := openStore(ctx)
	if err != nil {
		return err
	}

	f, err := os.Create(out)
	if err != nil {
		return fmt.Errorf("creating %s: %w", out, err)
	}
	defer f.Close()

	if err := store.Get(ctx, remotePath, f); err != nil {
		os.Remove(out)
		return fmt.Errorf("retrieving %s: %w", remotePath, err)
	}

	info, _ := f.Stat()
	fmt.Printf("retrieved %s → %s (%s)\n", remotePath, out, formatSize(info.Size()))
	return nil
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	fs.Parse(args)

	prefix := ""
	if fs.NArg() > 0 {
		prefix = fs.Arg(0)
	}

	ctx := context.Background()
	store, err := openStore(ctx)
	if err != nil {
		return err
	}

	entries, err := store.List(ctx, prefix)
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		fmt.Println("no files found")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "PATH\tSIZE\tMODIFIED\tNAMESPACE\n")
	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			e.Path,
			formatSize(e.Size),
			e.Modified.Format("2006-01-02 15:04"),
			e.Namespace,
		)
	}
	w.Flush()
	return nil
}

func cmdRemove(args []string) error {
	fs := flag.NewFlagSet("rm", flag.ExitOnError)
	fs.Parse(args)

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: skyfs rm <path>")
	}

	ctx := context.Background()
	store, err := openStore(ctx)
	if err != nil {
		return err
	}

	if err := store.Remove(ctx, fs.Arg(0)); err != nil {
		return err
	}

	fmt.Printf("removed %s\n", fs.Arg(0))
	return nil
}

func cmdInfo(_ []string) error {
	ctx := context.Background()
	store, err := openStore(ctx)
	if err != nil {
		return err
	}

	info, err := store.Info(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("Identity:   %s\n", info.ID)
	fmt.Printf("Files:      %d\n", info.FileCount)
	fmt.Printf("Total size: %s\n", formatSize(info.TotalSize))
	if len(info.Namespaces) > 0 {
		fmt.Printf("Namespaces: %v\n", info.Namespaces)
	}
	return nil
}

func cmdCompact(args []string) error {
	fs := flag.NewFlagSet("compact", flag.ExitOnError)
	maxSnapshots := fs.Int("keep", 3, "number of snapshots to keep")
	fs.Parse(args)

	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	id, err := skyfs.LoadIdentity(cfg.IdentityFile)
	if err != nil {
		return fmt.Errorf("loading identity: %w", err)
	}
	backend, err := makeBackend(ctx, cfg)
	if err != nil {
		return err
	}

	result, err := skyfs.Compact(ctx, backend, id, *maxSnapshots)
	if err != nil {
		return err
	}

	fmt.Printf("Compacted %d ops into snapshot\n", result.OpsCompacted)
	fmt.Printf("  Ops deleted:       %d\n", result.OpsDeleted)
	fmt.Printf("  Snapshots kept:    %d\n", result.SnapshotsKept)
	fmt.Printf("  Snapshots deleted: %d\n", result.SnapshotsDeleted)
	return nil
}

func cmdGC(args []string) error {
	fs := flag.NewFlagSet("gc", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "show what would be deleted without deleting")
	fs.Parse(args)

	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	id, err := skyfs.LoadIdentity(cfg.IdentityFile)
	if err != nil {
		return fmt.Errorf("loading identity: %w", err)
	}
	backend, err := makeBackend(ctx, cfg)
	if err != nil {
		return err
	}

	result, err := skyfs.GC(ctx, backend, id, *dryRun)
	if err != nil {
		return err
	}

	if *dryRun {
		fmt.Println("Dry run (no changes made):")
	}
	fmt.Printf("Blobs referenced: %d\n", result.BlobsReferenced)
	fmt.Printf("Blobs found:      %d\n", result.BlobsFound)
	fmt.Printf("Blobs deleted:    %d\n", result.BlobsDeleted)
	fmt.Printf("Bytes reclaimed:  %s\n", formatSize(result.BytesReclaimed))
	return nil
}

func openStore(ctx context.Context) (*skyfs.Store, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	id, err := skyfs.LoadIdentity(cfg.IdentityFile)
	if err != nil {
		return nil, fmt.Errorf("loading identity: %w", err)
	}

	backend, err := makeBackend(ctx, cfg)
	if err != nil {
		return nil, err
	}

	return skyfs.New(backend, id), nil
}

func makeBackend(ctx context.Context, cfg *config.Config) (*s3backend.Backend, error) {
	backend, err := s3backend.New(ctx, s3backend.Config{
		Bucket:         cfg.Bucket,
		Region:         cfg.Region,
		Endpoint:       cfg.Endpoint,
		ForcePathStyle: cfg.ForcePathStyle,
	})
	if err != nil {
		return nil, fmt.Errorf("connecting to S3: %w", err)
	}
	return backend, nil
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

func printUsage() {
	fmt.Printf(`skyfs — encrypted file storage (%s)

Usage:
  skyfs init --bucket <name> [--region <r>] [--endpoint <url>] [--path-style]
  skyfs put <file> [--as <remote-path>]
  skyfs get <path> [--out <local-path>]
  skyfs ls [prefix]
  skyfs rm <path>
  skyfs info
  skyfs compact [--keep <n>]
  skyfs gc [--dry-run]
  skyfs version

Environment:
  S3_ACCESS_KEY_ID        S3 access key
  S3_SECRET_ACCESS_KEY    S3 secret key
`, version)
}
