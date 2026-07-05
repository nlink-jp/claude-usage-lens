// Package aggregate rolls priced records up by one or more dimensions.
package aggregate

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nlink-jp/claude-usage-lens/core/model"
)

// Dimension is a group-by key.
type Dimension string

const (
	ByHour       Dimension = "hour"
	ByDay        Dimension = "day"
	ByWeek       Dimension = "week"
	ByMonth      Dimension = "month"
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
// dependency on pricing or I/O. Passing no dimensions groups by day. Time
// dimensions (hour/day/week/month) are bucketed in loc, the caller's timezone.
func Aggregate(recs []model.PricedRecord, dims []Dimension, loc *time.Location) ([]Row, error) {
	if len(dims) == 0 {
		dims = []Dimension{ByDay}
	}
	byKey := make(map[string]*Row)
	for i := range recs {
		r := &recs[i]
		key := keyFor(r, dims, loc)
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

func keyFor(r *model.PricedRecord, dims []Dimension, loc *time.Location) string {
	parts := make([]string, len(dims))
	for i, d := range dims {
		parts[i] = dimValue(r, d, loc)
	}
	return strings.Join(parts, "|")
}

func dimValue(r *model.PricedRecord, d Dimension, loc *time.Location) string {
	switch d {
	case ByHour:
		if r.Timestamp.IsZero() {
			return "unknown"
		}
		return r.Timestamp.In(loc).Format("2006-01-02 15h")
	case ByDay:
		if r.Timestamp.IsZero() {
			return "unknown"
		}
		return r.Timestamp.In(loc).Format("2006-01-02")
	case ByWeek:
		if r.Timestamp.IsZero() {
			return "unknown"
		}
		y, w := r.Timestamp.In(loc).ISOWeek()
		return fmt.Sprintf("%04d-W%02d", y, w)
	case ByMonth:
		if r.Timestamp.IsZero() {
			return "unknown"
		}
		return r.Timestamp.In(loc).Format("2006-01")
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
		"hour": ByHour, "day": ByDay, "week": ByWeek, "month": ByMonth,
		"session": BySession, "project": ByProject,
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

// IsTimeDimension reports whether d buckets by time (and so has a well-defined
// contiguous ordering that DenseTimeRows can fill).
func IsTimeDimension(d Dimension) bool {
	switch d {
	case ByHour, ByDay, ByWeek, ByMonth:
		return true
	}
	return false
}

// timeKeyer returns the key formatter for a time dimension, bucketed in loc.
// ok is false for non-time dimensions.
func timeKeyer(d Dimension, loc *time.Location) (key func(time.Time) string, ok bool) {
	switch d {
	case ByHour:
		return func(t time.Time) string { return t.In(loc).Format("2006-01-02 15h") }, true
	case ByDay:
		return func(t time.Time) string { return t.In(loc).Format("2006-01-02") }, true
	case ByWeek:
		return func(t time.Time) string { y, w := t.In(loc).ISOWeek(); return fmt.Sprintf("%04d-W%02d", y, w) }, true
	case ByMonth:
		return func(t time.Time) string { return t.In(loc).Format("2006-01") }, true
	}
	return nil, false
}

// DenseTimeRows fills the gaps in a single-time-dimension roll-up so the series
// is contiguous: every bucket between start and end (inclusive) is present in
// loc, missing ones as zero-cost rows. Existing rows are preserved unchanged, and
// any out-of-range keys already present (e.g. "unknown") are kept. The result is
// sorted by key, matching Aggregate. For a non-time dimension, or when end is
// before start, rows are returned unchanged.
func DenseTimeRows(rows []Row, dim Dimension, start, end time.Time, loc *time.Location) []Row {
	key, ok := timeKeyer(dim, loc)
	if !ok || end.Before(start) {
		return rows
	}

	have := make(map[string]Row, len(rows))
	for _, r := range rows {
		have[r.Key] = r
	}

	// Enumerate buckets in loc, aligned to the local hour/day boundary. AddDate
	// handles DST correctly (each step lands on the next local midnight).
	s := start.In(loc)
	var cur time.Time
	var advance func(time.Time) time.Time
	if dim == ByHour {
		cur = time.Date(s.Year(), s.Month(), s.Day(), s.Hour(), 0, 0, 0, loc)
		advance = func(t time.Time) time.Time { return t.Add(time.Hour) }
	} else {
		cur = time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, loc)
		advance = func(t time.Time) time.Time { return t.AddDate(0, 0, 1) }
	}

	seen := make(map[string]bool)
	order := make([]string, 0, len(rows))
	for !cur.After(end) {
		if k := key(cur); !seen[k] {
			seen[k] = true
			order = append(order, k)
		}
		cur = advance(cur)
	}
	// Keep any existing keys the enumeration didn't cover (e.g. "unknown").
	for _, r := range rows {
		if !seen[r.Key] {
			seen[r.Key] = true
			order = append(order, r.Key)
		}
	}
	sort.Strings(order)

	out := make([]Row, 0, len(order))
	for _, k := range order {
		if r, ok := have[k]; ok {
			out = append(out, r)
		} else {
			out = append(out, Row{Key: k})
		}
	}
	return out
}

type unknownDimensionError struct{ name string }

func (e *unknownDimensionError) Error() string {
	return "unknown group-by dimension: " + e.name + " (want hour|day|week|month|session|project|model|entrypoint)"
}

// SortRows orders rows in place. "key" (default) sorts ascending by key; a
// metric name (cost|input|output|records|cache) sorts descending so the biggest
// contributors come first. An unknown key returns an error.
func SortRows(rows []Row, by string) error {
	switch by {
	case "", "key":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	case "cost":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].CostUSD > rows[j].CostUSD })
	case "input":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].InputTokens > rows[j].InputTokens })
	case "output":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].OutputTokens > rows[j].OutputTokens })
	case "records":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Records > rows[j].Records })
	case "cache":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].CacheTokens > rows[j].CacheTokens })
	default:
		return fmt.Errorf("unknown --sort %q (want key|cost|input|output|records|cache)", by)
	}
	return nil
}

