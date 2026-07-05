package audit

import (
	"math"
	"strings"
	"testing"

	"github.com/nlink-jp/claude-usage-lens/core/model"
)

func TestParseReader(t *testing.T) {
	// Synthetic fixture (PII-free) mirroring the real result-event shape:
	// two result events + non-result lines that must be ignored.
	lines := []string{
		`{"type":"user","message":{}}`,
		`{"type":"result","total_cost_usd":0.19,"modelUsage":{"claude-opus-4-8[1m]":{"costUSD":0.19,"inputTokens":5897}}}`,
		`{"type":"assistant","message":{}}`,
		`{"type":"result","total_cost_usd":2.5,"modelUsage":{"claude-opus-4-8":{"costUSD":2.0},"claude-haiku-4-5":{"costUSD":0.5}}}`,
		`not-json`,
	}
	g, err := parseReader(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	if g.Results != 2 {
		t.Fatalf("results = %d, want 2", g.Results)
	}
	if math.Abs(g.TotalUSD-2.69) > 1e-9 {
		t.Errorf("total = %v, want 2.69", g.TotalUSD)
	}
	if math.Abs(g.ByModel["claude-opus-4-8[1m]"]-0.19) > 1e-9 {
		t.Errorf("opus[1m] cost = %v, want 0.19", g.ByModel["claude-opus-4-8[1m]"])
	}
	if math.Abs(g.ByModel["claude-opus-4-8"]-2.0) > 1e-9 {
		t.Errorf("opus cost = %v, want 2.0", g.ByModel["claude-opus-4-8"])
	}
}

func TestParseRecordsReader(t *testing.T) {
	lines := []string{
		`{"type":"system","subtype":"init","cwd":"/sandbox/outputs","session_id":"sess-1","model":"claude-opus-4-8[1m]"}`,
		`{"type":"result","session_id":"sess-1","_audit_timestamp":"2026-06-17T15:50:18.915Z","uuid":"u1","request_id":"req_1","total_cost_usd":0.69,"modelUsage":{"claude-opus-4-8[1m]":{"inputTokens":100,"outputTokens":50,"cacheReadInputTokens":20,"cacheCreationInputTokens":10,"webSearchRequests":1,"costUSD":0.19},"claude-haiku-4-5-20251001":{"inputTokens":600000,"outputTokens":18000,"costUSD":0.50}}}`,
		`{"type":"assistant","message":{}}`,
	}
	recs, err := parseRecordsReader(strings.NewReader(strings.Join(lines, "\n")), "h")
	if err != nil {
		t.Fatal(err)
	}
	// one result × two models = two priced records
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	byModel := map[string]model.PricedRecord{}
	for _, r := range recs {
		byModel[r.Model] = r
	}
	// Display model is normalized ([1m] stripped); dedup key keeps the raw model.
	opus := byModel["claude-opus-4-8"]
	if math.Abs(opus.Cost.ListPriceUSD-0.19) > 1e-9 {
		t.Errorf("opus cost = %v, want 0.19 (from audit)", opus.Cost.ListPriceUSD)
	}
	if opus.MessageID != "u1:claude-opus-4-8[1m]" {
		t.Errorf("dedup key wrong: %q", opus.MessageID)
	}
	if opus.Source != model.SourceCowork || opus.Project != "/sandbox/outputs" || opus.SessionID != "sess-1" {
		t.Errorf("provenance wrong: %+v", opus.UsageRecord)
	}
	if opus.Usage.InputTokens != 100 || opus.Usage.CacheReadInputTokens != 20 || opus.Usage.CacheCreation1h != 10 || opus.Usage.WebSearchRequests != 1 {
		t.Errorf("opus usage wrong: %+v", opus.Usage)
	}
	if opus.Timestamp.Year() != 2026 {
		t.Errorf("timestamp not parsed: %v", opus.Timestamp)
	}
	// haiku — the internal helper usage the transcript would have missed
	haiku := byModel["claude-haiku-4-5-20251001"]
	if math.Abs(haiku.Cost.ListPriceUSD-0.50) > 1e-9 || haiku.Usage.InputTokens != 600000 {
		t.Errorf("haiku record wrong: cost=%v usage=%+v", haiku.Cost.ListPriceUSD, haiku.Usage)
	}
}
