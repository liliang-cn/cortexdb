package cortexdb

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

const (
	defaultKnowledgeMemoryTopKMemories    = 5
	defaultKnowledgeMemoryTopKKnowledge   = 4
	defaultKnowledgeMemoryMaxMemoryChars  = 1200
	defaultKnowledgeMemoryMaxFacts        = 6
	defaultKnowledgeMemoryMaxThemes       = 6
	defaultKnowledgeMemoryMaxSummaryChars = 320
)

// KnowledgeMemory exposes CortexDB as a higher-level memory, knowledge, and graph facade.
type KnowledgeMemory struct {
	db *DB
}

// KnowledgeMemory returns the high-level memory/knowledge facade over memory, knowledge, graph, and context packing APIs.
func (db *DB) KnowledgeMemory() *KnowledgeMemory {
	return &KnowledgeMemory{db: db}
}

// Remember stores one episodic memory item.
func (b *KnowledgeMemory) Remember(ctx context.Context, req KnowledgeMemoryRememberRequest) (*KnowledgeMemoryRememberResponse, error) {
	return b.db.SaveMemory(ctx, req)
}

// Recall retrieves a fused memory and knowledge view plus a packed context.
func (b *KnowledgeMemory) Recall(ctx context.Context, req KnowledgeMemoryRecallRequest) (*KnowledgeMemoryRecallResponse, error) {
	req = normalizeKnowledgeMemoryRecallRequest(req)
	query := resolveKnowledgeMemoryQuery(req.Query, req.Plan, req.EntityNames)
	if strings.TrimSpace(query) == "" {
		return nil, ErrEmptyText
	}

	resp := &KnowledgeMemoryRecallResponse{Query: query}

	var memoryResp *MemorySearchResponse
	if !req.DisableMemory {
		var err error
		memoryResp, err = b.db.SearchMemory(ctx, MemorySearchRequest{
			Query:            query,
			UserID:           req.UserID,
			SessionID:        req.SessionID,
			Scope:            req.Scope,
			Namespace:        req.Namespace,
			TopK:             req.TopKMemories,
			Keywords:         req.Keywords,
			AlternateQueries: req.AlternateQueries,
			RetrievalMode:    req.RetrievalMode,
			Plan:             req.Plan,
		})
		if err != nil {
			return nil, err
		}
		resp.MemoryPlan = memoryResp.Plan
		resp.MemoryDecision = memoryResp.Decision
		resp.Memories = memoryResp.Results
	}

	var knowledgeResp *KnowledgeSearchResponse
	if !req.DisableKnowledge {
		var err error
		knowledgeResp, err = b.db.SearchKnowledge(ctx, KnowledgeSearchRequest{
			Query:               query,
			Collection:          req.Collection,
			TopK:                req.TopKKnowledge,
			MaxHops:             req.MaxHops,
			MaxRelatedChunks:    req.MaxRelatedChunks,
			MaxContextChunks:    req.MaxContextChunks,
			MaxContextChars:     req.MaxContextChars,
			PerDocumentLimit:    req.PerDocumentLimit,
			EntityNames:         req.EntityNames,
			Keywords:            req.Keywords,
			AlternateQueries:    req.AlternateQueries,
			RetrievalMode:       req.RetrievalMode,
			DisableGraph:        req.DisableGraph,
			GraphLight:          req.GraphLight,
			MaxExpansionSeeds:   req.MaxExpansionSeeds,
			MaxTraversalNodes:   req.MaxTraversalNodes,
			MaxEntitiesPerChunk: req.MaxEntitiesPerChunk,
			Plan:                req.Plan,
		})
		if err != nil {
			return nil, err
		}
		resp.KnowledgePlan = knowledgeResp.Plan
		resp.KnowledgeDecision = knowledgeResp.Decision
		resp.Knowledge = knowledgeResp.Results
		resp.Chunks = knowledgeResp.Chunks
	}

	resp.Entities = KnowledgeMemoryCollectEntities(req.EntityNames, resp.Memories, resp.Knowledge, resp.Chunks)
	// Fold edge-accurate graph traversal into recall so relational questions are
	// answered from graph edges, not just lexical chunk text.
	graphSeeds := append(append([]string{}, req.EntityNames...), resp.Entities...)
	resp.GraphFacts = b.collectGraphFacts(ctx, query, graphSeeds)
	resp.ContextPack = buildKnowledgeMemoryContextPack(query, resp.Memories, knowledgeResp, resp.Entities, resp.GraphFacts, req.MaxMemoryItems, req.MaxMemoryChars)
	if len(resp.Entities) == 0 {
		resp.Entities = resp.ContextPack.Entities
	}
	return resp, nil
}

