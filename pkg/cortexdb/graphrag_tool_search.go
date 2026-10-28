package cortexdb

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
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
		reviewStatus:   reviewStatusOf(resolution.Plan.Filters),
	}

	variants := t.db.chunkVariantWords(ctx, searchOpts.Collection, resolution.Plan.AlternateQueries)
	queries := lexicalSearchQueries(resolution.Plan.Query, resolution.Plan.Keywords, resolution.Plan.AlternateQueries, variants.kept)
	if len(queries) == 0 {
		return nil, ErrEmptyText
	}

	// A plain question stops when it fills top_k by itself. A search with
	// alternate queries or keywords runs every phrasing, each to four times
	// top_k as SearchTextOnly's expanded path does, and fuses their lists by
	// rank (see fuseLexicalLists): a list cut at top_k would rank a row by
	// where the cut fell.
	if len(resolution.Plan.AlternateQueries) > 0 || len(resolution.Plan.Keywords) > 0 {
		return t.searchTextPhrasings(ctx, resolution, searchOpts, fusedPhrasings(resolution.Plan.Query, resolution.Plan.AlternateQueries, queries))
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
			slog.Warn("cortexdb: cjk bigram search skipped", observability.LogArgs(ctx, "err", err)...)
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
	return t.cutTextCandidates(ctx, ordered, searchOpts.TopK, resolution), nil
}

// searchTextPhrasings is searchTextCandidates for a search with alternate
// queries or keywords: every phrasing runs, to four times top_k, and the CJK
// bigram list of the question joins them as one of its own.
func (t *GraphRAGToolbox) searchTextPhrasings(ctx context.Context, resolution retrievalPlanResolution, searchOpts TextSearchOptions, phrasings []lexicalPhrasing) ([]core.ScoredEmbedding, error) {
	listOpts := searchOpts
	listOpts.TopK *= 4
	var lists []lexicalList
	var firstErr error
	for _, phrasing := range phrasings {
		results, err := t.db.SearchTextOnly(ctx, phrasing.query, listOpts)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		lists = append(lists, lexicalList{rows: results, own: phrasing.own})
	}
	// A CJK sentence is one token to the word index; see lexical_cjk.go.
	if core.ContainsCJK(resolution.Plan.Query) {
		cjk, err := t.db.searchTextCJK(ctx, resolution.Plan.Query, searchOpts)
		if err != nil {
			slog.Warn("cortexdb: cjk bigram search skipped", observability.LogArgs(ctx, "err", err)...)
		}
		lists = append(lists, lexicalList{rows: cjk, own: true, share: cjkShareOfQuestion(resolution.Plan.Query)})
	}
	ordered := fuseLexicalLists(0, lists...)
	if len(ordered) == 0 {
		return nil, firstErr
	}
	return t.cutTextCandidates(ctx, ordered, searchOpts.TopK, resolution), nil
}

// cutTextCandidates cuts the ranked candidates of a text search to topK,
// the rows that pass SearchKnowledge's keyword floor first (see
// keywordFloorFirst), and applies the plan's filters.
func (t *GraphRAGToolbox) cutTextCandidates(ctx context.Context, ordered []core.ScoredEmbedding, topK int, resolution retrievalPlanResolution) []core.ScoredEmbedding {
	ordered = keywordFloorFirst(ctx, ordered, topK)
	if len(ordered) > topK {
		ordered = ordered[:topK]
	}
	// SearchTextOnly has applied the document gate to each phrasing, with
	// its budget refilled; the plan's filters (document_ids) apply here, as
	// on main.
	return t.db.filterScoredEmbeddings(ctx, ordered, resolution.Plan.Filters)
}

// SearchChunksByEntities finds chunks that are linked to the requested
// entities. Under a caller scope the walk takes only what the caller is
// served (walkNeighbors), so a chunk is counted only when the caller's own
// part of the graph reaches it.
func (t *GraphRAGToolbox) SearchChunksByEntities(ctx context.Context, req ToolSearchChunksByEntitiesRequest) (*ToolSearchChunksByEntitiesResponse, error) {
	return t.searchChunksByEntities(ctx, req, t.db.walkNeighbors)
}

