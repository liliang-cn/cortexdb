package cortexdb

import (
	"context"
	"errors"
	"fmt"
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

	metadata := cloneStringMap(req.Metadata)
	version := 1
	if existing != nil {
		version = existing.Version + 1
	}

	plan, err := db.buildKnowledgeMutationPlan(ctx, knowledgeMutationInput{
		KnowledgeID:  req.KnowledgeID,
		Title:        req.Title,
		Content:      req.Content,
		SourceURL:    req.SourceURL,
		Author:       req.Author,
		Collection:   req.Collection,
		ChunkSize:    req.ChunkSize,
		ChunkOverlap: req.ChunkOverlap,
		Version:      version,
		Metadata:     metadata,
		Entities:     req.Entities,
		Relations:    req.Relations,
	})
	if err != nil {
		return nil, err
	}
	if err := db.applyKnowledgeMutation(ctx, req.KnowledgeID, existing != nil, plan); err != nil {
		return nil, err
	}

	record, err := db.loadKnowledgeRecord(ctx, req.KnowledgeID)
	if err != nil {
		return nil, err
	}

	return &KnowledgeSaveResponse{
		Knowledge:       *record,
		DocumentNodeID:  plan.ingest.documentNodeID,
		EntityNodeIDs:   uniqueSortedStrings(plan.ingest.entityNodeIDs),
		RelationEdgeIDs: uniqueSortedStrings(plan.ingest.relationEdgeIDs),
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
		plan, err := db.buildKnowledgeMutationPlan(ctx, knowledgeMutationInput{
			KnowledgeID:  req.KnowledgeID,
			Title:        title,
			Content:      content,
			SourceURL:    sourceURL,
			Author:       author,
			Collection:   collection,
			ChunkSize:    chunkSize,
			ChunkOverlap: chunkOverlap,
			Version:      existing.Version + 1,
			Metadata:     metadata,
			Entities:     req.Entities,
			Relations:    req.Relations,
		})
		if err != nil {
			return nil, err
		}
		if err := db.applyKnowledgeMutation(ctx, req.KnowledgeID, true, plan); err != nil {
			return nil, err
		}
		ingest = &plan.ingest
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
	if !replaceArtifacts {
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
	resolution := resolveRetrievalPlan(retrievalPlanInput{
		Query:               req.Query,
		Plan:                req.Plan,
		Keywords:            req.Keywords,
		AlternateQueries:    req.AlternateQueries,
		EntityNames:         req.EntityNames,
		RetrievalMode:       req.RetrievalMode,
		DisableGraph:        req.DisableGraph,
		Filters:             &RetrievalFilters{Collection: req.Collection},
		SupportsGraph:       true,
		EmptyQueryUsesGraph: false,
	})
	if strings.TrimSpace(resolution.Plan.Query) == "" {
		return nil, ErrEmptyText
	}

	opts := GraphRAGQueryOptions{
		Collection:          applyRetrievalPlanCollection(req.Collection, resolution.Plan.Filters),
		TopK:                req.TopK,
		MaxHops:             req.MaxHops,
		MaxRelatedChunks:    req.MaxRelatedChunks,
		MaxContextChunks:    req.MaxContextChunks,
		MaxContextChars:     req.MaxContextChars,
		PerDocumentLimit:    req.PerDocumentLimit,
		DiversityLambda:     req.DiversityLambda,
		Rerank:              true,
		DisableRerank:       req.DisableRerank,
		RetrievalMode:       resolution.Plan.RetrievalMode,
		DisableGraph:        req.DisableGraph,
		GraphLight:          req.GraphLight,
		MaxExpansionSeeds:   req.MaxExpansionSeeds,
		MaxTraversalNodes:   req.MaxTraversalNodes,
		MaxEntitiesPerChunk: req.MaxEntitiesPerChunk,
		Plan:                &resolution.Plan,
	}
	applyGraphRAGQueryDefaults(&opts)

	var result *GraphRAGQueryResult
	var err error
	if db.HasEmbedder() && resolution.Decision.EffectiveMode != RetrievalModeLexical {
		result, err = db.SearchGraphRAG(ctx, resolution.Plan.Query, opts)
	} else {
		result, err = db.GraphRAGTools().SearchGraphRAGLexical(ctx, ToolSearchGraphRAGLexicalRequest{
			Query:               resolution.Plan.Query,
			Collection:          opts.Collection,
			TopK:                req.TopK,
			MaxHops:             req.MaxHops,
			MaxRelatedChunks:    req.MaxRelatedChunks,
			MaxContextChunks:    req.MaxContextChunks,
			MaxContextChars:     req.MaxContextChars,
			PerDocumentLimit:    req.PerDocumentLimit,
			DiversityLambda:     req.DiversityLambda,
			DisableRerank:       req.DisableRerank,
			RetrievalMode:       resolution.Plan.RetrievalMode,
			DisableGraph:        req.DisableGraph,
			GraphLight:          req.GraphLight,
			MaxExpansionSeeds:   req.MaxExpansionSeeds,
			MaxTraversalNodes:   req.MaxTraversalNodes,
			MaxEntitiesPerChunk: req.MaxEntitiesPerChunk,
			Plan:                &resolution.Plan,
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
		Query:    result.Query,
		Plan:     result.Plan,
		Decision: result.Decision,
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
