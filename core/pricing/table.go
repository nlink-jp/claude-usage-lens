// Package pricing holds per-model rate tables and token-type multipliers.
// It is self-contained (no I/O, no other core deps) so the cost engine stays pure.
package pricing

// Rates are USD prices per 1,000,000 tokens for a single model, plus the
// cache/token-type multipliers applied relative to the input price.
type Rates struct {
	InputPerMTok           float64 // base input, USD / 1M tok
	OutputPerMTok          float64 // base output, USD / 1M tok
	CacheReadMultiplier    float64 // × InputPerMTok (typically 0.1)
	CacheWrite1hMultiplier float64 // × InputPerMTok (typically 2.0)
	CacheWrite5mMultiplier float64 // × InputPerMTok (typically 1.25)
	WebSearchPerReq        float64 // USD per server web_search request
	WebFetchPerReq         float64 // USD per server web_fetch request
}

// TierMultiplier scales the whole cost by service tier.
func TierMultiplier(tier string) float64 {
	switch tier {
	case "batch":
		return 0.5
	default: // standard, priority
		return 1.0
	}
}

// Table maps a model name to its Rates. The built-in Default is overridden by
// the user's config.toml [pricing] section at load time.
type Table map[string]Rates

// Default returns the built-in rate table.
//
// TODO(phase1): populate with the current published Claude prices. Rates must be
// fetched at implementation time (see /claude-api) and NOT filled from memory —
// prices drift. Deliberately empty for now so unknown/synthetic models cost 0.
func Default() Table {
	return Table{}
}

// Lookup returns the rates for a model and whether it is known. Unknown models
// (including "<synthetic>") report false and are treated as zero-cost.
func (t Table) Lookup(model string) (Rates, bool) {
	r, ok := t[model]
	return r, ok
}
