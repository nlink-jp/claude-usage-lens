package pricing

import "testing"

func TestDefaultTable_KnownModels(t *testing.T) {
	tbl := Default()
	cases := []struct {
		model      string
		wantInput  float64
		wantOutput float64
	}{
		{"claude-opus-5", 5, 25},
		{"claude-opus-4-8", 5, 25},
		{"claude-fable-5", 10, 50},
		{"claude-sonnet-5", 3, 15},
		{"claude-haiku-4-5", 1, 5},
	}
	for _, c := range cases {
		r, ok := tbl.Lookup(c.model)
		if !ok {
			t.Errorf("%s: not found in default table", c.model)
			continue
		}
		if r.InputPerMTok != c.wantInput || r.OutputPerMTok != c.wantOutput {
			t.Errorf("%s: got %v/%v, want %v/%v", c.model, r.InputPerMTok, r.OutputPerMTok, c.wantInput, c.wantOutput)
		}
		if r.CacheReadMultiplier != 0.10 || r.CacheWrite5mMultiplier != 1.25 || r.CacheWrite1hMultiplier != 2.00 {
			t.Errorf("%s: unexpected cache multipliers: %+v", c.model, r)
		}
		if r.WebSearchPerReq != 0.01 || r.WebFetchPerReq != 0 {
			t.Errorf("%s: web tool rates wrong: search=%v fetch=%v (want 0.01 / 0)", c.model, r.WebSearchPerReq, r.WebFetchPerReq)
		}
	}
}

func TestLookup_DatedSnapshotSuffix(t *testing.T) {
	tbl := Default()
	r, ok := tbl.Lookup("claude-haiku-4-5-20251001")
	if !ok {
		t.Fatal("dated haiku snapshot should resolve to the base alias")
	}
	if r.InputPerMTok != 1 || r.OutputPerMTok != 5 {
		t.Errorf("dated snapshot resolved to wrong rates: %+v", r)
	}
	// @-separated snapshot (Vertex style) also resolves.
	if _, ok := tbl.Lookup("claude-opus-4-5@20251101"); !ok {
		t.Error("@-dated opus snapshot should resolve to the base alias")
	}
	// [1m] variant tag (1M context) resolves to base rates.
	if r, ok := tbl.Lookup("claude-opus-4-8[1m]"); !ok || r.InputPerMTok != 5 {
		t.Errorf("[1m] variant should resolve to base opus rates: %+v ok=%v", r, ok)
	}
	if _, ok := tbl.Lookup("claude-fable-5[1m]"); !ok {
		t.Error("fable [1m] variant should resolve")
	}
	// A non-date suffix must NOT be stripped.
	if _, ok := tbl.Lookup("claude-opus-4-8-turbo"); ok {
		t.Error("non-date suffix should not resolve")
	}
}

func TestDefaultTable_UnknownModelIsFree(t *testing.T) {
	tbl := Default()
	if _, ok := tbl.Lookup("<synthetic>"); ok {
		t.Error("<synthetic> should not be in the table (must cost 0)")
	}
	if _, ok := tbl.Lookup("gpt-4"); ok {
		t.Error("non-Claude model should not be in the table")
	}
}

func TestTierMultiplier(t *testing.T) {
	if TierMultiplier("batch") != 0.5 {
		t.Errorf("batch tier = %v, want 0.5", TierMultiplier("batch"))
	}
	if TierMultiplier("standard") != 1.0 || TierMultiplier("priority") != 1.0 {
		t.Error("standard/priority tier should be 1.0")
	}
}

// Fast mode is a $10/$50 premium tier, offered only on Opus 5 and Opus 4.8.
// Opus 4.7 rejects a fast request and Opus 4.6 serves it at standard rates, so
// neither may carry fast prices.
func TestDefaultTable_FastModeTier(t *testing.T) {
	tbl := Default()

	for _, m := range []string{"claude-opus-5", "claude-opus-4-8"} {
		r, ok := tbl.Lookup(m)
		if !ok {
			t.Fatalf("%s missing from the table", m)
		}
		if !r.HasFast() {
			t.Errorf("%s should offer fast mode", m)
		}
		if r.FastInputPerMTok != 10 || r.FastOutputPerMTok != 50 {
			t.Errorf("%s fast rates = %v/%v, want 10/50", m, r.FastInputPerMTok, r.FastOutputPerMTok)
		}
		in, out := r.Base(SpeedFast)
		if in != 10 || out != 50 {
			t.Errorf("%s Base(fast) = %v/%v, want 10/50", m, in, out)
		}
		if in, out := r.Base(SpeedStandard); in != 5 || out != 25 {
			t.Errorf("%s Base(standard) = %v/%v, want 5/25", m, in, out)
		}
	}

	for _, m := range []string{"claude-opus-4-7", "claude-opus-4-6", "claude-sonnet-5", "claude-fable-5"} {
		r, ok := tbl.Lookup(m)
		if !ok {
			t.Fatalf("%s missing from the table", m)
		}
		if r.HasFast() {
			t.Errorf("%s must not have a fast tier", m)
		}
		// A fast-flagged record on such a model bills at standard rates.
		in, _ := r.Base(SpeedFast)
		if in != r.InputPerMTok {
			t.Errorf("%s Base(fast) = %v, want the standard %v", m, in, r.InputPerMTok)
		}
	}
}

// The [1m] / dated-suffix normalization must preserve the fast tier.
func TestLookup_VariantKeepsFastRates(t *testing.T) {
	r, ok := Default().Lookup("claude-opus-5[1m]")
	if !ok || !r.HasFast() {
		t.Errorf("opus-5[1m] should resolve with its fast tier: %+v ok=%v", r, ok)
	}
}
