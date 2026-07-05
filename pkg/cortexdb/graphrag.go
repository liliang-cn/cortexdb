package cortexdb

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

const defaultGraphRAGCollection = "graphrag_chunks"

const (
	// RetrievalModeAuto enables lightweight heuristics to decide whether graph expansion is worth the cost.
	RetrievalModeAuto = "auto"
	// RetrievalModeLexical disables graph expansion and uses only lexical/vector seed retrieval plus packing.
	RetrievalModeLexical = "lexical"
	// RetrievalModeGraph always enables graph expansion and entity enrichment.
	RetrievalModeGraph = "graph"
	// RetrievalModeHybrid fuses vector and lexical retrieval with reciprocal
	// rank fusion. It is what SearchKnowledge uses under auto when an embedder is
	// available, so exact-keyword and semantic matches are combined.
	RetrievalModeHybrid = "hybrid"
)

// GraphRAGDocument is the source unit ingested into the GraphRAG workflow.
type GraphRAGDocument struct {
	ID       string
	Title    string
	Content  string
	Metadata map[string]string
}

// GraphEntity describes an extracted entity.
type GraphEntity struct {
	Name string
	Type string
}

// GraphRelationship describes a directed relationship between entities.
type GraphRelationship struct {
	From   string
	To     string
	Type   string
	Weight float64
}

// GraphExtraction holds entities and relationships extracted from text.
type GraphExtraction struct {
	Entities      []GraphEntity
	Relationships []GraphRelationship
}

// GraphRAGExtractor extracts entities and relationships from text.
type GraphRAGExtractor interface {
	Extract(ctx context.Context, text string) (*GraphExtraction, error)
}

// GraphRAGIngestOptions controls GraphRAG ingestion behavior.
type GraphRAGIngestOptions struct {
	Collection   string
	ChunkSize    int
	ChunkOverlap int
	Extractor    GraphRAGExtractor
}

// GraphRAGIngestResult summarizes the graph artifacts created during ingestion.
type GraphRAGIngestResult struct {
	DocumentNodeID string
	ChunkNodeIDs   []string
	EntityNodeIDs  []string
}

// GraphRAGQueryOptions controls GraphRAG retrieval behavior.
type GraphRAGQueryOptions struct {
	Collection          string
	TopK                int
	MaxHops             int
	MaxRelatedChunks    int
	MaxContextChunks    int
	MaxContextChars     int
	PerDocumentLimit    int
	Rerank              bool
	DisableRerank       bool
	DiversityLambda     float64
	DisableGraph        bool
	RetrievalMode       string
	GraphLight          bool
	MaxExpansionSeeds   int
	MaxTraversalNodes   int
	MaxEntitiesPerChunk int
	Plan                *RetrievalPlan
}

// GraphRAGChunkResult is a retrieved chunk plus graph context.
type GraphRAGChunkResult struct {
	ID          string
	DocumentID  string
	Content     string
	Score       float64
	BaseScore   float64
	RerankScore float64
	Entities    []string
}

// GraphRAGQueryResult contains the assembled GraphRAG retrieval output.
type GraphRAGQueryResult struct {
	Query    string
	Plan     RetrievalPlan
	Decision RetrievalDecision
	Chunks   []GraphRAGChunkResult
	Entities []string
	Context  string
}

