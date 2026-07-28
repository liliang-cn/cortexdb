package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/liliang-cn/cortexdb/v2/internal/encoding"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// defaultSearchTopK is the page size used when a caller names none. top_k is optional throughout the
// tool surface, so every path that truncates by it has to settle on this first — a zero carried into a
// truncation returns an empty result set rather than the default page.
const defaultSearchTopK = 10

// TextSearchOptions defines options for text-only search.
type TextSearchOptions struct {
	Collection       string
	TopK             int
	Threshold        float64
	Keywords         []string
	AlternateQueries []string

	// Authorize, when set, is a retrieval-layer security gate: it is applied to
	// every candidate and only authorized rows count toward TopK. The search
	// over-fetches internally so the caller still receives up to TopK authorized
	// results. This pushes access control (RBAC/ABAC) into the retrieval boundary
	// instead of leaving it to post-filtering in application code.
	Authorize func(core.ScoredEmbedding) bool

	// Reranker, when set, re-scores and reorders the authorized candidate set
	// (typically a cross-encoder or LLM) before MinScore filtering and TopK
	// truncation — the standard recall→precision second stage.
	Reranker core.Reranker

	// MinScore, when > 0, drops candidates whose (possibly reranked) Score is
	// below the threshold — a relevance floor that prevents weak tail matches
	// from being surfaced.
	MinScore float64
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

// HybridSearchTextWithOptions is the full retrieval pipeline for production RAG:
// hybrid recall (vector + BM25 when an embedder is set, BM25-only otherwise),
// then the optional retrieval-layer stages in order:
//
//	recall(over-fetch) → Authorize(RBAC/ABAC) → Reranker → MinScore → TopK
//
// Keeping these stages inside the retrieval call makes access control and the
// relevance floor non-bypassable by callers.
func (db *DB) HybridSearchTextWithOptions(ctx context.Context, query string, opts TextSearchOptions) ([]core.ScoredEmbedding, error) {
	if query == "" {
		return nil, ErrEmptyText
	}
	wantK := opts.TopK
	if wantK <= 0 {
		wantK = 10
	}

	// Over-fetch so enough survive Authorize/Reranker to still fill TopK.
	fetchK := wantK
	if opts.Authorize != nil || opts.Reranker != nil {
		fetchK = overfetchK(wantK)
	}
	candidates, err := db.HybridSearchText(ctx, query, fetchK)
	if err != nil {
		return nil, err
	}

	// 1) Authorization gate.
	if opts.Authorize != nil {
		kept := candidates[:0]
		for _, c := range candidates {
			if opts.Authorize(c) {
				kept = append(kept, c)
			}
		}
		candidates = kept
	}

	// 2) Rerank (cross-encoder / LLM second stage).
	if opts.Reranker != nil && len(candidates) > 0 {
		reranked, rerr := opts.Reranker.Rerank(ctx, query, candidates)
		if rerr != nil {
			return nil, fmt.Errorf("rerank: %w", rerr)
		}
		candidates = reranked
	}

	// 3) Relevance floor.
	if opts.MinScore > 0 {
		kept := candidates[:0]
		for _, c := range candidates {
			if c.Score >= opts.MinScore {
				kept = append(kept, c)
			}
		}
		candidates = kept
	}

	// 4) Final TopK.
	if len(candidates) > wantK {
		candidates = candidates[:wantK]
	}
	return candidates, nil
}

// SearchTextOnly performs pure FTS5 full-text search without embeddings.
func (db *DB) SearchTextOnly(ctx context.Context, query string, opts TextSearchOptions) ([]core.ScoredEmbedding, error) {
	if query == "" {
		return nil, ErrEmptyText
	}
	if opts.TopK <= 0 {
		opts.TopK = defaultSearchTopK
	}

	// Retrieval-layer authorization: over-fetch, apply the gate, then return up
	// to TopK authorized rows. Keeping this here (rather than in the caller)
	// makes the security predicate a non-bypassable part of retrieval.
	if opts.Authorize != nil {
		authorize := opts.Authorize
		wantK := opts.TopK
		inner := opts
		inner.Authorize = nil
		inner.TopK = overfetchK(wantK)
		candidates, err := db.SearchTextOnly(ctx, query, inner)
		if err != nil {
			return nil, err
		}
		out := make([]core.ScoredEmbedding, 0, wantK)
		for _, c := range candidates {
			if authorize(c) {
				out = append(out, c)
				if len(out) >= wantK {
					break
				}
			}
		}
		return out, nil
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

// overfetchK returns how many candidates to pull when an Authorize gate is set,
// so enough rows survive filtering to still fill TopK.
func overfetchK(wantK int) int {
	k := wantK * 5
	if k < 50 {
		k = 50
	}
	return k
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

// ftsSearchQuery builds the lexical lookup for one query, returning the same columns either way.
//
// Normally that is a MATCH against the FTS index. A CJK query of one or two characters produces no
// trigram, so MATCH would return nothing however much of the corpus contains the term — those fall
// back to a substring scan, ordered by rowid since there is no BM25 score to rank by.
func ftsSearchQuery(query string, opts TextSearchOptions) (string, []any) {
	columns := `e.id, e.collection_id, c.name, e.vector, e.content, e.doc_id, e.metadata, e.acl`
	collectionClause := ""
	var collectionArgs []any
	if opts.Collection != "" {
		collectionClause = " AND c.name = ?"
		collectionArgs = append(collectionArgs, opts.Collection)
	}

	if core.BelowTrigramFloor(query) {
		args := append([]any{core.SubstringPattern(query)}, collectionArgs...)
		return `
			SELECT ` + columns + `, 0 as score
			FROM embeddings e
			LEFT JOIN collections c ON e.collection_id = c.id
			WHERE e.content LIKE ? ESCAPE '\'` + collectionClause + `
			ORDER BY e.rowid LIMIT ?
		`, append(args, opts.TopK)
	}

	// CJK text needs the trigram companion index; see core.CJKAwareIndex.
	index := core.CJKAwareIndex("chunks_fts", query)
	args := append([]any{query}, collectionArgs...)
	return `
		SELECT ` + columns + `, bm25(` + index + `) as score
		FROM ` + index + `
		JOIN embeddings e ON ` + index + `.rowid = e.rowid
		LEFT JOIN collections c ON e.collection_id = c.id
		WHERE ` + index + ` MATCH ?` + collectionClause + `
		ORDER BY score LIMIT ?
	`, append(args, opts.TopK)
}

func (db *DB) ftsSearch(ctx context.Context, query string, opts TextSearchOptions) ([]core.ScoredEmbedding, error) {
	querySQL, args := ftsSearchQuery(query, opts)
	rows, err := db.store.GetDB().QueryContext(ctx, querySQL, args...)
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
		var aclJSON sql.NullString
		var score float64

		if err := rows.Scan(&id, &collectionID, &collectionName, &vectorBytes, &content, &docID, &metadataJSON, &aclJSON, &score); err != nil {
			continue
		}
		normalizedScore := 1.0 / (1.0 + (-score))
		if opts.Threshold > 0 && normalizedScore < opts.Threshold {
			continue
		}
		metadata, _ := encoding.DecodeMetadata(metadataJSON)
		var acl []string
		if aclJSON.Valid && aclJSON.String != "" {
			_ = json.Unmarshal([]byte(aclJSON.String), &acl)
		}
		results = append(results, core.ScoredEmbedding{
			Embedding: core.Embedding{
				ID:         id,
				Collection: collectionName.String,
				Content:    content,
				DocID:      docID.String,
				Metadata:   metadata,
				ACL:        acl,
			},
			Score: normalizedScore,
		})
	}

	return results, rows.Err()
}
