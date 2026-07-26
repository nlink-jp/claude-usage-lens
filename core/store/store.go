// Package store persists priced records durably so reports are fast and data
// survives Claude Code's automatic session cleanup.
//
// The implementation uses modernc.org/sqlite (pure-Go, no CGO) in WAL mode, so a
// running `watch` and an ad-hoc `report` can touch the DB concurrently on every
// OS without a C toolchain.
package store

import (
	"database/sql"
	"fmt"
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

	// RepriceCode recomputes cost_usd for every stored `code` record using price,
	// from the token columns already in the store — no source transcript needed.
	// This is what makes a pricing-table change (a newly released model, a rate
	// revision) apply to history, instead of only to records ingested afterwards:
	// ingest is incremental, so already-read bytes are never re-priced.
	//
	// `cowork` rows are deliberately untouched — their cost is Anthropic's own
	// audited total_cost_usd, not something we compute. dryRun reports what would
	// change without writing.
	RepriceCode(price func(model.UsageRecord) model.Cost, dryRun bool) (RepriceResult, error)

	// IngestState / SetIngestState track how far each source file has been read,
	// so ingest only consumes bytes appended since last time.
	IngestState(path string) (offset int64, ok bool, err error)
	SetIngestState(path string, size, mtime, offset int64) error

	Close() error
}

// RepriceResult summarizes a RepriceCode pass. The USD totals cover the scanned
// (`code`) rows only, so OldTotalUSD → NewTotalUSD is the exact effect on the
// code-side cost; cowork totals are unaffected.
type RepriceResult struct {
	Scanned     int     // code rows examined
	Changed     int     // rows whose cost differed (written unless dryRun)
	OldTotalUSD float64 // sum of cost_usd before
	NewTotalUSD float64 // sum of cost_usd after
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
  speed         TEXT,
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
	if err := migrate(db); err != nil {
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

// addedColumns are columns introduced after the first release. `CREATE TABLE IF
// NOT EXISTS` leaves an existing table untouched, so a store created by an older
// build would otherwise keep the old shape and every write naming a new column
// would fail. Each entry is added only when absent, so migrate is idempotent and
// safe to run on every Open.
//
// Existing rows get the zero value, which for `speed` reads as standard — the
// truth for every record written before fast mode was modeled, since the speed
// was simply not recorded. Re-ingesting cannot recover it either: ingest is
// incremental and those bytes are already consumed.
var addedColumns = []struct{ name, decl string }{
	{"speed", "TEXT"},
}

func migrate(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(usage_records)")
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	for rows.Next() {
		var (
			cid         int
			name, ctype string
			notNull, pk int
			dflt        sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		existing[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, c := range addedColumns {
		if existing[c.name] {
			continue
		}
		if _, err := db.Exec("ALTER TABLE usage_records ADD COLUMN " + c.name + " " + c.decl); err != nil {
			return fmt.Errorf("add column %s: %w", c.name, err)
		}
	}
	return nil
}

const upsertSQL = `INSERT INTO usage_records
 (message_id, request_id, ts, source, entrypoint, host, session_id, project, model, service_tier, speed,
  input_tokens, output_tokens, cache_read, cache_1h, cache_5m, web_search, web_fetch, cost_usd, ingested_at)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
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
			r.SessionID, r.Project, r.Model, r.ServiceTier, r.Speed,
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
 COALESCE(speed, ''), input_tokens, output_tokens, cache_read, cache_1h, cache_5m, web_search, web_fetch, cost_usd
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
			&r.SessionID, &r.Project, &r.Model, &r.ServiceTier, &r.Speed,
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

// repriceSelect pulls exactly what Compute needs plus the current cost, for
// `code` rows only. Cowork costs are authoritative and never recomputed.
const repriceSelect = `SELECT message_id, model, service_tier, COALESCE(speed, ''),
 input_tokens, output_tokens, cache_read, cache_1h, cache_5m, web_search, web_fetch, cost_usd
 FROM usage_records WHERE source = 'code'`

func (s *sqliteStore) RepriceCode(price func(model.UsageRecord) model.Cost, dryRun bool) (RepriceResult, error) {
	var res RepriceResult

	// Collect first, then write: SQLite dislikes UPDATEs issued while its own
	// SELECT cursor is still open on the same table.
	type change struct {
		messageID string
		cost      float64
	}
	var changes []change

	rows, err := s.db.Query(repriceSelect)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var rec model.UsageRecord
		var old float64
		if err := rows.Scan(
			&rec.MessageID, &rec.Model, &rec.ServiceTier, &rec.Speed,
			&rec.Usage.InputTokens, &rec.Usage.OutputTokens, &rec.Usage.CacheReadInputTokens,
			&rec.Usage.CacheCreation1h, &rec.Usage.CacheCreation5m,
			&rec.Usage.WebSearchRequests, &rec.Usage.WebFetchRequests, &old,
		); err != nil {
			rows.Close()
			return res, err
		}
		rec.Source = model.SourceCode
		now := price(rec).ListPriceUSD

		res.Scanned++
		res.OldTotalUSD += old
		res.NewTotalUSD += now
		if now != old {
			res.Changed++
			changes = append(changes, change{rec.MessageID, now})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	if dryRun || len(changes) == 0 {
		return res, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return res, err
	}
	stmt, err := tx.Prepare("UPDATE usage_records SET cost_usd = ? WHERE message_id = ?")
	if err != nil {
		tx.Rollback()
		return res, err
	}
	defer stmt.Close()
	for _, c := range changes {
		if _, err := stmt.Exec(c.cost, c.messageID); err != nil {
			tx.Rollback()
			return res, err
		}
	}
	return res, tx.Commit()
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