// InsertGraphDocument ingests a document into the vector store and graph store for GraphRAG retrieval.
func (db *DB) InsertGraphDocument(ctx context.Context, doc GraphRAGDocument, opts GraphRAGIngestOptions) (*GraphRAGIngestResult, error) {
	if db.embedder == nil {
		return nil, ErrEmbedderNotConfigured
	}
	if doc.ID == "" {
		return nil, fmt.Errorf("cortexdb: graph document ID cannot be empty")
	}
	if strings.TrimSpace(doc.Content) == "" {
		return nil, ErrEmptyText
	}

	applyGraphRAGIngestDefaults(&opts)
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}
	if err := db.ensureGraphRAGCollection(ctx, opts.Collection); err != nil {
		return nil, err
	}

	chunks := splitGraphRAGText(doc.Content, opts.ChunkSize, opts.ChunkOverlap)
	if len(chunks) == 0 {
		return nil, ErrEmptyText
	}

	chunkVectors, err := db.embedder.EmbedBatch(ctx, chunks)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEmbeddingFailed, err)
	}

	documentNodeID := graphDocumentNodeID(doc.ID)
	documentVector := averageVectors(chunkVectors, db.embedder.Dim())

	documentRecord := &core.Document{
		ID:      doc.ID,
		Title:   doc.Title,
		Content: doc.Content,
		Version: 1,
	}
	if err := db.upsertGraphRAGDocumentRecord(ctx, documentRecord); err != nil {
		return nil, err
	}

	documentNode := &graph.GraphNode{
		ID:       documentNodeID,
		Vector:   documentVector,
		Content:  firstNonEmpty(doc.Title, doc.Content),
		NodeType: "document",
		Properties: map[string]interface{}{
			"document_id": doc.ID,
			"title":       doc.Title,
		},
	}
	if err := db.graph.UpsertNode(ctx, documentNode); err != nil {
		return nil, fmt.Errorf("upsert document node: %w", err)
	}

	embeddings := make([]*core.Embedding, 0, len(chunks))
	chunkNodes := make([]*graph.GraphNode, 0, len(chunks))
	edges := make([]*graph.GraphEdge, 0, len(chunks)*3)
	chunkNodeIDs := make([]string, 0, len(chunks))

	entityTexts := make(map[string]GraphEntity)
	entityMentions := make(map[string]map[string]struct{})
	relationshipKeys := make(map[string]graph.GraphEdge)

	extractor := opts.Extractor
	if extractor == nil {
		extractor = defaultGraphRAGExtractor{}
	}

	for i, chunk := range chunks {
		chunkID := graphChunkNodeID(doc.ID, i)
		chunkNodeIDs = append(chunkNodeIDs, chunkID)

		metadata := map[string]string{
			"graph_kind":  "chunk",
			"document_id": doc.ID,
			"chunk_index": fmt.Sprintf("%d", i),
			"title":       doc.Title,
		}
		for k, v := range doc.Metadata {
			metadata[k] = v
		}

		embeddings = append(embeddings, &core.Embedding{
			ID:         chunkID,
			Collection: opts.Collection,
			Vector:     chunkVectors[i],
			Content:    chunk,
			DocID:      doc.ID,
			Metadata:   metadata,
		})

		chunkNodes = append(chunkNodes, &graph.GraphNode{
			ID:       chunkID,
			Vector:   chunkVectors[i],
			Content:  chunk,
			NodeType: "chunk",
			Properties: map[string]interface{}{
				"document_id": doc.ID,
				"chunk_index": i,
				"title":       doc.Title,
			},
		})

		edges = append(edges, &graph.GraphEdge{
			ID:         fmt.Sprintf("edge:doc_chunk:%s:%d", doc.ID, i),
			FromNodeID: documentNodeID,
			ToNodeID:   chunkID,
			EdgeType:   "has_chunk",
			Weight:     1.0,
		})
		if i > 0 {
			edges = append(edges, &graph.GraphEdge{
				ID:         fmt.Sprintf("edge:chunk_next:%s:%d", doc.ID, i),
				FromNodeID: graphChunkNodeID(doc.ID, i-1),
				ToNodeID:   chunkID,
				EdgeType:   "next",
				Weight:     1.0,
			})
		}

		extraction, err := extractor.Extract(ctx, chunk)
		if err != nil {
			return nil, fmt.Errorf("extract graph entities: %w", err)
		}
		if extraction == nil {
			continue
		}

		for _, entity := range extraction.Entities {
			if strings.TrimSpace(entity.Name) == "" {
				continue
			}
			entityID := graphEntityNodeID(entity.Name)
			entityTexts[entityID] = GraphEntity{
				Name: entity.Name,
				Type: firstNonEmpty(entity.Type, "entity"),
			}
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
			relationshipKeys[key] = graph.GraphEdge{
				ID:         fmt.Sprintf("edge:rel:%s:%s:%s:%s", chunkID, fromID, toID, relType),
				FromNodeID: fromID,
				ToNodeID:   toID,
				EdgeType:   relType,
				Weight:     weight,
				Properties: map[string]interface{}{
					"source_chunk_id": chunkID,
					"document_id":     doc.ID,
				},
			}
		}
	}

	if err := db.validateExtractedGraphData(ctx, entityTexts, relationshipKeys); err != nil {
		return nil, fmt.Errorf("validate extracted graph data: %w", err)
	}

	if err := db.store.UpsertBatch(ctx, embeddings); err != nil {
		return nil, fmt.Errorf("upsert graphrag embeddings: %w", err)
	}
	if _, err := db.graph.UpsertNodesBatch(ctx, chunkNodes); err != nil {
		return nil, fmt.Errorf("upsert chunk graph nodes: %w", err)
	}

	entityNodeIDs := make([]string, 0, len(entityTexts))
	if len(entityTexts) > 0 {
		entityNames := make([]string, 0, len(entityTexts))
		idOrder := make([]string, 0, len(entityTexts))
		for entityID, entity := range entityTexts {
			idOrder = append(idOrder, entityID)
			entityNames = append(entityNames, entity.Name)
		}

		entityVectors, err := db.embedder.EmbedBatch(ctx, entityNames)
		if err != nil {
			return nil, fmt.Errorf("embed entities: %w", err)
		}

		entityNodes := make([]*graph.GraphNode, 0, len(entityNames))
		for i, entityID := range idOrder {
			entity := entityTexts[entityID]
			entityNodeIDs = append(entityNodeIDs, entityID)
			entityNodes = append(entityNodes, &graph.GraphNode{
				ID:       entityID,
				Vector:   entityVectors[i],
				Content:  entity.Name,
				NodeType: entity.Type,
				Properties: map[string]interface{}{
					"name": entity.Name,
					"type": entity.Type,
				},
			})
		}
		if _, err := db.graph.UpsertNodesBatch(ctx, entityNodes); err != nil {
			return nil, fmt.Errorf("upsert entity nodes: %w", err)
		}

		for chunkID, mentioned := range entityMentions {
			for entityID := range mentioned {
				edges = append(edges, &graph.GraphEdge{
					ID:         fmt.Sprintf("edge:mention:%s:%s", chunkID, entityID),
					FromNodeID: chunkID,
					ToNodeID:   entityID,
					EdgeType:   "mentions",
					Weight:     1.0,
				})
			}
		}
	}

	if len(relationshipKeys) > 0 {
		keys := make([]string, 0, len(relationshipKeys))
		for key := range relationshipKeys {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			rel := relationshipKeys[key]
			relCopy := rel
			edges = append(edges, &relCopy)
		}
	}

	if len(edges) > 0 {
		if _, err := db.graph.UpsertEdgesBatch(ctx, edges); err != nil {
			return nil, fmt.Errorf("upsert graph edges: %w", err)
		}
	}

	return &GraphRAGIngestResult{
		DocumentNodeID: documentNodeID,
		ChunkNodeIDs:   chunkNodeIDs,
		EntityNodeIDs:  entityNodeIDs,
	}, nil
}

