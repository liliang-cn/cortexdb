// Package memory implements a Hindsight-inspired multi-layered memory system for AI agents.
//
// It follows the retain → recall → reflect lifecycle:
//   - Retain:  stores facts with a layer classification (WorldFact, Observation, MentalModel, Experience)
//   - Recall:  TEMPR four-channel retrieval (Semantic, Keyword/BM25, Graph, Temporal) fused with RRF
//   - Reflect: wraps recalled context with Mission / Directives / Disposition for LLM prompt injection
//
// All store mutations go through the graph store so that relationships between facts can be
// traversed later; the SQLite vector store backs semantic search and BM25 FTS5.
package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
	"github.com/liliang-cn/sqvect/v2/pkg/graph"
)

// ---------------------------------------------------------------------------
// MemoryLayer – priority hierarchy (higher-priority layers are checked first)
// ---------------------------------------------------------------------------

// MemoryLayer defines the priority tier of a stored memory record.
// The hierarchy mirrors Hindsight's knowledge pyramid.
type MemoryLayer int8

const (
	// LayerMentalModel represents user-curated high-level summaries.
	// These are injected first during Reflect and carry the highest authority.
	LayerMentalModel MemoryLayer = iota

	// LayerObservation represents automatically consolidated knowledge synthesised
	// from multiple WorldFacts, capturing patterns such as preference shifts over time.
	LayerObservation

	// LayerWorldFact represents objective facts about the world / the user.
	// This is the default layer used by the backward-compatible StoreFact helper.
	LayerWorldFact

	// LayerExperience represents the agent's own past actions or recommendations.
	LayerExperience
)

// layerToNodeType converts a MemoryLayer to its canonical graph-node type string.
func layerToNodeType(l MemoryLayer) string {
	switch l {
	case LayerMentalModel:
		return "mental_model"
	case LayerObservation:
		return "observation"
	case LayerWorldFact:
		return "world_fact"
	case LayerExperience:
		return "experience"
	default:
		return "world_fact"
	}
}

// nodeTypeToLayer reverses layerToNodeType.
func nodeTypeToLayer(t string) MemoryLayer {
	switch t {
	case "mental_model":
		return LayerMentalModel
	case "observation":
		return LayerObservation
	case "experience":
		return LayerExperience
	default:
		return LayerWorldFact
	}
}

// ---------------------------------------------------------------------------
// BankConfig – persona and reasoning constraints
// ---------------------------------------------------------------------------

// BankConfig configures a memory bank's persona and reasoning constraints.
// These settings shape the Reflect output but do not affect Recall retrieval.
type BankConfig struct {
	// Mission is a natural-language description of the agent's identity and focus area.
	// Example: "I am a coding assistant specialising in Go. I prefer simplicity over cleverness."
	Mission string `json:"mission"`

	// Directives are hard rules the agent must never violate.
	// Example: "Never recommend closed-source-only solutions."
	Directives []string `json:"directives"`

	// Disposition maps trait names (e.g., "empathy", "skepticism") to levels on a 1–5 scale,
	// subtly influencing interpretation and tone during Reflect.
	Disposition map[string]float32 `json:"disposition"`
}

// ---------------------------------------------------------------------------
// RetainInput – typed input for Retain
// ---------------------------------------------------------------------------

// RetainInput holds the information to store via Retain.
type RetainInput struct {
	// UserID identifies the memory owner.
	UserID string

	// FactID is a stable, caller-defined identifier for this fact within the user's space.
	// Combined with UserID and Layer to form the graph-node ID.
	FactID string

	// Content is the human-readable text representation of the fact.
	Content string

	// Vector is the embedding for this fact, required for semantic recall.
	Vector []float32

	// Layer classifies the memory in the knowledge hierarchy.
	Layer MemoryLayer

	// Metadata is arbitrary key-value data attached to the fact node.
	Metadata map[string]interface{}

	// TimeRef optionally records when this fact occurred, enabling temporal recall.
	// If nil, the current wall-clock time is used.
	TimeRef *time.Time
}

