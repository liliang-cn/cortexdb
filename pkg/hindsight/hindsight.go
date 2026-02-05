// Package hindsight provides a Hindsight-style AI agent memory system built on sqvect.
//
// It implements three core operations: Retain, Recall, and Reflect.
// This is a pure memory system - no LLM or HTTP dependencies.
// The caller is responsible for generating embeddings and extracting entities.
package hindsight

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/liliang-cn/sqvect/v2/pkg/core"
	"github.com/liliang-cn/sqvect/v2/pkg/graph"
	"github.com/liliang-cn/sqvect/v2/pkg/sqvect"
)

// System is the Hindsight memory system built on sqvect.
// It provides Retain, Recall, and Reflect operations for AI agent memory.
type System struct {
	db         *sqvect.DB
	store      core.Store
	graph      *graph.GraphStore
	collection *core.Collection
	mu         sync.RWMutex
	banks      map[string]*Bank
}

// Config configures the Hindsight system.
type Config struct {
	// DBPath is the path to the SQLite database
	DBPath string

	// VectorDim is the embedding vector dimension (0 for auto-detect)
	VectorDim int

	// Collection is the sqvect collection name for memories
	Collection string
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig(dbPath string) *Config {
	return &Config{
		DBPath:     dbPath,
		VectorDim:  0, // Auto-detect
		Collection: "memories",
	}
}

// New creates a new Hindsight memory system.
func New(cfg *Config) (*System, error) {
	db, err := sqvect.Open(sqvect.Config{
		Path:         cfg.DBPath,
		Dimensions:   cfg.VectorDim,
		SimilarityFn: core.CosineSimilarity,
		IndexType:    core.IndexTypeHNSW,
	})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	sys := &System{
		db:    db,
		store: db.Vector(),
		graph: db.Graph(),
		banks: make(map[string]*Bank),
	}

	// Initialize the store and graph schema
	ctx := context.Background()
	if err := sys.store.Init(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize store: %w", err)
	}

	// Initialize graph schema
	if err := sys.graph.InitGraphSchema(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize graph: %w", err)
	}

	// Create default collection if it doesn't exist
	if _, err := sys.store.GetCollection(ctx, cfg.Collection); err != nil {
		if _, err := sys.store.CreateCollection(ctx, cfg.Collection, cfg.VectorDim); err != nil {
			// Collection may already exist, continue
		}
	}

	// Store collection reference and load persisted banks
	collection, _ := sys.store.GetCollection(ctx, cfg.Collection)
	sys.collection = collection
	_ = sys.loadPersistedBanks(ctx)

	return sys, nil
}

// Close closes the underlying database.
func (s *System) Close() error {
	return s.db.Close()
}

// DB returns the underlying sqvect database for advanced usage.
func (s *System) DB() *sqvect.DB {
	return s.db
}

// Store returns the underlying core store for advanced usage.
func (s *System) Store() core.Store {
	return s.store
}

// Graph returns the underlying graph store for advanced usage.
func (s *System) Graph() *graph.GraphStore {
	return s.graph
}

// Retain stores a new memory in the system.
// The caller must provide pre-processed data (vector, entities).
func (s *System) Retain(ctx context.Context, mem *Memory) error {
	if mem.ID == "" {
		mem.ID = uuid.New().String()
	}
	if mem.CreatedAt.IsZero() {
		mem.CreatedAt = time.Now()
	}

	// Build metadata for sqvect
	metadata := s.buildMetadata(mem)

	// Extract entities from content for graph storage (optional, failures ignored)
	for _, entity := range mem.Entities {
		// Add entity node to graph (using a simple hash-based vector)
		node := &graph.GraphNode{
			ID:       entity,
			NodeType: "entity",
			Content:  entity,
			// Use a minimal vector for the entity node
			Vector: make([]float32, 3),
		}
		_ = s.graph.UpsertNode(ctx, node) // Ignore errors - graph is optional
	}

	// Store the memory vector and content
	embedding := &core.Embedding{
		ID:         mem.ID,
		Collection: s.getCollection(),
		Vector:     mem.Vector,
		Content:    mem.Content,
		Metadata:   metadata,
	}

	err := s.store.Upsert(ctx, embedding)
	if err != nil {
		return fmt.Errorf("store memory: %w", err)
	}

	return nil
}

// getCollection returns the collection name for memories.
func (s *System) getCollection() string {
	return "memories"
}

// buildMetadata converts a Memory to sqvect metadata map.
func (s *System) buildMetadata(mem *Memory) map[string]string {
	metadata := make(map[string]string)

	// Core metadata
	metadata["bank_id"] = mem.BankID
	metadata["memory_type"] = string(mem.Type)
	metadata["created_at"] = fmt.Sprintf("%d", mem.CreatedAt.Unix())

	// Type-specific metadata
	if mem.Type == OpinionMemory || mem.Type == ObservationMemory {
		metadata["confidence"] = fmt.Sprintf("%.4f", mem.Confidence)
	}

	// Store entities as comma-separated string for filtering
	if len(mem.Entities) > 0 {
		metadata["entities"] = strings.Join(mem.Entities, ",")
	}

	// Copy additional metadata (convert to string)
	for k, v := range mem.Metadata {
		metadata[k] = fmt.Sprintf("%v", v)
	}

	return metadata
}

// Recall retrieves memories using the specified TEMPR strategies.
func (s *System) Recall(ctx context.Context, req *RecallRequest) ([]*RecallResult, error) {
	if req.Strategy == nil {
		req.Strategy = DefaultStrategy()
	}

	var allResults []*RecallResult
	var wg sync.WaitGroup
	resultsMu := sync.Mutex{}

	// Run enabled strategies in parallel
	if req.Strategy.Memory && req.QueryVector != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if results := s.semanticSearch(ctx, req); results != nil {
				resultsMu.Lock()
				allResults = append(allResults, results...)
				resultsMu.Unlock()
			}
		}()
	}

	if req.Strategy.Priming && req.Query != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if results := s.keywordSearch(ctx, req); results != nil {
				resultsMu.Lock()
				allResults = append(allResults, results...)
				resultsMu.Unlock()
			}
		}()
	}

	if req.Strategy.Temporal != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if results := s.temporalSearch(ctx, req); results != nil {
				resultsMu.Lock()
				allResults = append(allResults, results...)
				resultsMu.Unlock()
			}
		}()
	}

	if len(req.Strategy.Entity) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if results := s.entitySearch(ctx, req); results != nil {
				resultsMu.Lock()
				allResults = append(allResults, results...)
				resultsMu.Unlock()
			}
		}()
	}

	wg.Wait()

	// Apply RRF (Reciprocal Rank Fusion) to combine results
	merged := s.rrfFuse(allResults)

	// Apply top-K limit
	if req.TopK > 0 && len(merged) > req.TopK {
		merged = merged[:req.TopK]
	}

	return merged, nil
}

