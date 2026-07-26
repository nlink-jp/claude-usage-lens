package cost

import (
	"math"
	"testing"

	"github.com/nlink-jp/claude-usage-lens/core/model"
	"github.com/nlink-jp/claude-usage-lens/core/pricing"
)

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// synthetic rates — NOT real Claude prices, chosen for clean arithmetic:
// $3/Mtok input, $15/Mtok output, cache read 0.1×, 1h 2×, 5m 1.25×, web search $0.01/req.
var testRates = pricing.Rates{
	InputPerMTok:           3.0,
	OutputPerMTok:          15.0,
	CacheReadMultiplier:    0.1,
	CacheWrite1hMultiplier: 2.0,
	CacheWrite5mMultiplier: 1.25,
	WebSearchPerReq:        0.01,
	WebFetchPerReq:         0.0,
}

func TestCompute(t *testing.T) {
	tests := []struct {
		name string
		u    model.Usage
		tier string
		want float64
	}{
		{
			name: "input and output",
			u:    model.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000},
			tier: "standard",
			want: 3.0 + 15.0,
		},
		{
			name: "cache read is 0.1x input",
			u:    model.Usage{CacheReadInputTokens: 1_000_000},
			tier: "standard",
			want: 0.3,
		},
		{
			name: "cache writes 1h=2x and 5m=1.25x",
			u:    model.Usage{CacheCreation1h: 1_000_000, CacheCreation5m: 1_000_000},
			tier: "standard",
			want: 6.0 + 3.75,
		},
		{
			name: "web search billed per request",
			u:    model.Usage{WebSearchRequests: 5},
			tier: "standard",
			want: 0.05,
		},
		{
			name: "batch tier halves the total",
			u:    model.Usage{InputTokens: 1_000_000},
			tier: "batch",
			want: 1.5,
		},
		{
			name: "empty usage costs nothing",
			u:    model.Usage{},
			tier: "standard",
			want: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Compute(tc.u, testRates, tc.tier, "standard")
			if !almostEqual(got, tc.want) {
				t.Errorf("Compute() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestComputeRecord_KnownModel(t *testing.T) {
	tbl := pricing.Table{"test-model": testRates}
	rec := model.UsageRecord{
		Model:       "test-model",
		ServiceTier: "standard",
		Usage:       model.Usage{InputTokens: 1_000_000},
	}
	if c := ComputeRecord(rec, tbl); !almostEqual(c.ListPriceUSD, 3.0) {
		t.Errorf("ComputeRecord() = %v, want 3.0", c.ListPriceUSD)
	}
}

func TestComputeRecord_SyntheticModelIsFree(t *testing.T) {
	tbl := pricing.Table{"test-model": testRates}
	rec := model.UsageRecord{Model: "<synthetic>", Usage: model.Usage{InputTokens: 999_999}}
	if c := ComputeRecord(rec, tbl); c.ListPriceUSD != 0 {
		t.Errorf("synthetic model cost = %v, want 0", c.ListPriceUSD)
	}
}

// fastRates adds a fast tier to testRates: $6/Mtok in, $30/Mtok out — double the
// standard pair, matching the real Opus 5 relationship ($5/$25 → $10/$50).
var fastRates = func() pricing.Rates {
	r := testRates
	r.FastInputPerMTok = 6.0
	r.FastOutputPerMTok = 30.0
	return r
}()

func TestCompute_FastModeUsesThePremiumPair(t *testing.T) {
	u := model.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}

	if got := Compute(u, fastRates, "standard", pricing.SpeedStandard); !almostEqual(got, 18.0) {
		t.Errorf("standard speed = %v, want 18.0 (3 + 15)", got)
	}
	if got := Compute(u, fastRates, "standard", pricing.SpeedFast); !almostEqual(got, 36.0) {
		t.Errorf("fast speed = %v, want 36.0 (6 + 30)", got)
	}
}

// Cache multipliers are relative to the *effective* input price, so a fast-mode
// cache read costs 0.1x the fast rate — not 0.1x the standard one.
func TestCompute_FastModeScalesCacheToo(t *testing.T) {
	u := model.Usage{CacheReadInputTokens: 1_000_000, CacheCreation1h: 1_000_000}

	// standard: 1M×3×0.1 + 1M×3×2 = 0.3 + 6 = 6.3
	if got := Compute(u, fastRates, "standard", pricing.SpeedStandard); !almostEqual(got, 6.3) {
		t.Errorf("standard cache = %v, want 6.3", got)
	}
	// fast: 1M×6×0.1 + 1M×6×2 = 0.6 + 12 = 12.6
	if got := Compute(u, fastRates, "standard", pricing.SpeedFast); !almostEqual(got, 12.6) {
		t.Errorf("fast cache = %v, want 12.6", got)
	}
}

// A fast-flagged record on a model with no fast tier bills at standard rates —
// the same thing the API does when a model ignores the fast request.
func TestCompute_FastOnModelWithoutFastTierFallsBack(t *testing.T) {
	u := model.Usage{InputTokens: 1_000_000}
	if got := Compute(u, testRates, "standard", pricing.SpeedFast); !almostEqual(got, 3.0) {
		t.Errorf("fast on a standard-only model = %v, want the standard 3.0", got)
	}
}

// An empty speed means "transcript predates the field", i.e. standard.
func TestCompute_UnknownSpeedIsStandard(t *testing.T) {
	u := model.Usage{InputTokens: 1_000_000}
	for _, speed := range []string{"", "turbo"} {
		if got := Compute(u, fastRates, "standard", speed); !almostEqual(got, 3.0) {
			t.Errorf("speed %q = %v, want the standard 3.0", speed, got)
		}
	}
}

func TestComputeRecord_CarriesSpeedThrough(t *testing.T) {
	tbl := pricing.Table{"test-model": fastRates}
	rec := model.UsageRecord{
		Model:       "test-model",
		ServiceTier: "standard",
		Speed:       pricing.SpeedFast,
		Usage:       model.Usage{InputTokens: 1_000_000},
	}
	if c := ComputeRecord(rec, tbl); !almostEqual(c.ListPriceUSD, 6.0) {
		t.Errorf("ComputeRecord() = %v, want 6.0 (fast rate)", c.ListPriceUSD)
	}
}
