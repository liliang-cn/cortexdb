package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/liliang-cn/sqvect/internal/encoding"
	"github.com/liliang-cn/sqvect/pkg/quantization"
)

// Upsert inserts or updates a single embedding
func (s *SQLiteStore) Upsert(ctx context.Context, emb *Embedding) error {
	s.mu.RLock()
	currentDim := s.config.VectorDim
	s.mu.RUnlock()

	if s.closed {
		return wrapError("upsert", ErrStoreClosed)
	}

	incomingDim := len(emb.Vector)

	// Auto-detect dimension on first insert
	if currentDim == 0 {
		s.mu.Lock()
		if s.config.VectorDim == 0 { // Double-check after acquiring write lock
			s.config.VectorDim = incomingDim
			currentDim = incomingDim

			// Initialize quantizer now that we know the dimension
			if s.config.Quantization.Enabled && s.quantizer == nil {
				if s.config.Quantization.Type == "binary" {
					s.quantizer = quantization.NewBinaryQuantizer(currentDim)
				} else {
					sq, err := quantization.NewScalarQuantizer(currentDim, s.config.Quantization.NBits)
					if err != nil {
						s.logger.Warn("failed to create scalar quantizer", "error", err)
					} else {
						s.quantizer = sq
					}
				}
				if s.hnswIndex != nil {
					s.hnswIndex.SetQuantizer(s.quantizer)
				}
			}
		} else {
			currentDim = s.config.VectorDim
		}
		s.mu.Unlock()
	}

	// Auto-train quantizer if not trained
	if s.quantizer != nil {
		trained := false
		if sq, ok := s.quantizer.(*quantization.ScalarQuantizer); ok {
			trained = sq.Trained
		} else if bq, ok := s.quantizer.(*quantization.BinaryQuantizer); ok {
			trained = bq.Trained
		}

		if !trained {
			if err := s.TrainQuantizer(ctx); err != nil {
				s.logger.Warn("failed to auto-train quantizer", "error", err)
			}
		}
	}

	// Handle dimension mismatch
	if incomingDim != currentDim {
		adaptedVector, err := s.adapter.AdaptVector(emb.Vector, incomingDim, currentDim)
		if err != nil {
			return wrapError("upsert", err)
		}
		s.adapter.logDimensionEvent("adapt", incomingDim, currentDim, emb.ID)
		emb.Vector = adaptedVector
	}

	// Validate adapted embedding
	if err := encoding.ValidateEmbedding(*emb, currentDim); err != nil {
		return wrapError("upsert", err)
	}

	// Re-acquire read lock for database operations
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Determine collection ID
	collectionID := emb.CollectionID
	if collectionID == 0 {
		// If no collection specified, use default or get by name
		if emb.Collection != "" {
			collection, err := s.GetCollection(ctx, emb.Collection)
			if err != nil {
				return wrapError("upsert", fmt.Errorf("collection '%s' not found: %w", emb.Collection, err))
			}
			collectionID = collection.ID
		} else {
			collectionID = 1 // Default collection
		}
	}

	// Encode vector and metadata
	vectorBytes, err := encoding.EncodeVector(emb.Vector)
	if err != nil {
		return wrapError("upsert", err)
	}

	metadataJSON, err := encoding.EncodeMetadata(emb.Metadata)
	if err != nil {
		return wrapError("upsert", err)
	}

	// Encode ACL
	var aclJSON []byte
	if len(emb.ACL) > 0 {
		aclJSON, err = json.Marshal(emb.ACL)
		if err != nil {
			return wrapError("upsert", fmt.Errorf("failed to marshal ACL: %w", err))
		}
	}

	// Handle DocID (treat empty as NULL)
	var docID sql.NullString
	if emb.DocID != "" {
		docID.String = emb.DocID
		docID.Valid = true
	}

	// Insert or replace
	query := `
	INSERT OR REPLACE INTO embeddings (id, collection_id, vector, content, doc_id, metadata, acl, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`

	_, err = s.db.ExecContext(ctx, query, emb.ID, collectionID, vectorBytes, emb.Content, docID, metadataJSON, aclJSON)
	if err != nil {
		return wrapError("upsert", fmt.Errorf("failed to insert embedding: %w", err))
	}

	// Update HNSW index if enabled
	if s.config.HNSW.Enabled && s.hnswIndex != nil {
		if err := s.hnswIndex.Insert(emb.ID, emb.Vector); err != nil {
			s.logger.Warn("failed to insert vector into HNSW index", "id", emb.ID, "error", err)
		}
	}

	// Update IVF index if enabled and trained
	if s.config.IndexType == IndexTypeIVF && s.ivfIndex != nil && s.ivfIndex.Trained {
		if err := s.ivfIndex.Add(emb.ID, emb.Vector); err != nil {
			s.logger.Warn("failed to add vector to IVF index", "id", emb.ID, "error", err)
		}
	}

	return nil
}

