package graphflow

import (
	"context"
	"testing"
)

func TestHeuristicExtractor(t *testing.T) {
	extractor := HeuristicExtractor{}
	result, err := extractor.Extract(context.Background(), SourceDocument{
		ID:      "doc-1",
		Type:    "markdown",
		Title:   "Apollo Plan",
		Content: "Alice works on Apollo. `DeadlineManager` coordinates Friday.",
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(result.Nodes) < 2 || len(result.Edges) == 0 {
		t.Fatalf("expected heuristic graph, got %+v", result)
	}
}

type fakeJSONGenerator struct {
	payload []byte
}

func (f fakeJSONGenerator) GenerateJSON(_ context.Context, _ string, _ string) ([]byte, error) {
	return f.payload, nil
}

func TestLLMExtractor(t *testing.T) {
	extractor := LLMExtractor{
		Client: fakeJSONGenerator{
			payload: []byte(`{
				"nodes":[
					{"id":"doc:1","label":"Doc","type":"document"},
					{"id":"entity:apollo","label":"Apollo","type":"entity"}
				],
				"edges":[
					{"source":"doc:1","target":"entity:apollo","relation":"mentions","confidence":"EXTRACTED","directed":true}
				]
			}`),
		},
	}
	result, err := extractor.Extract(context.Background(), SourceDocument{
		ID:      "doc-1",
		Type:    "markdown",
		Title:   "Apollo Plan",
		Content: "Apollo is important.",
	})
	if err != nil {
		t.Fatalf("llm extract: %v", err)
	}
	if result.SourceID != "doc-1" || len(result.Nodes) != 2 {
		t.Fatalf("unexpected llm result: %+v", result)
	}
}
