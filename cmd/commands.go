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
	groupBy := fs.String("group-by", "day", "hour|day|week|month|session|project|model|entrypoint (comma-separated)")
	source := fs.String("source", "all", "code|cowork|all")
	entrypoint := fs.String("entrypoint", "", "filter by entrypoint (cli|claude-desktop|sdk-py|local-agent)")
	modelFilter := fs.String("model", "", "filter by model id (substring)")
	projectFilter := fs.String("project", "", "filter by project path (substring)")
	sortBy := fs.String("sort", "", "sort rows: key|cost|input|output|records|cache")
	top := fs.Int("top", 0, "keep only the top N rows after sorting (0 = all)")
	breakdown := fs.Bool("breakdown", false, "expand cache read/write columns")
	summary := fs.Bool("summary", false, "print period summary stats instead of rows")
	compare := fs.Bool("compare", false, "compare this period vs the preceding equal-length period (needs --since)")
	asJSON := fs.Bool("json", false, "machine-readable JSON output")
	if err := fs.Parse(args); err != nil {
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

	load := func(sinceU, untilU int64) ([]model.PricedRecord, error) {
		recs, err := st.Query(store.Filter{Since: sinceU, Until: untilU, Source: src})
		if err != nil {
			return nil, err
		}
		return applyFilters(recs, *entrypoint, *modelFilter, *projectFilter), nil
	}

	recs, err := load(sinceU, untilU)
	if err != nil {
		return err
	}

	switch {
	case *compare:
		if sinceU == 0 {
			return fmt.Errorf("--compare requires --since")
		}
		until := untilU
		if until == 0 {
			until = time.Now().Unix()
		}
		span := until - sinceU
		prev, err := load(sinceU-span, sinceU)
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(buildComparison(recs, prev))
		}
		printComparison(os.Stdout, recs, prev)
		return nil

	case *summary:
		s := aggregate.Summarize(recs)
		if *asJSON {
			return printJSON(s)
		}
		printSummary(os.Stdout, s)
		return nil
	}

	dims, err := aggregate.ParseDimensions(*groupBy)
	if err != nil {
		return err
	}
	rows, err := aggregate.Aggregate(recs, dims)
	if err != nil {
		return err
	}
	if *sortBy != "" {
		if err := aggregate.SortRows(rows, *sortBy); err != nil {
			return err
		}
	}
	if *top > 0 && *top < len(rows) {
		rows = rows[:*top]
	}

	if *asJSON {
		return printJSON(rows)
	}
	printReport(os.Stdout, rows, *breakdown)
	return nil
}

// applyFilters narrows records by entrypoint (exact), model (substring), and
// project (substring). Empty filters are no-ops.
func applyFilters(recs []model.PricedRecord, ep, mdl, project string) []model.PricedRecord {
	if ep == "" && mdl == "" && project == "" {
		return recs
	}
	out := make([]model.PricedRecord, 0, len(recs))
	for _, r := range recs {
		if ep != "" && string(r.Entrypoint) != ep {
			continue
		}
		if mdl != "" && !strings.Contains(r.Model, mdl) {
			continue
		}
		if project != "" && !strings.Contains(r.Project, project) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func printSummary(w io.Writer, s aggregate.Summary) {
	period := "(no dated records)"
	if s.FirstDay != "" {
		period = s.FirstDay + " → " + s.LastDay
	}
	fmt.Fprintf(w, "period:  %s  (%d active days)\n", period, s.ActiveDays)
	fmt.Fprintf(w, "records: %d    tokens: in %d / out %d / cache %d\n", s.Records, s.InputTokens, s.OutputTokens, s.CacheTokens)
	fmt.Fprintf(w, "total:   $%.2f    daily avg: $%.2f\n", s.TotalUSD, s.DailyAvgUSD)
	if s.PeakDay != "" {
		fmt.Fprintf(w, "peak:    %s $%.2f    projection(30d): $%.2f\n", s.PeakDay, s.PeakUSD, s.Projection30USD)
	}
	fmt.Fprintln(w, "\nCosts are an API list-price EQUIVALENT (notional), not an actual bill.")
}

// periodTotals is the cross-record sum used by --compare.
type periodTotals struct {
	Records      int     `json:"records"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheTokens  int64   `json:"cache_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

func totalsOf(recs []model.PricedRecord) periodTotals {
	var t periodTotals
	for _, r := range recs {
		t.Records++
		t.InputTokens += r.Usage.InputTokens
		t.OutputTokens += r.Usage.OutputTokens
		t.CacheTokens += r.Usage.CacheReadInputTokens + r.Usage.CacheCreation1h + r.Usage.CacheCreation5m
		t.CostUSD += r.Cost.ListPriceUSD
	}
	return t
}

type comparison struct {
	Current  periodTotals `json:"current"`
	Previous periodTotals `json:"previous"`
}

func buildComparison(cur, prev []model.PricedRecord) comparison {
	return comparison{Current: totalsOf(cur), Previous: totalsOf(prev)}
}

func printComparison(w io.Writer, cur, prev []model.PricedRecord) {
	c, p := totalsOf(cur), totalsOf(prev)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "METRIC\tCURRENT\tPREVIOUS\tDELTA\tDELTA%")
	row := func(name string, cur, prev float64, money bool) {
		d := cur - prev
		pct := "—"
		if prev != 0 {
			pct = fmt.Sprintf("%+.1f%%", d/prev*100)
		}
		if money {
			fmt.Fprintf(tw, "%s\t$%.2f\t$%.2f\t%+.2f\t%s\n", name, cur, prev, d, pct)
		} else {
			fmt.Fprintf(tw, "%s\t%.0f\t%.0f\t%+.0f\t%s\n", name, cur, prev, d, pct)
		}
	}
	row("cost(USD)", c.CostUSD, p.CostUSD, true)
	row("input", float64(c.InputTokens), float64(p.InputTokens), false)
	row("output", float64(c.OutputTokens), float64(p.OutputTokens), false)
	row("records", float64(c.Records), float64(p.Records), false)
	tw.Flush()
	fmt.Fprintln(w, "\ncurrent vs. the preceding equal-length period. Costs are notional.")
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
