package cmd

// calibrate / limits — real-quota calibration (ADR-0001).
//
// The actual remaining quota is server-side and exposed through no official
// machine-readable interface, so it cannot be collected from logs. Instead the
// user records the official percentage read off Claude Code's /usage screen
// (`calibrate add`), and `limits` divides our measured window consumption by it
// to derive the effective cap and thus a REAL-anchored remaining figure.

import (
	"flag"
	"fmt"
	"time"

	"github.com/nlink-jp/claude-usage-lens/core/limits"
	"github.com/nlink-jp/claude-usage-lens/core/model"
	"github.com/nlink-jp/claude-usage-lens/core/store"
)

// consumptionBetween sums store records over [since, until] (unix seconds) on
// both budget bases. All sources count — the subscription window covers Code
// and Cowork alike.
func consumptionBetween(st store.Store, since, until int64) (limits.Consumption, error) {
	recs, err := st.Query(store.Filter{Since: since, Until: until})
	var c limits.Consumption
	for _, r := range recs {
		c.Add(r)
	}
	return c, err
}

// calibrationConsumption measures the calibration's own anchor span:
// [window start, observed-at].
func calibrationConsumption(st store.Store, c model.Calibration, period time.Duration) (limits.Consumption, error) {
	start, _ := limits.CalibrationWindow(c, period)
	return consumptionBetween(st, start.Unix(), c.ObservedAt.Unix())
}

// --- calibrate ---

func runCalibrate(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: claude-usage-lens calibrate <add|list|remove> [flags]")
	}
	action, rest := args[0], args[1:]
	switch action {
	case "add":
		return runCalibrateAdd(rest)
	case "list":
		return runCalibrateList(rest)
	case "remove":
		return runCalibrateRemove(rest)
	default:
		return fmt.Errorf("unknown calibrate action %q (want add|list|remove)", action)
	}
}

