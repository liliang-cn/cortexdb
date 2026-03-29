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
	if _, err := t.db.graph.UpsertNodesBatch(ctx, chunkNodes); err != nil {
		return nil, fmt.Errorf("upsert chunk nodes: %w", err)
	}
	if _, err := t.db.graph.UpsertEdgesBatch(ctx, edges); err != nil {
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
	if err := t.db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
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
		entityID := resolveEntityNodeID(entity.ID, entity.Name)
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
			NodeType:   firstNonEmpty(entity.Type, "entity"),
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

	if len(nodes) > 0 {
		if _, err := t.db.graph.UpsertNodesBatch(ctx, nodes); err != nil {
			return nil, fmt.Errorf("upsert entity nodes: %w", err)
		}
	}
	if len(edges) > 0 {
		if _, err := t.db.graph.UpsertEdgesBatch(ctx, edges); err != nil {
			return nil, fmt.Errorf("upsert mention edges: %w", err)
		}
	}

	return &ToolUpsertEntitiesResponse{
		EntityNodeIDs:    entityIDs,
		MentionEdgeCount: len(edges),
	}, nil
}

// UpsertRelations writes relation edges between entities.
func (t *GraphRAGToolbox) UpsertRelations(ctx context.Context, req ToolUpsertRelationsRequest) (*ToolUpsertRelationsResponse, error) {
	if len(req.Relations) == 0 {
		return &ToolUpsertRelationsResponse{}, nil
	}
	if err := t.db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}
	if err := t.db.validateRelationInputs(ctx, req.Relations); err != nil {
		return nil, err
	}

	edges := make([]*graph.GraphEdge, 0, len(req.Relations))
	edgeIDs := make([]string, 0, len(req.Relations))
	for i, rel := range req.Relations {
		fromID := resolveEntityNodeID("", rel.From)
		toID := resolveEntityNodeID("", rel.To)
		if fromID == "" || toID == "" {
			continue
		}
		edgeType := firstNonEmpty(rel.Type, "related_to")
		edgeID := fmt.Sprintf("edge:relation:%s:%s:%s:%d", fromID, toID, edgeType, i)
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
		edges = append(edges, &graph.GraphEdge{
			ID:         edgeID,
			FromNodeID: fromID,
			ToNodeID:   toID,
			EdgeType:   edgeType,
			Weight:     weight,
			Properties: properties,
		})
	}

	if len(edges) > 0 {
		if _, err := t.db.graph.UpsertEdgesBatch(ctx, edges); err != nil {
			return nil, fmt.Errorf("upsert relation edges: %w", err)
		}
	}

	return &ToolUpsertRelationsResponse{EdgeIDs: edgeIDs}, nil
}
