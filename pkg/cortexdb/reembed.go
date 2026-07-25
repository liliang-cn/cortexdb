package cortexdb

import (
	"context"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// ReembedOptions controls a re-embedding pass.
type ReembedOptions struct {
	// Limit caps how many rows are processed (0 = all of them).
	Limit int
	// BatchSize is how many texts go to the embedder per call (0 = 16).
	BatchSize int
	// DryRun reports what would change without writing anything.
	DryRun bool
}

// ReembedReport is the outcome of a re-embedding pass.
type ReembedReport struct {
	TargetDim  int  `json:"targetDim"`
	Candidates int  `json:"candidates"`
	Reembedded int  `json:"reembedded"`
	Failed     int  `json:"failed"`
	DryRun     bool `json:"dryRun"`
	// Collections whose declared dimension was brought in line with their contents.
	Reconciled int      `json:"reconciled"`
	Errors     []string `json:"errors,omitempty"`
}

// ReembedMismatchedVectors recomputes vectors whose stored dimensionality differs from
// the configured embedder's.
//
// Such rows appear whenever a store outlives an embedding model. They cannot enter the
// vector index — a graph holds one dimensionality — so they quietly stop being
// retrievable by similarity while lexical search still finds them, which masks the
// problem. The numbers cannot be salvaged by truncating or padding: vectors from two
// models occupy unrelated spaces, so the only honest repair is to embed the stored text
// again with the current model.
//
// Returns ErrEmbedderNotConfigured when the DB has no embedder.
func (db *DB) ReembedMismatchedVectors(ctx context.Context, opts ReembedOptions) (*ReembedReport, error) {
	if db.embedder == nil {
		return nil, ErrEmbedderNotConfigured
	}
	targetDim := db.embedder.Dim()
	if targetDim <= 0 {
		return nil, fmt.Errorf("cortexdb: embedder reports dimension %d", targetDim)
	}
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = 16
	}

	stale, err := db.store.MismatchedEmbeddings(ctx, targetDim, opts.Limit)
	if err != nil {
		return nil, err
	}
	report := &ReembedReport{TargetDim: targetDim, Candidates: len(stale), DryRun: opts.DryRun}
	if opts.DryRun || len(stale) == 0 {
		return report, nil
	}

	for start := 0; start < len(stale); start += batchSize {
		end := start + batchSize
		if end > len(stale) {
			end = len(stale)
		}
		batch := stale[start:end]
		texts := make([]string, len(batch))
		for i, emb := range batch {
			texts[i] = emb.Content
		}
		vectors, err := db.embedder.EmbedBatch(ctx, texts)
		if err != nil {
			report.Failed += len(batch)
			report.Errors = append(report.Errors, fmt.Sprintf("embed batch at %d: %v", start, err))
			continue
		}
		if len(vectors) != len(batch) {
			report.Failed += len(batch)
			report.Errors = append(report.Errors,
				fmt.Sprintf("embed batch at %d returned %d vectors for %d texts", start, len(vectors), len(batch)))
			continue
		}
		updates := make([]*core.Embedding, 0, len(batch))
		for i, emb := range batch {
			if len(vectors[i]) != targetDim {
				report.Failed++
				report.Errors = append(report.Errors,
					fmt.Sprintf("row %s: embedder returned %d dimensions, want %d", emb.ID, len(vectors[i]), targetDim))
				continue
			}
			emb.Vector = vectors[i]
			updates = append(updates, emb)
		}
		if len(updates) == 0 {
			continue
		}
		if err := db.store.UpsertBatch(ctx, updates); err != nil {
			report.Failed += len(updates)
			report.Errors = append(report.Errors, fmt.Sprintf("write batch at %d: %v", start, err))
			continue
		}
		report.Reembedded += len(updates)
	}
	// A collection records the dimension it was created with. Leaving it stale would keep
	// the drift report flagging rows that are now correct.
	if report.Reembedded > 0 {
		reconciled, err := db.store.ReconcileCollectionDimensions(ctx, targetDim)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("reconcile collection dimensions: %v", err))
		} else {
			report.Reconciled = reconciled
		}
	}
	return report, nil
}

// DimensionReport surfaces vector-dimension drift across the store, so a caller can see
// whether a re-embedding pass is needed.
func (db *DB) DimensionReport(ctx context.Context) (*core.DimensionReport, error) {
	return db.store.DimensionReport(ctx)
}

// VectorDimensionRepairRequest is the `vector_dimension_repair` MCP tool input.
type VectorDimensionRepairRequest struct {
	DryRun    *bool `json:"dry_run,omitempty"`
	Limit     int   `json:"limit,omitempty"`
	BatchSize int   `json:"batch_size,omitempty"`
}

// VectorDimensionRepairResponse pairs the drift report with the repair outcome.
type VectorDimensionRepairResponse struct {
	Report *core.DimensionReport `json:"report"`
	Repair *ReembedReport        `json:"repair,omitempty"`
}

// RepairVectorDimensions backs the `vector_dimension_repair` MCP tool. It always reports
// the drift; it only rewrites vectors when dry_run is explicitly false.
func (db *DB) RepairVectorDimensions(ctx context.Context, req VectorDimensionRepairRequest) (*VectorDimensionRepairResponse, error) {
	report, err := db.DimensionReport(ctx)
	if err != nil {
		return nil, err
	}
	response := &VectorDimensionRepairResponse{Report: report}
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	repair, err := db.ReembedMismatchedVectors(ctx, ReembedOptions{
		Limit:     req.Limit,
		BatchSize: req.BatchSize,
		DryRun:    dryRun,
	})
	if err != nil {
		return response, err
	}
	response.Repair = repair
	return response, nil
}
