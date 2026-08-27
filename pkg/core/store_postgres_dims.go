package core

// Dimension bookkeeping for the PostgreSQL store.
//
// A store outlives the embedding model that filled it. Swap the model and the
// old rows keep their old width: vectors that cannot be compared with the new
// ones, that the index will not carry, and that therefore stop being
// retrievable while lexical search keeps working and hides the loss. These are
// the methods that make that visible and repairable, and they answer the same
// questions the SQLite store answers, in the same terms.
//
// What differs is only how a width is read. SQLite stores a vector as a BLOB
// and infers the dimension from its byte length; pgvector stores it as a typed
// value and reports the dimension directly, with vector_dims(). Same number,
// no header arithmetic.

import (
	"context"
	"fmt"
	"sort"
)

// DimensionReport groups stored vectors by collection and length, exactly as
// the SQLite store does: a collection whose declared dimension is 0 has never
// had one recorded, so its rows are counted but never called mismatched.
func (s *PostgresStore) DimensionReport(ctx context.Context) (*DimensionReport, error) {
	// SQLite also guards `length(vector) > 0`, because a BLOB can be empty.
	// pgvector cannot hold a zero-dimension vector at all, so the only guard
	// that can matter here is NULL — and the column is declared NOT NULL, so
	// this holds only for a table created by some older DDL.
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.name, c.dimensions, vector_dims(e.vector), count(*)
		FROM embeddings e
		JOIN collections c ON c.id = e.collection_id
		WHERE e.vector IS NOT NULL
		GROUP BY c.name, c.dimensions, vector_dims(e.vector)
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
		var declared, dim, count int
		if err := rows.Scan(&name, &declared, &dim, &count); err != nil {
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

// MismatchedEmbeddings returns rows whose stored vector width differs from
// wantDim, newest first, capped by limit (limit <= 0 means no cap). Content
// comes with them so a caller holding an embedder can re-embed the text.
//
// What "mismatched" can mean here is narrower than on SQLite, and worth being
// precise about, because the difference is in the column type rather than in
// this query.
//
// Init picks the column type from the configured dimension: `vector(N)` when
// one is known, bare `vector` when it is not (see store_postgres.go). Those
// two cases behave differently:
//
//   - Bare `vector`. Width is per row, so a store that was filled by one model
//     and then by another holds both widths side by side, exactly as SQLite
//     does, and this returns the rows of the wrong one. This is the case the
//     method exists for.
//
//   - `vector(N)`. PostgreSQL enforces the width at write time, so a row of
//     any other width was never accepted and drift *within* the table cannot
//     arise. What can still arise is drift between the table and the model:
//     the table was created as vector(768), the embedder now produces 1024,
//     and every row is mismatched — which this reports faithfully, because it
//     compares against wantDim rather than against the column.
//
// The second case is worth a caller's attention for a reason that belongs to
// the schema and not to this method: the repair pass writes the new vectors
// back with UpsertBatch, and a `vector(768)` column will refuse a 1024-wide
// value. On this backend widening the column (ALTER TABLE ... TYPE vector(N),
// then rebuilding the ANN index) is a migration the operator has to run; the
// report is honest about the state either way, which is what lets them know
// the migration is needed.
func (s *PostgresStore) MismatchedEmbeddings(ctx context.Context, wantDim, limit int) ([]*Embedding, error) {
	if wantDim <= 0 {
		return nil, fmt.Errorf("target dimension must be positive, got %d", wantDim)
	}
	// `e.id` after `created_at DESC` only breaks ties. SQLite leaves those to
	// the query planner; ordering rows written in the same clock tick is not
	// part of the contract either way, and a stable answer is easier to test.
	query := `
		SELECT e.id, e.content, c.name
		FROM embeddings e
		JOIN collections c ON c.id = e.collection_id
		WHERE e.vector IS NOT NULL
		  AND vector_dims(e.vector) <> $1
		  AND e.content IS NOT NULL
		  AND e.content <> ''
		ORDER BY e.created_at DESC, e.id
	`
	args := []any{wantDim}
	if limit > 0 {
		args = append(args, limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
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

// ReconcileCollectionDimensions brings each collection's declared dimension in
// line with what it actually stores, for collections whose vectors are now
// uniformly dim. Returns the number of collections updated.
//
// Re-embedding rewrites vectors but cannot know whether the recorded dimension
// was deliberate, so it is left alone until every row in the collection agrees.
// Without this the drift report keeps flagging rows that are in fact correct.
func (s *PostgresStore) ReconcileCollectionDimensions(ctx context.Context, dim int) (int, error) {
	if dim <= 0 {
		return 0, fmt.Errorf("dimension must be positive, got %d", dim)
	}
	// SQLite also stamps collections.updated_at here. This backend's
	// collections table has no such column (see Init), so there is nothing to
	// stamp — the row means the same thing, it just does not record when it
	// last changed.
	result, err := s.db.ExecContext(ctx, `
		UPDATE collections
		SET dimensions = $1
		WHERE dimensions <> $1
		  AND id IN (
			SELECT collection_id FROM embeddings
			WHERE vector IS NOT NULL
			GROUP BY collection_id
			HAVING min(vector_dims(vector)) = $1 AND max(vector_dims(vector)) = $1
		  )
	`, dim)
	if err != nil {
		return 0, fmt.Errorf("failed to reconcile collection dimensions: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return 0, nil // driver does not report it; the update still applied
	}
	return int(updated), nil
}

// SyncDeletedEmbeddingIDs and SyncUpsertedEmbeddings are no-ops here, and that
// is the whole implementation rather than a gap in it.
//
// On SQLiteStore these exist because the vector index lives in the process:
// HNSW and IVF are Go structures held on the store, and a write that reached
// the database by another path — UpsertBatchTx joining a caller's transaction,
// a delete committed elsewhere — leaves them describing rows that have moved.
// The methods hand the index the news.
//
// PostgresStore has no such structure. Its index is the pgvector HNSW index
// created in Init, it lives in the database, and PostgreSQL maintains it as
// part of the same transaction as the write. By the time either of these could
// be called, the index already agrees with the rows. There is nothing to tell.
//
// So: no in-process index is introduced in order to have something to sync.
// BrainStore's comment notes these return no error because a drifted index is
// a performance problem rather than a correctness one — on this backend it is
// not even that, because it cannot drift.

// SyncDeletedEmbeddingIDs is a no-op: pgvector's index is maintained by
// PostgreSQL, in the same transaction as the delete. See the note above.
func (s *PostgresStore) SyncDeletedEmbeddingIDs(context.Context, []string) {}

// SyncUpsertedEmbeddings is a no-op: pgvector's index is maintained by
// PostgreSQL, in the same transaction as the write. See the note above.
func (s *PostgresStore) SyncUpsertedEmbeddings(context.Context, []*Embedding) {}
