package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nlink-jp/claude-usage-lens/core/model"
)

func TestStore_FilePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	path := filepath.Join(dir, "usage.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != dirPerms {
		t.Errorf("data dir perms = %o, want %o", perm, dirPerms)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != dbFilePerms {
		t.Errorf("db file perms = %o, want %o", perm, dbFilePerms)
	}
}

func priced(id, mdl string, in, out int64, usd float64, ts time.Time) model.PricedRecord {
	return model.PricedRecord{
		UsageRecord: model.UsageRecord{
			MessageID: id,
			Timestamp: ts,
			Source:    model.SourceCode,
			Model:     mdl,
			Usage:     model.Usage{InputTokens: in, OutputTokens: out},
		},
		Cost: model.Cost{ListPriceUSD: usd, Tier: "standard"},
	}
}

func TestStore_UpsertIdempotentAndDurable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	t0 := time.Unix(1_770_000_000, 0).UTC() // fixed timestamp (no Date.now in tests)
	recs := []model.PricedRecord{
		priced("msg_a", "claude-opus-4-8", 100, 50, 1.25, t0),
		priced("msg_b", "claude-haiku-4-5", 10, 5, 0.05, t0.Add(time.Hour)),
	}

	n, err := s.Upsert(recs)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if n != 2 {
		t.Fatalf("first upsert inserted %d, want 2", n)
	}

	// Idempotency: re-upserting the same message_ids inserts nothing.
	n, err = s.Upsert(recs)
	if err != nil {
		t.Fatalf("Upsert (2nd): %v", err)
	}
	if n != 0 {
		t.Fatalf("second upsert inserted %d, want 0 (idempotent)", n)
	}

	got, err := s.Query(Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("query returned %d rows, want 2", len(got))
	}
	// Ordered by ts: msg_a first.
	if got[0].MessageID != "msg_a" || got[1].MessageID != "msg_b" {
		t.Errorf("rows not ts-ordered: %v, %v", got[0].MessageID, got[1].MessageID)
	}
	if got[0].Cost.ListPriceUSD != 1.25 || got[0].Usage.InputTokens != 100 {
		t.Errorf("row 0 round-trip wrong: %+v", got[0])
	}
	if !got[0].Timestamp.Equal(t0) {
		t.Errorf("timestamp round-trip wrong: got %v want %v", got[0].Timestamp, t0)
	}
}