// semanticSearch performs vector similarity search.
func (s *System) semanticSearch(ctx context.Context, req *RecallRequest) []*RecallResult {
	topK := req.Strategy.TopK
	if topK == 0 {
		topK = req.TopK
	}
	if topK == 0 {
		topK = 10
	}

	opts := core.SearchOptions{
		Collection: s.getCollection(),
		TopK:       topK,
		Filter:     map[string]string{"bank_id": req.BankID},
	}

	results, err := s.store.Search(ctx, req.QueryVector, opts)
	if err != nil {
		return nil
	}

	out := make([]*RecallResult, 0, len(results))
	for _, r := range results {
		mem := s.scoredEmbeddingToMemory(&r)
		if mem != nil {
			out = append(out, &RecallResult{
				Memory:   mem,
				Score:    r.Score,
				Strategy: "semantic",
			})
		}
	}
	return out
}

// keywordSearch performs BM25 keyword search using HybridSearch.
func (s *System) keywordSearch(ctx context.Context, req *RecallRequest) []*RecallResult {
	topK := req.Strategy.TopK
	if topK == 0 {
		topK = req.TopK
	}
	if topK == 0 {
		topK = 10
	}

	// Use HybridSearch for keyword + vector combination
	// HybridSearch combines FTS5 keyword search with vector search using RRF
	opts := core.HybridSearchOptions{
		SearchOptions: core.SearchOptions{
			Collection: s.getCollection(),
			TopK:       topK,
			Filter:     map[string]string{"bank_id": req.BankID},
			QueryText:  req.Query,
			TextWeight: 0.7, // Higher weight for text similarity
		},
		RRFK: 60, // RRF constant
	}

	var results []core.ScoredEmbedding
	var err error

	if req.QueryVector != nil {
		results, err = s.store.HybridSearch(ctx, req.QueryVector, req.Query, opts)
	} else {
		// No vector provided - just do semantic search with text similarity
		searchOpts := core.SearchOptions{
			Collection: s.getCollection(),
			TopK:       topK,
			Filter:     map[string]string{"bank_id": req.BankID},
			QueryText:  req.Query,
			TextWeight: 1.0, // Text-only
		}
		// Need at least a minimal vector for search
		dummyVec := make([]float32, 1)
		results, err = s.store.Search(ctx, dummyVec, searchOpts)
	}

	if err != nil {
		return nil
	}

	out := make([]*RecallResult, 0, len(results))
	for _, r := range results {
		mem := s.scoredEmbeddingToMemory(&r)
		if mem != nil {
			out = append(out, &RecallResult{
				Memory:   mem,
				Score:    r.Score,
				Strategy: "keyword",
			})
		}
	}
	return out
}

