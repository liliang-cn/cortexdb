// Package core provides multi-vector entity support
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/liliang-cn/sqvect/v2/internal/encoding"
)

// MultiVectorEntity represents an entity with multiple vectors
type MultiVectorEntity struct {
	// Entity ID
	ID string
	
	// Vectors associated with this entity
	Vectors map[string][]float32
	
	// Metadata for the entity
	Metadata map[string]interface{}
	
	// Content/text for the entity
	Content string
}

// MultiVectorSearchOptions for multi-vector search
type MultiVectorSearchOptions struct {
	// Which vector fields to search
	VectorFields []string
	
	// Weights for each vector field
	FieldWeights map[string]float32
	
	// Aggregation method for combining scores
	Aggregation AggregationMethod
	
	// Standard search options
	SearchOptions
}

// AggregationMethod for combining multi-vector scores
type AggregationMethod string

const (
	AggregateMax     AggregationMethod = "max"
	AggregateMin     AggregationMethod = "min"
	AggregateAverage AggregationMethod = "average"
	AggregateSum     AggregationMethod = "sum"
	AggregateWeighted AggregationMethod = "weighted"
)

// UpsertMultiVector inserts or updates a multi-vector entity
func (s *SQLiteStore) UpsertMultiVector(ctx context.Context, entity *MultiVectorEntity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if s.closed {
		return wrapError("upsert_multi_vector", ErrStoreClosed)
	}
	
	// Start transaction
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError("upsert_multi_vector", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil {
			// Log error but don't override the main error
			_ = err
		}
	}()
	
	// Insert each vector with a composite ID
	for fieldName, vector := range entity.Vectors {
		compositeID := fmt.Sprintf("%s___%s", entity.ID, fieldName)
		
		// Prepare metadata with field info
		metadata := make(map[string]interface{})
		if entity.Metadata != nil {
			for k, v := range entity.Metadata {
				metadata[k] = v
			}
		}
		metadata["_entity_id"] = entity.ID
		metadata["_vector_field"] = fieldName
		
		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			return wrapError("upsert_multi_vector", err)
		}
		
		vectorBytes, err := encoding.EncodeVector(vector)
		if err != nil {
			return wrapError("upsert_multi_vector", err)
		}
		
		_, err = tx.ExecContext(ctx, `
			INSERT OR REPLACE INTO embeddings (id, collection_id, vector, content, doc_id, metadata, created_at)
			VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		`, compositeID, 1, vectorBytes, entity.Content, entity.ID, string(metadataJSON))
		
		if err != nil {
			return wrapError("upsert_multi_vector", err)
		}
		
		// Update HNSW index if enabled
		if s.config.HNSW.Enabled && s.hnswIndex != nil {
			if err := s.hnswIndex.Insert(compositeID, vector); err != nil {
				// Log error but don't fail the entire operation
				_ = err
			}
		}
	}
	
	return tx.Commit()
}

