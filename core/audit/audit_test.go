package audit

import (
	"math"
	"strings"
	"testing"
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
