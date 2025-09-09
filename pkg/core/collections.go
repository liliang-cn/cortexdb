package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Collection represents a logical grouping of embeddings
type Collection struct {
	ID          int                    `json:"id"`
	Name        string                 `json:"name"`
	Dimensions  int                    `json:"dimensions"`
	Description string                 `json:"description,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// CollectionStats represents statistics for a collection
type CollectionStats struct {
	Name            string    `json:"name"`
	Count           int64     `json:"count"`
	Dimensions      int       `json:"dimensions"`
	Size            int64     `json:"size"`
	CreatedAt       time.Time `json:"created_at"`
	LastInsertedAt  time.Time `json:"last_inserted_at,omitempty"`
}

// CreateCollection creates a new collection
func (s *SQLiteStore) CreateCollection(ctx context.Context, name string, dimensions int) (*Collection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, wrapError("create_collection", ErrStoreClosed)
	}

	// Check if collection already exists
	var exists bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM collections WHERE name = ?)", name).Scan(&exists)
	if err != nil {
		return nil, wrapError("create_collection", fmt.Errorf("failed to check collection existence: %w", err))
	}
	if exists {
		return nil, wrapError("create_collection", fmt.Errorf("collection '%s' already exists", name))
	}

	// Allow 0 dimensions for auto-detection
	if dimensions < 0 {
		return nil, wrapError("create_collection", fmt.Errorf("dimensions must be non-negative"))
	}

	// Insert new collection
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO collections (name, dimensions, created_at, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, name, dimensions)
	if err != nil {
		return nil, wrapError("create_collection", fmt.Errorf("failed to create collection: %w", err))
	}

	_, err = result.LastInsertId()
	if err != nil {
		return nil, wrapError("create_collection", fmt.Errorf("failed to get collection ID: %w", err))
	}

	// Get the created collection directly without lock conflict
	collection := &Collection{}
	var metadataJSON sql.NullString
	var description sql.NullString

	err = s.db.QueryRowContext(ctx, `
		SELECT id, name, dimensions, description, metadata, created_at, updated_at
		FROM collections WHERE name = ?
	`, name).Scan(
		&collection.ID,
		&collection.Name,
		&collection.Dimensions,
		&description,
		&metadataJSON,
		&collection.CreatedAt,
		&collection.UpdatedAt,
	)

	if description.Valid {
		collection.Description = description.String
	}

	if err != nil {
		return nil, wrapError("create_collection", fmt.Errorf("failed to retrieve created collection: %w", err))
	}

	// Parse metadata if present
	if metadataJSON.Valid && metadataJSON.String != "" {
		if err := json.Unmarshal([]byte(metadataJSON.String), &collection.Metadata); err != nil {
			// Log error but don't fail
			collection.Metadata = nil
		}
	}

	return collection, nil
}

// GetCollection retrieves a collection by name
func (s *SQLiteStore) GetCollection(ctx context.Context, name string) (*Collection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("get_collection", ErrStoreClosed)
	}

	collection := &Collection{}
	var metadataJSON sql.NullString
	var description sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, dimensions, description, metadata, created_at, updated_at
		FROM collections WHERE name = ?
	`, name).Scan(
		&collection.ID,
		&collection.Name,
		&collection.Dimensions,
		&description,
		&metadataJSON,
		&collection.CreatedAt,
		&collection.UpdatedAt,
	)

	if description.Valid {
		collection.Description = description.String
	}

	if err == sql.ErrNoRows {
		return nil, wrapError("get_collection", fmt.Errorf("collection '%s' not found", name))
	}
	if err != nil {
		return nil, wrapError("get_collection", fmt.Errorf("failed to get collection: %w", err))
	}

	// Parse metadata if present
	if metadataJSON.Valid && metadataJSON.String != "" {
		if err := json.Unmarshal([]byte(metadataJSON.String), &collection.Metadata); err != nil {
			// Log error but don't fail
			collection.Metadata = nil
		}
	}

	return collection, nil
}

