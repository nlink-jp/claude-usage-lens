package pricing

import "testing"

// Prices as published on Anthropic's pricing page (verified 2026-10-14). The
// cache-read multiplier is per model: 0.1× everywhere except Claude Fable 5.1 /
// Mythos 5.1 (0.025×) and Claude Opus 5.5 / Sonnet 5.5 (0.05×), whose reads
// are footnoted.
// The write multipliers are the same for every model.
func TestDefaultTable_KnownModels(t *testing.T) {
	tbl := Default()
	cases := []struct {
		model         string
		wantInput     float64
		wantOutput    float64
		wantCacheRead float64
	}{
		{"claude-fable-5-1", 10, 50, 0.025},
		{"claude-mythos-5-1", 10, 50, 0.025},
		{"claude-fable-5", 10, 50, 0.10},
		{"claude-opus-5-5", 4, 20, 0.05},
		{"claude-opus-5", 5, 25, 0.10},
		{"claude-opus-4-8", 5, 25, 0.10},
		{"claude-sonnet-5-5", 2, 10, 0.05}, // $0.10 cache read, footnoted with Opus 5.5
		{"claude-sonnet-5", 2, 10, 0.10},   // the scheduled rise to $3/$15 was cancelled
		{"claude-sonnet-4-6", 3, 15, 0.10},
		{"claude-haiku-5-5", 0.10, 0.50, 0.10}, // under 100k prompt tokens; see the long-context test
		{"claude-haiku-4-5", 1, 5, 0.10},
		// Retired on the first-party API, kept for older transcripts.
		{"claude-opus-4-1", 15, 75, 0.10},
		{"claude-opus-4", 15, 75, 0.10},
		{"claude-sonnet-4", 3, 15, 0.10},
		{"claude-3-5-haiku", 0.8, 4, 0.10},
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
		if r.CacheReadMultiplier != c.wantCacheRead {
			t.Errorf("%s: cache read multiplier = %v, want %v", c.model, r.CacheReadMultiplier, c.wantCacheRead)
		}
		if r.CacheWrite5mMultiplier != 1.25 || r.CacheWrite1hMultiplier != 2.00 {
			t.Errorf("%s: unexpected cache write multipliers: %+v", c.model, r)
		}
		if r.WebSearchPerReq != 0.01 || r.WebFetchPerReq != 0 {
			t.Errorf("%s: web tool rates wrong: search=%v fetch=%v (want 0.01 / 0)", c.model, r.WebSearchPerReq, r.WebFetchPerReq)
		}
	}
}

