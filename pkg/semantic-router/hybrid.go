package semanticrouter

import (
	"context"
	"fmt"
	"sync"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
)

// HybridRouter combines dense vector embeddings with sparse keyword matching.
// It uses a weighted combination of both similarity scores for routing decisions.
type HybridRouter struct {
	dense       *Router
	denseMu     sync.RWMutex

	sparse      *SparseRouter
	sparseMu    sync.RWMutex

	alpha       float64 // Weight for dense similarity (0-1)
	threshold   float64

	// Similarity function for dense vectors
	similarityFunc core.SimilarityFunc
}

// HybridRouteResult extends RouteResult with separate dense and sparse scores.
type HybridRouteResult struct {
	RouteResult

	// DenseScore is the similarity from dense vector matching
	DenseScore float64 `json:"denseScore"`

	// SparseScore is the similarity from sparse keyword matching
	SparseScore float64 `json:"sparseScore"`

	// CombinedScore is the weighted combination score
	CombinedScore float64 `json:"combinedScore"`
}

// SparseRouter handles routing using sparse encoders (BM25, TFIDF).
type SparseRouter struct {
	routes    []*SparseRoute
	encoder   SparseEncoder
	config    SparseConfig
	mu        sync.RWMutex
}

// SparseRoute represents a route for sparse encoding.
type SparseRoute struct {
	Name         string
	Utterances   []string
	Handler      RouteHandler
	Metadata     map[string]string

	cachedVectors map[string]float64
	cachedOnce    sync.Once
	cachedErr     error
}

// SparseConfig holds configuration for sparse router.
type SparseConfig struct {
	Threshold float64
	TopK      int
}

// DefaultSparseConfig returns default sparse router configuration.
func DefaultSparseConfig() SparseConfig {
	return SparseConfig{
		Threshold: 0.3, // Lower threshold for sparse matching
		TopK:      1,
	}
}

// NewSparseRouter creates a new sparse router.
func NewSparseRouter(encoder SparseEncoder, opts ...SparseConfigOption) (*SparseRouter, error) {
	if encoder == nil {
		return nil, fmt.Errorf("encoder cannot be nil")
	}

	config := DefaultSparseConfig()
	for _, opt := range opts {
		opt(&config)
	}

	return &SparseRouter{
		routes:  make([]*SparseRoute, 0),
		encoder: encoder,
		config:  config,
	}, nil
}

// SparseConfigOption modifies sparse router configuration.
type SparseConfigOption func(*SparseConfig)

// WithSparseThreshold sets the threshold for sparse router.
func WithSparseThreshold(threshold float64) SparseConfigOption {
	return func(c *SparseConfig) {
		c.Threshold = threshold
	}
}

// WithSparseTopK sets the top-k for sparse router.
func WithSparseTopK(k int) SparseConfigOption {
	return func(c *SparseConfig) {
		c.TopK = k
	}
}

// Add adds a route to the sparse router.
func (r *SparseRouter) Add(route *SparseRoute) error {
	if route == nil {
		return fmt.Errorf("route cannot be nil")
	}
	if route.Name == "" {
		return fmt.Errorf("route name cannot be empty")
	}
	if len(route.Utterances) == 0 {
		return fmt.Errorf("route must have at least one utterance")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, existing := range r.routes {
		if existing.Name == route.Name {
			return fmt.Errorf("route %q already exists", route.Name)
		}
	}

	r.routes = append(r.routes, route)
	return nil
}

// Route performs sparse routing.
func (r *SparseRouter) Route(ctx context.Context, query string) (*RouteResult, error) {
	if query == "" {
		return &RouteResult{}, nil
	}

	queryVec := r.encoder.EncodeSparse(query)

	r.mu.RLock()
	defer r.mu.RUnlock()

	var bestRoute *SparseRoute
	var maxScore float64

	for _, route := range r.routes {
		vectors, err := r.getRouteVectors(route)
		if err != nil {
			continue
		}

		for _, vec := range vectors {
			score := SparseSimilarity(queryVec, vec)
			if score > maxScore {
				maxScore = score
				bestRoute = route
			}
		}
	}

	result := &RouteResult{
		Score: maxScore,
	}

	if bestRoute != nil {
		result.RouteName = bestRoute.Name
		result.Handler = bestRoute.Handler
		result.Matched = maxScore >= r.config.Threshold
	}

	return result, nil
}

// getRouteVectors returns cached or computed sparse vectors for a route.
func (r *SparseRouter) getRouteVectors(route *SparseRoute) ([]map[string]float64, error) {
	// Compute vectors for all utterances
	vectors := make([]map[string]float64, len(route.Utterances))
	for i, utterance := range route.Utterances {
		vectors[i] = r.encoder.EncodeSparse(utterance)
	}
	return vectors, nil
}

// List returns all route names.
func (r *SparseRouter) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, len(r.routes))
	for i, route := range r.routes {
		names[i] = route.Name
	}
	return names
}

