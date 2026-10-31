package core

import (
	"context"
	"database/sql"

	"github.com/liliang-cn/cortexdb/v2/internal/pgscan"
)

// beginVectorScan opens the transaction a vector search runs in, with
// pgscan.Setting applied: an unfiltered search of up to 1000 rows widens the
// HNSW scan to its limit, any other is ranked exactly. The caller rolls it
// back once its rows are read.
func (s *PostgresStore) beginVectorScan(ctx context.Context, limit int, filtered bool) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, pgscan.Setting(limit, filtered)); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}
