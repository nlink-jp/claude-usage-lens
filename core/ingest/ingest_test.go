package ingest

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nlink-jp/claude-usage-lens/core/model"
	"github.com/nlink-jp/claude-usage-lens/core/pricing"
	"github.com/nlink-jp/claude-usage-lens/core/store"
)

// seed opens a temp store holding one code record per model id, each with
// 1M input tokens (so cost == the model's input rate) and no cost yet.
func seed(t *testing.T, models ...string) store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	ts := time.Unix(1_770_000_000, 0).UTC()
	recs := make([]model.PricedRecord, 0, len(models))
	for i, m := range models {
		recs = append(recs, model.PricedRecord{
			UsageRecord: model.UsageRecord{
				MessageID: "msg_" + m + string(rune('a'+i)),
				Timestamp: ts,
				Source:    model.SourceCode,
				Model:     m,
				Usage:     model.Usage{InputTokens: 1_000_000},
			},
			Cost: model.Cost{ListPriceUSD: 0},
		})
	}
	if _, err := st.Upsert(recs); err != nil {
		t.Fatal(err)
	}
	return st
}

// A model released after the build lands in the store at $0. Once it is added to
// the pricing table, Reprice must repair that history in place — the regression
// that made claude-opus-5 report $0.
func TestReprice_FixesRecordsStoredBeforeTheModelWasPriced(t *testing.T) {
	st := seed(t, "claude-opus-5")

	// Before: the table doesn't know the model, so it stays free and is reported.
	stale := pricing.Table{"claude-opus-4-8": pricing.Default()["claude-opus-4-8"]}
	res, err := Reprice(st, stale, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.NewTotalUSD != 0 {
		t.Errorf("unpriced model should cost 0, got %v", res.NewTotalUSD)
	}
	if res.UnknownModels["claude-opus-5"] != 1 {
		t.Errorf("unpriced model not reported: %v", res.UnknownModels)
	}

	// After: the current table prices it at $5 / 1M input tokens.
	res, err = Reprice(st, pricing.Default(), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed != 1 || res.NewTotalUSD != 5 {
		t.Errorf("reprice = %d changed, $%v; want 1 changed, $5", res.Changed, res.NewTotalUSD)
	}
	if len(res.UnknownModels) != 0 {
		t.Errorf("nothing should be unpriced now: %v", res.UnknownModels)
	}
}

func TestReprice_DryRunDoesNotWrite(t *testing.T) {
	st := seed(t, "claude-opus-5")

	res, err := Reprice(st, pricing.Default(), true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed != 1 {
		t.Fatalf("dry run should report 1 change, got %d", res.Changed)
	}
	rows, err := st.Query(store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Cost.ListPriceUSD != 0 {
		t.Errorf("dry run wrote to the store: %v", rows[0].Cost.ListPriceUSD)
	}
}

// "<synthetic>" is intentionally free, so it must never be reported as a gap.
func TestReprice_SyntheticIsNotReportedAsUnpriced(t *testing.T) {
	st := seed(t, model.SyntheticModel)

	res, err := Reprice(st, pricing.Default(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.UnknownModels) != 0 {
		t.Errorf("synthetic should not be reported: %v", res.UnknownModels)
	}
}