// temporalSearch performs time-range filtered search.
func (s *System) temporalSearch(ctx context.Context, req *RecallRequest) []*RecallResult {
	tf := req.Strategy.Temporal
	if tf == nil {
		return nil
	}

	topK := req.Strategy.TopK
	if topK == 0 {
		topK = req.TopK
	}
	if topK == 0 {
		topK = 10
	}

	// For temporal search, we first get all bank memories then filter
	opts := core.SearchOptions{
		Collection: s.getCollection(),
		TopK:       topK * 5, // Get more to filter
		Filter:     map[string]string{"bank_id": req.BankID},
	}

	if req.QueryVector == nil {
		return nil // Need vector for search
	}

	results, err := s.store.Search(ctx, req.QueryVector, opts)
	if err != nil {
		return nil
	}

	// Filter by time range
	out := make([]*RecallResult, 0)
	for _, r := range results {
		mem := s.scoredEmbeddingToMemory(&r)
		if mem == nil {
			continue
		}

		// Check time range
		inRange := true
		if tf.Start != nil && mem.CreatedAt.Before(*tf.Start) {
			inRange = false
		}
		if tf.End != nil && mem.CreatedAt.After(*tf.End) {
			inRange = false
		}

		if inRange {
			out = append(out, &RecallResult{
				Memory:   mem,
				Score:    r.Score,
				Strategy: "temporal",
			})
		}
	}

	return out
}

// entitySearch performs graph-based entity search.
func (s *System) entitySearch(ctx context.Context, req *RecallRequest) []*RecallResult {
	if len(req.Strategy.Entity) == 0 {
		return nil
	}

	topK := req.Strategy.TopK
	if topK == 0 {
		topK = req.TopK
	}
	if topK == 0 {
		topK = 10
	}

	// Find memories that contain the specified entities
	// We use metadata filtering for this
	var allResults []*RecallResult
	seen := make(map[string]bool)

	for _, entity := range req.Strategy.Entity {
		opts := core.SearchOptions{
			Collection: s.getCollection(),
			TopK:       topK,
			Filter:     map[string]string{"bank_id": req.BankID},
		}

		var results []core.ScoredEmbedding
		var err error

		if req.QueryVector != nil {
			results, err = s.store.Search(ctx, req.QueryVector, opts)
		} else {
			// No vector - can't search effectively
			continue
		}

		if err != nil {
			continue
		}

		for _, r := range results {
			if !seen[r.ID] {
				// Check if this memory contains the entity
				if entities, ok := r.Metadata["entities"]; ok {
					if strings.Contains(entities, entity) {
						seen[r.ID] = true
						mem := s.scoredEmbeddingToMemory(&r)
						if mem != nil {
							allResults = append(allResults, &RecallResult{
								Memory:   mem,
								Score:    r.Score,
								Strategy: "entity",
							})
						}
					}
				}
			}
		}
	}

	return allResults
}

