package semanticrouter

import (
	"context"
	"testing"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
)

func TestNewHybridRouter(t *testing.T) {
	dense := NewMockEmbedder(128)
	sparse := NewTFIDFEncoder()

	router, err := NewHybridRouter(dense, sparse)
	if err != nil {
		t.Fatalf("NewHybridRouter() error = %v", err)
	}

	if router == nil {
		t.Fatal("Router is nil")
	}

	if router.alpha != 0.7 {
		t.Errorf("Default alpha = %v, want 0.7", router.alpha)
	}
}

func TestNewHybridRouterNilDense(t *testing.T) {
	sparse := NewTFIDFEncoder()

	_, err := NewHybridRouter(nil, sparse)
	if err == nil {
		t.Fatal("Expected error for nil dense embedder")
	}
}

func TestNewHybridRouterNilSparse(t *testing.T) {
	dense := NewMockEmbedder(128)

	_, err := NewHybridRouter(dense, nil)
	if err == nil {
		t.Fatal("Expected error for nil sparse encoder")
	}
}

func TestHybridRouterAdd(t *testing.T) {
	dense := NewMockEmbedder(128)
	sparse := NewBM25Encoder()

	router, _ := NewHybridRouter(dense, sparse)

	denseRoute := &Route{
		Name:       "refund",
		Utterances: []string{"I want a refund", "this is broken"},
	}

	sparseRoute := &SparseRoute{
		Name:       "refund",
		Utterances: []string{"refund money back"},
	}

	err := router.Add(denseRoute, sparseRoute)
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	names := router.List()
	if len(names) != 1 {
		t.Fatalf("Expected 1 route, got %d", len(names))
	}
	if names[0] != "refund" {
		t.Errorf("Expected route name 'refund', got %q", names[0])
	}
}

func TestHybridRouterRoute(t *testing.T) {
	dense := NewMockEmbedder(128)
	sparse := NewBM25Encoder()

	router, _ := NewHybridRouter(dense, sparse,
		WithHybridAlpha(0.5),
		WithHybridThreshold(0.4),
	)

	denseRoute := &Route{
		Name:       "refund",
		Utterances: []string{"I want a refund"},
		Handler: func(ctx context.Context, query string, score float64) (string, error) {
			return "REFUND", nil
		},
	}

	sparseRoute := &SparseRoute{
		Name:       "refund",
		Utterances: []string{"refund money back"},
	}

	router.Add(denseRoute, sparseRoute)

	ctx := context.Background()
	result, err := router.Route(ctx, "I want a refund")
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}

	// Should have some scores
	if result.DenseScore < 0 || result.DenseScore > 1 {
		t.Errorf("DenseScore = %v, want value in [0, 1]", result.DenseScore)
	}

	if result.SparseScore < 0 {
		t.Errorf("SparseScore = %v, want >= 0", result.SparseScore)
	}

	if result.CombinedScore < 0 || result.CombinedScore > 1 {
		t.Errorf("CombinedScore = %v, want value in [0, 1]", result.CombinedScore)
	}

	// Combined score should be weighted average
	expectedCombined := 0.5*result.DenseScore + 0.5*result.SparseScore
	if result.CombinedScore != expectedCombined {
		t.Errorf("CombinedScore = %v, want %v", result.CombinedScore, expectedCombined)
	}
}

func TestHybridRouterRouteBatch(t *testing.T) {
	dense := NewMockEmbedder(64)
	sparse := NewBM25Encoder()

	router, _ := NewHybridRouter(dense, sparse)

	denseRoute := &Route{
		Name:       "chat",
		Utterances: []string{"hello", "hi there"},
	}

	sparseRoute := &SparseRoute{
		Name:       "chat",
		Utterances: []string{"hello greeting"},
	}

	router.Add(denseRoute, sparseRoute)

	queries := []string{"hello", "hi", "greeting"}
	ctx := context.Background()

	results, err := router.RouteBatch(ctx, queries)
	if err != nil {
		t.Fatalf("RouteBatch() error = %v", err)
	}

	if len(results) != len(queries) {
		t.Fatalf("Got %d results, want %d", len(results), len(queries))
	}
}

