package collect

import "github.com/nlink-jp/claude-usage-lens/core/model"

// Dedup removes duplicate records by MessageID. Session resume/fork copies the
// same assistant messages into multiple transcript files, so the same msg_...
// can appear more than once; the first occurrence wins and order is preserved.
// Records with an empty MessageID are always kept (nothing to key on).
func Dedup(recs []model.UsageRecord) []model.UsageRecord {
	seen := make(map[string]struct{}, len(recs))
	out := make([]model.UsageRecord, 0, len(recs))
	for _, r := range recs {
		if r.MessageID != "" {
			if _, dup := seen[r.MessageID]; dup {
				continue
			}
			seen[r.MessageID] = struct{}{}
		}
		out = append(out, r)
	}
	return out
}
