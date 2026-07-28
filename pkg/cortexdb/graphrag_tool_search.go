package cortexdb

import (
	"context"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// SearchText runs lexical retrieval over chunk content.
func (t *GraphRAGToolbox) SearchText(ctx context.Context, req ToolSearchTextRequest) (*ToolSearchTextResponse, error) {
	resolution := resolveRetrievalPlan(retrievalPlanInput{
		Query:               req.Query,
		Plan:                req.Plan,
		Keywords:            req.Keywords,
		AlternateQueries:    req.AlternateQueries,
		RetrievalMode:       req.RetrievalMode,
		DisableGraph:        req.DisableGraph,
		Filters:             &RetrievalFilters{Collection: req.Collection},
		SupportsGraph:       true,
		EmptyQueryUsesGraph: false,
	})
	if strings.TrimSpace(resolution.Plan.Query) == "" {
		return nil, ErrEmptyText
	}

	results, err := t.searchTextCandidates(ctx, req, resolution)
	if err != nil {
		return nil, err
	}
	chunks, err := t.toolChunksFromSearchResults(ctx, results, resolution.Decision.UseGraph, maxEntitiesPerChunk(resolution.Plan.RetrievalMode, req.GraphLight, req.MaxEntitiesPerChunk))
	if err != nil {
		return nil, err
	}
	return &ToolSearchTextResponse{
		Plan:     resolution.Plan,
		Decision: resolution.Decision,
		Chunks:   chunks,
	}, nil
}

func (t *GraphRAGToolbox) searchTextCandidates(ctx context.Context, req ToolSearchTextRequest, resolution retrievalPlanResolution) ([]core.ScoredEmbedding, error) {
	// top_k is optional for the caller, so normalise it here rather than relying on the default
	// SearchTextOnly applies to its own copy: the merge below truncates to this value, and a zero
	// left in place cuts the entire result set away instead of returning the default page of it.
	if req.TopK <= 0 {
		req.TopK = defaultSearchTopK
	}
	searchOpts := TextSearchOptions{
		Collection: applyRetrievalPlanCollection(req.Collection, resolution.Plan.Filters),
		TopK:       req.TopK,
		Threshold:  req.Threshold,
	}

	queries := lexicalSearchQueries(resolution.Plan.Query, resolution.Plan.Keywords, resolution.Plan.AlternateQueries)
	if len(queries) == 0 {
		return nil, ErrEmptyText
	}

	merged := make(map[string]core.ScoredEmbedding)
	var firstErr error
	for idx, query := range queries {
		results, err := t.db.SearchTextOnly(ctx, query, searchOpts)
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
		if idx == 0 && len(merged) >= searchOpts.TopK {
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
	if len(ordered) > searchOpts.TopK {
		ordered = ordered[:searchOpts.TopK]
	}
	return filterScoredEmbeddings(ordered, resolution.Plan.Filters), nil
}

// SearchChunksByEntities finds chunks that are linked to the requested entities.
func (t *GraphRAGToolbox) SearchChunksByEntities(ctx context.Context, req ToolSearchChunksByEntitiesRequest) (*ToolSearchChunksByEntitiesResponse, error) {
	if len(req.EntityNames) == 0 {
		return &ToolSearchChunksByEntitiesResponse{}, nil
	}
	if req.TopK <= 0 {
		req.TopK = defaultSearchTopK
	}
	if req.MaxHops <= 0 {
		req.MaxHops = 1
	}

	scoreMap := make(map[string]float64)
	var firstErr error
	for _, entityName := range req.EntityNames {
		entityID := resolveEntityNodeID("", entityName)
		neighbors, err := t.db.graph.Neighbors(ctx, entityID, graph.TraversalOptions{
			MaxDepth:  req.MaxHops,
			Direction: "both",
			NodeTypes: []string{"chunk"},
			Limit:     req.TopK * 8,
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, node := range neighbors {
			scoreMap[node.ID] += 1.0
		}
	}
	if len(scoreMap) == 0 && firstErr != nil {
		return nil, firstErr
	}

	ordered := sortIDsByScore(scoreMap)
	if len(ordered) > req.TopK {
		ordered = ordered[:req.TopK]
	}
	chunks, err := t.loadToolChunks(ctx, scoreMap, ordered, true, maxEntitiesPerChunk(RetrievalModeGraph, false, 0))
	if err != nil {
		return nil, err
	}
	return &ToolSearchChunksByEntitiesResponse{Chunks: chunks}, nil
}

// ExpandGraph expands a graph neighborhood and returns a materialized subgraph.
func (t *GraphRAGToolbox) ExpandGraph(ctx context.Context, req ToolExpandGraphRequest) (*ToolExpandGraphResponse, error) {
	if len(req.NodeIDs) == 0 {
		return &ToolExpandGraphResponse{}, nil
	}
	if req.MaxHops <= 0 {
		req.MaxHops = 1
	}

	nodeSet := make(map[string]struct{}, len(req.NodeIDs))
	for _, nodeID := range req.NodeIDs {
		if nodeID == "" {
			continue
		}
		nodeSet[nodeID] = struct{}{}
		neighbors, err := t.db.graph.Neighbors(ctx, nodeID, graph.TraversalOptions{
			MaxDepth:  req.MaxHops,
			Direction: "both",
			EdgeTypes: req.EdgeTypes,
			NodeTypes: req.NodeTypes,
			Limit:     req.Limit,
		})
		if err != nil {
			return nil, err
		}
		for _, node := range neighbors {
			nodeSet[node.ID] = struct{}{}
		}
	}

	nodeIDs := sortedKeysFromSet(nodeSet)
	subgraph, err := t.db.graph.Subgraph(ctx, nodeIDs)
	if err != nil {
		return nil, err
	}
	return &ToolExpandGraphResponse{Nodes: subgraph.Nodes, Edges: subgraph.Edges}, nil
}

// GetNodes fetches graph nodes by ID.
func (t *GraphRAGToolbox) GetNodes(ctx context.Context, req ToolGetNodesRequest) (*ToolGetNodesResponse, error) {
	nodes, err := t.db.graph.GetNodesBatch(ctx, req.NodeIDs)
	if err != nil {
		return nil, err
	}
	return &ToolGetNodesResponse{Nodes: nodes}, nil
}

// GetChunks fetches chunk records by ID.
func (t *GraphRAGToolbox) GetChunks(ctx context.Context, req ToolGetChunksRequest) (*ToolGetChunksResponse, error) {
	chunks, err := t.loadToolChunks(ctx, nil, req.ChunkIDs, shouldLoadChunkEntities(req.RetrievalMode, req.DisableGraph, ""), maxEntitiesPerChunk(req.RetrievalMode, req.GraphLight, req.MaxEntitiesPerChunk))
	if err != nil {
		return nil, err
	}
	return &ToolGetChunksResponse{Chunks: chunks}, nil
}

// BuildContext packs chunk text into a bounded context string.
func (t *GraphRAGToolbox) BuildContext(ctx context.Context, req ToolBuildContextRequest) (*ToolBuildContextResponse, error) {
	chunks, err := t.loadToolChunks(ctx, nil, req.ChunkIDs, shouldLoadChunkEntities(req.RetrievalMode, req.DisableGraph, ""), maxEntitiesPerChunk(req.RetrievalMode, req.GraphLight, req.MaxEntitiesPerChunk))
	if err != nil {
		return nil, err
	}

	queryOpts := GraphRAGQueryOptions{
		MaxContextChunks: req.MaxContextChunks,
		MaxContextChars:  req.MaxContextChars,
		PerDocumentLimit: req.PerDocumentLimit,
		DisableRerank:    true,
	}
	applyGraphRAGQueryDefaults(&queryOpts)

	graphChunks := make([]GraphRAGChunkResult, 0, len(chunks))
	for i, chunk := range chunks {
		graphChunks = append(graphChunks, GraphRAGChunkResult{
			ID:          chunk.ID,
			DocumentID:  chunk.DocumentID,
			Content:     chunk.Content,
			Score:       float64(len(chunks) - i),
			BaseScore:   float64(len(chunks) - i),
			RerankScore: float64(len(chunks) - i),
			Entities:    chunk.Entities,
		})
	}

	packed := packGraphRAGContext(graphChunks, queryOpts)
	return &ToolBuildContextResponse{
		Chunks:  packed,
		Context: buildGraphRAGContext(packed),
	}, nil
}

// SearchGraphRAGLexical performs no-embedder GraphRAG retrieval for external LLM orchestration.
func (t *GraphRAGToolbox) SearchGraphRAGLexical(ctx context.Context, req ToolSearchGraphRAGLexicalRequest) (*GraphRAGQueryResult, error) {
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
		DisableRerank:       req.DisableRerank,
		DiversityLambda:     req.DiversityLambda,
		Rerank:              true,
		RetrievalMode:       resolution.Plan.RetrievalMode,
		DisableGraph:        req.DisableGraph,
		GraphLight:          req.GraphLight,
		MaxExpansionSeeds:   req.MaxExpansionSeeds,
		MaxTraversalNodes:   req.MaxTraversalNodes,
		MaxEntitiesPerChunk: req.MaxEntitiesPerChunk,
		Plan:                &resolution.Plan,
	}
	applyGraphRAGQueryDefaults(&opts)

	seedResp, err := t.SearchText(ctx, ToolSearchTextRequest{
		Query:               resolution.Plan.Query,
		Collection:          opts.Collection,
		TopK:                opts.TopK,
		Threshold:           0,
		RetrievalMode:       resolution.Plan.RetrievalMode,
		DisableGraph:        req.DisableGraph,
		GraphLight:          req.GraphLight,
		MaxEntitiesPerChunk: req.MaxEntitiesPerChunk,
		Plan:                &resolution.Plan,
	})
	if err != nil {
		return nil, err
	}

	result := &GraphRAGQueryResult{
		Query:    resolution.Plan.Query,
		Plan:     resolution.Plan,
		Decision: resolution.Decision,
	}
	useGraph := resolution.Decision.UseGraph
	entityNames := resolution.Plan.EntityNames
	if useGraph && len(entityNames) == 0 {
		entityNames = extractEntityNames(extractTitleEntities(resolution.Plan.Query))
	}

	chunkResults := make(map[string]*GraphRAGChunkResult)
	seedIDs := make(map[string]struct{})
	entitySet := make(map[string]struct{})
	seedOrder := make([]string, 0, len(seedResp.Chunks))

	addChunk := func(chunk ToolChunk, seed bool) {
		if !allowDocumentID(resolution.Plan.Filters, chunk.DocumentID) {
			return
		}
		existing, ok := chunkResults[chunk.ID]
		if !ok {
			existing = &GraphRAGChunkResult{
				ID:         chunk.ID,
				DocumentID: chunk.DocumentID,
				Content:    chunk.Content,
				Score:      chunk.Score,
				BaseScore:  chunk.Score,
			}
			chunkResults[chunk.ID] = existing
		} else if chunk.Score > existing.Score {
			existing.Score = chunk.Score
			existing.BaseScore = chunk.Score
		}
		if seed {
			if _, exists := seedIDs[chunk.ID]; !exists {
				seedIDs[chunk.ID] = struct{}{}
				seedOrder = append(seedOrder, chunk.ID)
			}
		}
	}

	for _, chunk := range seedResp.Chunks {
		addChunk(chunk, true)
	}

	if useGraph && len(entityNames) > 0 {
		entityResp, err := t.SearchChunksByEntities(ctx, ToolSearchChunksByEntitiesRequest{
			EntityNames: entityNames,
			TopK:        opts.TopK,
			MaxHops:     opts.MaxHops,
		})
		if err != nil {
			return nil, err
		}
		for _, chunk := range entityResp.Chunks {
			if chunk.Score < 0.75 {
				chunk.Score = 0.75
			}
			addChunk(chunk, true)
		}
	}

	if len(seedOrder) == 0 {
		if useGraph {
			result.Entities = sortedKeys(entitySet)
		}
		return result, nil
	}

	if !useGraph {
		allChunks := make([]GraphRAGChunkResult, 0, len(seedOrder))
		for _, seedID := range seedOrder {
			if chunk := chunkResults[seedID]; chunk != nil {
				allChunks = append(allChunks, *chunk)
			}
		}
		allChunks = t.db.rerankGraphRAGChunks(ctx, resolution.Plan.Query, allChunks, opts)
		allChunks = packGraphRAGContext(allChunks, opts)
		result.Chunks = allChunks
		result.Context = buildGraphRAGContext(allChunks)
		return result, nil
	}

	expandedEntities, err := t.db.expandGraphChunkNeighborhoods(ctx, chunkResults, seedOrder, opts, resolution.Plan.Filters)
	if err != nil {
		return nil, err
	}
	for entityName := range expandedEntities {
		entitySet[entityName] = struct{}{}
	}

	chunkIDs := make([]string, 0, len(chunkResults))
	for chunkID := range chunkResults {
		chunkIDs = append(chunkIDs, chunkID)
	}
	entityNamesByChunk, err := t.db.chunkEntityNamesBatch(ctx, chunkIDs, opts.MaxEntitiesPerChunk)
	if err != nil {
		return nil, err
	}
	for chunkID, chunk := range chunkResults {
		chunk.Entities = entityNamesByChunk[chunkID]
		for _, entityName := range chunk.Entities {
			entitySet[entityName] = struct{}{}
		}
	}

	seedChunks := make([]GraphRAGChunkResult, 0, len(seedOrder))
	for _, seedID := range seedOrder {
		if chunk := chunkResults[seedID]; chunk != nil {
			seedChunks = append(seedChunks, *chunk)
		}
	}

	relatedChunks := make([]GraphRAGChunkResult, 0, len(chunkResults))
	for chunkID, chunk := range chunkResults {
		if _, ok := seedIDs[chunkID]; ok {
			continue
		}
		relatedChunks = append(relatedChunks, *chunk)
	}
	sort.Slice(relatedChunks, func(i, j int) bool { return relatedChunks[i].Score > relatedChunks[j].Score })
	if len(relatedChunks) > opts.MaxRelatedChunks {
		relatedChunks = relatedChunks[:opts.MaxRelatedChunks]
	}

	allChunks := append(seedChunks, relatedChunks...)
	allChunks = t.db.rerankGraphRAGChunks(ctx, resolution.Plan.Query, allChunks, opts)
	allChunks = packGraphRAGContext(allChunks, opts)

	result.Chunks = allChunks
	result.Entities = sortedKeys(entitySet)
	result.Context = buildGraphRAGContext(allChunks)
	return result, nil
}
