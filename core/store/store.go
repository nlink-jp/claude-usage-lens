// Package store persists priced records durably so reports are fast and data
// survives Claude Code's automatic session cleanup.
//
// The implementation uses modernc.org/sqlite (pure-Go, no CGO) in WAL mode, so a
// running `watch` and an ad-hoc `report` can touch the DB concurrently on every
// OS without a C toolchain.
package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/nlink-jp/claude-usage-lens/core/model"
)

// Store is the persistence boundary. Implementations must be safe to Open from a
// scheduled `ingest` and a long-running `watch` alike.
type Store interface {
	// Upsert idempotently inserts records keyed by MessageID (the global dedup
	// key). It MUST NOT delete existing rows — that is what makes stored data
	// outlive deletion of the source transcripts. Returns the count newly inserted.
	Upsert(recs []model.PricedRecord) (inserted int, err error)

	// Query returns priced records matching the filter, ordered by timestamp.
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

const schema = `
CREATE TABLE IF NOT EXISTS usage_records (
  message_id    TEXT PRIMARY KEY,
  request_id    TEXT,
  ts            INTEGER,
  source        TEXT,
  entrypoint    TEXT,
  host          TEXT,
  session_id    TEXT,
  project       TEXT,
  model         TEXT,
  service_tier  TEXT,
  input_tokens  INTEGER,
  output_tokens INTEGER,
  cache_read    INTEGER,
  cache_1h      INTEGER,
  cache_5m      INTEGER,
  web_search    INTEGER,
  web_fetch     INTEGER,
  cost_usd      REAL,
  ingested_at   INTEGER
);
CREATE INDEX IF NOT EXISTS idx_usage_ts     ON usage_records(ts);
CREATE INDEX IF NOT EXISTS idx_usage_source ON usage_records(source);
CREATE INDEX IF NOT EXISTS idx_usage_model  ON usage_records(model);

CREATE TABLE IF NOT EXISTS ingest_state (
  path        TEXT PRIMARY KEY,
  size        INTEGER,
  mtime       INTEGER,
  last_offset INTEGER,
  updated_at  INTEGER
);
`

type sqliteStore struct {
	db *sql.DB
}

// Store file permissions. The DB holds metadata (project paths, timestamps) that
// is personal, so it's kept owner-only: the data dir is 0700 (which also shields
// the WAL/SHM sidecars) and the DB file is 0600.
//
// These are UNIX modes and only take effect on macOS/Linux. On Windows, Go's
// os.Chmod only toggles the read-only bit, so this does not owner-restrict the
// file; protection there relies on the user-profile ACLs (%LocalAppData%).
// Applying NTFS ACLs directly is out of scope (Windows is experimental anyway).
const (
	dirPerms    os.FileMode = 0o700
	dbFilePerms os.FileMode = 0o600
)

// Open opens (creating if absent) the SQLite store at path, enabling WAL mode
// and creating the schema.
func Open(path string) (Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, dirPerms); err != nil {
			return nil, err
		}
		// Tighten an already-existing data dir too (MkdirAll leaves its perms
		// untouched) so the WAL/SHM sidecars aren't exposed. Best-effort.
		_ = os.Chmod(dir, dirPerms)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	// Tighten the DB file to owner-only (SQLite creates it under the umask).
	// Best-effort and only for a real on-disk file (skips ":memory:").
	if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() {
		_ = os.Chmod(path, dbFilePerms)
	}
	return &sqliteStore{db: db}, nil
}

const upsertSQL = `INSERT INTO usage_records
 (message_id, request_id, ts, source, entrypoint, host, session_id, project, model, service_tier,
  input_tokens, output_tokens, cache_read, cache_1h, cache_5m, web_search, web_fetch, cost_usd, ingested_at)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(message_id) DO NOTHING`

func (s *sqliteStore) Upsert(recs []model.PricedRecord) (int, error) {
	if len(recs) == 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	stmt, err := tx.Prepare(upsertSQL)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	defer stmt.Close()

	now := time.Now().Unix()
	inserted := 0
	for _, r := range recs {
		key := r.MessageID
		if key == "" {
			// Anomalous record with no message id — synthesize a stable key so we
			// neither drop it nor collide distinct records onto one row.
			key = "noid:" + r.RequestID + ":" + r.SessionID
		}
		var tsUnix int64
		if !r.Timestamp.IsZero() {
			tsUnix = r.Timestamp.Unix()
		}
		res, err := stmt.Exec(
			key, r.RequestID, tsUnix, string(r.Source), string(r.Entrypoint), r.Host,
			r.SessionID, r.Project, r.Model, r.ServiceTier,
			r.Usage.InputTokens, r.Usage.OutputTokens, r.Usage.CacheReadInputTokens,
			r.Usage.CacheCreation1h, r.Usage.CacheCreation5m,
			r.Usage.WebSearchRequests, r.Usage.WebFetchRequests, r.Cost.ListPriceUSD, now,
		)
		if err != nil {
			tx.Rollback()
			return inserted, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted++
		}
	}
	if err := tx.Commit(); err != nil {
		return inserted, err
	}
	return inserted, nil
}

const querySelect = `SELECT message_id, request_id, ts, source, entrypoint, host, session_id, project, model, service_tier,
 input_tokens, output_tokens, cache_read, cache_1h, cache_5m, web_search, web_fetch, cost_usd
 FROM usage_records WHERE 1=1`

func (s *sqliteStore) Query(f Filter) ([]model.PricedRecord, error) {
	q := querySelect
	var args []any
	if f.Since > 0 {
		q += " AND ts >= ?"
		args = append(args, f.Since)
	}
	if f.Until > 0 {
		q += " AND ts <= ?"
		args = append(args, f.Until)
	}
	if f.Source != "" {
		q += " AND source = ?"
		args = append(args, string(f.Source))
	}
	q += " ORDER BY ts"

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.PricedRecord
	for rows.Next() {
		var r model.PricedRecord
		var tsUnix int64
		var src, ep string
		if err := rows.Scan(
			&r.MessageID, &r.RequestID, &tsUnix, &src, &ep, &r.Host,
			&r.SessionID, &r.Project, &r.Model, &r.ServiceTier,
			&r.Usage.InputTokens, &r.Usage.OutputTokens, &r.Usage.CacheReadInputTokens,
			&r.Usage.CacheCreation1h, &r.Usage.CacheCreation5m,
			&r.Usage.WebSearchRequests, &r.Usage.WebFetchRequests, &r.Cost.ListPriceUSD,
		); err != nil {
			return out, err
		}
		r.Source = model.Source(src)
		r.Entrypoint = model.Entrypoint(ep)
		r.Cost.Tier = r.ServiceTier
		if tsUnix > 0 {
			r.Timestamp = time.Unix(tsUnix, 0).UTC()
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *sqliteStore) IngestState(path string) (int64, bool, error) {
	var off int64
	err := s.db.QueryRow("SELECT last_offset FROM ingest_state WHERE path = ?", path).Scan(&off)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return off, true, nil
}

func (s *sqliteStore) SetIngestState(path string, size, mtime, offset int64) error {
	_, err := s.db.Exec(
		`INSERT INTO ingest_state (path, size, mtime, last_offset, updated_at)
		 VALUES (?,?,?,?,?)
		 ON CONFLICT(path) DO UPDATE SET
		   size=excluded.size, mtime=excluded.mtime,
		   last_offset=excluded.last_offset, updated_at=excluded.updated_at`,
		path, size, mtime, offset, time.Now().Unix(),
	)
	return err
}

func (s *sqliteStore) Close() error { return s.db.Close() }
