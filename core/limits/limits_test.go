package limits

import (
	"strings"
	"testing"
	"time"

	"github.com/nlink-jp/claude-usage-lens/core/model"
)

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func weeklyCal(observed, resets string, u float64) model.Calibration {
	return model.Calibration{
		ObservedAt:  ts(observed),
		ResetsAt:    ts(resets),
		Window:      WindowWeekly,
		Utilization: u,
		Source:      "manual",
	}
}

func TestValidate(t *testing.T) {
	period, err := Period(WindowWeekly)
	if err != nil {
		t.Fatal(err)
	}
	ok := weeklyCal("2026-08-08T12:00:00Z", "2026-08-13T09:00:00Z", 45)
	if err := Validate(ok, period); err != nil {
		t.Errorf("valid calibration rejected: %v", err)
	}

	cases := []struct {
		name string
		c    model.Calibration
		want string
	}{
		{"zero utilization", weeklyCal("2026-08-08T12:00:00Z", "2026-08-13T09:00:00Z", 0), "outside (0, 100]"},
		{"over 100", weeklyCal("2026-08-08T12:00:00Z", "2026-08-13T09:00:00Z", 101), "outside (0, 100]"},
		{"observed after reset", weeklyCal("2026-08-14T00:00:00Z", "2026-08-13T09:00:00Z", 45), "not before resets-at"},
		{"observed at reset", weeklyCal("2026-08-13T09:00:00Z", "2026-08-13T09:00:00Z", 45), "not before resets-at"},
		{"outside window", weeklyCal("2026-08-01T00:00:00Z", "2026-08-13T09:00:00Z", 45), "outside the window"},
	}
	for _, tc := range cases {
		err := Validate(tc.c, period)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want containing %q", tc.name, err, tc.want)
		}
	}
}

func TestCurrentWindow(t *testing.T) {
	period := 7 * 24 * time.Hour
	anchor := ts("2026-08-13T09:00:00Z") // a future reset seen on /usage

	cases := []struct {
		now       string
		wantStart string
	}{
		// Inside the anchor window itself.
		{"2026-08-08T12:00:00Z", "2026-08-06T09:00:00Z"},
		// Exactly at the anchor reset → next window starts.
		{"2026-08-13T09:00:00Z", "2026-08-13T09:00:00Z"},
		// Two cadence steps later.
		{"2026-08-27T08:59:59Z", "2026-08-20T09:00:00Z"},
		// Before the anchor window (historical query).
		{"2026-08-01T00:00:00Z", "2026-07-30T09:00:00Z"},
		// Exactly on an earlier cadence boundary.
		{"2026-07-30T09:00:00Z", "2026-07-30T09:00:00Z"},
		// Just before an earlier cadence boundary.
		{"2026-07-30T08:59:59Z", "2026-07-23T09:00:00Z"},
	}
	for _, tc := range cases {
		start, end := CurrentWindow(anchor, period, ts(tc.now))
		if !start.Equal(ts(tc.wantStart)) {
			t.Errorf("now %s: start = %s, want %s", tc.now, start.Format(time.RFC3339), tc.wantStart)
		}
		if !end.Equal(start.Add(period)) {
			t.Errorf("now %s: end != start+period", tc.now)
		}
		n := ts(tc.now)
		if n.Before(start) || !n.Before(end) {
			t.Errorf("now %s not inside [%s, %s)", tc.now, start.Format(time.RFC3339), end.Format(time.RFC3339))
		}
	}
}

func TestDeriveCaps(t *testing.T) {
	c := weeklyCal("2026-08-08T12:00:00Z", "2026-08-13T09:00:00Z", 40)

	caps, err := DeriveCaps(c, Consumption{CostUSD: 100, Tokens: 2_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if caps.CostUSD != 250 { // 100 / 0.40
		t.Errorf("cost cap = %v, want 250", caps.CostUSD)
	}
	if caps.Tokens != 5_000_000 {
		t.Errorf("token cap = %v, want 5000000", caps.Tokens)
	}

	if _, err := DeriveCaps(c, Consumption{}); err == nil {
		t.Error("zero consumption should not derive a cap")
	}
}

func TestBuildStatus(t *testing.T) {
	// Calibrated at 40% with $100 consumed; later in the SAME window $150 is
	// consumed in total → 60% of the derived $250 cap, $100 remaining.
	c := weeklyCal("2026-08-08T12:00:00Z", "2026-08-13T09:00:00Z", 40)
	now := ts("2026-08-10T00:00:00Z")

	s, err := BuildStatus(c,
		Consumption{CostUSD: 100, Tokens: 1_000_000},
		Consumption{CostUSD: 150, Tokens: 2_000_000},
		now)
	if err != nil {
		t.Fatal(err)
	}
	if !s.WindowStart.Equal(ts("2026-08-06T09:00:00Z")) || !s.WindowEnd.Equal(ts("2026-08-13T09:00:00Z")) {
		t.Errorf("window = %s → %s", s.WindowStart.Format(time.RFC3339), s.WindowEnd.Format(time.RFC3339))
	}
	if s.Caps.CostUSD != 250 || s.Caps.Tokens != 2_500_000 {
		t.Errorf("caps = %+v", s.Caps)
	}
	if s.UtilizationCostPct != 60 {
		t.Errorf("cost utilization = %v, want 60", s.UtilizationCostPct)
	}
	if s.UtilizationTokensPct != 80 {
		t.Errorf("token utilization = %v, want 80", s.UtilizationTokensPct)
	}
	if s.Remaining.CostUSD != 100 || s.Remaining.Tokens != 500_000 {
		t.Errorf("remaining = %+v", s.Remaining)
	}
	if s.Calibration.AgeDays < 1.49 || s.Calibration.AgeDays > 1.51 {
		t.Errorf("age = %v days, want ~1.5", s.Calibration.AgeDays)
	}
}

// Consumption beyond the cap floors remaining at zero rather than going negative.
func TestBuildStatus_OverCap(t *testing.T) {
	c := weeklyCal("2026-08-08T12:00:00Z", "2026-08-13T09:00:00Z", 90)
	s, err := BuildStatus(c,
		Consumption{CostUSD: 90, Tokens: 900},
		Consumption{CostUSD: 120, Tokens: 1_200},
		ts("2026-08-12T00:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Remaining.CostUSD != 0 || s.Remaining.Tokens != 0 {
		t.Errorf("remaining should floor at zero: %+v", s.Remaining)
	}
	if s.UtilizationCostPct <= 100 {
		t.Errorf("over-cap utilization should exceed 100%%: %v", s.UtilizationCostPct)
	}
}

// A status computed in a LATER window than the calibration's still uses the
// calibration-window consumption for the caps, and the new window for usage.
func TestBuildStatus_LaterWindow(t *testing.T) {
	c := weeklyCal("2026-08-08T12:00:00Z", "2026-08-13T09:00:00Z", 50)
	now := ts("2026-08-15T00:00:00Z") // one reset has passed

	s, err := BuildStatus(c,
		Consumption{CostUSD: 50, Tokens: 500},
		Consumption{CostUSD: 10, Tokens: 100},
		now)
	if err != nil {
		t.Fatal(err)
	}
	if !s.WindowStart.Equal(ts("2026-08-13T09:00:00Z")) {
		t.Errorf("window start = %s, want the post-reset window", s.WindowStart.Format(time.RFC3339))
	}
	if s.Caps.CostUSD != 100 {
		t.Errorf("cap = %v, want 100", s.Caps.CostUSD)
	}
	if s.UtilizationCostPct != 10 {
		t.Errorf("utilization = %v, want 10", s.UtilizationCostPct)
	}
}
