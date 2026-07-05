package collect

import "testing"

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
