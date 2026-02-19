// Package hindsight: hooks.go defines two extensibility hooks for injecting
// LLM-backed logic without coupling the package to any specific provider.
//
// Hook 1 – FactExtractorFn
//
//	Plugs into RetainFromText: the caller's hook receives raw conversation
//	messages, extracts structured facts (with pre-computed embeddings) and
//	returns them for persistence via Retain.  Typical implementations: an
//	OpenAI/Anthropic structured-output call, a local Ollama model, or a
//	rule-based extractor used in tests.
//
// Hook 2 – RerankerFn
//
//	Plugs into Recall: applied after TEMPR+RRF fusion to reorder candidates
//	using a cross-encoder or LLM relevance scorer.  Errors silently fall back
//	to the RRF-ranked order so Recall always returns a valid result.
package hindsight

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
)

// ---------------------------------------------------------------------------
// Hook function types
// ---------------------------------------------------------------------------

// FactExtractorFn is a caller-provided hook that extracts structured facts from
// raw conversation messages for a given memory bank.
//
// The implementation is responsible for:
//   - Parsing messages for factual claims, preferences, and entities.
//   - Computing vector embeddings for each extracted fact.
//   - Returning a deterministic ID per fact (used as the memory node key).
//
// Example wiring (OpenAI structured output):
//
//	sys.SetFactExtractor(func(ctx context.Context, bankID string, msgs []*core.Message) ([]ExtractedFact, error) {
//	    resp, err := openaiClient.Chat(ctx, buildPrompt(msgs))
//	    if err != nil { return nil, err }
//	    facts := parseResponse(resp)
//	    for i := range facts {
//	        facts[i].Vector, _ = embedText(ctx, facts[i].Content)
//	    }
//	    return facts, nil
//	})
type FactExtractorFn func(ctx context.Context, bankID string, messages []*core.Message) ([]ExtractedFact, error)

// RerankerFn is a caller-provided hook that reranks a list of RecallResults
// given the original query text after TEMPR+RRF fusion.
//
// The implementation receives the full candidate list ordered by RRF score and
// must return the same items (or a subset) in the desired relevance order.
// Returning an empty slice is treated as a failure and the original RRF order
// is preserved as fallback.
//
// Example wiring (cross-encoder via a local HTTP sidecar):
//
//	sys.SetReranker(func(ctx context.Context, query string, candidates []*RecallResult) ([]*RecallResult, error) {
//	    texts := make([]string, len(candidates))
//	    for i, c := range candidates { texts[i] = c.Content }
//	    scores, err := crossEncoderClient.Score(ctx, query, texts)
//	    if err != nil { return nil, err }
//	    sort.Slice(candidates, func(i, j int) bool { return scores[i] > scores[j] })
//	    return candidates, nil
//	})
type RerankerFn func(ctx context.Context, query string, candidates []*RecallResult) ([]*RecallResult, error)

// ---------------------------------------------------------------------------
// ExtractedFact
// ---------------------------------------------------------------------------