// SearchMultiVector performs multi-vector search
func (s *SQLiteStore) SearchMultiVector(ctx context.Context, queryVectors map[string][]float32, opts MultiVectorSearchOptions) ([]ScoredEmbedding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return nil, wrapError("search_multi_vector", ErrStoreClosed)
	}
	
	// Aggregate results from each vector field
	entityScores := make(map[string][]float32)
	entityEmbeddings := make(map[string]ScoredEmbedding)
	
	for fieldName, queryVector := range queryVectors {
		// Only search specified fields
		if len(opts.VectorFields) > 0 {
			found := false
			for _, f := range opts.VectorFields {
				if f == fieldName {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		
		// Search with field-specific filter
		fieldOpts := opts.SearchOptions
		if fieldOpts.Filter == nil {
			fieldOpts.Filter = make(map[string]string)
		}
		fieldOpts.Filter["_vector_field"] = fieldName
		
		results, err := s.Search(ctx, queryVector, fieldOpts)
		if err != nil {
			continue
		}
		
		// Group by entity ID
		for _, result := range results {
			// Extract entity ID from metadata
			entityID, ok := result.Metadata["_entity_id"]
			if !ok {
				continue
			}
			
			if _, exists := entityScores[entityID]; !exists {
				entityScores[entityID] = []float32{}
				entityEmbeddings[entityID] = result
			}
			
			// Apply field weight if specified
			score := float32(result.Score)
			if weight, exists := opts.FieldWeights[fieldName]; exists {
				score *= weight
			}
			
			entityScores[entityID] = append(entityScores[entityID], score)
		}
	}
	
	// Aggregate scores
	finalResults := []ScoredEmbedding{}
	for entityID, scores := range entityScores {
		embedding := entityEmbeddings[entityID]
		embedding.Score = float64(s.aggregateScores(scores, opts.Aggregation))
		embedding.ID = entityID // Use entity ID instead of composite ID
		finalResults = append(finalResults, embedding)
	}
	
	// Sort by aggregated score
	for i := 0; i < len(finalResults)-1; i++ {
		for j := i + 1; j < len(finalResults); j++ {
			if finalResults[j].Score > finalResults[i].Score {
				finalResults[i], finalResults[j] = finalResults[j], finalResults[i]
			}
		}
	}
	
	// Apply TopK limit
	if opts.TopK > 0 && len(finalResults) > opts.TopK {
		finalResults = finalResults[:opts.TopK]
	}
	
	return finalResults, nil
}

// aggregateScores combines multiple scores based on method
func (s *SQLiteStore) aggregateScores(scores []float32, method AggregationMethod) float32 {
	if len(scores) == 0 {
		return 0
	}
	
	switch method {
	case AggregateMax:
		max := scores[0]
		for _, score := range scores[1:] {
			if score > max {
				max = score
			}
		}
		return max
		
	case AggregateMin:
		min := scores[0]
		for _, score := range scores[1:] {
			if score < min {
				min = score
			}
		}
		return min
		
	case AggregateSum:
		sum := float32(0)
		for _, score := range scores {
			sum += score
		}
		return sum
		
	case AggregateAverage, AggregateWeighted:
		sum := float32(0)
		for _, score := range scores {
			sum += score
		}
		return sum / float32(len(scores))
		
	default:
		// Default to average
		sum := float32(0)
		for _, score := range scores {
			sum += score
		}
		return sum / float32(len(scores))
	}
}

// GetMultiVectorEntity retrieves a multi-vector entity
func (s *SQLiteStore) GetMultiVectorEntity(ctx context.Context, entityID string) (*MultiVectorEntity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return nil, wrapError("get_multi_vector", ErrStoreClosed)
	}
	
	// Query all vectors for this entity
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, vector, content, metadata
		FROM embeddings
		WHERE json_extract(metadata, '$._entity_id') = ?
	`, entityID)
	if err != nil {
		return nil, wrapError("get_multi_vector", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			// Log error but don't override the main error
			_ = err
		}
	}()
	
	entity := &MultiVectorEntity{
		ID:       entityID,
		Vectors:  make(map[string][]float32),
		Metadata: make(map[string]interface{}),
	}
	
	for rows.Next() {
		var compositeID, content, metadataJSON string
		var vectorBytes []byte
		
		if err := rows.Scan(&compositeID, &vectorBytes, &content, &metadataJSON); err != nil {
			continue
		}
		
		// Decode vector
		vector, err := encoding.DecodeVector(vectorBytes)
		if err != nil {
			continue
		}
		
		// Parse metadata
		var metadata map[string]interface{}
		if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
			continue
		}
		
		// Extract vector field name
		if fieldName, ok := metadata["_vector_field"].(string); ok {
			entity.Vectors[fieldName] = vector
		}
		
		// Store content and clean metadata
		if entity.Content == "" {
			entity.Content = content
		}
		
		// Remove internal fields from metadata
		delete(metadata, "_entity_id")
		delete(metadata, "_vector_field")
		
		// Merge metadata
		for k, v := range metadata {
			entity.Metadata[k] = v
		}
	}
	
	if len(entity.Vectors) == 0 {
		return nil, wrapError("get_multi_vector", ErrNotFound)
	}
	
	return entity, nil
}

// DeleteMultiVectorEntity deletes all vectors for an entity
func (s *SQLiteStore) DeleteMultiVectorEntity(ctx context.Context, entityID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if s.closed {
		return wrapError("delete_multi_vector", ErrStoreClosed)
	}
	
	// Get all composite IDs first
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM embeddings
		WHERE json_extract(metadata, '$._entity_id') = ?
	`, entityID)
	if err != nil {
		return wrapError("delete_multi_vector", err)
	}
	
	var compositeIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			compositeIDs = append(compositeIDs, id)
		}
	}
	if err := rows.Close(); err != nil {
		// Log error but don't override the main error
		_ = err
	}
	
	// Delete from database
	_, err = s.db.ExecContext(ctx, `
		DELETE FROM embeddings
		WHERE json_extract(metadata, '$._entity_id') = ?
	`, entityID)
	if err != nil {
		return wrapError("delete_multi_vector", err)
	}
	
	// Update HNSW index
	if s.config.HNSW.Enabled && s.hnswIndex != nil {
		for _, id := range compositeIDs {
			if err := s.hnswIndex.Delete(id); err != nil {
				// Log error but don't fail the entire operation
				_ = err
			}
		}
	}
	
	return nil
}