package cortexdb

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// TextSearchOptions defines options for text-only search.
type TextSearchOptions struct {
	Collection       string
	TopK             int
	Threshold        float64
	Keywords         []string
	AlternateQueries []string
}

// InsertText inserts text with automatic embedding generation.
func (db *DB) InsertText(ctx context.Context, id string, text string, metadata map[string]string) error {
	if db.embedder == nil {
		return ErrEmbedderNotConfigured
	}
	if text == "" {
		return ErrEmptyText
	}

	vec, err := db.embedder.Embed(ctx, text)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrEmbeddingFailed, err)
	}

	embedding := &core.Embedding{
		ID:       id,
		Vector:   vec,
		Content:  text,
		Metadata: metadata,
	}
	return db.store.Upsert(ctx, embedding)
}

// InsertTextBatch inserts multiple texts with automatic embedding generation.
func (db *DB) InsertTextBatch(ctx context.Context, texts map[string]string, metadata map[string]string) error {
	if db.embedder == nil {
		return ErrEmbedderNotConfigured
	}

	ids := make([]string, 0, len(texts))
	textList := make([]string, 0, len(texts))
	for id, text := range texts {
		if text == "" {
			continue
		}
		ids = append(ids, id)
		textList = append(textList, text)
	}
	if len(textList) == 0 {
		return nil
	}

	vectors, err := db.embedder.EmbedBatch(ctx, textList)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrEmbeddingFailed, err)
	}

	embeddings := make([]*core.Embedding, len(ids))
	for i, id := range ids {
		embeddings[i] = &core.Embedding{
			ID:       id,
			Vector:   vectors[i],
			Content:  textList[i],
			Metadata: metadata,
		}
	}
	return db.store.UpsertBatch(ctx, embeddings)
}

// InsertTextWithVector inserts text with a pre-computed vector.
func (db *DB) InsertTextWithVector(ctx context.Context, id string, text string, vector []float32, metadata map[string]string) error {
	if text == "" {
		return ErrEmptyText
	}
	if vector == nil {
		return ErrInvalidVector
	}

	embedding := &core.Embedding{
		ID:       id,
		Vector:   vector,
		Content:  text,
		Metadata: metadata,
	}
	return db.store.UpsertBatchWithAdapt(ctx, []*core.Embedding{embedding})
}

// InsertTextBatchWithVectors inserts multiple texts with pre-computed vectors.
func (db *DB) InsertTextBatchWithVectors(ctx context.Context, texts map[string]string, vectors [][]float32, metadata map[string]string) error {
	if len(texts) == 0 {
		return nil
	}
	if len(vectors) == 0 {
		return fmt.Errorf("vectors cannot be empty")
	}

	ids := make([]string, 0, len(texts))
	textList := make([]string, 0, len(texts))
	for id, text := range texts {
		if text == "" {
			continue
		}
		ids = append(ids, id)
		textList = append(textList, text)
	}
	if len(textList) == 0 {
		return nil
	}
	if len(ids) != len(vectors) {
		return fmt.Errorf("text count (%d) must match vector count (%d)", len(ids), len(vectors))
	}

	embeddings := make([]*core.Embedding, len(ids))
	for i, id := range ids {
		embeddings[i] = &core.Embedding{
			ID:       id,
			Vector:   vectors[i],
			Content:  textList[i],
			Metadata: metadata,
		}
	}
	return db.store.UpsertBatchWithAdapt(ctx, embeddings)
}

// SearchText performs similarity search using text query.
func (db *DB) SearchText(ctx context.Context, query string, topK int) ([]core.ScoredEmbedding, error) {
	return db.SearchTextInCollection(ctx, "", query, topK)
}

// SearchTextInCollection performs similarity search using text query within a collection.
func (db *DB) SearchTextInCollection(ctx context.Context, collection string, query string, topK int) ([]core.ScoredEmbedding, error) {
	if query == "" {
		return nil, ErrEmptyText
	}
	if db.embedder == nil {
		return db.SearchTextOnly(ctx, query, TextSearchOptions{
			Collection: collection,
			TopK:       topK,
		})
	}

	vec, err := db.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEmbeddingFailed, err)
	}

	opts := core.SearchOptions{
		Collection: collection,
		TopK:       topK,
		QueryText:  query,
	}
	return db.store.Search(ctx, vec, opts)
}