// searchChunksByEntities is SearchChunksByEntities over the walk neighbors.
func (t *GraphRAGToolbox) searchChunksByEntities(ctx context.Context, req ToolSearchChunksByEntitiesRequest, neighbors func(context.Context, string, graph.TraversalOptions) ([]*graph.GraphNode, error)) (*ToolSearchChunksByEntitiesResponse, error) {
	if len(req.EntityNames) == 0 {
		return &ToolSearchChunksByEntitiesResponse{}, nil
	}
	if req.TopK <= 0 {
		req.TopK = defaultSearchTopK
	}
	if req.MaxHops <= 0 {
		req.MaxHops = 1
	}

	// The walk keeps the first top_k*8 nodes it meets per entity, in edge
	// order, before any chunk is loaded. The chunks are loaded, and the
	// hidden ones left out, before the cut to top_k, so a hidden chunk does
	// not use up the caller's limit. When hidden chunks also used up the
	// walk's pool, the walk is taken again with twice the pool, until top_k
	// is filled or no walk was cut. Under a caller scope the walk never
	// meets what the scope hides (walkNeighbors), and only an expired
	// document's chunks, which a walk with no scope still meets, are left
	// out here. Past maxChunkWalkPool the walk has no
	// limit (pool 0), as a top_k*8 that overflows had on main, so the loop
	// ends. A cancelled call ends it too: under a scope the seeds cannot
	// be read, and without one every walk fails, and either is the answer.
	entityIDs := make([]string, 0, len(req.EntityNames))
	for _, entityName := range req.EntityNames {
		entityIDs = append(entityIDs, resolveEntityNodeID("", entityName))
	}
	entityIDs, err := t.db.servedSeeds(ctx, entityIDs)
	if err != nil {
		return nil, err
	}
	for pool := chunkWalkPool(req.TopK); ; pool = widerChunkWalkPool(pool) {
		scoreMap := make(map[string]float64)
		var firstErr error
		cut := false
		for _, entityID := range entityIDs {
			reached, err := neighbors(ctx, entityID, graph.TraversalOptions{
				MaxDepth:  req.MaxHops,
				Direction: "both",
				Limit:     pool,
			})
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			// The type is read here, not by the walk, so the length of the
			// walk tells whether its limit cut it.
			cut = cut || (pool > 0 && len(reached) >= pool)
			for _, node := range reached {
				if node.NodeType == "chunk" {
					scoreMap[node.ID] += 1.0
				}
			}
		}
		if len(scoreMap) == 0 && firstErr != nil {
			return nil, firstErr
		}
		chunks, err := t.loadToolChunks(ctx, scoreMap, sortIDsByScore(scoreMap), true, maxEntitiesPerChunk(RetrievalModeGraph, false, 0))
		if err != nil {
			return nil, err
		}
		if len(chunks) >= req.TopK || !cut {
			return &ToolSearchChunksByEntitiesResponse{Chunks: chunks[:min(len(chunks), req.TopK)]}, nil
		}
	}
}

// maxChunkWalkPool is the largest pool a walk of search_chunks_by_entities
// takes; past it the walk has no limit.
const maxChunkWalkPool = 1 << 20

// chunkWalkPool is the first pool of a walk for top_k: top_k*8, or no limit
// (0) when that passes maxChunkWalkPool, overflow included.
func chunkWalkPool(topK int) int {
	if topK > maxChunkWalkPool/8 {
		return 0
	}
	return topK * 8
}

