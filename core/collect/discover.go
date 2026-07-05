package collect

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nlink-jp/claude-usage-lens/core/model"
)

// maxEntriesScanned bounds how many filesystem entries Discover will visit, as a
// safety net against a source root accidentally set to a filesystem root (which
// would otherwise walk the whole disk). It's far above any real usage (session
// files number in the thousands); exceeding it is treated as a misconfiguration
// and surfaced as an error, never a silent truncation. A var so tests can lower it.
var maxEntriesScanned = 1_000_000

// Discovered is one located transcript file with its provenance.
type Discovered struct {
	Path   string
	Source model.Source
}

// Discover enumerates transcript JSONL files under the given source roots
// (typically resolved by core/platform for the host OS). Path building/matching
// uses path/filepath so separators are correct on every OS.
//
// For the code root, every *.jsonl is a transcript. For the cowork root, a
// session embeds a full Claude Code tree, so transcripts live under a
// ".claude/projects/" segment (the session output and its subagents/). Files
// outside that — audit.jsonl (pre-computed cost; would double-count, and is the
// validation harness's ground truth) and skills-plugin script templates — are
// deliberately excluded.
func Discover(codeRoot, coworkRoot string) ([]Discovered, error) {
	var out []Discovered
	var scanned int

	walk := func(root string, src model.Source, keep func(path string) bool) error {
		if root == "" {
			return nil
		}
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			return nil // a missing root is not an error — the product may not be installed
		}
		return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // tolerate unreadable subtrees
			}
			scanned++
			if scanned > maxEntriesScanned {
				return fmt.Errorf("aborting scan of %q after %d entries — is the source root misconfigured (e.g. set to a filesystem root)? narrow it via config [sources] or --source-root", root, maxEntriesScanned)
			}
			if d.IsDir() {
				return nil
			}
			if keep(p) {
				out = append(out, Discovered{Path: p, Source: src})
			}
			return nil
		})
	}

	if err := walk(codeRoot, model.SourceCode, isJSONL); err != nil {
		return out, err
	}
	if err := walk(coworkRoot, model.SourceCowork, isCoworkTranscript); err != nil {
		return out, err
	}
	return out, nil
}

func isJSONL(p string) bool { return strings.HasSuffix(p, ".jsonl") }

func isCoworkTranscript(p string) bool {
	return isJSONL(p) && strings.Contains(filepath.ToSlash(p), "/.claude/projects/")
}