// rrfFuse combines results from multiple strategies using Reciprocal Rank Fusion.
// RRF formula: score = sum(1 / (k + rank)) for each strategy
// where k is a constant (typically 60)
func (s *System) rrfFuse(results []*RecallResult) []*RecallResult {
	const k = 60

	if len(results) == 0 {
		return results
	}

	// Group by memory ID
	byID := make(map[string]*RecallResult)
	ranks := make(map[string]map[string]int) // memoryID -> strategy -> rank

	// Track rank per strategy
	byStrategy := make(map[string][]*RecallResult)
	for _, r := range results {
		byStrategy[r.Strategy] = append(byStrategy[r.Strategy], r)
	}

	// Calculate ranks and aggregate
	for strategy, stratResults := range byStrategy {
		for rank, r := range stratResults {
			if ranks[r.ID] == nil {
				ranks[r.ID] = make(map[string]int)
			}
			ranks[r.ID][strategy] = rank + 1

			if existing := byID[r.ID]; existing == nil {
				byID[r.ID] = r
				r.Score = 0
			}
		}
	}

	// Calculate RRF scores
	for id, r := range byID {
		rrfScore := 0.0
		for _, rank := range ranks[id] {
			rrfScore += 1.0 / (k + float64(rank))
		}
		r.Score = rrfScore
	}

	// Convert to slice and sort by score
	merged := make([]*RecallResult, 0, len(byID))
	for _, r := range byID {
		merged = append(merged, r)
	}

	// Sort by score descending
	for i := 0; i < len(merged); i++ {
		for j := i + 1; j < len(merged); j++ {
			if merged[j].Score > merged[i].Score {
				merged[i], merged[j] = merged[j], merged[i]
			}
		}
	}

	return merged
}

// scoredEmbeddingToMemory converts a core.ScoredEmbedding to a Memory.
func (s *System) scoredEmbeddingToMemory(r *core.ScoredEmbedding) *Memory {
	if r == nil {
		return nil
	}

	mem := &Memory{
		ID:        r.ID,
		Content:   r.Content,
		Vector:    r.Vector,
		Metadata:  make(map[string]any),
		CreatedAt: time.Now(),
	}

	// Extract metadata
	if bankID, ok := r.Metadata["bank_id"]; ok {
		mem.BankID = bankID
	}
	if memType, ok := r.Metadata["memory_type"]; ok {
		mem.Type = MemoryType(memType)
	}
	if createdStr, ok := r.Metadata["created_at"]; ok {
		var created int64
		fmt.Sscanf(createdStr, "%d", &created)
		mem.CreatedAt = time.Unix(created, 0)
	}
	if confStr, ok := r.Metadata["confidence"]; ok {
		var conf float64
		fmt.Sscanf(confStr, "%f", &conf)
		mem.Confidence = conf
	}
	if entities, ok := r.Metadata["entities"]; ok && entities != "" {
		mem.Entities = strings.Split(entities, ",")
	}

	return mem
}

// Reflect generates context for Agent reasoning based on retrieved memories.
// This does NOT call an LLM - it formats memories for the caller to use.
func (s *System) Reflect(ctx context.Context, req *ContextRequest) (*ContextResponse, error) {
	// First, recall relevant memories
	recallReq := &RecallRequest{
		BankID:      req.BankID,
		Query:       req.Query,
		QueryVector: req.QueryVector,
		Strategy:    req.Strategy,
		TopK:        req.TopK,
	}

	results, err := s.Recall(ctx, recallReq)
	if err != nil {
		return nil, fmt.Errorf("recall memories: %w", err)
	}

	// Get bank disposition for context formatting
	s.mu.RLock()
	bank := s.banks[req.BankID]
	s.mu.RUnlock()

	// Format context
	context := s.formatContext(results, bank, req.Query)

	// Count tokens (rough estimate: ~4 chars per token)
	tokenCount := len(context) / 4
	if req.TokenBudget > 0 && tokenCount > req.TokenBudget {
		// Truncate context to fit budget
		context = context[:req.TokenBudget*4]
		tokenCount = req.TokenBudget
	}

	// Extract memories
	memories := make([]*Memory, len(results))
	for i, r := range results {
		memories[i] = r.Memory
	}

	return &ContextResponse{
		Context:    context,
		Memories:   memories,
		TokenCount: tokenCount,
	}, nil
}