// ---------------------------------------------------------------------------
// RecallResult – a single ranked memory item returned by Recall
// ---------------------------------------------------------------------------

// RecallResult is a single ranked memory item from multi-channel TEMPR retrieval.
type RecallResult struct {
	// ID is the graph-node ID of this memory record.
	ID string `json:"id"`

	// Content is the human-readable text of the memory.
	Content string `json:"content"`

	// Layer indicates which tier of the knowledge hierarchy this record belongs to.
	Layer MemoryLayer `json:"layer"`

	// RRFScore is the Reciprocal Rank Fusion combined score across all active channels.
	// Higher is better.
	RRFScore float64 `json:"rrf_score"`

	// Sources lists which retrieval channels contributed to this record's score.
	// Possible values: "semantic", "keyword", "graph", "temporal".
	Sources []string `json:"sources"`

	// Metadata is the original key-value data stored with this fact.
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// ---------------------------------------------------------------------------
// MemoryContext – aggregated recall output for LLM context injection
// ---------------------------------------------------------------------------

// MemoryContext holds all recall results ready for LLM context injection.
type MemoryContext struct {
	// RecentHistory is the short-term window (last N messages from the current session).
	RecentHistory []*core.Message `json:"recent_history"`

	// RankedMemories is the TEMPR-fused, RRF-ranked list of long-term memories.
	// Items are ordered by RRFScore descending; higher-layer records receive a bonus.
	RankedMemories []*RecallResult `json:"ranked_memories"`

	// RelatedFacts is a backward-compatible view of graph-node facts.
	// New code should prefer RankedMemories.
	RelatedFacts []*graph.GraphNode `json:"related_facts"`

	// SemanticRecall is a backward-compatible view of messages from the semantic channel.
	// New code should prefer RankedMemories.
	SemanticRecall []*core.Message `json:"semantic_recall"`
}

// ---------------------------------------------------------------------------
// ConsolidateFn – user-supplied LLM hook for observation synthesis
// ---------------------------------------------------------------------------

// ConsolidateFn is a caller-provided callback that synthesises a set of new facts into an
// Observation node. The caller backs this with an LLM call.
//
//   - existing: content of the current Observation node (empty string if none exists yet).
//   - newFacts:  content strings of the newly retained facts to incorporate.
//
// Returns the synthesised observation content string.
type ConsolidateFn func(ctx context.Context, existing string, newFacts []string) (string, error)

// ---------------------------------------------------------------------------
// MemoryManager
// ---------------------------------------------------------------------------

// MemoryManager coordinates multi-layered memory Retain, Recall, and Reflect operations.
// It is safe for concurrent use.
type MemoryManager struct {
	store      *core.SQLiteStore
	graphStore *graph.GraphStore

	mu            sync.RWMutex
	bankConfig    BankConfig
	factExtractor FactExtractorFn // optional; set via SetFactExtractor
	reranker      RerankerFn      // optional; set via SetReranker
}

// NewMemoryManager creates a MemoryManager linking a vector store and a graph store.
func NewMemoryManager(s *core.SQLiteStore, g *graph.GraphStore) *MemoryManager {
	return &MemoryManager{
		store:      s,
		graphStore: g,
	}
}

// SetBankConfig atomically replaces the bank's persona configuration.
func (m *MemoryManager) SetBankConfig(cfg BankConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bankConfig = cfg
}

// GetBankConfig returns a snapshot of the current bank configuration.
func (m *MemoryManager) GetBankConfig() BankConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.bankConfig
}

// SetFactExtractor registers the hook used by RetainFromText to extract structured
// facts from raw conversation messages.
// Passing nil clears a previously registered extractor.
func (m *MemoryManager) SetFactExtractor(fn FactExtractorFn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.factExtractor = fn
}

// SetReranker registers the hook used by Recall to rerank TEMPR+RRF results using
// a cross-encoder or LLM relevance scorer.
// Passing nil clears a previously registered reranker (RRF order is used as fallback).
func (m *MemoryManager) SetReranker(fn RerankerFn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reranker = fn
}

// ---------------------------------------------------------------------------
// Retain – store a memory fact with layer classification
// ---------------------------------------------------------------------------