// UpsertBatch inserts or updates multiple embeddings in a transaction
func (s *SQLiteStore) UpsertBatch(ctx context.Context, embs []*Embedding) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return wrapError("upsert_batch", ErrStoreClosed)
	}

	if len(embs) == 0 {
		return nil
	}

	// Auto-train quantizer if not trained
	if s.quantizer != nil {
		trained := false
		if sq, ok := s.quantizer.(*quantization.ScalarQuantizer); ok {
			trained = sq.Trained
		} else if bq, ok := s.quantizer.(*quantization.BinaryQuantizer); ok {
			trained = bq.Trained
		}

		if !trained {
			// Extract some vectors for training
			var trainingVectors [][]float32
			for i := 0; i < len(embs) && i < 1000; i++ {
				trainingVectors = append(trainingVectors, embs[i].Vector)
			}
			if sq, ok := s.quantizer.(*quantization.ScalarQuantizer); ok {
				if err := sq.Train(trainingVectors); err != nil {
					s.logger.Warn("failed to train scalar quantizer during batch upsert", "error", err)
				}
			} else if bq, ok := s.quantizer.(*quantization.BinaryQuantizer); ok {
				if err := bq.Train(trainingVectors); err != nil {
					s.logger.Warn("failed to train binary quantizer during batch upsert", "error", err)
				}
			}
		}
	}

	// Start transaction
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError("upsert_batch", fmt.Errorf("failed to begin transaction: %w", err))
	}
	defer func() {
		if rollErr := tx.Rollback(); rollErr != nil {
			s.logger.Warn("failed to rollback transaction during batch upsert", "error", rollErr)
		}
	}()

	// Prepare statement
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO embeddings (id, collection_id, vector, content, doc_id, metadata, acl, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		return wrapError("upsert_batch", fmt.Errorf("failed to prepare statement: %w", err))
	}
	defer func() {
		if closeErr := stmt.Close(); closeErr != nil {
			s.logger.Warn("failed to close statement during batch upsert", "error", closeErr)
		}
	}()

	// Execute for each embedding
	for i, emb := range embs {
		if err := encoding.ValidateEmbedding(*emb, s.config.VectorDim); err != nil {
			return wrapError("upsert_batch", fmt.Errorf("invalid embedding at index %d: %w", i, err))
		}

		// Determine collection ID
		collectionID := emb.CollectionID
		if collectionID == 0 {
			// If no collection specified, use default or get by name
			if emb.Collection != "" {
				collection, err := s.GetCollection(ctx, emb.Collection)
				if err != nil {
					return wrapError("upsert_batch", fmt.Errorf("collection '%s' not found at index %d: %w", emb.Collection, i, err))
				}
				collectionID = collection.ID
			} else {
				collectionID = 1 // Default collection
			}
		}

		vectorBytes, err := encoding.EncodeVector(emb.Vector)
		if err != nil {
			return wrapError("upsert_batch", fmt.Errorf("failed to encode vector at index %d: %w", i, err))
		}

		metadataJSON, err := encoding.EncodeMetadata(emb.Metadata)
		if err != nil {
			return wrapError("upsert_batch", fmt.Errorf("failed to encode metadata at index %d: %w", i, err))
		}

		// Encode ACL
		var aclJSON []byte
		if len(emb.ACL) > 0 {
			aclJSON, err = json.Marshal(emb.ACL)
			if err != nil {
				return wrapError("upsert_batch", fmt.Errorf("failed to marshal ACL at index %d: %w", i, err))
			}
		}

		// Handle DocID
		var docID sql.NullString
		if emb.DocID != "" {
			docID.String = emb.DocID
			docID.Valid = true
		}

		_, err = stmt.ExecContext(ctx, emb.ID, collectionID, vectorBytes, emb.Content, docID, metadataJSON, aclJSON)
		if err != nil {
			return wrapError("upsert_batch", fmt.Errorf("failed to insert embedding at index %d: %w", i, err))
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return wrapError("upsert_batch", fmt.Errorf("failed to commit transaction: %w", err))
	}

	s.logger.Debug("batch upsert completed", "count", len(embs))

	// Update HNSW index if enabled
	if s.config.HNSW.Enabled && s.hnswIndex != nil {
		var errorCount int
		for _, emb := range embs {
			if err := s.hnswIndex.Insert(emb.ID, emb.Vector); err != nil {
				s.logger.Warn("failed to insert vector into HNSW index during batch upsert", "id", emb.ID, "error", err)
				errorCount++
			}
		}
		if errorCount > 0 {
			s.logger.Warn("some vectors failed to insert into HNSW index during batch upsert", "count", errorCount)
		}
	}

	// Update IVF index if enabled and trained
	if s.config.IndexType == IndexTypeIVF && s.ivfIndex != nil && s.ivfIndex.Trained {
		var errorCount int
		for _, emb := range embs {
			if err := s.ivfIndex.Add(emb.ID, emb.Vector); err != nil {
				s.logger.Warn("failed to add vector to IVF index during batch upsert", "id", emb.ID, "error", err)
				errorCount++
			}
		}
		if errorCount > 0 {
			s.logger.Warn("some vectors failed to add to IVF index during batch upsert", "count", errorCount)
		}
	}

	return nil
}

