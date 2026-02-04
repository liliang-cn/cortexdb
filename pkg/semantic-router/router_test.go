package semanticrouter

import (
	"context"
	"fmt"
	"testing"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
)

func TestNewRouter(t *testing.T) {
	embedder := NewMockEmbedder(128)
	router, err := NewRouter(embedder)
	if err != nil {
		t.Fatalf("Failed to create router: %v", err)
	}
	if router == nil {
		t.Fatal("Router is nil")
	}
}

func TestNewRouterNilEmbedder(t *testing.T) {
	_, err := NewRouter(nil)
	if err == nil {
		t.Fatal("Expected error for nil embedder")
	}
}

func TestRouterAdd(t *testing.T) {
	embedder := NewMockEmbedder(128)
	router, _ := NewRouter(embedder)

	route := &Route{
		Name:       "refund",
		Utterances: []string{"我要退款", "这东西坏了", "把钱还我"},
	}

	err := router.Add(route)
	if err != nil {
		t.Fatalf("Failed to add route: %v", err)
	}

	// Verify route was added
	routes := router.List()
	if len(routes) != 1 {
		t.Fatalf("Expected 1 route, got %d", len(routes))
	}
	if routes[0] != "refund" {
		t.Fatalf("Expected route name 'refund', got %q", routes[0])
	}
}

func TestRouterAddDuplicate(t *testing.T) {
	embedder := NewMockEmbedder(128)
	router, _ := NewRouter(embedder)

	route1 := &Route{
		Name:       "refund",
		Utterances: []string{"我要退款"},
	}
	route2 := &Route{
		Name:       "refund",
		Utterances: []string{"把钱还我"},
	}

	router.Add(route1)
	err := router.Add(route2)
	if err == nil {
		t.Fatal("Expected error for duplicate route name")
	}
}