// BuildContextPack returns the same fused retrieval response as Recall, centered on the assembled context pack.
func (b *KnowledgeMemory) BuildContextPack(ctx context.Context, req KnowledgeMemoryBuildContextPackRequest) (*KnowledgeMemoryBuildContextPackResponse, error) {
	return b.Recall(ctx, req)
}

// PromoteToKnowledge promotes one or more memory records into durable knowledge.
func (b *KnowledgeMemory) PromoteToKnowledge(ctx context.Context, req KnowledgeMemoryPromoteToKnowledgeRequest) (*KnowledgeMemoryPromoteToKnowledgeResponse, error) {
	memoryIDs := orderedUniqueNonEmptyStrings(req.MemoryIDs)
	if len(memoryIDs) == 0 {
		return nil, fmt.Errorf("memory_ids are required")
	}

	memories := make([]MemoryRecord, 0, len(memoryIDs))
	contentParts := make([]string, 0, len(memoryIDs))
	sourceScopes := make([]string, 0, len(memoryIDs))
	for _, memoryID := range memoryIDs {
		memoryResp, err := b.db.GetMemory(ctx, MemoryGetRequest{MemoryID: memoryID})
		if err != nil {
			return nil, err
		}
		memories = append(memories, memoryResp.Memory)
		contentParts = append(contentParts, strings.TrimSpace(memoryResp.Memory.Content))
		sourceScopes = append(sourceScopes, strings.TrimSpace(memoryResp.Memory.Scope))
	}

	joinWith := req.JoinWith
	if joinWith == "" {
		joinWith = "\n\n"
	}

	metadata := cloneStringMap(req.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["knowledge_memory_source_memory_ids"] = strings.Join(memoryIDs, ",")
	metadata["knowledge_memory_source_memory_scopes"] = strings.Join(uniqueSortedStrings(sourceScopes), ",")

	knowledgeID := strings.TrimSpace(req.KnowledgeID)
	if knowledgeID == "" {
		knowledgeID = "knowledge:" + uuid.NewString()
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = compactTitle(strings.Join(contentParts, " "))
	}

	saveResp, err := b.db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID:  knowledgeID,
		Title:        title,
		Content:      strings.Join(contentParts, joinWith),
		SourceURL:    req.SourceURL,
		Author:       req.Author,
		Collection:   req.Collection,
		ChunkSize:    req.ChunkSize,
		ChunkOverlap: req.ChunkOverlap,
		Metadata:     metadata,
		Entities:     req.Entities,
		Relations:    req.Relations,
	})
	if err != nil {
		return nil, err
	}

	return &KnowledgeMemoryPromoteToKnowledgeResponse{
		Knowledge:       saveResp.Knowledge,
		Memories:        memories,
		DocumentNodeID:  saveResp.DocumentNodeID,
		EntityNodeIDs:   saveResp.EntityNodeIDs,
		RelationEdgeIDs: saveResp.RelationEdgeIDs,
	}, nil
}

