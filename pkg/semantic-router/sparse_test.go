package semanticrouter

import (
	"context"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		minTerms int
	}{
		{
			name:     "simple text",
			text:     "hello world",
			minTerms: 2,
		},
		{
			name:     "with stop words",
			text:     "the quick brown fox jumps over the lazy dog",
			minTerms: 6,
		},
		{
			name:     "chinese text (space-separated)",
			text:     "我要 退款 这东西 坏了",
			minTerms: 3,
		},
		{
			name:     "mixed",
			text:     "hello 你好 world 世界",
			minTerms: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			terms := tokenize(tt.text)
			if len(terms) < tt.minTerms {
				t.Errorf("tokenize() returned %d terms, expected at least %d", len(terms), tt.minTerms)
			}
		})
	}
}

func TestBM25Encoder(t *testing.T) {
	documents := []string{
		"the quick brown fox",
		"jumps over the lazy dog",
		"hello world test",
	}

	encoder := NewBM25Encoder()
	ctx := context.Background()

	err := encoder.Fit(ctx, documents)
	if err != nil {
		t.Fatalf("Fit() error = %v", err)
	}

	if encoder.totalDocs != len(documents) {
		t.Errorf("totalDocs = %d, want %d", encoder.totalDocs, len(documents))
	}

	if encoder.Vocabulary() == nil {
		t.Error("Vocabulary() returned nil")
	}

	// Test encoding
	vec := encoder.EncodeSparse("quick fox")
	if len(vec) == 0 {
		t.Error("EncodeSparse() returned empty vector")
	}

	// Terms from document should have non-zero scores
	if score, ok := vec["quick"]; !ok || score <= 0 {
		t.Errorf("Expected positive score for 'quick', got %v", score)
	}
}

func TestBM25EncoderWithParams(t *testing.T) {
	encoder := NewBM25EncoderWithParams(1.5, 0.8)

	if encoder.k1 != 1.5 {
		t.Errorf("k1 = %v, want 1.5", encoder.k1)
	}
	if encoder.b != 0.8 {
		t.Errorf("b = %v, want 0.8", encoder.b)
	}
}

func TestBM25EncoderBatch(t *testing.T) {
	documents := []string{
		"the quick brown fox",
		"jumps over the lazy dog",
	}

	encoder := NewBM25Encoder()
	ctx := context.Background()
	encoder.Fit(ctx, documents)

	texts := []string{"quick", "lazy"}
	results := encoder.EncodeSparseBatch(texts)

	if len(results) != len(texts) {
		t.Fatalf("EncodeSparseBatch() returned %d results, want %d", len(results), len(texts))
	}

	for i, vec := range results {
		if len(vec) == 0 {
			t.Errorf("Result %d is empty", i)
		}
	}
}

func TestTFIDFEncoder(t *testing.T) {
	documents := []string{
		"the quick brown fox",
		"jumps over the lazy dog",
		"hello world test",
	}

	encoder := NewTFIDFEncoder()
	ctx := context.Background()

	err := encoder.Fit(ctx, documents)
	if err != nil {
		t.Fatalf("Fit() error = %v", err)
	}

	if encoder.totalDocs != len(documents) {
		t.Errorf("totalDocs = %d, want %d", encoder.totalDocs, len(documents))
	}

	// Test encoding
	vec := encoder.EncodeSparse("quick fox")
	if len(vec) == 0 {
		t.Error("EncodeSparse() returned empty vector")
	}

	// Terms from document should have non-zero scores
	if score, ok := vec["quick"]; !ok || score <= 0 {
		t.Errorf("Expected positive score for 'quick', got %v", score)
	}
}

func TestTFIDFEncoderSublinearTF(t *testing.T) {
	documents := []string{
		"test test test repeated",
		"another document",
	}

	encoder := NewTFIDFEncoderWithSublinearTF()
	ctx := context.Background()
	encoder.Fit(ctx, documents)

	vec := encoder.EncodeSparse("test test")
	if len(vec) == 0 {
		t.Error("EncodeSparse() returned empty vector")
	}

	// With sublinear TF, repeated terms should have sublinear scaling
	score, ok := vec["test"]
	if !ok || score <= 0 {
		t.Errorf("Expected positive score for 'test', got %v", score)
	}
}

