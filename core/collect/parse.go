// Package collect discovers session-log transcripts and parses them into
// deduplicated UsageRecords. It is the only package that reads raw JSONL.
package collect

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nlink-jp/claude-usage-lens/core/model"
)

// rawLine mirrors the subset of a transcript record we care about. Unknown
// fields are ignored by encoding/json — that tolerance is what lets the parser
// survive schema drift.
type rawLine struct {
	Type       string `json:"type"`
	Timestamp  string `json:"timestamp"`
	SessionID  string `json:"sessionId"`
	Cwd        string `json:"cwd"`
	Entrypoint string `json:"entrypoint"`
	RequestID  string `json:"requestId"`
	Message    *struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			InputTokens              int64  `json:"input_tokens"`
			OutputTokens             int64  `json:"output_tokens"`
			CacheReadInputTokens     int64  `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64  `json:"cache_creation_input_tokens"`
			ServiceTier              string `json:"service_tier"`
			CacheCreation            *struct {
				Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
				Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
			} `json:"cache_creation"`
			ServerToolUse *struct {
				WebSearch int64 `json:"web_search_requests"`
				WebFetch  int64 `json:"web_fetch_requests"`
			} `json:"server_tool_use"`
		} `json:"usage"`
	} `json:"message"`
}

// ParseFile reads one JSONL transcript and returns its assistant usage records.
// src/host describe provenance stamped onto every record.
func ParseFile(path string, src model.Source, host string) ([]model.UsageRecord, error) {
	recs, _, err := ParseFrom(path, 0, src, host)
	return recs, err
}

// ParseFrom reads a transcript starting at byte offset and returns its records
// plus the file's current size (the new offset to persist). Transcripts are
// append-only whole-line JSONL, so a previously-recorded offset always lands on
// a line boundary. If the file has shrunk below offset (rotated/truncated), it
// is re-read from the start.
func ParseFrom(path string, offset int64, src model.Source, host string) (recs []model.UsageRecord, newOffset int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	size := fi.Size()
	if offset < 0 || offset > size {
		offset = 0
	}
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, 0, err
		}
	}
	recs, err = parseReader(f, src, host)
	return recs, size, err
}

// parseReader is the testable core of ParseFile. It streams lines, trims a
// trailing '\r' (CRLF safety), decodes each tolerantly, and keeps only
// assistant records carrying token usage. The "<synthetic>" model (local
// synthetic responses) is excluded — it carries no billable cost.
func parseReader(r io.Reader, src model.Source, host string) ([]model.UsageRecord, error) {
	var out []model.UsageRecord
	sc := bufio.NewScanner(r)
	// Transcript lines can be large (embedded tool results); grow the buffer.
	sc.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimRight(sc.Text(), "\r"))
		if line == "" {
			continue
		}
		var raw rawLine
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue // tolerant: skip malformed lines
		}
		if raw.Type != "assistant" || raw.Message == nil || raw.Message.Usage == nil {
			continue
		}
		if raw.Message.Model == "" || raw.Message.Model == "<synthetic>" {
			continue
		}

		u := raw.Message.Usage
		rec := model.UsageRecord{
			MessageID:   raw.Message.ID,
			RequestID:   raw.RequestID,
			Timestamp:   parseTime(raw.Timestamp),
			Source:      src,
			Entrypoint:  entrypointFor(raw.Entrypoint, src),
			Host:        host,
			SessionID:   raw.SessionID,
			Project:     raw.Cwd,
			Model:       raw.Message.Model,
			ServiceTier: u.ServiceTier,
			Usage: model.Usage{
				InputTokens:          u.InputTokens,
				OutputTokens:         u.OutputTokens,
				CacheReadInputTokens: u.CacheReadInputTokens,
			},
		}
		if u.CacheCreation != nil {
			rec.Usage.CacheCreation1h = u.CacheCreation.Ephemeral1h
			rec.Usage.CacheCreation5m = u.CacheCreation.Ephemeral5m
		} else if u.CacheCreationInputTokens > 0 {
			// Aggregate present without the 1h/5m split (rare). Attribute to 5m —
			// the cheaper default — so we neither drop nor over-bill the write.
			rec.Usage.CacheCreation5m = u.CacheCreationInputTokens
		}
		if u.ServerToolUse != nil {
			rec.Usage.WebSearchRequests = u.ServerToolUse.WebSearch
			rec.Usage.WebFetchRequests = u.ServerToolUse.WebFetch
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	return out, nil
}

// entrypointFor maps the in-record entrypoint to a model.Entrypoint. A cowork
// record with no entrypoint field is tagged as the cowork entrypoint.
func entrypointFor(raw string, src model.Source) model.Entrypoint {
	if raw != "" {
		return model.Entrypoint(raw)
	}
	if src == model.SourceCowork {
		return model.EntrypointCowork
	}
	return ""
}

// parseTime parses an ISO-8601 timestamp, returning the zero time on failure
// (the record is still kept — a missing timestamp shouldn't drop usage).
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
