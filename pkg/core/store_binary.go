package core

import (
	"context"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/internal/encoding"
	"github.com/liliang-cn/cortexdb/v2/pkg/index"
)

// IndexTypeBinary on SQLite: the binary codes live in memory, the full
// vectors stay in the embeddings table, and every search rescores its Hamming
// shortlist with the rows it loads from there. Memory is 1/32 of keeping the
// float32 vectors resident, and the rescoring is the same scoring the linear
// path does (processCandidates), so a result's Score means what it always
// meant.
//
// The index is rebuilt from the table at Init — it is a pure function of the
// table, so there is no snapshot to go stale — and kept current by the same
// write paths that maintain HNSW and IVF.

// binaryEnabled reports whether this store is configured for binary search.
func (s *SQLiteStore) binaryEnabled() bool { return s.config.IndexType == IndexTypeBinary }

func (s *SQLiteStore) newBinaryIndex(dim int) *index.BinaryIndex {
	return index.NewBinaryIndex(dim, index.BinaryIndexOptions{
		Oversample: s.config.Binary.Oversample,
		Center:     !s.config.Binary.NoCentering,
	})
}

// initBinaryIndex builds the index at Init when the dimension is known.
// With the dimension still to be detected, the first insert creates it.
func (s *SQLiteStore) initBinaryIndex(ctx context.Context) error {
	if !s.binaryEnabled() {
		return nil
	}
	// As for a quantized HNSW: a store reopened with its dimension left to
	// auto-detection has vectors to index, and without this would search
	// them by scanning the table until the first insert told it the width.
	if s.config.VectorDim == 0 {
		if dim := s.storedVectorDim(ctx); dim > 0 {
			s.config.VectorDim = dim
		}
	}
	if s.config.VectorDim <= 0 {
		return nil
	}
	s.binaryMu.Lock()
	s.binaryIndex = s.newBinaryIndex(s.config.VectorDim)
	idx := s.binaryIndex
	s.binaryMu.Unlock()
	return s.reloadBinaryIndex(ctx, idx)
}

// reloadBinaryIndex replaces the index content with every vector of its
// dimension in the embeddings table, retraining the center. It streams the
// table (twice, when centering), so the vectors are never all in memory: the
// index keeps a 32nd of their size, and collecting them first made the
// float32 copy the whole of its peak.
func (s *SQLiteStore) reloadBinaryIndex(ctx context.Context, idx *index.BinaryIndex) error {
	return idx.RebuildStreaming(func(yield func(string, []float32) error) error {
		rows, err := s.db.QueryContext(ctx, "SELECT id, vector FROM embeddings")
		if err != nil {
			return fmt.Errorf("binary index: load vectors: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				return err
			}
			v, err := encoding.DecodeVector(raw)
			if err != nil || len(v) != idx.Dim() {
				continue // another width is not searchable by this index
			}
			if err := yield(id, v); err != nil {
				return err
			}
		}
		return rows.Err()
	})
}

// binaryIndexFor returns the index, creating it for this dimension if binary
// search is configured and none exists yet.
func (s *SQLiteStore) binaryIndexFor(dim int) *index.BinaryIndex {
	if !s.binaryEnabled() || dim <= 0 {
		return nil
	}
	s.binaryMu.Lock()
	defer s.binaryMu.Unlock()
	if s.binaryIndex == nil {
		s.binaryIndex = s.newBinaryIndex(dim)
	}
	return s.binaryIndex
}

// currentBinaryIndex is the index if one exists.
func (s *SQLiteStore) currentBinaryIndex() *index.BinaryIndex {
	s.binaryMu.Lock()
	defer s.binaryMu.Unlock()
	return s.binaryIndex
}

// binaryAdd indexes committed embeddings, and recenters once the store has
// doubled since the center was learned: a center learned from the first
// handful of rows of a growing store is a poor center for the rest.
func (s *SQLiteStore) binaryAdd(ctx context.Context, embs ...*Embedding) {
	for _, emb := range embs {
		if emb == nil || emb.ID == "" {
			continue
		}
		idx := s.binaryIndexFor(len(emb.Vector))
		if idx == nil || idx.Dim() != len(emb.Vector) {
			continue
		}
		if err := idx.Insert(emb.ID, emb.Vector); err != nil {
			s.logger.Warn("failed to add vector to binary index", "id", emb.ID, "error", err)
		}
	}
	if idx := s.currentBinaryIndex(); idx != nil && idx.NeedsRetrain() {
		if err := s.reloadBinaryIndex(ctx, idx); err != nil {
			s.logger.Warn("failed to recenter binary index", "error", err)
		}
	}
}

// binaryDelete drops deleted ids from the index.
func (s *SQLiteStore) binaryDelete(ids ...string) {
	idx := s.currentBinaryIndex()
	if idx == nil {
		return
	}
	for _, id := range ids {
		idx.Delete(id)
	}
}

// searchWithBinary is the IndexTypeBinary search: Hamming shortlist, rows
// loaded by id, exact scoring and filtering by processCandidates.
func (s *SQLiteStore) searchWithBinary(ctx context.Context, idx *index.BinaryIndex, query []float32, opts SearchOptions) ([]ScoredEmbedding, error) {
	if opts.TopK <= 0 {
		opts.TopK = 10
	}
	if len(query) != idx.Dim() {
		return s.searchLinear(ctx, query, opts)
	}
	candidateIDs, _ := idx.Candidates(query, opts.TopK*idx.Oversample())
	if len(candidateIDs) == 0 {
		return s.searchLinear(ctx, query, opts)
	}
	candidates, err := s.fetchEmbeddingsByIDs(ctx, candidateIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch candidates: %w", err)
	}
	results, err := s.processCandidates(query, candidates, opts)
	if err != nil {
		return nil, err
	}
	// The shortlist is drawn from the whole store, so a collection or
	// metadata filter applied to it can leave fewer than TopK — the same
	// post-filter problem searchWithHNSW describes, with the same remedy.
	if len(results) < opts.TopK && (opts.Collection != "" || len(opts.Filter) > 0) {
		return s.searchLinear(ctx, query, opts)
	}
	return results, nil
}