func runCalibrateAdd(args []string) error {
	fs := flag.NewFlagSet("calibrate add", flag.ExitOnError)
	utilization := fs.Float64("utilization", 0, "the official utilization percent shown by /usage (required, 0–100)")
	resetsAt := fs.String("resets-at", "", "the next reset instant shown by /usage (required; 2026-08-13T09:00 | RFC3339)")
	at := fs.String("at", "", "when the reading was taken (default: now)")
	note := fs.String("note", "", "free-form note stored with the point")
	tz := fs.String("tz", "local", "timezone for datetime flags: local | utc | IANA name")
	asJSON := fs.Bool("json", false, "machine-readable JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	loc, err := resolveTZ(*tz)
	if err != nil {
		return err
	}
	if *resetsAt == "" {
		return fmt.Errorf("--resets-at is required — it is the reset instant the /usage screen shows")
	}
	resetsU, err := parseSince(*resetsAt, loc)
	if err != nil {
		return fmt.Errorf("bad --resets-at: %w", err)
	}
	observedU := time.Now().Unix()
	if *at != "" {
		if observedU, err = parseSince(*at, loc); err != nil {
			return fmt.Errorf("bad --at: %w", err)
		}
	}

	cal := model.Calibration{
		ObservedAt:  time.Unix(observedU, 0).UTC(),
		ResetsAt:    time.Unix(resetsU, 0).UTC(),
		Window:      limits.WindowWeekly,
		Utilization: *utilization,
		Source:      "manual",
		Note:        *note,
	}
	period, err := limits.Period(cal.Window)
	if err != nil {
		return err
	}
	if err := limits.Validate(cal, period); err != nil {
		return err
	}

	st, _, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	// Derive the caps up front: a window with no locally visible consumption
	// cannot anchor a cap, and storing such a point would only poison `limits`.
	calCons, err := calibrationConsumption(st, cal, period)
	if err != nil {
		return err
	}
	caps, err := limits.DeriveCaps(cal, calCons)
	if err != nil {
		return err
	}

	id, err := st.AddCalibration(cal)
	if err != nil {
		return err
	}
	cal.ID = id

	if *asJSON {
		return printJSON(map[string]any{"id": id, "caps": caps})
	}
	fmt.Printf("calibration #%d recorded: %.1f%% at %s (resets %s)\n",
		id, cal.Utilization, cal.ObservedAt.In(loc).Format("2006-01-02 15:04"), cal.ResetsAt.In(loc).Format("2006-01-02 15:04"))
	fmt.Printf("derived weekly cap: $%.2f  /  %d tokens (in+out)\n", caps.CostUSD, caps.Tokens)
	fmt.Println("\nRun `claude-usage-lens limits` to see calibrated remaining quota.")
	return nil
}

func runCalibrateList(args []string) error {
	fs := flag.NewFlagSet("calibrate list", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable JSON output")
	tz := fs.String("tz", "local", "timezone for display: local | utc | IANA name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	loc, err := resolveTZ(*tz)
	if err != nil {
		return err
	}

	st, _, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	cals, err := st.Calibrations("")
	if err != nil {
		return err
	}

	type calRow struct {
		ID          int64        `json:"id"`
		ObservedAt  time.Time    `json:"observed_at"`
		ResetsAt    time.Time    `json:"resets_at"`
		Window      string       `json:"window"`
		Utilization float64      `json:"utilization_pct"`
		Source      string       `json:"source"`
		Note        string       `json:"note,omitempty"`
		Caps        *limits.Caps `json:"caps,omitempty"`
	}
	rows := make([]calRow, 0, len(cals))
	for _, c := range cals {
		row := calRow{ID: c.ID, ObservedAt: c.ObservedAt, ResetsAt: c.ResetsAt,
			Window: c.Window, Utilization: c.Utilization, Source: c.Source, Note: c.Note}
		if period, err := limits.Period(c.Window); err == nil {
			if cons, err := calibrationConsumption(st, c, period); err == nil {
				if caps, err := limits.DeriveCaps(c, cons); err == nil {
					row.Caps = &caps
				}
			}
		}
		rows = append(rows, row)
	}

	if *asJSON {
		return printJSON(rows)
	}
	if len(rows) == 0 {
		fmt.Println("no calibrations recorded — add one with:")
		fmt.Println("  claude-usage-lens calibrate add --utilization <pct> --resets-at <datetime>")
		return nil
	}
	for _, r := range rows {
		caps := "cap underivable (no consumption in window)"
		if r.Caps != nil {
			caps = fmt.Sprintf("cap $%.2f / %d tok", r.Caps.CostUSD, r.Caps.Tokens)
		}
		fmt.Printf("#%-3d %s  %5.1f%%  resets %s  [%s]  %s  %s\n",
			r.ID, r.ObservedAt.In(loc).Format("2006-01-02 15:04"), r.Utilization,
			r.ResetsAt.In(loc).Format("2006-01-02 15:04"), r.Source, caps, r.Note)
	}
	return nil
}

func runCalibrateRemove(args []string) error {
	fs := flag.NewFlagSet("calibrate remove", flag.ExitOnError)
	id := fs.Int64("id", 0, "calibration id to remove (see `calibrate list`)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id <= 0 {
		return fmt.Errorf("--id is required")
	}
	st, _, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	ok, err := st.DeleteCalibration(*id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no calibration #%d", *id)
	}
	fmt.Printf("removed calibration #%d\n", *id)
	return nil
}

// --- limits ---

// limitEventJSON is the wire form of a stored rate-limit event.
type limitEventJSON struct {
	UUID       string    `json:"uuid"`
	Timestamp  time.Time `json:"timestamp"`
	Source     string    `json:"source"`
	SessionID  string    `json:"session_id"`
	Status     int       `json:"status"`
	Message    string    `json:"message,omitempty"`
	RateLimits string    `json:"rate_limits_json,omitempty"`
}

// limitsPayload is what `limits --json` emits. Calibrated=false (with no
// status) means "no usable calibration yet" — the GUI falls back to its
// assumed budget on that signal, so absence is a state, not an error.
type limitsPayload struct {
	Calibrated bool             `json:"calibrated"`
	Reason     string           `json:"reason,omitempty"` // why not calibrated
	Status     *limits.Status   `json:"status,omitempty"`
	Events     []limitEventJSON `json:"limit_events_in_window,omitempty"`
}

func runLimits(args []string) error {
	fs := flag.NewFlagSet("limits", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable JSON output")
	tz := fs.String("tz", "local", "timezone for display: local | utc | IANA name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	loc, err := resolveTZ(*tz)
	if err != nil {
		return err
	}

	st, _, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	payload, err := buildLimitsPayload(st, time.Now())
	if err != nil {
		return err
	}

	if *asJSON {
		return printJSON(payload)
	}
	printLimits(payload, loc)
	return nil
}

// buildLimitsPayload assembles the calibrated view: the newest weekly
// calibration that can derive a cap wins; older ones are only a fallback when
// the newest window had no visible consumption.
func buildLimitsPayload(st store.Store, now time.Time) (limitsPayload, error) {
	cals, err := st.Calibrations(limits.WindowWeekly)
	if err != nil {
		return limitsPayload{}, err
	}
	if len(cals) == 0 {
		return limitsPayload{Calibrated: false, Reason: "no calibration recorded"}, nil
	}

	var lastErr error
	for _, cal := range cals {
		period, err := limits.Period(cal.Window)
		if err != nil {
			lastErr = err
			continue
		}
		calCons, err := calibrationConsumption(st, cal, period)
		if err != nil {
			return limitsPayload{}, err
		}
		winStart, _ := limits.CurrentWindow(cal.ResetsAt, period, now)
		current, err := consumptionBetween(st, winStart.Unix(), now.Unix())
		if err != nil {
			return limitsPayload{}, err
		}
		status, err := limits.BuildStatus(cal, calCons, current, now)
		if err != nil {
			lastErr = err
			continue
		}

		evs, err := st.LimitEvents(winStart.Unix(), 0)
		if err != nil {
			return limitsPayload{}, err
		}
		events := make([]limitEventJSON, 0, len(evs))
		for _, e := range evs {
			events = append(events, limitEventJSON{
				UUID: e.UUID, Timestamp: e.Timestamp, Source: string(e.Source),
				SessionID: e.SessionID, Status: e.Status, Message: e.Message,
				RateLimits: e.RateLimitsJSON,
			})
		}
		return limitsPayload{Calibrated: true, Status: &status, Events: events}, nil
	}
	return limitsPayload{Calibrated: false, Reason: fmt.Sprintf("no usable calibration: %v", lastErr)}, nil
}

// humanDuration renders a duration as days + hours + minutes — "6d 6h59m" is
// readable where "150h59m0s" is not. Sub-day durations fall back to h/m.
func humanDuration(d time.Duration) string {
	d = max(d.Round(time.Minute), 0)
	days := int(d / (24 * time.Hour))
	rem := d % (24 * time.Hour)
	h := int(rem / time.Hour)
	m := int(rem % time.Hour / time.Minute)
	if days > 0 {
		return fmt.Sprintf("%dd %dh%02dm", days, h, m)
	}
	return fmt.Sprintf("%dh%02dm", h, m)
}

func printLimits(p limitsPayload, loc *time.Location) {
	if !p.Calibrated {
		fmt.Printf("not calibrated (%s)\n\n", p.Reason)
		fmt.Println("Read the official percentage from Claude Code's /usage screen, then:")
		fmt.Println("  claude-usage-lens calibrate add --utilization <pct> --resets-at <datetime>")
		return
	}
	s := p.Status
	f := func(t time.Time) string { return t.In(loc).Format("2006-01-02 15:04") }
	fmt.Printf("weekly window: %s → %s (resets in %s)\n",
		f(s.WindowStart), f(s.WindowEnd), humanDuration(time.Until(s.WindowEnd)))
	fmt.Printf("calibration:   %.1f%% observed %s (%.1f days ago, %s)\n",
		s.Calibration.Utilization, f(s.Calibration.ObservedAt), s.Calibration.AgeDays, s.Calibration.Source)
	fmt.Printf("derived cap:   $%.2f  /  %d tokens (in+out)\n", s.Caps.CostUSD, s.Caps.Tokens)
	fmt.Printf("consumed:      $%.2f (%.1f%%)  /  %d tokens (%.1f%%)\n",
		s.Consumed.CostUSD, s.UtilizationCostPct, s.Consumed.Tokens, s.UtilizationTokensPct)
	fmt.Printf("remaining:     $%.2f  /  %d tokens\n", s.Remaining.CostUSD, s.Remaining.Tokens)
	if len(p.Events) > 0 {
		fmt.Printf("\n%d rate-limit event(s) in this window:\n", len(p.Events))
		for _, e := range p.Events {
			fmt.Printf("  %s  HTTP %d  session %s\n", e.Timestamp.In(loc).Format("2006-01-02 15:04"), e.Status, e.SessionID)
		}
	}
	fmt.Println("\nCaps are DERIVED from your /usage reading against locally visible usage")
	fmt.Println("(Code + Cowork) — re-calibrate occasionally, and after plan/promo changes.")
}
