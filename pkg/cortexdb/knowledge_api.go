package cortexdb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
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
	if metadata == nil {
		metadata = map[string]string{}
	}
	var stored map[string]any
	if existing != nil {
		stored = existing.Metadata
	}
	if err := carryExpiry(metadata, stored, req.ExpiresAt); err != nil {
		return nil, err
	}
	stampWriter(ctx, metadata, plainString)
	if err := setReview(ctx, metadata, storedMetadata(existing), req.review()); err != nil {
		return nil, err
	}
	db.stampIngestMarks(metadata, req.Content, resolvedIngestOptions(req.Collection, req.ChunkSize, req.ChunkOverlap))
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
	retire := retireNothing
	if existing != nil {
		retire = retireGraph
	}
	if err := db.applyKnowledgeMutation(ctx, req.KnowledgeID, retire, plan); err != nil {
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
	if metadata == nil && (writerOf(ctx) != "" || req.ExpiresAt != nil || req.ReviewStatus != "") {
		metadata = map[string]string{}
	}
	if metadata != nil {
		if err := carryExpiry(metadata, existing.Metadata, req.ExpiresAt); err != nil {
			return nil, err
		}
		if err := setReview(ctx, metadata, existing.Metadata, req.review()); err != nil {
			return nil, err
		}
	}
	stampUpdater(ctx, metadata, existing.Metadata, plainString)

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
		// The chunks are rebuilt, so the marks describe the new ones: the
		// hash of the content as it now is, and the chunking used here.
		if metadata == nil {
			metadata = map[string]string{}
		}
		db.stampIngestMarks(metadata, content, resolvedIngestOptions(collection, chunkSize, chunkOverlap))
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
		if err := db.applyKnowledgeMutation(ctx, req.KnowledgeID, retireGraph, plan); err != nil {
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
//
// The list form takes at most MaxListedIDs ids and answers each distinct id
// once (listedIDs). It lists a document the caller's scope does not reach under
// missing, in the order asked, exactly as an id that names nothing: a
// confined caller cannot tell the two apart. The single form stays
// unconfined on purpose, as GetMemory's does: the row guard of the ports
// (authz.Scope.GuardToolRows) reads through it.
func (db *DB) GetKnowledge(ctx context.Context, req KnowledgeGetRequest) (*KnowledgeGetResponse, error) {
	if len(req.KnowledgeIDs) > 0 {
		ids, err := listedIDs(req.KnowledgeIDs)
		if err != nil {
			return nil, err
		}
		resp := &KnowledgeGetResponse{}
		for _, id := range ids {
			if id == "" {
				resp.Missing = append(resp.Missing, id)
				continue
			}
			record, err := db.loadKnowledgeRecord(ctx, id)
			if errors.Is(err, core.ErrNotFound) {
				resp.Missing = append(resp.Missing, id)
				continue
			}
			if err != nil {
				return nil, err
			}
			if !knowledgeListed(ctx, record) {
				resp.Missing = append(resp.Missing, id)
				continue
			}
			resp.Knowledges = append(resp.Knowledges, *record)
		}
		if len(resp.Knowledges) > 0 {
			resp.Knowledge = resp.Knowledges[0]
		}
		return resp, nil
	}
	record, err := db.loadKnowledgeRecord(ctx, req.KnowledgeID)
	if err != nil {
		return nil, err
	}
	return &KnowledgeGetResponse{Knowledge: *record}, nil
}

// SearchKnowledge searches durable knowledge and groups chunk results by knowledge document.
func (db *DB) SearchKnowledge(ctx context.Context, req KnowledgeSearchRequest) (*KnowledgeSearchResponse, error) {
	ctx = withVariantMemo(ctx)
	// Pre-retrieval query transformation (rewrite + HyDE): fold any transformer
	// AlternateQueries/Keywords into the request before planning, and capture the
	// hypothetical document to steer the semantic query vector below. Best-effort:
	// hydeDoc is "" when no transformer is set or it declined/errored.
	doneTransform := observability.Time(ctx, "query-transform")
	hydeDoc := db.applyQueryTransform(ctx, &req)
	doneTransform()

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
		PreferSemantic:      db.HasEmbedder(),
	})
	if strings.TrimSpace(resolution.Plan.Query) == "" {
		return nil, ErrEmptyText
	}
	autoUsesWalk(&resolution.Decision, db.HasEmbedder())
	if err := db.autoWalksFromAnEntity(ctx, &resolution); err != nil {
		return nil, err
	}

	collection := applyRetrievalPlanCollection(req.Collection, resolution.Plan.Filters)
	// A collection the caller may not read answers like an empty one.
	if !knowledgeReadPermitted(ctx, collection) {
		return &KnowledgeSearchResponse{Query: resolution.Plan.Query, Plan: resolution.Plan, Decision: resolution.Decision}, nil
	}
	opts := GraphRAGQueryOptions{
		Collection:          collection,
		TopK:                req.TopK,
		MaxHops:             req.MaxHops,
		MaxRelatedChunks:    req.MaxRelatedChunks,
		MaxContextChunks:    req.MaxContextChunks,
		MaxContextChars:     req.MaxContextChars,
		PerDocumentLimit:    req.PerDocumentLimit,
		ChunkWindow:         req.ChunkWindow,
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

	lexReq := ToolSearchGraphRAGLexicalRequest{
		Query:               resolution.Plan.Query,
		Collection:          opts.Collection,
		TopK:                req.TopK,
		MaxHops:             req.MaxHops,
		MaxRelatedChunks:    req.MaxRelatedChunks,
		MaxContextChunks:    req.MaxContextChunks,
		MaxContextChars:     req.MaxContextChars,
		PerDocumentLimit:    req.PerDocumentLimit,
		ChunkWindow:         req.ChunkWindow,
		DiversityLambda:     req.DiversityLambda,
		DisableRerank:       req.DisableRerank,
		RetrievalMode:       resolution.Plan.RetrievalMode,
		DisableGraph:        req.DisableGraph,
		GraphLight:          req.GraphLight,
		MaxExpansionSeeds:   req.MaxExpansionSeeds,
		MaxTraversalNodes:   req.MaxTraversalNodes,
		MaxEntitiesPerChunk: req.MaxEntitiesPerChunk,
		Plan:                &resolution.Plan,
	}

	// The keyword floor applied below is handed to the lexical candidate
	// search too, which fills top_k with the rows that pass it first.
	if resolution.Decision.EffectiveMode != RetrievalModePPR {
		ctx = withKnowledgeKeywordFloor(ctx, db.newKnowledgeKeywordFloor(ctx, resolution.Plan, opts.Collection))
	}

	var result *GraphRAGQueryResult
	var err error
	if resolution.Decision.EffectiveMode == RetrievalModePPR {
		result, err = db.searchKnowledgePPR(ctx, req, resolution, opts, lexReq, hydeDoc)
	} else if db.HasEmbedder() && resolution.Decision.EffectiveMode != RetrievalModeLexical {
		// Fuse vector + lexical: semantic-only silently dropped exact-keyword
		// matches, so hybrid keeps both. When a HyDE document was produced, the
		// vector path embeds it instead of the raw query; the lexical path keeps
		// the raw query untouched.
		opts.EmbedText = hydeDoc
		result, err = db.searchKnowledgeHybrid(ctx, resolution.Plan.Query, opts, lexReq)
	} else {
		result, err = db.GraphRAGTools().SearchGraphRAGLexical(ctx, lexReq)
		if err == nil {
			recallExplainerOf(ctx).candidates(explainKnowledge, explainKeyword, chunkCandidates(result.Chunks, func(c GraphRAGChunkResult) float64 { return c.Score }))
		}
	}
	if err != nil {
		return nil, err
	}
	if resolution.Decision.EffectiveMode == RetrievalModePPR {
		recallExplainerOf(ctx).candidates(explainKnowledge, explainPPR, chunkCandidates(result.Chunks, func(c GraphRAGChunkResult) float64 { return c.Score }))
	}
	// PPR is left alone. A walk reaches passages that share no word with the
	// question — the second hop of a multi-hop question is one — and the
	// multi-hop benchmarks a floor there would have to be measured on were
	// not at hand.
	if resolution.Decision.EffectiveMode != RetrievalModePPR {
		if err := db.applyKnowledgeFloors(ctx, result, resolution.Plan, resolution.Decision, opts.Collection, opts); err != nil {
			return nil, err
		}
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
		Degraded: result.Degraded,
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
