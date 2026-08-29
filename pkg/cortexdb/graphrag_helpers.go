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

// A name starts with a capital and runs on through letters and digits, so
// internal capitals and trailing digits stay inside one token. The pattern used
// to be `[A-Z][a-z0-9]+`, which cannot match a word with a capital in the middle
// at all: there is no word boundary inside "CortexDB", so it matched neither
// that nor SQLite, GraphRAG, DRBD, MCP or FTS5 — most of the vocabulary of a
// technical corpus was invisible to entity extraction.
var titleEntityPattern = regexp.MustCompile(`\b(?:[A-Z][A-Za-z0-9]+(?:\s+[A-Z][A-Za-z0-9]+)*)\b`)

// acronymPattern matches a token that is all capitals and digits. Romanisation
// debris is Title Case, never this, so such tokens skip the plausibility filter
// that would otherwise drop DRBD and FTS5 for having no vowel.
var acronymPattern = regexp.MustCompile(`^[A-Z0-9]+$`)

// minCorroborationMatches is how many Title Case matches a text needs before the
// corroboration rule is worth applying. Below it the text is a note or a single
// chunk, where every name legitimately appears once.
const minCorroborationMatches = 12

type defaultGraphRAGExtractor struct{}

func (defaultGraphRAGExtractor) Extract(_ context.Context, text string) (*GraphExtraction, error) {
	entities := extractCorpusEntities(text)
	return &GraphExtraction{Entities: entities}, nil
}

// entityStopwords are words whose capital letter says nothing about them. English
// capitalises the first word of every sentence, so a pattern looking for Title
// Case collects the grammar along with the names: real stores ended up with
// "This", "Only", "Next", "Requires" and "Measured" as entities, and since
// co-occurrence pairs them with everything nearby, each one spread.
//
// Kept to closed-class words and verbs/adverbs that are near-never part of a
// name. Common nouns are deliberately absent — "Gateway", "Volume" and "Node"
// are exactly the entities worth having, and a dictionary cannot tell those from
// "Library" without knowing the subject.
var entityStopwords = map[string]struct{}{}

// entityLeadingStopwords is the subset safe to strip from the front of a longer
// match: no proper name begins with one. "This Gateway" is a mention of Gateway,
// while "New York" keeps its "New" because adjectives are not in here.
var entityLeadingStopwords = map[string]struct{}{}

func init() {
	for _, w := range []string{
		// closed class — also the safe-to-strip set
		"the", "this", "that", "these", "those", "a", "an", "and", "but", "or",
		"if", "when", "while", "because", "since", "however", "therefore",
		"thus", "hence", "also", "then", "though", "although", "otherwise",
		"instead", "meanwhile", "besides", "moreover", "furthermore",
	} {
		entityStopwords[w] = struct{}{}
		entityLeadingStopwords[w] = struct{}{}
	}
	for _, w := range []string{
		// pronouns and determiners
		"it", "its", "they", "them", "their", "we", "our", "you", "your",
		"he", "she", "his", "her", "there", "here", "all", "any", "some",
		"each", "every", "both", "few", "many", "most", "more", "less",
		"only", "just", "even", "such", "same", "other", "another", "no", "not",
		// verbs and participles that open sentences
		"is", "are", "was", "were", "be", "been", "being", "do", "does", "did",
		"have", "has", "had", "can", "could", "will", "would", "should", "may",
		"might", "must", "let", "make", "made", "use", "used", "using", "add",
		"added", "set", "get", "put", "run", "see", "note", "noted", "given",
		"keep", "kept", "call", "called", "return", "returns", "returned",
		"require", "requires", "required", "measure", "measured", "apparent",
		"consider", "without", "with", "from", "into", "onto", "than",
		"before", "after", "during", "unless", "until", "why", "how", "what",
		"which", "who", "whom", "whose", "where",
		// time and ordinal adverbs
		"now", "today", "yesterday", "tomorrow", "once", "twice", "next",
		"last", "first", "second", "third", "finally", "still", "yet", "so",
		// all-caps prose markers, now that acronyms are matched at all
		"ok", "todo", "fixme", "note", "warning", "error", "info", "debug",
		"caveat", "important", "why", "how",
	} {
		entityStopwords[w] = struct{}{}
	}
}

