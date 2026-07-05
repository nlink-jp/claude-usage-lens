package store

import (
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
