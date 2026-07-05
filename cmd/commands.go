package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/nlink-jp/claude-usage-lens/core/platform"
)

// notImpl reports a command that is wired but whose engine lands in Phase 1.
func notImpl(name string) error {
	return fmt.Errorf("%s: not yet implemented (Phase 1)", name)
}

func runIngest(args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ExitOnError)
	fs.String("source", "all", "code|cowork|all")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return notImpl("ingest")
}

func runReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	fs.String("since", "", `start of range (e.g. 2026-07-01 | 7d | today)`)
	fs.String("until", "", "end of range")
	fs.String("group-by", "day", "day|session|project|model|entrypoint (comma-separated)")
	fs.String("source", "all", "code|cowork|all")
	fs.String("entrypoint", "", "filter: cli|claude-desktop|sdk-py")
	fs.Bool("breakdown", false, "expand the token-type breakdown")
	fs.Bool("json", false, "machine-readable JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return notImpl("report")
}

func runSessions(args []string) error {
	fs := flag.NewFlagSet("sessions", flag.ExitOnError)
	fs.Bool("json", false, "machine-readable JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return notImpl("sessions")
}

func runModels(args []string) error {
	fs := flag.NewFlagSet("models", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	return notImpl("models")
}

func runWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	return notImpl("watch")
}

// runDoctor is implemented in the scaffold: it resolves the OS-specific paths and
// reports whether each exists, so a user (notably on Windows, where paths are
// inferred) can verify resolution with one command and report back.
func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	roots, err := platform.SourceRoots()
	if err != nil {
		return err
	}
	cfg, _ := platform.ConfigDir()
	data, _ := platform.DataDir()

	fmt.Printf("claude-usage-lens doctor (%s/%s)\n\n", runtime.GOOS, runtime.GOARCH)
	fmt.Println("sources:")
	reportDir("code", roots.CodeRoot)
	reportDir("cowork", roots.CoworkRoot)
	fmt.Printf("\nconfig: %s\n", cfg)
	fmt.Printf("data:   %s\n", data)
	fmt.Println("\n(paths are overridable via config [sources] / --source-root)")
	return nil
}

func reportDir(label, dir string) {
	status := "MISSING"
	entries := 0
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		status = "ok"
		if matches, err := filepath.Glob(filepath.Join(dir, "*")); err == nil {
			entries = len(matches)
		}
	}
	fmt.Printf("  %-7s [%-7s] %s\n", label, status, dir)
	if status == "ok" {
		fmt.Printf("           %d top-level entr%s\n", entries, plural(entries))
	}
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