// Delete removes an embedding by ID
func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return wrapError("delete", ErrStoreClosed)
	}

	if id == "" {
		return wrapError("delete", fmt.Errorf("ID cannot be empty"))
	}

	result, err := s.db.ExecContext(ctx, "DELETE FROM embeddings WHERE id = ?", id)
	if err != nil {
		return wrapError("delete", fmt.Errorf("failed to delete embedding: %w", err))
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return wrapError("delete", fmt.Errorf("failed to get rows affected: %w", err))
	}

	if rowsAffected == 0 {
		return wrapError("delete", ErrNotFound)
	}

	// Update HNSW index if enabled
	if s.config.HNSW.Enabled && s.hnswIndex != nil {
		if err := s.hnswIndex.Delete(id); err != nil {
			s.logger.Warn("failed to delete vector from HNSW index", "id", id, "error", err)
		}
	}

	// Update IVF index if enabled
	if s.ivfIndex != nil {
		if err := s.ivfIndex.Delete(id); err != nil {
			s.logger.Warn("failed to delete vector from IVF index", "id", id, "error", err)
		}
	}

	return nil
}

// DeleteByDocID removes all embeddings for a document
func (s *SQLiteStore) DeleteByDocID(ctx context.Context, docID string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return wrapError("delete_by_doc_id", ErrStoreClosed)
	}

	if docID == "" {
		return wrapError("delete_by_doc_id", fmt.Errorf("doc ID cannot be empty"))
	}

	_, err := s.db.ExecContext(ctx, "DELETE FROM embeddings WHERE doc_id = ?", docID)
	if err != nil {
		return wrapError("delete_by_doc_id", fmt.Errorf("failed to delete embeddings: %w", err))
	}

	return nil
}

// DeleteBatch removes multiple embeddings by their IDs in a single operation
func (s *SQLiteStore) DeleteBatch(ctx context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return wrapError("delete_batch", ErrStoreClosed)
	}

	if len(ids) == 0 {
		return nil
	}

	// Filter out empty IDs
	validIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) != "" {
			validIDs = append(validIDs, id)
		}
	}

	if len(validIDs) == 0 {
		return nil
	}

	// 1. Delete from SQLite
	// Use chunks to avoid SQLite parameter limit (default 999)
	totalRowsAffected := int64(0)
	chunkSize := 500
	for i := 0; i < len(validIDs); i += chunkSize {
		end := i + chunkSize
		if end > len(validIDs) {
			end = len(validIDs)
		}

		chunk := validIDs[i:end]
		placeholders := make([]string, len(chunk))
		args := make([]interface{}, len(chunk))
		for j, id := range chunk {
			placeholders[j] = "?"
			args[j] = id
		}

		query := fmt.Sprintf("DELETE FROM embeddings WHERE id IN (%s)", strings.Join(placeholders, ","))
		result, err := s.db.ExecContext(ctx, query, args...)
		if err != nil {
			return wrapError("delete_batch", fmt.Errorf("failed to delete chunk: %w", err))
		}

		rows, err := result.RowsAffected()
		if err != nil {
			s.logger.Warn("failed to get rows affected during batch delete", "error", err)
		} else {
			totalRowsAffected += rows
		}
	}

	if totalRowsAffected == 0 {
		return wrapError("delete_batch", ErrNotFound)
	}

	// 2. Delete from Memory Indexes
	if s.hnswIndex != nil {
		for _, id := range validIDs {
			if err := s.hnswIndex.Delete(id); err != nil {
				s.logger.Warn("failed to delete vector from HNSW index during batch delete", "id", id, "error", err)
			}
		}
	}

	if s.ivfIndex != nil {
		for _, id := range validIDs {
			if err := s.ivfIndex.Delete(id); err != nil {
				s.logger.Warn("failed to delete vector from IVF index during batch delete", "id", id, "error", err)
			}
		}
	}

	s.logger.Debug("batch delete completed", "deleted", totalRowsAffected)

	return nil
}

