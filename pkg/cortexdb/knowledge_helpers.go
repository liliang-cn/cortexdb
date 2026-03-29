package cortexdb

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

func (db *DB) cleanupKnowledgeArtifacts(ctx context.Context, knowledgeID string) error {
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return fmt.Errorf("init graph schema: %w", err)
	}

	tx, err := db.store.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin knowledge cleanup transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	chunks, err := db.knowledgeChunkRefsTx(ctx, tx, knowledgeID)
	if err != nil {
		return err
	}
	chunkIDs := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		chunkIDs = append(chunkIDs, chunk.ID)
	}

	deletedNodeIDs, err := db.cleanupKnowledgeGraphArtifactsTx(ctx, tx, knowledgeID, chunks)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM embeddings WHERE doc_id = ?`, knowledgeID); err != nil {
		return fmt.Errorf("delete knowledge chunks: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM documents WHERE id = ?`, knowledgeID); err != nil {
		return fmt.Errorf("delete knowledge document: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit knowledge cleanup transaction: %w", err)
	}

	db.store.SyncDeletedEmbeddingIDs(ctx, chunkIDs)
	db.graph.SyncDeletedNodeIDs(ctx, deletedNodeIDs)
	return nil
}

func (db *DB) upsertKnowledgeDocumentRecord(ctx context.Context, doc *core.Document) error {
	existing, err := db.store.GetDocument(ctx, doc.ID)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			if err := db.store.CreateDocument(ctx, doc); err != nil {
				return fmt.Errorf("create knowledge document: %w", err)
			}
			return nil
		}
		return fmt.Errorf("get knowledge document: %w", err)
	}

	existing.Title = doc.Title
	existing.Content = doc.Content
	existing.SourceURL = doc.SourceURL
	existing.Version = doc.Version
	existing.Author = doc.Author
	existing.Metadata = doc.Metadata
	if err := db.store.UpdateDocument(ctx, existing); err != nil {
		return fmt.Errorf("update knowledge document: %w", err)
	}
	return nil
}

func (db *DB) loadKnowledgeRecord(ctx context.Context, knowledgeID string) (*KnowledgeRecord, error) {
	if knowledgeID == "" {
		return nil, fmt.Errorf("knowledge_id is required")
	}

	doc, err := db.store.GetDocument(ctx, knowledgeID)
	if err != nil {
		return nil, err
	}
	chunks, err := db.store.GetByDocID(ctx, knowledgeID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}

	chunkIDs := make([]string, 0, len(chunks))
	entitySet := make(map[string]struct{})
	entityNamesByChunk := make(map[string][]string)
	if len(chunks) > 0 {
		ids := make([]string, 0, len(chunks))
		for _, chunk := range chunks {
			ids = append(ids, chunk.ID)
		}
		entityNamesByChunk, err = db.chunkEntityNamesBatch(ctx, ids, 32)
		if err != nil {
			return nil, err
		}
	}
	for _, chunk := range chunks {
		chunkIDs = append(chunkIDs, chunk.ID)
		for _, entity := range entityNamesByChunk[chunk.ID] {
			entitySet[entity] = struct{}{}
		}
	}

	collection, err := db.knowledgeCollection(ctx, knowledgeID)
	if err != nil {
		return nil, err
	}

	return &KnowledgeRecord{
		ID:         doc.ID,
		Title:      doc.Title,
		Content:    doc.Content,
		SourceURL:  doc.SourceURL,
		Author:     doc.Author,
		Collection: collection,
		Metadata:   anyMapToStringMap(doc.Metadata),
		ChunkIDs:   chunkIDs,
		Entities:   uniqueSortedStrings(sortedKeysFromSet(entitySet)),
		CreatedAt:  doc.CreatedAt,
		UpdatedAt:  doc.UpdatedAt,
	}, nil
}

func (db *DB) knowledgeCollection(ctx context.Context, knowledgeID string) (string, error) {
	chunks, err := db.store.GetByDocID(ctx, knowledgeID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return "", err
	}
	if len(chunks) == 0 {
		return defaultGraphRAGCollection, nil
	}
	firstChunk, err := db.store.GetByID(ctx, chunks[0].ID)
	if err != nil {
		return "", err
	}
	return firstChunk.Collection, nil
}

func (db *DB) aggregateKnowledgeHits(ctx context.Context, chunks []GraphRAGChunkResult) ([]KnowledgeSearchHit, error) {
	type aggregate struct {
		hit       KnowledgeSearchHit
		entitySet map[string]struct{}
		chunkSet  map[string]struct{}
	}

	docCache := make(map[string]*core.Document)
	grouped := make(map[string]*aggregate)
	order := make([]string, 0)

	for _, chunk := range chunks {
		docID := chunk.DocumentID
		if docID == "" {
			docID = chunk.ID
		}
		agg, ok := grouped[docID]
		if !ok {
			agg = &aggregate{
				hit: KnowledgeSearchHit{
					KnowledgeID: docID,
					Score:       chunk.Score,
					Snippet:     compactSnippet(chunk.Content),
				},
				entitySet: make(map[string]struct{}),
				chunkSet:  make(map[string]struct{}),
			}
			grouped[docID] = agg
			order = append(order, docID)
		}
		if chunk.Score > agg.hit.Score {
			agg.hit.Score = chunk.Score
			if snippet := compactSnippet(chunk.Content); snippet != "" {
				agg.hit.Snippet = snippet
			}
		}
		if _, exists := agg.chunkSet[chunk.ID]; !exists {
			agg.chunkSet[chunk.ID] = struct{}{}
			agg.hit.ChunkIDs = append(agg.hit.ChunkIDs, chunk.ID)
		}
		for _, entity := range chunk.Entities {
			agg.entitySet[entity] = struct{}{}
		}

		if chunk.DocumentID == "" {
			continue
		}
		if _, ok := docCache[chunk.DocumentID]; !ok {
			doc, err := db.store.GetDocument(ctx, chunk.DocumentID)
			if err != nil {
				return nil, err
			}
			docCache[chunk.DocumentID] = doc
		}
		doc := docCache[chunk.DocumentID]
		agg.hit.Title = doc.Title
		agg.hit.SourceURL = doc.SourceURL
		agg.hit.Author = doc.Author
		agg.hit.Metadata = anyMapToStringMap(doc.Metadata)
	}

	results := make([]KnowledgeSearchHit, 0, len(grouped))
	for _, docID := range order {
		agg := grouped[docID]
		agg.hit.Entities = uniqueSortedStrings(sortedKeysFromSet(agg.entitySet))
		results = append(results, agg.hit)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].KnowledgeID < results[j].KnowledgeID
		}
		return results[i].Score > results[j].Score
	})
	return results, nil
}
