package graphflow

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var (
	graphflowEntityPattern   = regexp.MustCompile(`\b[A-Z][A-Za-z0-9_]{1,}\b`)
	graphflowBacktickPattern = regexp.MustCompile("`([^`]+)`")
	// Pinyin/romaji syllables: short, no Han characters, carrying a tone mark. A primary
	// school textbook prints them above every new character, and the capitalised-word
	// pattern above then reads them as entities.
	graphflowRomanisationPattern = regexp.MustCompile(`^[A-Za-z]*[āáǎàēéěèīíǐìōóǒòūúǔùǖǘǚǜüńň][A-Za-zāáǎàēéěèīíǐìōóǒòūúǔùǖǘǚǜüńň]*$`)
)

// containsCJK reports whether the text holds Han, Hiragana, Katakana or Hangul.
func containsCJK(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			return true
		}
	}
	return false
}

// HeuristicExtractor is a deterministic extractor that emits a basic node/edge graph from text and code.
type HeuristicExtractor struct{}

// Extract emits a document node, entity nodes, mention edges, and simple co-occurrence edges.
func (HeuristicExtractor) Extract(_ context.Context, doc SourceDocument) (*ExtractionResult, error) {
	if strings.TrimSpace(doc.ID) == "" {
		return nil, fmt.Errorf("source document id is required")
	}

	docNodeID := "doc:" + doc.ID
	nodes := []ExtractionNode{{
		ID:         docNodeID,
		Label:      firstNonEmpty(doc.Title, doc.ID),
		Type:       "document",
		Summary:    summarize(doc.Content, 140),
		SourceFile: doc.Path,
		Metadata:   cloneStringMap(doc.Metadata),
	}}
	nodeSet := map[string]struct{}{docNodeID: {}}
	edges := make([]ExtractionEdge, 0)

	entityNames := extractEntities(doc.Content)
	for _, name := range entityNames {
		entityID := "entity:" + normalizeLabel(name)
		if _, ok := nodeSet[entityID]; !ok {
			nodes = append(nodes, ExtractionNode{
				ID:         entityID,
				Label:      name,
				Type:       "entity",
				SourceFile: doc.Path,
			})
			nodeSet[entityID] = struct{}{}
		}
		edges = append(edges, ExtractionEdge{
			Source:     docNodeID,
			Target:     entityID,
			Relation:   "mentions",
			Confidence: ConfidenceExtracted,
			Directed:   true,
			SourceFile: doc.Path,
		})
	}

	for _, sentence := range splitSentences(doc.Content) {
		entities := extractEntities(sentence)
		for i := 0; i < len(entities)-1; i++ {
			source := "entity:" + normalizeLabel(entities[i])
			target := "entity:" + normalizeLabel(entities[i+1])
			if source == target {
				continue
			}
			edges = append(edges, ExtractionEdge{
				Source:     source,
				Target:     target,
				Relation:   "co_occurs",
				Confidence: ConfidenceInferred,
				Directed:   false,
				SourceFile: doc.Path,
			})
		}
	}

	result := &ExtractionResult{
		SourceID:   doc.ID,
		SourceType: doc.Type,
		Title:      doc.Title,
		Nodes:      nodes,
		Edges:      dedupeEdges(edges),
		Metadata:   cloneStringMap(doc.Metadata),
	}
	if err := ValidateExtraction(result); err != nil {
		return nil, err
	}
	return result, nil
}

// isRomanisation reports whether a token looks like a pinyin/romaji syllable rather than
// a name worth putting in a graph.
func isRomanisation(token string) bool {
	if len([]rune(token)) > 6 {
		return false
	}
	return graphflowRomanisationPattern.MatchString(token)
}

func extractEntities(text string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	// The capitalised-word pattern only sees the Latin alphabet, so on CJK text it cannot
	// find real entities — everything it matches is romanisation or stray Latin. Emitting
	// those filled graphs with syllable fragments ("Wng", "Qn") in place of concepts, so
	// for predominantly CJK documents the Latin pass is skipped and extraction is left to
	// an LLM extractor. Backtick spans below are explicit markup and still honoured.
	cjk := containsCJK(text)
	for _, match := range graphflowEntityPattern.FindAllString(text, -1) {
		if _, ok := seen[match]; ok {
			continue
		}
		if cjk || isRomanisation(match) {
			continue
		}
		seen[match] = struct{}{}
		out = append(out, match)
	}
	for _, groups := range graphflowBacktickPattern.FindAllStringSubmatch(text, -1) {
		if len(groups) < 2 {
			continue
		}
		value := strings.TrimSpace(groups[1])
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func dedupeEdges(edges []ExtractionEdge) []ExtractionEdge {
	seen := make(map[string]struct{}, len(edges))
	out := make([]ExtractionEdge, 0, len(edges))
	for _, edge := range edges {
		key := edge.Source + "|" + edge.Relation + "|" + edge.Target + "|" + fmt.Sprint(edge.Directed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, edge)
	}
	return out
}

func summarize(text string, max int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if len(text) <= max {
		return text
	}
	return text[:max] + "..."
}

func splitSentences(text string) []string {
	fields := strings.FieldsFunc(strings.ReplaceAll(text, "\n", " "), func(r rune) bool {
		return r == '.' || r == '!' || r == '?'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func normalizeLabel(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, ":", "_")
	return value
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
