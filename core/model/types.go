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
	Usage       Usage
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
