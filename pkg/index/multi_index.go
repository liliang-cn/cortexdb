// Package index provides advanced indexing structures
package index

import (
	"sort"
	"sync"
)

// IndexType represents the type of index
type IndexType string

const (
	IndexTypeHNSW   IndexType = "hnsw"
	IndexTypeIVF    IndexType = "ivf"
	IndexTypeFlat   IndexType = "flat"
	IndexTypeHybrid IndexType = "hybrid"
)

// MultiIndex combines multiple index types for optimal performance
type MultiIndex struct {
	indices map[IndexType]VectorIndex
	config  MultiIndexConfig
	mu      sync.RWMutex
}

// MultiIndexConfig configures the multi-index strategy
type MultiIndexConfig struct {
	// Primary index for fast approximate search
	PrimaryIndex IndexType
	
	// Secondary indices for refinement
	SecondaryIndices []IndexType
	
	// Strategy for combining results
	CombineStrategy CombineStrategy
	
	// Reranking configuration
	RerankTopK int
	
	// Parallel search
	Parallel bool
}

// CombineStrategy defines how to combine results from multiple indices
type CombineStrategy string

const (
	// Take best results from primary index only
	StrategyPrimaryOnly CombineStrategy = "primary_only"
	
	// Merge results from all indices
	StrategyMergeAll CombineStrategy = "merge_all"
	
	// Use primary for candidates, secondary for reranking
	StrategyRerank CombineStrategy = "rerank"
	
	// Voting-based combination
	StrategyVoting CombineStrategy = "voting"
)

// VectorIndex is the common interface for all index types
type VectorIndex interface {
	Insert(id string, vector []float32) error
	Search(query []float32, k int) ([]string, []float32)
	Delete(id string) error
	Size() int
}

// NewMultiIndex creates a new multi-index
func NewMultiIndex(config MultiIndexConfig) *MultiIndex {
	return &MultiIndex{
		indices: make(map[IndexType]VectorIndex),
		config:  config,
	}
}

// AddIndex adds an index to the multi-index
func (m *MultiIndex) AddIndex(indexType IndexType, index VectorIndex) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.indices[indexType] = index
}

// Insert inserts a vector into all indices
func (m *MultiIndex) Insert(id string, vector []float32) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	if m.config.Parallel {
		var wg sync.WaitGroup
		errChan := make(chan error, len(m.indices))
		
		for _, index := range m.indices {
			wg.Add(1)
			go func(idx VectorIndex) {
				defer wg.Done()
				if err := idx.Insert(id, vector); err != nil {
					errChan <- err
				}
			}(index)
		}
		
		wg.Wait()
		close(errChan)
		
		// Check for errors
		for err := range errChan {
			if err != nil {
				return err
			}
		}
	} else {
		for _, index := range m.indices {
			if err := index.Insert(id, vector); err != nil {
				return err
			}
		}
	}
	
	return nil
}

// Search performs multi-index search based on strategy
func (m *MultiIndex) Search(query []float32, k int) ([]string, []float32) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	switch m.config.CombineStrategy {
	case StrategyPrimaryOnly:
		return m.searchPrimaryOnly(query, k)
	case StrategyMergeAll:
		return m.searchMergeAll(query, k)
	case StrategyRerank:
		return m.searchWithRerank(query, k)
	case StrategyVoting:
		return m.searchWithVoting(query, k)
	default:
		return m.searchPrimaryOnly(query, k)
	}
}

// searchPrimaryOnly uses only the primary index
func (m *MultiIndex) searchPrimaryOnly(query []float32, k int) ([]string, []float32) {
	primary, exists := m.indices[m.config.PrimaryIndex]
	if !exists {
		return []string{}, []float32{}
	}
	return primary.Search(query, k)
}

// searchMergeAll merges results from all indices
func (m *MultiIndex) searchMergeAll(query []float32, k int) ([]string, []float32) {
	type result struct {
		id   string
		dist float32
	}
	
	resultMap := make(map[string]float32)
	
	// Collect results from all indices
	for _, index := range m.indices {
		ids, dists := index.Search(query, k)
		for i, id := range ids {
			if existingDist, exists := resultMap[id]; !exists || dists[i] < existingDist {
				resultMap[id] = dists[i]
			}
		}
	}
	
	// Convert to slice and sort
	results := make([]result, 0, len(resultMap))
	for id, dist := range resultMap {
		results = append(results, result{id, dist})
	}
	
	sort.Slice(results, func(i, j int) bool {
		return results[i].dist < results[j].dist
	})
	
	// Extract top-k
	limit := k
	if limit > len(results) {
		limit = len(results)
	}
	
	ids := make([]string, limit)
	dists := make([]float32, limit)
	for i := 0; i < limit; i++ {
		ids[i] = results[i].id
		dists[i] = results[i].dist
	}
	
	return ids, dists
}