// ExpandEntityContext expands graph context around entity nodes and builds a chunk context pack.
func (b *KnowledgeMemory) ExpandEntityContext(ctx context.Context, req KnowledgeMemoryExpandEntityContextRequest) (*KnowledgeMemoryExpandEntityContextResponse, error) {
	entityNodeIDs, entityNames, err := b.resolveEntityInputs(ctx, req.EntityIDs, req.EntityNames)
	if err != nil {
		return nil, err
	}
	if len(entityNodeIDs) == 0 {
		return nil, fmt.Errorf("entity_ids or entity_names are required")
	}
	if req.MaxHops <= 0 {
		req.MaxHops = 2
	}
	if req.TopKChunks <= 0 {
		req.TopKChunks = 8
	}

	expandResp, err := b.db.GraphRAGTools().ExpandGraph(ctx, ToolExpandGraphRequest{
		NodeIDs:   entityNodeIDs,
		MaxHops:   req.MaxHops,
		EdgeTypes: req.EdgeTypes,
		NodeTypes: req.NodeTypes,
		Limit:     req.Limit,
	})
	if err != nil {
		return nil, err
	}

	searchResp, err := b.db.GraphRAGTools().SearchChunksByEntities(ctx, ToolSearchChunksByEntitiesRequest{
		EntityNames: entityNames,
		TopK:        req.TopKChunks,
		MaxHops:     req.MaxHops,
	})
	if err != nil {
		return nil, err
	}

	chunkIDs := make([]string, 0, len(searchResp.Chunks))
	chunks := make([]GraphRAGChunkResult, 0, len(searchResp.Chunks))
	for _, chunk := range searchResp.Chunks {
		chunkIDs = append(chunkIDs, chunk.ID)
		chunks = append(chunks, GraphRAGChunkResult{
			ID:          chunk.ID,
			DocumentID:  chunk.DocumentID,
			Content:     chunk.Content,
			Score:       chunk.Score,
			BaseScore:   chunk.Score,
			RerankScore: chunk.Score,
			Entities:    chunk.Entities,
		})
	}

	contextSection := ""
	if len(chunkIDs) > 0 {
		contextResp, err := b.db.GraphRAGTools().BuildContext(ctx, ToolBuildContextRequest{
			ChunkIDs:            chunkIDs,
			MaxContextChunks:    req.MaxContextChunks,
			MaxContextChars:     req.MaxContextChars,
			PerDocumentLimit:    req.PerDocumentLimit,
			RetrievalMode:       req.RetrievalMode,
			DisableGraph:        req.DisableGraph,
			GraphLight:          req.GraphLight,
			MaxEntitiesPerChunk: req.MaxEntitiesPerChunk,
		})
		if err != nil {
			return nil, err
		}
		chunks = contextResp.Chunks
		contextSection = contextResp.Context
	}

	contextPack := KnowledgeMemoryContextPackFromSections(strings.Join(entityNames, " "), []KnowledgeMemoryContextSection{
		{
			Kind:      "graph",
			Title:     "Graph",
			Text:      contextSection,
			SourceIDs: orderedUniqueNonEmptyStrings(chunkIDs),
		},
	}, nil, nil, chunkIDs, entityNames)

	return &KnowledgeMemoryExpandEntityContextResponse{
		EntityNodeIDs: entityNodeIDs,
		Nodes:         expandResp.Nodes,
		Edges:         expandResp.Edges,
		Chunks:        chunks,
		ContextPack:   contextPack,
	}, nil
}

// Neighbors returns the graph neighbors around one node or entity.
func (b *KnowledgeMemory) Neighbors(ctx context.Context, req KnowledgeMemoryNeighborsRequest) (*KnowledgeMemoryNeighborsResponse, error) {
	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" {
		nodeID = resolveEntityNodeID("", req.EntityName)
	}
	if nodeID == "" {
		return nil, fmt.Errorf("node_id or entity_name is required")
	}

	// This traverses the graph directly rather than through ExpandGraph, so it
	// needs its own interface expansion to answer "neighbours of type
	// Facility" the same way.
	nodeTypes, err := b.db.expandOntologyTypeFilter(ctx, req.NodeTypes)
	if err != nil {
		return nil, err
	}
	neighbors, err := b.db.Graph().Neighbors(ctx, nodeID, graph.TraversalOptions{
		MaxDepth:  req.MaxDepth,
		EdgeTypes: req.EdgeTypes,
		NodeTypes: nodeTypes,
		Direction: req.Direction,
		Limit:     req.Limit,
	})
	if err != nil {
		return nil, err
	}

	return &KnowledgeMemoryNeighborsResponse{
		ResolvedNodeID: nodeID,
		Neighbors:      neighbors,
	}, nil
}

