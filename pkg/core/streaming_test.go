package core

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func TestStreamSearch(t *testing.T) {
	dbPath := fmt.Sprintf("/tmp/test_stream_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()
	
	config := DefaultConfig()
	config.Path = dbPath
	config.VectorDim = 4
	
	store, err := NewWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()
	
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}
	
	// Insert test vectors
	vectors := []*Embedding{
		{ID: "vec1", Vector: []float32{1, 0, 0, 0}},
		{ID: "vec2", Vector: []float32{0, 1, 0, 0}},
		{ID: "vec3", Vector: []float32{0, 0, 1, 0}},
		{ID: "vec4", Vector: []float32{1, 1, 0, 0}},
		{ID: "vec5", Vector: []float32{1, 0, 1, 0}},
	}
	
	for _, v := range vectors {
		if err := store.Upsert(ctx, v); err != nil {
			t.Fatalf("Failed to insert %s: %v", v.ID, err)
		}
	}
	
	// Test streaming search
	query := []float32{0.9, 0.1, 0, 0}
	opts := StreamingOptions{
		SearchOptions: SearchOptions{TopK: 3},
		BatchSize:     2,
		MaxLatency:    10 * time.Millisecond,
	}
	
	stream, err := store.StreamSearch(ctx, query, opts)
	if err != nil {
		t.Fatalf("StreamSearch failed: %v", err)
	}
	
	// Collect results
	var results []StreamingResult
	for result := range stream {
		results = append(results, result)
		t.Logf("Received: %s (score: %.4f, batch: %d)", 
			result.ID, result.Score, result.BatchID)
	}
	
	if len(results) == 0 {
		t.Errorf("No results received from stream")
	}
	
	// Check that results come in batches
	batchCounts := make(map[int]int)
	for _, r := range results {
		batchCounts[r.BatchID]++
	}
	
	t.Logf("Batch distribution: %v", batchCounts)
}

func TestStreamSearchWithEarlyTermination(t *testing.T) {
	dbPath := fmt.Sprintf("/tmp/test_stream_early_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()
	
	config := DefaultConfig()
	config.Path = dbPath
	config.VectorDim = 4
	
	store, err := NewWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()
	
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}
	
	// Insert many vectors
	for i := 0; i < 100; i++ {
		v := &Embedding{
			ID:     fmt.Sprintf("vec%d", i),
			Vector: []float32{float32(i % 10) / 10, float32(i % 7) / 7, float32(i % 5) / 5, float32(i % 3) / 3},
		}
		if err := store.Upsert(ctx, v); err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
	}
	
	// Test with early termination
	query := []float32{0.5, 0.5, 0.5, 0.5}
	opts := StreamingOptions{
		SearchOptions:    SearchOptions{TopK: 10},
		BatchSize:        10,
		EarlyTerminate:   true,
		QualityThreshold: 0.8,
	}
	
	stream, err := store.StreamSearch(ctx, query, opts)
	if err != nil {
		t.Fatalf("StreamSearch failed: %v", err)
	}
	
	// Collect results
	var results []StreamingResult
	for result := range stream {
		results = append(results, result)
	}
	
	t.Logf("Received %d results with early termination", len(results))
	
	// Should have terminated early
	if len(results) >= 100 {
		t.Errorf("Early termination didn't work, got all results")
	}
}

func TestParallelStreamSearch(t *testing.T) {
	dbPath := fmt.Sprintf("/tmp/test_parallel_stream_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()
	
	config := DefaultConfig()
	config.Path = dbPath
	config.VectorDim = 4
	
	store, err := NewWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()
	
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}
	
	// Insert test vectors
	for i := 0; i < 20; i++ {
		v := &Embedding{
			ID:     fmt.Sprintf("vec%d", i),
			Vector: []float32{float32(i) / 20, float32(20-i) / 20, 0, 0},
		}
		if err := store.Upsert(ctx, v); err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
	}
	
	// Multiple queries
	queries := [][]float32{
		{1, 0, 0, 0},
		{0, 1, 0, 0},
		{0.5, 0.5, 0, 0},
	}
	
	opts := StreamingOptions{
		SearchOptions: SearchOptions{TopK: 5},
		BatchSize:     5,
	}
	
	streams, err := store.ParallelStreamSearch(ctx, queries, opts)
	if err != nil {
		t.Fatalf("ParallelStreamSearch failed: %v", err)
	}
	
	// Collect results from all streams
	var wg sync.WaitGroup
	results := make([][]StreamingResult, len(queries))
	
	for i, stream := range streams {
		wg.Add(1)
		go func(idx int, s <-chan StreamingResult) {
			defer wg.Done()
			for result := range s {
				results[idx] = append(results[idx], result)
			}
		}(i, stream)
	}
	
	wg.Wait()
	
	for i, queryResults := range results {
		t.Logf("Query %d returned %d results", i, len(queryResults))
	}
}

