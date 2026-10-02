package cortexdb

import (
	"context"
	"fmt"
	"log"
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
		// Each phrasing below is a variant of one question; the CJK bigram
		// path runs once on the question itself, after the loop.
		skipCJKBigrams: true,
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

	// A CJK sentence is one token to the word index; see lexical_cjk.go.
	if core.ContainsCJK(resolution.Plan.Query) {
		cjk, err := t.db.searchTextCJK(ctx, resolution.Plan.Query, searchOpts)
		if err != nil {
			log.Printf("cortexdb: cjk bigram search skipped: %v", err)
		}
		for _, result := range cjk {
			if existing, ok := merged[result.ID]; !ok || result.Score > existing.Score {
				merged[result.ID] = result
			}
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
	// Traversal filters on the node type stored on each node, so an interface
	// has to become its implementors before the hop is taken.
	nodeTypes, err := t.db.expandOntologyTypeFilter(ctx, req.NodeTypes)
	if err != nil {
		return nil, err
	}

	nodeSet := make(map[string]struct{}, len(req.NodeIDs))
	var scores map[string]float64
	if len(req.RelationPolicies) > 0 {
		scores = map[string]float64{}
	}
	for _, nodeID := range req.NodeIDs {
		if nodeID == "" {
			continue
		}
		nodeSet[nodeID] = struct{}{}
		if scores != nil {
			scores[nodeID] = 1
		}
		opts := graph.TraversalOptions{
			MaxDepth:  req.MaxHops,
			Direction: "both",
			EdgeTypes: req.EdgeTypes,
			NodeTypes: nodeTypes,
			Limit:     req.Limit,
			Relations: req.RelationPolicies,
		}
		if scores != nil {
			scored, err := t.db.graph.ScoredNeighbors(ctx, nodeID, opts)
			if err != nil {
				return nil, err
			}
			for _, n := range scored {
				nodeSet[n.NodeID] = struct{}{}
				if n.Score > scores[n.NodeID] {
					scores[n.NodeID] = n.Score
				}
			}
			continue
		}
		neighbors, err := t.db.graph.Neighbors(ctx, nodeID, opts)
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
	return &ToolExpandGraphResponse{Nodes: subgraph.Nodes, Edges: subgraph.Edges, Scores: scores}, nil
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
	result, err := t.searchGraphRAGLexical(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := t.db.attachGraphRAGPaths(ctx, result, GraphRAGQueryOptions{
		ReturnPaths:      req.ReturnPaths,
		PathMaxDepth:     req.PathMaxDepth,
		RelationPolicies: req.RelationPolicies,
		MaxPaths:         req.MaxPaths,
	}); err != nil {
		return nil, fmt.Errorf("search paths: %w", err)
	}
	return result, nil
}

func (t *GraphRAGToolbox) searchGraphRAGLexical(ctx context.Context, req ToolSearchGraphRAGLexicalRequest) (*GraphRAGQueryResult, error) {
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
		ChunkWindow:         req.ChunkWindow,
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

	chunkResults := make(map[string]*GraphRAGChunkResult)
	entitySet := make(map[string]struct{})
	seedOrder := make([]string, 0, len(seedResp.Chunks))
	for _, chunk := range seedResp.Chunks {
		if !allowDocumentID(resolution.Plan.Filters, chunk.DocumentID) {
			continue
		}
		if _, dup := chunkResults[chunk.ID]; dup {
			continue
		}
		chunkResults[chunk.ID] = &GraphRAGChunkResult{
			ID:         chunk.ID,
			DocumentID: chunk.DocumentID,
			Content:    chunk.Content,
			Score:      chunk.Score,
			BaseScore:  chunk.Score,
		}
		seedOrder = append(seedOrder, chunk.ID)
	}

	if !useGraph {
		if len(seedOrder) == 0 {
			return result, nil
		}
		// Naming what a chunk mentions is not graph expansion and does not
		// wait for the expansion decision — see the same branch in
		// graphrag.go. One batched lookup of the mention edges.
		// Unless the caller opted out: an explicit lexical mode or DisableGraph
		// means "do not touch the graph", and that contract stands. The rule
		// is the one the chunk-loading tools already use.
		if shouldLoadChunkEntities(opts.RetrievalMode, opts.DisableGraph, "") {
			names, err := t.db.chunkEntityNamesBatch(ctx, seedOrder, opts.MaxEntitiesPerChunk)
			if err != nil {
				return nil, fmt.Errorf("load chunk entities: %w", err)
			}
			for chunkID, chunk := range chunkResults {
				chunk.Entities = names[chunkID]
				for _, entityName := range chunk.Entities {
					entitySet[entityName] = struct{}{}
				}
			}
			result.Entities = sortedKeys(entitySet)
		}
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
		if err := t.db.widenGraphRAGContext(ctx, result, opts); err != nil {
			return nil, err
		}
		return result, nil
	}

	// Graph mode: three rankings of chunks, fused by rank.
	//
	//  L: the lexical seeds, as the lexical mode ranks them;
	//  E: chunks that mention an entity the query names, by specificity;
	//  X: chunks graph expansion reaches from the head of L and E.
	//
	// They used to be merged by score, and the scores are not on one scale:
	// a lexical seed carries an RRF value near 1/61, an entity-linked chunk
	// the count of query entities it is linked to (floored at 0.75), an
	// expanded chunk half its parent's. The reranker min-max normalises them
	// together, so every graph-derived chunk, however loosely linked,
	// outranked every lexical hit, and the passage the question names fell
	// out of the top five: graph, and auto, which picks graph for any
	// capitalised word, scored 9-11 points of recall@5 below lexical on 2Wiki
	// and MuSiQue. Ranks are on one scale by construction, and RRF lets a
	// chunk two rankings agree on rise above one that either ranks first
	// alone.
	lexicalRank := make(map[string]int, len(seedOrder))
	for i, id := range seedOrder {
		lexicalRank[id] = i + 1
	}
	entityIDs, err := t.db.queryEntityNodeIDs(ctx, resolution.Plan.Query, resolution.Plan.EntityNames)
	if err != nil {
		return nil, err
	}
	linked, err := t.db.entityLinkedChunks(ctx, entityIDs, opts.Collection, resolution.Plan.Filters, lexicalRank, max(opts.TopK*graphEntityPoolFactor, graphEntityPoolMin))
	if err != nil {
		return nil, err
	}
	entityRank := make(map[string]int, len(linked))
	for i, chunk := range linked {
		entityRank[chunk.ID] = i + 1
		if _, ok := chunkResults[chunk.ID]; !ok {
			c := chunk
			chunkResults[chunk.ID] = &c
		}
	}
	if len(chunkResults) == 0 {
		return result, nil
	}

	fused := make(map[string]float64, len(chunkResults))
	firstOrder := make([]string, 0, len(chunkResults))
	for id := range chunkResults {
		if r, ok := lexicalRank[id]; ok {
			fused[id] += 1 / (hybridRRFK + float64(r))
		}
		if r, ok := entityRank[id]; ok {
			fused[id] += 1 / (hybridRRFK + float64(r))
		}
		firstOrder = append(firstOrder, id)
	}
	sort.Slice(firstOrder, func(i, j int) bool {
		if fused[firstOrder[i]] != fused[firstOrder[j]] {
			return fused[firstOrder[i]] > fused[firstOrder[j]]
		}
		return firstOrder[i] < firstOrder[j]
	})
	// Expansion starts from the fused head, and orders what it reaches by
	// the fused score of where it came from.
	for _, id := range firstOrder {
		chunkResults[id].Score = fused[id]
	}
	expandedEntities, err := t.db.expandGraphChunkNeighborhoods(ctx, chunkResults, firstOrder, opts, resolution.Plan.Filters)
	if err != nil {
		return nil, err
	}
	for entityName := range expandedEntities {
		entitySet[entityName] = struct{}{}
	}

	relatedIDs := make([]string, 0, len(chunkResults))
	for chunkID := range chunkResults {
		if _, first := fused[chunkID]; !first {
			relatedIDs = append(relatedIDs, chunkID)
		}
	}
	sort.Slice(relatedIDs, func(i, j int) bool {
		si, sj := chunkResults[relatedIDs[i]].Score, chunkResults[relatedIDs[j]].Score
		if si != sj {
			return si > sj
		}
		return relatedIDs[i] < relatedIDs[j]
	})
	if len(relatedIDs) > opts.MaxRelatedChunks {
		relatedIDs = relatedIDs[:opts.MaxRelatedChunks]
	}
	for i, id := range relatedIDs {
		fused[id] = graphExpansionRRFWeight / (hybridRRFK + float64(i+1))
	}

	allChunks := make([]GraphRAGChunkResult, 0, len(fused))
	chunkIDs := make([]string, 0, len(fused))
	for id, score := range fused {
		chunk := chunkResults[id]
		// Expansion may have raised a first-stage chunk's score on its own
		// scale; the fused value is the one to rank by.
		chunk.Score = score
		allChunks = append(allChunks, *chunk)
		chunkIDs = append(chunkIDs, id)
	}
	entityNamesByChunk, err := t.db.chunkEntityNamesBatch(ctx, chunkIDs, opts.MaxEntitiesPerChunk)
	if err != nil {
		return nil, err
	}
	for i := range allChunks {
		allChunks[i].Entities = entityNamesByChunk[allChunks[i].ID]
		for _, entityName := range allChunks[i].Entities {
			entitySet[entityName] = struct{}{}
		}
	}
	sort.Slice(allChunks, func(i, j int) bool {
		if allChunks[i].Score != allChunks[j].Score {
			return allChunks[i].Score > allChunks[j].Score
		}
		return allChunks[i].ID < allChunks[j].ID
	})
	allChunks = t.db.rerankGraphRAGChunks(ctx, resolution.Plan.Query, allChunks, opts)
	allChunks = packGraphRAGContext(allChunks, opts)

	result.Chunks = allChunks
	result.Entities = sortedKeys(entitySet)
	result.Context = buildGraphRAGContext(allChunks)
	if err := t.db.widenGraphRAGContext(ctx, result, opts); err != nil {
		return nil, err
	}
	return result, nil
}

// graphExpansionRRFWeight is how much a rank in the expansion list counts
// against a rank in the lexical or entity list. Expansion reaches a chunk by
// adjacency alone — the next passage, a co-occurring entity — so at full
// weight its head displaced lexical hits on single-hop questions (-2 points
// of recall@5 on MuSiQue sub-questions); at half it fills the context behind
// them, which is what it is for.
const graphExpansionRRFWeight = 0.5

// The entity list is read as wide as the pool PPR fuses, for the same reason:
// it is a list to fuse, not a context to read.
const (
	graphEntityPoolFactor = 5
	graphEntityPoolMin    = 20
)
