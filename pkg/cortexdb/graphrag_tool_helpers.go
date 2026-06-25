package cortexdb

import (
	"context"
	"hash/fnv"
	"sort"
	"strings"
	"unicode"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

func (t *GraphRAGToolbox) lexicalVectorDim(ctx context.Context, collection string) (int, error) {
	if collection != "" {
		existing, err := t.db.store.GetCollection(ctx, collection)
		if err == nil && existing.Dimensions > 0 {
			return existing.Dimensions, nil
		}
	}
	if dim := t.db.store.Config().VectorDim; dim > 0 {
		return dim, nil
	}
	return defaultLexicalVectorDim, nil
}

func (t *GraphRAGToolbox) ensureLexicalCollection(ctx context.Context, name string, dim int) error {
	return t.db.ensureCollectionExists(ctx, name, dim, "lexical")
}

func lexicalVectorForText(text string, dim int) []float32 {
	if dim <= 0 {
		dim = defaultLexicalVectorDim
	}
	vector := make([]float32, dim)
	for _, token := range strings.Fields(strings.ToLower(text)) {
		token = normalizeToolToken(token)
		if token == "" {
			continue
		}
		h := fnv.New64a()
		_, _ = h.Write([]byte(token))
		index := int(h.Sum64() % uint64(dim))
		vector[index] += 1
	}
	if isAllZero(vector) {
		vector[0] = 1
	}
	return vector
}

func (t *GraphRAGToolbox) toolChunksFromSearchResults(ctx context.Context, results []core.ScoredEmbedding, includeEntities bool, maxEntitiesPerChunk int) ([]ToolChunk, error) {
	if len(results) == 0 {
		return nil, nil
	}

	entityNamesByChunk := make(map[string][]string)
	if includeEntities {
		var err error
		entityNamesByChunk, err = t.db.chunkEntityNamesBatch(ctx, scoredEmbeddingsOrder(results), maxEntitiesPerChunk)
		if err != nil {
			return nil, err
		}
	}

	chunks := make([]ToolChunk, 0, len(results))
	for _, result := range results {
		chunks = append(chunks, ToolChunk{
			ID:         result.ID,
			DocumentID: result.DocID,
			Content:    result.Content,
			Score:      result.Score,
			Metadata:   result.Metadata,
			Entities:   entityNamesByChunk[result.ID],
		})
	}
	return chunks, nil
}

func (t *GraphRAGToolbox) loadToolChunks(ctx context.Context, scoreMap map[string]float64, orderedIDs []string, includeEntities bool, maxEntitiesPerChunk int) ([]ToolChunk, error) {
	embeddings, err := t.db.embeddingsByIDs(ctx, orderedIDs)
	if err != nil {
		return nil, err
	}

	entityNamesByChunk := make(map[string][]string)
	if includeEntities {
		entityNamesByChunk, err = t.db.chunkEntityNamesBatch(ctx, orderedIDs, maxEntitiesPerChunk)
		if err != nil {
			return nil, err
		}
	}

	chunks := make([]ToolChunk, 0, len(orderedIDs))
	for _, chunkID := range orderedIDs {
		emb, ok := embeddings[chunkID]
		if !ok {
			continue
		}
		score := 0.0
		if scoreMap != nil {
			score = scoreMap[chunkID]
		}
		chunks = append(chunks, ToolChunk{
			ID:         emb.ID,
			DocumentID: emb.DocumentID,
			Content:    emb.Content,
			Score:      score,
			Metadata:   emb.Metadata,
			Entities:   entityNamesByChunk[chunkID],
		})
	}
	return chunks, nil
}

// EntityNodeID returns the normalized graph node id that cortexdb stores an
// entity under, given its raw id or name. Consumers that need to map a chunk or
// source id to its entity node (e.g. to seed ExpandGraph) should use this rather
// than reimplementing the normalization, which is otherwise an internal detail.
func EntityNodeID(idOrName string) string {
	return resolveEntityNodeID(idOrName, "")
}

func resolveEntityNodeID(id string, name string) string {
	if strings.HasPrefix(id, "entity:") {
		return id
	}
	if strings.HasPrefix(name, "entity:") {
		return name
	}
	if strings.TrimSpace(id) != "" {
		return graphEntityNodeID(id)
	}
	if strings.TrimSpace(name) != "" {
		return graphEntityNodeID(name)
	}
	return ""
}

func normalizeToolToken(token string) string {
	return strings.Trim(token, " \t\r\n.,!?;:\"'()[]{}<>")
}

// sanitizeFTSQuery turns arbitrary user text into a safe FTS5 MATCH expression.
// Each whitespace-separated token is wrapped in a double-quoted string literal,
// so FTS5 operators in the raw text (':' column filter, '*', '-', '^', 'OR',
// 'NEAR', parentheses, …) are treated as literal terms rather than query
// syntax. Embedded double quotes are escaped by doubling. Tokens containing no
// letter or digit are dropped; the result is "" when nothing usable remains.
func sanitizeFTSQuery(query string) string {
	fields := strings.Fields(query)
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		if !strings.ContainsFunc(field, func(r rune) bool {
			return unicode.IsLetter(r) || unicode.IsDigit(r)
		}) {
			continue
		}
		terms = append(terms, `"`+strings.ReplaceAll(field, `"`, `""`)+`"`)
	}
	return strings.Join(terms, " ")
}