func TestTFIDFEncoderBatch(t *testing.T) {
	documents := []string{
		"the quick brown fox",
		"jumps over the lazy dog",
	}

	encoder := NewTFIDFEncoder()
	ctx := context.Background()
	encoder.Fit(ctx, documents)

	texts := []string{"quick", "lazy"}
	results := encoder.EncodeSparseBatch(texts)

	if len(results) != len(texts) {
		t.Fatalf("EncodeSparseBatch() returned %d results, want %d", len(results), len(texts))
	}
}

func TestSparseSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a        map[string]float64
		b        map[string]float64
		wantZero bool
	}{
		{
			name:     "identical vectors",
			a:        map[string]float64{"hello": 1.0, "world": 1.0},
			b:        map[string]float64{"hello": 1.0, "world": 1.0},
			wantZero: false,
		},
		{
			name:     "orthogonal vectors",
			a:        map[string]float64{"hello": 1.0},
			b:        map[string]float64{"world": 1.0},
			wantZero: true,
		},
		{
			name:     "overlapping vectors",
			a:        map[string]float64{"hello": 1.0, "world": 1.0},
			b:        map[string]float64{"hello": 1.0, "test": 1.0},
			wantZero: false,
		},
		{
			name:     "empty vectors",
			a:        map[string]float64{},
			b:        map[string]float64{},
			wantZero: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sim := SparseSimilarity(tt.a, tt.b)
			if tt.wantZero && sim > 0 {
				t.Errorf("SparseSimilarity() = %v, want ~0", sim)
			}
			if !tt.wantZero && sim == 0 {
				t.Errorf("SparseSimilarity() = 0, want > 0")
			}
		})
	}
}

func TestTopTerms(t *testing.T) {
	vec := map[string]float64{
		"hello": 0.1,
		"world": 0.9,
		"test":  0.5,
		"foo":   0.3,
	}

	// Get top 2
	terms := TopTerms(vec, 2)

	if len(terms) != 2 {
		t.Fatalf("TopTerms() returned %d terms, want 2", len(terms))
	}

	// Should be sorted by score descending
	if terms[0].Term != "world" || terms[0].Score != 0.9 {
		t.Errorf("Expected 'world' with score 0.9 first, got %v", terms[0])
	}

	if terms[1].Term != "test" || terms[1].Score != 0.5 {
		t.Errorf("Expected 'test' with score 0.5 second, got %v", terms[1])
	}
}

func TestTopTermsAll(t *testing.T) {
	vec := map[string]float64{
		"hello": 0.1,
		"world": 0.9,
		"test":  0.5,
	}

	// Get all terms (k=0 or k > len(vec))
	terms := TopTerms(vec, 0)

	if len(terms) != 3 {
		t.Fatalf("TopTerms() returned %d terms, want 3", len(terms))
	}
}

func TestHybridEmbedder(t *testing.T) {
	dense := NewMockEmbedder(128)
	sparse := NewTFIDFEncoder()

	documents := []string{
		"the quick brown fox",
		"jumps over the lazy dog",
		"hello world test",
	}
	ctx := context.Background()
	tfidf := sparse
	tfidf.Fit(ctx, documents)

	hybrid := NewHybridEmbedder(dense, sparse)

	if hybrid.Dimensions() != 128 {
		t.Errorf("Dimensions() = %d, want 128", hybrid.Dimensions())
	}

	if hybrid.GetAlpha() != 0.5 {
		t.Errorf("GetAlpha() = %v, want 0.5", hybrid.GetAlpha())
	}

	// Test SetAlpha
	hybrid.SetAlpha(0.7)
	if hybrid.GetAlpha() != 0.7 {
		t.Errorf("SetAlpha(0.7) -> GetAlpha() = %v, want 0.7", hybrid.GetAlpha())
	}

	// Test sparse encoding
	sparseVec := hybrid.EncodeSparse("quick fox")
	if len(sparseVec) == 0 {
		t.Error("EncodeSparse() returned empty vector")
	}
}