// formatContext formats retrieved memories into LLM-ready context text.
func (s *System) formatContext(results []*RecallResult, bank *Bank, query string) string {
	if len(results) == 0 {
		return "No relevant memories found."
	}

	var b strings.Builder

	b.WriteString(fmt.Sprintf("# Relevant Memories for: %s\n\n", query))

	// Group by memory type
	byType := make(map[MemoryType][]*RecallResult)
	for _, r := range results {
		byType[r.Memory.Type] = append(byType[r.Memory.Type], r)
	}

	// Output World facts
	if world, ok := byType[WorldMemory]; ok && len(world) > 0 {
		b.WriteString("## Facts\n")
		for _, r := range world {
			b.WriteString(fmt.Sprintf("- %s (score: %.3f)\n", r.Content, r.Score))
		}
		b.WriteString("\n")
	}

	// Output Bank experiences
	if experiences, ok := byType[BankMemory]; ok && len(experiences) > 0 {
		b.WriteString("## Experiences\n")
		for _, r := range experiences {
			b.WriteString(fmt.Sprintf("- %s\n", r.Content))
		}
		b.WriteString("\n")
	}

	// Output Observations (derived insights)
	if observations, ok := byType[ObservationMemory]; ok && len(observations) > 0 {
		b.WriteString("## Insights\n")
		for _, r := range observations {
			confidence := r.Memory.Confidence
			b.WriteString(fmt.Sprintf("- %s (confidence: %.2f)\n", r.Content, confidence))
		}
		b.WriteString("\n")
	}

	// Output Opinions
	if opinions, ok := byType[OpinionMemory]; ok && len(opinions) > 0 {
		b.WriteString("## Beliefs\n")
		for _, r := range opinions {
			confidence := r.Memory.Confidence
			b.WriteString(fmt.Sprintf("- %s (confidence: %.2f)\n", r.Content, confidence))
		}
	}

	return b.String()
}

// CreateBank creates a new memory bank and persists it to the database.
func (s *System) CreateBank(ctx context.Context, bank *Bank) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.banks[bank.ID]; exists {
		return fmt.Errorf("bank %s already exists", bank.ID)
	}

	bank.CreatedAt = time.Now().Unix()

	// Persist bank to graph_nodes for cross-session persistence
	vectorDim := 1536 // default dimension
	if s.collection != nil && s.collection.Dimensions > 0 {
		vectorDim = s.collection.Dimensions
	}
	// Create a small non-zero vector to pass validation
	vector := make([]float32, vectorDim)
	if len(vector) > 0 {
		vector[0] = 0.0001 // Small non-zero value
	}

	properties := map[string]interface{}{
		"name":        bank.Name,
		"description": bank.Description,
		"background": bank.Background,
		"skepticism":  bank.Skepticism,
		"literalism":  bank.Literalism,
		"empathy":     bank.Empathy,
		"created_at":  bank.CreatedAt,
	}

	if err := s.graph.UpsertNode(ctx, &graph.GraphNode{
		ID:         bank.ID,
		Vector:     vector,
		Content:    bank.Name,
		NodeType:   "bank",
		Properties: properties,
	}); err != nil {
		// Log but don't fail - bank is still in memory
		fmt.Printf("[Warning] Failed to persist bank to graph: %v\n", err)
	}

	s.banks[bank.ID] = bank
	return nil
}

// GetBank retrieves a memory bank by ID.
// First checks in-memory cache, then loads from database if not found.
func (s *System) GetBank(bankID string) (*Bank, bool) {
	s.mu.RLock()
	bank, ok := s.banks[bankID]
	s.mu.RUnlock()

	if ok {
		return bank, true
	}

	// Try to load from database graph
	ctx := context.Background()
	node, err := s.graph.GetNode(ctx, bankID)
	if err == nil && node.NodeType == "bank" {
		bank := &Bank{
			ID:          node.ID,
			Name:        node.Content,
			Description: node.Content,
			CreatedAt:   node.CreatedAt.Unix(),
		}

		s.mu.Lock()
		s.banks[bankID] = bank
		s.mu.Unlock()

		return bank, true
	}

	return nil, false
}

// ListBanks returns all registered banks.
func (s *System) ListBanks() []*Bank {
	s.mu.RLock()
	defer s.mu.RUnlock()

	banks := make([]*Bank, 0, len(s.banks))
	for _, b := range s.banks {
		banks = append(banks, b)
	}
	return banks
}

