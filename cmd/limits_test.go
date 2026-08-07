package cmd

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nlink-jp/claude-usage-lens/core/limits"
	"github.com/nlink-jp/claude-usage-lens/core/model"
	"github.com/nlink-jp/claude-usage-lens/core/store"
)

func TestHumanDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{150*time.Hour + 59*time.Minute, "6d 6h59m"},
		{26 * time.Hour, "1d 2h00m"},
		{3*time.Hour + 5*time.Minute, "3h05m"},
		{0, "0h00m"},
		{-time.Hour, "0h00m"}, // a passed reset never renders negative
	}
	for _, tc := range cases {
		if got := humanDuration(tc.d); got != tc.want {
			t.Errorf("humanDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// buildLimitsPayload: no calibration → calibrated=false; a usable calibration
// → calibrated status; an underivable newest calibration (no consumption in
// its window) → falls back to the older usable point.
func TestBuildLimitsPayload(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)

	p, err := buildLimitsPayload(st, now)
	if err != nil {
		t.Fatal(err)
	}
	if p.Calibrated {
		t.Fatal("empty store should not be calibrated")
	}

	// $40 consumed before the observation instant, in the calibration window.
	obs := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	resets := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	if _, err := st.Upsert([]model.PricedRecord{{
		UsageRecord: model.UsageRecord{MessageID: "m1", Timestamp: obs.Add(-time.Hour),
			Source: model.SourceCode, Model: "claude-opus-5",
			Usage: model.Usage{InputTokens: 1000, OutputTokens: 500}},
		Cost: model.Cost{ListPriceUSD: 40},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddCalibration(model.Calibration{
		ObservedAt: obs, ResetsAt: resets, Window: limits.WindowWeekly,
		Utilization: 40, Source: "manual",
	}); err != nil {
		t.Fatal(err)
	}

	p, err = buildLimitsPayload(st, now)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Calibrated || p.Status == nil {
		t.Fatalf("expected calibrated payload: %+v", p)
	}
	if p.Status.Caps.CostUSD != 100 { // 40 / 0.40
		t.Errorf("cap = %v, want 100", p.Status.Caps.CostUSD)
	}
	if p.Status.Consumed.CostUSD != 40 || p.Status.UtilizationCostPct != 40 {
		t.Errorf("consumed = %+v (%v%%)", p.Status.Consumed, p.Status.UtilizationCostPct)
	}

	// A newer calibration whose window holds no consumption cannot derive a
	// cap; the older usable point must win over a dead payload.
	if _, err := st.AddCalibration(model.Calibration{
		ObservedAt: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC),
		ResetsAt:   time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC),
		Window:     limits.WindowWeekly, Utilization: 10, Source: "manual",
	}); err != nil {
		t.Fatal(err)
	}
	p, err = buildLimitsPayload(st, now)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Calibrated || p.Status.Caps.CostUSD != 100 {
		t.Errorf("fallback to older usable calibration failed: %+v", p)
	}

	// A limit event inside the current window is surfaced.
	if _, err := st.UpsertLimitEvents([]model.LimitEvent{{
		UUID: "ev-1", Timestamp: now.Add(-time.Hour), Source: model.SourceCode, Status: 429,
	}}); err != nil {
		t.Fatal(err)
	}
	p, _ = buildLimitsPayload(st, now)
	if len(p.Events) != 1 || p.Events[0].UUID != "ev-1" {
		t.Errorf("limit event not surfaced: %+v", p.Events)
	}
}
