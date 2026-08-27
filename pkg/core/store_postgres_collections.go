package core

// Collection statistics, and an honest answer about quantization.

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// GetCollectionStats counts what is in a collection and how much room it takes.
//
// Size is pg_column_size rather than SQLite's LENGTH(vector). LENGTH on a
// pgvector column is its dimension count, not its bytes, so reusing the same
// SQL would have returned a number that looked plausible and meant something
// else — the worst kind of wrong for a statistic nobody double-checks.
// pg_column_size reports the stored width including TOAST compression, which
// is the closest true analogue of what SQLite is measuring.
func (s *PostgresStore) GetCollectionStats(ctx context.Context, name string) (*CollectionStats, error) {
	collection, err := s.GetCollection(ctx, name)
	if err != nil {
		return nil, err
	}
	stats := &CollectionStats{
		Name:       collection.Name,
		Dimensions: collection.Dimensions,
		CreatedAt:  collection.CreatedAt,
	}
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(pg_column_size(vector)), 0)
		FROM embeddings WHERE collection_id = $1`, collection.ID).
		Scan(&stats.Count, &stats.Size)
	if err != nil {
		return nil, fmt.Errorf("get collection stats: %w", err)
	}

	if stats.Count > 0 {
		var last sql.NullTime
		// A missing timestamp is not worth failing the whole report over —
		// the same call SQLite makes and the same shrug when it does not
		// answer.
		if err := s.db.QueryRowContext(ctx,
			`SELECT MAX(created_at) FROM embeddings WHERE collection_id = $1`, collection.ID).
			Scan(&last); err == nil && last.Valid {
			stats.LastInsertedAt = last.Time
		} else {
			stats.LastInsertedAt = time.Time{}
		}
	}
	return stats, nil
}

// TrainQuantizer has nothing to train here, and says so rather than
// pretending.
//
// SQLiteStore trains a quantizer for the index it keeps in process. pgvector
// has no such object: compression is a column type chosen at DDL time —
// halfvec for 16-bit, bit for binary — so it is a migration, not a training
// run. A silent no-op would be the wrong answer, because a caller reaching for
// this wants their vectors to get smaller and would be told they had.
//
// Contrast TrainIndex, which IS a no-op here: the index genuinely exists and
// PostgreSQL maintains it. Nothing is being skipped there. Here it would be.
func (s *PostgresStore) TrainQuantizer(context.Context) error {
	return fmt.Errorf(
		"TrainQuantizer: pgvector has no trainable quantizer — compression is a column type "+
			"(halfvec, bit) chosen when the table is created, so it is a migration rather than a "+
			"training run: %w", ErrPostgresStoreUnimplemented)
}