// searchWithRerank uses primary index for candidates, secondary for reranking
func (m *MultiIndex) searchWithRerank(query []float32, k int) ([]string, []float32) {
	primary, exists := m.indices[m.config.PrimaryIndex]
	if !exists {
		return []string{}, []float32{}
	}
	
	// Get candidates from primary index
	candidateK := k * 2
	if m.config.RerankTopK > 0 {
		candidateK = m.config.RerankTopK
	}
	candidateIDs, _ := primary.Search(query, candidateK)
	
	if len(candidateIDs) == 0 {
		return []string{}, []float32{}
	}
	
	// Rerank using secondary indices
	type result struct {
		id        string
		score     float32
		voteCount int
	}
	
	resultMap := make(map[string]*result)
	for _, id := range candidateIDs {
		resultMap[id] = &result{id: id, score: 0, voteCount: 0}
	}
	
	// Get refined distances from secondary indices
	for _, indexType := range m.config.SecondaryIndices {
		if secondary, exists := m.indices[indexType]; exists {
			ids, dists := secondary.Search(query, candidateK)
			for i, id := range ids {
				if r, exists := resultMap[id]; exists {
					r.score += dists[i]
					r.voteCount++
				}
			}
		}
	}
	
	// Average scores and sort
	results := make([]result, 0, len(resultMap))
	for _, r := range resultMap {
		if r.voteCount > 0 {
			r.score /= float32(r.voteCount)
		}
		results = append(results, *r)
	}
	
	sort.Slice(results, func(i, j int) bool {
		return results[i].score < results[j].score
	})
	
	// Return top-k
	limit := k
	if limit > len(results) {
		limit = len(results)
	}
	
	ids := make([]string, limit)
	dists := make([]float32, limit)
	for i := 0; i < limit; i++ {
		ids[i] = results[i].id
		dists[i] = results[i].score
	}
	
	return ids, dists
}

// searchWithVoting uses voting from multiple indices
func (m *MultiIndex) searchWithVoting(query []float32, k int) ([]string, []float32) {
	type vote struct {
		id    string
		score float32
		count int
		ranks []int
	}
	
	voteMap := make(map[string]*vote)
	
	// Collect votes from all indices
	for _, index := range m.indices {
		ids, dists := index.Search(query, k*2)
		for rank, id := range ids {
			if v, exists := voteMap[id]; exists {
				v.count++
				v.score += dists[rank]
				v.ranks = append(v.ranks, rank)
			} else {
				voteMap[id] = &vote{
					id:    id,
					score: dists[rank],
					count: 1,
					ranks: []int{rank},
				}
			}
		}
	}
	
	// Calculate voting scores
	votes := make([]vote, 0, len(voteMap))
	for _, v := range voteMap {
		// Weighted score: more votes = better, lower average rank = better
		avgRank := 0
		for _, r := range v.ranks {
			avgRank += r
		}
		v.score = v.score / float32(v.count) - float32(v.count)*0.1 // Bonus for more votes
		votes = append(votes, *v)
	}
	
	// Sort by voting score
	sort.Slice(votes, func(i, j int) bool {
		if votes[i].count != votes[j].count {
			return votes[i].count > votes[j].count
		}
		return votes[i].score < votes[j].score
	})
	
	// Return top-k
	limit := k
	if limit > len(votes) {
		limit = len(votes)
	}
	
	ids := make([]string, limit)
	dists := make([]float32, limit)
	for i := 0; i < limit; i++ {
		ids[i] = votes[i].id
		dists[i] = votes[i].score
	}
	
	return ids, dists
}

// Delete removes a vector from all indices
func (m *MultiIndex) Delete(id string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	for _, index := range m.indices {
		if err := index.Delete(id); err != nil {
			return err
		}
	}
	return nil
}

// Size returns the size of the primary index
func (m *MultiIndex) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	if primary, exists := m.indices[m.config.PrimaryIndex]; exists {
		return primary.Size()
	}
	return 0
}

// Stats returns statistics for all indices
func (m *MultiIndex) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	stats := make(map[string]interface{})
	stats["primary_index"] = m.config.PrimaryIndex
	stats["secondary_indices"] = m.config.SecondaryIndices
	stats["combine_strategy"] = m.config.CombineStrategy
	
	indexStats := make(map[string]int)
	for indexType, index := range m.indices {
		indexStats[string(indexType)] = index.Size()
	}
	stats["index_sizes"] = indexStats
	
	return stats
}

