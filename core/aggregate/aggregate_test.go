package aggregate

import (
	"testing"
	"time"

	"github.com/nlink-jp/claude-usage-lens/core/model"
)

func rec(model_ string, ts time.Time, in, out int64, usd float64) model.PricedRecord {
	return model.PricedRecord{
		UsageRecord: model.UsageRecord{
			Timestamp: ts,
			Model:     model_,
			Usage:     model.Usage{InputTokens: in, OutputTokens: out, CacheReadInputTokens: 5, CacheCreation1h: 3, CacheCreation5m: 2},
		},
		Cost: model.Cost{ListPriceUSD: usd},
	}
}

func TestAggregate_ByModel(t *testing.T) {
	day := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	recs := []model.PricedRecord{
		rec("claude-opus-4-8", day, 100, 50, 1.0),
		rec("claude-opus-4-8", day, 200, 60, 2.0),
		rec("claude-haiku-4-5", day, 10, 5, 0.1),
	}
	rows, err := Aggregate(recs, []Dimension{ByModel})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	// Sorted by key: claude-haiku-4-5 before claude-opus-4-8.
	if rows[0].Key != "claude-haiku-4-5" || rows[1].Key != "claude-opus-4-8" {
		t.Fatalf("rows not key-sorted: %q, %q", rows[0].Key, rows[1].Key)
	}
	opus := rows[1]
	if opus.Records != 2 || opus.InputTokens != 300 || opus.OutputTokens != 110 {
		t.Errorf("opus aggregation wrong: %+v", opus)
	}
	if opus.CostUSD != 3.0 {
		t.Errorf("opus cost = %v, want 3.0", opus.CostUSD)
	}
	if opus.CacheReadTokens != 10 || opus.CacheWriteTokens != 10 || opus.CacheTokens != 20 {
		t.Errorf("opus cache split wrong: %+v", opus)
	}
}

func TestAggregate_MultiDimAndDay(t *testing.T) {
	d1 := time.Date(2026, 7, 5, 1, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 7, 6, 1, 0, 0, 0, time.UTC)
	recs := []model.PricedRecord{
		rec("claude-opus-4-8", d1, 1, 1, 0.1),
		rec("claude-opus-4-8", d2, 1, 1, 0.1),
	}
	rows, err := Aggregate(recs, []Dimension{ByDay, ByModel})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (two days)", len(rows))
	}
	if rows[0].Key != "2026-07-05|claude-opus-4-8" {
		t.Errorf("composite key wrong: %q", rows[0].Key)
	}
}

func TestAggregate_WeekAndMonth(t *testing.T) {
	// 2026-07-05 is ISO week 27; 2026-08-01 is month 2026-08.
	recs := []model.PricedRecord{
		rec("claude-opus-4-8", time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC), 1, 1, 0.1),
		rec("claude-opus-4-8", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), 1, 1, 0.2),
	}
	wk, _ := Aggregate(recs, []Dimension{ByWeek})
	if len(wk) != 2 || wk[0].Key != "2026-W27" {
		t.Errorf("week bucket wrong: %+v", wk)
	}
	mo, _ := Aggregate(recs, []Dimension{ByMonth})
	if len(mo) != 2 || mo[0].Key != "2026-07" || mo[1].Key != "2026-08" {
		t.Errorf("month bucket wrong: %+v", mo)
	}
}

func TestSortRows(t *testing.T) {
	rows := []Row{
		{Key: "a", CostUSD: 1, Records: 30},
		{Key: "b", CostUSD: 3, Records: 10},
		{Key: "c", CostUSD: 2, Records: 20},
	}
	if err := SortRows(rows, "cost"); err != nil {
		t.Fatal(err)
	}
	if rows[0].Key != "b" || rows[1].Key != "c" || rows[2].Key != "a" {
		t.Errorf("cost desc sort wrong: %v %v %v", rows[0].Key, rows[1].Key, rows[2].Key)
	}
	if err := SortRows(rows, "key"); err != nil {
		t.Fatal(err)
	}
	if rows[0].Key != "a" {
		t.Errorf("key asc sort wrong: %v", rows[0].Key)
	}
	if err := SortRows(rows, "bogus"); err == nil {
		t.Error("expected error for unknown sort key")
	}
}

func TestSummarize(t *testing.T) {
	d1 := time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)
	recs := []model.PricedRecord{
		rec("claude-opus-4-8", d1, 10, 10, 100.0),
		rec("claude-opus-4-8", d1, 10, 10, 100.0), // 7/4 total 200
		rec("claude-opus-4-8", d2, 10, 10, 40.0),  // 7/5 total 40
	}
	s := Summarize(recs)
	if s.ActiveDays != 2 || s.FirstDay != "2026-07-04" || s.LastDay != "2026-07-05" {
		t.Errorf("period wrong: %+v", s)
	}
	if s.TotalUSD != 240.0 || s.DailyAvgUSD != 120.0 {
		t.Errorf("totals wrong: total=%v avg=%v", s.TotalUSD, s.DailyAvgUSD)
	}
	if s.PeakDay != "2026-07-04" || s.PeakUSD != 200.0 {
		t.Errorf("peak wrong: %v %v", s.PeakDay, s.PeakUSD)
	}
	if s.Projection30USD != 3600.0 { // 120 * 30
		t.Errorf("projection wrong: %v", s.Projection30USD)
	}
}

func TestParseDimensions(t *testing.T) {
	got, err := ParseDimensions("day,model")
	if err != nil || len(got) != 2 || got[0] != ByDay || got[1] != ByModel {
		t.Fatalf("ParseDimensions(day,model) = %v, %v", got, err)
	}
	if _, err := ParseDimensions("bogus"); err == nil {
		t.Error("expected error for unknown dimension")
	}
	if got, _ := ParseDimensions(""); len(got) != 1 || got[0] != ByDay {
		t.Errorf("empty should default to day, got %v", got)
	}
}
