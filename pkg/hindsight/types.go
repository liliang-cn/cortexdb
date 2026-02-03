// Package hindsight provides a Hindsight-style AI agent memory system built on sqvect.
//
// It implements three core operations: Retain, Recall, and Reflect.
// This is a pure memory system - no LLM or HTTP dependencies.
// The caller is responsible for generating embeddings and extracting entities.
package hindsight

import "time"

// MemoryType represents the epistemic category of a memory.
// Hindsight separates memories by type for clarity:
//   - World: Objective facts received ("Alice works at Google")
//   - Bank: Bank's own actions ("I recommended Python to Bob")
//   - Opinion: Formed beliefs with confidence ("Python is best for ML" with 0.85 confidence)
//   - Observation: Complex mental models derived from reflection
type MemoryType string

const (
	WorldMemory      MemoryType = "world"      // Objective facts about the world
	BankMemory       MemoryType = "bank"       // Agent's own experiences and actions
	OpinionMemory    MemoryType = "opinion"    // Formed beliefs with confidence scores
	ObservationMemory MemoryType = "observation" // Mental models derived from reflection
)

// Memory represents a single stored memory.
// The caller provides pre-processed data (vectors, entities).
type Memory struct {
	// ID is the unique identifier for this memory
	ID string

	// BankID identifies which memory bank this memory belongs to
	BankID string

	// Type categorizes the memory epistemically
	Type MemoryType

	// Content is the original text content
	Content string

	// Vector is the embedding vector (provided by caller)
	Vector []float32

	// Entities are related people, places, concepts (provided by caller)
	Entities []string

	// Confidence is the belief strength (0-1, only used for Opinion type)
	Confidence float64

	// Metadata holds additional information like timestamps, sources, etc.
	Metadata map[string]any

	// CreatedAt is when this memory was stored
	CreatedAt time.Time
}

// RecallStrategy configures which TEMPR strategies to use for recall.
// TEMPR stands for Temporal, Entity, Memory, Priming, and Recall.
type RecallStrategy struct {
	// Temporal filters by time range
	Temporal *TemporalFilter

	// Entity filters by specific entities
	Entity []string

	// Memory enables semantic vector search
	Memory bool

	// Priming enables keyword/BM25 search
	Priming bool

	// TopK limits results per strategy (0 = no limit)
	TopK int
}

// DefaultStrategy returns a strategy with all TEMPR methods enabled.
func DefaultStrategy() *RecallStrategy {
	return &RecallStrategy{
		Temporal: nil,
		Entity:   nil,
		Memory:   true,
		Priming:  true,
		TopK:     10,
	}
}

// TemporalFilter specifies a time range for temporal recall.
type TemporalFilter struct {
	Start *time.Time // Inclusive start time
	End   *time.Time // Inclusive end time
}

// RecallRequest is the input for the Recall operation.
type RecallRequest struct {
	// BankID specifies which memory bank to search
	BankID string

	// Query is the search query text
	Query string

	// QueryVector is the embedding of the query (provided by caller)
	QueryVector []float32

	// Strategy configures which TEMPR strategies to use
	Strategy *RecallStrategy

	// TopK is the maximum total results to return
	TopK int
}

// RecallResult is a scored memory returned from recall.
type RecallResult struct {
	*Memory
	Score    float64 // Combined relevance score
	Strategy string  // Which strategy produced this result
}

// ContextRequest is the input for the Reflect operation.
// Reflect returns formatted context for Agent reasoning (no LLM calls).
type ContextRequest struct {
	// BankID specifies which memory bank to use
	BankID string

	// Query is the question or topic to reflect on
	Query string

	// QueryVector is the embedding of the query (provided by caller)
	QueryVector []float32

	// Strategy configures which TEMPR strategies to use
	Strategy *RecallStrategy

	// TopK is the maximum memories to include in context
	TopK int

	// TokenBudget is the approximate max tokens for output (0 = no limit)
	TokenBudget int
}

// ContextResponse contains the formatted context for Agent reasoning.
type ContextResponse struct {
	// Context is the formatted text ready for LLM consumption
	Context string

	// Memories are the retrieved memories that formed the context
	Memories []*Memory

	// TokenCount is the approximate token count of the context
	TokenCount int
}

// Observation represents a mental model derived from reflection.
// Observations connect multiple memories to form new insights.
type Observation struct {
	// ID is the unique identifier
	ID string

	// BankID is the memory bank this observation belongs to
	BankID string

	// Content is the derived insight/observation
	Content string

	// Vector is the embedding of the observation
	Vector []float32

	// SourceMemoryIDs are the memories that led to this observation
	SourceMemoryIDs []string

	// Confidence is the confidence in this observation (0-1)
	Confidence float64

	// ObservationType categorizes the observation
	ObservationType ObservationType

	// Reasoning explains how this observation was derived
	Reasoning string

	// CreatedAt is when this observation was created
	CreatedAt time.Time
}

// ObservationType represents the category of observation.
type ObservationType string

const (
	// PatternObservation: Recognized patterns across experiences
	PatternObservation ObservationType = "pattern"
	// CausalObservation: Understood cause-effect relationships
	CausalObservation ObservationType = "causal"
	// GeneralizationObservation: General rules from specific instances
	GeneralizationObservation ObservationType = "generalization"
	// PreferenceObservation: Learned user/system preferences
	PreferenceObservation ObservationType = "preference"
	// RiskObservation: Identified risks or potential issues
	RiskObservation ObservationType = "risk"
	// StrategyObservation: Effective strategies learned
	StrategyObservation ObservationType = "strategy"
)

// ReflectRequest is the input for generating observations through reflection.
type ReflectRequest struct {
	// BankID is the memory bank to reflect on
	BankID string

	// Query is the topic or question to reflect on
	Query string

	// QueryVector is the embedding of the query (provided by caller)
	QueryVector []float32

	// Strategy configures which TEMPR strategies to use for finding relevant memories
	Strategy *RecallStrategy

	// TopK is the maximum memories to consider for reflection
	TopK int

	// ObservationTypes specifies which types of observations to generate
	// If empty, generates all relevant types
	ObservationTypes []ObservationType

	// MinConfidence is the minimum confidence for an observation to be persisted
	MinConfidence float64
}

// ReflectResponse contains the results of reflection.
type ReflectResponse struct {
	// Observations are the new insights generated from reflection
	Observations []*Observation

	// Context is the formatted context (for backward compatibility)
	Context string

	// SourceMemories are the memories that informed these observations
	SourceMemories []*Memory
}
