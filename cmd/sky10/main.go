// Command sky10 is the unified CLI for the sky10 ecosystem.
package main

import (
	"fmt"
	"os"
)

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
	case "key":
		err = runKey(os.Args[2:])
	case "fs":
		err = runFS(os.Args[2:])
	case "version", "--version":
		fmt.Printf("sky10 %s (%s) built %s\n", version, commit, buildDate)
		return
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

func printUsage() {
	fmt.Printf(`sky10 — encrypted storage & agent coordination (%s)

Usage:
  sky10 key <command>     Key management
  sky10 fs <command>      Encrypted file storage
  sky10 version           Show version
  sky10 help              Show this help

Run 'sky10 key help' or 'sky10 fs help' for subcommand details.

Environment:
  S3_ACCESS_KEY_ID        S3 access key
  S3_SECRET_ACCESS_KEY    S3 secret key
`, version)
}
