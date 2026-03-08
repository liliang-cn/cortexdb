package cortexdb

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

const defaultGraphRAGCollection = "graphrag_chunks"

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
	Collection       string
	TopK             int
	MaxHops          int
	MaxRelatedChunks int
	MaxContextChunks int
	MaxContextChars  int
	PerDocumentLimit int
	Rerank           bool
	DiversityLambda  float64
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
	if strings.TrimSpace(query) == "" {
		return nil, ErrEmptyText
	}

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

	result := &GraphRAGQueryResult{Query: query}
	if len(seeds) == 0 {
		return result, nil
	}

	chunkResults := make(map[string]*GraphRAGChunkResult)
	entitySet := make(map[string]struct{})
	seedIDs := make(map[string]struct{})

	for _, seed := range seeds {
		chunkResults[seed.ID] = &GraphRAGChunkResult{
			ID:         seed.ID,
			DocumentID: seed.DocID,
			Content:    seed.Content,
			Score:      seed.Score,
		}
		seedIDs[seed.ID] = struct{}{}

		neighbors, err := db.graph.Neighbors(ctx, seed.ID, graph.TraversalOptions{
			MaxDepth:  opts.MaxHops,
			Direction: "both",
			Limit:     opts.TopK * 12,
		})
		if err != nil {
			return nil, fmt.Errorf("expand graph neighborhood: %w", err)
		}

		for _, neighbor := range neighbors {
			switch neighbor.NodeType {
			case "entity":
				entitySet[neighbor.Content] = struct{}{}
			case "chunk":
				if _, isSeed := seedIDs[neighbor.ID]; isSeed {
					continue
				}
				related := chunkResults[neighbor.ID]
				if related == nil {
					related = &GraphRAGChunkResult{
						ID:      neighbor.ID,
						Content: neighbor.Content,
						Score:   seed.Score * 0.5,
					}
					if documentID, ok := stringProperty(neighbor.Properties, "document_id"); ok {
						related.DocumentID = documentID
					}
					chunkResults[neighbor.ID] = related
				} else if seed.Score*0.5 > related.Score {
					related.Score = seed.Score * 0.5
				}
			}
		}
	}

	for chunkID, chunk := range chunkResults {
		neighbors, err := db.graph.Neighbors(ctx, chunkID, graph.TraversalOptions{
			MaxDepth:  1,
			Direction: "both",
			NodeTypes: []string{"entity"},
			Limit:     16,
		})
		if err != nil {
			return nil, fmt.Errorf("load chunk entities: %w", err)
		}
		chunk.Entities = make([]string, 0, len(neighbors))
		for _, entityNode := range neighbors {
			chunk.Entities = append(chunk.Entities, entityNode.Content)
			entitySet[entityNode.Content] = struct{}{}
		}
		sort.Strings(chunk.Entities)
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

type defaultGraphRAGExtractor struct{}

func (defaultGraphRAGExtractor) Extract(_ context.Context, text string) (*GraphExtraction, error) {
	entities := extractTitleEntities(text)
	return &GraphExtraction{Entities: entities}, nil
}

func applyGraphRAGIngestDefaults(opts *GraphRAGIngestOptions) {
	if opts.Collection == "" {
		opts.Collection = defaultGraphRAGCollection
	}
	if opts.ChunkSize <= 0 {
		opts.ChunkSize = 120
	}
	if opts.ChunkOverlap < 0 {
		opts.ChunkOverlap = 0
	}
	if opts.ChunkOverlap >= opts.ChunkSize {
		opts.ChunkOverlap = opts.ChunkSize / 4
	}
}

func applyGraphRAGQueryDefaults(opts *GraphRAGQueryOptions) {
	if opts.Collection == "" {
		opts.Collection = defaultGraphRAGCollection
	}
	if opts.TopK <= 0 {
		opts.TopK = 4
	}
	if opts.MaxHops <= 0 {
		opts.MaxHops = 2
	}
	if opts.MaxRelatedChunks < 0 {
		opts.MaxRelatedChunks = 0
	}
	if opts.MaxRelatedChunks == 0 {
		opts.MaxRelatedChunks = opts.TopK
	}
	if opts.MaxContextChunks <= 0 {
		opts.MaxContextChunks = opts.TopK + opts.MaxRelatedChunks
	}
	if opts.MaxContextChars <= 0 {
		opts.MaxContextChars = 2400
	}
	if opts.PerDocumentLimit <= 0 {
		opts.PerDocumentLimit = 2
	}
	if !opts.Rerank {
		// keep explicit false only if user set lambda too; default should be enabled
		opts.Rerank = true
	}
	if opts.DiversityLambda <= 0 || opts.DiversityLambda > 1 {
		opts.DiversityLambda = 0.75
	}
}

func (db *DB) ensureGraphRAGCollection(ctx context.Context, name string) error {
	_, err := db.store.GetCollection(ctx, name)
	if err == nil {
		return nil
	}
	_, err = db.store.CreateCollection(ctx, name, db.embedder.Dim())
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("ensure graphrag collection: %w", err)
	}
	return nil
}

