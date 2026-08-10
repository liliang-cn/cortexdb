package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

type knowledgeMutationInput struct {
	KnowledgeID  string
	Title        string
	Content      string
	SourceURL    string
	Author       string
	Collection   string
	ChunkSize    int
	ChunkOverlap int
	Version      int
	Metadata     map[string]string
	Entities     []ToolEntityInput
	Relations    []ToolRelationInput
}

type knowledgeMutationPlan struct {
	document   *core.Document
	embeddings []*core.Embedding
	graphOps   *graph.BatchGraphOperation
	ingest     knowledgeIngestResult
}

func (db *DB) buildKnowledgeMutationPlan(ctx context.Context, input knowledgeMutationInput) (*knowledgeMutationPlan, error) {
	if input.KnowledgeID == "" {
		return nil, fmt.Errorf("knowledge_id is required")
	}
	if strings.TrimSpace(input.Content) == "" {
		return nil, ErrEmptyText
	}
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}

	ingestOpts := GraphRAGIngestOptions{
		Collection:   input.Collection,
		ChunkSize:    input.ChunkSize,
		ChunkOverlap: input.ChunkOverlap,
	}
	applyGraphRAGIngestDefaults(&ingestOpts)

	plan := &knowledgeMutationPlan{
		document: &core.Document{
			ID:        input.KnowledgeID,
			Title:     input.Title,
			Content:   input.Content,
			SourceURL: input.SourceURL,
			Version:   input.Version,
			Author:    input.Author,
			Metadata:  stringMapToAnyMap(input.Metadata),
		},
		graphOps: &graph.BatchGraphOperation{},
		ingest: knowledgeIngestResult{
			documentNodeID: graphDocumentNodeID(input.KnowledgeID),
			collection:     ingestOpts.Collection,
		},
	}

	if db.HasEmbedder() {
		return db.buildEmbedderKnowledgePlan(ctx, input, ingestOpts, plan)
	}
	return db.buildLexicalKnowledgePlan(ctx, input, ingestOpts, plan)
}

func (db *DB) buildEmbedderKnowledgePlan(ctx context.Context, input knowledgeMutationInput, ingestOpts GraphRAGIngestOptions, plan *knowledgeMutationPlan) (*knowledgeMutationPlan, error) {
	if err := db.ensureGraphRAGCollection(ctx, ingestOpts.Collection); err != nil {
		return nil, err
	}

	chunks := splitGraphRAGText(input.Content, ingestOpts.ChunkSize, ingestOpts.ChunkOverlap)
	if len(chunks) == 0 {
		return nil, ErrEmptyText
	}

	chunkVectors, err := db.embedder.EmbedBatch(ctx, chunks)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEmbeddingFailed, err)
	}
	docVector := averageVectors(chunkVectors, db.embedder.Dim())

	chunkEmbeddings, chunkNodes, edgeMap, chunkIDs := buildKnowledgeChunkArtifacts(input, ingestOpts.Collection, chunks, chunkVectors, docVector)
	plan.embeddings = append(plan.embeddings, chunkEmbeddings...)
	plan.graphOps.NodeUpserts = append(plan.graphOps.NodeUpserts, chunkNodes...)

	entityTexts, entityMentions, relationshipMap, err := db.extractKnowledgeEntities(ctx, input, chunks, chunkIDs)
	if err != nil {
		return nil, err
	}
	if err := db.validateExtractedGraphData(ctx, entityTexts, relationshipMap); err != nil {
		return nil, fmt.Errorf("validate extracted graph data: %w", err)
	}

	entityNodes, entityTypes, mentionEdges, relationEdges, entityIDs, err := db.buildExtractedEntityArtifacts(ctx, entityTexts, entityMentions, relationshipMap)
	if err != nil {
		return nil, err
	}
	plan.ingest.entityNodeIDs = append(plan.ingest.entityNodeIDs, entityIDs...)
	for _, edge := range mentionEdges {
		edgeMap[edge.ID] = edge
	}
	for _, edge := range relationEdges {
		edgeMap[edge.ID] = edge
	}

	if err := db.appendKnowledgeExplicitArtifacts(ctx, input, entityNodes, entityTypes, edgeMap, &plan.ingest); err != nil {
		return nil, err
	}
	plan.graphOps.NodeUpserts = append(plan.graphOps.NodeUpserts, sortedNodePointers(entityNodes)...)
	plan.graphOps.EdgeUpserts = sortedEdgePointers(edgeMap)
	return plan, nil
}