// HybridIndex combines HNSW for speed with IVF for accuracy
type HybridIndex struct {
	hnsw  *HNSW
	ivf   *IVFIndex
	alpha float32 // Weight between HNSW (0) and IVF (1)
	mu    sync.RWMutex
}

// NewHybridIndex creates a new hybrid HNSW+IVF index
func NewHybridIndex(dimension int, hnswM int, ivfCentroids int) *HybridIndex {
	return &HybridIndex{
		hnsw:  NewHNSW(hnswM, 200, EuclideanDistance),
		ivf:   NewIVFIndex(dimension, ivfCentroids),
		alpha: 0.5, // Default balanced weight
	}
}

// Train trains the IVF component
func (h *HybridIndex) Train(vectors [][]float32) error {
	return h.ivf.Train(vectors)
}

// Insert adds a vector to both indices
func (h *HybridIndex) Insert(id string, vector []float32) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	if err := h.hnsw.Insert(id, vector); err != nil {
		return err
	}
	return h.ivf.Add(id, vector)
}

// Search performs hybrid search
func (h *HybridIndex) Search(query []float32, k int) ([]string, []float32) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	// Get candidates from HNSW (fast)
	hnswIDs, _ := h.hnsw.Search(query, k*2, 50)
	
	// Get candidates from IVF (accurate)
	ivfIDs, ivfDists, _ := h.ivf.Search(query, k)
	
	// Merge results with preference for IVF (more accurate)
	seen := make(map[string]bool)
	results := make([]struct {
		id   string
		dist float32
	}, 0)
	
	// Add IVF results first (more accurate)
	for i, id := range ivfIDs {
		results = append(results, struct {
			id   string
			dist float32
		}{id, ivfDists[i]})
		seen[id] = true
	}
	
	// Add HNSW results not in IVF
	for _, id := range hnswIDs {
		if !seen[id] && len(results) < k {
			// Compute actual distance for HNSW results
			if node, exists := h.hnsw.Nodes[id]; exists {
				dist := h.hnsw.DistFunc(query, node.Vector)
				results = append(results, struct {
					id   string
					dist float32
				}{id, dist})
			}
		}
	}
	
	// Sort by distance
	sort.Slice(results, func(i, j int) bool {
		return results[i].dist < results[j].dist
	})
	
	// Extract top-k
	limit := k
	if limit > len(results) {
		limit = len(results)
	}
	
	ids := make([]string, limit)
	dists := make([]float32, limit)
	for i := 0; i < limit; i++ {
		ids[i] = results[i].id
		dists[i] = results[i].dist
	}
	
	return ids, dists
}

// Delete removes from both indices
func (h *HybridIndex) Delete(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	h.hnsw.Delete(id)
	// IVF doesn't support delete, would need rebuild
	return nil
}

// Size returns the size
func (h *HybridIndex) Size() int {
	return h.hnsw.Size()
}

// SetAlpha sets the weight between HNSW (0) and IVF (1)
func (h *HybridIndex) SetAlpha(alpha float32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.alpha = alpha
}

// VectorIndex adapters for interface compatibility

// HNSWAdapter wraps HNSW to implement VectorIndex interface
type HNSWAdapter struct {
	*HNSW
	defaultEf int
}

// NewHNSWAdapter creates a new HNSW adapter
func NewHNSWAdapter(dimension, M int, distFunc func([]float32, []float32) float32) *HNSWAdapter {
	return &HNSWAdapter{
		HNSW:      NewHNSW(dimension, M, distFunc),
		defaultEf: 50,
	}
}

// Search implements VectorIndex interface
func (h *HNSWAdapter) Search(query []float32, k int) ([]string, []float32) {
	ef := h.defaultEf
	if ef < k {
		ef = k * 2
	}
	return h.HNSW.Search(query, k, ef)
}

// SetEf sets the default ef parameter
func (h *HNSWAdapter) SetEf(ef int) {
	h.defaultEf = ef
}

// IVFAdapter wraps IVFIndex to implement VectorIndex interface
type IVFAdapter struct {
	*IVFIndex
}

// NewIVFAdapter creates a new IVF adapter
func NewIVFAdapter(dimension, nCentroids int) *IVFAdapter {
	return &IVFAdapter{
		IVFIndex: NewIVFIndex(dimension, nCentroids),
	}
}

// Insert implements VectorIndex interface
func (ivf *IVFAdapter) Insert(id string, vector []float32) error {
	return ivf.IVFIndex.Add(id, vector)
}

// Search implements VectorIndex interface
func (ivf *IVFAdapter) Search(query []float32, k int) ([]string, []float32) {
	ids, distances, _ := ivf.IVFIndex.Search(query, k)
	return ids, distances
}