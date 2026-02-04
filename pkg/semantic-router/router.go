package semanticrouter

import (
	"context"
	"fmt"
	"sync"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
)

// Router performs semantic routing using vector similarity.
type Router struct {
	routes    []*Route
	embedder  Embedder
	config    Config
	store     core.Store // Optional: use persistent storage for routes
	mu        sync.RWMutex
}

// NewRouter creates a new semantic router.
func NewRouter(embedder Embedder, opts ...ConfigOption) (*Router, error) {
	if embedder == nil {
		return nil, fmt.Errorf("embedder cannot be nil")
	}

	config := DefaultConfig()
	for _, opt := range opts {
		opt(&config)
	}

	return &Router{
		routes:   make([]*Route, 0),
		embedder: embedder,
		config:   config,
	}, nil
}

// NewRouterWithStore creates a new semantic router with persistent storage.
// Routes and their embeddings are stored in the vector store for persistence.
func NewRouterWithStore(store core.Store, embedder Embedder, opts ...ConfigOption) (*Router, error) {
	if store == nil {
		return nil, fmt.Errorf("store cannot be nil")
	}
	if embedder == nil {
		return nil, fmt.Errorf("embedder cannot be nil")
	}

	config := DefaultConfig()
	for _, opt := range opts {
		opt(&config)
	}

	r := &Router{
		routes:   make([]*Route, 0),
		embedder: embedder,
		config:   config,
		store:    store,
	}

	// Load existing routes from store
	if err := r.loadRoutesFromStore(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to load routes: %w", err)
	}

	return r, nil
}

// ConfigOption is a function that modifies router configuration.
type ConfigOption func(*Config)

// WithThreshold sets the similarity threshold for route matching.
func WithThreshold(threshold float64) ConfigOption {
	return func(c *Config) {
		c.Threshold = threshold
	}
}

// WithSimilarityFunc sets the similarity function.
func WithSimilarityFunc(fn core.SimilarityFunc) ConfigOption {
	return func(c *Config) {
		c.SimilarityFunc = fn
	}
}

// WithTopK sets the number of top results to consider.
func WithTopK(k int) ConfigOption {
	return func(c *Config) {
		c.TopK = k
	}
}

// WithCacheEmbeddings enables or disables embedding caching.
func WithCacheEmbeddings(enabled bool) ConfigOption {
	return func(c *Config) {
		c.CacheEmbeddings = enabled
	}
}

// Add adds a new route to the router.
func (r *Router) Add(route *Route) error {
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

	// Check for duplicate route names
	for _, existing := range r.routes {
		if existing.Name == route.Name {
			return fmt.Errorf("route %q already exists", route.Name)
		}
	}

	r.routes = append(r.routes, route)

	// If store is available, persist the route
	if r.store != nil {
		ctx := context.Background()
		if err := r.persistRoute(ctx, route); err != nil {
			return fmt.Errorf("failed to persist route: %w", err)
		}
	}

	return nil
}

// AddBatch adds multiple routes at once.
func (r *Router) AddBatch(routes []*Route) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	ctx := context.Background()

	for _, route := range routes {
		if route.Name == "" {
			return fmt.Errorf("route name cannot be empty")
		}
		if len(route.Utterances) == 0 {
			return fmt.Errorf("route %q must have at least one utterance", route.Name)
		}

		// Check for duplicates
		for _, existing := range r.routes {
			if existing.Name == route.Name {
				return fmt.Errorf("route %q already exists", route.Name)
			}
		}
	}

	r.routes = append(r.routes, routes...)

	// Persist routes if store is available
	if r.store != nil {
		for _, route := range routes {
			if err := r.persistRoute(ctx, route); err != nil {
				return fmt.Errorf("failed to persist route %q: %w", route.Name, err)
			}
		}
	}

	return nil
}

// Remove removes a route by name.
func (r *Router) Remove(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, route := range r.routes {
		if route.Name == name {
			// Remove from slice
			r.routes = append(r.routes[:i], r.routes[i+1:]...)

			// Remove from store if available
			if r.store != nil {
				ctx := context.Background()
				opts := core.SearchOptions{
					Filter:     map[string]string{"route_name": name},
					TopK:       1000,
					Collection: "semantic_routes",
				}
				results, err := r.store.Search(ctx, make([]float32, r.embedder.Dimensions()), opts)
				if err == nil {
					ids := make([]string, len(results))
					for i, r := range results {
						ids[i] = r.ID
					}
					r.store.DeleteBatch(ctx, ids)
				}
			}

			return nil
		}
	}

	return fmt.Errorf("route %q not found", name)
}

