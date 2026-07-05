package cortexdb

import (
	"strings"
	"testing"
)

func TestSplitGraphRAGTextSentenceAware(t *testing.T) {
	// Multi-sentence text over the word budget must split on sentence
	// boundaries, never mid-sentence.
	text := "Alpha bravo charlie delta echo. Foxtrot golf hotel india juliet. Kilo lima mike november oscar. Papa quebec romeo sierra tango."
	chunks := splitGraphRAGText(text, 10, 0)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d: %v", len(chunks), chunks)
	}
	for _, c := range chunks {
		c = strings.TrimSpace(c)
		if c == "" {
			t.Fatalf("empty chunk")
		}
		// Each chunk should end at a sentence terminator (no mid-sentence cut).
		last := c[len(c)-1]
		if last != '.' && last != '!' && last != '?' {
			t.Errorf("chunk not sentence-aligned (ends %q): %q", string(last), c)
		}
		if got := len(strings.Fields(c)); got > 10 {
			t.Errorf("chunk exceeds word budget (%d): %q", got, c)
		}
	}
}

func TestSplitGraphRAGTextOverlap(t *testing.T) {
	text := "One two three four five. Six seven eight nine ten. Eleven twelve thirteen fourteen fifteen."
	chunks := splitGraphRAGText(text, 10, 5)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	// With overlap, the start of chunk 2 should repeat the tail sentence of chunk 1.
	if !strings.Contains(chunks[1], "Six seven eight nine ten.") {
		t.Errorf("expected overlap sentence carried into chunk 2, got: %q", chunks[1])
	}
}

func TestSplitGraphRAGTextOversizedSentence(t *testing.T) {
	// A single sentence longer than the budget must still be split (word window).
	long := strings.Repeat("word ", 30) // 30 words, no terminator
	chunks := splitGraphRAGText(long, 10, 2)
	if len(chunks) < 2 {
		t.Fatalf("oversized single sentence should be word-windowed, got %d chunks", len(chunks))
	}
	for _, c := range chunks {
		if got := len(strings.Fields(c)); got > 10 {
			t.Errorf("word-window chunk exceeds budget (%d)", got)
		}
	}
}

func TestSplitGraphRAGTextShortReturnsOne(t *testing.T) {
	if got := splitGraphRAGText("just a short sentence.", 120, 0); len(got) != 1 {
		t.Errorf("short text should be one chunk, got %d", len(got))
	}
	if got := splitGraphRAGText("   ", 120, 0); got != nil {
		t.Errorf("blank text should be nil, got %v", got)
	}
}