// ShortestPath resolves and traverses the shortest path between two nodes or entities.
func (b *KnowledgeMemory) ShortestPath(ctx context.Context, req KnowledgeMemoryShortestPathRequest) (*KnowledgeMemoryShortestPathResponse, error) {
	fromID := strings.TrimSpace(req.FromNodeID)
	if fromID == "" {
		fromID = resolveEntityNodeID("", req.FromEntityName)
	}
	toID := strings.TrimSpace(req.ToNodeID)
	if toID == "" {
		toID = resolveEntityNodeID("", req.ToEntityName)
	}
	if fromID == "" || toID == "" {
		return nil, fmt.Errorf("from_node_id/to_node_id or from_entity_name/to_entity_name are required")
	}

	path, err := b.db.Graph().ShortestPath(ctx, fromID, toID)
	if err != nil {
		return nil, err
	}

	return &KnowledgeMemoryShortestPathResponse{
		FromNodeID: fromID,
		ToNodeID:   toID,
		Path:       path,
	}, nil
}

// Reflect retrieves relevant context and synthesizes a structured reflection.
func (b *KnowledgeMemory) Reflect(ctx context.Context, req KnowledgeMemoryReflectRequest) (*KnowledgeMemoryReflectResponse, error) {
	recallResp, err := b.Recall(ctx, req.Recall)
	if err != nil {
		return nil, err
	}

	input := KnowledgeMemoryReflectInput{Recall: *recallResp}
	reflector := b.db.KnowledgeMemoryReflector
	var reflection *KnowledgeMemoryReflection
	if reflector != nil {
		reflection, err = reflector.Reflect(ctx, req, input)
		if err != nil {
			return nil, err
		}
	} else {
		reflection = deterministicKnowledgeMemoryReflection(req, input)
	}
	if reflection == nil {
		return nil, fmt.Errorf("KnowledgeMemory reflector returned nil reflection")
	}

	reflection.SourceMemoryIDs = uniqueSortedStrings(append(reflection.SourceMemoryIDs, recallResp.ContextPack.MemoryIDs...))
	reflection.SourceKnowledgeIDs = uniqueSortedStrings(append(reflection.SourceKnowledgeIDs, recallResp.ContextPack.KnowledgeIDs...))
	reflection.SourceChunkIDs = uniqueSortedStrings(append(reflection.SourceChunkIDs, recallResp.ContextPack.ChunkIDs...))
	reflection.Entities = uniqueSortedStrings(append(reflection.Entities, recallResp.Entities...))
	if reflection.ContextPack.Text == "" {
		reflection.ContextPack = recallResp.ContextPack
	}

	return &KnowledgeMemoryReflectResponse{
		Reflection: *reflection,
		Recall:     *recallResp,
	}, nil
}