// stripLeadingStopwords removes grammar words from the front of a match.
//
// Which words count as grammar depends on where the match sits. Mid-sentence a
// capital is a choice, so only the closed class goes — "Set Theory" keeps its
// "Set". Opening a sentence the capital is forced by grammar and says nothing,
// so any stopword goes: "See Dr Smith" is a mention of Dr Smith.
func stripLeadingStopwords(match string, sentenceInitial bool) string {
	words := strings.Fields(match)
	for len(words) > 1 {
		lower := strings.ToLower(words[0])
		_, closed := entityLeadingStopwords[lower]
		_, any := entityStopwords[lower]
		if !closed && !(sentenceInitial && any) {
			break
		}
		words = words[1:]
	}
	return strings.Join(words, " ")
}

// allStopwords reports whether a match is grammar all the way through.
func allStopwords(match string) bool {
	words := strings.Fields(match)
	if len(words) == 0 {
		return true
	}
	for _, w := range words {
		if _, ok := entityStopwords[strings.ToLower(w)]; !ok {
			return false
		}
	}
	return true
}

// memberAccessAt reports whether the match at start is the tail of a dotted or
// pathed identifier — the "Printf" in log.Printf, which is a function call being
// discussed, not a thing the text is about.
func memberAccessAt(text string, start int) bool {
	if start == 0 {
		return false
	}
	switch text[start-1] {
	case '.', '/', ':', '\\':
		return true
	}
	return false
}

// sentenceInitialAt reports whether the match at start opens a sentence, a line
// or a list item — positions where a capital letter is required by grammar and
// therefore carries no evidence that the word is a name.
func sentenceInitialAt(text string, start int) bool {
	for i := start - 1; i >= 0; i-- {
		switch c := text[i]; c {
		case ' ', '\t', '-', '*', '#', '>', '|', ')', ']', '+':
			continue
		case '\n', '\r', '.', '!', '?', ':', ';', '(', '[':
			return true
		default:
			if c >= '0' && c <= '9' {
				continue // "1. Options ..." — a numbered list marker
			}
			return false
		}
	}
	return true
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
		if acronymPattern.MatchString(word) {
			continue
		}
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
	// No collection means no restriction, and is left that way. Defaulting it to the collection
	// ingest happens to write into narrows every unscoped search to that one collection: anything
	// stored elsewhere — imported agent memory, a per-book collection — becomes unfindable unless
	// the caller already knows where to look, which is the opposite of what an unscoped search asks.
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
	return EntityNodeIDPrefix + id
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
	return extractEntitiesFromText(text, false)
}

// extractCorpusEntities is extractTitleEntities with one extra demand: a word
// that only ever appears where grammar would capitalise it anyway has to earn
// its place by appearing capitalised somewhere else too.
//
// Only for text being written into the graph. Queries are one line long, so
// almost every word in them opens a sentence and the rule would reject the
// entity hints the query exists to give.
func extractCorpusEntities(text string) []GraphEntity {
	return extractEntitiesFromText(text, true)
}

func extractEntitiesFromText(text string, corroborate bool) []GraphEntity {
	requirePlausible := textHasCJK(text)
	spans := titleEntityPattern.FindAllStringIndex(text, -1)
	// A rule that asks for corroboration can only run where there is text to
	// corroborate against. A short note names each thing once, usually opening a
	// sentence, so applying it there deletes the entities instead of the noise.
	corroborate = corroborate && len(spans) >= minCorroborationMatches
	// Where a word stands mid-sentence, its capital is a choice rather than a
	// rule — that is the corroboration the second pass looks for.
	corroborated := make(map[string]struct{})
	if corroborate {
		for _, span := range spans {
			if sentenceInitialAt(text, span[0]) || memberAccessAt(text, span[0]) {
				continue
			}
			for _, w := range strings.Fields(text[span[0]:span[1]]) {
				corroborated[strings.ToLower(w)] = struct{}{}
			}
		}
	}

	seen := make(map[string]struct{})
	entities := make([]GraphEntity, 0, len(spans))
	for _, span := range spans {
		if memberAccessAt(text, span[0]) {
			continue
		}
		initial := sentenceInitialAt(text, span[0])
		match := stripLeadingStopwords(strings.TrimSpace(text[span[0]:span[1]]), initial)
		if len(match) < 2 || allStopwords(match) {
			continue
		}
		if requirePlausible && !plausibleLatinEntity(match) {
			continue
		}
		if corroborate && initial {
			if _, ok := corroborated[strings.ToLower(strings.Fields(match)[0])]; !ok {
				continue
			}
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
