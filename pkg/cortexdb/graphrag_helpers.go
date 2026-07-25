package cortexdb

import (
	"context"
	"fmt"
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

// textHasCJK reports whether the text holds Han, Hiragana, Katakana or Hangul.
func textHasCJK(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			return true
		}
	}
	return false
}

// plausibleLatinEntity reports whether a Title Case match looks like a real name rather
// than romanisation debris. Applied only to text that also contains CJK, so bilingual
// material keeps its genuine Latin entities ("Transformer", "Chain Rule") while syllable
// fragments ("Gu Dr", "Wng", "Sh", "Qn") are dropped: those are short and, once the PDF
// text layer has mangled the tone marks, usually vowel-less.
func plausibleLatinEntity(match string) bool {
	for _, word := range strings.Fields(match) {
		if len([]rune(word)) < 3 {
			return false
		}
		if !strings.ContainsAny(strings.ToLower(word), "aeiou") {
			return false
		}
	}
	return strings.TrimSpace(match) != ""
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
// splitGraphRAGText splits text into chunks of at most chunkSize words. It is
// sentence/paragraph-aware: whole sentences are packed into a chunk up to the
// word budget and are never cut mid-sentence, so chunks stay coherent retrieval
// units. Consecutive chunks share up to chunkOverlap words of trailing
// sentences for context continuity. A single sentence longer than chunkSize
// falls back to a word window.
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
		return enforceChunkCharLimit([]string{strings.Join(words, " ")}, chunkSize, chunkOverlap)
	}

	sentences := splitChunkSentences(text)
	if len(sentences) <= 1 {
		return enforceChunkCharLimit(wordWindowChunks(text, chunkSize, chunkOverlap), chunkSize, chunkOverlap)
	}

	var chunks []string
	var cur []string
	curWords := 0
	flush := func() {
		if len(cur) > 0 {
			chunks = append(chunks, strings.Join(cur, " "))
		}
	}
	for _, sent := range sentences {
		sw := len(strings.Fields(sent))
		if sw > chunkSize {
			// Oversized single sentence: flush, then word-window it.
			flush()
			cur, curWords = nil, 0
			chunks = append(chunks, wordWindowChunks(sent, chunkSize, chunkOverlap)...)
			continue
		}
		if curWords+sw > chunkSize && len(cur) > 0 {
			flush()
			cur, curWords = overlapTailSentences(cur, chunkOverlap)
		}
		cur = append(cur, sent)
		curWords += sw
	}
	flush()
	return enforceChunkCharLimit(chunks, chunkSize, chunkOverlap)
}

func enforceChunkCharLimit(chunks []string, chunkSize, chunkOverlap int) []string {
	maxChars := chunkSize * 4
	if maxChars < 400 {
		maxChars = 400
	}
	overlapChars := chunkOverlap * 4
	if overlapChars >= maxChars {
		overlapChars = maxChars / 4
	}
	var limited []string
	for _, chunk := range chunks {
		runes := []rune(strings.TrimSpace(chunk))
		if len(runes) <= maxChars {
			if len(runes) > 0 {
				limited = append(limited, string(runes))
			}
			continue
		}
		step := maxChars - overlapChars
		if step <= 0 {
			step = maxChars
		}
		for start := 0; start < len(runes); start += step {
			end := start + maxChars
			if end > len(runes) {
				end = len(runes)
			}
			limited = append(limited, string(runes[start:end]))
			if end == len(runes) {
				break
			}
		}
	}
	return limited
}

// splitChunkSentences breaks text into sentences on ASCII and CJK terminators,
// treating newlines as hard (paragraph) boundaries. Terminators stay attached.
func splitChunkSentences(text string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" {
			out = append(out, s)
		}
		b.Reset()
	}
	for _, r := range text {
		if r == '\n' {
			flush()
			continue
		}
		b.WriteRune(r)
		switch r {
		case '.', '!', '?', '。', '！', '？', '；', ';':
			flush()
		}
	}
	flush()
	return out
}

// overlapTailSentences returns the trailing sentences whose combined word count
// fits within overlapWords, plus that word count — used to seed the next chunk.
func overlapTailSentences(cur []string, overlapWords int) ([]string, int) {
	if overlapWords <= 0 || len(cur) == 0 {
		return nil, 0
	}
	total := 0
	start := len(cur)
	for i := len(cur) - 1; i >= 0; i-- {
		w := len(strings.Fields(cur[i]))
		if total+w > overlapWords {
			break
		}
		total += w
		start = i
	}
	return append([]string(nil), cur[start:]...), total
}

// wordWindowChunks is the fallback sliding word window for text with no usable
// sentence structure (or a single oversized sentence).
func wordWindowChunks(text string, chunkSize, chunkOverlap int) []string {
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

// rerankGraphRAGChunks reorders GraphRAG chunks via the public Rerank API, so the
// chunk path and the generic API share one implementation. Chunks map to
// RerankItems (Content→Text, DocumentID→GroupKey for near-duplicate suppression);
// the blend weights and MMR diversity are Rerank's defaults. When a semantic
// reranker is configured, its query-document relevance replaces the base score
// before the heuristic blend + MMR, so the ordering is cross-encoder driven.
func (db *DB) rerankGraphRAGChunks(ctx context.Context, query string, chunks []GraphRAGChunkResult, opts GraphRAGQueryOptions) []GraphRAGChunkResult {
	if len(chunks) == 0 {
		return nil
	}
	byID := make(map[string]*GraphRAGChunkResult, len(chunks))
	items := make([]RerankItem, len(chunks))
	for i := range chunks {
		byID[chunks[i].ID] = &chunks[i]
		items[i] = RerankItem{
			ID:       chunks[i].ID,
			Text:     chunks[i].Content,
			Score:    chunks[i].Score,
			Entities: chunks[i].Entities,
			GroupKey: chunks[i].DocumentID,
		}
	}
	db.applySemanticRerank(ctx, query, items)
	ranked := Rerank(query, items, RerankOptions{TopN: opts.MaxContextChunks, DiversityLambda: opts.DiversityLambda})

	selected := make([]GraphRAGChunkResult, 0, len(ranked))
	for _, it := range ranked {
		c := byID[it.ID]
		c.RerankScore = it.RerankScore
		selected = append(selected, *c)
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

// extractTitleEntities finds Title Case Latin names.
//
// It cannot find anything in Chinese, Japanese or Korean text — the pattern only sees the
// Latin alphabet — so on a CJK corpus every match is incidental Latin, and in practice
// that means romanisation. A scanned textbook is the worst case: its text layer mangles
// pinyin diacritics into stray capitals ("hSn dAi", "pT Wng", "Gu Dr"), which are
// perfectly good Title Case and used to fill the graph with syllable debris instead of
// concepts. Tone marks cannot be tested for, because the mangling destroys them, so the
// pass is skipped for text that is mostly CJK and left to an LLM extractor.
func extractTitleEntities(text string) []GraphEntity {
	requirePlausible := textHasCJK(text)
	matches := titleEntityPattern.FindAllString(text, -1)
	seen := make(map[string]struct{})
	entities := make([]GraphEntity, 0, len(matches))
	for _, match := range matches {
		match = strings.TrimSpace(match)
		if len(match) < 2 {
			continue
		}
		if requirePlausible && !plausibleLatinEntity(match) {
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
