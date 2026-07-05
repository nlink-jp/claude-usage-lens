package collect

import "github.com/nlink-jp/claude-usage-lens/core/model"

// Discovered is one located transcript file with its provenance.
type Discovered struct {
	Path   string
	Source model.Source
}

// Discover enumerates transcript JSONL files under the given source roots
// (typically resolved by core/platform for the host OS). All path building must
// use path/filepath so separators are correct on every OS.
//
// TODO(phase1): walk the code root (<root>/<encoded-cwd>/<sessionId>.jsonl) and the
// cowork root (<root>/**/outputs/*.jsonl and audit.jsonl), returning one Discovered
// per file. Never decode the <encoded-cwd> directory name — provenance comes from
// the record contents during parsing.
func Discover(codeRoot, coworkRoot string) ([]Discovered, error) {
	return nil, model.ErrNotImplemented
}