// DeleteBank removes a memory bank.
func (s *System) DeleteBank(bankID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.banks[bankID]; !exists {
		return fmt.Errorf("bank %s not found", bankID)
	}

	delete(s.banks, bankID)

	// Also delete from graph database for persistence
	ctx := context.Background()
	_ = s.graph.DeleteNode(ctx, bankID)

	return nil
}

// Observe performs deep reflection to generate observations from memories.
// This is the true "Reflect" operation that forms new connections and persists them.
// The caller can optionally provide generated observations, or the system can
// analyze patterns in existing memories.
func (s *System) Observe(ctx context.Context, req *ReflectRequest) (*ReflectResponse, error) {
	// First, recall relevant memories
	recallReq := &RecallRequest{
		BankID:      req.BankID,
		Query:       req.Query,
		QueryVector: req.QueryVector,
		Strategy:    req.Strategy,
		TopK:        req.TopK,
	}

	results, err := s.Recall(ctx, recallReq)
	if err != nil {
		return nil, fmt.Errorf("recall memories: %w", err)
	}

	// Get bank disposition
	s.mu.RLock()
	bank := s.banks[req.BankID]
	s.mu.RUnlock()

	// Extract source memories
	sourceMemories := make([]*Memory, len(results))
	for i, r := range results {
		sourceMemories[i] = r.Memory
	}

	// Generate observations based on memory patterns
	// In a full implementation, this would use an LLM to generate observations.
	// For now, we provide a framework for the caller to add observations.
	observations := s.detectPatterns(sourceMemories, bank, req)

	// Persist observations that meet confidence threshold
	for _, obs := range observations {
		if obs.Confidence >= req.MinConfidence {
			if err := s.RetainObservation(ctx, obs); err != nil {
				return nil, fmt.Errorf("persist observation: %w", err)
			}
		}
	}

	// Also generate formatted context for backward compatibility
	context := s.formatReflectContext(results, observations, bank, req.Query)

	return &ReflectResponse{
		Observations:   observations,
		Context:        context,
		SourceMemories: sourceMemories,
	}, nil
}

// detectPatterns analyzes memories to detect patterns and generate observations.
// This is a simplified implementation - a full version would use an LLM.
func (s *System) detectPatterns(memories []*Memory, bank *Bank, req *ReflectRequest) []*Observation {
	var observations []*Observation

	if len(memories) < 2 {
		return observations
	}

	// Group memories by entities to find patterns
	byEntity := make(map[string][]*Memory)
	for _, m := range memories {
		for _, e := range m.Entities {
			byEntity[e] = append(byEntity[e], m)
		}
	}

	// Detect patterns for entities with multiple memories
	for entity, entityMemories := range byEntity {
		if len(entityMemories) >= 2 {
			// Check for pattern: repeated interactions or behaviors
			if s.isPreferencePattern(entityMemories) {
				obs := &Observation{
					ID:              uuid.New().String(),
					BankID:          req.BankID,
					Content:         fmt.Sprintf("%s shows consistent patterns in behavior", entity),
					SourceMemoryIDs: memoryIDs(entityMemories),
					Confidence:      s.calculateConfidence(entityMemories, bank),
					ObservationType: PreferenceObservation,
					Reasoning:       "Multiple memories indicate consistent behavior patterns",
					CreatedAt:       time.Now(),
				}
				observations = append(observations, obs)
			}

			// Check for causal patterns
			if s.isCausalPattern(entityMemories) {
				obs := &Observation{
					ID:              uuid.New().String(),
					BankID:          req.BankID,
					Content:         fmt.Sprintf("%s's actions show cause-effect patterns", entity),
					SourceMemoryIDs: memoryIDs(entityMemories),
					Confidence:      s.calculateConfidence(entityMemories, bank),
					ObservationType: CausalObservation,
					Reasoning:       "Identified cause-effect relationship patterns",
					CreatedAt:       time.Now(),
				}
				observations = append(observations, obs)
			}
		}
	}

	// Detect generalization patterns across all memories
	if len(memories) >= 3 {
		generalization := s.generalizeFromMemories(memories, bank, req)
		if generalization != nil {
			observations = append(observations, generalization)
		}
	}

	return observations
}

