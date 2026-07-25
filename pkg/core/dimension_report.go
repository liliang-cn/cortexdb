package core

import (
	"context"
	"fmt"
	"sort"
)

// A store outlives the embedding model that filled it. Change models — or point two
// collections at different ones — and the old rows keep their old dimensionality. Those
// vectors cannot be compared with the new ones (different length, and in truth a
// different space), so they are refused by the vector index and simply stop being
// retrievable, while lexical search keeps working and hides the loss.
//
// Nothing used to report that. DimensionReport makes the drift visible so it can be
// repaired (re-embed the affected content — adapting the numbers by truncating or
// padding would produce coordinates in no model's space at all).

// DimensionCount is how many rows hold vectors of one size.
type DimensionCount struct {
	Dim  int `json:"dim"`
	Rows int `json:"rows"`
}

// CollectionDimensions describes the vector sizes actually stored in one collection.
type CollectionDimensions struct {
	Collection string           `json:"collection"`
	Declared   int              `json:"declared"`   // dimension the collection was created with
	Rows       int              `json:"rows"`       // rows holding a vector
	Dimensions []DimensionCount `json:"dimensions"` // stored sizes, smallest first
	Mismatched int              `json:"mismatched"` // rows whose dimension is not Declared
}

// RowsWithDim reports how many rows hold vectors of exactly dim.
func (c CollectionDimensions) RowsWithDim(dim int) int {
	for _, entry := range c.Dimensions {
		if entry.Dim == dim {
			return entry.Rows
		}
	}
	return 0
}

// DimensionReport summarises vector dimensionality across the store.
type DimensionReport struct {
	Collections []CollectionDimensions `json:"collections"`
	Mismatched  int                    `json:"mismatched"` // total rows needing repair
}

// NeedsRepair reports whether any stored vector disagrees with its collection.
func (r *DimensionReport) NeedsRepair() bool {
	return r != nil && r.Mismatched > 0
}

// DimensionReport groups stored vectors by collection and length. A collection whose
// `declared` dimension is 0 has never had one recorded; its rows are counted but not
// treated as mismatched.
func (s *SQLiteStore) DimensionReport(ctx context.Context) (*DimensionReport, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.name, c.dimensions, length(e.vector), count(*)
		FROM embeddings e
		JOIN collections c ON c.id = e.collection_id
		WHERE e.vector IS NOT NULL AND length(e.vector) > 0
		GROUP BY c.name, c.dimensions, length(e.vector)
		ORDER BY c.name
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to group vector dimensions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type accumulator struct {
		entry *CollectionDimensions
		byDim map[int]int
	}
	byCollection := make(map[string]*accumulator)
	order := make([]string, 0)
	for rows.Next() {
		var name string
		var declared, vectorBytes, count int
		if err := rows.Scan(&name, &declared, &vectorBytes, &count); err != nil {
			return nil, fmt.Errorf("failed to read dimension row: %w", err)
		}
		acc, seen := byCollection[name]
		if !seen {
			acc = &accumulator{
				entry: &CollectionDimensions{Collection: name, Declared: declared},
				byDim: make(map[int]int),
			}
			byCollection[name] = acc
			order = append(order, name)
		}
		dim := vectorDimFromBytes(vectorBytes)
		acc.byDim[dim] += count
		acc.entry.Rows += count
		if declared > 0 && dim != declared {
			acc.entry.Mismatched += count
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan dimension rows: %w", err)
	}

	report := &DimensionReport{Collections: make([]CollectionDimensions, 0, len(order))}
	for _, name := range order {
		acc := byCollection[name]
		dims := make([]int, 0, len(acc.byDim))
		for dim := range acc.byDim {
			dims = append(dims, dim)
		}
		sort.Ints(dims)
		for _, dim := range dims {
			acc.entry.Dimensions = append(acc.entry.Dimensions, DimensionCount{Dim: dim, Rows: acc.byDim[dim]})
		}
		report.Mismatched += acc.entry.Mismatched
		report.Collections = append(report.Collections, *acc.entry)
	}
	return report, nil
}

// MismatchedEmbeddings returns rows whose stored vector length differs from wantDim,
// newest first, capped by limit (limit <= 0 means no cap). Content is included so a
// caller with an embedder can re-embed it.
func (s *SQLiteStore) MismatchedEmbeddings(ctx context.Context, wantDim, limit int) ([]*Embedding, error) {
	if wantDim <= 0 {
		return nil, fmt.Errorf("target dimension must be positive, got %d", wantDim)
	}
	query := `
		SELECT e.id, e.content, c.name
		FROM embeddings e
		JOIN collections c ON c.id = e.collection_id
		WHERE e.vector IS NOT NULL
		  AND length(e.vector) > 0
		  AND length(e.vector) != ?
		  AND e.content IS NOT NULL
		  AND e.content != ''
		ORDER BY e.created_at DESC
	`
	args := []any{vectorBytesFromDim(wantDim)}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list mismatched vectors: %w", err)
	}
	defer func() { _ = rows.Close() }()

	stale := make([]*Embedding, 0)
	for rows.Next() {
		var emb Embedding
		var collection string
		if err := rows.Scan(&emb.ID, &emb.Content, &collection); err != nil {
			return nil, fmt.Errorf("failed to read mismatched row: %w", err)
		}
		emb.Collection = collection
		stale = append(stale, &emb)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan mismatched rows: %w", err)
	}
	return stale, nil
}

// Stored vectors carry a small header before the float32 payload, so bytes and
// dimensions are not a bare multiple of four.
const vectorHeaderBytes = 4

func vectorDimFromBytes(vectorBytes int) int {
	if vectorBytes <= vectorHeaderBytes {
		return 0
	}
	return (vectorBytes - vectorHeaderBytes) / 4
}

func vectorBytesFromDim(dim int) int {
	return dim*4 + vectorHeaderBytes
}

// ReconcileCollectionDimensions brings each collection's declared dimension in line with
// what it actually stores, for collections whose vectors are now uniformly dim.
//
// Re-embedding rewrites vectors but cannot know whether the collection's recorded
// dimension was deliberate, so it is left alone until every row agrees. Without this the
// drift report keeps flagging rows that are in fact correct. Returns the number of
// collections updated.
func (s *SQLiteStore) ReconcileCollectionDimensions(ctx context.Context, dim int) (int, error) {
	if dim <= 0 {
		return 0, fmt.Errorf("dimension must be positive, got %d", dim)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE collections
		SET dimensions = ?, updated_at = CURRENT_TIMESTAMP
		WHERE dimensions != ?
		  AND id IN (
			SELECT collection_id FROM embeddings
			WHERE vector IS NOT NULL AND length(vector) > 0
			GROUP BY collection_id
			HAVING min(length(vector)) = ? AND max(length(vector)) = ?
		  )
	`, dim, dim, vectorBytesFromDim(dim), vectorBytesFromDim(dim))
	if err != nil {
		return 0, fmt.Errorf("failed to reconcile collection dimensions: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return 0, nil // driver does not report it; the update still applied
	}
	return int(updated), nil
}
