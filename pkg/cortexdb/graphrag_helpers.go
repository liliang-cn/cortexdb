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
)

var titleEntityPattern = regexp.MustCompile(`\b(?:[A-Z][a-z0-9]+(?:\s+[A-Z][a-z0-9]+)*)\b`)

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
	opts.RetrievalMode = normalizeRetrievalMode(opts.RetrievalMode)
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
	if opts.DisableRerank {
		opts.Rerank = false
	} else if !opts.Rerank {
		opts.Rerank = true
	}
	if opts.DiversityLambda <= 0 || opts.DiversityLambda > 1 {
		opts.DiversityLambda = 0.75
	}
	applyGraphRuntimeDefaults(opts)
}

func (db *DB) ensureGraphRAGCollection(ctx context.Context, name string) error {
	return db.ensureCollectionExists(ctx, name, db.embedder.Dim(), "graphrag")
}

func (db *DB) ensureCollectionExists(ctx context.Context, name string, dim int, label string) error {
	if name == "" {
		name = defaultGraphRAGCollection
	}
	if _, err := db.store.GetCollection(ctx, name); err == nil {
		return nil
	}
	if _, err := db.store.CreateCollection(ctx, name, dim); err != nil {
		if _, getErr := db.store.GetCollection(ctx, name); getErr == nil {
			return nil
		}
		return fmt.Errorf("ensure %s collection: %w", label, err)
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

// splitGraphRAGText splits text into overlapping chunks of up to chunkSize
// words. It uses a sliding window over ALL words in the document (whitespace,
// including newlines, is collapsed) rather than chunking each line/paragraph
// independently. This keeps short lines — headings, list items, single
// commands — merged with their surrounding context instead of becoming tiny
// standalone chunks, which produce semantically thin embeddings and poor
// retrieval ranking.
func splitGraphRAGText(text string, chunkSize, chunkOverlap int) []string {
	if chunkSize <= 0 {
		chunkSize = 120
	}
	if chunkOverlap < 0 || chunkOverlap >= chunkSize {
		chunkOverlap = 0
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	if len(words) <= chunkSize {
		return []string{strings.Join(words, " ")}
	}

	step := chunkSize - chunkOverlap
	if step <= 0 {
		step = chunkSize
	}

	var chunks []string
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

func extractTitleEntities(text string) []GraphEntity {
	matches := titleEntityPattern.FindAllString(text, -1)
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
