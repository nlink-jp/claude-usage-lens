// Package ingest orchestrates the collect → dedup → price → store pipeline.
// It is the reusable service layer the CLI (and a future GUI) call to bring the
// durable store up to date with the local transcripts.
package ingest

import (
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

// Run brings the store up to date from the source roots. For each transcript it
// reads only the bytes appended since the last ingest (incremental), dedups
// within the batch, prices each record, and idempotently upserts. A single
// unreadable/corrupt file is counted and skipped, not fatal.
//
// sources, when non-nil, restricts ingestion to the given source set (e.g. only
// SourceCode). host stamps provenance on every record.
func Run(st store.Store, roots platform.Roots, tbl pricing.Table, host string, sources map[model.Source]bool) (Result, error) {
	files, err := collect.Discover(roots.CodeRoot, roots.CoworkRoot)
	if err != nil {
		return Result{}, err
	}

	var res Result
	for _, f := range files {
		if sources != nil && !sources[f.Source] {
			continue
		}
		res.FilesScanned++

		offset, _, err := st.IngestState(f.Path)
		if err != nil {
			return res, err
		}

		recs, newOffset, err := collect.ParseFrom(f.Path, offset, f.Source, host)
		if err != nil {
			res.FileErrors++
			continue
		}
		if newOffset == offset && len(recs) == 0 {
			continue // unchanged since last ingest
		}
		res.FilesChanged++

		deduped := collect.Dedup(recs)
		priced := make([]model.PricedRecord, len(deduped))
		for i, r := range deduped {
			priced[i] = model.PricedRecord{UsageRecord: r, Cost: cost.ComputeRecord(r, tbl)}
		}

		n, err := st.Upsert(priced)
		if err != nil {
			return res, err
		}
		res.NewRecords += n

		if err := st.SetIngestState(f.Path, newOffset, 0, newOffset); err != nil {
			return res, err
		}
	}
	return res, nil
}