func (db *DB) buildLexicalKnowledgePlan(ctx context.Context, input knowledgeMutationInput, ingestOpts GraphRAGIngestOptions, plan *knowledgeMutationPlan) (*knowledgeMutationPlan, error) {
	toolbox := db.GraphRAGTools()
	vectorDim, err := toolbox.lexicalVectorDim(ctx, ingestOpts.Collection)
	if err != nil {
		return nil, err
	}
	if err := toolbox.ensureLexicalCollection(ctx, ingestOpts.Collection, vectorDim); err != nil {
		return nil, err
	}

	chunks := splitGraphRAGText(input.Content, ingestOpts.ChunkSize, ingestOpts.ChunkOverlap)
	if len(chunks) == 0 {
		return nil, ErrEmptyText
	}

	chunkVectors := make([][]float32, 0, len(chunks))
	for _, chunk := range chunks {
		chunkVectors = append(chunkVectors, lexicalVectorForText(chunk, vectorDim))
	}
	docVector := lexicalVectorForText(firstNonEmpty(input.Title, input.Content), vectorDim)

	chunkEmbeddings, chunkNodes, edgeMap, _ := buildKnowledgeChunkArtifacts(input, ingestOpts.Collection, chunks, chunkVectors, docVector)
	plan.embeddings = append(plan.embeddings, chunkEmbeddings...)
	plan.graphOps.NodeUpserts = append(plan.graphOps.NodeUpserts, chunkNodes...)

	entityNodes := make(map[string]*graph.GraphNode)
	entityTypes := make(map[string]string)
	if err := db.appendKnowledgeExplicitArtifacts(ctx, input, entityNodes, entityTypes, edgeMap, &plan.ingest); err != nil {
		return nil, err
	}
	plan.graphOps.NodeUpserts = append(plan.graphOps.NodeUpserts, sortedNodePointers(entityNodes)...)
	plan.graphOps.EdgeUpserts = sortedEdgePointers(edgeMap)
	return plan, nil
}

func buildKnowledgeChunkArtifacts(input knowledgeMutationInput, collection string, chunks []string, chunkVectors [][]float32, documentVector []float32) ([]*core.Embedding, []*graph.GraphNode, map[string]*graph.GraphEdge, []string) {
	documentNodeID := graphDocumentNodeID(input.KnowledgeID)
	docNode := &graph.GraphNode{
		ID:       documentNodeID,
		Vector:   documentVector,
		Content:  firstNonEmpty(input.Title, input.Content),
		NodeType: "document",
		Properties: map[string]any{
			"document_id": input.KnowledgeID,
			"title":       input.Title,
		},
	}

	embeddings := make([]*core.Embedding, 0, len(chunks))
	nodes := make([]*graph.GraphNode, 0, len(chunks)+1)
	nodes = append(nodes, docNode)
	edgeMap := make(map[string]*graph.GraphEdge, len(chunks)*2)
	chunkIDs := make([]string, 0, len(chunks))

	for i, chunk := range chunks {
		chunkID := graphChunkNodeID(input.KnowledgeID, i)
		chunkIDs = append(chunkIDs, chunkID)

		metadata := map[string]string{
			"graph_kind":  "chunk",
			"document_id": input.KnowledgeID,
			"chunk_index": fmt.Sprintf("%d", i),
			"title":       input.Title,
		}
		for key, value := range input.Metadata {
			metadata[key] = value
		}

		embeddings = append(embeddings, &core.Embedding{
			ID:         chunkID,
			Collection: collection,
			Vector:     chunkVectors[i],
			Content:    chunk,
			DocID:      input.KnowledgeID,
			Metadata:   metadata,
		})

		nodes = append(nodes, &graph.GraphNode{
			ID:       chunkID,
			Vector:   chunkVectors[i],
			Content:  chunk,
			NodeType: "chunk",
			Properties: map[string]any{
				"document_id": input.KnowledgeID,
				"chunk_index": i,
				"title":       input.Title,
			},
		})

		docChunkEdge := &graph.GraphEdge{
			ID:         fmt.Sprintf("edge:doc_chunk:%s:%d", input.KnowledgeID, i),
			FromNodeID: documentNodeID,
			ToNodeID:   chunkID,
			EdgeType:   "has_chunk",
			Weight:     1.0,
		}
		edgeMap[docChunkEdge.ID] = docChunkEdge

		if i > 0 {
			nextEdge := &graph.GraphEdge{
				ID:         fmt.Sprintf("edge:chunk_next:%s:%d", input.KnowledgeID, i),
				FromNodeID: graphChunkNodeID(input.KnowledgeID, i-1),
				ToNodeID:   chunkID,
				EdgeType:   "next",
				Weight:     1.0,
			}
			edgeMap[nextEdge.ID] = nextEdge
		}
	}

	return embeddings, nodes, edgeMap, chunkIDs
}

