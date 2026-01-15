package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/liliang-cn/sqvect/v2/internal/encoding"
)

// GetByID gets an embedding by its ID
func (s *SQLiteStore) GetByID(ctx context.Context, id string) (*Embedding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("get_by_id", ErrStoreClosed)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			e.id, e.vector, e.content, e.doc_id, e.metadata, e.acl, e.created_at,
			COALESCE(c.name, '') as collection_name
		FROM embeddings e
		LEFT JOIN collections c ON e.collection_id = c.id
		WHERE e.id = ?
	`, id)
	if err != nil {
		return nil, wrapError("get_by_id", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			s.logger.Warn("failed to close rows during get by ID", "error", closeErr)
		}
	}()

	if !rows.Next() {
		return nil, wrapError("get_by_id", ErrNotFound)
	}

	emb, err := s.scanEmbeddingForGet(rows)
	if err != nil {
		return nil, wrapError("get_by_id", err)
	}

	return emb, nil
}

// GetByDocID returns all embeddings for a specific document ID
func (s *SQLiteStore) GetByDocID(ctx context.Context, docID string) ([]*Embedding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("get_by_doc_id", ErrStoreClosed)
	}

	if docID == "" {
		return nil, wrapError("get_by_doc_id", fmt.Errorf("doc ID cannot be empty"))
	}

	query := "SELECT id, vector, content, doc_id, metadata FROM embeddings WHERE doc_id = ? ORDER BY created_at"
	rows, err := s.db.QueryContext(ctx, query, docID)
	if err != nil {
		return nil, wrapError("get_by_doc_id", fmt.Errorf("failed to query embeddings: %w", err))
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			s.logger.Warn("failed to close rows during get by doc ID", "error", closeErr)
		}
	}()

	var embeddings []*Embedding
	for rows.Next() {
		embedding, err := s.scanEmbeddingForGet(rows)
		if err != nil {
			s.logger.Warn("failed to scan embedding during get by doc ID", "error", err)
			continue // Skip invalid embeddings
		}
		embeddings = append(embeddings, embedding)
	}

	if err := rows.Err(); err != nil {
		return nil, wrapError("get_by_doc_id", fmt.Errorf("error iterating rows: %w", err))
	}

	return embeddings, nil
}

// ListDocuments returns all unique document IDs in the store
func (s *SQLiteStore) ListDocuments(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("list_documents", ErrStoreClosed)
	}

	query := "SELECT DISTINCT doc_id FROM embeddings WHERE doc_id IS NOT NULL AND doc_id != '' ORDER BY doc_id"
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, wrapError("list_documents", fmt.Errorf("failed to query documents: %w", err))
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			s.logger.Warn("failed to close rows during list documents", "error", closeErr)
		}
	}()

	var docIDs []string
	for rows.Next() {
		var docID string
		if err := rows.Scan(&docID); err != nil {
			return nil, wrapError("list_documents", fmt.Errorf("failed to scan doc_id: %w", err))
		}
		docIDs = append(docIDs, docID)
	}

	if err := rows.Err(); err != nil {
		return nil, wrapError("list_documents", fmt.Errorf("error iterating rows: %w", err))
	}

	return docIDs, nil
}

// ListDocumentsWithInfo returns detailed information about documents
func (s *SQLiteStore) ListDocumentsWithInfo(ctx context.Context) ([]DocumentInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("list_documents_with_info", ErrStoreClosed)
	}

	query := `
	SELECT
		doc_id,
		COUNT(*) as embedding_count,
		MIN(created_at) as first_created,
		MAX(created_at) as last_updated
	FROM embeddings
	WHERE doc_id IS NOT NULL AND doc_id != ''
	GROUP BY doc_id
	ORDER BY doc_id
	`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, wrapError("list_documents_with_info", fmt.Errorf("failed to query document info: %w", err))
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			s.logger.Warn("failed to close rows during list documents with info", "error", closeErr)
		}
	}()

	var documents []DocumentInfo
	for rows.Next() {
		var docID, firstCreated, lastUpdated string
		var embeddingCount int

		if err := rows.Scan(&docID, &embeddingCount, &firstCreated, &lastUpdated); err != nil {
			return nil, wrapError("list_documents_with_info", fmt.Errorf("failed to scan row: %w", err))
		}

		docInfo := DocumentInfo{
			DocID:          docID,
			EmbeddingCount: embeddingCount,
			FirstCreated:   &firstCreated,
			LastUpdated:    &lastUpdated,
		}

		documents = append(documents, docInfo)
	}

	if err := rows.Err(); err != nil {
		return nil, wrapError("list_documents_with_info", fmt.Errorf("error iterating rows: %w", err))
	}

	return documents, nil
}

// GetDocumentsByType returns documents filtered by metadata type
func (s *SQLiteStore) GetDocumentsByType(ctx context.Context, docType string) ([]*Embedding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("get_documents_by_type", ErrStoreClosed)
	}

	if docType == "" {
		return nil, wrapError("get_documents_by_type", fmt.Errorf("doc type cannot be empty"))
	}

	query := "SELECT id, vector, content, doc_id, metadata FROM embeddings ORDER BY created_at"
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, wrapError("get_documents_by_type", fmt.Errorf("failed to query embeddings: %w", err))
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			s.logger.Warn("failed to close rows during get documents by type", "error", closeErr)
		}
	}()

	var embeddings []*Embedding
	for rows.Next() {
		embedding, err := s.scanEmbeddingForGet(rows)
		if err != nil {
			s.logger.Warn("failed to scan embedding during get documents by type", "error", err)
			continue // Skip invalid embeddings
		}

		// Filter by type in metadata
		if embedding.Metadata != nil && embedding.Metadata["type"] == docType {
			embeddings = append(embeddings, embedding)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, wrapError("get_documents_by_type", fmt.Errorf("error iterating rows: %w", err))
	}

	return embeddings, nil
}

// scanEmbeddingForGet scans a row into an embedding for Get methods
func (s *SQLiteStore) scanEmbeddingForGet(rows *sql.Rows) (*Embedding, error) {
	var id, content, metadataJSON string
	var docID sql.NullString
	var aclJSON []byte
	var vectorBytes []byte

	// Check columns count
	cols, _ := rows.Columns()

	var collectionName string
	var createdAt time.Time

	var err error

	if len(cols) == 8 { // GetByID format
		err = rows.Scan(&id, &vectorBytes, &content, &docID, &metadataJSON, &aclJSON, &createdAt, &collectionName)
	} else if len(cols) == 5 { // Old format (GetByDocID)
		err = rows.Scan(&id, &vectorBytes, &content, &docID, &metadataJSON)
	} else {
		return nil, fmt.Errorf("unexpected column count: %d", len(cols))
	}

	if err != nil {
		return nil, fmt.Errorf("failed to scan row: %w", err)
	}

	vector, err := encoding.DecodeVector(vectorBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to decode vector: %w", err)
	}

	metadata, err := encoding.DecodeMetadata(metadataJSON)
	if err != nil {
		metadata = nil // Continue with nil metadata
	}

	var acl []string
	if len(aclJSON) > 0 {
		if err := json.Unmarshal(aclJSON, &acl); err != nil {
			s.logger.Warn("failed to unmarshal ACL", "error", err)
		}
	}

	return &Embedding{
		ID:        id,
		Collection: collectionName,
		Vector:    vector,
		Content:   content,
		DocID:     docID.String,
		Metadata:  metadata,
		ACL:       acl,
	}, nil
}
