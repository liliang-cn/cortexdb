package sqvect

import (
	"context"
	"errors"
)

// Embedder defines the interface for text-to-vector embedding.
// Users can implement this interface to integrate any embedding model
// (OpenAI, Ollama, local models, etc.) with sqvect.
type Embedder interface {
	// Embed converts a single text string into a vector.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch converts multiple texts into vectors in a single call.
	// This is optional but recommended for better performance with batch operations.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Dim returns the dimension of vectors produced by this embedder.
	Dim() int
}

// Errors related to embedder operations
var (
	// ErrEmbedderNotConfigured is returned when text operations are called
	// but no embedder was configured during initialization.
	ErrEmbedderNotConfigured = errors.New("sqvect: embedder not configured, use WithEmbedder option or call vector methods directly")

	// ErrEmptyText is returned when an empty text string is provided.
	ErrEmptyText = errors.New("sqvect: empty text provided")

	// ErrEmbeddingFailed is returned when the embedder fails to produce a vector.
	ErrEmbeddingFailed = errors.New("sqvect: embedding failed")
)

// BaseEmbedder provides a default implementation of EmbedBatch that calls Embed for each text.
// Embedders can embed this to get batch support for free.
type BaseEmbedder struct {
	embedFn func(ctx context.Context, text string) ([]float32, error)
	dimFn   func() int
}

// Embed calls the underlying embed function for a single text.
func (b *BaseEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return b.embedFn(ctx, text)
}

// EmbedBatch provides a default batch implementation using goroutines.
func (b *BaseEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	errs := make([]error, len(texts))

	type result struct {
		idx int
		vec []float32
		err error
	}

	ch := make(chan result, len(texts))

	for i, text := range texts {
		go func(idx int, t string) {
			vec, err := b.embedFn(ctx, t)
			ch <- result{idx: idx, vec: vec, err: err}
		}(i, text)
	}

	for range texts {
		r := <-ch
		results[r.idx] = r.vec
		errs[r.idx] = r.err
	}

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}

	return results, nil
}

// Dim returns the dimension of vectors.
func (b *BaseEmbedder) Dim() int {
	return b.dimFn()
}