func TestStore_QueryFilters(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	base := time.Unix(1_770_000_000, 0).UTC()
	_, err = s.Upsert([]model.PricedRecord{
		priced("m1", "claude-opus-4-8", 1, 1, 0.1, base),
		priced("m2", "claude-opus-4-8", 1, 1, 0.1, base.Add(48*time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.Query(Filter{Since: base.Add(24 * time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].MessageID != "m2" {
		t.Errorf("since-filter wrong: %+v", got)
	}
}

// coworkPriced mirrors priced() but tags the row as cowork, whose cost is
// authoritative (from audit.jsonl) and must survive a reprice untouched.
func coworkPriced(id, mdl string, in, out int64, usd float64, ts time.Time) model.PricedRecord {
	r := priced(id, mdl, in, out, usd, ts)
	r.Source = model.SourceCowork
	return r
}

func TestStore_RepriceCode(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	t0 := time.Unix(1_770_000_000, 0).UTC()
	if _, err := s.Upsert([]model.PricedRecord{
		// Stored at $0 — the shape of a model that was missing from the table.
		priced("msg_new", "claude-opus-5", 1_000_000, 0, 0, t0),
		// Already correct: must be counted as scanned but not changed.
		priced("msg_old", "claude-opus-4-8", 1_000_000, 0, 5, t0),
		coworkPriced("msg_cw", "claude-opus-5", 1_000_000, 0, 99, t0),
	}); err != nil {
		t.Fatal(err)
	}

	// Flat $5 per 1M input tokens, whatever the model.
	flat := func(rec model.UsageRecord) model.Cost {
		return model.Cost{ListPriceUSD: float64(rec.Usage.InputTokens) / 1e6 * 5}
	}

	// Dry run reports the change but writes nothing.
	res, err := s.RepriceCode(flat, true)
	if err != nil {
		t.Fatalf("RepriceCode(dry): %v", err)
	}
	if res.Scanned != 2 || res.Changed != 1 {
		t.Errorf("dry run: scanned=%d changed=%d, want 2/1 (cowork excluded)", res.Scanned, res.Changed)
	}
	if res.OldTotalUSD != 5 || res.NewTotalUSD != 10 {
		t.Errorf("dry run totals: %v → %v, want 5 → 10", res.OldTotalUSD, res.NewTotalUSD)
	}
	rows, err := s.Query(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if costOf(t, rows, "msg_new") != 0 {
		t.Error("dry run must not write")
	}

	// Real run persists.
	if _, err := s.RepriceCode(flat, false); err != nil {
		t.Fatalf("RepriceCode: %v", err)
	}
	rows, err = s.Query(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if got := costOf(t, rows, "msg_new"); got != 5 {
		t.Errorf("msg_new cost = %v, want 5", got)
	}
	if got := costOf(t, rows, "msg_old"); got != 5 {
		t.Errorf("msg_old cost = %v, want 5 (unchanged)", got)
	}
	if got := costOf(t, rows, "msg_cw"); got != 99 {
		t.Errorf("cowork cost = %v, want 99 (audit cost must not be recomputed)", got)
	}

	// Idempotent: a second pass finds nothing to change.
	res, err = s.RepriceCode(flat, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed != 0 {
		t.Errorf("second pass changed %d rows, want 0", res.Changed)
	}
}

func costOf(t *testing.T, rows []model.PricedRecord, id string) float64 {
	t.Helper()
	for _, r := range rows {
		if r.MessageID == id {
			return r.Cost.ListPriceUSD
		}
	}
	t.Fatalf("row %s not found", id)
	return 0
}

func TestStore_IngestStateRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, ok, err := s.IngestState("/some/file.jsonl"); err != nil || ok {
		t.Fatalf("unknown path should report ok=false: ok=%v err=%v", ok, err)
	}
	if err := s.SetIngestState("/some/file.jsonl", 1000, 42, 512); err != nil {
		t.Fatal(err)
	}
	off, ok, err := s.IngestState("/some/file.jsonl")
	if err != nil || !ok || off != 512 {
		t.Fatalf("state round-trip wrong: off=%d ok=%v err=%v", off, ok, err)
	}
	// Update advances the offset.
	if err := s.SetIngestState("/some/file.jsonl", 2000, 43, 1500); err != nil {
		t.Fatal(err)
	}
	if off, _, _ := s.IngestState("/some/file.jsonl"); off != 1500 {
		t.Errorf("offset not updated: %d", off)
	}
}

// A store created by an older build has no `speed` column. Open must add it in
// place rather than failing every subsequent write, and the existing rows must
// survive — the store outlives the transcripts, so losing them is unrecoverable.
func TestStore_MigratesLegacySchemaInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.db")

	// Build the pre-speed schema by hand and seed a row.
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE usage_records (
	  message_id TEXT PRIMARY KEY, request_id TEXT, ts INTEGER, source TEXT,
	  entrypoint TEXT, host TEXT, session_id TEXT, project TEXT, model TEXT,
	  service_tier TEXT, input_tokens INTEGER, output_tokens INTEGER,
	  cache_read INTEGER, cache_1h INTEGER, cache_5m INTEGER, web_search INTEGER,
	  web_fetch INTEGER, cost_usd REAL, ingested_at INTEGER)`); err != nil {
		t.Fatal(err)
	}
	// The old build always wrote every column, so the fixture does too.
	if _, err := legacy.Exec(`INSERT INTO usage_records
	  (message_id, request_id, ts, source, entrypoint, host, session_id, project, model,
	   service_tier, input_tokens, output_tokens, cache_read, cache_1h, cache_5m,
	   web_search, web_fetch, cost_usd, ingested_at)
	  VALUES ('msg_legacy', 'req_1', 1770000000, 'code', 'cli', 'h', 'sess', '/p', 'claude-opus-4-8',
	          'standard', 7, 3, 0, 0, 0, 0, 0, 1.5, 1770000000)`); err != nil {
		t.Fatal(err)
	}
	legacy.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a legacy store should migrate, not fail: %v", err)
	}
	defer s.Close()

	rows, err := s.Query(Filter{})
	if err != nil {
		t.Fatalf("Query after migration: %v", err)
	}
	if len(rows) != 1 || rows[0].MessageID != "msg_legacy" {
		t.Fatalf("legacy row lost: %+v", rows)
	}
	if rows[0].Speed != "" {
		t.Errorf("migrated row speed = %q, want empty (= standard)", rows[0].Speed)
	}
	if rows[0].Cost.ListPriceUSD != 1.5 || rows[0].Usage.InputTokens != 7 {
		t.Errorf("legacy row corrupted: %+v", rows[0])
	}

	// Writes naming the new column now work.
	t0 := time.Unix(1_770_000_100, 0).UTC()
	rec := priced("msg_new", "claude-opus-5", 1, 1, 0.5, t0)
	rec.Speed = "fast"
	if _, err := s.Upsert([]model.PricedRecord{rec}); err != nil {
		t.Fatalf("Upsert after migration: %v", err)
	}
	rows, _ = s.Query(Filter{})
	if got := speedOf(t, rows, "msg_new"); got != "fast" {
		t.Errorf("speed round-trip = %q, want fast", got)
	}
}

// Migration runs on every Open and must be a no-op the second time.
func TestStore_MigrationIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.db")
	for i := range 3 {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("Open #%d: %v", i+1, err)
		}
		s.Close()
	}
}

func speedOf(t *testing.T, rows []model.PricedRecord, id string) string {
	t.Helper()
	for _, r := range rows {
		if r.MessageID == id {
			return r.Speed
		}
	}
	t.Fatalf("row %s not found", id)
	return ""
}
