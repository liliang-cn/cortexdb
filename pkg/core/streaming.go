package core

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// StreamingResult represents a single result in streaming search
type StreamingResult struct {
	ScoredEmbedding
	Timestamp time.Time
	BatchID   int
}

// StreamingOptions configures streaming search behavior
type StreamingOptions struct {
	SearchOptions
	BatchSize       int           // Number of vectors to process per batch
	MaxLatency      time.Duration // Maximum time to wait before sending partial results
	EarlyTerminate  bool          // Stop when enough good results are found
	QualityThreshold float64      // Score threshold for early termination
	ProgressCallback func(processed, total int) // Optional progress reporting
}

// StreamSearch performs incremental vector search with results streaming
func (s *SQLiteStore) StreamSearch(ctx context.Context, query []float32, opts StreamingOptions) (<-chan StreamingResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return nil, wrapError("stream_search", ErrStoreClosed)
	}
	
	// Set defaults
	if opts.BatchSize <= 0 {
		opts.BatchSize = 100
	}
	if opts.MaxLatency <= 0 {
		opts.MaxLatency = 100 * time.Millisecond
	}
	
	// Create result channel
	resultChan := make(chan StreamingResult, opts.BatchSize)
	
	// Start streaming goroutine
	go func() {
		defer close(resultChan)
		
		// Get all candidates
		candidates, err := s.fetchCandidates(ctx, opts.SearchOptions)
		if err != nil {
			return
		}
		
		// Process in batches
		batchID := 0
		totalCandidates := len(candidates)
		
		for i := 0; i < totalCandidates; i += opts.BatchSize {
			select {
			case <-ctx.Done():
				return // Context cancelled
			default:
			}
			
			end := i + opts.BatchSize
			if end > totalCandidates {
				end = totalCandidates
			}
			
			batch := candidates[i:end]
			batchResults := make([]StreamingResult, 0, len(batch))
			
			// Score batch
			for _, candidate := range batch {
				score := s.similarityFn(query, candidate.Vector)
				candidate.Score = score
				
				result := StreamingResult{
					ScoredEmbedding: candidate,
					Timestamp:       time.Now(),
					BatchID:         batchID,
				}
				batchResults = append(batchResults, result)
			}
			
			// Sort batch by score
			sort.Slice(batchResults, func(j, k int) bool {
				return batchResults[j].Score > batchResults[k].Score
			})
			
			// Send results
			for _, result := range batchResults {
				select {
				case resultChan <- result:
				case <-ctx.Done():
					return
				case <-time.After(opts.MaxLatency):
					// Skip if channel is full after max latency
					continue
				}
				
				// Check early termination
				if opts.EarlyTerminate && result.Score >= opts.QualityThreshold {
					if opts.TopK > 0 && i+len(batchResults) >= opts.TopK {
						return // Found enough good results
					}
				}
			}
			
			// Report progress
			if opts.ProgressCallback != nil {
				opts.ProgressCallback(end, totalCandidates)
			}
			
			batchID++
		}
	}()
	
	return resultChan, nil
}

// ParallelStreamSearch performs parallel streaming search across multiple queries
func (s *SQLiteStore) ParallelStreamSearch(ctx context.Context, queries [][]float32, opts StreamingOptions) ([]<-chan StreamingResult, error) {
	channels := make([]<-chan StreamingResult, len(queries))
	
	for i, query := range queries {
		ch, err := s.StreamSearch(ctx, query, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to start stream for query %d: %w", i, err)
		}
		channels[i] = ch
	}
	
	return channels, nil
}