func TestMergeStreamResults(t *testing.T) {
	ctx := context.Background()
	
	// Create multiple channels
	ch1 := make(chan StreamingResult, 2)
	ch2 := make(chan StreamingResult, 2)
	ch3 := make(chan StreamingResult, 2)
	
	// Send data
	go func() {
		ch1 <- StreamingResult{
			ScoredEmbedding: ScoredEmbedding{
				Embedding: Embedding{ID: "a1"},
				Score:     0.9,
			},
			BatchID: 0,
		}
		ch1 <- StreamingResult{
			ScoredEmbedding: ScoredEmbedding{
				Embedding: Embedding{ID: "a2"},
				Score:     0.8,
			},
			BatchID: 1,
		}
		close(ch1)
	}()
	
	go func() {
		ch2 <- StreamingResult{
			ScoredEmbedding: ScoredEmbedding{
				Embedding: Embedding{ID: "b1"},
				Score:     0.85,
			},
			BatchID: 0,
		}
		close(ch2)
	}()
	
	go func() {
		ch3 <- StreamingResult{
			ScoredEmbedding: ScoredEmbedding{
				Embedding: Embedding{ID: "c1"},
				Score:     0.7,
			},
			BatchID: 0,
		}
		close(ch3)
	}()
	
	// Merge
	merged := MergeStreamResults(ctx, ch1, ch2, ch3)
	
	// Collect all results
	var results []StreamingResult
	for result := range merged {
		results = append(results, result)
	}
	
	if len(results) != 4 {
		t.Errorf("Expected 4 merged results, got %d", len(results))
	}
	
	// Check all IDs are present
	ids := make(map[string]bool)
	for _, r := range results {
		ids[r.ID] = true
	}
	
	expected := []string{"a1", "a2", "b1", "c1"}
	for _, id := range expected {
		if !ids[id] {
			t.Errorf("Missing ID in merged results: %s", id)
		}
	}
}

func TestCollectTopKFromStream(t *testing.T) {
	ctx := context.Background()
	ch := make(chan StreamingResult)
	
	// Send results in background
	go func() {
		for i := 0; i < 10; i++ {
			ch <- StreamingResult{
				ScoredEmbedding: ScoredEmbedding{
					Embedding: Embedding{ID: fmt.Sprintf("vec%d", i)},
					Score:     float64(10-i) / 10.0,
				},
				BatchID: i / 3,
			}
			time.Sleep(5 * time.Millisecond)
		}
		close(ch)
	}()
	
	// Collect top 5
	topK, err := CollectTopKFromStream(ctx, ch, 5)
	if err != nil {
		t.Fatalf("CollectTopKFromStream failed: %v", err)
	}
	
	if len(topK) != 5 {
		t.Errorf("Expected 5 results, got %d", len(topK))
	}
	
	// Check ordering
	for i := 1; i < len(topK); i++ {
		if topK[i].Score > topK[i-1].Score {
			t.Errorf("Results not properly ordered")
		}
	}
	
	t.Logf("Top-5 results:")
	for i, r := range topK {
		t.Logf("  %d. %s (score: %.2f)", i+1, r.ID, r.Score)
	}
}

func TestIncrementalIndex(t *testing.T) {
	dbPath := fmt.Sprintf("/tmp/test_incremental_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()
	
	config := DefaultConfig()
	config.Path = dbPath
	config.VectorDim = 4
	
	store, err := NewWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()
	
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}
	
	// Create incremental index
	idx := NewIncrementalIndex(store)
	defer idx.Close()
	
	// Add initial vectors
	for i := 0; i < 5; i++ {
		v := &Embedding{
			ID:     fmt.Sprintf("vec%d", i),
			Vector: []float32{float32(i) / 5, 0, 0, 0},
		}
		if err := store.Upsert(ctx, v); err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
	}
	
	// Add async updates
	for i := 5; i < 10; i++ {
		v := &Embedding{
			ID:     fmt.Sprintf("vec%d", i),
			Vector: []float32{float32(i) / 10, 0, 0, 0},
		}
		if err := idx.AddAsync(v); err != nil {
			t.Fatalf("Failed to add async: %v", err)
		}
	}
	
	// Search with updates
	query := []float32{0.5, 0, 0, 0}
	results, err := idx.SearchWithUpdates(ctx, query, SearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("SearchWithUpdates failed: %v", err)
	}
	
	t.Logf("Found %d results with incremental updates", len(results))
	for i, r := range results {
		t.Logf("  %d. %s (score: %.4f)", i+1, r.ID, r.Score)
	}
	
	// Wait a bit for background processing
	time.Sleep(200 * time.Millisecond)
	
	// Search again - should have more results
	results2, err := store.Search(ctx, query, SearchOptions{TopK: 10})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	
	t.Logf("After background processing: %d results", len(results2))
}