// Consolidate reflects over relevant context, stores a summary memory, and can optionally promote it to knowledge.
func (b *KnowledgeMemory) Consolidate(ctx context.Context, req KnowledgeMemoryConsolidateRequest) (*KnowledgeMemoryConsolidateResponse, error) {
	reflectResp, err := b.Reflect(ctx, req.Reflect)
	if err != nil {
		return nil, err
	}
	summary := strings.TrimSpace(reflectResp.Reflection.Summary)
	if summary == "" {
		return nil, fmt.Errorf("reflection summary is empty")
	}

	memoryID := strings.TrimSpace(req.MemoryID)
	if memoryID == "" {
		memoryID = uuid.NewString()
	}

	metadata := cloneAnyMap(req.Metadata)
	metadata["knowledge_memory_summary"] = true
	metadata["knowledge_memory_themes"] = append([]string(nil), reflectResp.Reflection.Themes...)
	metadata["knowledge_memory_entities"] = append([]string(nil), reflectResp.Reflection.Entities...)
	metadata["knowledge_memory_source_memory_ids"] = append([]string(nil), reflectResp.Reflection.SourceMemoryIDs...)
	metadata["knowledge_memory_source_knowledge_ids"] = append([]string(nil), reflectResp.Reflection.SourceKnowledgeIDs...)
	metadata["knowledge_memory_source_chunk_ids"] = append([]string(nil), reflectResp.Reflection.SourceChunkIDs...)

	saveResp, err := b.Remember(ctx, KnowledgeMemoryRememberRequest{
		MemoryID:   memoryID,
		UserID:     firstNonEmpty(req.UserID, req.Reflect.Recall.UserID),
		SessionID:  firstNonEmpty(req.SessionID, req.Reflect.Recall.SessionID),
		Scope:      firstNonEmpty(req.Scope, req.Reflect.Recall.Scope),
		Namespace:  firstNonEmpty(req.Namespace, req.Reflect.Recall.Namespace),
		Role:       firstNonEmpty(req.Role, "summary"),
		Content:    summary,
		Metadata:   metadata,
		Importance: req.Importance,
		TTLSeconds: req.TTLSeconds,
	})
	if err != nil {
		return nil, err
	}

	var knowledge *KnowledgeRecord
	if req.PromoteToKnowledge || req.Promotion != nil {
		promotionReq := KnowledgeMemoryPromoteToKnowledgeRequest{}
		if req.Promotion != nil {
			promotionReq = *req.Promotion
		}
		if len(promotionReq.MemoryIDs) == 0 {
			promotionReq.MemoryIDs = []string{saveResp.Memory.ID}
		}
		if strings.TrimSpace(promotionReq.Title) == "" {
			promotionReq.Title = compactTitle(summary)
		}
		promoteResp, err := b.PromoteToKnowledge(ctx, promotionReq)
		if err != nil {
			return nil, err
		}
		knowledge = &promoteResp.Knowledge
	}

	return &KnowledgeMemoryConsolidateResponse{
		Reflection: reflectResp.Reflection,
		Recall:     reflectResp.Recall,
		Memory:     saveResp.Memory,
		Knowledge:  knowledge,
	}, nil
}

func normalizeKnowledgeMemoryRecallRequest(req KnowledgeMemoryRecallRequest) KnowledgeMemoryRecallRequest {
	if req.TopKMemories <= 0 {
		req.TopKMemories = defaultKnowledgeMemoryTopKMemories
	}
	if req.TopKKnowledge <= 0 {
		req.TopKKnowledge = defaultKnowledgeMemoryTopKKnowledge
	}
	if req.MaxMemoryItems <= 0 {
		req.MaxMemoryItems = req.TopKMemories
	}
	if req.MaxMemoryChars <= 0 {
		req.MaxMemoryChars = defaultKnowledgeMemoryMaxMemoryChars
	}
	return req
}

func resolveKnowledgeMemoryQuery(query string, plan *RetrievalPlan, entityNames []string) string {
	query = strings.TrimSpace(query)
	if query != "" {
		return query
	}
	if plan != nil && strings.TrimSpace(plan.Query) != "" {
		return strings.TrimSpace(plan.Query)
	}
	if len(entityNames) > 0 {
		return strings.Join(uniqueSortedStrings(entityNames), " ")
	}
	return ""
}

func buildKnowledgeMemoryContextPack(query string, memories []MemorySearchHit, knowledgeResp *KnowledgeSearchResponse, entities []string, graphFacts []KnowledgeMemoryGraphFact, maxMemoryItems, maxMemoryChars int) KnowledgeMemoryContextPack {
	sections := make([]KnowledgeMemoryContextSection, 0, 4)
	memoryIDs := make([]string, 0, len(memories))
	knowledgeIDs := make([]string, 0)
	chunkIDs := make([]string, 0)

	// Graph facts go first: when present they are usually the precise, edge-
	// accurate answer to a relational question.
	if gf := renderGraphFacts(graphFacts); gf != "" {
		sections = append(sections, KnowledgeMemoryContextSection{
			Kind:  "graph_facts",
			Title: "Graph facts",
			Text:  gf,
		})
	}

	memoryText := buildKnowledgeMemoryMemoryContext(memories, maxMemoryItems, maxMemoryChars)
	for _, hit := range memories {
		memoryIDs = append(memoryIDs, hit.Memory.ID)
	}
	if memoryText != "" {
		sections = append(sections, KnowledgeMemoryContextSection{
			Kind:      "memories",
			Title:     "Memories",
			Text:      memoryText,
			SourceIDs: orderedUniqueNonEmptyStrings(memoryIDs),
		})
	}

	if knowledgeResp != nil {
		for _, hit := range knowledgeResp.Results {
			knowledgeIDs = append(knowledgeIDs, hit.KnowledgeID)
		}
		for _, chunk := range knowledgeResp.Chunks {
			chunkIDs = append(chunkIDs, chunk.ID)
		}
		if strings.TrimSpace(knowledgeResp.Context) != "" {
			sections = append(sections, KnowledgeMemoryContextSection{
				Kind:      "knowledge",
				Title:     "Knowledge",
				Text:      knowledgeResp.Context,
				SourceIDs: orderedUniqueNonEmptyStrings(chunkIDs),
			})
		}
	}

	return KnowledgeMemoryContextPackFromSections(query, sections, memoryIDs, knowledgeIDs, chunkIDs, entities)
}

