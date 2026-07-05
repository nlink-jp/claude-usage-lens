package cmd

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/nlink-jp/claude-usage-lens/core/aggregate"
	"github.com/nlink-jp/claude-usage-lens/core/audit"
	"github.com/nlink-jp/claude-usage-lens/core/collect"
	"github.com/nlink-jp/claude-usage-lens/core/cost"
	"github.com/nlink-jp/claude-usage-lens/core/ingest"
	"github.com/nlink-jp/claude-usage-lens/core/model"
	"github.com/nlink-jp/claude-usage-lens/core/platform"
	"github.com/nlink-jp/claude-usage-lens/core/pricing"
	"github.com/nlink-jp/claude-usage-lens/core/store"
)

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

// --- verify (cross-check our cost against Cowork audit.jsonl ground truth) ---

func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	roots, err := platform.SourceRoots()
	if err != nil {
		return err
	}
	auditFiles, err := audit.DiscoverFiles(roots.CoworkRoot)
	if err != nil {
		return err
	}
	if len(auditFiles) == 0 {
		return fmt.Errorf("no Cowork audit.jsonl found under %s (verify needs Cowork data)", roots.CoworkRoot)
	}

	tbl := pricing.Default()
	host, _ := os.Hostname()

	type vrow struct {
		Session  string  `json:"session"`
		OursUSD  float64 `json:"ours_usd"`
		TruthUSD float64 `json:"truth_usd"`
		DeltaUSD float64 `json:"delta_usd"`
	}
	var rows []vrow
	var sumOurs, sumTruth float64

	for _, af := range auditFiles {
		dir := filepath.Dir(af)
		g, err := audit.Parse(af)
		if err != nil {
			continue
		}
		// Our cost for every transcript under the same session dir (incl subagents).
		files, _ := collect.Discover("", dir)
		var recs []model.UsageRecord
		for _, fl := range files {
			rs, err := collect.ParseFile(fl.Path, model.SourceCowork, host)
			if err != nil {
				continue
			}
			recs = append(recs, rs...)
		}
		recs = collect.Dedup(recs)
		var ours float64
		for _, r := range recs {
			ours += cost.ComputeRecord(r, tbl).ListPriceUSD
		}
		rows = append(rows, vrow{filepath.Base(dir), ours, g.TotalUSD, ours - g.TotalUSD})
		sumOurs += ours
		sumTruth += g.TotalUSD
	}

	if *asJSON {
		return printJSON(map[string]any{
			"sessions":        rows,
			"total_ours_usd":  sumOurs,
			"total_truth_usd": sumTruth,
			"total_delta_usd": sumOurs - sumTruth,
		})
	}

	pctOf := func(delta, truth float64) string {
		if truth == 0 {
			return "—"
		}
		return fmt.Sprintf("%+.1f%%", delta/truth*100)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tOURS(USD)\tAUDIT(USD)\tDELTA\tDELTA%")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t$%.4f\t$%.4f\t%+.4f\t%s\n", r.Session, r.OursUSD, r.TruthUSD, r.DeltaUSD, pctOf(r.DeltaUSD, r.TruthUSD))
	}
	fmt.Fprintf(tw, "TOTAL\t$%.4f\t$%.4f\t%+.4f\t%s\n", sumOurs, sumTruth, sumOurs-sumTruth, pctOf(sumOurs-sumTruth, sumTruth))
	tw.Flush()
	fmt.Println("\nOURS = our computed notional cost; AUDIT = Cowork audit.jsonl total_cost_usd (ground truth).")
	return nil
}

// --- watch (near-real-time: poll + incremental ingest) ---

func runWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	intervalStr := fs.String("interval", "5s", "poll interval (e.g. 5s, 30s, 2m)")
	source := fs.String("source", "all", "code|cowork|all")
	if err := fs.Parse(args); err != nil {
		return err
	}
	interval, err := time.ParseDuration(*intervalStr)
	if err != nil || interval < time.Second {
		return fmt.Errorf("bad --interval %q (min 1s)", *intervalStr)
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
	tbl := pricing.Default()

	// tick runs one incremental ingest and returns new-record count + current
	// grand totals (records, cost).
	tick := func() (newRecs, count int, total float64, err error) {
		res, err := ingest.Run(st, roots, tbl, host, sources)
		if err != nil {
			return 0, 0, 0, err
		}
		recs, err := st.Query(store.Filter{})
		if err != nil {
			return 0, 0, 0, err
		}
		for _, r := range recs {
			total += r.Cost.ListPriceUSD
		}
		return res.NewRecords, len(recs), total, nil
	}

	stamp := func() string { return time.Now().Format("15:04:05") }

	fmt.Printf("watching (interval %s, Ctrl-C to stop) → %s\n", interval, dbPath)
	_, count, total, err := tick()
	if err != nil {
		return err
	}
	fmt.Printf("[%s] baseline: %d records, total $%.2f\n", stamp(), count, total)
	prevTotal := total

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nstopped.")
			return nil
		case <-ticker.C:
			n, count, total, err := tick()
			if err != nil {
				fmt.Fprintf(os.Stderr, "ingest error: %v\n", err)
				continue
			}
			if n > 0 {
				fmt.Printf("[%s] +%d rec (Δ$%.2f)   now: %d rec / $%.2f\n", stamp(), n, total-prevTotal, count, total)
				prevTotal = total
			}
		}
	}
}

// --- daemon (register periodic ingest with the OS scheduler) ---

func runDaemon(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: claude-usage-lens daemon <install|uninstall|status> [flags]")
	}
	action, rest := args[0], args[1:]
	switch action {
	case "install":
		fs := flag.NewFlagSet("daemon install", flag.ExitOnError)
		intervalStr := fs.String("interval", "15m", "how often to ingest (e.g. 15m, 1h)")
		dryRun := fs.Bool("dry-run", false, "print the service config without installing")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		interval, err := time.ParseDuration(*intervalStr)
		if err != nil || interval < time.Minute {
			return fmt.Errorf("bad --interval %q (min 1m)", *intervalStr)
		}
		bin, err := os.Executable()
		if err != nil {
			return err
		}
		if *dryRun {
			cfg, err := platform.RenderDaemonConfig(bin, int(interval.Seconds()))
			if err != nil {
				return err
			}
			fmt.Print(cfg)
			return nil
		}
		info, err := platform.InstallDaemon(bin, int(interval.Seconds()))
		if err != nil {
			return err
		}
		fmt.Printf("installed %s daemon '%s' — runs `ingest` every %s\n  config: %s\n", info.Kind, info.Label, interval, info.ConfigPath)
		return nil

	case "uninstall":
		info, err := platform.UninstallDaemon()
		if err != nil {
			return err
		}
		fmt.Printf("removed daemon '%s'\n  (%s)\n", info.Label, info.ConfigPath)
		return nil

	case "status":
		info, err := platform.DaemonStatus()
		if err != nil {
			return err
		}
		state := "not installed"
		if info.Loaded {
			state = "loaded (running periodically)"
		} else if fileExists(info.ConfigPath) {
			state = "installed but not loaded"
		}
		fmt.Printf("daemon '%s' (%s): %s\n  config: %s\n", info.Label, info.Kind, state, info.ConfigPath)
		return nil

	default:
		return fmt.Errorf("unknown daemon action %q (want install|uninstall|status)", action)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
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