func (db *DB) upsertGraphRAGDocumentRecord(ctx context.Context, doc *core.Document) error {
	existing, err := db.store.GetDocument(ctx, doc.ID)
	if err != nil {
		if err := db.store.CreateDocument(ctx, doc); err != nil {
			return fmt.Errorf("create document record: %w", err)
		}
		return nil
	}
	existing.Title = doc.Title
	existing.Content = doc.Content
	existing.Version++
	if err := db.store.UpdateDocument(ctx, existing); err != nil {
		return fmt.Errorf("update document record: %w", err)
	}
	return nil
}

func splitGraphRAGText(text string, chunkSize, chunkOverlap int) []string {
	paragraphs := strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == '\r'
	})

	var chunks []string
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			continue
		}
		if len(words) <= chunkSize {
			chunks = append(chunks, strings.Join(words, " "))
			continue
		}

		step := chunkSize - chunkOverlap
		if step <= 0 {
			step = chunkSize
		}
		for start := 0; start < len(words); start += step {
			end := start + chunkSize
			if end > len(words) {
				end = len(words)
			}
			chunks = append(chunks, strings.Join(words[start:end], " "))
			if end == len(words) {
				break
			}
		}
	}

	return chunks
}

func averageVectors(vectors [][]float32, dim int) []float32 {
	if len(vectors) == 0 {
		return make([]float32, dim)
	}
	avg := make([]float32, dim)
	for _, vector := range vectors {
		for i := 0; i < len(vector) && i < dim; i++ {
			avg[i] += vector[i]
		}
	}
	for i := range avg {
		avg[i] /= float32(len(vectors))
	}
	return avg
}

func graphDocumentNodeID(documentID string) string {
	return "doc:" + documentID
}

func graphChunkNodeID(documentID string, index int) string {
	return fmt.Sprintf("chunk:%s:%03d", documentID, index)
}

func graphEntityNodeID(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range normalized {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('_')
		}
	}
	id := strings.Trim(b.String(), "_")
	if id == "" {
		id = "entity"
	}
	return "entity:" + id
}

func buildGraphRAGContext(chunks []GraphRAGChunkResult) string {
	if len(chunks) == 0 {
		return ""
	}

	var lines []string
	for _, chunk := range chunks {
		prefix := chunk.ID
		if chunk.DocumentID != "" {
			prefix = chunk.DocumentID + "/" + chunk.ID
		}
		lines = append(lines, fmt.Sprintf("[%s] %s", prefix, chunk.Content))
	}
	return strings.Join(lines, "\n")
}

func rerankGraphRAGChunks(query string, chunks []GraphRAGChunkResult, opts GraphRAGQueryOptions) []GraphRAGChunkResult {
	if len(chunks) == 0 {
		return nil
	}

	queryTerms := tokenSet(query)
	queryEntities := tokenSet(strings.Join(extractEntityNames(extractTitleEntities(query)), " "))

	normalizedBase := normalizeChunkScores(chunks)
	for i := range chunks {
		termOverlap := overlapScore(queryTerms, tokenSet(chunks[i].Content))
		entityOverlap := overlapScore(queryEntities, tokenSet(strings.Join(chunks[i].Entities, " ")))
		chunks[i].RerankScore = normalizedBase[i]*0.6 + termOverlap*0.25 + entityOverlap*0.15
	}

	selected := make([]GraphRAGChunkResult, 0, len(chunks))
	remaining := append([]GraphRAGChunkResult(nil), chunks...)
	for len(remaining) > 0 && len(selected) < opts.MaxContextChunks {
		bestIdx := 0
		bestScore := -math.MaxFloat64
		for i := range remaining {
			redundancy := maxRedundancy(remaining[i], selected)
			score := opts.DiversityLambda*remaining[i].RerankScore - (1-opts.DiversityLambda)*redundancy
			if score > bestScore {
				bestScore = score
				bestIdx = i
			}
		}
		selected = append(selected, remaining[bestIdx])
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
	}

	return selected
}

