// Package semantic-router provides a semantic routing layer for LLM applications.
// It uses vector similarity to classify user queries and route them to appropriate handlers
// before invoking expensive LLM calls.
package semanticrouter

import (
	"context"
	"sync"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
)

// Route represents a single semantic route with associated example utterances.
type Route struct {
	// Name is the unique identifier for this route
	Name string `json:"name"`

	// Utterances are example phrases that represent this route's intent
	Utterances []string `json:"utterances"`

	// Handler is an optional function to execute when this route is matched
	Handler RouteHandler `json:"-"`

	// Metadata stores additional information about the route
	Metadata map[string]string `json:"metadata,omitempty"`

	// cachedVectors stores pre-computed embeddings for utterances
	cachedVectors [][]float32
	cachedOnce    sync.Once
	cachedErr     error
}

// RouteHandler is a function that handles a matched route.
// It receives the original query and the confidence score.
type RouteHandler func(ctx context.Context, query string, score float64) (string, error)

// RouteResult represents the result of a routing decision.
type RouteResult struct {
	// RouteName is the name of the matched route, empty if no match
	RouteName string `json:"routeName"`

	// Score is the similarity score (0.0 to 1.0)
	Score float64 `json:"score"`

	// Matched indicates whether the score exceeded the threshold
	Matched bool `json:"matched"`

	// Handler is the matched route's handler function
	Handler RouteHandler `json:"-"`
}

// Embedder is the interface for computing text embeddings.
type Embedder interface {
	// Embed converts a single text string into a vector embedding.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch converts multiple text strings into vector embeddings.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Dimensions returns the dimensionality of the embedding vectors.
	Dimensions() int
}

// Config holds configuration for the semantic router.
type Config struct {
	// Threshold is the minimum similarity score to consider a route matched (default: 0.82)
	Threshold float64 `json:"threshold"`

	// SimilarityFunc is the function used to compute vector similarity (default: cosine)
	// Use core.CosineSimilarity, core.DotProduct, or core.EuclideanDist
	SimilarityFunc core.SimilarityFunc `json:"-"`

	// TopK is the number of top results to consider when finding routes (default: 1)
	TopK int `json:"topK"`

	// CacheEmbeddings enables caching of utterance embeddings (default: true)
	CacheEmbeddings bool `json:"cacheEmbeddings"`
}

// DefaultConfig returns a configuration with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Threshold:       0.82,
		SimilarityFunc:  core.CosineSimilarity,
		TopK:            1,
		CacheEmbeddings: true,
	}
}