// The Fable 5.1 entry must not be a copy of Fable 5's: the only difference is
// the cache-read multiplier, and it is the difference that matters.
func TestDefaultTable_Fable51DiffersOnlyInCacheRead(t *testing.T) {
	tbl := Default()
	f51, _ := tbl.Lookup("claude-fable-5-1")
	f5, _ := tbl.Lookup("claude-fable-5")
	if f51.CacheReadMultiplier == f5.CacheReadMultiplier {
		t.Fatalf("fable-5-1 cache read = %v, must differ from fable-5's %v", f51.CacheReadMultiplier, f5.CacheReadMultiplier)
	}
	f51.CacheReadMultiplier = f5.CacheReadMultiplier
	if f51 != f5 {
		t.Errorf("fable-5-1 differs from fable-5 beyond the cache read: %+v vs %+v", f51, f5)
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
	// The 5.1 variant keeps its own (cheaper) cache-read multiplier, and its
	// trailing "-1" is not mistaken for a date suffix.
	if r, ok := tbl.Lookup("claude-fable-5-1[1m]"); !ok || r.CacheReadMultiplier != 0.025 {
		t.Errorf("fable-5-1[1m] should resolve with the 0.025x cache read: %+v ok=%v", r, ok)
	}
	// Retired models resolve from their dated API IDs.
	for m, in := range map[string]float64{
		"claude-opus-4-1-20250805":  15,
		"claude-opus-4-20250514":    15,
		"claude-sonnet-4-20250514":  3,
		"claude-3-5-haiku-20241022": 0.8,
	} {
		if r, ok := tbl.Lookup(m); !ok || r.InputPerMTok != in {
			t.Errorf("%s should resolve at $%v input: %+v ok=%v", m, in, r, ok)
		}
	}
	// Opus 5.5's trailing "-5" is not a date suffix either: it must resolve to
	// its own entry, never fall through to Opus 5.
	if r, ok := tbl.Lookup("claude-opus-5-5[1m]"); !ok || r.InputPerMTok != 4 || r.CacheReadMultiplier != 0.05 {
		t.Errorf("opus-5-5[1m] should resolve to Opus 5.5's own rates: %+v ok=%v", r, ok)
	}
	// Sonnet 5.5's [1m] variant resolves too. (Its rates equal Sonnet 5's, so
	// this cannot tell the two entries apart; stripDateSuffix only strips an
	// 8-digit suffix, which is what keeps "-5" from falling through.)
	if r, ok := tbl.Lookup("claude-sonnet-5-5[1m]"); !ok || r != tbl["claude-sonnet-5-5"] {
		t.Errorf("sonnet-5-5[1m] should resolve to Sonnet 5.5's own rates: %+v ok=%v", r, ok)
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

// Fast mode is a premium tier offered only on Opus 5.5 ($8/$40) and Opus 5 /
// Opus 4.8 ($10/$50) — the fast pair is per model, not a shared constant.
// Opus 4.7 rejects a fast request and Opus 4.6 serves it at standard rates, so
// neither may carry fast prices.
func TestDefaultTable_FastModeTier(t *testing.T) {
	tbl := Default()

	fast := []struct {
		model           string
		stdIn, stdOut   float64
		fastIn, fastOut float64
	}{
		{"claude-opus-5-5", 4, 20, 8, 40},
		{"claude-opus-5", 5, 25, 10, 50},
		{"claude-opus-4-8", 5, 25, 10, 50},
	}
	for _, c := range fast {
		r, ok := tbl.Lookup(c.model)
		if !ok {
			t.Fatalf("%s missing from the table", c.model)
		}
		if !r.HasFast() {
			t.Errorf("%s should offer fast mode", c.model)
		}
		if in, out := r.Base(SpeedFast, 0); in != c.fastIn || out != c.fastOut {
			t.Errorf("%s Base(fast) = %v/%v, want %v/%v", c.model, in, out, c.fastIn, c.fastOut)
		}
		if in, out := r.Base(SpeedStandard, 0); in != c.stdIn || out != c.stdOut {
			t.Errorf("%s Base(standard) = %v/%v, want %v/%v", c.model, in, out, c.stdIn, c.stdOut)
		}
	}

	for _, m := range []string{"claude-opus-4-7", "claude-opus-4-6", "claude-sonnet-5-5", "claude-sonnet-5", "claude-haiku-5-5", "claude-fable-5", "claude-fable-5-1", "claude-mythos-5-1"} {
		r, ok := tbl.Lookup(m)
		if !ok {
			t.Fatalf("%s missing from the table", m)
		}
		if r.HasFast() {
			t.Errorf("%s must not have a fast tier", m)
		}
		// A fast-flagged record on such a model bills at standard rates.
		in, _ := r.Base(SpeedFast, 0)
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

// Claude Haiku 5.5 is the only model priced by prompt length: $0.10/$0.50 up
// to 100,000 prompt tokens, $0.50/$2.50 over it. "Over" is strict, so exactly
// 100,000 stays in the lower tier.
func TestDefaultTable_Haiku55LongContextTier(t *testing.T) {
	r, ok := Default().Lookup("claude-haiku-5-5")
	if !ok {
		t.Fatal("claude-haiku-5-5 missing from the table")
	}
	if !r.HasLongContext() || r.LongContextThreshold != 100_000 {
		t.Fatalf("haiku-5-5 should have a 100k long-context tier: %+v", r)
	}
	cases := []struct {
		prompt  int64
		in, out float64
	}{
		{0, 0.10, 0.50},
		{100_000, 0.10, 0.50},
		{100_001, 0.50, 2.50},
		{900_000, 0.50, 2.50},
	}
	for _, c := range cases {
		if in, out := r.Base(SpeedStandard, c.prompt); in != c.in || out != c.out {
			t.Errorf("Base(standard, %d) = %v/%v, want %v/%v", c.prompt, in, out, c.in, c.out)
		}
	}
	// No fast tier: a fast-flagged record still follows the prompt-length tier.
	if in, out := r.Base(SpeedFast, 200_000); in != 0.50 || out != 2.50 {
		t.Errorf("Base(fast, 200k) = %v/%v, want the long pair 0.50/2.50", in, out)
	}
	// The variant tags must keep the tier.
	for _, m := range []string{"claude-haiku-5-5[1m]", "claude-haiku-5-5-20261007"} {
		if v, ok := Default().Lookup(m); !ok || v != r {
			t.Errorf("%s should resolve to Haiku 5.5's own rates: %+v ok=%v", m, v, ok)
		}
	}
}

// Every other model is flat across its context window.
func TestDefaultTable_OnlyHaiku55HasLongContext(t *testing.T) {
	for m, r := range Default() {
		if r.HasLongContext() != (m == "claude-haiku-5-5") {
			t.Errorf("%s: HasLongContext = %v", m, r.HasLongContext())
		}
	}
}

// A threshold without prices is not a tier — it must not bill long prompts at $0.
func TestHasLongContext_ThresholdWithoutPrices(t *testing.T) {
	r := rates(1, 5)
	r.LongContextThreshold = 100_000
	if r.HasLongContext() {
		t.Fatal("a threshold with no long prices should not count as a tier")
	}
	if in, out := r.Base(SpeedStandard, 500_000); in != 1 || out != 5 {
		t.Errorf("Base = %v/%v, want the standard 1/5", in, out)
	}
}

// Fast wins over the long-context tier: fast pricing covers the full window.
func TestBase_FastWinsOverLongContext(t *testing.T) {
	r := withLongContext(withFast(rates(1, 5), 2, 10), 100_000, 3, 15)
	if in, out := r.Base(SpeedFast, 500_000); in != 2 || out != 10 {
		t.Errorf("Base(fast, 500k) = %v/%v, want the fast 2/10", in, out)
	}
	if in, out := r.Base(SpeedStandard, 500_000); in != 3 || out != 15 {
		t.Errorf("Base(standard, 500k) = %v/%v, want the long 3/15", in, out)
	}
}