// MergeStreamResults merges multiple streaming result channels into one
func MergeStreamResults(ctx context.Context, channels ...<-chan StreamingResult) <-chan StreamingResult {
	out := make(chan StreamingResult)
	var wg sync.WaitGroup
	
	// Start a goroutine for each input channel
	for _, ch := range channels {
		wg.Add(1)
		go func(c <-chan StreamingResult) {
			defer wg.Done()
			for {
				select {
				case result, ok := <-c:
					if !ok {
						return
					}
					select {
					case out <- result:
					case <-ctx.Done():
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}(ch)
	}
	
	// Close output channel when all inputs are done
	go func() {
		wg.Wait()
		close(out)
	}()
	
	return out
}

// CollectTopKFromStream collects top-k results from a streaming channel
func CollectTopKFromStream(ctx context.Context, stream <-chan StreamingResult, k int) ([]ScoredEmbedding, error) {
	// Use a heap to maintain top-k
	topK := make([]ScoredEmbedding, 0, k)
	seen := make(map[string]bool)
	
	for {
		select {
		case result, ok := <-stream:
			if !ok {
				// Stream closed, return what we have
				return topK, nil
			}
			
			// Skip duplicates
			if seen[result.ID] {
				continue
			}
			seen[result.ID] = true
			
			// Add to top-k
			if len(topK) < k {
				topK = append(topK, result.ScoredEmbedding)
				sort.Slice(topK, func(i, j int) bool {
					return topK[i].Score > topK[j].Score
				})
			} else if result.Score > topK[k-1].Score {
				topK[k-1] = result.ScoredEmbedding
				sort.Slice(topK, func(i, j int) bool {
					return topK[i].Score > topK[j].Score
				})
			}
			
			// Check if we have k results with good scores
			if len(topK) == k && topK[k-1].Score > 0.9 {
				// Drain remaining results
				go func() {
					for range stream {
					}
				}()
				return topK, nil
			}
			
		case <-ctx.Done():
			return topK, ctx.Err()
		}
	}
}

// IncrementalIndex allows adding vectors while searching continues
type IncrementalIndex struct {
	mu        sync.RWMutex
	store     *SQLiteStore
	updates   chan *Embedding
	closed    bool
	closeOnce sync.Once
}

// NewIncrementalIndex creates a new incremental index
func NewIncrementalIndex(store *SQLiteStore) *IncrementalIndex {
	idx := &IncrementalIndex{
		store:   store,
		updates: make(chan *Embedding, 100),
	}
	
	// Start background update processor
	go idx.processUpdates()
	
	return idx
}

// AddAsync adds a vector asynchronously
func (idx *IncrementalIndex) AddAsync(emb *Embedding) error {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	
	if idx.closed {
		return fmt.Errorf("index is closed")
	}
	
	select {
	case idx.updates <- emb:
		return nil
	default:
		return fmt.Errorf("update queue is full")
	}
}

// SearchWithUpdates performs search while considering ongoing updates
func (idx *IncrementalIndex) SearchWithUpdates(ctx context.Context, query []float32, opts SearchOptions) ([]ScoredEmbedding, error) {
	// Get current results
	results, err := idx.store.Search(ctx, query, opts)
	if err != nil {
		return nil, err
	}
	
	// Check pending updates
	idx.mu.RLock()
	pendingCount := len(idx.updates)
	idx.mu.RUnlock()
	
	if pendingCount > 0 {
		// Process some pending updates quickly
		processedCount := 0
		timeout := time.After(10 * time.Millisecond)
		
		for processedCount < pendingCount && processedCount < 10 {
			select {
			case emb := <-idx.updates:
				if emb != nil {
					score := idx.store.similarityFn(query, emb.Vector)
					if opts.TopK == 0 || len(results) < opts.TopK || score > results[len(results)-1].Score {
						// This update might affect results
						results = append(results, ScoredEmbedding{
							Embedding: *emb,
							Score:     score,
						})
						
						// Re-sort
						sort.Slice(results, func(i, j int) bool {
							return results[i].Score > results[j].Score
						})
						
						// Trim to TopK
						if opts.TopK > 0 && len(results) > opts.TopK {
							results = results[:opts.TopK]
						}
					}
					
					// Re-queue for background processing
					idx.updates <- emb
				}
				processedCount++
				
			case <-timeout:
				break
			}
		}
	}
	
	return results, nil
}

// Close shuts down the incremental index
func (idx *IncrementalIndex) Close() {
	idx.closeOnce.Do(func() {
		idx.mu.Lock()
		idx.closed = true
		close(idx.updates)
		idx.mu.Unlock()
	})
}

// processUpdates handles background update processing
func (idx *IncrementalIndex) processUpdates() {
	batch := make([]*Embedding, 0, 100)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	
	for {
		select {
		case emb, ok := <-idx.updates:
			if !ok {
				// Channel closed, flush remaining batch
				if len(batch) > 0 {
					_ = idx.store.UpsertBatch(context.Background(), batch)
				}
				return
			}
			
			batch = append(batch, emb)
			
			// Flush batch if full
			if len(batch) >= 100 {
				_ = idx.store.UpsertBatch(context.Background(), batch)
				batch = batch[:0]
			}
			
		case <-ticker.C:
			// Periodic flush
			if len(batch) > 0 {
				_ = idx.store.UpsertBatch(context.Background(), batch)
				batch = batch[:0]
			}
		}
	}
}