package cortexdb

import (
	"context"
	"fmt"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// IngestDocument stores lexical chunks and graph nodes without requiring an embedder.
func (t *GraphRAGToolbox) IngestDocument(ctx context.Context, req ToolIngestDocumentRequest) (*ToolIngestDocumentResponse, error) {
	if req.DocumentID == "" {
		return nil, fmt.Errorf("document_id is required")
	}
	if strings.TrimSpace(req.Content) == "" {
		return nil, ErrEmptyText
	}

	ingestOpts := GraphRAGIngestOptions{
		Collection:   req.Collection,
		ChunkSize:    req.ChunkSize,
		ChunkOverlap: req.ChunkOverlap,
	}
	applyGraphRAGIngestDefaults(&ingestOpts)

	if err := t.db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}

	vectorDim, err := t.lexicalVectorDim(ctx, ingestOpts.Collection)
	if err != nil {
		return nil, err
	}
	if err := t.ensureLexicalCollection(ctx, ingestOpts.Collection, vectorDim); err != nil {
		return nil, err
	}

	chunks := splitGraphRAGText(req.Content, ingestOpts.ChunkSize, ingestOpts.ChunkOverlap)
	if len(chunks) == 0 {
		return nil, ErrEmptyText
	}

	docRecord := &core.Document{
		ID:      req.DocumentID,
		Title:   req.Title,
		Content: req.Content,
		Version: 1,
	}
	if err := t.db.upsertGraphRAGDocumentRecord(ctx, docRecord); err != nil {
		return nil, err
	}

	docNodeID := graphDocumentNodeID(req.DocumentID)
	docNode := &graph.GraphNode{
		ID:       docNodeID,
		Vector:   lexicalVectorForText(firstNonEmpty(req.Title, req.Content), vectorDim),
		Content:  firstNonEmpty(req.Title, req.Content),
		NodeType: "document",
		Properties: map[string]interface{}{
			"document_id": req.DocumentID,
			"title":       req.Title,
		},
	}
	if err := t.db.graph.UpsertNode(ctx, docNode); err != nil {
		return nil, fmt.Errorf("upsert document node: %w", err)
	}

	embeddings := make([]*core.Embedding, 0, len(chunks))
	chunkNodes := make([]*graph.GraphNode, 0, len(chunks))
	edges := make([]*graph.GraphEdge, 0, len(chunks)*2)
	chunkIDs := make([]string, 0, len(chunks))

	for i, chunk := range chunks {
		chunkID := graphChunkNodeID(req.DocumentID, i)
		chunkIDs = append(chunkIDs, chunkID)

		metadata := map[string]string{
			"graph_kind":  "chunk",
			"document_id": req.DocumentID,
			"chunk_index": fmt.Sprintf("%d", i),
			"title":       req.Title,
		}
		for k, v := range req.Metadata {
			metadata[k] = v
		}

		chunkVector := lexicalVectorForText(chunk, vectorDim)
		embeddings = append(embeddings, &core.Embedding{
			ID:         chunkID,
			Collection: ingestOpts.Collection,
			Vector:     chunkVector,
			Content:    chunk,
			DocID:      req.DocumentID,
			Metadata:   metadata,
		})
		chunkNodes = append(chunkNodes, &graph.GraphNode{
			ID:       chunkID,
			Vector:   chunkVector,
			Content:  chunk,
			NodeType: "chunk",
			Properties: map[string]interface{}{
				"document_id": req.DocumentID,
				"chunk_index": i,
				"title":       req.Title,
			},
		})
		edges = append(edges, &graph.GraphEdge{
			ID:         fmt.Sprintf("edge:doc_chunk:%s:%d", req.DocumentID, i),
			FromNodeID: docNodeID,
			ToNodeID:   chunkID,
			EdgeType:   "has_chunk",
			Weight:     1.0,
		})
		if i > 0 {
			edges = append(edges, &graph.GraphEdge{
				ID:         fmt.Sprintf("edge:chunk_next:%s:%d", req.DocumentID, i),
				FromNodeID: graphChunkNodeID(req.DocumentID, i-1),
				ToNodeID:   chunkID,
				EdgeType:   "next",
				Weight:     1.0,
			})
		}
	}

	if err := t.db.store.UpsertBatch(ctx, embeddings); err != nil {
		return nil, fmt.Errorf("upsert lexical chunks: %w", err)
	}
	// Both batches report rejected rows in their result rather than in err. The
	// result used to be discarded here, so an ingest whose every edge was rejected
	// still returned the chunk ids and reported success.
	chunkNodeResult, err := t.db.graph.UpsertNodesBatch(ctx, chunkNodes)
	if err != nil {
		return nil, fmt.Errorf("upsert chunk nodes: %w", err)
	}
	if err := chunkNodeResult.Err(); err != nil {
		return nil, fmt.Errorf("upsert chunk nodes: %w", err)
	}
	edgeResult, err := t.db.graph.UpsertEdgesBatch(ctx, edges)
	if err != nil {
		return nil, fmt.Errorf("upsert chunk edges: %w", err)
	}
	if err := edgeResult.Err(); err != nil {
		return nil, fmt.Errorf("upsert chunk edges: %w", err)
	}

	return &ToolIngestDocumentResponse{
		DocumentNodeID: docNodeID,
		ChunkNodeIDs:   chunkIDs,
		Collection:     ingestOpts.Collection,
	}, nil
}