// SearchGraphRAG performs seed chunk retrieval plus graph neighborhood expansion.
func (db *DB) SearchGraphRAG(ctx context.Context, query string, opts GraphRAGQueryOptions) (*GraphRAGQueryResult, error) {
	if db.embedder == nil {
		return nil, ErrEmbedderNotConfigured
	}

	resolution := resolveRetrievalPlan(retrievalPlanInput{
		Query:               query,
		Plan:                opts.Plan,
		RetrievalMode:       opts.RetrievalMode,
		DisableGraph:        opts.DisableGraph,
		Filters:             &RetrievalFilters{Collection: opts.Collection},
		SupportsGraph:       true,
		EmptyQueryUsesGraph: false,
		// This path always has an embedder, so auto should report/behave as
		// semantic rather than falling back to lexical.
		PreferSemantic: true,
	})
	query = resolution.Plan.Query
	if strings.TrimSpace(query) == "" {
		return nil, ErrEmptyText
	}

	opts.Collection = applyRetrievalPlanCollection(opts.Collection, resolution.Plan.Filters)
	opts.RetrievalMode = resolution.Plan.RetrievalMode
	applyGraphRAGQueryDefaults(&opts)
	queryVector, err := db.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEmbeddingFailed, err)
	}

	seeds, err := db.store.Search(ctx, queryVector, core.SearchOptions{
		Collection: opts.Collection,
		TopK:       opts.TopK,
		QueryText:  query,
	})
	if err != nil {
		return nil, fmt.Errorf("search graphrag seeds: %w", err)
	}
	seeds = filterScoredEmbeddings(seeds, resolution.Plan.Filters)

	result := &GraphRAGQueryResult{
		Query:    query,
		Plan:     resolution.Plan,
		Decision: resolution.Decision,
	}
	if len(seeds) == 0 {
		return result, nil
	}

	useGraph := resolution.Decision.UseGraph

	chunkResults := make(map[string]*GraphRAGChunkResult)
	entitySet := make(map[string]struct{})
	seedIDs := make(map[string]struct{})
	seedOrder := make([]string, 0, len(seeds))

	for _, seed := range seeds {
		chunkResults[seed.ID] = &GraphRAGChunkResult{
			ID:         seed.ID,
			DocumentID: seed.DocID,
			Content:    seed.Content,
			Score:      seed.Score,
		}
		seedIDs[seed.ID] = struct{}{}
		seedOrder = append(seedOrder, seed.ID)
	}

	if !useGraph {
		seedChunkList := make([]GraphRAGChunkResult, 0, len(seeds))
		for _, seed := range seeds {
			if chunk, ok := chunkResults[seed.ID]; ok {
				chunk.BaseScore = chunk.Score
				seedChunkList = append(seedChunkList, *chunk)
			}
		}
		result.Chunks = append(result.Chunks, seedChunkList...)
		if opts.Rerank {
			result.Chunks = rerankGraphRAGChunks(query, result.Chunks, opts)
		} else {
			sort.Slice(result.Chunks, func(i, j int) bool { return result.Chunks[i].Score > result.Chunks[j].Score })
			for i := range result.Chunks {
				result.Chunks[i].RerankScore = result.Chunks[i].Score
			}
		}
		result.Chunks = packGraphRAGContext(result.Chunks, opts)
		result.Context = buildGraphRAGContext(result.Chunks)
		return result, nil
	}

	expandedEntities, err := db.expandGraphChunkNeighborhoods(ctx, chunkResults, seedOrder, opts, resolution.Plan.Filters)
	if err != nil {
		return nil, fmt.Errorf("expand graph neighborhood: %w", err)
	}
	for entityName := range expandedEntities {
		entitySet[entityName] = struct{}{}
	}

	chunkIDs := make([]string, 0, len(chunkResults))
	for chunkID := range chunkResults {
		chunkIDs = append(chunkIDs, chunkID)
	}
	entityNamesByChunk, err := db.chunkEntityNamesBatch(ctx, chunkIDs, opts.MaxEntitiesPerChunk)
	if err != nil {
		return nil, fmt.Errorf("load chunk entities: %w", err)
	}
	for chunkID, chunk := range chunkResults {
		chunk.Entities = entityNamesByChunk[chunkID]
		for _, entityName := range chunk.Entities {
			entitySet[entityName] = struct{}{}
		}
	}

	seedChunkList := make([]GraphRAGChunkResult, 0, len(seeds))
	relatedChunkList := make([]GraphRAGChunkResult, 0, len(chunkResults))
	for _, seed := range seeds {
		if chunk, ok := chunkResults[seed.ID]; ok {
			chunk.BaseScore = chunk.Score
			seedChunkList = append(seedChunkList, *chunk)
		}
	}
	for chunkID, chunk := range chunkResults {
		if _, ok := seedIDs[chunkID]; ok {
			continue
		}
		chunk.BaseScore = chunk.Score
		relatedChunkList = append(relatedChunkList, *chunk)
	}
	sort.Slice(relatedChunkList, func(i, j int) bool { return relatedChunkList[i].Score > relatedChunkList[j].Score })
	if len(relatedChunkList) > opts.MaxRelatedChunks {
		relatedChunkList = relatedChunkList[:opts.MaxRelatedChunks]
	}

	result.Chunks = append(result.Chunks, seedChunkList...)
	result.Chunks = append(result.Chunks, relatedChunkList...)
	if opts.Rerank {
		result.Chunks = rerankGraphRAGChunks(query, result.Chunks, opts)
	} else {
		sort.Slice(result.Chunks, func(i, j int) bool { return result.Chunks[i].Score > result.Chunks[j].Score })
		for i := range result.Chunks {
			result.Chunks[i].RerankScore = result.Chunks[i].Score
		}
	}
	result.Chunks = packGraphRAGContext(result.Chunks, opts)

	result.Entities = sortedKeys(entitySet)
	result.Context = buildGraphRAGContext(result.Chunks)
	return result, nil
}
