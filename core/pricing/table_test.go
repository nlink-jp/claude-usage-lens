package pricing

import "testing"

func TestDefaultTable_KnownModels(t *testing.T) {
	tbl := Default()
	cases := []struct {
		model      string
		wantInput  float64
		wantOutput float64
	}{
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