// UpsertEntities writes entity nodes and mention edges for caller-supplied structured extraction.
func (t *GraphRAGToolbox) UpsertEntities(ctx context.Context, req ToolUpsertEntitiesRequest) (*ToolUpsertEntitiesResponse, error) {
	if len(req.Entities) == 0 {
		return &ToolUpsertEntitiesResponse{}, nil
	}
	if err := t.db.guardStrictActions(ctx); err != nil {
		return nil, err
	}
	if err := t.db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}
	compiled, err := t.db.activeCompiledOntology(ctx)
	if err != nil {
		return nil, err
	}
	if err := t.db.validateEntityInputs(ctx, req.Entities); err != nil {
		return nil, err
	}

	vectorDim, err := t.lexicalVectorDim(ctx, defaultGraphRAGCollection)
	if err != nil {
		return nil, err
	}

	nodes := make([]*graph.GraphNode, 0, len(req.Entities))
	edges := make([]*graph.GraphEdge, 0)
	entityIDs := make([]string, 0, len(req.Entities))

	for _, entity := range req.Entities {
		if strings.TrimSpace(entity.Name) == "" && strings.TrimSpace(entity.ID) == "" {
			continue
		}
		entityID, err := ontologyEntityNodeID(compiled, entity)
		if err != nil {
			return nil, err
		}
		entityIDs = append(entityIDs, entityID)

		properties := map[string]interface{}{}
		if entity.Name != "" {
			properties["name"] = entity.Name
		}
		if entity.Description != "" {
			properties["description"] = entity.Description
		}
		for k, v := range entity.Metadata {
			properties[k] = v
		}

		nodes = append(nodes, &graph.GraphNode{
			ID:         entityID,
			Vector:     lexicalVectorForText(strings.TrimSpace(entity.Name+" "+entity.Description), vectorDim),
			Content:    firstNonEmpty(entity.Name, entity.ID),
			NodeType:   ontologyCanonicalNodeType(compiled, firstNonEmpty(entity.Type, "entity")),
			Properties: properties,
		})

		for _, chunkID := range entity.ChunkIDs {
			if chunkID == "" {
				continue
			}
			edges = append(edges, &graph.GraphEdge{
				ID:         fmt.Sprintf("edge:mention:%s:%s", chunkID, entityID),
				FromNodeID: chunkID,
				ToNodeID:   entityID,
				EdgeType:   "mentions",
				Weight:     1.0,
			})
		}
	}

	// Provenance, before anything is written. The entity nodes carry the union
	// of every document that asserted them, and the mention edges get their
	// chunk endpoints created if the caller never wrote them as graph nodes.
	// Without the first, no query can answer "where did this entity come from"
	// and no purge can remove a document's entities without guessing. Without
	// the second, every mention edge from a caller that embeds its chunks
	// outside the graph (the normal shape for an external ingest pipeline) is
	// rejected by the foreign key — for a long time silently, so the graph had
	// entities but no record of what mentioned them.
	if req.DocumentID != "" && len(nodes) > 0 {
		if err := t.mergeEntitySourceDocuments(ctx, nodes, req.DocumentID); err != nil {
			return nil, err
		}
	}
	stubs, err := t.missingChunkStubs(ctx, edges, req.DocumentID, vectorDim)
	if err != nil {
		return nil, err
	}
	nodes = append(nodes, stubs...)

	if len(nodes) > 0 {
		nodeResult, err := t.db.graph.UpsertNodesBatch(ctx, nodes)
		if err != nil {
			return nil, fmt.Errorf("upsert entity nodes: %w", err)
		}
		if err := nodeResult.Err(); err != nil {
			return nil, fmt.Errorf("upsert entity nodes: %w", err)
		}
	}
	if len(edges) > 0 {
		// A mention edge whose chunk was never written as a node is rejected by the
		// foreign key. Reported, because the alternative — the count of edges the
		// caller asked for, minus the ones the store kept — was indistinguishable
		// from a graph that grew.
		edgeResult, err := t.db.graph.UpsertEdgesBatch(ctx, edges)
		if err != nil {
			return nil, fmt.Errorf("upsert mention edges: %w", err)
		}
		if err := edgeResult.Err(); err != nil {
			return nil, fmt.Errorf("upsert mention edges: %w", err)
		}
	}

	return &ToolUpsertEntitiesResponse{
		EntityNodeIDs:    entityIDs,
		MentionEdgeCount: len(edges),
	}, nil
}

