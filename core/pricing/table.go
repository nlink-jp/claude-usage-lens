// Package pricing holds per-model rate tables and token-type multipliers.
// It is self-contained (no I/O, no other core deps) so the cost engine stays pure.
package pricing

import "strings"

// Rates are USD prices per 1,000,000 tokens for a single model, plus the
// cache/token-type multipliers applied relative to the input price.
type Rates struct {
	InputPerMTok           float64 // base input, USD / 1M tok
	OutputPerMTok          float64 // base output, USD / 1M tok
	CacheReadMultiplier    float64 // × the effective input price (typically 0.1)
	CacheWrite1hMultiplier float64 // × the effective input price (typically 2.0)
	CacheWrite5mMultiplier float64 // × the effective input price (typically 1.25)
	WebSearchPerReq        float64 // USD per server web_search request
	WebFetchPerReq         float64 // USD per server web_fetch request

	// Fast-mode prices (`speed: "fast"`). Zero means the model has no fast tier,
	// in which case a fast-flagged record is billed at the standard rates — which
	// is also what the API does on a model that silently ignores the request.
	FastInputPerMTok  float64
	FastOutputPerMTok float64
}

// Speed selects which price pair applies to a record.
const (
	SpeedStandard = "standard"
	SpeedFast     = "fast"
)

// Base returns the input/output prices in effect for the given speed. Fast mode
// is a premium on the same model, so the cache multipliers apply on top of
// whichever pair this returns — a cache read during a fast-mode turn costs
// 0.1× the *fast* input price, not the standard one.
//
// An unrecognised speed, or fast on a model with no fast tier, falls back to
// the standard pair: over-charging usage we cannot confirm would be worse than
// reporting it at the rate the API would have billed.
func (r Rates) Base(speed string) (input, output float64) {
	if speed == SpeedFast && r.HasFast() {
		return r.FastInputPerMTok, r.FastOutputPerMTok
	}
	return r.InputPerMTok, r.OutputPerMTok
}