func lexicalSearchQueries(query string, keywords []string, alternateQueries []string) []string {
	trimmed := strings.TrimSpace(query)

	queries := make([]string, 0, 5)
	seen := make(map[string]struct{}, 5)
	addQuery := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		queries = append(queries, value)
	}

	// Raw query / alternate-query text is natural language that may contain
	// FTS5-significant punctuation (notably ':' which FTS5 reads as a column
	// filter, e.g. "user:" -> "no such column: user"). Quote each token so the
	// text matches as literal terms instead of being parsed as query syntax.
	if trimmed != "" {
		addQuery(sanitizeFTSQuery(trimmed))
	}

	for _, alternateQuery := range alternateQueries {
		addQuery(sanitizeFTSQuery(alternateQuery))
	}

	plannedKeywords := lexicalQueryKeywords(strings.Join(keywords, " "))
	autoKeywords := lexicalQueryKeywords(trimmed)
	if len(autoKeywords) > 0 {
		addQuery(strings.Join(autoKeywords, " OR "))
	}

	allKeywords := mergeLexicalKeywords(plannedKeywords, autoKeywords)
	if len(allKeywords) > 0 {
		addQuery(strings.Join(formatFTSKeywords(allKeywords, false), " OR "))
		addQuery(strings.Join(formatFTSKeywords(allKeywords, true), " OR "))
	}

	return queries
}

func lexicalQueryKeywords(query string) []string {
	rawTokens := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	keywords := make([]string, 0, len(rawTokens))
	seen := make(map[string]struct{}, len(rawTokens))
	for _, token := range rawTokens {
		token = normalizeToolToken(token)
		if token == "" || len(token) < 2 {
			continue
		}
		if _, skip := lexicalQueryStopwords[token]; skip {
			continue
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		keywords = append(keywords, token)
	}
	return keywords
}

func mergeLexicalKeywords(groups ...[]string) []string {
	merged := make([]string, 0)
	seen := make(map[string]struct{})
	for _, group := range groups {
		for _, keyword := range group {
			if keyword == "" {
				continue
			}
			if _, ok := seen[keyword]; ok {
				continue
			}
			seen[keyword] = struct{}{}
			merged = append(merged, keyword)
		}
	}
	return merged
}

func formatFTSKeywords(keywords []string, prefix bool) []string {
	terms := make([]string, 0, len(keywords))
	for _, keyword := range keywords {
		if keyword == "" {
			continue
		}
		term := keyword
		if prefix && isASCIIAlphaNum(keyword) && len(keyword) > 2 {
			term += "*"
		}
		terms = append(terms, term)
	}
	return terms
}

var lexicalQueryStopwords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {}, "by": {},
	"does": {}, "for": {}, "from": {}, "how": {}, "in": {}, "into": {}, "is": {}, "it": {},
	"of": {}, "on": {}, "or": {}, "that": {}, "the": {}, "their": {}, "there": {}, "these": {},
	"this": {}, "to": {}, "was": {}, "were": {}, "what": {}, "when": {}, "where": {}, "which": {},
	"who": {}, "why": {}, "with": {},
}

func isASCIIAlphaNum(value string) bool {
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return value != ""
}

func isAllZero(values []float32) bool {
	for _, value := range values {
		if value != 0 {
			return false
		}
	}
	return true
}

func scoredEmbeddingsOrder(results []core.ScoredEmbedding) []string {
	ordered := make([]string, 0, len(results))
	for _, result := range results {
		ordered = append(ordered, result.ID)
	}
	return ordered
}

func sortIDsByScore(scoreMap map[string]float64) []string {
	ids := make([]string, 0, len(scoreMap))
	for id := range scoreMap {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if scoreMap[ids[i]] == scoreMap[ids[j]] {
			return ids[i] < ids[j]
		}
		return scoreMap[ids[i]] > scoreMap[ids[j]]
	})
	return ids
}

func sortedKeysFromSet(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Strings(keys)
	return keys
}
