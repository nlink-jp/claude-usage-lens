// Package audit reads Cowork audit.jsonl logs, which carry Anthropic's own
// pre-computed cost (total_cost_usd per completed query, plus a modelUsage
// breakdown). It is the ground truth the `verify` command cross-checks our
// computed cost against.
package audit

import (
	"bufio"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nlink-jp/claude-usage-lens/core/model"
)

// Ground is the authoritative cost extracted from one audit.jsonl.
type Ground struct {
	TotalUSD float64            // Σ result.total_cost_usd
	ByModel  map[string]float64 // model → Σ modelUsage.costUSD
	Results  int                // number of result events
}

type rawResult struct {
	Type         string  `json:"type"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	ModelUsage   map[string]struct {
		CostUSD float64 `json:"costUSD"`
	} `json:"modelUsage"`
}

// Parse reads an audit.jsonl and sums the cost across its result events.
func Parse(path string) (Ground, error) {
	f, err := os.Open(path)
	if err != nil {
		return Ground{}, err
	}
	defer f.Close()
	return parseReader(f)
}

func parseReader(r io.Reader) (Ground, error) {
	g := Ground{ByModel: map[string]float64{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimRight(sc.Text(), "\r"))
		if line == "" {
			continue
		}
		var res rawResult
		if err := json.Unmarshal([]byte(line), &res); err != nil {
			continue // tolerant
		}
		if res.Type != "result" {
			continue
		}
		g.Results++
		g.TotalUSD += res.TotalCostUSD
		for m, u := range res.ModelUsage {
			g.ByModel[m] += u.CostUSD
		}
	}
	return g, sc.Err()
}

// rawRecord captures the audit fields ParseRecords needs: the init record's cwd
// and every result event's per-model usage + authoritative cost.
type rawRecord struct {
	Type           string `json:"type"`
	Cwd            string `json:"cwd"`
	SessionID      string `json:"session_id"`
	AuditTimestamp string `json:"_audit_timestamp"`
	UUID           string `json:"uuid"`
	RequestID      string `json:"request_id"`
	ModelUsage     map[string]struct {
		InputTokens              int64   `json:"inputTokens"`
		OutputTokens             int64   `json:"outputTokens"`
		CacheReadInputTokens     int64   `json:"cacheReadInputTokens"`
		CacheCreationInputTokens int64   `json:"cacheCreationInputTokens"`
		WebSearchRequests        int64   `json:"webSearchRequests"`
		CostUSD                  float64 `json:"costUSD"`
	} `json:"modelUsage"`
}

// ParseRecords turns an audit.jsonl into priced records — one per (result event,
// model) — using Anthropic's own per-model costUSD as the authoritative cost.
// This is exact (it includes internal helper calls, e.g. haiku for titles, that
// never appear in the transcript). The dedup key is the result event's uuid plus
// the model, so re-ingesting is idempotent. The project (cwd) comes from the
// audit's own system/init record; the entrypoint is "local-agent".
func ParseRecords(path, host string) ([]model.PricedRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseRecordsReader(f, host)
}

func parseRecordsReader(r io.Reader, host string) ([]model.PricedRecord, error) {
	var out []model.PricedRecord
	var cwd string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimRight(sc.Text(), "\r"))
		if line == "" {
			continue
		}
		var rec rawRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue // tolerant
		}
		if cwd == "" && rec.Cwd != "" {
			cwd = rec.Cwd // from the system/init record (appears before results)
		}
		if rec.Type != "result" {
			continue
		}
		ts := parseAuditTime(rec.AuditTimestamp)
		for m, u := range rec.ModelUsage {
			out = append(out, model.PricedRecord{
				UsageRecord: model.UsageRecord{
					MessageID:  rec.UUID + ":" + m, // raw model keeps the key unique
					RequestID:  rec.RequestID,
					Timestamp:  ts,
					Source:     model.SourceCowork,
					Entrypoint: model.Entrypoint("local-agent"),
					Host:       host,
					SessionID:  rec.SessionID,
					Project:    cwd,
					// Normalize the display model: drop a "[1m]" (1M-context) variant
					// tag so cowork opus[1m] groups with code's claude-opus-4-8.
					Model: stripBracketSuffix(m),
					Usage: model.Usage{
						InputTokens:          u.InputTokens,
						OutputTokens:         u.OutputTokens,
						CacheReadInputTokens: u.CacheReadInputTokens,
						// Audit reports cache-creation as one total (no 1h/5m split);
						// it's informational here since Cost comes straight from audit.
						CacheCreation1h:   u.CacheCreationInputTokens,
						WebSearchRequests: u.WebSearchRequests,
					},
				},
				Cost: model.Cost{ListPriceUSD: u.CostUSD},
			})
		}
	}
	return out, sc.Err()
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

func parseAuditTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// DiscoverFiles finds every audit.jsonl under the cowork root. A missing root
// is not an error (Cowork may not be installed).
func DiscoverFiles(coworkRoot string) ([]string, error) {
	if coworkRoot == "" {
		return nil, nil
	}
	if fi, err := os.Stat(coworkRoot); err != nil || !fi.IsDir() {
		return nil, nil
	}
	var out []string
	err := filepath.WalkDir(coworkRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && d.Name() == "audit.jsonl" {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}
