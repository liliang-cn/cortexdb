package memory

// hooks.go defines the two extensibility hooks that allow callers to inject LLM or
// model-backed logic into the memory pipeline without coupling sqvect to any specific
// LLM provider.
//
// Hook 1 – FactExtractorFn
//   Plugs into RetainFromText(): the caller supplies raw conversation messages; the hook
//   extracts structured facts (with embeddings) which are then persisted via Retain.
//   Typical implementations: an OpenAI/Anthropic structured-output call, a local Ollama
//   model, a simple rule-based extractor for testing.
//
// Hook 2 – RerankerFn
//   Plugs into Recall(): after the TEMPR four-channel retrieval and RRF fusion, the hook
//   reranks the candidate list using a more expensive cross-encoder or LLM relevance scorer.
//   Without this hook Recall returns the raw RRF-sorted list.
//   Typical implementations: a Sentence-Transformers cross-encoder via a gRPC sidecar,
//   an LLM asked "rank these by relevance", a Cohere Rerank API call.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
)

// ---------------------------------------------------------------------------
// ExtractedFact – output element of FactExtractorFn
// ---------------------------------------------------------------------------

// ExtractedFact is a single structured fact produced by FactExtractorFn.
// Both Content and Vector must be populated; facts with an empty Vector are skipped
// during RetainFromText with a non-fatal error logged in the returned ExtractResult.
type ExtractedFact struct {
	// FactID is a stable identifier scoped to the user (e.g., "preferred_language").
	// If empty, RetainFromText skips this fact.
	FactID string `json:"fact_id"`

	// Content is the human-readable fact string (e.g., "Alice prefers Go over Python").
	Content string `json:"content"`

	// Layer classifies the memory tier. Defaults to LayerWorldFact if zero-valued.
	Layer MemoryLayer `json:"layer"`

	// Vector is the embedding for the fact content.
	// The FactExtractorFn is responsible for computing this (or delegating to an embedding API).
	// A fact without a Vector is skipped by RetainFromText.
	Vector []float32 `json:"vector,omitempty"`

	// Metadata is optional arbitrary key-value data to attach.
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// ---------------------------------------------------------------------------
// FactExtractorFn
// ---------------------------------------------------------------------------

// FactExtractorFn is a caller-provided hook that extracts structured facts from a slice
// of raw conversation messages for a given user.
//
// The implementation is responsible for:
//   - Parsing the messages for factual claims, preferences, and entities.
//   - Computing or obtaining vector embeddings for each extracted fact.
//   - Returning a deterministic FactID per fact (used as the graph node key).
//
// Example wiring (OpenAI structured output):
//
//	mem.SetFactExtractor(func(ctx context.Context, userID string, msgs []*core.Message) ([]memory.ExtractedFact, error) {
//	    prompt := buildExtractionPrompt(msgs)
//	    resp, err := openaiClient.Chat(ctx, prompt)
//	    if err != nil { return nil, err }
//	    facts := parseStructuredOutput(resp)
//	    for i := range facts {
//	        facts[i].Vector, err = embedText(ctx, facts[i].Content)
//	        if err != nil { return nil, err }
//	    }
//	    return facts, nil
//	})
type FactExtractorFn func(ctx context.Context, userID string, messages []*core.Message) ([]ExtractedFact, error)

// ---------------------------------------------------------------------------
// RerankerFn
// ---------------------------------------------------------------------------

// RerankerFn is a caller-provided hook that reranks a list of RecallResults given the
// original query text after TEMPR+RRF fusion.
//
// The implementation receives the full candidate list ordered by RRFScore and must return
// the same items (or a subset) in the desired relevance order. Returning an empty slice is
// treated as a reranker failure and the original RRF order is used as fallback.
//
// Example wiring (cross-encoder via a local HTTP model):
//
//	mem.SetReranker(func(ctx context.Context, query string, candidates []*memory.RecallResult) ([]*memory.RecallResult, error) {
//	    texts := make([]string, len(candidates))
//	    for i, c := range candidates { texts[i] = c.Content }
//	    scores, err := crossEncoderClient.Score(ctx, query, texts)
//	    if err != nil { return nil, err }
//	    sort.Slice(candidates, func(i, j int) bool { return scores[i] > scores[j] })
//	    return candidates, nil
//	})
type RerankerFn func(ctx context.Context, query string, candidates []*RecallResult) ([]*RecallResult, error)

// ---------------------------------------------------------------------------
// ExtractResult – outcome report from RetainFromText
// ---------------------------------------------------------------------------

// ExtractResult reports the outcome of a RetainFromText call.
type ExtractResult struct {
	// Retained is the number of facts successfully extracted and persisted.
	Retained int `json:"retained"`

	// Skipped is the number of facts returned by the extractor but not persisted
	// (e.g., missing FactID or Vector).
	Skipped int `json:"skipped"`

	// Errors collects non-fatal per-fact errors. A non-nil Errors slice does not
	// mean the entire operation failed; some facts may still have been retained.
	Errors []error `json:"errors,omitempty"`
}

// Err returns a combined error if any per-fact errors occurred, or nil otherwise.
func (r *ExtractResult) Err() error {
	if len(r.Errors) == 0 {
		return nil
	}
	msgs := make([]string, len(r.Errors))
	for i, e := range r.Errors {
		msgs[i] = e.Error()
	}
	return errors.New(strings.Join(msgs, "; "))
}

// ---------------------------------------------------------------------------
// ErrNoFactExtractor – sentinel error
// ---------------------------------------------------------------------------

// ErrNoFactExtractor is returned by RetainFromText when no FactExtractorFn has been
// configured on the MemoryManager.
var ErrNoFactExtractor = errors.New("memory: no FactExtractorFn configured; call SetFactExtractor first")

// ErrNoReranker is returned by callers that check whether a reranker is active.
var ErrNoReranker = errors.New("memory: no RerankerFn configured")

// ---------------------------------------------------------------------------
// RetainFromText – auto-extract + persist pipeline
// ---------------------------------------------------------------------------

// RetainFromText feeds raw conversation messages through the configured FactExtractorFn
// and persists each successfully extracted fact via Retain.
//
// It is the high-level entry-point analogous to Hindsight's retain() endpoint, where
// knowledge extraction is handled transparently by the system.
//
// Partial success is possible: facts that cannot be persisted are recorded in
// ExtractResult.Errors while successfully retained facts increment ExtractResult.Retained.
//
// Returns ErrNoFactExtractor if SetFactExtractor has not been called.
func (m *MemoryManager) RetainFromText(ctx context.Context, userID string, messages []*core.Message) (*ExtractResult, error) {
	m.mu.RLock()
	extractor := m.factExtractor
	m.mu.RUnlock()

	if extractor == nil {
		return nil, ErrNoFactExtractor
	}
	if userID == "" {
		return nil, fmt.Errorf("RetainFromText: userID is required")
	}
	if len(messages) == 0 {
		return &ExtractResult{}, nil
	}

	facts, err := extractor(ctx, userID, messages)
	if err != nil {
		return nil, fmt.Errorf("RetainFromText: extractor returned error: %w", err)
	}

	result := &ExtractResult{}

	for i, f := range facts {
		if f.FactID == "" {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Errorf("fact[%d]: empty FactID, skipped", i))
			continue
		}
		if len(f.Vector) == 0 {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Errorf("fact[%d] %q: empty Vector, skipped", i, f.FactID))
			continue
		}

		if err := m.Retain(ctx, RetainInput{
			UserID:   userID,
			FactID:   f.FactID,
			Content:  f.Content,
			Vector:   f.Vector,
			Layer:    f.Layer,
			Metadata: f.Metadata,
		}); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("fact[%d] %q: retain: %w", i, f.FactID, err))
			continue
		}
		result.Retained++
	}

	return result, nil
}