func (db *DB) extractKnowledgeEntities(ctx context.Context, input knowledgeMutationInput, chunks []string, chunkIDs []string) (map[string]GraphEntity, map[string]map[string]struct{}, map[string]graph.GraphEdge, error) {
	extractor := defaultGraphRAGExtractor{}
	entityTexts := make(map[string]GraphEntity)
	entityMentions := make(map[string]map[string]struct{})
	relationshipMap := make(map[string]graph.GraphEdge)

	for i, chunk := range chunks {
		extraction, err := extractor.Extract(ctx, chunk)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("extract graph entities: %w", err)
		}
		if extraction == nil {
			continue
		}

		chunkID := chunkIDs[i]
		for _, entity := range extraction.Entities {
			if strings.TrimSpace(entity.Name) == "" {
				continue
			}
			entityID := graphEntityNodeID(entity.Name)
			entityTexts[entityID] = GraphEntity{Name: entity.Name, Type: firstNonEmpty(entity.Type, "entity")}
			if entityMentions[chunkID] == nil {
				entityMentions[chunkID] = make(map[string]struct{})
			}
			entityMentions[chunkID][entityID] = struct{}{}
		}

		for _, rel := range extraction.Relationships {
			if strings.TrimSpace(rel.From) == "" || strings.TrimSpace(rel.To) == "" {
				continue
			}
			fromID := graphEntityNodeID(rel.From)
			toID := graphEntityNodeID(rel.To)
			relType := firstNonEmpty(rel.Type, "related_to")
			weight := rel.Weight
			if weight == 0 {
				weight = 1.0
			}
			key := fmt.Sprintf("%s|%s|%s|%s", chunkID, fromID, toID, relType)
			relationshipMap[key] = graph.GraphEdge{
				ID:         fmt.Sprintf("edge:rel:%s:%s:%s:%s", chunkID, fromID, toID, relType),
				FromNodeID: fromID,
				ToNodeID:   toID,
				EdgeType:   relType,
				Weight:     weight,
				Properties: map[string]any{
					"source_chunk_id": chunkID,
					"document_id":     input.KnowledgeID,
				},
			}
		}
	}

	return entityTexts, entityMentions, relationshipMap, nil
}

