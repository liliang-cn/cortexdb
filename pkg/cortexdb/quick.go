package cortexdb

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// Quick is a simplified interface for common operations.
type Quick struct {
	db *DB
}

// Quick creates a simple interface for quick operations.
func (db *DB) Quick() *Quick {
	return &Quick{db: db}
}

// Info returns information about the database configuration.
func (q *Quick) Info() DBInfo {
	return q.db.Info()
}

// Add adds a vector with automatic ID generation.
func (q *Quick) Add(ctx context.Context, vector []float32, content string) (string, error) {
	return q.AddToCollection(ctx, "", vector, content)
}

// AddToCollection adds a vector to a specific collection with automatic ID generation.
func (q *Quick) AddToCollection(ctx context.Context, collection string, vector []float32, content string) (string, error) {
	id := generateID()
	embedding := &core.Embedding{
		ID:         id,
		Collection: collection,
		Vector:     vector,
		Content:    content,
	}

	err := q.db.store.Upsert(ctx, embedding)
	return id, err
}

// Search performs similarity search.
func (q *Quick) Search(ctx context.Context, query []float32, topK int) ([]core.ScoredEmbedding, error) {
	return q.SearchInCollection(ctx, "", query, topK)
}

// SearchInCollection performs similarity search within a collection.
func (q *Quick) SearchInCollection(ctx context.Context, collection string, query []float32, topK int) ([]core.ScoredEmbedding, error) {
	opts := core.SearchOptions{
		Collection: collection,
		TopK:       topK,
		Threshold:  0.0,
	}
	return q.db.store.Search(ctx, query, opts)
}

// AddText adds text with automatic ID generation and embedding.
func (q *Quick) AddText(ctx context.Context, text string, metadata map[string]string) (string, error) {
	return q.AddTextToCollection(ctx, "", text, metadata)
}

// AddTextToCollection adds text to a specific collection with automatic ID generation.
func (q *Quick) AddTextToCollection(ctx context.Context, collection string, text string, metadata map[string]string) (string, error) {
	id := generateID()
	if q.db.embedder == nil {
		return "", ErrEmbedderNotConfigured
	}

	vec, err := q.db.embedder.Embed(ctx, text)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrEmbeddingFailed, err)
	}

	embedding := &core.Embedding{
		ID:         id,
		Collection: collection,
		Vector:     vec,
		Content:    text,
		Metadata:   metadata,
	}

	err = q.db.store.Upsert(ctx, embedding)
	return id, err
}

// SearchText performs similarity search using text query.
func (q *Quick) SearchText(ctx context.Context, query string, topK int) ([]core.ScoredEmbedding, error) {
	return q.SearchTextInCollection(ctx, "", query, topK)
}

// SearchTextInCollection performs similarity search using text query within a collection.
func (q *Quick) SearchTextInCollection(ctx context.Context, collection string, query string, topK int) ([]core.ScoredEmbedding, error) {
	return q.db.SearchTextInCollection(ctx, collection, query, topK)
}

// SearchTextOnly performs pure FTS5 full-text search without embeddings.
func (q *Quick) SearchTextOnly(ctx context.Context, query string, topK int) ([]core.ScoredEmbedding, error) {
	return q.db.SearchTextOnly(ctx, query, TextSearchOptions{TopK: topK})
}

// AddWithVector adds a vector with automatic ID generation using a pre-computed vector.
func (q *Quick) AddWithVector(ctx context.Context, vector []float32, content string, metadata map[string]string) (string, error) {
	return q.AddWithVectorToCollection(ctx, "", vector, content, metadata)
}

// AddWithVectorToCollection adds a vector to a specific collection with automatic ID generation.
func (q *Quick) AddWithVectorToCollection(ctx context.Context, collection string, vector []float32, content string, metadata map[string]string) (string, error) {
	id := generateID()
	embedding := &core.Embedding{
		ID:         id,
		Collection: collection,
		Vector:     vector,
		Content:    content,
		Metadata:   metadata,
	}

	err := q.db.store.UpsertBatchWithAdapt(ctx, []*core.Embedding{embedding})
	return id, err
}

// AddBatchWithVectors adds multiple vectors with automatic ID generation.
func (q *Quick) AddBatchWithVectors(ctx context.Context, vectors [][]float32, contents []string, metadata map[string]string) ([]string, error) {
	return q.AddBatchWithVectorsToCollection(ctx, "", vectors, contents, metadata)
}

// AddBatchWithVectorsToCollection adds multiple vectors to a specific collection with automatic ID generation.
func (q *Quick) AddBatchWithVectorsToCollection(ctx context.Context, collection string, vectors [][]float32, contents []string, metadata map[string]string) ([]string, error) {
	if len(vectors) == 0 || len(contents) == 0 {
		return nil, nil
	}
	if len(vectors) != len(contents) {
		return nil, fmt.Errorf("vector count (%d) must match content count (%d)", len(vectors), len(contents))
	}

	ids := make([]string, len(vectors))
	embeddings := make([]*core.Embedding, len(vectors))
	for i, vector := range vectors {
		id := generateID()
		ids[i] = id
		embeddings[i] = &core.Embedding{
			ID:         id,
			Collection: collection,
			Vector:     vector,
			Content:    contents[i],
			Metadata:   metadata,
		}
	}

	if err := q.db.store.UpsertBatchWithAdapt(ctx, embeddings); err != nil {
		return nil, err
	}
	return ids, nil
}

func generateID() string {
	return uuid.New().String()
}
