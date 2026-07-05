package collect

import (
	"testing"

	"github.com/nlink-jp/claude-usage-lens/core/model"
)

func TestDedup_ByMessageID(t *testing.T) {
	in := []model.UsageRecord{
		{MessageID: "msg_a"},
		{MessageID: "msg_b"},
		{MessageID: "msg_a"}, // duplicate from a resumed session
		{MessageID: ""},      // no id: kept
		{MessageID: ""},      // no id: kept
	}
	got := Dedup(in)

	if len(got) != 4 {
		t.Fatalf("Dedup len = %d, want 4", len(got))
	}
	if got[0].MessageID != "msg_a" || got[1].MessageID != "msg_b" {
		t.Errorf("first-occurrence order not preserved: %+v", got)
	}
}

func TestDedup_Empty(t *testing.T) {
	if got := Dedup(nil); len(got) != 0 {
		t.Errorf("Dedup(nil) = %v, want empty", got)
	}
}