// DeleteByFilter removes embeddings matching the given metadata filter
func (s *SQLiteStore) DeleteByFilter(ctx context.Context, filter *MetadataFilter) error {
	if filter == nil || filter.IsEmpty() {
		return wrapError("delete_by_filter", fmt.Errorf("filter cannot be empty"))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return wrapError("delete_by_filter", ErrStoreClosed)
	}

	// Build WHERE clause from filter
	whereClause, params := filter.ToSQL()
	if whereClause == "" {
		return wrapError("delete_by_filter", fmt.Errorf("failed to build filter"))
	}

	// First, get the IDs that will be deleted (for index cleanup)
	idQuery := fmt.Sprintf("SELECT id FROM embeddings WHERE %s", whereClause)
	rows, err := s.db.QueryContext(ctx, idQuery, params...)
	if err != nil {
		return wrapError("delete_by_filter", fmt.Errorf("failed to query embeddings: %w", err))
	}

	var idsToDelete []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			idsToDelete = append(idsToDelete, id)
		}
	}
	if closeErr := rows.Close(); closeErr != nil {
		s.logger.Warn("failed to close rows during delete by filter", "error", closeErr)
	}

	if len(idsToDelete) == 0 {
		return nil // Nothing to delete
	}

	// Now delete the embeddings
	deleteQuery := fmt.Sprintf("DELETE FROM embeddings WHERE %s", whereClause)
	_, err = s.db.ExecContext(ctx, deleteQuery, params...)
	if err != nil {
		return wrapError("delete_by_filter", fmt.Errorf("failed to delete embeddings: %w", err))
	}

	// Update Memory Indexes
	if s.hnswIndex != nil {
		for _, id := range idsToDelete {
			if err := s.hnswIndex.Delete(id); err != nil {
				s.logger.Warn("failed to delete vector from HNSW index during filter delete", "id", id, "error", err)
			}
		}
	}

	if s.ivfIndex != nil {
		for _, id := range idsToDelete {
			if err := s.ivfIndex.Delete(id); err != nil {
				s.logger.Warn("failed to delete vector from IVF index during filter delete", "id", id, "error", err)
			}
		}
	}

	s.logger.Debug("delete by filter completed", "deleted", len(idsToDelete))

	return nil
}

// Clear removes all embeddings from the store
func (s *SQLiteStore) Clear(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return wrapError("clear", ErrStoreClosed)
	}

	_, err := s.db.ExecContext(ctx, "DELETE FROM embeddings")
	if err != nil {
		return wrapError("clear", fmt.Errorf("failed to clear embeddings: %w", err))
	}

	s.logger.Info("cleared all embeddings")

	return nil
}

// ClearByDocID removes all embeddings for specific document IDs
func (s *SQLiteStore) ClearByDocID(ctx context.Context, docIDs []string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return wrapError("clear_by_doc_id", ErrStoreClosed)
	}

	if len(docIDs) == 0 {
		return nil // Nothing to clear
	}

	// Start transaction for batch deletion
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError("clear_by_doc_id", fmt.Errorf("failed to begin transaction: %w", err))
	}
	defer func() {
		if rollErr := tx.Rollback(); rollErr != nil {
			s.logger.Warn("failed to rollback transaction during clear by doc ID", "error", rollErr)
		}
	}()

	// Prepare statement for deletion
	stmt, err := tx.PrepareContext(ctx, "DELETE FROM embeddings WHERE doc_id = ?")
	if err != nil {
		return wrapError("clear_by_doc_id", fmt.Errorf("failed to prepare statement: %w", err))
	}
	defer func() {
		if closeErr := stmt.Close(); closeErr != nil {
			s.logger.Warn("failed to close statement during clear by doc ID", "error", closeErr)
		}
	}()

	// Execute deletion for each doc ID
	for _, docID := range docIDs {
		if docID == "" {
			continue // Skip empty doc IDs
		}
		_, err = stmt.ExecContext(ctx, docID)
		if err != nil {
			return wrapError("clear_by_doc_id", fmt.Errorf("failed to delete embeddings for doc_id %s: %w", docID, err))
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return wrapError("clear_by_doc_id", fmt.Errorf("failed to commit transaction: %w", err))
	}

	s.logger.Debug("cleared embeddings by doc IDs", "count", len(docIDs))

	return nil
}
