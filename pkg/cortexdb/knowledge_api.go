package cortexdb

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

type knowledgeIngestResult struct {
	documentNodeID  string
	entityNodeIDs   []string
	relationEdgeIDs []string
	collection      string
}

// SaveKnowledge stores or replaces a knowledge item and its retrieval artifacts.
func (db *DB) SaveKnowledge(ctx context.Context, req KnowledgeSaveRequest) (*KnowledgeSaveResponse, error) {
	if req.KnowledgeID == "" {
		return nil, fmt.Errorf("knowledge_id is required")
	}
	if strings.TrimSpace(req.Content) == "" {
		return nil, ErrEmptyText
	}

	existing, err := db.store.GetDocument(ctx, req.KnowledgeID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("get existing knowledge: %w", err)
	}
	if existing != nil {
		if err := db.cleanupKnowledgeArtifacts(ctx, req.KnowledgeID); err != nil {
			return nil, err
		}
	}

	metadata := cloneStringMap(req.Metadata)
	ingest, err := db.ingestKnowledgeContent(ctx, req.KnowledgeID, req.Title, req.Content, req.Collection, req.ChunkSize, req.ChunkOverlap, metadata, req.Entities, req.Relations)
	if err != nil {
		return nil, err
	}

	version := 1
	if existing != nil {
		version = existing.Version + 1
	}
	if err := db.upsertKnowledgeDocumentRecord(ctx, &core.Document{
		ID:        req.KnowledgeID,
		Title:     req.Title,
		Content:   req.Content,
		SourceURL: req.SourceURL,
		Version:   version,
		Author:    req.Author,
		Metadata:  stringMapToAnyMap(metadata),
	}); err != nil {
		return nil, err
	}

	record, err := db.loadKnowledgeRecord(ctx, req.KnowledgeID)
	if err != nil {
		return nil, err
	}

	return &KnowledgeSaveResponse{
		Knowledge:       *record,
		DocumentNodeID:  ingest.documentNodeID,
		EntityNodeIDs:   uniqueSortedStrings(ingest.entityNodeIDs),
		RelationEdgeIDs: uniqueSortedStrings(ingest.relationEdgeIDs),
	}, nil
}

// UpdateKnowledge updates a knowledge item and refreshes retrieval artifacts when necessary.
func (db *DB) UpdateKnowledge(ctx context.Context, req KnowledgeUpdateRequest) (*KnowledgeSaveResponse, error) {
	if req.KnowledgeID == "" {
		return nil, fmt.Errorf("knowledge_id is required")
	}

	existing, err := db.store.GetDocument(ctx, req.KnowledgeID)
	if err != nil {
		return nil, fmt.Errorf("get knowledge: %w", err)
	}

	title := existing.Title
	if req.Title != nil {
		title = *req.Title
	}
	content := existing.Content
	if req.Content != nil {
		if strings.TrimSpace(*req.Content) == "" {
			return nil, ErrEmptyText
		}
		content = *req.Content
	}
	sourceURL := existing.SourceURL
	if req.SourceURL != nil {
		sourceURL = *req.SourceURL
	}
	author := existing.Author
	if req.Author != nil {
		author = *req.Author
	}
	metadata := anyMapToStringMap(existing.Metadata)
	if req.Metadata != nil {
		metadata = cloneStringMap(req.Metadata)
	}

	collection, err := db.knowledgeCollection(ctx, req.KnowledgeID)
	if err != nil {
		return nil, err
	}
	if req.Collection != nil {
		collection = *req.Collection
	}

	chunkSize := 0
	if req.ChunkSize != nil {
		chunkSize = *req.ChunkSize
	}
	chunkOverlap := 0
	if req.ChunkOverlap != nil {
		chunkOverlap = *req.ChunkOverlap
	}

	replaceArtifacts := req.Content != nil || req.Title != nil || req.Collection != nil || req.Metadata != nil
	ingest := &knowledgeIngestResult{}
	if replaceArtifacts {
		if err := db.cleanupKnowledgeArtifacts(ctx, req.KnowledgeID); err != nil {
			return nil, err
		}
		ingest, err = db.ingestKnowledgeContent(ctx, req.KnowledgeID, title, content, collection, chunkSize, chunkOverlap, metadata, req.Entities, req.Relations)
		if err != nil {
			return nil, err
		}
	} else if len(req.Entities) > 0 || len(req.Relations) > 0 {
		toolbox := db.GraphRAGTools()
		if len(req.Entities) > 0 {
			entityResp, err := toolbox.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
				DocumentID: req.KnowledgeID,
				Entities:   req.Entities,
			})
			if err != nil {
				return nil, err
			}
			if entityResp != nil {
				ingest.entityNodeIDs = append(ingest.entityNodeIDs, entityResp.EntityNodeIDs...)
			}
		}
		if len(req.Relations) > 0 {
			relResp, err := toolbox.UpsertRelations(ctx, ToolUpsertRelationsRequest{
				DocumentID: req.KnowledgeID,
				Relations:  req.Relations,
			})
			if err != nil {
				return nil, err
			}
			if relResp != nil {
				ingest.relationEdgeIDs = append(ingest.relationEdgeIDs, relResp.EdgeIDs...)
			}
		}
	}

	if err := db.upsertKnowledgeDocumentRecord(ctx, &core.Document{
		ID:        req.KnowledgeID,
		Title:     title,
		Content:   content,
		SourceURL: sourceURL,
		Version:   existing.Version + 1,
		Author:    author,
		Metadata:  stringMapToAnyMap(metadata),
	}); err != nil {
		return nil, err
	}

	record, err := db.loadKnowledgeRecord(ctx, req.KnowledgeID)
	if err != nil {
		return nil, err
	}

	return &KnowledgeSaveResponse{
		Knowledge:       *record,
		DocumentNodeID:  ingest.documentNodeID,
		EntityNodeIDs:   uniqueSortedStrings(ingest.entityNodeIDs),
		RelationEdgeIDs: uniqueSortedStrings(ingest.relationEdgeIDs),
	}, nil
}