func TestHybridRouterAlpha(t *testing.T) {
	dense := NewMockEmbedder(32)
	sparse := NewBM25Encoder()

	router, _ := NewHybridRouter(dense, sparse)

	// Test GetAlpha
	if router.GetAlpha() != 0.7 {
		t.Errorf("GetAlpha() = %v, want 0.7", router.GetAlpha())
	}

	// Test SetAlpha
	router.SetAlpha(0.8)
	if router.GetAlpha() != 0.8 {
		t.Errorf("SetAlpha(0.8) -> GetAlpha() = %v, want 0.8", router.GetAlpha())
	}
}

func TestHybridRouterWithAlpha(t *testing.T) {
	dense := NewMockEmbedder(32)
	sparse := NewTFIDFEncoder()

	router, err := NewHybridRouter(dense, sparse, WithHybridAlpha(0.9))
	if err != nil {
		t.Fatalf("NewHybridRouter() error = %v", err)
	}

	if router.GetAlpha() != 0.9 {
		t.Errorf("GetAlpha() = %v, want 0.9", router.GetAlpha())
	}
}

func TestHybridRouterStats(t *testing.T) {
	dense := NewMockEmbedder(64)
	sparse := NewBM25Encoder()

	router, _ := NewHybridRouter(dense, sparse)

	denseRoute := &Route{
		Name:       "test",
		Utterances: []string{"test utterance"},
	}

	sparseRoute := &SparseRoute{
		Name:       "test",
		Utterances: []string{"test keywords"},
	}

	router.Add(denseRoute, sparseRoute)

	stats := router.Stats()

	if stats["alpha"].(float64) != 0.7 {
		t.Errorf("alpha in stats = %v, want 0.7", stats["alpha"])
	}

	denseStats, ok := stats["dense_router"].(map[string]interface{})
	if !ok {
		t.Fatal("dense_router not in stats")
	}

	if denseStats["route_count"].(int) != 1 {
		t.Errorf("dense route_count = %v, want 1", denseStats["route_count"])
	}
}

func TestSparseRouter(t *testing.T) {
	encoder := NewTFIDFEncoder()

	router, err := NewSparseRouter(encoder)
	if err != nil {
		t.Fatalf("NewSparseRouter() error = %v", err)
	}

	route := &SparseRoute{
		Name:       "refund",
		Utterances: []string{"refund money back"},
	}

	err = router.Add(route)
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	names := router.List()
	if len(names) != 1 {
		t.Fatalf("Expected 1 route, got %d", len(names))
	}
}

func TestSparseRouterNilEncoder(t *testing.T) {
	_, err := NewSparseRouter(nil)
	if err == nil {
		t.Fatal("Expected error for nil encoder")
	}
}

