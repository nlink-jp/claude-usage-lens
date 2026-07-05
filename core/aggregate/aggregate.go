// Package aggregate rolls priced records up by one or more dimensions.
package aggregate

import "github.com/nlink-jp/claude-usage-lens/core/model"

// Dimension is a group-by key.
type Dimension string

const (
	ByDay        Dimension = "day"
	BySession    Dimension = "session"
	ByProject    Dimension = "project"
	ByModel      Dimension = "model"
	ByEntrypoint Dimension = "entrypoint"
)

// Row is one aggregated bucket.
type Row struct {
	Key          string // composite key for the requested dimensions
	Records      int
	InputTokens  int64
	OutputTokens int64
	CacheTokens  int64 // read + 1h + 5m
	CostUSD      float64
}

// Aggregate groups priced records by the given dimensions and sums tokens/cost.
//
// TODO(phase1): build the composite key per record (e.g. day|model), fold into a
// map[string]*Row, then return rows sorted by key (or cost). Pure function — takes
// already-priced records so it has no dependency on pricing or I/O.
func Aggregate(recs []model.PricedRecord, dims []Dimension) ([]Row, error) {
	return nil, model.ErrNotImplemented
}