// isPreferencePattern checks if memories indicate a preference pattern.
func (s *System) isPreferencePattern(memories []*Memory) bool {
	preferenceKeywords := []string{"prefers", "likes", "enjoys", "prefers", "chooses", "selects"}
	for _, m := range memories {
		content := strings.ToLower(m.Content)
		for _, kw := range preferenceKeywords {
			if strings.Contains(content, kw) {
				return true
			}
		}
	}
	return false
}

// isCausalPattern checks if memories indicate cause-effect patterns.
func (s *System) isCausalPattern(memories []*Memory) bool {
	causalKeywords := []string{"because", "since", "due to", "caused", "resulted in", "led to"}
	for _, m := range memories {
		content := strings.ToLower(m.Content)
		for _, kw := range causalKeywords {
			if strings.Contains(content, kw) {
				return true
			}
		}
	}
	return false
}

// generalizeFromMemories creates a generalization observation from multiple memories.
func (s *System) generalizeFromMemories(memories []*Memory, bank *Bank, req *ReflectRequest) *Observation {
	// Look for common themes
	themes := make(map[string]int)
	for _, m := range memories {
		words := strings.Fields(strings.ToLower(m.Content))
		for _, w := range words {
			if len(w) > 4 { // Only consider longer words
				themes[w]++
			}
		}
	}

	// Find the most common theme
	var topTheme string
	var maxCount int
	for theme, count := range themes {
		if count > maxCount && count >= 2 {
			maxCount = count
			topTheme = theme
		}
	}

	if topTheme == "" {
		return nil
	}

	return &Observation{
		ID:              uuid.New().String(),
		BankID:          req.BankID,
		Content:         fmt.Sprintf("General pattern: interactions involving %s show consistency", topTheme),
		SourceMemoryIDs: memoryIDs(memories),
		Confidence:      s.calculateConfidence(memories, bank),
		ObservationType: GeneralizationObservation,
		Reasoning:       "Multiple memories share common themes",
		CreatedAt:       time.Now(),
	}
}

// calculateConfidence calculates observation confidence based on memory evidence and bank disposition.
func (s *System) calculateConfidence(memories []*Memory, bank *Bank) float64 {
	if len(memories) == 0 {
		return 0
	}

	// Base confidence from number of supporting memories
	baseConf := float64(len(memories)) / 10.0
	if baseConf > 0.9 {
		baseConf = 0.9
	}

	// Adjust based on bank disposition
	if bank != nil {
		// High skepticism reduces confidence
		if bank.Skepticism >= 4 {
			baseConf *= 0.8
		}
		// Low skepticism increases confidence
		if bank.Skepticism <= 2 {
			baseConf *= 1.1
		}
	}

	if baseConf > 1.0 {
		baseConf = 1.0
	}
	if baseConf < 0.1 {
		baseConf = 0.1
	}

	return baseConf
}

// memoryIDs extracts IDs from a slice of memories.
func memoryIDs(memories []*Memory) []string {
	ids := make([]string, len(memories))
	for i, m := range memories {
		ids[i] = m.ID
	}
	return ids
}

// RetainObservation stores an observation as a memory.
func (s *System) RetainObservation(ctx context.Context, obs *Observation) error {
	if obs.ID == "" {
		obs.ID = uuid.New().String()
	}
	if obs.CreatedAt.IsZero() {
		obs.CreatedAt = time.Now()
	}

	// Store source memory IDs in metadata
	metadata := make(map[string]any)
	metadata["source_memory_ids"] = strings.Join(obs.SourceMemoryIDs, ",")
	metadata["observation_type"] = string(obs.ObservationType)
	metadata["reasoning"] = obs.Reasoning

	// Create a memory from the observation
	mem := &Memory{
		ID:        obs.ID,
		BankID:    obs.BankID,
		Type:      ObservationMemory,
		Content:   obs.Content,
		Vector:    obs.Vector,
		Entities:  []string{}, // Could extract from content
		Confidence: obs.Confidence,
		Metadata:  metadata,
		CreatedAt: obs.CreatedAt,
	}

	return s.Retain(ctx, mem)
}