// HybridSearchText performs hybrid search combining vector and keyword matching.
func (db *DB) HybridSearchText(ctx context.Context, query string, topK int) ([]core.ScoredEmbedding, error) {
	if query == "" {
		return nil, ErrEmptyText
	}
	if db.embedder == nil {
		return db.store.HybridSearch(ctx, nil, query, core.HybridSearchOptions{
			SearchOptions: core.SearchOptions{TopK: topK},
		})
	}

	vec, err := db.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEmbeddingFailed, err)
	}

	return db.store.HybridSearch(ctx, vec, query, core.HybridSearchOptions{
		SearchOptions: core.SearchOptions{TopK: topK},
	})
}

// SearchTextOnly performs pure FTS5 full-text search without embeddings.
func (db *DB) SearchTextOnly(ctx context.Context, query string, opts TextSearchOptions) ([]core.ScoredEmbedding, error) {
	if query == "" {
		return nil, ErrEmptyText
	}
	if opts.TopK <= 0 {
		opts.TopK = 10
	}

	if len(opts.Keywords) > 0 || len(opts.AlternateQueries) > 0 {
		return db.searchTextOnlyExpanded(ctx, query, opts)
	}

	results, err := db.store.HybridSearch(ctx, nil, query, core.HybridSearchOptions{
		SearchOptions: core.SearchOptions{
			Collection: opts.Collection,
			TopK:       opts.TopK,
			Threshold:  opts.Threshold,
		},
	})
	if err == nil {
		return results, nil
	}
	return db.ftsSearch(ctx, query, opts)
}

func (db *DB) searchTextOnlyExpanded(ctx context.Context, query string, opts TextSearchOptions) ([]core.ScoredEmbedding, error) {
	queries := lexicalSearchQueries(query, opts.Keywords, opts.AlternateQueries)
	if len(queries) == 0 {
		return nil, ErrEmptyText
	}

	merged := make(map[string]core.ScoredEmbedding)
	var firstErr error
	for idx, searchQuery := range queries {
		results, err := db.ftsSearch(ctx, searchQuery, TextSearchOptions{
			Collection: opts.Collection,
			TopK:       opts.TopK * 4,
			Threshold:  opts.Threshold,
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if len(results) == 0 {
			continue
		}

		scoreWeight := 1.0 - float64(idx)*0.05
		if scoreWeight < 0.8 {
			scoreWeight = 0.8
		}
		for _, result := range results {
			result.Score *= scoreWeight
			if existing, ok := merged[result.ID]; !ok || result.Score > existing.Score {
				merged[result.ID] = result
			}
		}
		if idx == 0 && len(merged) >= opts.TopK {
			break
		}
	}

	if len(merged) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, nil
	}

	ordered := make([]core.ScoredEmbedding, 0, len(merged))
	for _, result := range merged {
		ordered = append(ordered, result)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Score == ordered[j].Score {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].Score > ordered[j].Score
	})
	if opts.TopK > 0 && len(ordered) > opts.TopK {
		ordered = ordered[:opts.TopK]
	}
	return ordered, nil
}

func (db *DB) ftsSearch(ctx context.Context, query string, opts TextSearchOptions) ([]core.ScoredEmbedding, error) {
	rows, err := db.store.GetDB().QueryContext(ctx, `
		SELECT e.id, e.collection_id, c.name, e.vector, e.content, e.doc_id, e.metadata, bm25(chunks_fts) as score
		FROM chunks_fts
		JOIN embeddings e ON chunks_fts.rowid = e.rowid
		LEFT JOIN collections c ON e.collection_id = c.id
		WHERE chunks_fts MATCH ?
	`+func() string {
		if opts.Collection == "" {
			return " ORDER BY score LIMIT ?"
		}
		return " AND c.name = ? ORDER BY score LIMIT ?"
	}(), func() []any {
		args := []any{query}
		if opts.Collection != "" {
			args = append(args, opts.Collection)
		}
		args = append(args, opts.TopK)
		return args
	}()...)
	if err != nil {
		return nil, fmt.Errorf("FTS search failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []core.ScoredEmbedding
	for rows.Next() {
		var id, content string
		var collectionID int
		var collectionName, docID sql.NullString
		var vectorBytes []byte
		var metadataJSON string
		var score float64

		if err := rows.Scan(&id, &collectionID, &collectionName, &vectorBytes, &content, &docID, &metadataJSON, &score); err != nil {
			continue
		}
		normalizedScore := 1.0 / (1.0 + (-score))
		if opts.Threshold > 0 && normalizedScore < opts.Threshold {
			continue
		}
		results = append(results, core.ScoredEmbedding{
			Embedding: core.Embedding{
				ID:         id,
				Collection: collectionName.String,
				Content:    content,
				DocID:      docID.String,
			},
			Score: normalizedScore,
		})
	}

	return results, rows.Err()
}
