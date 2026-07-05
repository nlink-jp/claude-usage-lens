// Package collect discovers session-log transcripts and parses them into
// deduplicated UsageRecords. It is the only package that reads raw JSONL.
package collect

import "github.com/nlink-jp/claude-usage-lens/core/model"

// ParseFile reads one JSONL transcript and returns its assistant usage records.
// src/host describe provenance stamped onto every record.
//
// TODO(phase1): stream the file line by line; trim a trailing '\r' (CRLF safety);
// json-decode each line tolerantly (ignore unknown fields to survive schema drift);
// keep records with type=="assistant" and a non-nil message.usage; map the usage
// fields (incl. cache_creation.ephemeral_1h/5m and server_tool_use) and the
// in-record cwd/sessionId/entrypoint into a model.UsageRecord. Skip "<synthetic>".
func ParseFile(path string, src model.Source, host string) ([]model.UsageRecord, error) {
	return nil, model.ErrNotImplemented
}
