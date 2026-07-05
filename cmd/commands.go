package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/nlink-jp/claude-usage-lens/core/aggregate"
	"github.com/nlink-jp/claude-usage-lens/core/ingest"
	"github.com/nlink-jp/claude-usage-lens/core/model"
	"github.com/nlink-jp/claude-usage-lens/core/platform"
	"github.com/nlink-jp/claude-usage-lens/core/pricing"
	"github.com/nlink-jp/claude-usage-lens/core/store"
)

// notImpl reports a command that is wired but whose engine lands in a later phase.
func notImpl(name string) error {
	return fmt.Errorf("%s: not yet implemented (Phase 2)", name)
}

// --- shared helpers ---

// openStore opens the durable store at the OS-standard data dir.
func openStore() (store.Store, string, error) {
	dataDir, err := platform.DataDir()
	if err != nil {
		return nil, "", err
	}
	dbPath := filepath.Join(dataDir, "usage.db")
	st, err := store.Open(dbPath)
	return st, dbPath, err
}

func sourceValue(s string) (model.Source, error) {
	switch s {
	case "", "all":
		return "", nil
	case "code":
		return model.SourceCode, nil
	case "cowork":
		return model.SourceCowork, nil
	default:
		return "", fmt.Errorf("bad --source %q (want code|cowork|all)", s)
	}
}

// parseSince interprets an absolute date (YYYY-MM-DD), a relative "Nd", or
// "today". Empty means unbounded (0).
func parseSince(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	now := time.Now().UTC()
	switch {
	case s == "today":
		y, m, d := now.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix(), nil
	case strings.HasSuffix(s, "d"):
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("bad relative range %q (want e.g. 7d)", s)
		}
		return now.AddDate(0, 0, -n).Unix(), nil
	default:
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return 0, fmt.Errorf("bad --since %q (want YYYY-MM-DD | Nd | today)", s)
		}
		return t.Unix(), nil
	}
}

// parseUntil is like parseSince but a bare date is treated inclusively (end of day).
func parseUntil(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Add(24*time.Hour - time.Second).Unix(), nil
	}
	return parseSince(s)
}

// --- ingest ---

func runIngest(args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ExitOnError)
	source := fs.String("source", "all", "code|cowork|all")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var sources map[model.Source]bool
	if sv, err := sourceValue(*source); err != nil {
		return err
	} else if sv != "" {
		sources = map[model.Source]bool{sv: true}
	}

	roots, err := platform.SourceRoots()
	if err != nil {
		return err
	}
	host, _ := os.Hostname()

	st, dbPath, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	res, err := ingest.Run(st, roots, pricing.Default(), host, sources)
	if err != nil {
		return err
	}
	fmt.Printf("ingest complete → %s\n", dbPath)
	fmt.Printf("  files scanned: %d\n", res.FilesScanned)
	fmt.Printf("  files changed: %d\n", res.FilesChanged)
	fmt.Printf("  new records:   %d\n", res.NewRecords)
	if res.FileErrors > 0 {
		fmt.Printf("  file errors:   %d (skipped)\n", res.FileErrors)
	}
	return nil
}

// --- report ---

func runReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	since := fs.String("since", "", `start of range (2026-07-01 | 7d | today)`)
	until := fs.String("until", "", "end of range (inclusive date)")
	groupBy := fs.String("group-by", "day", "day|session|project|model|entrypoint (comma-separated)")
	source := fs.String("source", "all", "code|cowork|all")
	entrypoint := fs.String("entrypoint", "", "filter: cli|claude-desktop|sdk-py")
	breakdown := fs.Bool("breakdown", false, "expand cache read/write columns")
	asJSON := fs.Bool("json", false, "machine-readable JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dims, err := aggregate.ParseDimensions(*groupBy)
	if err != nil {
		return err
	}
	sinceU, err := parseSince(*since)
	if err != nil {
		return err
	}
	untilU, err := parseUntil(*until)
	if err != nil {
		return err
	}
	src, err := sourceValue(*source)
	if err != nil {
		return err
	}

	st, _, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	recs, err := st.Query(store.Filter{Since: sinceU, Until: untilU, Source: src})
	if err != nil {
		return err
	}
	if *entrypoint != "" {
		recs = filterEntrypoint(recs, *entrypoint)
	}
	rows, err := aggregate.Aggregate(recs, dims)
	if err != nil {
		return err
	}

	if *asJSON {
		return printJSON(rows)
	}
	printReport(os.Stdout, rows, *breakdown)
	return nil
}

