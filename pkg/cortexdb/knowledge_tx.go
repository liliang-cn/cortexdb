package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
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
	dedupe     *contentLookup
	condition  *ingestCondition
	// mediaVectors replace the document's media vectors (media_vectors.go).
	mediaVectors []mediaVectorRow
}

// knowledgeRetire says what a write takes out of what is stored before it
// writes, in the same transaction.
type knowledgeRetire int

const (
	// retireNothing: a first write. Every row is inserted under its id.
	retireNothing knowledgeRetire = iota
	// retireChunks: ingest_document over an existing document. Chunk i is
	// upserted in place;
	// only the chunk rows and chunk nodes past the new text's end go, with
	// the edges that hang off them (has_chunk, next, mentions). The
	// document node, the entities and the relation edges stay: the caller
	// attached them, and the text does not unsay them.
	retireChunks
	// retireGraph: knowledge_save and knowledge_update over an existing
	// document, whatever they change. The document's whole graph is retired
	// and rebuilt from the text and the request's entities and relations, as
	// on main.
	retireGraph
)

// contentLookup is a dedupe check to run inside the write transaction: a
// document of the collection, other than the one being written, that holds
// this content stops the write with duplicateContent.
type contentLookup struct {
	hash       string
	collection string
}

// duplicateContent is what applyKnowledgeMutation returns when the lookup
// found a holder. Nothing was written; the ingest reports duplicate_of.
type duplicateContent struct{ holder string }