// Route finds the best matching route for a given query.
func (r *Router) Route(ctx context.Context, query string) (*RouteResult, error) {
	if query == "" {
		return &RouteResult{}, nil
	}

	// Get query embedding
	queryVec, err := r.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to embed query: %w", err)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	var bestRoute *Route
	var maxScore float64

	// Find the best matching route
	for _, route := range r.routes {
		vectors, err := r.getRouteVectors(ctx, route)
		if err != nil {
			continue
		}

		for _, vec := range vectors {
			score := r.config.SimilarityFunc(queryVec, vec)
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

// RouteBatch routes multiple queries and returns results for each.
func (r *Router) RouteBatch(ctx context.Context, queries []string) ([]*RouteResult, error) {
	results := make([]*RouteResult, len(queries))

	for i, query := range queries {
		result, err := r.Route(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("failed to route query %q: %w", query, err)
		}
		results[i] = result
	}

	return results, nil
}

// List returns all registered route names.
func (r *Router) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, len(r.routes))
	for i, route := range r.routes {
		names[i] = route.Name
	}
	return names
}

// Get retrieves a route by name.
func (r *Router) Get(name string) *Route {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, route := range r.routes {
		if route.Name == name {
			return route
		}
	}
	return nil
}

// getRouteVectors returns the cached or computed embeddings for a route's utterances.
func (r *Router) getRouteVectors(ctx context.Context, route *Route) ([][]float32, error) {
	// If store is available, try to load from there first
	if r.store != nil {
		opts := core.SearchOptions{
			Filter:     map[string]string{"route_name": route.Name},
			TopK:       1000,
			Collection: "semantic_routes",
		}
		results, err := r.store.Search(ctx, make([]float32, r.embedder.Dimensions()), opts)
		if err == nil && len(results) > 0 {
			vectors := make([][]float32, len(results))
			for i, r := range results {
				vectors[i] = r.Vector
			}
			return vectors, nil
		}
	}

	// Fall back to caching in memory
	if !r.config.CacheEmbeddings {
		return r.embedVectors(ctx, route.Utterances)
	}

	var err error
	route.cachedOnce.Do(func() {
		route.cachedVectors, err = r.embedVectors(ctx, route.Utterances)
	})

	return route.cachedVectors, err
}

// embedVectors converts text utterances to embeddings.
func (r *Router) embedVectors(ctx context.Context, utterances []string) ([][]float32, error) {
	if len(utterances) == 0 {
		return nil, nil
	}

	// Try batch embedding first
	vectors, err := r.embedder.EmbedBatch(ctx, utterances)
	if err == nil {
		return vectors, nil
	}

	// Fall back to individual embeddings
	vectors = make([][]float32, len(utterances))
	for i, utterance := range utterances {
		vec, err := r.embedder.Embed(ctx, utterance)
		if err != nil {
			return nil, fmt.Errorf("failed to embed utterance %q: %w", utterance, err)
		}
		vectors[i] = vec
	}

	return vectors, nil
}

// persistRoute stores a route's utterances in the vector store.
func (r *Router) persistRoute(ctx context.Context, route *Route) error {
	// Create collection for routes if it doesn't exist
	_, err := r.store.CreateCollection(ctx, "semantic_routes", r.embedder.Dimensions())
	if err != nil {
		// Collection might already exist, ignore error
	}

	vectors, err := r.embedVectors(ctx, route.Utterances)
	if err != nil {
		return err
	}

	embeddings := make([]*core.Embedding, len(vectors))
	for i, vec := range vectors {
		embeddings[i] = &core.Embedding{
			Vector:     vec,
			Content:    route.Utterances[i],
			Collection: "semantic_routes",
			Metadata: map[string]string{
				"route_name": route.Name,
			},
		}
	}

	return r.store.UpsertBatch(ctx, embeddings)
}

// loadRoutesFromStore loads routes from the vector store.
func (r *Router) loadRoutesFromStore(ctx context.Context) error {
	// Get all routes from the semantic_routes collection
	collections, err := r.store.ListCollections(ctx)
	if err != nil {
		return nil // Store might not be initialized yet
	}

	found := false
	for _, col := range collections {
		if col.Name == "semantic_routes" {
			found = true
			break
		}
	}

	if !found {
		return nil // No routes stored yet
	}

	// Search for all route embeddings
	opts := core.SearchOptions{
		TopK:       10000,
		Collection: "semantic_routes",
	}
	dim := r.embedder.Dimensions()
	results, err := r.store.Search(ctx, make([]float32, dim), opts)
	if err != nil {
		return nil
	}

	// Group results by route name
	routeMap := make(map[string]*Route)
	for _, result := range results {
		routeName := result.Metadata["route_name"]
		if routeName == "" {
			continue
		}

		if _, exists := routeMap[routeName]; !exists {
			routeMap[routeName] = &Route{
				Name:       routeName,
				Utterances: []string{},
			}
		}

		routeMap[routeName].Utterances = append(routeMap[routeName].Utterances, result.Content)
		routeMap[routeName].cachedVectors = append(routeMap[routeName].cachedVectors, result.Vector)
	}

	// Add to routes
	for _, route := range routeMap {
		r.routes = append(r.routes, route)
	}

	return nil
}

// Stats returns statistics about the router.
func (r *Router) Stats() map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	totalUtterances := 0
	for _, route := range r.routes {
		totalUtterances += len(route.Utterances)
	}

	return map[string]interface{}{
		"route_count":       len(r.routes),
		"total_utterances":  totalUtterances,
		"threshold":         r.config.Threshold,
		"top_k":             r.config.TopK,
		"cache_enabled":     r.config.CacheEmbeddings,
		"has_persistent_store": r.store != nil,
	}
}