// ExtractedFact is a single structured fact produced by FactExtractorFn.
// Both ID and Vector must be non-empty; facts missing either are skipped by
// RetainFromText and counted in ExtractResult.Skipped.
type ExtractedFact struct {
	// ID is a stable identifier scoped to the bank (e.g., "user_pref_lang").
	ID string `json:"id"`

	// Type categorises the memory epistemically. Defaults to WorldMemory if zero.
	Type MemoryType `json:"type"`

	// Content is the human-readable fact string.
	Content string `json:"content"`

	// Vector is the embedding for Content (caller must compute this).
	Vector []float32 `json:"vector,omitempty"`

	// Entities are related people, places, or concepts.
	Entities []string `json:"entities,omitempty"`

	// Confidence is the belief strength (0–1); meaningful for OpinionMemory.
	Confidence float64 `json:"confidence,omitempty"`

	// Metadata is optional arbitrary key-value data to attach.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// ---------------------------------------------------------------------------
// ExtractResult
// ---------------------------------------------------------------------------

// ExtractResult reports the outcome of a RetainFromText call.
// A non-nil Errors slice does not mean total failure; partial success is
// possible — the Retained counter reflects successful persistence.
type ExtractResult struct {
	// Retained is the count of facts successfully persisted.
	Retained int `json:"retained"`

	// Skipped is the count of facts not persisted (missing ID or Vector).
	Skipped int `json:"skipped"`

	// Errors collects non-fatal per-fact errors.
	Errors []error `json:"errors,omitempty"`
}

// Err returns a combined error if any per-fact errors occurred, or nil.
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
// AutoRetainConfig
// ---------------------------------------------------------------------------

// AutoRetainConfig controls the automatic retain behaviour triggered by AddMessage.
type AutoRetainConfig struct {
	// Enabled turns auto-retain on or off. Defaults to false until explicitly enabled.
	Enabled bool

	// WindowSize is the number of recent messages passed to FactExtractorFn each
	// time a trigger fires. A larger window increases context at the cost of more
	// tokens sent to the extractor. Default: 6.
	WindowSize int

	// TriggerEvery fires extraction after every N messages that match RoleFilter.
	// Set to 1 to extract after every matching message, 2 for every pair, etc.
	// Default: 2 (extract after each user+assistant turn).
	TriggerEvery int

	// RoleFilter restricts which message roles increment the counter.
	// Nil or empty means all roles count.
	// Example: []string{"user"} to trigger only on user messages.
	RoleFilter []string
}

// defaultAutoRetainConfig returns sensible defaults.
func defaultAutoRetainConfig() AutoRetainConfig {
	return AutoRetainConfig{
		Enabled:      false,
		WindowSize:   6,
		TriggerEvery: 2,
		RoleFilter:   nil, // all roles
	}
}

// ---------------------------------------------------------------------------
// Sentinel errors
// ---------------------------------------------------------------------------

// ErrNoFactExtractor is returned by RetainFromText when no FactExtractorFn is
// configured on the System.
var ErrNoFactExtractor = errors.New("hindsight: no FactExtractorFn configured; call SetFactExtractor first")

// ErrNoReranker is returned by callers that programmatically check whether a
// reranker is active.
var ErrNoReranker = errors.New("hindsight: no RerankerFn configured")

// ---------------------------------------------------------------------------
// Hook registration methods
// ---------------------------------------------------------------------------

// SetFactExtractor registers a FactExtractorFn for use by RetainFromText and
// auto-retain (AddMessage). Safe for concurrent use.
func (s *System) SetFactExtractor(fn FactExtractorFn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.factExtractor = fn
}

// SetReranker registers a RerankerFn for use by Recall.
// Safe for concurrent use; replaces any previously registered reranker.
func (s *System) SetReranker(fn RerankerFn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reranker = fn
}

// SetAutoRetain enables automatic fact extraction on AddMessage and configures
// its behaviour. Requires a FactExtractorFn to be registered via SetFactExtractor
// before auto-retain can fire. Calling this with cfg.Enabled = false disables it.
//
// Example:
//
//	sys.SetFactExtractor(myExtractor)
//	sys.SetAutoRetain(&hindsight.AutoRetainConfig{
//	    Enabled:      true,
//	    WindowSize:   6,
//	    TriggerEvery: 2,
//	})
//	// From now on, sys.AddMessage() triggers extraction automatically.
func (s *System) SetAutoRetain(cfg *AutoRetainConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cfg == nil {
		s.autoRetainCfg = defaultAutoRetainConfig()
		s.autoRetainCfg.Enabled = false
		return
	}
	if cfg.WindowSize <= 0 {
		cfg.WindowSize = 6
	}
	if cfg.TriggerEvery <= 0 {
		cfg.TriggerEvery = 2
	}
	s.autoRetainCfg = *cfg
	if cfg.Enabled {
		// reset counter so the first trigger fires after TriggerEvery messages
		s.autoRetainCounter = 0
	}
}

// ---------------------------------------------------------------------------
// RetainFromText
// ---------------------------------------------------------------------------

// RetainFromText feeds raw conversation messages through the configured
// FactExtractorFn and persists each successfully extracted fact via Retain.
// This is the high-level entry-point that pairs with the FactExtractorFn hook:
// callers supply raw messages; knowledge extraction and storage happen here.
//
// Partial success is supported — failed facts are recorded in
// ExtractResult.Errors while successfully retained facts increment
// ExtractResult.Retained.
//
// Returns ErrNoFactExtractor if SetFactExtractor has not been called.
func (s *System) RetainFromText(ctx context.Context, bankID string, messages []*core.Message) (*ExtractResult, error) {
	s.mu.RLock()
	extractor := s.factExtractor
	s.mu.RUnlock()

	if extractor == nil {
		return nil, ErrNoFactExtractor
	}
	if bankID == "" {
		return nil, fmt.Errorf("RetainFromText: bankID is required")
	}
	if len(messages) == 0 {
		return &ExtractResult{}, nil
	}

	facts, err := extractor(ctx, bankID, messages)
	if err != nil {
		return nil, fmt.Errorf("RetainFromText: extractor error: %w", err)
	}

	result := &ExtractResult{}
	for i, f := range facts {
		if f.ID == "" {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Errorf("fact[%d]: empty ID, skipped", i))
			continue
		}
		if len(f.Vector) == 0 {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Errorf("fact[%d] %q: empty Vector, skipped", i, f.ID))
			continue
		}
		if err := s.Retain(ctx, &Memory{
			ID:         f.ID,
			BankID:     bankID,
			Type:       f.Type,
			Content:    f.Content,
			Vector:     f.Vector,
			Entities:   f.Entities,
			Confidence: f.Confidence,
			Metadata:   f.Metadata,
		}); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("fact[%d] %q: retain: %w", i, f.ID, err))
			continue
		}
		result.Retained++
	}
	return result, nil
}