func (e duplicateContent) Error() string { return "content already stored as " + e.holder }

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

	entityNodes, entityTypes, mentionEdges, relationEdges, entityIDs, err := db.buildExtractedEntityArtifacts(ctx, entityTexts, entityMentions, relationshipMap, db.embedder.EmbedBatch)
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
	if err := db.mergeEntityNodeProperties(ctx, entityNodes, ingestOpts.Collection); err != nil {
		return nil, err
	}
	recordEntitySourceDocument(entityNodes, input.KnowledgeID)
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

	chunkEmbeddings, chunkNodes, edgeMap, chunkIDs := buildKnowledgeChunkArtifacts(input, ingestOpts.Collection, chunks, chunkVectors, docVector)
	plan.embeddings = append(plan.embeddings, chunkEmbeddings...)
	plan.graphOps.NodeUpserts = append(plan.graphOps.NodeUpserts, chunkNodes...)

	// Extraction is deterministic and needs no model, so it runs here as it
	// does with an embedder. It used not to: entity vectors came from the
	// embedder, and without one this path skipped extraction altogether. A
	// knowledge base built without an embedder — the plugin's default — had
	// chunks and no entity graph, and every graph-walking retrieval mode fell
	// back to lexical on it, silently; benchmarked through SaveKnowledge, graph
	// and ppr scored exactly what lexical did. Entity vectors are lexical
	// vectors here, like the chunks'.
	entityTexts, entityMentions, relationshipMap, err := db.extractKnowledgeEntities(ctx, input, chunks, chunkIDs)
	if err != nil {
		return nil, err
	}
	if err := db.validateExtractedGraphData(ctx, entityTexts, relationshipMap); err != nil {
		return nil, fmt.Errorf("validate extracted graph data: %w", err)
	}
	lexicalVectors := func(_ context.Context, texts []string) ([][]float32, error) {
		vectors := make([][]float32, len(texts))
		for i, text := range texts {
			vectors[i] = lexicalVectorForText(text, vectorDim)
		}
		return vectors, nil
	}
	entityNodes, entityTypes, mentionEdges, relationEdges, entityIDs, err := db.buildExtractedEntityArtifacts(ctx, entityTexts, entityMentions, relationshipMap, lexicalVectors)
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
	if err := db.mergeEntityNodeProperties(ctx, entityNodes, ingestOpts.Collection); err != nil {
		return nil, err
	}
	recordEntitySourceDocument(entityNodes, input.KnowledgeID)
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
	// The content hash is a property of the node as well as of the record,
	// where ingest_document also puts it and where dedupe looks it up.
	if hash := input.Metadata[contentHashKey]; hash != "" {
		docNode.Properties[contentHashKey] = hash
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
			if !recordOnlyKey(key) {
				metadata[key] = value
			}
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

	// Names the caller declared in this same request. Extraction must not build
	// a second, untyped node for them: a declared entity is keyed by its
	// ontology identity while extraction keys on the name, so the two land on
	// different ids and the generic one shadows the typed one when a later
	// relation resolves its endpoints by name. That surfaced as a link being
	// refused for connecting "Airport and entity" instead of Airport and Flight.
	declared := make(map[string]struct{}, len(input.Entities)*2)
	declaredNames := make([]string, 0, len(input.Entities))
	for _, entity := range input.Entities {
		for _, alias := range []string{entity.Name, entity.ID} {
			if alias = strings.TrimSpace(alias); alias != "" {
				declared[strings.ToLower(alias)] = struct{}{}
			}
		}
		if name := strings.TrimSpace(entity.Name); name != "" {
			declaredNames = append(declaredNames, strings.ToLower(name))
		}
	}

	for i, chunk := range chunks {
		extraction, err := extractor.Extract(ctx, chunk)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("extract graph entities: %w", err)
		}
		if extraction == nil {
			continue
		}

		chunkID := chunkIDs[i]
		suppressed := make(map[string]struct{})
		for _, entity := range extraction.Entities {
			if strings.TrimSpace(entity.Name) == "" {
				continue
			}
			if _, ok := declared[strings.ToLower(strings.TrimSpace(entity.Name))]; ok {
				continue
			}
			if partOfADeclaredName(entity.Name, chunk, declaredNames) {
				suppressed[graphEntityNodeID(entity.Name)] = struct{}{}
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
			if _, ok := suppressed[fromID]; ok {
				continue
			}
			if _, ok := suppressed[toID]; ok {
				continue
			}
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

	// The title names what every chunk of the document is about; see
	// title_entity.go. A name the caller declared stays theirs.
	if entity, ok := documentTitleEntity(input.Title); ok {
		if _, isDeclared := declared[strings.ToLower(entity.Name)]; !isDeclared {
			entityID := graphEntityNodeID(entity.Name)
			if _, seen := entityTexts[entityID]; !seen {
				entityTexts[entityID] = entity
			}
			for _, chunkID := range chunkIDs {
				if entityMentions[chunkID] == nil {
					entityMentions[chunkID] = make(map[string]struct{})
				}
				entityMentions[chunkID][entityID] = struct{}{}
			}
		}
	}

	return entityTexts, entityMentions, relationshipMap, nil
}

// partOfADeclaredName reports whether name is only some of the words of a
// longer name the caller declared and the chunk spells out. The extractor
// reads "Bridge 01" as "Bridge"; next to a declared "Bridge 01" that fragment
// is not a second entity but a piece of the first, and as a node it joins
// every chunk naming any bridge into one hub the graph walks then spread over.
func partOfADeclaredName(name, chunk string, declaredNames []string) bool {
	words := strings.Fields(strings.ToLower(name))
	if len(words) == 0 {
		return false
	}
	fragment := " " + strings.Join(words, " ") + " "
	chunk = strings.ToLower(chunk)
	for _, full := range declaredNames {
		fullWords := strings.Fields(full)
		if len(fullWords) <= len(words) || !strings.Contains(chunk, full) {
			continue
		}
		if strings.Contains(" "+strings.Join(fullWords, " ")+" ", fragment) {
			return true
		}
	}
	return false
}

// entityVectorizer turns entity names into node vectors: the embedder when
// one is configured, lexical vectors when not.
type entityVectorizer func(ctx context.Context, texts []string) ([][]float32, error)

func (db *DB) buildExtractedEntityArtifacts(ctx context.Context, entityTexts map[string]GraphEntity, entityMentions map[string]map[string]struct{}, relationshipMap map[string]graph.GraphEdge, vectorize entityVectorizer) (map[string]*graph.GraphNode, map[string]string, []*graph.GraphEdge, []*graph.GraphEdge, []string, error) {
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

		entityVectors, err := vectorize(ctx, entityNames)
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

		// Extraction types everything it finds "entity", and the upsert would
		// write that over a type somebody declared earlier.
		pending := make([]*graph.GraphNode, 0, len(orderedEntityIDs))
		for _, entityID := range orderedEntityIDs {
			pending = append(pending, entityNodes[entityID])
		}
		if err := db.preserveDeclaredEntityTypes(ctx, pending); err != nil {
			return nil, nil, nil, nil, nil, err
		}
		for _, entityID := range orderedEntityIDs {
			entityTypes[entityID] = entityNodes[entityID].NodeType
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

// mergeEntityNodeProperties applies the upsert-updates rule to every entity
// node a knowledge write is about to produce.
//
// It sits at the flush rather than beside one of the two producers, because
// both produce nodes for the same objects and only one of them used to be
// covered. An entity a document merely MENTIONS is built by extraction; an
// entity it DECLARES is built here; a mention and a declaration of the same
// object are the same object. Covering one and not the other made the outcome
// depend on whether the request happened to declare something unrelated.
func (db *DB) mergeEntityNodeProperties(ctx context.Context, entityNodes map[string]*graph.GraphNode, collection string) error {
	if len(entityNodes) == 0 {
		return nil
	}
	nodes := make([]*graph.GraphNode, 0, len(entityNodes))
	for _, node := range entityNodes {
		nodes = append(nodes, node)
	}
	prior, err := db.mergePriorEntityProperties(ctx, nodes)
	if err != nil {
		return err
	}
	mergeEntityCollections(nodes, prior, collection)
	return nil
}

// recordEntitySourceDocument names the document among the sources of every
// entity node a knowledge write is about to produce, after the sources
// mergeEntityNodeProperties carried over from the stored node, as
// upsert_entities does. Without it, the cleanup of the document could not
// tell the entities it alone asserted from the ones another document, or a
// hand, still holds: only those it leaves with no source are its to remove.
func recordEntitySourceDocument(entityNodes map[string]*graph.GraphNode, documentID string) {
	for _, node := range entityNodes {
		node.Properties["source_document_ids"] = unionStrings(node.Properties["source_document_ids"], []string{documentID})
	}
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
		vectorized, err := db.embedVectorizedEntities(ctx, compiled, input.Entities, vectorDim)
		if err != nil {
			return err
		}

		for i, entity := range input.Entities {
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

			vector, ok := vectorized[i]
			if !ok {
				vector = lexicalVectorForText(strings.TrimSpace(entity.Name+" "+entity.Description), vectorDim)
			}
			entityNodes[entityID] = &graph.GraphNode{
				ID:         entityID,
				Vector:     vector,
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

	for _, rel := range relations {
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
		// Named by what the edge connects and the document that states it
		// (relationEdgeID), not by its place in the request: two documents
		// stating the same relation are two edges, each with its own
		// provenance, where an id ending in the index let the second
		// overwrite the first's document_id, and a scope reading the edge
		// then took it for the other document's.
		edgeID := relationEdgeID(fromID, toID, edgeType, input.KnowledgeID)
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
		// The same relation twice in one request is one edge.
		if seen, ok := edgeMap[edgeID]; ok {
			mergeRelationProperties(seen.Properties, properties)
			continue
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

func (db *DB) applyKnowledgeMutation(ctx context.Context, knowledgeID string, retire knowledgeRetire, plan *knowledgeMutationPlan) error {
	// A write over a stored document clears its media vectors, whatever it
	// writes, so a document saved again as text keeps none.
	touchesMedia := retire != retireNothing || len(plan.mediaVectors) > 0
	if touchesMedia {
		if err := db.ensureMediaVectorTable(ctx); err != nil {
			return err
		}
	}
	tx, err := db.store.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin knowledge mutation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Writers of one id queue on PostgreSQL, conditional or not, so a
	// condition checked here still holds when the document is written.
	if err := db.lockDocumentIDTx(ctx, tx, knowledgeID); err != nil {
		return err
	}
	if plan.condition != nil {
		if err := db.checkIngestConditionTx(ctx, tx, knowledgeID, plan.condition); err != nil {
			return err
		}
	}
	// The lookup runs once the transaction holds the write lock (SQLite
	// queues the writers of a process, core/sqlite_writelock.go; PostgreSQL
	// takes an advisory lock on the hash), so two ingests of the same content
	// that race cannot both find nothing: the second sees the first's commit.
	if plan.dedupe != nil {
		holder, err := db.contentHolderTx(ctx, tx, plan.dedupe.hash, knowledgeID, plan.dedupe.collection)
		if err != nil {
			return err
		}
		if holder != "" {
			return duplicateContent{holder: holder}
		}
	}

	oldChunkRefs := make([]*core.Embedding, 0)
	oldChunkIDs := make([]string, 0)
	deletedNodeIDs := make([]string, 0)
	if retire != retireNothing {
		oldChunkRefs, err = db.knowledgeChunkRefsTx(ctx, tx, knowledgeID)
		if err != nil {
			return err
		}
		for _, chunk := range oldChunkRefs {
			oldChunkIDs = append(oldChunkIDs, chunk.ID)
		}
		if retire == retireGraph {
			deletedNodeIDs, err = db.cleanupKnowledgeGraphArtifactsTx(ctx, tx, knowledgeID, oldChunkRefs)
		} else {
			// Other text: chunk i of the new text is chunk i of the old one,
			// upserted in place with the graph attached to it, as on main.
			// Only the chunks past the new text's end are retired, with
			// the edges that hang off them, so none of them is served again.
			oldChunkIDs = staleChunkIDs(oldChunkIDs, plan.embeddings)
			deletedNodeIDs, err = db.retireChunkNodesTx(ctx, tx, oldChunkIDs)
		}
		if err != nil {
			return err
		}
		if err := deleteStringIDsTx(ctx, db.Dialect(), tx, "embeddings", "id", oldChunkIDs); err != nil {
			return fmt.Errorf("delete prior knowledge chunks: %w", err)
		}
	}

	if err := db.upsertKnowledgeDocumentRecordTx(ctx, tx, plan.document); err != nil {
		return err
	}
	if err := db.store.UpsertBatchTx(ctx, tx, plan.embeddings); err != nil {
		return err
	}
	if touchesMedia {
		if err := db.writeMediaVectorsTx(ctx, tx, knowledgeID, retire != retireNothing, plan.mediaVectors); err != nil {
			return err
		}
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

// contentHolderQuery is the dedupe lookup: its hash expression is the one
// idx_documents_content_sha256 indexes on both stores.
func (db *DB) contentHolderQuery() string {
	return `
		SELECT MIN(d.id) FROM documents d
		WHERE ` + db.Dialect().JSONTextGuarded("d.metadata", contentHashKey) + ` = ?
		  AND d.id <> ?
		  AND EXISTS (SELECT 1 FROM embeddings e JOIN collections c ON c.id = e.collection_id WHERE e.doc_id = d.id AND c.name = ?)
		  AND ` + db.visiblePredicate() + `
	`
}

// contentHolderTx names a document of the collection, other than exceptID,
// whose record carries this content hash, or "" when none does. It reads
// every holder (a sixteen-row cap missed the copy in this collection behind
// sixteen in another) and answers the smallest id, so the answer does not
// depend on the order the rows come in. On PostgreSQL the hash is locked for
// the transaction first, which is what makes two racing ingests wait for
// each other there. A holder the search gate hides (expired) does not hold
// the content: what a search cannot serve is not a copy.
func (db *DB) contentHolderTx(ctx context.Context, tx *sql.Tx, hash, exceptID, collection string) (string, error) {
	if db.Dialect().Kind() == sqldialect.Postgres {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, hash); err != nil {
			return "", fmt.Errorf("lock content hash: %w", err)
		}
	}
	var holder sql.NullString
	err := db.querierQueryRow(ctx, tx, db.contentHolderQuery(), hash, exceptID, collection, instant(time.Now())).Scan(&holder)
	if err != nil {
		return "", fmt.Errorf("look up content hash: %w", err)
	}
	return holder.String, nil
}

func (db *DB) upsertKnowledgeDocumentRecordTx(ctx context.Context, tx *sql.Tx, doc *core.Document) error {
	metadataJSON, err := json.Marshal(doc.Metadata)
	if err != nil {
		return fmt.Errorf("marshal knowledge document metadata: %w", err)
	}
	if _, err := db.txExec(ctx, tx, `
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

// staleChunkIDs is the chunk ids of the previous text that the new text does
// not write again.
func staleChunkIDs(previous []string, next []*core.Embedding) []string {
	kept := make(map[string]struct{}, len(next))
	for _, e := range next {
		kept[e.ID] = struct{}{}
	}
	stale := make([]string, 0, len(previous))
	for _, id := range previous {
		if _, ok := kept[id]; !ok {
			stale = append(stale, id)
		}
	}
	return stale
}