// ListCollections lists all collections
func (s *SQLiteStore) ListCollections(ctx context.Context) ([]*Collection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("list_collections", ErrStoreClosed)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, dimensions, description, metadata, created_at, updated_at
		FROM collections ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, wrapError("list_collections", fmt.Errorf("failed to list collections: %w", err))
	}
	defer rows.Close()

	var collections []*Collection
	for rows.Next() {
		collection := &Collection{}
		var metadataJSON sql.NullString
		var description sql.NullString

		err := rows.Scan(
			&collection.ID,
			&collection.Name,
			&collection.Dimensions,
			&description,
			&metadataJSON,
			&collection.CreatedAt,
			&collection.UpdatedAt,
		)
		if err != nil {
			return nil, wrapError("list_collections", fmt.Errorf("failed to scan collection: %w", err))
		}

		if description.Valid {
			collection.Description = description.String
		}

		// Parse metadata if present
		if metadataJSON.Valid && metadataJSON.String != "" {
			if err := json.Unmarshal([]byte(metadataJSON.String), &collection.Metadata); err != nil {
				// Log error but don't fail
				collection.Metadata = nil
			}
		}

		collections = append(collections, collection)
	}

	return collections, nil
}

// DeleteCollection deletes a collection and all its embeddings
func (s *SQLiteStore) DeleteCollection(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return wrapError("delete_collection", ErrStoreClosed)
	}

	// Start transaction
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError("delete_collection", fmt.Errorf("failed to start transaction: %w", err))
	}
	defer tx.Rollback()

	// Get collection ID first
	var collectionID int
	err = tx.QueryRowContext(ctx, "SELECT id FROM collections WHERE name = ?", name).Scan(&collectionID)
	if err == sql.ErrNoRows {
		return wrapError("delete_collection", fmt.Errorf("collection '%s' not found", name))
	}
	if err != nil {
		return wrapError("delete_collection", fmt.Errorf("failed to find collection: %w", err))
	}

	// Delete all embeddings in the collection
	_, err = tx.ExecContext(ctx, "DELETE FROM embeddings WHERE collection_id = ?", collectionID)
	if err != nil {
		return wrapError("delete_collection", fmt.Errorf("failed to delete embeddings: %w", err))
	}

	// Delete the collection
	_, err = tx.ExecContext(ctx, "DELETE FROM collections WHERE id = ?", collectionID)
	if err != nil {
		return wrapError("delete_collection", fmt.Errorf("failed to delete collection: %w", err))
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return wrapError("delete_collection", fmt.Errorf("failed to commit transaction: %w", err))
	}

	return nil
}

// GetCollectionStats returns statistics for a collection
func (s *SQLiteStore) GetCollectionStats(ctx context.Context, name string) (*CollectionStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("get_collection_stats", ErrStoreClosed)
	}

	// First get collection info
	collection, err := s.GetCollection(ctx, name)
	if err != nil {
		return nil, err
	}

	stats := &CollectionStats{
		Name:       collection.Name,
		Dimensions: collection.Dimensions,
		CreatedAt:  collection.CreatedAt,
	}

	// Get embedding count and size
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(LENGTH(vector)), 0)
		FROM embeddings WHERE collection_id = ?
	`, collection.ID).Scan(&stats.Count, &stats.Size)
	if err != nil {
		return nil, wrapError("get_collection_stats", fmt.Errorf("failed to get stats: %w", err))
	}

	// Get last inserted timestamp if any embeddings exist
	if stats.Count > 0 {
		err = s.db.QueryRowContext(ctx, `
			SELECT MAX(created_at) FROM embeddings WHERE collection_id = ?
		`, collection.ID).Scan(&stats.LastInsertedAt)
		if err != nil {
			// Don't fail for this
			stats.LastInsertedAt = time.Time{}
		}
	}

	return stats, nil
}

// getDefaultCollection gets or creates the default collection
func (s *SQLiteStore) getDefaultCollection(ctx context.Context) (*Collection, error) {
	// Try to get default collection first
	collection, err := s.GetCollection(ctx, "default")
	if err != nil {
		// If not found, create it
		if strings.Contains(fmt.Sprintf("%v", err), "not found") {
			return s.CreateCollection(ctx, "default", s.config.VectorDim)
		}
		return nil, err
	}
	return collection, nil
}