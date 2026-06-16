package connector

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Checkpoint is a source's resumable position. Cursor carries Route A's per-table
// watermark (JSON); Position carries Route B's LSN / binlog position. Only one is
// used per source kind.
type Checkpoint struct {
	Cursor   string
	Position string
}

// CheckpointStore persists and loads a source's Checkpoint by a stable key.
type CheckpointStore interface {
	Load(ctx context.Context, sourceKey string) (Checkpoint, bool, error)
	Save(ctx context.Context, sourceKey string, cp Checkpoint) error
}

// SQLiteCheckpointStore stores checkpoints in the knowledge DB, co-located with
// the data they track.
type SQLiteCheckpointStore struct{ db *sql.DB }

// NewSQLiteCheckpointStore creates the state table on the knowledge DB handle.
func NewSQLiteCheckpointStore(cdb *cortexdb.DB) (*SQLiteCheckpointStore, error) {
	sdb := cdb.SQL()
	if _, err := sdb.Exec(`CREATE TABLE IF NOT EXISTS connector_sync_state (
		source_key TEXT PRIMARY KEY,
		cursor     TEXT,
		position   TEXT,
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return nil, fmt.Errorf("connector: create sync state table: %w", err)
	}
	return &SQLiteCheckpointStore{db: sdb}, nil
}

func (s *SQLiteCheckpointStore) Load(ctx context.Context, sourceKey string) (Checkpoint, bool, error) {
	var cp Checkpoint
	err := s.db.QueryRowContext(ctx,
		`SELECT cursor, position FROM connector_sync_state WHERE source_key=?`, sourceKey).
		Scan(&cp.Cursor, &cp.Position)
	if err == sql.ErrNoRows {
		return Checkpoint{}, false, nil
	}
	if err != nil {
		return Checkpoint{}, false, err
	}
	return cp, true, nil
}

func (s *SQLiteCheckpointStore) Save(ctx context.Context, sourceKey string, cp Checkpoint) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO connector_sync_state (source_key, cursor, position, updated_at)
		 VALUES (?,?,?, datetime('now'))
		 ON CONFLICT(source_key) DO UPDATE SET cursor=excluded.cursor, position=excluded.position, updated_at=datetime('now')`,
		sourceKey, cp.Cursor, cp.Position)
	return err
}
