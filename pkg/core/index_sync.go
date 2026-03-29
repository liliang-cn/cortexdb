package core

import "context"

// SyncDeletedEmbeddingIDs removes committed embedding deletions from in-memory indexes.
func (s *SQLiteStore) SyncDeletedEmbeddingIDs(_ context.Context, ids []string) {
	if len(ids) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

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
}
