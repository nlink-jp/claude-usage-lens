// Package cmd implements the claude-usage-lens CLI dispatch.
//
// Dispatch uses the standard library `flag` package (no third-party CLI
// framework) to keep the scaffold dependency-free and offline-buildable. If a
// richer UX is wanted later, swapping to cobra is a self-contained change.
package cmd

import (
	"fmt"
	"io"
	"os"
)

// Execute runs the CLI. version is injected from main via -ldflags.
func Execute(version string) {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}

	args := os.Args[2:]
	var err error
	switch os.Args[1] {
	case "ingest":
		err = runIngest(args)
	case "report":
		err = runReport(args)
	case "sessions":
		err = runSessions(args)
	case "models":
		err = runModels(args)
	case "doctor":
		err = runDoctor(args)
	case "watch":
		err = runWatch(args)
	case "version", "-v", "--version":
		fmt.Printf("claude-usage-lens %s\n", version)
		return
	case "help", "-h", "--help":
		usage(os.Stdout)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `claude-usage-lens — collect token usage & cost from Claude Code / Cowork local logs

Usage:
  claude-usage-lens <command> [flags]

Commands:
  ingest     Incrementally load new/changed sessions into the durable store
  report     Aggregate stored usage by day / session / project / model
  sessions   List sessions with tokens and cost
  models     Show the pricing table and flag drift
  doctor     Diagnose resolved source/store/config paths (cross-OS verification)
  watch      [Phase 2] Continuously ingest in near-real-time
  version    Print the version

Run 'claude-usage-lens <command> -h' for command-specific flags.

Note: costs are an API list-price EQUIVALENT (notional), not an actual bill —
subscription (Max/Pro) usage is not billed per token.
`)
}
