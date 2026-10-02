package core

import (
	"context"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/index"
)

// IndexTypeBinary on PostgreSQL: pgvector does binary quantization itself
// (0.7+), so this backend uses it rather than holding codes in Go. The index
// is an HNSW graph over binary_quantize(vector)::bit(N) with Hamming
// distance, and a search is one statement — the inner query walks that index
// for the k × Oversample nearest codes, the outer one reorders them by exact
// cosine distance on the vector column:
//
//	SELECT … FROM (SELECT * FROM embeddings
//	               ORDER BY binary_quantize(vector)::bit(N) <~> binary_quantize($1)
//	               LIMIT k×oversample) AS embeddings
//	ORDER BY vector <=> $1 LIMIT k
//
// Two things differ from SQLite. binary_quantize thresholds at zero, so there
// is no centering here (on the real embeddings measured, centering changed
// recall@10 by under a point either way). And the bit index replaces the
// float HNSW index rather than sitting beside it, which is the point: the bit
// graph is 1/32 the size of the vector graph and also indexes vectors wider
// than the 2000 dimensions pgvector's float index accepts.
//
// A search with a collection or metadata filter keeps the exact path: the
// filter is in the WHERE clause, and an index scan cut off at
// k × oversample before the filter would come back short.

// pgBitMaxIndexedDims is pgvector's limit for HNSW on the bit type.
const pgBitMaxIndexedDims = 64000

// buildBinaryIndex creates the bit index. It records why not when it cannot,
// and search stays exact.
func (s *PostgresStore) buildBinaryIndex(ctx context.Context) error {
	dim := s.config.VectorDim
	switch {
	case dim <= 0:
		s.indexed, s.binary, s.why = false, false, "no fixed dimension yet: a binary index needs one, so search is exact"
		return nil
	case dim > pgBitMaxIndexedDims:
		s.indexed, s.binary, s.why = false, false, fmt.Sprintf(
			"%d dimensions is past pgvector's %d-bit index limit, so search is exact", dim, pgBitMaxIndexedDims)
		return nil
	}
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_embeddings_vec_bq
		ON embeddings USING hnsw ((binary_quantize(vector)::bit(%d)) bit_hamming_ops)`, dim))
	if err != nil {
		// pgvector before 0.7 has no binary_quantize. Not fatal.
		s.indexed, s.binary, s.why = false, false, "binary index not built, search is exact: "+err.Error()
		return nil
	}
	s.indexed, s.binary, s.why = true, true, ""
	return nil
}

func (s *PostgresStore) binaryOversample() int {
	if s.config.Binary.Oversample > 0 {
		return s.config.Binary.Oversample
	}
	return index.DefaultBinaryOversample
}

// searchBinary is the unfiltered search through the bit index.
func (s *PostgresStore) searchBinary(ctx context.Context, query []float32, topK int, threshold float64) ([]ScoredEmbedding, error) {
	shortlist := topK * s.binaryOversample()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // read-only; rollback after commit is a no-op
	// An HNSW scan returns at most ef_search rows (default 40), so a
	// shortlist longer than that would be cut short without a word.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL hnsw.ef_search = %d", max(shortlist, 40))); err != nil {
		return nil, err
	}
	q := fmt.Sprintf(`
		SELECT %[1]s, 1 - (vector <=> $1) AS score
		FROM (SELECT * FROM embeddings
		      ORDER BY binary_quantize(vector)::bit(%[2]d) <~> binary_quantize($1::vector(%[2]d))
		      LIMIT $2) AS embeddings
		ORDER BY vector <=> $1
		LIMIT $3`, pgSearchColumns, s.config.VectorDim)
	rows, err := tx.QueryContext(ctx, q, PgVectorLiteral(query), shortlist, topK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScoredEmbedding
	for rows.Next() {
		e, err := scanPgScored(rows)
		if err != nil {
			return nil, err
		}
		if threshold > 0 && e.Score < threshold {
			continue
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}