func TestHybridEmbedderWithAlpha(t *testing.T) {
	dense := NewMockEmbedder(64)
	sparse := NewBM25Encoder()

	hybrid := NewHybridEmbedderWithAlpha(dense, sparse, 0.8)

	if hybrid.GetAlpha() != 0.8 {
		t.Errorf("GetAlpha() = %v, want 0.8", hybrid.GetAlpha())
	}
}

func TestHybridEmbedderDenseCompatibility(t *testing.T) {
	dense := NewMockEmbedder(32)
	sparse := NewTFIDFEncoder()

	documents := []string{"test document"}
	ctx := context.Background()
	tfidf := sparse
	tfidf.Fit(ctx, documents)

	hybrid := NewHybridEmbedder(dense, sparse)

	// Test that it implements Embedder interface
	vec, err := hybrid.Embed(ctx, "test")
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}

	if len(vec) != 32 {
		t.Errorf("Embed() returned vector of length %d, want 32", len(vec))
	}

	// Test batch
	vecs, err := hybrid.EmbedBatch(ctx, []string{"test", "hello"})
	if err != nil {
		t.Fatalf("EmbedBatch() error = %v", err)
	}

	if len(vecs) != 2 {
		t.Fatalf("EmbedBatch() returned %d vectors, want 2", len(vecs))
	}
}

func TestHybridSimilarity(t *testing.T) {
	dense := NewMockEmbedder(16)
	bm25 := NewBM25Encoder()

	documents := []string{"test document hello world"}
	ctx := context.Background()
	bm25.Fit(ctx, documents)

	hybrid := NewHybridEmbedder(dense, bm25)

	// Create some test vectors
	queryDense, _ := dense.Embed(ctx, "hello")
	compareDense, _ := dense.Embed(ctx, "hello")

	querySparse := bm25.EncodeSparse("hello world")
	compareSparse := bm25.EncodeSparse("hello world")

	// Compute hybrid similarity
	sim := hybrid.HybridSimilarity(queryDense, querySparse, compareDense, compareSparse, nil)

	if sim < 0 || sim > 1 {
		t.Errorf("HybridSimilarity() = %v, want value in [0, 1]", sim)
	}
}

func TestBM25EncoderChinese(t *testing.T) {
	// Note: Current tokenizer splits on whitespace, so Chinese phrases
	// without spaces are treated as single tokens
	documents := []string{
		"退款 坏了 还钱",
		"你好 天气 真好",
		"测试 文档",
	}

	encoder := NewBM25Encoder()
	ctx := context.Background()

	err := encoder.Fit(ctx, documents)
	if err != nil {
		t.Fatalf("Fit() error = %v", err)
	}

	// Test encoding
	vec := encoder.EncodeSparse("退款 测试")
	if len(vec) == 0 {
		t.Error("EncodeSparse() returned empty vector")
	}

	// "退款" should have a score
	if score, ok := vec["退款"]; ok && score <= 0 {
		t.Errorf("Expected positive score for '退款', got %v", score)
	}
}

func TestTFIDFEncoderChinese(t *testing.T) {
	// Note: Current tokenizer splits on whitespace
	documents := []string{
		"退款 坏了",
		"你好 天气",
		"测试 文档",
	}

	encoder := NewTFIDFEncoder()
	ctx := context.Background()

	err := encoder.Fit(ctx, documents)
	if err != nil {
		t.Fatalf("Fit() error = %v", err)
	}

	vec := encoder.EncodeSparse("退款 测试")
	if len(vec) == 0 {
		t.Error("EncodeSparse() returned empty vector")
	}

	if score, ok := vec["退款"]; ok && score <= 0 {
		t.Errorf("Expected positive score for '退款', got %v", score)
	}
}