// widerChunkWalkPool doubles a pool, up to no limit (0) past
// maxChunkWalkPool.
func widerChunkWalkPool(pool int) int {
	if pool > maxChunkWalkPool/2 {
		return 0
	}
	return pool * 2
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

	seeds, err := t.db.servedSeeds(ctx, req.NodeIDs)
	if err != nil {
		return nil, err
	}
	nodeSet := make(map[string]struct{}, len(seeds))
	var scores map[string]float64
	if len(req.RelationPolicies) > 0 {
		scores = map[string]float64{}
	}
	// Under a caller scope the walk takes only the nodes and edges the
	// caller is served (walkAdmit): what it reaches, and the score of the
	// best path to it, are those of the caller's part of the graph.
	admit := t.db.walkAdmit(ctx)
	for _, nodeID := range seeds {
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
			Admit:     admit,
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
	nodes, edges := t.db.withoutExpiredDocuments(ctx, subgraph.Nodes, subgraph.Edges, scores)
	nodes, edges = ofTypes(nodes, edges, scores, nodeTypes, seeds)
	return &ToolExpandGraphResponse{Nodes: nodes, Edges: edges, Scores: scores}, nil
}

// withoutExpiredDocuments drops from a subgraph the document and chunk nodes
// of a hidden document (expired, held by the review gate, or out of the
// caller's scope), the memory nodes the review gate or the scope hides, the
// entities the scope hides (scopedNode), the edges that touch them and their
// scores, and the edges the caller is not served (servedEdges); an entity
// the scope serves is a reduced copy: the graph is the one place a search
// could otherwise still reach a hidden passage, through the entities it
// mentions.
func (db *DB) withoutExpiredDocuments(ctx context.Context, nodes []*graph.GraphNode, edges []*graph.GraphEdge, scores map[string]float64) ([]*graph.GraphNode, []*graph.GraphEdge) {
	docIDs := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if docID, _ := node.Properties["document_id"].(string); node.NodeType == "chunk" || node.NodeType == "document" {
			docIDs = append(docIDs, docID)
		}
	}
	hidden := db.hiddenAmong(ctx, nil, docIDs)
	dropped := db.hiddenMemoryNodes(ctx, nodes)
	scope, _ := callerScopeOf(ctx)
	keptNodes := make([]*graph.GraphNode, 0, len(nodes))
	for _, node := range nodes {
		docID, _ := node.Properties["document_id"].(string)
		if _, gone := dropped[node.ID]; gone {
			delete(scores, node.ID)
			continue
		}
		if (node.NodeType == "chunk" || node.NodeType == "document") && !allowDocument(nil, hidden, docID) {
			dropped[node.ID] = struct{}{}
			delete(scores, node.ID)
			continue
		}
		served, ok := scopedNode(node, scope.Knowledge)
		if !ok {
			dropped[node.ID] = struct{}{}
			delete(scores, node.ID)
			continue
		}
		keptNodes = append(keptNodes, served)
	}
	if len(dropped) == 0 {
		return keptNodes, db.servedEdges(ctx, edges)
	}
	keptEdges := make([]*graph.GraphEdge, 0, len(edges))
	for _, edge := range edges {
		if _, gone := dropped[edge.FromNodeID]; gone {
			continue
		}
		if _, gone := dropped[edge.ToNodeID]; gone {
			continue
		}
		keptEdges = append(keptEdges, edge)
	}
	return keptNodes, db.servedEdges(ctx, keptEdges)
}

// servedSeeds keeps, among the node ids a walk starts from, those the
// caller's scope serves, in their order: a walk from a hidden node would
// tell what the node links to. A seed that does not exist is dropped as
// well, and the walk answers as it does from a node that does not exist.
// When the seeds cannot be read, none is kept and the error is returned,
// so a caller tells an outage from a walk with nothing in it. Without a
// caller scope rule it reads nothing and keeps every id.
func (db *DB) servedSeeds(ctx context.Context, ids []string) ([]string, error) {
	if scope, _ := callerScopeOf(ctx); scope.Knowledge == nil && scope.Memory == nil {
		return ids, nil
	}
	nodes, err := db.graph.GetNodesBatch(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read the seeds of a walk: %w", err)
	}
	nodes, _ = db.withoutExpiredDocuments(ctx, nodes, nil, nil)
	served := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		served[node.ID] = true
	}
	return slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return !served[id] }), nil
}

// hiddenMemoryNodes is the set of memory nodes the review gate, or the
// caller scope's memory rule, leaves out of a walk, read one memory at a
// time: a walk meets few. A memory that cannot be read is hidden, as a
// document is, so an outage does not lift the gate or the scope.
func (db *DB) hiddenMemoryNodes(ctx context.Context, nodes []*graph.GraphNode) map[string]struct{} {
	hidden := map[string]struct{}{}
	scope, _ := callerScopeOf(ctx)
	if !db.reviewGate && scope.Memory == nil {
		return hidden
	}
	for _, node := range nodes {
		memoryID, ok := memoryIDFromGraphNode(node.ID)
		if !ok {
			continue
		}
		if row, err := db.loadMemoryRow(ctx, memoryID); err == nil && db.memoryNodeShown(ctx, row) {
			continue
		}
		hidden[node.ID] = struct{}{}
	}
	return hidden
}

// memoryNodeShown reports whether a walk may show the memory row: the
// review gate, when it is on, and the caller's memory rule let it through.
func (db *DB) memoryNodeShown(ctx context.Context, row *memoryRow) bool {
	if db.reviewGate && reviewHides(row.record.ReviewStatus) {
		return false
	}
	permitted, _ := db.memoryReadPermitted(ctx, row.record.SessionID)
	return permitted
}

