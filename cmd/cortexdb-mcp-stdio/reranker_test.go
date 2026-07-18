package main

import (
	"strings"
	"testing"
)

func TestParseRerankScoresCohereShape(t *testing.T) {
	// Cohere/Jina/vLLM: {"results":[{index,relevance_score}]}, reordered.
	body := `{"results":[{"index":2,"relevance_score":0.9},{"index":0,"relevance_score":0.1},{"index":1,"relevance_score":0.5}]}`
	got, err := parseRerankScores(strings.NewReader(body), 3)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []float64{0.1, 0.5, 0.9}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %v want %v (full %v)", i, got[i], want[i], got)
		}
	}
}

func TestParseRerankScoresTEIShape(t *testing.T) {
	// TEI: bare array of {index,score}.
	body := `[{"index":0,"score":0.3},{"index":1,"score":0.7}]`
	got, err := parseRerankScores(strings.NewReader(body), 2)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got[0] != 0.3 || got[1] != 0.7 {
		t.Fatalf("got %v", got)
	}
}

func TestParseRerankScoresEmptyIsError(t *testing.T) {
	if _, err := parseRerankScores(strings.NewReader(`{"results":[]}`), 2); err == nil {
		t.Fatalf("expected error on empty results")
	}
}