// NewHybridRouter creates a new hybrid router.
func NewHybridRouter(
	dense Embedder,
	sparse SparseEncoder,
	opts ...HybridConfigOption,
) (*HybridRouter, error) {
	if dense == nil {
		return nil, fmt.Errorf("dense embedder cannot be nil")
	}
	if sparse == nil {
		return nil, fmt.Errorf("sparse encoder cannot be nil")
	}

	config := DefaultHybridConfig()
	for _, opt := range opts {
		opt(&config)
	}

	// Create dense router
	denseRouter, err := NewRouter(dense,
		WithThreshold(config.DenseThreshold),
		WithSimilarityFunc(config.SimilarityFunc),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create dense router: %w", err)
	}

	// Create sparse router
	sparseRouter, err := NewSparseRouter(sparse,
		WithSparseThreshold(config.SparseThreshold),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create sparse router: %w", err)
	}

	return &HybridRouter{
		dense:         denseRouter,
		sparse:        sparseRouter,
		alpha:         config.Alpha,
		threshold:     config.Threshold,
		similarityFunc: config.SimilarityFunc,
	}, nil
}

// HybridConfig holds configuration for hybrid router.
type HybridConfig struct {
	// Alpha is weight for dense (0-1). Final = alpha*dense + (1-alpha)*sparse
	Alpha float64

	// Threshold for combined score
	Threshold float64

	// DenseThreshold for dense-only decisions
	DenseThreshold float64

	// SparseThreshold for sparse-only decisions
	SparseThreshold float64

	// Similarity function for dense vectors
	SimilarityFunc core.SimilarityFunc
}

// DefaultHybridConfig returns default hybrid configuration.
func DefaultHybridConfig() HybridConfig {
	return HybridConfig{
		Alpha:           0.7, // Favor dense by default
		Threshold:       0.6,
		DenseThreshold:  0.82,
		SparseThreshold: 0.3,
		SimilarityFunc:  core.CosineSimilarity,
	}
}

// HybridConfigOption modifies hybrid router configuration.
type HybridConfigOption func(*HybridConfig)

// WithHybridAlpha sets the alpha weight.
func WithHybridAlpha(alpha float64) HybridConfigOption {
	return func(c *HybridConfig) {
		c.Alpha = alpha
	}
}

// WithHybridThreshold sets the combined threshold.
func WithHybridThreshold(threshold float64) HybridConfigOption {
	return func(c *HybridConfig) {
		c.Threshold = threshold
	}
}

// WithDenseThreshold sets the dense threshold.
func WithDenseThreshold(threshold float64) HybridConfigOption {
	return func(c *HybridConfig) {
		c.DenseThreshold = threshold
	}
}

// WithHybridSparseThreshold sets the sparse threshold for hybrid router.
func WithHybridSparseThreshold(threshold float64) HybridConfigOption {
	return func(c *HybridConfig) {
		c.SparseThreshold = threshold
	}
}

// WithHybridSimilarityFunc sets the similarity function.
func WithHybridSimilarityFunc(fn core.SimilarityFunc) HybridConfigOption {
	return func(c *HybridConfig) {
		c.SimilarityFunc = fn
	}
}

// Add adds a route to both dense and sparse routers.
func (h *HybridRouter) Add(route *Route, sparseRoute *SparseRoute) error {
	h.denseMu.Lock()
	h.sparseMu.Lock()
	defer h.denseMu.Unlock()
	defer h.sparseMu.Unlock()

	if route != nil {
		if err := h.dense.Add(route); err != nil {
			return fmt.Errorf("failed to add dense route: %w", err)
		}
	}

	if sparseRoute != nil {
		if err := h.sparse.Add(sparseRoute); err != nil {
			return fmt.Errorf("failed to add sparse route: %w", err)
		}
	}

	return nil
}

