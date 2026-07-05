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
			got := Compute(tc.u, testRates, tc.tier)
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
