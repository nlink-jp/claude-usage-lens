package collect

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDiscover_ScanCapAborts(t *testing.T) {
	orig := maxEntriesScanned
	maxEntriesScanned = 3 // force the safety net on a small tree
	defer func() { maxEntriesScanned = orig }()

	root := t.TempDir()
	for i := range 10 {
		if err := os.WriteFile(filepath.Join(root, "s"+strconv.Itoa(i)+".jsonl"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Discover(root, ""); err == nil {
		t.Fatal("expected an error when the scan cap is exceeded")
	} else if !strings.Contains(err.Error(), "aborting scan") {
		t.Errorf("error should explain the abort, got: %v", err)
	}
}

func TestDiscover_UnderCapKeepsJSONL(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"a.jsonl", "b.jsonl", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, n), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Discover(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d discovered, want 2 (only .jsonl)", len(got))
	}
}

func TestIsCoworkTranscript(t *testing.T) {
	// Real cowork path shapes. The encoded-cwd dir can END in "-outputs"
	// (because the original cwd ended in /outputs) — that is NOT a path segment,
	// so matching on "/outputs/" would be wrong. The reliable signal is the
	// embedded ".claude/projects/" segment.
	cases := []struct {
		path string
		want bool
	}{
		// session transcript (encoded-cwd dir happens to end in -outputs)
		{"/x/local_abc/.claude/projects/-Users-magi-...-outputs/6139264d.jsonl", true},
		// subagent transcript
		{"/x/local_abc/.claude/projects/-Users-magi-...-outputs/6139264d/subagents/agent-a17.jsonl", true},
		// audit log — excluded (sits outside .claude/projects/)
		{"/x/local_abc/audit.jsonl", false},
		// skills-plugin template — excluded
		{"/x/skills-plugin/eb80/dc5d/skills/multi-actor-narration/talk-podcast/script.template.jsonl", false},
		// non-jsonl
		{"/x/local_abc/.claude/projects/enc/foo.json", false},
	}
	for _, c := range cases {
		if got := isCoworkTranscript(c.path); got != c.want {
			t.Errorf("isCoworkTranscript(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
