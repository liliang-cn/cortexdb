package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/internal/encoding"
)

// UpsertBatchTx writes embeddings inside an existing transaction. Call SyncUpsertedEmbeddings after commit.
func (s *SQLiteStore) UpsertBatchTx(ctx context.Context, tx *sql.Tx, embs []*Embedding) error {
	s.mu.RLock()
	closed := s.closed
	vectorDim := s.config.VectorDim
	s.mu.RUnlock()

	if closed {
		return wrapError("upsert_batch_tx", ErrStoreClosed)
	}
	if len(embs) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO embeddings (id, collection_id, vector, content, doc_id, metadata, acl, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		return wrapError("upsert_batch_tx", fmt.Errorf("failed to prepare statement: %w", err))
	}
	defer func() {
		if closeErr := stmt.Close(); closeErr != nil {
			s.logger.Warn("failed to close statement during transactional batch upsert", "error", closeErr)
		}
	}()

	for i, emb := range embs {
		if err := encoding.ValidateEmbedding(emb.Vector, vectorDim); err != nil {
			return wrapError("upsert_batch_tx", fmt.Errorf("invalid embedding at index %d: %w", i, err))
		}

		collectionID, err := lookupCollectionIDTx(ctx, tx, emb)
		if err != nil {
			return wrapError("upsert_batch_tx", fmt.Errorf("resolve collection at index %d: %w", i, err))
		}

		vectorBytes, err := encoding.EncodeVector(emb.Vector)
		if err != nil {
			return wrapError("upsert_batch_tx", fmt.Errorf("failed to encode vector at index %d: %w", i, err))
		}

		metadataJSON, err := encoding.EncodeMetadata(emb.Metadata)
		if err != nil {
			return wrapError("upsert_batch_tx", fmt.Errorf("failed to encode metadata at index %d: %w", i, err))
		}

		var aclJSON []byte
		if len(emb.ACL) > 0 {
			aclJSON, err = json.Marshal(emb.ACL)
			if err != nil {
				return wrapError("upsert_batch_tx", fmt.Errorf("failed to marshal ACL at index %d: %w", i, err))
			}
		}

		var docID sql.NullString
		if emb.DocID != "" {
			docID.String = emb.DocID
			docID.Valid = true
		}

		if _, err := stmt.ExecContext(ctx, emb.ID, collectionID, vectorBytes, emb.Content, docID, metadataJSON, aclJSON); err != nil {
			return wrapError("upsert_batch_tx", fmt.Errorf("failed to insert embedding at index %d: %w", i, err))
		}
	}

	return nil
}

// SyncUpsertedEmbeddings updates in-memory indexes after embeddings were committed through UpsertBatchTx.
func (s *SQLiteStore) SyncUpsertedEmbeddings(_ context.Context, embs []*Embedding) {
	if len(embs) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.config.HNSW.Enabled && s.hnswIndex != nil {
		for _, emb := range embs {
			if emb == nil || emb.ID == "" {
				continue
			}
			if err := s.hnswIndex.Insert(emb.ID, emb.Vector); err != nil {
				s.logger.Warn("failed to insert vector into HNSW index after transactional upsert", "id", emb.ID, "error", err)
			}
		}
	}

	if s.config.IndexType == IndexTypeIVF && s.ivfIndex != nil && s.ivfIndex.Trained {
		for _, emb := range embs {
			if emb == nil || emb.ID == "" {
				continue
			}
			if err := s.ivfIndex.Add(emb.ID, emb.Vector); err != nil {
				s.logger.Warn("failed to add vector to IVF index after transactional upsert", "id", emb.ID, "error", err)
			}
		}
	}
}

func lookupCollectionIDTx(ctx context.Context, tx *sql.Tx, emb *Embedding) (int, error) {
	if emb.CollectionID != 0 {
		return emb.CollectionID, nil
	}
	if emb.Collection == "" {
		return 1, nil
	}

	var collectionID int
	if err := tx.QueryRowContext(ctx, `SELECT id FROM collections WHERE name = ?`, emb.Collection).Scan(&collectionID); err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("collection %q not found", emb.Collection)
		}
		return 0, err
	}
	return collectionID, nil
}
