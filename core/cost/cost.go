// Package cost computes list-price-equivalent (notional) cost from usage + rates.
// Every function here is pure — no I/O, no globals — so it is fully unit-testable
// and reusable by both the CLI and the future GUI.
package cost

import (
	"github.com/nlink-jp/claude-usage-lens/core/model"
	"github.com/nlink-jp/claude-usage-lens/core/pricing"
)

const perMillion = 1_000_000.0

// Compute returns the notional (API list-price) cost of a single usage record
// given the model's rates, service tier, and speed.
//
// speed selects the base price pair — fast mode is a premium tier on the same
// model — and the cache multipliers then apply on top of whichever pair won, so
// a cache read during a fast-mode turn costs 0.1× the *fast* input price.
//
// Token-type accounting (`in` / `out` = the effective prices for this speed):
//
//	input      = InputTokens          × in
//	output     = OutputTokens         × out
//	cache read = CacheReadInputTokens × in × CacheReadMultiplier
//	cache 1h   = CacheCreation1h      × in × CacheWrite1hMultiplier
//	cache 5m   = CacheCreation5m      × in × CacheWrite5mMultiplier
//	web tools  = requests             × per-request price
//
// The subtotal is scaled by the service-tier multiplier (e.g. batch = 0.5×).
// Batch and fast mode are mutually exclusive at the API level, so the two
// modifiers never actually stack.
func Compute(u model.Usage, r pricing.Rates, tier, speed string) float64 {
	perTok := func(n int64, ratePerMTok float64) float64 {
		return float64(n) / perMillion * ratePerMTok
	}
	in, out := r.Base(speed)
	subtotal := perTok(u.InputTokens, in) +
		perTok(u.OutputTokens, out) +
		perTok(u.CacheReadInputTokens, in*r.CacheReadMultiplier) +
		perTok(u.CacheCreation1h, in*r.CacheWrite1hMultiplier) +
		perTok(u.CacheCreation5m, in*r.CacheWrite5mMultiplier) +
		float64(u.WebSearchRequests)*r.WebSearchPerReq +
		float64(u.WebFetchRequests)*r.WebFetchPerReq

	return subtotal * pricing.TierMultiplier(tier)
}

// ComputeRecord resolves the model's rates from the table and returns a Cost.
// Records whose model is absent from the table (e.g. "<synthetic>") cost 0.
func ComputeRecord(rec model.UsageRecord, t pricing.Table) model.Cost {
	r, ok := t.Lookup(rec.Model)
	if !ok {
		return model.Cost{ListPriceUSD: 0, Tier: rec.ServiceTier}
	}
	return model.Cost{
		ListPriceUSD: Compute(rec.Usage, r, rec.ServiceTier, rec.Speed),
		Tier:         rec.ServiceTier,
	}
}
