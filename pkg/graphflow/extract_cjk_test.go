package graphflow

import (
	"context"
	"strings"
	"testing"
)

// The capitalised-word pattern that finds entities only sees the Latin alphabet. On a
// Chinese corpus every match is romanisation, so the graph used to fill with pinyin
// syllables ("Wng", "Qn", "Gu Dr") while not one real concept was extracted.

func TestHeuristicExtractorSkipsRomanisationOnChineseText(t *testing.T) {
	doc := SourceDocument{
		ID:      "yuwen-3",
		Title:   "义务教育教科书·语文三年级上册",
		Content: "Qiū Tiān De Yǔ\n秋天的雨，是一把钥匙。\nWǔ Cǎi Bīn Fēn\n它有一盒五彩缤纷的颜料。\n",
	}
	result, err := HeuristicExtractor{}.Extract(context.Background(), doc)
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}
	for _, node := range result.Nodes {
		if node.Type != "entity" {
			continue
		}
		t.Errorf("Chinese text produced entity %q (%s) — romanisation is not a concept",
			node.Label, node.ID)
	}
	// The document node is still there, so chunking and mention structure keep working.
	if len(result.Nodes) == 0 || result.Nodes[0].Type != "document" {
		t.Fatalf("document node missing: %+v", result.Nodes)
	}
}

func TestHeuristicExtractorKeepsEntitiesInLatinText(t *testing.T) {
	doc := SourceDocument{
		ID:      "calculus",
		Title:   "The Calculus Lifesaver",
		Content: "The Chain Rule applies when Composition of Functions appears. See Theorem 3.",
	}
	result, err := HeuristicExtractor{}.Extract(context.Background(), doc)
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}
	labels := make([]string, 0)
	for _, node := range result.Nodes {
		if node.Type == "entity" {
			labels = append(labels, node.Label)
		}
	}
	if len(labels) == 0 {
		t.Fatal("Latin text produced no entities; the heuristic pass was disabled too broadly")
	}
	joined := strings.Join(labels, ",")
	for _, want := range []string{"Chain", "Composition", "Theorem"} {
		if !strings.Contains(joined, want) {
			t.Errorf("entity %q missing from %s", want, joined)
		}
	}
}

func TestRomanisationDetection(t *testing.T) {
	for _, token := range []string{"Qiū", "tiān", "yǔ", "Wǎng", "ǎ"} {
		if !isRomanisation(token) {
			t.Errorf("%q not detected as romanisation", token)
		}
	}
	// Ordinary words carry no tone marks and must survive.
	for _, token := range []string{"Chain", "Theorem", "Rule", "Composition", "AB", "data"} {
		if isRomanisation(token) {
			t.Errorf("%q wrongly detected as romanisation", token)
		}
	}
}
