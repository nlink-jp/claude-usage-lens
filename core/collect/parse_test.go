package collect

import (
	"strings"
	"testing"

	"github.com/nlink-jp/claude-usage-lens/core/model"
)

func TestParseReader(t *testing.T) {
	// Synthetic fixture (PII-free). Covers: a user record (skip), a full
	// assistant record (keep), a <synthetic> record (skip), an assistant with no
	// usage (skip), a malformed line (skip), and a CRLF-terminated record (keep).
	lines := []string{
		`{"type":"user","message":{"role":"user"}}`,
		`{"type":"assistant","timestamp":"2026-07-05T10:00:00.000Z","sessionId":"sess-1","cwd":"/work/proj","entrypoint":"cli","requestId":"req_1","message":{"id":"msg_a","model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":10,"cache_creation_input_tokens":20,"service_tier":"standard","cache_creation":{"ephemeral_1h_input_tokens":20,"ephemeral_5m_input_tokens":0},"server_tool_use":{"web_search_requests":2,"web_fetch_requests":0}}}}`,
		`{"type":"assistant","message":{"id":"msg_syn","model":"<synthetic>","usage":{"input_tokens":5,"output_tokens":5}}}`,
		`{"type":"assistant","message":{"id":"msg_nousage","model":"claude-opus-4-8"}}`,
		`not-json-at-all`,
		`{"type":"assistant","sessionId":"sess-2","cwd":"/w","entrypoint":"claude-desktop","message":{"id":"msg_b","model":"claude-haiku-4-5","usage":{"input_tokens":1,"output_tokens":1,"service_tier":"standard"}}}` + "\r",
	}
	in := strings.NewReader(strings.Join(lines, "\n"))

	recs, err := parseReader(in, model.SourceCode, "testhost")
	if err != nil {
		t.Fatalf("parseReader error: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2: %+v", len(recs), recs)
	}

	a := recs[0]
	if a.MessageID != "msg_a" || a.Model != "claude-opus-4-8" {
		t.Errorf("record 0 identity wrong: %+v", a)
	}
	if a.Source != model.SourceCode || a.Entrypoint != model.EntrypointCLI || a.Host != "testhost" {
		t.Errorf("record 0 provenance wrong: %+v", a)
	}
	if a.Project != "/work/proj" || a.SessionID != "sess-1" || a.ServiceTier != "standard" {
		t.Errorf("record 0 context wrong: %+v", a)
	}
	if a.Usage.InputTokens != 100 || a.Usage.OutputTokens != 50 || a.Usage.CacheReadInputTokens != 10 {
		t.Errorf("record 0 tokens wrong: %+v", a.Usage)
	}
	if a.Usage.CacheCreation1h != 20 || a.Usage.CacheCreation5m != 0 {
		t.Errorf("record 0 cache split wrong: %+v", a.Usage)
	}
	if a.Usage.WebSearchRequests != 2 {
		t.Errorf("record 0 web search wrong: %+v", a.Usage)
	}
	if a.Timestamp.Year() != 2026 || a.Timestamp.Month() != 7 {
		t.Errorf("record 0 timestamp wrong: %v", a.Timestamp)
	}

	b := recs[1]
	if b.MessageID != "msg_b" || b.Entrypoint != model.EntrypointDesktop {
		t.Errorf("record 1 (CRLF line) wrong: %+v", b)
	}
}

func TestParseReader_CoworkEntrypointDefault(t *testing.T) {
	line := `{"type":"assistant","message":{"id":"msg_c","model":"claude-opus-4-8","usage":{"input_tokens":1,"output_tokens":1}}}`
	recs, err := parseReader(strings.NewReader(line), model.SourceCowork, "h")
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Entrypoint != model.EntrypointCowork {
		t.Errorf("cowork entrypoint default not applied: %+v", recs)
	}
}