// Retain persists a memory into the knowledge graph with full layer metadata.
// It is the primary ingestion point for all memory layers.
func (m *MemoryManager) Retain(ctx context.Context, input RetainInput) error {
	if input.UserID == "" {
		return fmt.Errorf("retain: userID is required")
	}
	if input.FactID == "" {
		return fmt.Errorf("retain: factID is required")
	}
	if len(input.Vector) == 0 {
		return fmt.Errorf("retain: vector is required")
	}

	if input.Metadata == nil {
		input.Metadata = make(map[string]interface{})
	}

	layerStr := layerToNodeType(input.Layer)
	input.Metadata["user_id"] = input.UserID
	input.Metadata["memory_layer"] = layerStr

	// Record temporal reference to enable time-range recall.
	ref := time.Now().UTC()
	if input.TimeRef != nil {
		ref = *input.TimeRef
	}
	input.Metadata["time_ref"] = ref.Format(time.RFC3339)

	node := &graph.GraphNode{
		ID:         buildNodeID(layerStr, input.UserID, input.FactID),
		Vector:     input.Vector,
		Content:    input.Content,
		NodeType:   layerStr,
		Properties: input.Metadata,
	}

	return m.graphStore.UpsertNode(ctx, node)
}

// Consolidate synthesises a set of new facts for a user into a LayerObservation record.
// The caller provides a ConsolidateFn backed by an LLM.
// If an observation node already exists its content is passed as context so the LLM can refine it.
func (m *MemoryManager) Consolidate(ctx context.Context, userID string, newFacts []string, vec []float32, fn ConsolidateFn) error {
	if fn == nil {
		return fmt.Errorf("consolidate: ConsolidateFn must not be nil")
	}
	if userID == "" {
		return fmt.Errorf("consolidate: userID is required")
	}

	// Load existing observation so the LLM can update rather than replace it.
	obsNodeID := buildNodeID("observation", userID, "consolidated")
	existing, _ := m.graphStore.GetNode(ctx, obsNodeID)

	existingContent := ""
	if existing != nil {
		existingContent = existing.Content
	}

	synthesised, err := fn(ctx, existingContent, newFacts)
	if err != nil {
		return fmt.Errorf("consolidate: fn returned error: %w", err)
	}
	if strings.TrimSpace(synthesised) == "" {
		return fmt.Errorf("consolidate: fn returned empty observation")
	}

	return m.Retain(ctx, RetainInput{
		UserID:  userID,
		FactID:  "consolidated",
		Content: synthesised,
		Vector:  vec,
		Layer:   LayerObservation,
	})
}

// ---------------------------------------------------------------------------
// StoreFact / LinkFacts – backward-compatible helpers
// ---------------------------------------------------------------------------

// StoreFact is a backward-compatible wrapper that stores a LayerWorldFact via Retain.
func (m *MemoryManager) StoreFact(ctx context.Context, userID, factID, content string, vector []float32, metadata map[string]interface{}) error {
	return m.Retain(ctx, RetainInput{
		UserID:   userID,
		FactID:   factID,
		Content:  content,
		Vector:   vector,
		Layer:    LayerWorldFact,
		Metadata: metadata,
	})
}

// LinkFacts creates a directed relationship between two fact nodes in the knowledge graph.
func (m *MemoryManager) LinkFacts(ctx context.Context, fromID, toID, relation string, weight float64) error {
	edge := &graph.GraphEdge{
		ID:         fmt.Sprintf("link_%s_%s_%s", fromID, toID, relation),
		FromNodeID: fromID,
		ToNodeID:   toID,
		EdgeType:   relation,
		Weight:     weight,
	}
	return m.graphStore.UpsertEdge(ctx, edge)
}

// ---------------------------------------------------------------------------
// buildNodeID
// ---------------------------------------------------------------------------

// buildNodeID constructs a deterministic, human-readable graph node ID.
func buildNodeID(layerStr, userID, factID string) string {
	return fmt.Sprintf("%s_%s_%s", layerStr, userID, factID)
}