func (db *DB) buildExtractedEntityArtifacts(ctx context.Context, entityTexts map[string]GraphEntity, entityMentions map[string]map[string]struct{}, relationshipMap map[string]graph.GraphEdge) (map[string]*graph.GraphNode, map[string]string, []*graph.GraphEdge, []*graph.GraphEdge, []string, error) {
	entityNodes := make(map[string]*graph.GraphNode, len(entityTexts))
	entityTypes := make(map[string]string, len(entityTexts))
	entityIDs := make([]string, 0, len(entityTexts))

	if len(entityTexts) > 0 {
		orderedEntityIDs := make([]string, 0, len(entityTexts))
		entityNames := make([]string, 0, len(entityTexts))
		for entityID := range entityTexts {
			orderedEntityIDs = append(orderedEntityIDs, entityID)
		}
		sort.Strings(orderedEntityIDs)
		for _, entityID := range orderedEntityIDs {
			entityNames = append(entityNames, entityTexts[entityID].Name)
		}

		entityVectors, err := db.embedder.EmbedBatch(ctx, entityNames)
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("embed entities: %w", err)
		}

		for i, entityID := range orderedEntityIDs {
			entity := entityTexts[entityID]
			entityIDs = append(entityIDs, entityID)
			entityTypes[entityID] = entity.Type
			entityNodes[entityID] = &graph.GraphNode{
				ID:       entityID,
				Vector:   entityVectors[i],
				Content:  entity.Name,
				NodeType: entity.Type,
				Properties: map[string]any{
					"name": entity.Name,
					"type": entity.Type,
				},
			}
		}
	}

	mentionEdges := make([]*graph.GraphEdge, 0)
	chunkIDs := make([]string, 0, len(entityMentions))
	for chunkID := range entityMentions {
		chunkIDs = append(chunkIDs, chunkID)
	}
	sort.Strings(chunkIDs)
	for _, chunkID := range chunkIDs {
		entityIDsForChunk := sortedKeysFromSet(entityMentions[chunkID])
		for _, entityID := range entityIDsForChunk {
			mentionEdges = append(mentionEdges, &graph.GraphEdge{
				ID:         fmt.Sprintf("edge:mention:%s:%s", chunkID, entityID),
				FromNodeID: chunkID,
				ToNodeID:   entityID,
				EdgeType:   "mentions",
				Weight:     1.0,
			})
		}
	}

	relationEdges := make([]*graph.GraphEdge, 0, len(relationshipMap))
	relationshipKeys := make([]string, 0, len(relationshipMap))
	for key := range relationshipMap {
		relationshipKeys = append(relationshipKeys, key)
	}
	sort.Strings(relationshipKeys)
	for _, key := range relationshipKeys {
		edge := relationshipMap[key]
		edgeCopy := edge
		relationEdges = append(relationEdges, &edgeCopy)
	}

	return entityNodes, entityTypes, mentionEdges, relationEdges, entityIDs, nil
}