// UpsertRelations writes relation edges between entities.
// relationEdgeID identifies an edge by what it connects and what it means.
//
// It used to end in the relation's index within the request, so the identity of an edge depended on
// where it happened to sit in the array: a caller that re-sent the same graph — a learning trace pushed
// again each turn, a document re-indexed — wrote a new row every time. One store had 102,855 edges of
// which 61,197 were distinct, a single edge repeated 292 times, and traversal weighted by however often
// the caller had happened to repeat itself.
//
// The document id stays part of the identity when there is one: a relation read from two sources is one
// edge per source, so the second does not overwrite the first's provenance. Callers with no document —
// a learning trace, an inference rule — collapse onto one edge, which is what they meant.
func relationEdgeID(fromID, toID, edgeType, documentID string) string {
	if documentID == "" {
		return fmt.Sprintf("edge:relation:%s:%s:%s", fromID, toID, edgeType)
	}
	return fmt.Sprintf("edge:relation:%s:%s:%s:doc:%s", fromID, toID, edgeType, documentID)
}

// mergeRelationProperties folds a repeat of the same edge into the one already collected: list-valued
// provenance is unioned, everything else is overlaid.
func mergeRelationProperties(into, from map[string]interface{}) {
	for key, value := range from {
		switch key {
		case "chunk_ids", "support_edge_ids":
			into[key] = unionStrings(into[key], value)
		default:
			into[key] = value
		}
	}
}

// unionStrings appends the strings of b to those of a, keeping order and dropping repeats.
func unionStrings(a, b interface{}) []string {
	merged := make([]string, 0)
	seen := make(map[string]struct{})
	for _, value := range []interface{}{a, b} {
		for _, item := range toStringSlice(value) {
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			merged = append(merged, item)
		}
	}
	return merged
}

func toStringSlice(value interface{}) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []string:
		return typed
	case []interface{}:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				items = append(items, text)
			}
		}
		return items
	default:
		return nil
	}
}

func (t *GraphRAGToolbox) UpsertRelations(ctx context.Context, req ToolUpsertRelationsRequest) (*ToolUpsertRelationsResponse, error) {
	if len(req.Relations) == 0 {
		return &ToolUpsertRelationsResponse{}, nil
	}
	if err := t.db.guardStrictActions(ctx); err != nil {
		return nil, err
	}
	if err := t.db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}
	compiled, err := t.db.activeCompiledOntology(ctx)
	if err != nil {
		return nil, err
	}
	if err := t.db.validateRelationInputs(ctx, req.Relations); err != nil {
		return nil, err
	}

	edges := make([]*graph.GraphEdge, 0, len(req.Relations))
	edgeIDs := make([]string, 0, len(req.Relations))
	byID := make(map[string]*graph.GraphEdge, len(req.Relations))
	for _, rel := range req.Relations {
		if strings.TrimSpace(rel.From) == "" || strings.TrimSpace(rel.To) == "" {
			continue
		}
		fromID, err := t.db.ontologyRelationEndpointNodeID(ctx, compiled, rel.From)
		if err != nil {
			return nil, err
		}
		toID, err := t.db.ontologyRelationEndpointNodeID(ctx, compiled, rel.To)
		if err != nil {
			return nil, err
		}
		if fromID == "" || toID == "" {
			continue
		}
		edgeType := firstNonEmpty(rel.Type, "related_to")
		edgeID := relationEdgeID(fromID, toID, edgeType, req.DocumentID)
		edgeIDs = append(edgeIDs, edgeID)

		properties := map[string]interface{}{}
		if req.DocumentID != "" {
			properties["document_id"] = req.DocumentID
		}
		if len(rel.ChunkIDs) > 0 {
			properties["chunk_ids"] = rel.ChunkIDs
		}
		if rel.Inferred {
			properties["inferred"] = true
		}
		if rel.Provenance != "" {
			properties["provenance"] = rel.Provenance
		}
		if rel.RuleID != "" {
			properties["rule_id"] = rel.RuleID
		}
		if len(rel.SupportEdgeIDs) > 0 {
			properties["support_edge_ids"] = rel.SupportEdgeIDs
		}
		for k, v := range rel.Metadata {
			properties[k] = v
		}

		weight := rel.Weight
		if weight == 0 {
			weight = 1.0
		}
		// The same edge twice in one request is one edge. Merged rather than appended, because the
		// batch writer executes them in order and the second would otherwise overwrite the first's
		// chunk ids — losing provenance without saying so.
		if existing, ok := byID[edgeID]; ok {
			mergeRelationProperties(existing.Properties, properties)
			existing.Weight = weight
			continue
		}
		edge := &graph.GraphEdge{
			ID:         edgeID,
			FromNodeID: fromID,
			ToNodeID:   toID,
			EdgeType:   edgeType,
			Weight:     weight,
			Properties: properties,
		}
		byID[edgeID] = edge
		edges = append(edges, edge)
	}

	response := &ToolUpsertRelationsResponse{EdgeIDs: edgeIDs}
	if len(edges) > 0 {
		result, err := t.db.graph.UpsertEdgesBatch(ctx, edges)
		if err != nil {
			return nil, fmt.Errorf("upsert relation edges: %w", err)
		}
		// The batch result used to be discarded. An edge the store rejects — most often because an
		// endpoint was never created as a node — was then dropped in silence, and the call returned the
		// ids of edges that are not in the graph. Nothing downstream could tell an empty graph from a
		// successful ingest.
		response.Written = result.SuccessCount
		if result.FailedCount > 0 {
			response.Rejected = rejectedEdgeMessages(result.Errors)
			if result.SuccessCount == 0 {
				return nil, fmt.Errorf("no relation could be written (%d rejected): %v",
					result.FailedCount, response.Rejected)
			}
		}
	}

	return response, nil
}

