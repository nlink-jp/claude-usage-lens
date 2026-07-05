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
// given the model's rates and service tier.
//
// Token-type accounting:
//
//	input      = InputTokens          × InputPerMTok
//	output     = OutputTokens         × OutputPerMTok
//	cache read = CacheReadInputTokens × InputPerMTok × CacheReadMultiplier
//	cache 1h   = CacheCreation1h      × InputPerMTok × CacheWrite1hMultiplier
//	cache 5m   = CacheCreation5m      × InputPerMTok × CacheWrite5mMultiplier
//	web tools  = requests             × per-request price
//
// The subtotal is scaled by the service-tier multiplier (e.g. batch = 0.5×).
func Compute(u model.Usage, r pricing.Rates, tier string) float64 {
	perTok := func(n int64, ratePerMTok float64) float64 {
		return float64(n) / perMillion * ratePerMTok
	}
	subtotal := perTok(u.InputTokens, r.InputPerMTok) +
		perTok(u.OutputTokens, r.OutputPerMTok) +
		perTok(u.CacheReadInputTokens, r.InputPerMTok*r.CacheReadMultiplier) +
		perTok(u.CacheCreation1h, r.InputPerMTok*r.CacheWrite1hMultiplier) +
		perTok(u.CacheCreation5m, r.InputPerMTok*r.CacheWrite5mMultiplier) +
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
		ListPriceUSD: Compute(rec.Usage, r, rec.ServiceTier),
		Tier:         rec.ServiceTier,
	}
}