// HasFast reports whether the model has a fast-mode price tier.
func (r Rates) HasFast() bool {
	return r.FastInputPerMTok > 0 || r.FastOutputPerMTok > 0
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

// Standard cache multipliers, relative to the base input rate: a cache read
// costs 0.1×, a 5-minute ephemeral cache write 1.25×, and a 1-hour ephemeral
// cache write 2×. The write multipliers hold for every model. The read
// multiplier has one footnoted exception on Anthropic's pricing page: Claude
// Fable 5.1 and Claude Mythos 5.1 charge cache reads at 0.025× ($0.25/MTok
// against a $10 base). That exception is what makes the multiplier a per-model
// field rather than a constant — see withCacheRead.
const (
	cacheReadMult        = 0.10
	cacheReadMultFable51 = 0.025
	cacheWrite5mMult     = 1.25
	cacheWrite1hMult     = 2.00
)

// webSearchPerReq is Anthropic's web-search charge: $10 per 1,000 searches =
// $0.01 per request, the same for every model. Web fetch has no extra charge.
const webSearchPerReq = 0.01

// fastInputPerMTok / fastOutputPerMTok are the fast-mode premium prices, the
// same for every model that offers the tier.
const (
	fastInputPerMTok  = 10.0
	fastOutputPerMTok = 50.0
)

// StandardRates returns a Rates for the given base input/output prices with
// Anthropic's standard cache multipliers and web-search charge. It is the
// starting point for a model defined purely in the user's config, so specifying
// only the two base prices still yields correct cache accounting.
func StandardRates(input, output float64) Rates { return rates(input, output) }

// rates builds a Rates from base input/output prices, applying the standard
// cache multipliers and the flat web-search per-request charge.
func rates(input, output float64) Rates {
	return Rates{
		InputPerMTok:           input,
		OutputPerMTok:          output,
		CacheReadMultiplier:    cacheReadMult,
		CacheWrite1hMultiplier: cacheWrite1hMult,
		CacheWrite5mMultiplier: cacheWrite5mMult,
		WebSearchPerReq:        webSearchPerReq,
		// WebFetchPerReq stays 0 — web fetch has no additional charge.
		// Fast prices stay 0 — most models have no fast tier.
	}
}

// withFast marks a model as fast-mode capable at the premium prices.
func withFast(r Rates) Rates {
	r.FastInputPerMTok = fastInputPerMTok
	r.FastOutputPerMTok = fastOutputPerMTok
	return r
}

// withCacheRead overrides the cache-read multiplier for a model whose reads are
// priced off the standard 0.1× (Claude Fable 5.1 / Mythos 5.1 at 0.025×). The
// write multipliers and everything else stay as rates() set them.
func withCacheRead(r Rates, mult float64) Rates {
	r.CacheReadMultiplier = mult
	return r
}

// VerifiedOn is the date the built-in table was last checked, column by column
// and footnotes included, against Anthropic's pricing page. `models` prints it
// so a reader can judge how stale the table may be.
const VerifiedOn = "2026-09-02"

// Default returns the built-in rate table.
//
// Prices are USD per 1M tokens, verified on VerifiedOn against Anthropic's live
// pricing page. Override or extend via config.toml [pricing]. Unknown models
// (including "<synthetic>") are absent by design → zero cost.
//
// No long-context tier: the pricing page states Claude 4.6 and later models
// include the full 1M context window at standard pricing — a 900k-token request
// costs the same per token as a 9k one. So a flat per-model rate is correct, and
// the "[1m]" variant tag is priced as the base model (confirmed empirically:
// claude-opus-4-8[1m] reconstructs at exactly $5/$25).
//
// Claude Fable 5.1 / Mythos 5.1 share Fable 5's $10/$50 and cache-write
// multipliers but charge cache reads at 0.025× ($0.25/MTok) — the pricing
// page's single footnoted exception to the 0.1× rule. In a long agentic
// session cache reads dominate the token count, so this one multiplier moves
// the notional cost by roughly 2×; copying Fable 5's entry would be wrong.
//
// claude-sonnet-5 is $2/$10: the launch price was announced as introductory
// through 2026-08-31, but the pricing page now states the scheduled increase
// to $3/$15 will not occur, so $2/$10 is the standard price.
//
// Fast mode (`speed: "fast"`) is a $10/$50 premium tier offered on Opus 5 and
// Opus 4.8 only. Opus 4.7 rejects the request outright and Opus 4.6 silently
// serves it at standard speed and standard rates, so neither carries fast
// prices here — a fast-flagged record on any other model bills as standard.
func Default() Table {
	return Table{
		// Fable / Mythos tier — 5.1 has the cheaper cache reads (see above).
		"claude-fable-5-1":  withCacheRead(rates(10, 50), cacheReadMultFable51),
		"claude-mythos-5-1": withCacheRead(rates(10, 50), cacheReadMultFable51),
		"claude-fable-5":    rates(10, 50),
		"claude-mythos-5":   rates(10, 50),
		// Opus tier — 5 and 4.8 additionally offer fast mode.
		"claude-opus-5":   withFast(rates(5, 25)),
		"claude-opus-4-8": withFast(rates(5, 25)),
		"claude-opus-4-7": rates(5, 25),
		"claude-opus-4-6": rates(5, 25),
		"claude-opus-4-5": rates(5, 25),
		// Sonnet tier
		"claude-sonnet-5":   rates(2, 10),
		"claude-sonnet-4-6": rates(3, 15),
		"claude-sonnet-4-5": rates(3, 15),
		// Haiku tier
		"claude-haiku-4-5": rates(1, 5),
	}
}

// Lookup returns the rates for a model and whether it is known. It tries an
// exact match, then normalized variants — stripping a trailing variant tag like
// "[1m]" (1M-context) and/or a dated-snapshot suffix like "-20251001". So
// "claude-opus-4-8[1m]" and "claude-haiku-4-5-20251001" both resolve to their
// base alias. Unknown models (including "<synthetic>") report false → zero cost.
func (t Table) Lookup(model string) (Rates, bool) {
	for _, c := range candidates(model) {
		if r, ok := t[c]; ok {
			return r, true
		}
	}
	return Rates{}, false
}

// candidates returns the model plus its normalized forms, most-specific first.
func candidates(m string) []string {
	out := []string{m}
	if b := stripBracketSuffix(m); b != m {
		out = append(out, b)
	}
	for _, c := range append([]string{}, out...) {
		if d := stripDateSuffix(c); d != c {
			out = append(out, d)
		}
	}
	return out
}

// stripBracketSuffix removes a trailing "[...]" variant tag (e.g. "[1m]").
func stripBracketSuffix(m string) string {
	if strings.HasSuffix(m, "]") {
		if i := strings.LastIndexByte(m, '['); i > 0 {
			return m[:i]
		}
	}
	return m
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
