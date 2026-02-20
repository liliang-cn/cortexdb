package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Document represents a high-level document containing multiple embeddings (chunks)
type Document struct {
	ID        string                 `json:"id"`
	Title     string                 `json:"title"`
	SourceURL string                 `json:"source_url,omitempty"`
	Version   int                    `json:"version"`
	Author    string                 `json:"author,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	ACL       []string               `json:"acl,omitempty"` // Allowed user IDs or groups
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// CreateDocument creates a new document record
func (s *SQLiteStore) CreateDocument(ctx context.Context, doc *Document) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return wrapError("create_document", ErrStoreClosed)
	}

	metadataJSON, err := json.Marshal(doc.Metadata)
	if err != nil {
		return wrapError("create_document", fmt.Errorf("failed to marshal metadata: %w", err))
	}

	aclJSON, err := json.Marshal(doc.ACL)
	if err != nil {
		return wrapError("create_document", fmt.Errorf("failed to marshal ACL: %w", err))
	}

	query := `
		INSERT INTO documents (id, title, source_url, version, author, metadata, acl, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`

	_, err = s.db.ExecContext(ctx, query, doc.ID, doc.Title, doc.SourceURL, doc.Version, doc.Author, metadataJSON, aclJSON)
	if err != nil {
		return wrapError("create_document", fmt.Errorf("failed to insert document: %w", err))
	}

	return nil
}

// GetDocument retrieves a document by ID
func (s *SQLiteStore) GetDocument(ctx context.Context, id string) (*Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("get_document", ErrStoreClosed)
	}

	var doc Document
	var metadataJSON, aclJSON []byte

	query := `
		SELECT id, title, source_url, version, author, metadata, acl, created_at, updated_at
		FROM documents WHERE id = ?
	`

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&doc.ID, &doc.Title, &doc.SourceURL, &doc.Version, &doc.Author,
		&metadataJSON, &aclJSON, &doc.CreatedAt, &doc.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, wrapError("get_document", ErrNotFound)
	}
	if err != nil {
		return nil, wrapError("get_document", err)
	}

	if len(metadataJSON) > 0 {
		_ = json.Unmarshal(metadataJSON, &doc.Metadata)
	}
	if len(aclJSON) > 0 {
		_ = json.Unmarshal(aclJSON, &doc.ACL)
	}

	return &doc, nil
}

// DeleteDocument deletes a document and all its associated embeddings (chunks)
func (s *SQLiteStore) DeleteDocument(ctx context.Context, id string) error {
	s.mu.Lock() // Write lock needed for cascading deletes in HNSW
	defer s.mu.Unlock()

	if s.closed {
		return wrapError("delete_document", ErrStoreClosed)
	}

	// 1. Find all embedding IDs for this document to remove from HNSW index
	// Note: SQLite FK CASCADE will handle the table rows, but we must manually update memory index
	if s.hnswIndex != nil || s.ivfIndex != nil {
		rows, err := s.db.QueryContext(ctx, "SELECT id FROM embeddings WHERE doc_id = ?", id)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var embID string
				if err := rows.Scan(&embID); err == nil {
					if s.hnswIndex != nil {
						_ = s.hnswIndex.Delete(embID)
					}
					if s.ivfIndex != nil {
						_ = s.ivfIndex.Delete(embID)
					}
				}
			}
		}
	}

	// 2. Delete document (Cascade will delete embeddings from DB)
	_, err := s.db.ExecContext(ctx, "DELETE FROM documents WHERE id = ?", id)
	if err != nil {
		return wrapError("delete_document", fmt.Errorf("failed to delete document: %w", err))
	}

	return nil
}

// UpdateDocument updates an existing document's metadata and other fields
func (s *SQLiteStore) UpdateDocument(ctx context.Context, doc *Document) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return wrapError("update_document", ErrStoreClosed)
	}

	metadataJSON, err := json.Marshal(doc.Metadata)
	if err != nil {
		return wrapError("update_document", fmt.Errorf("failed to marshal metadata: %w", err))
	}

	aclJSON, err := json.Marshal(doc.ACL)
	if err != nil {
		return wrapError("update_document", fmt.Errorf("failed to marshal ACL: %w", err))
	}

	query := `
		UPDATE documents
		SET title = ?, source_url = ?, version = ?, author = ?, metadata = ?, acl = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`

	result, err := s.db.ExecContext(ctx, query, doc.Title, doc.SourceURL, doc.Version, doc.Author, metadataJSON, aclJSON, doc.ID)
	if err != nil {
		return wrapError("update_document", fmt.Errorf("failed to update document: %w", err))
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return wrapError("update_document", err)
	}
	if rowsAffected == 0 {
		return wrapError("update_document", ErrNotFound)
	}

	return nil
}

// ListDocumentsWithFilter lists documents matching specific criteria
// TODO: Add more filter options as needed
func (s *SQLiteStore) ListDocumentsWithFilter(ctx context.Context, author string, limit int) ([]*Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
		SELECT id, title, source_url, version, author, metadata, acl, created_at, updated_at
		FROM documents WHERE 1=1
	`
	args := []interface{}{}

	if author != "" {
		query += " AND author = ?"
		args = append(args, author)
	}

	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapError("list_documents", err)
	}
	defer rows.Close()

	var docs []*Document
	for rows.Next() {
		var doc Document
		var metadataJSON, aclJSON []byte
		if err := rows.Scan(&doc.ID, &doc.Title, &doc.SourceURL, &doc.Version, &doc.Author, &metadataJSON, &aclJSON, &doc.CreatedAt, &doc.UpdatedAt); err != nil {
			continue
		}
		if len(metadataJSON) > 0 {
			_ = json.Unmarshal(metadataJSON, &doc.Metadata)
		}
		if len(aclJSON) > 0 {
			_ = json.Unmarshal(aclJSON, &doc.ACL)
		}
		docs = append(docs, &doc)
	}

	return docs, nil
}
