// Package platform resolves OS-specific source roots and application directories.
// The rest of the codebase depends only on these functions — never on hardcoded
// paths or separators. All concrete resolution lives in the build-tagged files
// (paths_darwin.go / paths_windows.go / paths_linux.go); path building there uses
// path/filepath so separators are correct per OS.
package platform

// Roots holds the resolved default source locations for the local machine.
type Roots struct {
	// CodeRoot is the Claude Code projects dir; it contains
	// <encoded-cwd>/<sessionId>.jsonl transcripts.
	CodeRoot string
	// CoworkRoot is the Cowork local-agent-mode sessions dir.
	CoworkRoot string
}

// SourceRoots returns the OS-specific default source roots. These are only
// defaults — the user can override them via config [sources] / --source-root,
// which is the safety valve for environments where the inference is wrong
// (notably Windows, where paths are unverified on real hardware).
func SourceRoots() (Roots, error) { return sourceRoots() }

// ConfigDir returns the OS-standard config dir for claude-usage-lens.
func ConfigDir() (string, error) { return configDir() }

// DataDir returns the OS-standard data dir (where usage.db lives).
func DataDir() (string, error) { return dataDir() }