func packGraphRAGContext(chunks []GraphRAGChunkResult, opts GraphRAGQueryOptions) []GraphRAGChunkResult {
	if len(chunks) == 0 {
		return nil
	}

	packed := make([]GraphRAGChunkResult, 0, min(len(chunks), opts.MaxContextChunks))
	docCounts := make(map[string]int)
	charCount := 0

	for _, chunk := range chunks {
		if len(packed) >= opts.MaxContextChunks {
			break
		}
		if chunk.DocumentID != "" && docCounts[chunk.DocumentID] >= opts.PerDocumentLimit {
			continue
		}

		lineLen := len(chunk.Content) + len(chunk.ID) + len(chunk.DocumentID) + 8
		if len(packed) > 0 && charCount+lineLen > opts.MaxContextChars {
			continue
		}

		packed = append(packed, chunk)
		charCount += lineLen
		if chunk.DocumentID != "" {
			docCounts[chunk.DocumentID]++
		}
	}

	if len(packed) == 0 && len(chunks) > 0 {
		return chunks[:1]
	}
	return packed
}

func normalizeChunkScores(chunks []GraphRAGChunkResult) []float64 {
	result := make([]float64, len(chunks))
	minScore, maxScore := chunks[0].Score, chunks[0].Score
	for _, chunk := range chunks[1:] {
		if chunk.Score < minScore {
			minScore = chunk.Score
		}
		if chunk.Score > maxScore {
			maxScore = chunk.Score
		}
	}
	if maxScore-minScore < 1e-9 {
		for i := range result {
			result[i] = 1
		}
		return result
	}
	for i, chunk := range chunks {
		result[i] = (chunk.Score - minScore) / (maxScore - minScore)
	}
	return result
}

func maxRedundancy(candidate GraphRAGChunkResult, selected []GraphRAGChunkResult) float64 {
	if len(selected) == 0 {
		return 0
	}
	candidateTerms := tokenSet(candidate.Content)
	maxScore := 0.0
	for _, existing := range selected {
		score := overlapScore(candidateTerms, tokenSet(existing.Content))
		if candidate.DocumentID != "" && candidate.DocumentID == existing.DocumentID {
			score = max(score, 0.85)
		}
		if score > maxScore {
			maxScore = score
		}
	}
	return maxScore
}

func overlapScore(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for key := range a {
		if _, ok := b[key]; ok {
			intersection++
		}
	}
	denom := len(a)
	if len(b) > denom {
		denom = len(b)
	}
	return float64(intersection) / float64(denom)
}

func tokenSet(text string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, token := range strings.Fields(strings.ToLower(text)) {
		token = strings.TrimFunc(token, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		if len(token) < 2 {
			continue
		}
		set[token] = struct{}{}
	}
	return set
}

func extractEntityNames(entities []GraphEntity) []string {
	result := make([]string, 0, len(entities))
	for _, entity := range entities {
		if entity.Name != "" {
			result = append(result, entity.Name)
		}
	}
	return result
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func extractTitleEntities(text string) []GraphEntity {
	entityRe := regexp.MustCompile(`\b(?:[A-Z][a-z0-9]+(?:\s+[A-Z][a-z0-9]+)*)\b`)
	matches := entityRe.FindAllString(text, -1)
	seen := make(map[string]struct{})
	entities := make([]GraphEntity, 0, len(matches))
	for _, match := range matches {
		match = strings.TrimSpace(match)
		if len(match) < 2 {
			continue
		}
		if _, exists := seen[match]; exists {
			continue
		}
		seen[match] = struct{}{}
		entities = append(entities, GraphEntity{Name: match, Type: "entity"})
	}
	return entities
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func stringProperty(properties map[string]interface{}, key string) (string, bool) {
	if properties == nil {
		return "", false
	}
	value, ok := properties[key]
	if !ok {
		return "", false
	}
	str, ok := value.(string)
	return str, ok
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
