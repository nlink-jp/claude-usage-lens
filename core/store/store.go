// Package store persists priced records durably so reports are fast and data
// survives Claude Code's automatic session cleanup.
//
// The Phase 1 implementation uses modernc.org/sqlite (pure-Go, no CGO) in WAL
// mode, so a running `watch` and an ad-hoc `report` can touch the DB concurrently
// on every OS without a C toolchain.
package store

import "github.com/nlink-jp/claude-usage-lens/core/model"

// Store is the persistence boundary. Implementations must be safe to Open from a
// scheduled `ingest` and a long-running `watch` alike.
type Store interface {
	// Upsert idempotently inserts records keyed by MessageID (the global dedup
	// key). It MUST NOT delete existing rows — that is what makes stored data
	// outlive deletion of the source transcripts. Returns the count newly inserted.
	Upsert(recs []model.PricedRecord) (inserted int, err error)

	// Query returns priced records matching the filter.
	Query(f Filter) ([]model.PricedRecord, error)

	// IngestState / SetIngestState track how far each source file has been read,
	// so ingest only consumes bytes appended since last time.
	IngestState(path string) (offset int64, ok bool, err error)
	SetIngestState(path string, size, mtime, offset int64) error

	Close() error
}

// Filter constrains a Query. Zero values mean "unbounded".
type Filter struct {
	Since  int64        // unix seconds; 0 = no lower bound
	Until  int64        // unix seconds; 0 = no upper bound
	Source model.Source // "" = all sources
}

// Open opens (creating if absent) the SQLite store at path.
//
// TODO(phase1): wire modernc.org/sqlite — create the usage_records
// (message_id PRIMARY KEY) and ingest_state (path PRIMARY KEY) tables, enable WAL,
// and return a concrete *sqliteStore. Returns ErrNotImplemented until then.
func Open(path string) (Store, error) {
	return nil, model.ErrNotImplemented
}
