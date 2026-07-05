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
