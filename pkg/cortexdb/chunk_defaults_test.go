package cortexdb

import (
	"strings"
	"testing"
)

// Unset, a document is chunked at 300 words with a 50-word overlap, so a
// question and the answer that follows it in a conversation share a chunk
// (#248); a size given without an overlap keeps no overlap.
func TestDefaultChunkingIs300WordsWithOverlap(t *testing.T) {
	opts := GraphRAGIngestOptions{}
	applyGraphRAGIngestDefaults(&opts)
	if opts.ChunkSize != 300 || opts.ChunkOverlap != 50 {
		t.Fatalf("defaults = %d words, %d overlap; want 300 and 50", opts.ChunkSize, opts.ChunkOverlap)
	}
	given := GraphRAGIngestOptions{ChunkSize: 120}
	applyGraphRAGIngestDefaults(&given)
	if given.ChunkSize != 120 || given.ChunkOverlap != 0 {
		t.Fatalf("a given size became %d words, %d overlap", given.ChunkSize, given.ChunkOverlap)
	}

	words := make([]string, 0, 700)
	for i := 0; i < 700; i++ {
		words = append(words, "w")
		if i%20 == 19 {
			words[len(words)-1] += "."
		}
	}
	chunks := splitGraphRAGText(strings.Join(words, " "), opts.ChunkSize, opts.ChunkOverlap)
	if len(chunks) < 2 || len(chunks) > 3 {
		t.Fatalf("700 words became %d chunks at the default size", len(chunks))
	}
}

// Unset, the context budget holds TopK default-sized chunks: 2,000
// characters per result, not 2,400 in all.
func TestDefaultContextBudgetScalesWithTopK(t *testing.T) {
	for _, topK := range []int{0, 4, 10} {
		opts := GraphRAGQueryOptions{TopK: topK}
		applyGraphRAGQueryDefaults(&opts)
		if want := opts.TopK * 2000; opts.MaxContextChars != want {
			t.Fatalf("TopK %d: MaxContextChars = %d, want %d", opts.TopK, opts.MaxContextChars, want)
		}
	}
	given := GraphRAGQueryOptions{TopK: 4, MaxContextChars: 1200}
	applyGraphRAGQueryDefaults(&given)
	if given.MaxContextChars != 1200 {
		t.Fatalf("a given budget became %d", given.MaxContextChars)
	}
}