func filterEntrypoint(recs []model.PricedRecord, ep string) []model.PricedRecord {
	out := make([]model.PricedRecord, 0, len(recs))
	for _, r := range recs {
		if string(r.Entrypoint) == ep {
			out = append(out, r)
		}
	}
	return out
}

func printReport(w io.Writer, rows []aggregate.Row, breakdown bool) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	var trec int
	var tin, tout, tread, twrite int64
	var tcost float64

	if breakdown {
		fmt.Fprintln(tw, "KEY\tRECORDS\tINPUT\tOUTPUT\tCACHE-RD\tCACHE-WR\tCOST(USD)")
	} else {
		fmt.Fprintln(tw, "KEY\tRECORDS\tINPUT\tOUTPUT\tCACHE\tCOST(USD)")
	}
	for _, r := range rows {
		trec += r.Records
		tin += r.InputTokens
		tout += r.OutputTokens
		tread += r.CacheReadTokens
		twrite += r.CacheWriteTokens
		tcost += r.CostUSD
		if breakdown {
			fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t$%.4f\n", r.Key, r.Records, r.InputTokens, r.OutputTokens, r.CacheReadTokens, r.CacheWriteTokens, r.CostUSD)
		} else {
			fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t$%.4f\n", r.Key, r.Records, r.InputTokens, r.OutputTokens, r.CacheTokens, r.CostUSD)
		}
	}
	if breakdown {
		fmt.Fprintf(tw, "TOTAL\t%d\t%d\t%d\t%d\t%d\t$%.4f\n", trec, tin, tout, tread, twrite, tcost)
	} else {
		fmt.Fprintf(tw, "TOTAL\t%d\t%d\t%d\t%d\t$%.4f\n", trec, tin, tout, tread+twrite, tcost)
	}
	tw.Flush()
	fmt.Fprintln(w, "\nCosts are an API list-price EQUIVALENT (notional), not an actual bill.")
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// --- sessions ---

func runSessions(args []string) error {
	fs := flag.NewFlagSet("sessions", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, _, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	recs, err := st.Query(store.Filter{})
	if err != nil {
		return err
	}
	rows, err := aggregate.Aggregate(recs, []aggregate.Dimension{aggregate.BySession})
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(rows)
	}
	printReport(os.Stdout, rows, false)
	return nil
}

// --- models ---

func runModels(args []string) error {
	fs := flag.NewFlagSet("models", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	tbl := pricing.Default()
	if *asJSON {
		return printJSON(tbl)
	}
	names := make([]string, 0, len(tbl))
	for k := range tbl {
		names = append(names, k)
	}
	sort.Strings(names)

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL\tINPUT/Mtok\tOUTPUT/Mtok\tCACHE-READ\tWRITE-5m\tWRITE-1h")
	for _, m := range names {
		r := tbl[m]
		fmt.Fprintf(tw, "%s\t$%.2f\t$%.2f\t%gx\t%gx\t%gx\n", m, r.InputPerMTok, r.OutputPerMTok, r.CacheReadMultiplier, r.CacheWrite5mMultiplier, r.CacheWrite1hMultiplier)
	}
	tw.Flush()
	fmt.Println("\nRates USD per 1M tokens (as of 2026-07-05). Override via config.toml [pricing].")
	return nil
}

// --- watch (Phase 2 stub) ---

func runWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	return notImpl("watch")
}

// --- doctor (diagnoses resolved paths) ---

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