func TestSparseRouterAddValidation(t *testing.T) {
	encoder := NewBM25Encoder()
	router, _ := NewSparseRouter(encoder)

	tests := []struct {
		name    string
		route   *SparseRoute
		wantErr bool
	}{
		{
			name:    "nil route",
			route:   nil,
			wantErr: true,
		},
		{
			name: "empty name",
			route: &SparseRoute{
				Name:       "",
				Utterances: []string{"test"},
			},
			wantErr: true,
		},
		{
			name: "no utterances",
			route: &SparseRoute{
				Name:       "test",
				Utterances: []string{},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := router.Add(tt.route)
			if (err != nil) != tt.wantErr {
				t.Errorf("Add() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSparseRouterRoute(t *testing.T) {
	encoder := NewTFIDFEncoder()
	router, _ := NewSparseRouter(encoder, WithSparseThreshold(0.1))

	// Fit the encoder with the route utterances first
	ctx := context.Background()
	utterances := []string{
		"politics president election government",
		"chitchat hello weather nice",
	}
	encoder.Fit(ctx, utterances)

	route := &SparseRoute{
		Name:       "politics",
		Utterances: []string{"politics president election government"},
		Handler: func(ctx context.Context, query string, score float64) (string, error) {
			return "POLITICS", nil
		},
	}

	router.Add(route)

	result, err := router.Route(ctx, "president election")
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}

	if result.RouteName != "politics" {
		t.Errorf("RouteName = %q, want 'politics'", result.RouteName)
	}
}

func TestSparseRouterConfigOptions(t *testing.T) {
	encoder := NewBM25Encoder()

	router, err := NewSparseRouter(encoder,
		WithSparseThreshold(0.5),
		WithSparseTopK(5),
	)
	if err != nil {
		t.Fatalf("NewSparseRouter() error = %v", err)
	}

	if router.config.Threshold != 0.5 {
		t.Errorf("Threshold = %v, want 0.5", router.config.Threshold)
	}

	if router.config.TopK != 5 {
		t.Errorf("TopK = %v, want 5", router.config.TopK)
	}
}

func TestDefaultSparseConfig(t *testing.T) {
	config := DefaultSparseConfig()

	if config.Threshold != 0.3 {
		t.Errorf("Threshold = %v, want 0.3", config.Threshold)
	}

	if config.TopK != 1 {
		t.Errorf("TopK = %v, want 1", config.TopK)
	}
}

func TestDefaultHybridConfig(t *testing.T) {
	config := DefaultHybridConfig()

	if config.Alpha != 0.7 {
		t.Errorf("Alpha = %v, want 0.7", config.Alpha)
	}

	if config.Threshold != 0.6 {
		t.Errorf("Threshold = %v, want 0.6", config.Threshold)
	}

	if config.DenseThreshold != 0.82 {
		t.Errorf("DenseThreshold = %v, want 0.82", config.DenseThreshold)
	}

	if config.SparseThreshold != 0.3 {
		t.Errorf("SparseThreshold = %v, want 0.3", config.SparseThreshold)
	}

	if config.SimilarityFunc == nil {
		t.Error("SimilarityFunc is nil")
	}
}

func TestHybridRouterEmptyQuery(t *testing.T) {
	dense := NewMockEmbedder(32)
	sparse := NewBM25Encoder()

	router, _ := NewHybridRouter(dense, sparse)

	ctx := context.Background()
	result, err := router.Route(ctx, "")
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}

	if result.RouteName != "" {
		t.Errorf("RouteName = %q, want empty", result.RouteName)
	}

	if result.Matched {
		t.Error("Matched = true, want false for empty query")
	}
}

func TestHybridRouterMultipleRoutes(t *testing.T) {
	dense := NewMockEmbedder(128)
	sparse := NewBM25Encoder()

	router, _ := NewHybridRouter(dense, sparse, WithHybridThreshold(0.3))

	// Train sparse encoder first
	ctx := context.Background()
	allUtterances := []string{
		"politics government president",
		"hello weather how are you",
		"election vote government",
		"weather nice day",
	}
	sparse.Fit(ctx, allUtterances)

	denseRoutes := []*Route{
		{Name: "politics", Utterances: []string{"politics government president"}},
		{Name: "chitchat", Utterances: []string{"hello weather how are you"}},
	}

	sparseRoutes := []*SparseRoute{
		{Name: "politics", Utterances: []string{"election vote government"}},
		{Name: "chitchat", Utterances: []string{"weather nice day"}},
	}

	router.AddBatch(denseRoutes, sparseRoutes)

	// Test politics route (exact match from dense)
	result, _ := router.Route(ctx, "politics government president")
	if result.RouteName != "politics" {
		t.Errorf("Expected 'politics', got %q", result.RouteName)
	}

	// Test chitchat route (exact match from dense)
	result, _ = router.Route(ctx, "hello weather how are you")
	if result.RouteName != "chitchat" {
		t.Errorf("Expected 'chitchat', got %q", result.RouteName)
	}
}

func TestHybridRouterWithSimilarityFunc(t *testing.T) {
	dense := NewMockEmbedder(64)
	sparse := NewTFIDFEncoder()

	router, err := NewHybridRouter(dense, sparse,
		WithHybridSimilarityFunc(core.DotProduct),
	)
	if err != nil {
		t.Fatalf("NewHybridRouter() error = %v", err)
	}

	if router.similarityFunc == nil {
		t.Error("SimilarityFunc is nil")
	}
}
