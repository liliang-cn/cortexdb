package core

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestAggregations(t *testing.T) {
	dbPath := fmt.Sprintf("/tmp/test_aggregations_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	config := DefaultConfig()
	config.Path = dbPath
	config.VectorDim = 3

	store, err := NewWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	// Create test collections
	_, err = store.CreateCollection(ctx, "products", 3)
	if err != nil {
		t.Fatalf("Failed to create products collection: %v", err)
	}

	_, err = store.CreateCollection(ctx, "documents", 3)
	if err != nil {
		t.Fatalf("Failed to create documents collection: %v", err)
	}

	// Insert test data with metadata
	testData := []struct {
		collection string
		embeddings []*Embedding
	}{
		{
			collection: "products",
			embeddings: []*Embedding{
				{
					ID:     "prod1",
					Vector: []float32{0.1, 0.2, 0.3},
					Metadata: map[string]string{
						"category": "electronics",
						"price":    "299.99",
						"rating":   "4.5",
						"stock":    "10",
					},
				},
				{
					ID:     "prod2",
					Vector: []float32{0.2, 0.3, 0.4},
					Metadata: map[string]string{
						"category": "electronics",
						"price":    "499.99",
						"rating":   "4.8",
						"stock":    "5",
					},
				},
				{
					ID:     "prod3",
					Vector: []float32{0.3, 0.4, 0.5},
					Metadata: map[string]string{
						"category": "clothing",
						"price":    "79.99",
						"rating":   "4.2",
						"stock":    "20",
					},
				},
				{
					ID:     "prod4",
					Vector: []float32{0.4, 0.5, 0.6},
					Metadata: map[string]string{
						"category": "clothing",
						"price":    "59.99",
						"rating":   "3.9",
						"stock":    "15",
					},
				},
				{
					ID:     "prod5",
					Vector: []float32{0.5, 0.6, 0.7},
					Metadata: map[string]string{
						"category": "books",
						"price":    "19.99",
						"rating":   "4.7",
						"stock":    "50",
					},
				},
			},
		},
		{
			collection: "documents",
			embeddings: []*Embedding{
				{
					ID:     "doc1",
					Vector: []float32{0.1, 0.1, 0.1},
					Metadata: map[string]string{
						"type":      "article",
						"wordCount": "1500",
						"author":    "Alice",
					},
				},
				{
					ID:     "doc2",
					Vector: []float32{0.2, 0.2, 0.2},
					Metadata: map[string]string{
						"type":      "article",
						"wordCount": "2000",
						"author":    "Bob",
					},
				},
				{
					ID:     "doc3",
					Vector: []float32{0.3, 0.3, 0.3},
					Metadata: map[string]string{
						"type":      "report",
						"wordCount": "5000",
						"author":    "Alice",
					},
				},
			},
		},
	}

	// Insert test data
	for _, td := range testData {
		for _, emb := range td.embeddings {
			emb.Collection = td.collection
			if err := store.Upsert(ctx, emb); err != nil {
				t.Fatalf("Failed to insert %s: %v", emb.ID, err)
			}
		}
	}

	t.Run("CountAggregation", func(t *testing.T) {
		req := AggregationRequest{
			Type:       AggregationCount,
			Collection: "products",
		}

		resp, err := store.Aggregate(ctx, req)
		if err != nil {
			t.Fatalf("Count aggregation failed: %v", err)
		}

		if len(resp.Results) != 1 {
			t.Errorf("Expected 1 result, got %d", len(resp.Results))
		}

		if resp.Results[0].Value != 5 {
			t.Errorf("Expected count 5, got %v", resp.Results[0].Value)
		}
	})

	t.Run("CountWithFilter", func(t *testing.T) {
		req := AggregationRequest{
			Type:       AggregationCount,
			Collection: "products",
			Filters: map[string]interface{}{
				"category": "electronics",
			},
		}

		resp, err := store.Aggregate(ctx, req)
		if err != nil {
			t.Fatalf("Count with filter failed: %v", err)
		}

		if resp.Results[0].Value != 2 {
			t.Errorf("Expected count 2 for electronics, got %v", resp.Results[0].Value)
		}
	})

	t.Run("SumAggregation", func(t *testing.T) {
		req := AggregationRequest{
			Type:       AggregationSum,
			Field:      "price",
			Collection: "products",
		}

		resp, err := store.Aggregate(ctx, req)
		if err != nil {
			t.Fatalf("Sum aggregation failed: %v", err)
		}

		expectedSum := 299.99 + 499.99 + 79.99 + 59.99 + 19.99
		if sum, ok := resp.Results[0].Value.(float64); ok {
			if abs(sum-expectedSum) > 0.01 {
				t.Errorf("Expected sum %.2f, got %.2f", expectedSum, sum)
			}
		} else {
			t.Errorf("Sum value is not float64: %T", resp.Results[0].Value)
		}
	})

	t.Run("AvgAggregation", func(t *testing.T) {
		req := AggregationRequest{
			Type:       AggregationAvg,
			Field:      "rating",
			Collection: "products",
		}

		resp, err := store.Aggregate(ctx, req)
		if err != nil {
			t.Fatalf("Avg aggregation failed: %v", err)
		}

		expectedAvg := (4.5 + 4.8 + 4.2 + 3.9 + 4.7) / 5.0
		if avg, ok := resp.Results[0].Value.(float64); ok {
			if abs(avg-expectedAvg) > 0.01 {
				t.Errorf("Expected avg %.2f, got %.2f", expectedAvg, avg)
			}
		} else {
			t.Errorf("Avg value is not float64: %T", resp.Results[0].Value)
		}
	})

	t.Run("MinMaxAggregation", func(t *testing.T) {
		// Test MIN
		reqMin := AggregationRequest{
			Type:       AggregationMin,
			Field:      "price",
			Collection: "products",
		}

		respMin, err := store.Aggregate(ctx, reqMin)
		if err != nil {
			t.Fatalf("Min aggregation failed: %v", err)
		}

		if minVal, ok := respMin.Results[0].Value.(float64); ok {
			if abs(minVal-19.99) > 0.01 {
				t.Errorf("Expected min 19.99, got %.2f", minVal)
			}
		}

		// Test MAX
		reqMax := AggregationRequest{
			Type:       AggregationMax,
			Field:      "price",
			Collection: "products",
		}

		respMax, err := store.Aggregate(ctx, reqMax)
		if err != nil {
			t.Fatalf("Max aggregation failed: %v", err)
		}

		if maxVal, ok := respMax.Results[0].Value.(float64); ok {
			if abs(maxVal-499.99) > 0.01 {
				t.Errorf("Expected max 499.99, got %.2f", maxVal)
			}
		}
	})

	t.Run("GroupByAggregation", func(t *testing.T) {
		req := AggregationRequest{
			Type:       AggregationGroupBy,
			GroupBy:    []string{"category"},
			Collection: "products",
		}

		resp, err := store.Aggregate(ctx, req)
		if err != nil {
			t.Fatalf("Group by aggregation failed: %v", err)
		}

		if len(resp.Results) != 3 {
			t.Errorf("Expected 3 groups, got %d", len(resp.Results))
		}

		// Check group counts
		categoryCount := make(map[string]int)
		for _, result := range resp.Results {
			if category, ok := result.GroupKeys["category"].(string); ok {
				categoryCount[category] = result.Count
			}
		}

		expectedCounts := map[string]int{
			"electronics": 2,
			"clothing":    2,
			"books":       1,
		}

		for category, expected := range expectedCounts {
			if actual, ok := categoryCount[category]; ok {
				if actual != expected {
					t.Errorf("Category %s: expected count %d, got %d", category, expected, actual)
				}
			} else {
				t.Errorf("Category %s not found in results", category)
			}
		}
	})

	t.Run("GroupByWithSum", func(t *testing.T) {
		req := AggregationRequest{
			Type:       AggregationSum,
			Field:      "stock",
			GroupBy:    []string{"category"},
			Collection: "products",
		}

		resp, err := store.Aggregate(ctx, req)
		if err != nil {
			t.Fatalf("Group by with sum failed: %v", err)
		}

		// Check stock sums by category
		categoryStock := make(map[string]float64)
		for _, result := range resp.Results {
			if category, ok := result.GroupKeys["category"].(string); ok {
				if stock, ok := result.Value.(float64); ok {
					categoryStock[category] = stock
				}
			}
		}

		expectedStock := map[string]float64{
			"electronics": 15,  // 10 + 5
			"clothing":    35,  // 20 + 15
			"books":       50,  // 50
		}

		for category, expected := range expectedStock {
			if actual, ok := categoryStock[category]; ok {
				if abs(actual-expected) > 0.01 {
					t.Errorf("Category %s: expected stock %.0f, got %.0f", category, expected, actual)
				}
			}
		}
	})

	t.Run("GroupByMultipleFields", func(t *testing.T) {
		req := AggregationRequest{
			Type:       AggregationGroupBy,
			GroupBy:    []string{"type", "author"},
			Collection: "documents",
		}

		resp, err := store.Aggregate(ctx, req)
		if err != nil {
			t.Fatalf("Multi-field group by failed: %v", err)
		}

		// Should have 3 groups: (article, Alice), (article, Bob), (report, Alice)
		if len(resp.Results) != 3 {
			t.Errorf("Expected 3 groups, got %d", len(resp.Results))
		}
	})

	t.Run("AggregationWithLimit", func(t *testing.T) {
		req := AggregationRequest{
			Type:       AggregationGroupBy,
			GroupBy:    []string{"category"},
			Collection: "products",
			Limit:      2,
		}

		resp, err := store.Aggregate(ctx, req)
		if err != nil {
			t.Fatalf("Aggregation with limit failed: %v", err)
		}

		if len(resp.Results) != 2 {
			t.Errorf("Expected 2 results with limit, got %d", len(resp.Results))
		}
	})

	t.Run("InvalidAggregation", func(t *testing.T) {
		// Test missing field for SUM
		req := AggregationRequest{
			Type:       AggregationSum,
			Collection: "products",
			// Field is missing
		}

		_, err := store.Aggregate(ctx, req)
		if err == nil {
			t.Error("Expected error for missing field in SUM aggregation")
		}

		// Test missing group_by for GROUP BY
		req2 := AggregationRequest{
			Type:       AggregationGroupBy,
			Collection: "products",
			// GroupBy is missing
		}

		_, err = store.Aggregate(ctx, req2)
		if err == nil {
			t.Error("Expected error for missing group_by in GROUP BY aggregation")
		}
	})

	t.Run("CrossCollectionAggregation", func(t *testing.T) {
		// Count all embeddings (no collection filter)
		req := AggregationRequest{
			Type: AggregationCount,
		}

		resp, err := store.Aggregate(ctx, req)
		if err != nil {
			t.Fatalf("Cross-collection count failed: %v", err)
		}

		totalExpected := 5 + 3 // products + documents
		if resp.Results[0].Value != totalExpected {
			t.Errorf("Expected total count %d, got %v", totalExpected, resp.Results[0].Value)
		}
	})
}

// Helper function for absolute value
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func BenchmarkAggregations(b *testing.B) {
	dbPath := fmt.Sprintf("/tmp/bench_aggregations_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	store, _ := NewWithConfig(Config{
		Path:      dbPath,
		VectorDim: 3,
	})
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	_ = store.Init(ctx)

	// Insert test data
	for i := 0; i < 1000; i++ {
		emb := &Embedding{
			ID:     fmt.Sprintf("emb_%d", i),
			Vector: []float32{float32(i % 10), float32(i % 20), float32(i % 30)},
			Metadata: map[string]string{
				"category": fmt.Sprintf("cat_%d", i%5),
				"value":    fmt.Sprintf("%d", i),
				"active":   fmt.Sprintf("%t", i%2 == 0),
			},
		}
		_ = store.Upsert(ctx, emb)
	}

	b.Run("Count", func(b *testing.B) {
		req := AggregationRequest{
			Type: AggregationCount,
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = store.Aggregate(ctx, req)
		}
	})

	b.Run("Sum", func(b *testing.B) {
		req := AggregationRequest{
			Type:  AggregationSum,
			Field: "value",
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = store.Aggregate(ctx, req)
		}
	})

	b.Run("GroupBy", func(b *testing.B) {
		req := AggregationRequest{
			Type:    AggregationGroupBy,
			GroupBy: []string{"category"},
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = store.Aggregate(ctx, req)
		}
	})

	b.Run("GroupByWithFilter", func(b *testing.B) {
		req := AggregationRequest{
			Type:    AggregationGroupBy,
			GroupBy: []string{"category"},
			Filters: map[string]interface{}{
				"active": true,
			},
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = store.Aggregate(ctx, req)
		}
	})
}