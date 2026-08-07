// Package model defines the OS-neutral data types shared across claude-usage-lens.
// Nothing here performs I/O; these types flow from collect → cost → aggregate → store.
package model

import "time"

// Source identifies which product produced a session log.
type Source string

const (
	SourceCode   Source = "code"   // Claude Code — ~/.claude/projects (CLI + desktop + sdk)
	SourceCowork Source = "cowork" // Claude Cowork — local-agent-mode sessions
)

// Entrypoint sub-classifies where a record originated. For the `code` source it
// mirrors the transcript's `entrypoint` field.
type Entrypoint string

const (
	EntrypointCLI     Entrypoint = "cli"
	EntrypointDesktop Entrypoint = "claude-desktop"
	EntrypointSDKPy   Entrypoint = "sdk-py"
	EntrypointCowork  Entrypoint = "cowork"
)

// SyntheticModel marks a locally generated response (no API call). It is never
// billable, so it is excluded at parse time and must not be reported as an
// unpriced model.
const SyntheticModel = "<synthetic>"

// Billable reports whether a model id should carry a cost — i.e. whether its
// absence from the pricing table is a gap worth warning about rather than the
// intended zero.
func Billable(modelID string) bool {
	return modelID != "" && modelID != SyntheticModel
}

// Usage is the raw token breakdown extracted from one assistant message's
// `message.usage`. All counts are absolute token counts (not deltas).
type Usage struct {
	InputTokens          int64
	OutputTokens         int64
	CacheReadInputTokens int64
	CacheCreation1h      int64 // cache_creation.ephemeral_1h_input_tokens
	CacheCreation5m      int64 // cache_creation.ephemeral_5m_input_tokens
	WebSearchRequests    int64 // server_tool_use.web_search_requests
	WebFetchRequests     int64 // server_tool_use.web_fetch_requests
}

// UsageRecord is one deduplicated assistant turn with its provenance.
// Project is taken from the in-record `cwd` — never decoded from the
// platform-specific directory name — so records stay OS-neutral.
type UsageRecord struct {
	MessageID   string // msg_... — the global dedup key
	RequestID   string // req_...
	Timestamp   time.Time
	Source      Source
	Entrypoint  Entrypoint
	Host        string // local machine identity; reserved for future multi-machine rollup
	SessionID   string
	Project     string // in-record cwd
	Model       string // e.g. claude-opus-4-8; "<synthetic>" carries no cost
	ServiceTier string // standard | priority | batch
	// Speed is the transcript's message.usage.speed: "fast" for a fast-mode turn
	// (a premium price tier on Opus 5 / 4.8), otherwise "standard". Empty means
	// the transcript predates the field, which is equivalent to standard.
	Speed string
	Usage Usage
}

// LimitEvent is one rate-limit rejection observed in a transcript — a
// `type:system, subtype:api_error` record whose status is 429 (or that carries
// a non-null rateLimits payload). It is ground truth for "the quota was
// exhausted at this instant" (ADR-0001). RateLimitsJSON is preserved verbatim
// because the payload schema has never been observed populated; nothing is
// interpreted from it, so a future schema cannot break ingestion.
type LimitEvent struct {
	UUID           string // the transcript record uuid — the dedup key
	Timestamp      time.Time
	Source         Source
	SessionID      string
	Status         int    // HTTP status from the error payload (429)
	Message        string // error message text, truncated at parse time
	RateLimitsJSON string // raw error.rateLimits JSON; "" when null/absent
}

// Calibration is one observed real-utilization reading (ADR-0001): at
// ObservedAt the official /usage screen showed Utilization percent consumed on
// Window, with the next reset at ResetsAt. Effective caps are DERIVED from a
// calibration at query time (consumption in the window ÷ utilization) — never
// stored — so repricing or late ingestion automatically improves them.
type Calibration struct {
	ID          int64
	CreatedAt   time.Time
	ObservedAt  time.Time
	ResetsAt    time.Time // the next reset instant as shown by /usage; anchors the cadence
	Window      string    // "weekly" (the only window in v1)
	Utilization float64   // percent, 0 < u ≤ 100
	Source      string    // "manual" | "limit_event"
	Note        string
}

// Cost is a computed list-price-equivalent (notional) cost. It is the API list
// price, NOT an actual billed amount — subscription (Max/Pro) usage is not billed
// per token. Always present this as "notional" to the user.
type Cost struct {
	ListPriceUSD float64
	Tier         string
}

// PricedRecord pairs a usage record with its computed cost. This is what the
// store persists and the aggregator rolls up.
type PricedRecord struct {
	UsageRecord
	Cost Cost
}