// formatReflectContext formats memories and observations into context.
func (s *System) formatReflectContext(results []*RecallResult, observations []*Observation, bank *Bank, query string) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("# Reflection on: %s\n\n", query))

	// Group memories by type
	byType := make(map[MemoryType][]*RecallResult)
	for _, r := range results {
		byType[r.Memory.Type] = append(byType[r.Memory.Type], r)
	}

	// Facts
	if world, ok := byType[WorldMemory]; ok && len(world) > 0 {
		b.WriteString("## Known Facts\n")
		for _, r := range world {
			b.WriteString(fmt.Sprintf("- %s\n", r.Content))
		}
		b.WriteString("\n")
	}

	// Experiences
	if experiences, ok := byType[BankMemory]; ok && len(experiences) > 0 {
		b.WriteString("## Past Experiences\n")
		for _, r := range experiences {
			b.WriteString(fmt.Sprintf("- %s\n", r.Content))
		}
		b.WriteString("\n")
	}

	// Observations (new insights from reflection)
	if len(observations) > 0 {
		b.WriteString("## New Insights\n")
		for _, obs := range observations {
			b.WriteString(fmt.Sprintf("- %s (confidence: %.2f)\n", obs.Content, obs.Confidence))
		}
		b.WriteString("\n")
	}

	// Existing opinions
	if opinions, ok := byType[OpinionMemory]; ok && len(opinions) > 0 {
		b.WriteString("## Existing Beliefs\n")
		for _, r := range opinions {
			b.WriteString(fmt.Sprintf("- %s\n", r.Content))
		}
	}

	return b.String()
}

// AddObservation adds a pre-generated observation to the memory bank.
// This allows the caller (e.g., an LLM) to generate observations and persist them.
func (s *System) AddObservation(ctx context.Context, obs *Observation) error {
	return s.RetainObservation(ctx, obs)
}

// GetObservations retrieves observations for a bank.
func (s *System) GetObservations(ctx context.Context, bankID string) ([]*Observation, error) {
	opts := core.SearchOptions{
		Collection: s.getCollection(),
		TopK:       100,
		Filter: map[string]string{
			"bank_id":     bankID,
			"memory_type": string(ObservationMemory),
		},
	}

	// Use a dummy vector for search
	dummyVec := make([]float32, 1)
	results, err := s.store.Search(ctx, dummyVec, opts)
	if err != nil {
		return nil, fmt.Errorf("search observations: %w", err)
	}

	var observations []*Observation
	for _, r := range results {
		mem := s.scoredEmbeddingToMemory(&r)
		if mem != nil && mem.Type == ObservationMemory {
			obs := &Observation{
				ID:         mem.ID,
				BankID:     mem.BankID,
				Content:    mem.Content,
				Vector:     mem.Vector,
				Confidence: mem.Confidence,
				CreatedAt:  mem.CreatedAt,
			}

			// Extract metadata
			if sourceIDs, ok := mem.Metadata["source_memory_ids"].(string); ok {
				obs.SourceMemoryIDs = strings.Split(sourceIDs, ",")
			}
			if obsType, ok := mem.Metadata["observation_type"].(string); ok {
				obs.ObservationType = ObservationType(obsType)
			}
			if reasoning, ok := mem.Metadata["reasoning"].(string); ok {
				obs.Reasoning = reasoning
			}

			observations = append(observations, obs)
		}
	}

	return observations, nil
}

// loadPersistedBanks loads banks from graph_nodes on system startup
func (s *System) loadPersistedBanks(ctx context.Context) error {
	filter := &graph.GraphFilter{
		NodeTypes: []string{"bank"},
	}

	nodes, err := s.graph.GetAllNodes(ctx, filter)
	if err != nil {
		return nil // Don't fail on load error
	}

	for _, node := range nodes {
		bank := &Bank{
			ID:          node.ID,
			Name:        node.Content,
			Description: node.Content,
			CreatedAt:   node.CreatedAt.Unix(),
		}

		s.banks[bank.ID] = bank
	}

	return nil
}

// nodeToBank converts a graph node to a Bank helper
func (s *System) nodeToBank(node *graph.GraphNode) *Bank {
	if node.NodeType != "bank" {
		return nil
	}

	return &Bank{
		ID:          node.ID,
		Name:        node.Content,
		Description: node.Content,
		CreatedAt:   node.CreatedAt.Unix(),
	}
}
