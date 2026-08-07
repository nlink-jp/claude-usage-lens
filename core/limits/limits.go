// Package limits derives real-quota state from calibration points (ADR-0001).
//
// The official remaining quota is server-side and exposed nowhere locally, so
// it cannot be collected from logs. What CAN be had is a calibration: the user
// reads the official utilization off Claude Code's /usage screen, and this
// package divides our own measured window consumption by that percentage to
// obtain an effective cap. Everything here is pure — consumption is computed by
// the caller from the store — so the math is testable without I/O.
package limits

import (
	"fmt"
	"time"

	"github.com/nlink-jp/claude-usage-lens/core/model"
)

// WindowWeekly is the only calibration window in v1. The 5-hour window resets
// too fast for manual calibration to stay meaningful (ADR-0001).
const WindowWeekly = "weekly"

// weeklyPeriod is the weekly window length. The reset cadence is a fixed
// 168-hour interval anchored at the calibration's ResetsAt instant — an
// absolute-duration cadence, deliberately not calendar arithmetic: the server
// resets on elapsed time, not on a wall-clock weekday.
const weeklyPeriod = 7 * 24 * time.Hour

// Period returns the window length for a window name.
func Period(window string) (time.Duration, error) {
	if window == WindowWeekly {
		return weeklyPeriod, nil
	}
	return 0, fmt.Errorf("unknown window %q (only %q is supported)", window, WindowWeekly)
}

// Consumption is a window's summed usage on both budget bases the GUI
// supports: notional cost and input+output tokens.
type Consumption struct {
	CostUSD float64 `json:"cost_usd"`
	Tokens  int64   `json:"tokens"` // input + output (cache excluded, as in the GUI's weekly basis)
}

// Add accumulates one priced record into the consumption.
func (c *Consumption) Add(r model.PricedRecord) {
	c.CostUSD += r.Cost.ListPriceUSD
	c.Tokens += r.Usage.InputTokens + r.Usage.OutputTokens
}

// Caps are the derived effective limits: what 100 % utilization corresponds to
// on each basis. They are "local-visible" caps — consumption only counts what
// local logs record, and the calibration absorbs the invisible remainder
// (claude.ai web/mobile) as long as the usage mix stays roughly stable.
type Caps struct {
	CostUSD float64 `json:"cost_usd"`
	Tokens  int64   `json:"tokens"`
}

// Validate checks a calibration for derivability. period is the window length.
func Validate(c model.Calibration, period time.Duration) error {
	if c.Utilization <= 0 || c.Utilization > 100 {
		return fmt.Errorf("utilization %.1f%% is outside (0, 100] — a 0%% reading cannot anchor a cap", c.Utilization)
	}
	if c.ObservedAt.IsZero() || c.ResetsAt.IsZero() {
		return fmt.Errorf("calibration needs both an observed-at and a resets-at instant")
	}
	if !c.ObservedAt.Before(c.ResetsAt) {
		return fmt.Errorf("observed-at %s is not before resets-at %s — /usage shows the NEXT reset",
			c.ObservedAt.Format(time.RFC3339), c.ResetsAt.Format(time.RFC3339))
	}
	if c.ResetsAt.Sub(c.ObservedAt) > period {
		return fmt.Errorf("observed-at %s is more than %s before resets-at %s — outside the window",
			c.ObservedAt.Format(time.RFC3339), period, c.ResetsAt.Format(time.RFC3339))
	}
	return nil
}

// CalibrationWindow returns the window that contains the calibration's
// observed-at instant: [ResetsAt−period, ResetsAt).
func CalibrationWindow(c model.Calibration, period time.Duration) (start, end time.Time) {
	return c.ResetsAt.Add(-period), c.ResetsAt
}

// CurrentWindow returns the window containing now, on the cadence anchored at
// anchorReset (resets occur at anchorReset + k·period for integer k, k may be
// negative). Works whether anchorReset is in the past or the future.
func CurrentWindow(anchorReset time.Time, period time.Duration, now time.Time) (start, end time.Time) {
	// Number of whole periods from the anchor's window start to now. Duration
	// division truncates toward zero, which lands one period late for instants
	// before the anchor — the After check steps back exactly then.
	anchorStart := anchorReset.Add(-period)
	k := now.Sub(anchorStart) / period
	start = anchorStart.Add(k * period)
	if start.After(now) {
		start = start.Add(-period)
	}
	return start, start.Add(period)
}

// DeriveCaps turns a calibration plus the consumption measured over
// [window start, observed-at] into effective caps. A zero consumption cannot
// anchor a cap (utilization > 0 with nothing visible locally means the usage
// happened outside the logs we can see).
func DeriveCaps(c model.Calibration, calWindow Consumption) (Caps, error) {
	frac := c.Utilization / 100
	if calWindow.CostUSD <= 0 && calWindow.Tokens <= 0 {
		return Caps{}, fmt.Errorf("no local consumption found in the calibration window ending %s — cannot derive a cap",
			c.ResetsAt.Format(time.RFC3339))
	}
	return Caps{
		CostUSD: calWindow.CostUSD / frac,
		Tokens:  int64(float64(calWindow.Tokens) / frac),
	}, nil
}

// Status is the calibrated view of the current window — what `limits` prints
// and the GUI renders. Percentages are estimates anchored on the calibration;
// they are NOT the live official numbers.
type Status struct {
	Window      string    `json:"window"`
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"` // the next reset

	Calibration struct {
		ObservedAt  time.Time `json:"observed_at"`
		ResetsAt    time.Time `json:"resets_at"`
		Utilization float64   `json:"utilization_pct"`
		Source      string    `json:"source"`
		AgeDays     float64   `json:"age_days"`
	} `json:"calibration"`

	Caps     Caps        `json:"caps"`
	Consumed Consumption `json:"consumed"`

	UtilizationCostPct   float64 `json:"utilization_cost_pct"`
	UtilizationTokensPct float64 `json:"utilization_tokens_pct"`

	Remaining Caps `json:"remaining"` // caps minus consumed, floored at zero
}

// BuildStatus assembles the calibrated Status for the window containing now.
// calWindow is the consumption over [calibration window start, observed-at];
// current is the consumption over [current window start, now].
func BuildStatus(c model.Calibration, calWindow, current Consumption, now time.Time) (Status, error) {
	period, err := Period(c.Window)
	if err != nil {
		return Status{}, err
	}
	if err := Validate(c, period); err != nil {
		return Status{}, err
	}
	caps, err := DeriveCaps(c, calWindow)
	if err != nil {
		return Status{}, err
	}

	var s Status
	s.Window = c.Window
	s.WindowStart, s.WindowEnd = CurrentWindow(c.ResetsAt, period, now)
	s.Calibration.ObservedAt = c.ObservedAt
	s.Calibration.ResetsAt = c.ResetsAt
	s.Calibration.Utilization = c.Utilization
	s.Calibration.Source = c.Source
	s.Calibration.AgeDays = now.Sub(c.ObservedAt).Hours() / 24
	s.Caps = caps
	s.Consumed = current
	if caps.CostUSD > 0 {
		s.UtilizationCostPct = current.CostUSD / caps.CostUSD * 100
	}
	if caps.Tokens > 0 {
		s.UtilizationTokensPct = float64(current.Tokens) / float64(caps.Tokens) * 100
	}
	s.Remaining = Caps{
		CostUSD: max(0, caps.CostUSD-current.CostUSD),
		Tokens:  max(0, caps.Tokens-current.Tokens),
	}
	return s, nil
}