// GetKnowledge fetches a durable knowledge item by ID.
func (db *DB) GetKnowledge(ctx context.Context, req KnowledgeGetRequest) (*KnowledgeGetResponse, error) {
	record, err := db.loadKnowledgeRecord(ctx, req.KnowledgeID)
	if err != nil {
		return nil, err
	}
	return &KnowledgeGetResponse{Knowledge: *record}, nil
}

// SearchKnowledge searches durable knowledge and groups chunk results by knowledge document.
func (db *DB) SearchKnowledge(ctx context.Context, req KnowledgeSearchRequest) (*KnowledgeSearchResponse, error) {
	if strings.TrimSpace(req.Query) == "" {
		return nil, ErrEmptyText
	}

	opts := GraphRAGQueryOptions{
		Collection:       req.Collection,
		TopK:             req.TopK,
		MaxHops:          req.MaxHops,
		MaxRelatedChunks: req.MaxRelatedChunks,
		MaxContextChunks: req.MaxContextChunks,
		MaxContextChars:  req.MaxContextChars,
		PerDocumentLimit: req.PerDocumentLimit,
		DiversityLambda:  req.DiversityLambda,
		Rerank:           true,
		RetrievalMode:    req.RetrievalMode,
		DisableGraph:     req.DisableGraph,
	}
	applyGraphRAGQueryDefaults(&opts)

	var result *GraphRAGQueryResult
	var err error
	if db.HasEmbedder() && normalizeRetrievalMode(req.RetrievalMode) != RetrievalModeLexical {
		result, err = db.SearchGraphRAG(ctx, req.Query, opts)
	} else {
		result, err = db.GraphRAGTools().SearchGraphRAGLexical(ctx, ToolSearchGraphRAGLexicalRequest{
			Query:            req.Query,
			Collection:       req.Collection,
			TopK:             req.TopK,
			MaxHops:          req.MaxHops,
			MaxRelatedChunks: req.MaxRelatedChunks,
			MaxContextChunks: req.MaxContextChunks,
			MaxContextChars:  req.MaxContextChars,
			PerDocumentLimit: req.PerDocumentLimit,
			DiversityLambda:  req.DiversityLambda,
			EntityNames:      req.EntityNames,
			Keywords:         req.Keywords,
			AlternateQueries: req.AlternateQueries,
			RetrievalMode:    req.RetrievalMode,
			DisableGraph:     req.DisableGraph,
		})
	}
	if err != nil {
		return nil, err
	}

	hits, err := db.aggregateKnowledgeHits(ctx, result.Chunks)
	if err != nil {
		return nil, err
	}

	return &KnowledgeSearchResponse{
		Query:    req.Query,
		Results:  hits,
		Chunks:   result.Chunks,
		Entities: result.Entities,
		Context:  result.Context,
	}, nil
}

// DeleteKnowledge removes a knowledge item and its retrieval artifacts.
func (db *DB) DeleteKnowledge(ctx context.Context, req KnowledgeDeleteRequest) (*KnowledgeDeleteResponse, error) {
	if req.KnowledgeID == "" {
		return nil, fmt.Errorf("knowledge_id is required")
	}
	if _, err := db.store.GetDocument(ctx, req.KnowledgeID); err != nil {
		return nil, err
	}
	if err := db.cleanupKnowledgeArtifacts(ctx, req.KnowledgeID); err != nil {
		return nil, err
	}
	return &KnowledgeDeleteResponse{KnowledgeID: req.KnowledgeID, Deleted: true}, nil
}

// SaveKnowledge stores or replaces a knowledge item through the tool surface.
func (t *GraphRAGToolbox) SaveKnowledge(ctx context.Context, req KnowledgeSaveRequest) (*KnowledgeSaveResponse, error) {
	return t.db.SaveKnowledge(ctx, req)
}

