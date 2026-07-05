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