func TestStreamSearchContextCancellation(t *testing.T) {
	dbPath := fmt.Sprintf("/tmp/test_stream_cancel_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()
	
	config := DefaultConfig()
	config.Path = dbPath
	config.VectorDim = 4
	
	store, err := NewWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()
	
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}
	
	// Insert many vectors
	for i := 0; i < 100; i++ {
		v := &Embedding{
			ID:     fmt.Sprintf("vec%d", i),
			Vector: []float32{float32(i) / 100, 0, 0, 0},
		}
		if err := store.Upsert(ctx, v); err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
	}
	
	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure cancel is always called
	
	query := []float32{0.5, 0, 0, 0}
	opts := StreamingOptions{
		SearchOptions: SearchOptions{TopK: 50},
		BatchSize:     5,
	}
	
	stream, err := store.StreamSearch(ctx, query, opts)
	if err != nil {
		t.Fatalf("StreamSearch failed: %v", err)
	}
	
	// Collect some results then cancel
	var results []StreamingResult
	count := 0
	for result := range stream {
		results = append(results, result)
		count++
		if count == 10 {
			cancel() // Cancel after 10 results
		}
		if count > 20 {
			t.Errorf("Received too many results after cancellation")
			break
		}
	}
	
	t.Logf("Received %d results before cancellation", len(results))
	
	if len(results) > 30 {
		t.Errorf("Context cancellation didn't stop the stream properly")
	}
}

func BenchmarkStreamSearch(b *testing.B) {
	dbPath := fmt.Sprintf("/tmp/bench_stream_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()
	
	config := DefaultConfig()
	config.Path = dbPath
	config.VectorDim = 128
	
	store, _ := NewWithConfig(config)
	defer func() { _ = store.Close() }()
	
	ctx := context.Background()
	_ = store.Init(ctx)
	
	// Insert vectors
	for i := 0; i < 10000; i++ {
		vec := make([]float32, 128)
		for j := range vec {
			vec[j] = float32(i*j%100) / 100
		}
		_ = store.Upsert(ctx, &Embedding{
			ID:     fmt.Sprintf("vec%d", i),
			Vector: vec,
		})
	}
	
	query := make([]float32, 128)
	for i := range query {
		query[i] = 0.5
	}
	
	opts := StreamingOptions{
		SearchOptions: SearchOptions{TopK: 100},
		BatchSize:     50,
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stream, _ := store.StreamSearch(ctx, query, opts)
		// Drain the stream
		for range stream {
		}
	}
}

func TestStreamingWithProgress(t *testing.T) {
	dbPath := fmt.Sprintf("/tmp/test_progress_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()
	
	config := DefaultConfig()
	config.Path = dbPath
	config.VectorDim = 4
	
	store, err := NewWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()
	
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}
	
	// Insert vectors
	for i := 0; i < 50; i++ {
		v := &Embedding{
			ID:     fmt.Sprintf("vec%d", i),
			Vector: []float32{float32(i) / 50, 0, 0, 0},
		}
		if err := store.Upsert(ctx, v); err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
	}
	
	// Track progress
	var progressUpdates []string
	progressCallback := func(processed, total int) {
		progress := fmt.Sprintf("%d/%d (%.1f%%)", 
			processed, total, float64(processed)/float64(total)*100)
		progressUpdates = append(progressUpdates, progress)
		t.Logf("Progress: %s", progress)
	}
	
	query := []float32{0.5, 0, 0, 0}
	opts := StreamingOptions{
		SearchOptions:    SearchOptions{TopK: 10},
		BatchSize:        10,
		ProgressCallback: progressCallback,
	}
	
	stream, err := store.StreamSearch(ctx, query, opts)
	if err != nil {
		t.Fatalf("StreamSearch failed: %v", err)
	}
	
	// Drain stream
	for range stream {
	}
	
	if len(progressUpdates) == 0 {
		t.Errorf("No progress updates received")
	}
	
	t.Logf("Received %d progress updates", len(progressUpdates))
}