func (db *DB) appendKnowledgeExplicitArtifacts(ctx context.Context, input knowledgeMutationInput, entityNodes map[string]*graph.GraphNode, entityTypes map[string]string, edgeMap map[string]*graph.GraphEdge, ingest *knowledgeIngestResult) error {
	if len(input.Entities) == 0 && len(input.Relations) == 0 {
		return nil
	}
	compiled, err := db.activeCompiledOntology(ctx)
	if err != nil {
		return err
	}

	// Endpoints of the relations below may name entities this same request is
	// creating, which are not in the graph yet.
	batchNodeIDsByName := make(map[string]string, len(input.Entities)*2)

	if len(input.Entities) > 0 {
		if err := db.validateEntityInputs(ctx, input.Entities); err != nil {
			return err
		}

		vectorDim, err := db.GraphRAGTools().lexicalVectorDim(ctx, defaultGraphRAGCollection)
		if err != nil {
			return err
		}

		for _, entity := range input.Entities {
			if strings.TrimSpace(entity.Name) == "" && strings.TrimSpace(entity.ID) == "" {
				continue
			}
			entityID, err := ontologyEntityNodeID(compiled, entity)
			if err != nil {
				return err
			}
			for _, alias := range []string{entity.Name, entity.ID} {
				if strings.TrimSpace(alias) != "" {
					batchNodeIDsByName[alias] = entityID
				}
			}
			nodeType := ontologyCanonicalNodeType(compiled, firstNonEmpty(entity.Type, "entity"))
			entityTypes[entityID] = nodeType
			ingest.entityNodeIDs = append(ingest.entityNodeIDs, entityID)

			properties := map[string]any{}
			if entity.Name != "" {
				properties["name"] = entity.Name
			}
			if entity.Description != "" {
				properties["description"] = entity.Description
			}
			for key, value := range entity.Metadata {
				properties[key] = value
			}

			entityNodes[entityID] = &graph.GraphNode{
				ID:         entityID,
				Vector:     lexicalVectorForText(strings.TrimSpace(entity.Name+" "+entity.Description), vectorDim),
				Content:    firstNonEmpty(entity.Name, entity.ID),
				NodeType:   nodeType,
				Properties: properties,
			}

			for _, chunkID := range entity.ChunkIDs {
				if chunkID == "" {
					continue
				}
				mentionEdge := &graph.GraphEdge{
					ID:         fmt.Sprintf("edge:mention:%s:%s", chunkID, entityID),
					FromNodeID: chunkID,
					ToNodeID:   entityID,
					EdgeType:   "mentions",
					Weight:     1.0,
				}
				edgeMap[mentionEdge.ID] = mentionEdge
			}
		}
	}

	if len(input.Relations) == 0 {
		return nil
	}
	// Rewritten to node IDs up front so validation and the edge write below
	// cannot disagree about which node an endpoint names.
	relations := resolveKnowledgeRelationEndpoints(input.Relations, batchNodeIDsByName)
	if err := db.validateKnowledgeRelationInputs(ctx, relations, entityTypes); err != nil {
		return err
	}

	for i, rel := range relations {
		fromID, err := db.ontologyRelationEndpointNodeID(ctx, compiled, rel.From)
		if err != nil {
			return err
		}
		toID, err := db.ontologyRelationEndpointNodeID(ctx, compiled, rel.To)
		if err != nil {
			return err
		}
		if fromID == "" || toID == "" {
			continue
		}
		edgeType := firstNonEmpty(rel.Type, "related_to")
		edgeID := fmt.Sprintf("edge:relation:%s:%s:%s:%d", fromID, toID, edgeType, i)
		ingest.relationEdgeIDs = append(ingest.relationEdgeIDs, edgeID)

		properties := map[string]any{}
		if input.KnowledgeID != "" {
			properties["document_id"] = input.KnowledgeID
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
		for key, value := range rel.Metadata {
			properties[key] = value
		}

		weight := rel.Weight
		if weight == 0 {
			weight = 1.0
		}
		edgeMap[edgeID] = &graph.GraphEdge{
			ID:         edgeID,
			FromNodeID: fromID,
			ToNodeID:   toID,
			EdgeType:   edgeType,
			Weight:     weight,
			Properties: properties,
		}
	}
	return nil
}

// resolveKnowledgeRelationEndpoints rewrites endpoints that name an entity
// being created in this same request to that entity's node ID. Under an
// active ontology a name alone carries no primary key, so an endpoint left as
// a name would not resolve to the node the request is about to write.
// Endpoints naming something outside the request are left alone, to be looked
// up in the graph.
func resolveKnowledgeRelationEndpoints(relations []ToolRelationInput, nodeIDsByName map[string]string) []ToolRelationInput {
	if len(nodeIDsByName) == 0 {
		return relations
	}
	resolved := make([]ToolRelationInput, 0, len(relations))
	for _, relation := range relations {
		if nodeID, ok := nodeIDsByName[relation.From]; ok {
			relation.From = nodeID
		}
		if nodeID, ok := nodeIDsByName[relation.To]; ok {
			relation.To = nodeID
		}
		resolved = append(resolved, relation)
	}
	return resolved
}

func (db *DB) validateKnowledgeRelationInputs(ctx context.Context, relations []ToolRelationInput, plannedEntityTypes map[string]string) error {
	return db.validateRelationInputsWithResolver(ctx, relations, func(ctx context.Context, nodeIDs []string) (map[string]string, error) {
		resolved := make(map[string]string, len(nodeIDs))
		missing := make([]string, 0, len(nodeIDs))
		for _, nodeID := range nodeIDs {
			if entityType, ok := plannedEntityTypes[nodeID]; ok {
				resolved[nodeID] = entityType
				continue
			}
			missing = append(missing, nodeID)
		}
		if len(missing) == 0 {
			return resolved, nil
		}

		existingTypes, err := db.loadOntologyNodeTypes(ctx, missing)
		if err != nil {
			return nil, err
		}
		for nodeID, entityType := range existingTypes {
			resolved[nodeID] = entityType
		}
		return resolved, nil
	})
}

func (db *DB) applyKnowledgeMutation(ctx context.Context, knowledgeID string, replace bool, plan *knowledgeMutationPlan) error {
	tx, err := db.store.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin knowledge mutation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	oldChunkRefs := make([]*core.Embedding, 0)
	oldChunkIDs := make([]string, 0)
	deletedNodeIDs := make([]string, 0)
	if replace {
		oldChunkRefs, err = db.knowledgeChunkRefsTx(ctx, tx, knowledgeID)
		if err != nil {
			return err
		}
		for _, chunk := range oldChunkRefs {
			oldChunkIDs = append(oldChunkIDs, chunk.ID)
		}
		deletedNodeIDs, err = db.cleanupKnowledgeGraphArtifactsTx(ctx, tx, knowledgeID, oldChunkRefs)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM embeddings WHERE doc_id = ?`, knowledgeID); err != nil {
			return fmt.Errorf("delete prior knowledge chunks: %w", err)
		}
	}

	if err := db.upsertKnowledgeDocumentRecordTx(ctx, tx, plan.document); err != nil {
		return err
	}
	if err := db.store.UpsertBatchTx(ctx, tx, plan.embeddings); err != nil {
		return err
	}
	graphResult, err := db.graph.ExecuteBatchTx(ctx, tx, plan.graphOps)
	if err != nil {
		return fmt.Errorf("apply graph mutation batch: %w", err)
	}
	if graphResult != nil && graphResult.FailedCount > 0 {
		return fmt.Errorf("apply graph mutation batch: %w", firstBatchError(graphResult))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit knowledge mutation transaction: %w", err)
	}

	db.store.SyncDeletedEmbeddingIDs(ctx, oldChunkIDs)
	db.graph.SyncDeletedNodeIDs(ctx, deletedNodeIDs)
	db.store.SyncUpsertedEmbeddings(ctx, plan.embeddings)
	db.graph.SyncUpsertedNodes(ctx, plan.graphOps.NodeUpserts)
	return nil
}

func (db *DB) upsertKnowledgeDocumentRecordTx(ctx context.Context, tx *sql.Tx, doc *core.Document) error {
	metadataJSON, err := json.Marshal(doc.Metadata)
	if err != nil {
		return fmt.Errorf("marshal knowledge document metadata: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO documents (id, title, source_url, content, version, author, metadata, acl, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, '[]', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			source_url = excluded.source_url,
			content = excluded.content,
			version = excluded.version,
			author = excluded.author,
			metadata = excluded.metadata,
			updated_at = CURRENT_TIMESTAMP
	`, doc.ID, doc.Title, doc.SourceURL, doc.Content, doc.Version, doc.Author, metadataJSON); err != nil {
		return fmt.Errorf("upsert knowledge document: %w", err)
	}
	return nil
}

func sortedNodePointers(nodes map[string]*graph.GraphNode) []*graph.GraphNode {
	orderedIDs := make([]string, 0, len(nodes))
	for nodeID := range nodes {
		orderedIDs = append(orderedIDs, nodeID)
	}
	sort.Strings(orderedIDs)

	ordered := make([]*graph.GraphNode, 0, len(orderedIDs))
	for _, nodeID := range orderedIDs {
		ordered = append(ordered, nodes[nodeID])
	}
	return ordered
}

func sortedEdgePointers(edges map[string]*graph.GraphEdge) []*graph.GraphEdge {
	orderedIDs := make([]string, 0, len(edges))
	for edgeID := range edges {
		orderedIDs = append(orderedIDs, edgeID)
	}
	sort.Strings(orderedIDs)

	ordered := make([]*graph.GraphEdge, 0, len(orderedIDs))
	for _, edgeID := range orderedIDs {
		ordered = append(ordered, edges[edgeID])
	}
	return ordered
}

func firstBatchError(result *graph.BatchResult) error {
	if result == nil {
		return fmt.Errorf("unknown graph batch failure")
	}
	for _, err := range result.Errors {
		if err != nil {
			return err
		}
	}
	return fmt.Errorf("graph batch had %d failed operations", result.FailedCount)
}