func TestRouterAddValidation(t *testing.T) {
	embedder := NewMockEmbedder(128)
	router, _ := NewRouter(embedder)

	tests := []struct {
		name    string
		route   *Route
		wantErr bool
	}{
		{
			name:    "nil route",
			route:   nil,
			wantErr: true,
		},
		{
			name: "empty name",
			route: &Route{
				Name:       "",
				Utterances: []string{"test"},
			},
			wantErr: true,
		},
		{
			name: "no utterances",
			route: &Route{
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

func TestRouterRoute(t *testing.T) {
	embedder := NewMockEmbedder(128)
	router, _ := NewRouter(embedder, WithThreshold(0.5))

	refundRoute := &Route{
		Name:       "refund",
		Utterances: []string{"我要退款", "这东西坏了", "把钱还我"},
		Handler: func(ctx context.Context, query string, score float64) (string, error) {
			return "REFUND_HANDLER", nil
		},
	}

	chatRoute := &Route{
		Name:       "chat",
		Utterances: []string{"你好", "在吗", "最近怎么样"},
		Handler: func(ctx context.Context, query string, score float64) (string, error) {
			return "CHAT_HANDLER", nil
		},
	}

	router.Add(refundRoute)
	router.Add(chatRoute)

	tests := []struct {
		name          string
		query         string
		wantRouteName string
		checkMatched  bool // if true, verify matched matches expected
		wantMatched   bool
	}{
		{
			name:          "exact match",
			query:         "我要退款",
			wantRouteName: "refund",
			checkMatched:  true,
			wantMatched:   true,
		},
		{
			name:          "exact match 2",
			query:         "这东西坏了",
			wantRouteName: "refund",
			checkMatched:  true,
			wantMatched:   true,
		},
		{
			name:          "exact match chat",
			query:         "你好",
			wantRouteName: "chat",
			checkMatched:  true,
			wantMatched:   true,
		},
		{
			name:          "empty query",
			query:         "",
			wantRouteName: "",
			checkMatched:  true,
			wantMatched:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			result, err := router.Route(ctx, tt.query)
			if err != nil {
				t.Fatalf("Route() error = %v", err)
			}

			if result.RouteName != tt.wantRouteName {
				t.Errorf("RouteName = %q, want %q", result.RouteName, tt.wantRouteName)
			}
			if tt.checkMatched && result.Matched != tt.wantMatched {
				t.Errorf("Matched = %v, want %v", result.Matched, tt.wantMatched)
			}
		})
	}
}

func TestRouterRouteBatch(t *testing.T) {
	embedder := NewMockEmbedder(128)
	router, _ := NewRouter(embedder, WithThreshold(0.5))

	router.Add(&Route{
		Name:       "refund",
		Utterances: []string{"我要退款", "这东西坏了"},
	})

	queries := []string{"我要退款", "在吗", "退款申请"}
	ctx := context.Background()

	results, err := router.RouteBatch(ctx, queries)
	if err != nil {
		t.Fatalf("RouteBatch() error = %v", err)
	}

	if len(results) != len(queries) {
		t.Fatalf("Got %d results, want %d", len(results), len(queries))
	}
}

func TestRouterRemove(t *testing.T) {
	embedder := NewMockEmbedder(128)
	router, _ := NewRouter(embedder)

	router.Add(&Route{
		Name:       "refund",
		Utterances: []string{"我要退款"},
	})

	err := router.Remove("refund")
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	if len(router.List()) != 0 {
		t.Fatalf("Expected 0 routes after removal, got %d", len(router.List()))
	}
}

func TestRouterRemoveNotFound(t *testing.T) {
	embedder := NewMockEmbedder(128)
	router, _ := NewRouter(embedder)

	err := router.Remove("nonexistent")
	if err == nil {
		t.Fatal("Expected error for non-existent route")
	}
}

func TestRouterGet(t *testing.T) {
	embedder := NewMockEmbedder(128)
	router, _ := NewRouter(embedder)

	route := &Route{
		Name:       "test",
		Utterances: []string{"test"},
		Handler: func(ctx context.Context, query string, score float64) (string, error) {
			return "handled", nil
		},
	}

	router.Add(route)

	// Get existing route
	got := router.Get("test")
	if got == nil {
		t.Fatal("Get() returned nil for existing route")
	}
	if got.Name != "test" {
		t.Errorf("Got route name %q, want 'test'", got.Name)
	}
	if got.Handler == nil {
		t.Error("Handler is nil")
	}

	// Get non-existent route
	got = router.Get("nonexistent")
	if got != nil {
		t.Error("Get() should return nil for non-existent route")
	}
}

func TestRouterStats(t *testing.T) {
	embedder := NewMockEmbedder(128)
	router, _ := NewRouter(embedder)

	router.Add(&Route{Name: "route1", Utterances: []string{"a", "b"}})
	router.Add(&Route{Name: "route2", Utterances: []string{"c"}})

	stats := router.Stats()

	if stats["route_count"].(int) != 2 {
		t.Errorf("route_count = %v, want 2", stats["route_count"])
	}
	if stats["total_utterances"].(int) != 3 {
		t.Errorf("total_utterances = %v, want 3", stats["total_utterances"])
	}
}

func TestConfigOptions(t *testing.T) {
	embedder := NewMockEmbedder(128)

	tests := []struct {
		name      string
		option    ConfigOption
		check     func(*Router) error
	}{
		{
			name:   "with threshold",
			option: WithThreshold(0.9),
			check: func(r *Router) error {
				if r.config.Threshold != 0.9 {
					return fmt.Errorf("threshold = %v, want 0.9", r.config.Threshold)
				}
				return nil
			},
		},
		{
			name:   "with top k",
			option: WithTopK(5),
			check: func(r *Router) error {
				if r.config.TopK != 5 {
					return fmt.Errorf("top_k = %v, want 5", r.config.TopK)
				}
				return nil
			},
		},
		{
			name:   "with cache disabled",
			option: WithCacheEmbeddings(false),
			check: func(r *Router) error {
				if r.config.CacheEmbeddings != false {
					return fmt.Errorf("cache_enabled = %v, want false", r.config.CacheEmbeddings)
				}
				return nil
			},
		},
		{
			name:   "with similarity func",
			option: WithSimilarityFunc(core.DotProduct),
			check: func(r *Router) error {
				if r.config.SimilarityFunc == nil {
					return fmt.Errorf("similarity func is nil")
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, _ := NewRouter(embedder, tt.option)
			if err := tt.check(router); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestMockEmbedder(t *testing.T) {
	embedder := NewMockEmbedder(128)

	ctx := context.Background()
	vec, err := embedder.Embed(ctx, "test")
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}

	if len(vec) != 128 {
		t.Fatalf("Got vector length %d, want 128", len(vec))
	}

	// Test determinism
	vec2, _ := embedder.Embed(ctx, "test")
	if len(vec) != len(vec2) {
		t.Fatal("Vector length changed")
	}
	for i := range vec {
		if vec[i] != vec2[i] {
			t.Errorf("Vector not deterministic at index %d: %v != %v", i, vec[i], vec2[i])
		}
	}

	// Test different texts produce different vectors
	vec3, _ := embedder.Embed(ctx, "different")
	same := true
	for i := range vec {
		if vec[i] != vec3[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("Different texts should produce different vectors")
	}
}

func TestMockEmbedderBatch(t *testing.T) {
	embedder := NewMockEmbedder(64)

	ctx := context.Background()
	texts := []string{"hello", "world", "test"}

	vectors, err := embedder.EmbedBatch(ctx, texts)
	if err != nil {
		t.Fatalf("EmbedBatch() error = %v", err)
	}

	if len(vectors) != len(texts) {
		t.Fatalf("Got %d vectors, want %d", len(vectors), len(texts))
	}

	for i, vec := range vectors {
		if len(vec) != 64 {
			t.Errorf("Vector %d has length %d, want 64", i, len(vec))
		}
	}
}

func TestMockEmbedderDimensions(t *testing.T) {
	embedder := NewMockEmbedder(256)

	if embedder.Dimensions() != 256 {
		t.Errorf("Dimensions() = %d, want 256", embedder.Dimensions())
	}
}

func TestCachedEmbedder(t *testing.T) {
	base := NewMockEmbedder(128)
	cached := NewCachedEmbedder(base)

	ctx := context.Background()

	// First call should hit base embedder
	vec1, err := cached.Embed(ctx, "test")
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}

	if cached.CacheSize() != 1 {
		t.Errorf("Cache size = %d, want 1", cached.CacheSize())
	}

	// Second call should use cache
	vec2, _ := cached.Embed(ctx, "test")
	if len(vec1) != len(vec2) {
		t.Fatal("Cached vector has different length")
	}
	for i := range vec1 {
		if vec1[i] != vec2[i] {
			t.Errorf("Cached vector differs at index %d", i)
		}
	}

	// Clear cache
	cached.ClearCache()
	if cached.CacheSize() != 0 {
		t.Errorf("Cache size after clear = %d, want 0", cached.CacheSize())
	}
}

func TestCachedEmbedderBatch(t *testing.T) {
	base := NewMockEmbedder(64)
	cached := NewCachedEmbedder(base)

	ctx := context.Background()
	texts := []string{"a", "b", "a", "c"} // "a" appears twice

	vectors, err := cached.EmbedBatch(ctx, texts)
	if err != nil {
		t.Fatalf("EmbedBatch() error = %v", err)
	}

	if len(vectors) != 4 {
		t.Fatalf("Got %d vectors, want 4", len(vectors))
	}

	// Cache should only have 3 entries (a, b, c)
	if cached.CacheSize() != 3 {
		t.Errorf("Cache size = %d, want 3", cached.CacheSize())
	}

	// Verify "a" at indices 0 and 2 are identical
	for i := range vectors[0] {
		if vectors[0][i] != vectors[2][i] {
			t.Errorf("Cached 'a' vectors differ at index %d", i)
		}
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.Threshold != 0.82 {
		t.Errorf("Threshold = %v, want 0.82", config.Threshold)
	}
	if config.TopK != 1 {
		t.Errorf("TopK = %v, want 1", config.TopK)
	}
	if config.SimilarityFunc == nil {
		t.Error("SimilarityFunc is nil")
	}
	if !config.CacheEmbeddings {
		t.Error("CacheEmbeddings should be true by default")
	}
}