// withoutExpiredProvenance takes out of each edge's provenance (chunk_ids,
// source_chunk_id, document_id) the chunks and the document of an expired
// document, or of one the review gate hides, so an edge does not name what
// the search hides. An edge that a live document also backs, or that names
// no provenance, stays: a fact between two entities has no expiry of its
// own. An edge that only hidden documents back is left out, since its id
// can name the document (upsert_relations with a document_id). An edge it
// changes is a copy: the graph's own edges are left as they are.
func (db *DB) withoutExpiredProvenance(ctx context.Context, edges []*graph.GraphEdge) []*graph.GraphEdge {
	var chunkIDs, docIDs []string
	for _, edge := range edges {
		if edge.Properties == nil {
			continue
		}
		chunkIDs = append(chunkIDs, stringsFromAny(edge.Properties["chunk_ids"])...)
		if src, _ := edge.Properties["source_chunk_id"].(string); src != "" {
			chunkIDs = append(chunkIDs, src)
		}
		if doc, _ := edge.Properties["document_id"].(string); doc != "" {
			docIDs = append(docIDs, doc)
		}
	}
	if len(chunkIDs) == 0 && len(docIDs) == 0 {
		return edges
	}
	docOf := map[string]string{}
	// A chunk whose node cannot be read is shown under the expiry alone,
	// which is advisory, and hidden under the review gate or a caller
	// scope's knowledge rule, which fail closed, as hiddenAmong does.
	scope, _ := callerScopeOf(ctx)
	unreadable := false
	if len(chunkIDs) > 0 {
		chunks, err := db.graph.GetNodesBatch(ctx, chunkIDs)
		if err != nil {
			if !db.reviewGate && scope.Knowledge == nil {
				return edges
			}
			unreadable = true
		}
		for _, chunk := range chunks {
			doc, _ := chunk.Properties["document_id"].(string)
			docOf[chunk.ID] = doc
			docIDs = append(docIDs, doc)
		}
	}
	// Under a knowledge rule a chunk that names no document of the
	// caller's, a chunk no node holds or one with no document, backs
	// nothing: nothing says which collection stated the edge, as nothing
	// says it of an entity with no list (scopedNode). Without one it is
	// shown as stored, as before.
	unplaced := func(id string) bool { return scope.Knowledge != nil && docOf[id] == "" }
	hidden := db.hiddenAmong(ctx, nil, docIDs)
	if len(hidden) == 0 && !unreadable && !slices.ContainsFunc(chunkIDs, unplaced) {
		return edges
	}
	hiddenChunk := func(id string) bool {
		_, gone := hidden[docOf[id]]
		return gone || unreadable || unplaced(id)
	}
	out := make([]*graph.GraphEdge, 0, len(edges))
	for _, edge := range edges {
		if edge.Properties == nil {
			out = append(out, edge)
			continue
		}
		chunks := stringsFromAny(edge.Properties["chunk_ids"])
		kept := slices.DeleteFunc(slices.Clone(chunks), hiddenChunk)
		src, _ := edge.Properties["source_chunk_id"].(string)
		doc, _ := edge.Properties["document_id"].(string)
		_, docGone := hidden[doc]
		srcGone := src != "" && hiddenChunk(src)
		if len(kept) == 0 && (src == "" || srcGone) && (doc == "" || docGone) && (len(chunks) > 0 || src != "" || doc != "") {
			continue
		}
		if len(kept) == len(chunks) && !srcGone && !docGone {
			out = append(out, edge)
			continue
		}
		copied := *edge
		copied.Properties = maps.Clone(edge.Properties)
		if len(kept) != len(chunks) {
			copied.Properties["chunk_ids"] = kept
		}
		if srcGone {
			delete(copied.Properties, "source_chunk_id")
		}
		if docGone {
			delete(copied.Properties, "document_id")
		}
		out = append(out, &copied)
	}
	return out
}

// GetNodes fetches graph nodes by ID. The chunk and document nodes of an
// expired document are left out, as get_chunks leaves its chunks out.
func (t *GraphRAGToolbox) GetNodes(ctx context.Context, req ToolGetNodesRequest) (*ToolGetNodesResponse, error) {
	nodes, err := t.db.graph.GetNodesBatch(ctx, req.NodeIDs)
	if err != nil {
		return nil, err
	}
	nodes, _ = t.db.withoutExpiredDocuments(ctx, nodes, nil, nil)
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
