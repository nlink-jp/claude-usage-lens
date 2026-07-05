// Package aggregate rolls priced records up by one or more dimensions.
package aggregate

import (
	"sort"
	"strings"

	"github.com/nlink-jp/claude-usage-lens/core/model"
)

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
	Key              string  `json:"key"`
	Records          int     `json:"records"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"` // 1h + 5m
	CacheTokens      int64   `json:"cache_tokens"`       // read + write
	CostUSD          float64 `json:"cost_usd"`
}

// Aggregate groups priced records by the given dimensions and sums tokens/cost.
// The composite key joins each dimension's value with "|". Rows are returned
// sorted by key. Pure function — it takes already-priced records, so it has no
// dependency on pricing or I/O. Passing no dimensions groups by day.
func Aggregate(recs []model.PricedRecord, dims []Dimension) ([]Row, error) {
	if len(dims) == 0 {
		dims = []Dimension{ByDay}
	}
	byKey := make(map[string]*Row)
	for i := range recs {
		r := &recs[i]
		key := keyFor(r, dims)
		row := byKey[key]
		if row == nil {
			row = &Row{Key: key}
			byKey[key] = row
		}
		write := r.Usage.CacheCreation1h + r.Usage.CacheCreation5m
		row.Records++
		row.InputTokens += r.Usage.InputTokens
		row.OutputTokens += r.Usage.OutputTokens
		row.CacheReadTokens += r.Usage.CacheReadInputTokens
		row.CacheWriteTokens += write
		row.CacheTokens += r.Usage.CacheReadInputTokens + write
		row.CostUSD += r.Cost.ListPriceUSD
	}

	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]Row, 0, len(keys))
	for _, k := range keys {
		out = append(out, *byKey[k])
	}
	return out, nil
}

func keyFor(r *model.PricedRecord, dims []Dimension) string {
	parts := make([]string, len(dims))
	for i, d := range dims {
		parts[i] = dimValue(r, d)
	}
	return strings.Join(parts, "|")
}

func dimValue(r *model.PricedRecord, d Dimension) string {
	switch d {
	case ByDay:
		if r.Timestamp.IsZero() {
			return "unknown"
		}
		return r.Timestamp.UTC().Format("2006-01-02")
	case BySession:
		return orUnknown(r.SessionID)
	case ByProject:
		return orUnknown(r.Project)
	case ByModel:
		return orUnknown(r.Model)
	case ByEntrypoint:
		return orUnknown(string(r.Entrypoint))
	default:
		return "unknown"
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// ParseDimensions maps a comma-separated flag value to Dimensions, validating
// each. Unknown names return an error naming the offender.
func ParseDimensions(csv string) ([]Dimension, error) {
	if strings.TrimSpace(csv) == "" {
		return []Dimension{ByDay}, nil
	}
	valid := map[string]Dimension{
		"day": ByDay, "session": BySession, "project": ByProject,
		"model": ByModel, "entrypoint": ByEntrypoint,
	}
	var dims []Dimension
	for _, part := range strings.Split(csv, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		d, ok := valid[part]
		if !ok {
			return nil, &unknownDimensionError{part}
		}
		dims = append(dims, d)
	}
	if len(dims) == 0 {
		dims = []Dimension{ByDay}
	}
	return dims, nil
}

type unknownDimensionError struct{ name string }

func (e *unknownDimensionError) Error() string {
	return "unknown group-by dimension: " + e.name + " (want day|session|project|model|entrypoint)"
}