// AddBatch adds multiple routes.
func (h *HybridRouter) AddBatch(denseRoutes []*Route, sparseRoutes []*SparseRoute) error {
	h.denseMu.Lock()
	h.sparseMu.Lock()
	defer h.denseMu.Unlock()
	defer h.sparseMu.Unlock()

	if len(denseRoutes) > 0 {
		if err := h.dense.AddBatch(denseRoutes); err != nil {
			return fmt.Errorf("failed to add dense routes: %w", err)
		}
	}

	if len(sparseRoutes) > 0 {
		for _, route := range sparseRoutes {
			if err := h.sparse.Add(route); err != nil {
				return fmt.Errorf("failed to add sparse route: %w", err)
			}
		}
	}

	return nil
}

// Route performs hybrid routing using both dense and sparse.
func (h *HybridRouter) Route(ctx context.Context, query string) (*HybridRouteResult, error) {
	if query == "" {
		return &HybridRouteResult{}, nil
	}

	// Get dense result
	denseResult, err := h.dense.Route(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("dense routing failed: %w", err)
	}

	// Get sparse result
	sparseResult, err := h.sparse.Route(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("sparse routing failed: %w", err)
	}

	// Compute combined score
	denseScore := denseResult.Score
	sparseScore := sparseResult.Score
	combinedScore := h.alpha*denseScore + (1-h.alpha)*sparseScore

	result := &HybridRouteResult{
		RouteResult: RouteResult{
			Score:   combinedScore,
			Matched: combinedScore >= h.threshold,
		},
		DenseScore:    denseScore,
		SparseScore:   sparseScore,
		CombinedScore: combinedScore,
	}

	// Determine best route name
	// Prefer the route that matched in either router
	if denseResult.Matched {
		result.RouteResult.RouteName = denseResult.RouteName
		result.RouteResult.Handler = denseResult.Handler
	} else if sparseResult.Matched {
		result.RouteResult.RouteName = sparseResult.RouteName
		result.RouteResult.Handler = sparseResult.Handler
	} else if combinedScore >= h.threshold {
		// Use the route with highest individual score
		if denseScore > sparseScore && denseResult.RouteName != "" {
			result.RouteResult.RouteName = denseResult.RouteName
			result.RouteResult.Handler = denseResult.Handler
		} else if sparseResult.RouteName != "" {
			result.RouteResult.RouteName = sparseResult.RouteName
			result.RouteResult.Handler = sparseResult.Handler
		}
	}

	return result, nil
}

// RouteBatch routes multiple queries.
func (h *HybridRouter) RouteBatch(ctx context.Context, queries []string) ([]*HybridRouteResult, error) {
	results := make([]*HybridRouteResult, len(queries))

	for i, query := range queries {
		result, err := h.Route(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("failed to route query %q: %w", query, err)
		}
		results[i] = result
	}

	return results, nil
}

// List returns all route names from both routers.
func (h *HybridRouter) List() []string {
	h.denseMu.RLock()
	defer h.denseMu.RUnlock()

	return h.dense.List()
}

// GetAlpha returns the current alpha value.
func (h *HybridRouter) GetAlpha() float64 {
	return h.alpha
}

// SetAlpha updates the alpha weight.
func (h *HybridRouter) SetAlpha(alpha float64) {
	h.alpha = alpha
}

// Stats returns statistics about the hybrid router.
func (h *HybridRouter) Stats() map[string]interface{} {
	h.denseMu.RLock()
	h.sparseMu.RLock()
	defer h.denseMu.RUnlock()
	defer h.sparseMu.RUnlock()

	denseStats := h.dense.Stats()
	sparseStats := map[string]interface{}{
		"route_count": len(h.sparse.routes),
		"threshold":   h.sparse.config.Threshold,
	}

	return map[string]interface{}{
		"alpha":           h.alpha,
		"threshold":       h.threshold,
		"dense_router":    denseStats,
		"sparse_router":   sparseStats,
	}
}
