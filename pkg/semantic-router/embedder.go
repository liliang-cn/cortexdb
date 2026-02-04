package semanticrouter

import (
	"context"
	"fmt"
)

// MockEmbedder is a simple embedder for testing purposes.
// It generates deterministic pseudo-random vectors based on input text.
type MockEmbedder struct {
	dimensions int
}

// NewMockEmbedder creates a mock embedder for testing.
func NewMockEmbedder(dimensions int) *MockEmbedder {
	return &MockEmbedder{
		dimensions: dimensions,
	}
}

// Embed generates a deterministic vector for the given text.
func (m *MockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vec := make([]float32, m.dimensions)

	// Simple hash-based generation for deterministic results
	hash := 0
	for _, c := range text {
		hash = hash*31 + int(c)
	}

	seed := uint32(hash)
	for i := range vec {
		// Generate a pseudo-random value between -1 and 1
		seed = seed*1664525 + 1013904223
		vec[i] = float32(int32(seed))/float32(0x7fffffff) * 2
	}

	return vec, nil
}

// EmbedBatch generates embeddings for multiple texts.
func (m *MockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := m.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		vectors[i] = vec
	}
	return vectors, nil
}

// Dimensions returns the embedding dimension.
func (m *MockEmbedder) Dimensions() int {
	return m.dimensions
}

// CachedEmbedder wraps another embedder and caches results.
type CachedEmbedder struct {
	embedder Embedder
	cache    map[string][]float32
}

// NewCachedEmbedder creates a new cached embedder.
func NewCachedEmbedder(embedder Embedder) *CachedEmbedder {
	return &CachedEmbedder{
		embedder: embedder,
		cache:    make(map[string][]float32),
	}
}

// Embed returns a cached embedding if available, otherwise computes and caches it.
func (c *CachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if vec, ok := c.cache[text]; ok {
		return vec, nil
	}

	vec, err := c.embedder.Embed(ctx, text)
	if err != nil {
		return nil, err
	}

	c.cache[text] = vec
	return vec, nil
}

// EmbedBatch embeds multiple texts, using cache where available.
func (c *CachedEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	uncached := make(map[int]string) // index -> text

	// Check cache first
	for i, text := range texts {
		if vec, ok := c.cache[text]; ok {
			vectors[i] = vec
		} else {
			uncached[i] = text
		}
	}

	// Embed uncached texts
	if len(uncached) > 0 {
		uncachedTexts := make([]string, 0, len(uncached))
		indexMap := make(map[int]int) // batch index -> original index
		for origIdx, text := range uncached {
			indexMap[len(uncachedTexts)] = origIdx
			uncachedTexts = append(uncachedTexts, text)
		}

		batchVecs, err := c.embedder.EmbedBatch(ctx, uncachedTexts)
		if err != nil {
			// Fall back to individual embeddings
			for origIdx, text := range uncached {
				vec, err := c.embedder.Embed(ctx, text)
				if err != nil {
					return nil, fmt.Errorf("failed to embed %q: %w", text, err)
				}
				vectors[origIdx] = vec
				c.cache[text] = vec
			}
			return vectors, nil
		}

		for batchIdx, vec := range batchVecs {
			origIdx := indexMap[batchIdx]
			vectors[origIdx] = vec
			c.cache[uncachedTexts[batchIdx]] = vec
		}
	}

	return vectors, nil
}

// Dimensions returns the embedding dimension.
func (c *CachedEmbedder) Dimensions() int {
	return c.embedder.Dimensions()
}

// ClearCache clears the embedding cache.
func (c *CachedEmbedder) ClearCache() {
	c.cache = make(map[string][]float32)
}

// CacheSize returns the number of cached embeddings.
func (c *CachedEmbedder) CacheSize() int {
	return len(c.cache)
}
