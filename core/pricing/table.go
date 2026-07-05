// Package pricing holds per-model rate tables and token-type multipliers.
// It is self-contained (no I/O, no other core deps) so the cost engine stays pure.
package pricing

import "strings"

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

// Standard cache/tier multipliers. These are model-independent in Anthropic's
// pricing: a cache read costs 0.1× the base input rate, a 5-minute ephemeral
// cache write 1.25×, and a 1-hour ephemeral cache write 2×.
const (
	cacheReadMult    = 0.10
	cacheWrite5mMult = 1.25
	cacheWrite1hMult = 2.00
)

// rates builds a Rates from base input/output prices, applying the standard
// cache multipliers. Web server-tool per-request prices default to 0 — set them
// via config.toml [pricing] if you bill for web_search / web_fetch (the current
// published per-request rate isn't modeled here to avoid baking a stale number).
func rates(input, output float64) Rates {
	return Rates{
		InputPerMTok:           input,
		OutputPerMTok:          output,
		CacheReadMultiplier:    cacheReadMult,
		CacheWrite1hMultiplier: cacheWrite1hMult,
		CacheWrite5mMultiplier: cacheWrite5mMult,
	}
}

// Default returns the built-in rate table.
//
// Prices are USD per 1M tokens, current as of 2026-07-05 (source: Anthropic
// published pricing). Override or extend via config.toml [pricing]. Unknown
// models (including "<synthetic>") are absent by design → zero cost.
//
// Note: claude-sonnet-5 has an introductory $2/$10 rate through 2026-08-31; the
// durable $3/$15 is baked here. Override in config if you want intro-rate costing.
func Default() Table {
	return Table{
		// Fable / Mythos tier
		"claude-fable-5":  rates(10, 50),
		"claude-mythos-5": rates(10, 50),
		// Opus tier
		"claude-opus-4-8": rates(5, 25),
		"claude-opus-4-7": rates(5, 25),
		"claude-opus-4-6": rates(5, 25),
		"claude-opus-4-5": rates(5, 25),
		// Sonnet tier
		"claude-sonnet-5":   rates(3, 15),
		"claude-sonnet-4-6": rates(3, 15),
		"claude-sonnet-4-5": rates(3, 15),
		// Haiku tier
		"claude-haiku-4-5": rates(1, 5),
	}
}

// Lookup returns the rates for a model and whether it is known. It first tries
// an exact match, then retries with any trailing dated-snapshot suffix stripped
// (e.g. "claude-haiku-4-5-20251001" → "claude-haiku-4-5"), since Claude Code
// logs some models by their dated ID. Unknown models (including "<synthetic>")
// report false and are treated as zero-cost.
func (t Table) Lookup(model string) (Rates, bool) {
	if r, ok := t[model]; ok {
		return r, true
	}
	if base := stripDateSuffix(model); base != model {
		if r, ok := t[base]; ok {
			return r, true
		}
	}
	return Rates{}, false
}

// stripDateSuffix removes a trailing "-YYYYMMDD" or "@YYYYMMDD" snapshot suffix.
func stripDateSuffix(m string) string {
	for _, sep := range []byte{'-', '@'} {
		i := strings.LastIndexByte(m, sep)
		if i <= 0 || i != len(m)-9 { // need sep + exactly 8 trailing chars
			continue
		}
		allDigits := true
		for _, c := range m[i+1:] {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return m[:i]
		}
	}
	return m
}