func KnowledgeMemoryContextPackFromSections(query string, sections []KnowledgeMemoryContextSection, memoryIDs, knowledgeIDs, chunkIDs, entities []string) KnowledgeMemoryContextPack {
	filtered := make([]KnowledgeMemoryContextSection, 0, len(sections))
	parts := make([]string, 0, len(sections))
	for _, section := range sections {
		if strings.TrimSpace(section.Text) == "" {
			continue
		}
		section.SourceIDs = orderedUniqueNonEmptyStrings(section.SourceIDs)
		filtered = append(filtered, section)
		parts = append(parts, fmt.Sprintf("[%s]\n%s", strings.ToUpper(section.Kind), strings.TrimSpace(section.Text)))
	}

	return KnowledgeMemoryContextPack{
		Query:        query,
		Text:         strings.TrimSpace(strings.Join(parts, "\n\n")),
		Sections:     filtered,
		MemoryIDs:    orderedUniqueNonEmptyStrings(memoryIDs),
		KnowledgeIDs: orderedUniqueNonEmptyStrings(knowledgeIDs),
		ChunkIDs:     orderedUniqueNonEmptyStrings(chunkIDs),
		Entities:     uniqueSortedStrings(entities),
	}
}

func buildKnowledgeMemoryMemoryContext(memories []MemorySearchHit, maxItems, maxChars int) string {
	if len(memories) == 0 {
		return ""
	}
	if maxItems <= 0 || maxItems > len(memories) {
		maxItems = len(memories)
	}
	lines := make([]string, 0, maxItems)
	charsUsed := 0
	for _, hit := range memories[:maxItems] {
		line := fmt.Sprintf("[%s] %s", hit.Memory.ID, strings.TrimSpace(hit.Memory.Content))
		if maxChars > 0 && charsUsed+len(line) > maxChars {
			remaining := maxChars - charsUsed
			if remaining <= 0 {
				break
			}
			line = clipString(line, remaining)
		}
		lines = append(lines, line)
		charsUsed += len(line)
		if maxChars > 0 && charsUsed >= maxChars {
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (b *KnowledgeMemory) resolveEntityInputs(ctx context.Context, entityIDs, entityNames []string) ([]string, []string, error) {
	nodeIDs := orderedUniqueNonEmptyStrings(entityIDs)
	names := orderedUniqueNonEmptyStrings(entityNames)

	for _, name := range names {
		nodeIDs = append(nodeIDs, resolveEntityNodeID("", name))
	}
	nodeIDs = orderedUniqueNonEmptyStrings(nodeIDs)
	if len(nodeIDs) == 0 {
		return nil, nil, nil
	}

	if len(names) == 0 {
		nodes, err := b.db.Graph().GetNodesBatch(ctx, nodeIDs)
		if err != nil {
			return nil, nil, err
		}
		for _, node := range nodes {
			if strings.TrimSpace(node.Content) != "" {
				names = append(names, node.Content)
				continue
			}
			if node.NodeType == "entity" {
				names = append(names, strings.TrimPrefix(node.ID, EntityNodeIDPrefix))
			}
		}
	}

	return nodeIDs, orderedUniqueNonEmptyStrings(names), nil
}

func KnowledgeMemoryCollectEntities(seed []string, memories []MemorySearchHit, knowledge []KnowledgeSearchHit, chunks []GraphRAGChunkResult) []string {
	entities := append([]string(nil), seed...)
	for _, hit := range memories {
		entities = append(entities, extractEntityNames(extractTitleEntities(hit.Memory.Content))...)
	}
	for _, hit := range knowledge {
		entities = append(entities, hit.Entities...)
		entities = append(entities, extractEntityNames(extractTitleEntities(hit.Title))...)
		entities = append(entities, extractEntityNames(extractTitleEntities(hit.Snippet))...)
	}
	for _, chunk := range chunks {
		entities = append(entities, chunk.Entities...)
	}
	return uniqueSortedStrings(entities)
}

func deterministicKnowledgeMemoryReflection(req KnowledgeMemoryReflectRequest, input KnowledgeMemoryReflectInput) *KnowledgeMemoryReflection {
	maxFacts := req.MaxFacts
	if maxFacts <= 0 {
		maxFacts = defaultKnowledgeMemoryMaxFacts
	}
	maxThemes := req.MaxThemes
	if maxThemes <= 0 {
		maxThemes = defaultKnowledgeMemoryMaxThemes
	}
	maxSummaryChars := req.MaxSummaryChars
	if maxSummaryChars <= 0 {
		maxSummaryChars = defaultKnowledgeMemoryMaxSummaryChars
	}

	facts := make([]string, 0, maxFacts)
	sourceMemoryIDs := make([]string, 0, len(input.Recall.Memories))
	for _, hit := range input.Recall.Memories {
		if len(facts) >= maxFacts {
			break
		}
		facts = append(facts, compactSnippet(hit.Memory.Content))
		sourceMemoryIDs = append(sourceMemoryIDs, hit.Memory.ID)
	}

	sourceKnowledgeIDs := make([]string, 0, len(input.Recall.Knowledge))
	for _, hit := range input.Recall.Knowledge {
		if len(facts) >= maxFacts {
			break
		}
		fact := firstNonEmpty(hit.Snippet, hit.Title)
		if strings.TrimSpace(fact) != "" {
			facts = append(facts, compactSnippet(fact))
		}
		sourceKnowledgeIDs = append(sourceKnowledgeIDs, hit.KnowledgeID)
	}

	if len(facts) == 0 && strings.TrimSpace(input.Recall.ContextPack.Text) != "" {
		facts = append(facts, compactSnippet(input.Recall.ContextPack.Text))
	}

	summary := strings.Join(facts, " ")
	summary = clipString(strings.TrimSpace(summary), maxSummaryChars)
	themes := KnowledgeMemoryThemes(req, input.Recall, maxThemes)

	return &KnowledgeMemoryReflection{
		Summary:            summary,
		Themes:             themes,
		Entities:           uniqueSortedStrings(input.Recall.Entities),
		Facts:              facts,
		SourceMemoryIDs:    orderedUniqueNonEmptyStrings(sourceMemoryIDs),
		SourceKnowledgeIDs: orderedUniqueNonEmptyStrings(sourceKnowledgeIDs),
		SourceChunkIDs:     orderedUniqueNonEmptyStrings(input.Recall.ContextPack.ChunkIDs),
		ContextPack:        input.Recall.ContextPack,
	}
}

func KnowledgeMemoryThemes(req KnowledgeMemoryReflectRequest, recall KnowledgeMemoryRecallResponse, maxThemes int) []string {
	themes := append([]string(nil), recall.Entities...)
	themes = append(themes, lexicalQueryKeywords(req.Recall.Query)...)
	if req.Instructions != "" {
		themes = append(themes, lexicalQueryKeywords(req.Instructions)...)
	}
	themes = orderedUniqueNonEmptyStrings(themes)
	if len(themes) > maxThemes {
		themes = themes[:maxThemes]
	}
	return themes
}

func orderedUniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func compactTitle(text string) string {
	title := compactSnippet(text)
	title = clipString(title, 96)
	title = strings.TrimSpace(title)
	if title == "" {
		return "Knowledge"
	}
	return title
}

func clipString(text string, max int) string {
	text = strings.TrimSpace(text)
	if max <= 0 || len(text) <= max {
		return text
	}
	if max <= 3 {
		return text[:max]
	}
	return strings.TrimSpace(text[:max-3]) + "..."
}
