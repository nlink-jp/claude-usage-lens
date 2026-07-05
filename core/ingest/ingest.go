// Package ingest orchestrates bringing the durable store up to date.
//
// Cost source differs by origin, on purpose:
//   - code (Claude Code): computed from the transcript's message.usage against
//     our pricing table. This is a notional (list-price-equivalent) estimate —
//     the transcript records the visible assistant turns, so internal helper
//     calls (e.g. haiku for titles/summaries) are not counted and replayed/
//     retried turns can be over-counted. Accurate to ~5% in practice.
//   - cowork: taken straight from Cowork's audit.jsonl (Anthropic's own
//     total_cost_usd / modelUsage). This is exact and includes the internal
//     calls the transcript omits.
package ingest

import (
	"os"

	"github.com/nlink-jp/claude-usage-lens/core/audit"
	"github.com/nlink-jp/claude-usage-lens/core/collect"
	"github.com/nlink-jp/claude-usage-lens/core/cost"
	"github.com/nlink-jp/claude-usage-lens/core/model"
	"github.com/nlink-jp/claude-usage-lens/core/platform"
	"github.com/nlink-jp/claude-usage-lens/core/pricing"
	"github.com/nlink-jp/claude-usage-lens/core/store"
)

// Result summarizes an ingest run.
type Result struct {
	FilesScanned int
	FilesChanged int
	NewRecords   int
	FileErrors   int
}

// Run brings the store up to date from the source roots. sources, when non-nil,
// restricts ingestion to the given source set. host stamps provenance on every
// record. Incremental: only changed files are re-read; upserts are idempotent.
func Run(st store.Store, roots platform.Roots, tbl pricing.Table, host string, sources map[model.Source]bool) (Result, error) {
	var res Result
	if sources == nil || sources[model.SourceCode] {
		if err := ingestCode(st, roots.CodeRoot, tbl, host, &res); err != nil {
			return res, err
		}
	}
	if sources == nil || sources[model.SourceCowork] {
		if err := ingestCowork(st, roots.CoworkRoot, host, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}

// ingestCode reads Claude Code transcripts incrementally, dedups, prices against
// the table, and upserts.
func ingestCode(st store.Store, codeRoot string, tbl pricing.Table, host string, res *Result) error {
	files, err := collect.Discover(codeRoot, "")
	if err != nil {
		return err
	}
	for _, f := range files {
		res.FilesScanned++
		offset, _, err := st.IngestState(f.Path)
		if err != nil {
			return err
		}
		recs, newOffset, err := collect.ParseFrom(f.Path, offset, f.Source, host)
		if err != nil {
			res.FileErrors++
			continue
		}
		if newOffset == offset && len(recs) == 0 {
			continue
		}
		res.FilesChanged++

		deduped := collect.Dedup(recs)
		priced := make([]model.PricedRecord, len(deduped))
		for i, r := range deduped {
			priced[i] = model.PricedRecord{UsageRecord: r, Cost: cost.ComputeRecord(r, tbl)}
		}
		n, err := st.Upsert(priced)
		if err != nil {
			return err
		}
		res.NewRecords += n
		if err := st.SetIngestState(f.Path, newOffset, 0, newOffset); err != nil {
			return err
		}
	}
	return nil
}

// ingestCowork reads each audit.jsonl (authoritative cost) and upserts its
// per-(result, model) records. The whole file is re-parsed on any size change;
// idempotency comes from the uuid:model dedup key, so re-parsing is safe.
func ingestCowork(st store.Store, coworkRoot, host string, res *Result) error {
	auditFiles, err := audit.DiscoverFiles(coworkRoot)
	if err != nil {
		return err
	}
	for _, af := range auditFiles {
		res.FilesScanned++
		offset, _, err := st.IngestState(af)
		if err != nil {
			return err
		}
		fi, err := os.Stat(af)
		if err != nil {
			res.FileErrors++
			continue
		}
		size := fi.Size()
		if size == offset {
			continue // unchanged
		}
		res.FilesChanged++

		priced, err := audit.ParseRecords(af, host)
		if err != nil {
			res.FileErrors++
			continue
		}
		n, err := st.Upsert(priced)
		if err != nil {
			return err
		}
		res.NewRecords += n
		if err := st.SetIngestState(af, size, 0, size); err != nil {
			return err
		}
	}
	return nil
}