// mergeEntitySourceDocuments unions documentID into each entity node's
// source_document_ids property, keeping what other documents already recorded.
//
// A read-merge-write rather than a plain property, because the node upsert
// replaces properties wholesale: a re-extraction from document B would
// otherwise erase document A's claim to a shared entity — and with it the only
// record that lets a purge of A know to leave the entity alone.
func (t *GraphRAGToolbox) mergeEntitySourceDocuments(ctx context.Context, nodes []*graph.GraphNode, documentID string) error {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	existing, err := t.db.graph.GetNodesBatch(ctx, ids)
	if err != nil {
		return fmt.Errorf("load entity provenance: %w", err)
	}
	prior := make(map[string][]string, len(existing))
	for _, node := range existing {
		if node == nil || node.Properties == nil {
			continue
		}
		prior[node.ID] = toStringSlice(node.Properties["source_document_ids"])
	}
	for _, node := range nodes {
		if node.Properties == nil {
			node.Properties = map[string]interface{}{}
		}
		node.Properties["source_document_ids"] = unionStrings(prior[node.ID], []string{documentID})
	}
	return nil
}

// missingChunkStubs returns a stub graph node for every mention-edge chunk that
// does not exist as a node yet.
//
// Callers that ingest through IngestDocument have real chunk nodes and get no
// stubs. Callers that embed their chunks outside the graph — an external
// pipeline with its own chunker — reference chunk ids the graph has never seen,
// and every mention edge they ask for dies on the foreign key. A stub carries
// no content (the text lives in the caller's embedding store, under the same
// id); it exists so the edge can, and so a purge by document can find it.
func (t *GraphRAGToolbox) missingChunkStubs(ctx context.Context, edges []*graph.GraphEdge, documentID string, vectorDim int) ([]*graph.GraphNode, error) {
	ids := make([]string, 0, len(edges))
	seen := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		if edge.EdgeType != "mentions" {
			continue
		}
		if _, ok := seen[edge.FromNodeID]; ok {
			continue
		}
		seen[edge.FromNodeID] = struct{}{}
		ids = append(ids, edge.FromNodeID)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	existing, err := t.db.graph.GetNodesBatch(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("check chunk nodes: %w", err)
	}
	have := make(map[string]struct{}, len(existing))
	for _, node := range existing {
		if node != nil {
			have[node.ID] = struct{}{}
		}
	}
	stubs := make([]*graph.GraphNode, 0)
	for _, id := range ids {
		if _, ok := have[id]; ok {
			continue
		}
		properties := map[string]interface{}{"stub": true}
		if documentID != "" {
			properties["document_id"] = documentID
		}
		stubs = append(stubs, &graph.GraphNode{
			ID: id,
			// The store requires a vector; derived from the id so it is
			// deterministic, and never expected to win a similarity search.
			Vector:     lexicalVectorForText(id, vectorDim),
			NodeType:   "chunk",
			Properties: properties,
		})
	}
	return stubs, nil
}

// rejectedEdgeMessages turns the batch's errors into something a caller can read, capped so one broken
// ingest of a whole book does not answer with ten thousand lines.
func rejectedEdgeMessages(errs []error) []string {
	const maxReported = 20
	messages := make([]string, 0, len(errs))
	for i, err := range errs {
		if i == maxReported {
			messages = append(messages, fmt.Sprintf("... and %d more", len(errs)-maxReported))
			break
		}
		messages = append(messages, err.Error())
	}
	return messages
}