// Summary is a period-level roll-up used by `report --summary`.
type Summary struct {
	FirstDay        string  `json:"first_day"`
	LastDay         string  `json:"last_day"`
	ActiveDays      int     `json:"active_days"`
	Records         int     `json:"records"`
	InputTokens     int64   `json:"input_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	CacheTokens     int64   `json:"cache_tokens"`
	TotalUSD        float64 `json:"total_usd"`
	DailyAvgUSD     float64 `json:"daily_avg_usd"`
	PeakDay         string  `json:"peak_day"`
	PeakUSD         float64 `json:"peak_usd"`
	Projection30USD float64 `json:"projection_30d_usd"`
}

// Summarize computes period statistics from priced records. Totals include every
// record; day-based metrics (active days, peak, projection) ignore records with
// no timestamp (bucketed as "unknown"). Days are bucketed in loc. The 30-day
// projection is the average cost per active day × 30.
func Summarize(recs []model.PricedRecord, loc *time.Location) Summary {
	dayRows, _ := Aggregate(recs, []Dimension{ByDay}, loc)
	var s Summary
	for _, r := range dayRows {
		s.Records += r.Records
		s.InputTokens += r.InputTokens
		s.OutputTokens += r.OutputTokens
		s.CacheTokens += r.CacheTokens
		s.TotalUSD += r.CostUSD
		if r.Key == "unknown" {
			continue
		}
		s.ActiveDays++
		if s.FirstDay == "" || r.Key < s.FirstDay {
			s.FirstDay = r.Key
		}
		if r.Key > s.LastDay {
			s.LastDay = r.Key
		}
		if r.CostUSD > s.PeakUSD {
			s.PeakUSD = r.CostUSD
			s.PeakDay = r.Key
		}
	}
	if s.ActiveDays > 0 {
		s.DailyAvgUSD = s.TotalUSD / float64(s.ActiveDays)
		s.Projection30USD = s.DailyAvgUSD * 30
	}
	return s
}
