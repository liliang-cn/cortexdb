package core

import (
	"context"
	"database/sql"
)

// SyncDeletedEmbeddingIDs removes committed embedding deletions from in-memory indexes.
func (s *SQLiteStore) SyncDeletedEmbeddingIDs(_ context.Context, ids []string) {
	if len(ids) == 0 {
		return
	}

	// Shared, as in SyncUpsertedEmbeddings: each index locks itself. Asked
	// for exclusively, the lock made this call wait for any sync under way,
	// and every new search wait behind it.
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.forgetIndexedIDs(ids)
}

// forgetIndexedIDs drops ids from every in-memory index. Each index locks
// itself; the caller holds s.mu, read or write, so the index pointers stay put.
func (s *SQLiteStore) forgetIndexedIDs(ids []string) {
	if len(ids) == 0 {
		return
	}
	if s.hnswIndex != nil {
		for _, id := range ids {
			if id == "" {
				continue
			}
			_ = s.hnswIndex.Delete(id)
		}
	}
	if s.ivfIndex != nil {
		for _, id := range ids {
			if id == "" {
				continue
			}
			_ = s.ivfIndex.Delete(id)
		}
	}
	s.binaryDelete(ids...)
}

// hasMemoryIndex reports whether any index lives in memory beside the table,
// so a delete has ids to tell it about.
func (s *SQLiteStore) hasMemoryIndex() bool {
	return s.hnswIndex != nil || s.ivfIndex != nil || s.currentBinaryIndex() != nil
}

// queryer is what both *sql.DB and *sql.Tx answer.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// idsToForget lists the embedding ids a delete is about to remove, for the
// in-memory indexes to drop after it. A delete by document, collection or
// everything used to leave its ids in the HNSW index: searches spent result
// slots on rows that no longer existed, and every snapshot kept them.
func (s *SQLiteStore) idsToForget(ctx context.Context, q queryer, query string, args ...any) []string {
	if !s.hasMemoryIndex() {
		return nil
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		s.logger.Warn("failed to list ids for index removal", "error", err)
		return nil
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}