// UpdateKnowledge updates a knowledge item through the tool surface.
func (t *GraphRAGToolbox) UpdateKnowledge(ctx context.Context, req KnowledgeUpdateRequest) (*KnowledgeSaveResponse, error) {
	return t.db.UpdateKnowledge(ctx, req)
}

// GetKnowledge fetches a knowledge item through the tool surface.
func (t *GraphRAGToolbox) GetKnowledge(ctx context.Context, req KnowledgeGetRequest) (*KnowledgeGetResponse, error) {
	return t.db.GetKnowledge(ctx, req)
}

// SearchKnowledge searches durable knowledge through the tool surface.
func (t *GraphRAGToolbox) SearchKnowledge(ctx context.Context, req KnowledgeSearchRequest) (*KnowledgeSearchResponse, error) {
	return t.db.SearchKnowledge(ctx, req)
}

// DeleteKnowledge deletes a knowledge item through the tool surface.
func (t *GraphRAGToolbox) DeleteKnowledge(ctx context.Context, req KnowledgeDeleteRequest) (*KnowledgeDeleteResponse, error) {
	return t.db.DeleteKnowledge(ctx, req)
}

func (db *DB) ingestKnowledgeContent(ctx context.Context, knowledgeID, title, content, collection string, chunkSize, chunkOverlap int, metadata map[string]string, entities []ToolEntityInput, relations []ToolRelationInput) (*knowledgeIngestResult, error) {
	doc := GraphRAGDocument{
		ID:       knowledgeID,
		Title:    title,
		Content:  content,
		Metadata: cloneStringMap(metadata),
	}
	toolbox := db.GraphRAGTools()
	result := &knowledgeIngestResult{}

	if db.HasEmbedder() {
		ingest, err := db.InsertGraphDocument(ctx, doc, GraphRAGIngestOptions{
			Collection:   collection,
			ChunkSize:    chunkSize,
			ChunkOverlap: chunkOverlap,
		})
		if err != nil {
			return nil, err
		}
		if ingest != nil {
			result.documentNodeID = ingest.DocumentNodeID
			result.entityNodeIDs = append(result.entityNodeIDs, ingest.EntityNodeIDs...)
		}
	} else {
		ingest, err := toolbox.IngestDocument(ctx, ToolIngestDocumentRequest{
			DocumentID:   knowledgeID,
			Title:        title,
			Content:      content,
			Collection:   collection,
			ChunkSize:    chunkSize,
			ChunkOverlap: chunkOverlap,
			Metadata:     cloneStringMap(metadata),
		})
		if err != nil {
			return nil, err
		}
		if ingest != nil {
			result.documentNodeID = ingest.DocumentNodeID
			result.collection = ingest.Collection
		}
	}

	if result.collection == "" {
		ingestOpts := GraphRAGIngestOptions{
			Collection:   collection,
			ChunkSize:    chunkSize,
			ChunkOverlap: chunkOverlap,
		}
		applyGraphRAGIngestDefaults(&ingestOpts)
		result.collection = ingestOpts.Collection
	}

	if len(entities) > 0 {
		entityResp, err := toolbox.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
			DocumentID: knowledgeID,
			Entities:   entities,
		})
		if err != nil {
			return nil, err
		}
		if entityResp != nil {
			result.entityNodeIDs = append(result.entityNodeIDs, entityResp.EntityNodeIDs...)
		}
	}
	if len(relations) > 0 {
		relResp, err := toolbox.UpsertRelations(ctx, ToolUpsertRelationsRequest{
			DocumentID: knowledgeID,
			Relations:  relations,
		})
		if err != nil {
			return nil, err
		}
		if relResp != nil {
			result.relationEdgeIDs = append(result.relationEdgeIDs, relResp.EdgeIDs...)
		}
	}

	return result, nil
}

func (db *DB) cleanupKnowledgeArtifacts(ctx context.Context, knowledgeID string) error {
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return fmt.Errorf("init graph schema: %w", err)
	}

	chunks, err := db.store.GetByDocID(ctx, knowledgeID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return fmt.Errorf("get knowledge chunks: %w", err)
	}

	nodeIDs := make([]string, 0, len(chunks)+1)
	nodeIDs = append(nodeIDs, graphDocumentNodeID(knowledgeID))
	for _, chunk := range chunks {
		nodeIDs = append(nodeIDs, chunk.ID)
	}
	if _, err := db.graph.DeleteNodesBatch(ctx, nodeIDs); err != nil {
		return fmt.Errorf("delete graph nodes: %w", err)
	}
	if err := db.store.DeleteDocument(ctx, knowledgeID); err != nil {
		return fmt.Errorf("delete knowledge document: %w", err)
	}
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
	toolbox := db.GraphRAGTools()
	for _, chunk := range chunks {
		chunkIDs = append(chunkIDs, chunk.ID)
		entities, err := toolbox.getChunkEntityNames(ctx, chunk.ID)
		if err != nil {
			return nil, err
		}
		for _, entity := range entities {
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
