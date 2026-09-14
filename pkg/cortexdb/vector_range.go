package cortexdb

import (
	"context"
	"errors"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// ErrNonPositiveRadius is returned when a range search is asked for a radius of
// zero or less.
//
// The check lives at the facade rather than being left to the engine because
// the two backends do not agree about it on their own, and a caller that asked
// for a nonsense bar should get the same refusal on either. A zero radius is
// also the shape a forgotten field takes, so answering it with "nothing is
// within zero distance" would turn an unset argument into an empty result set
// that looks like a genuine answer.
var ErrNonPositiveRadius = errors.New("cortexdb: range search radius must be positive")

// defaultVectorRangeToolLimit caps what search_vector_range returns when the
// caller names no cap of its own.
//
// The Go API defaults to no cap, because a program asking for everything above
// a bar can hold everything above that bar. A tool answer travels back into a
// model's context window, so an unbounded default there is a way to lose a
// conversation to one badly chosen radius. The response says when the cap bit,
// so the caller can tell a complete answer from a trimmed one.
const defaultVectorRangeToolLimit = 100

// VectorRangeOptions narrows a range search and optionally caps its result set.
type VectorRangeOptions struct {
	// Radius is a distance in the store's configured metric, not a similarity:
	// a row is kept when its distance from the query is less than or equal to
	// Radius. For the default cosine metric the distance is 1 - similarity, so
	// 0.1 keeps near-identical vectors and 0.5 keeps loosely related ones; for
	// a euclidean store it is the euclidean distance itself. Must be positive.
	Radius float32

	// Collection restricts the search to one collection. Empty searches all.
	Collection string

	// Filter restricts the search to rows whose metadata matches every entry.
	Filter map[string]string

	// MaxResults caps how many of the closest matches are returned. Zero means
	// no cap — every row inside the radius, which is the point of asking a
	// range question rather than a top-K one.
	MaxResults int
}

// RangeSearchVector returns every stored vector within opts.Radius of the query
// vector, closest first.
//
// This is the threshold-shaped counterpart to Search. Top-K always returns K
// rows, however far away the last of them is, so it cannot answer "find all the
// near-duplicates of this" or "is there anything in the store like this at
// all" — for those an arbitrary K either invents neighbours or hides them. A
// range query answers with everything above the bar and nothing below it, and
// an empty result is a real answer rather than a failure.
//
// Scores are similarities on the same scale Search reports, so callers can rank
// range hits alongside top-K hits without converting anything.
func (db *DB) RangeSearchVector(ctx context.Context, vector []float32, opts VectorRangeOptions) ([]core.ScoredEmbedding, error) {
	if len(vector) == 0 {
		return nil, ErrInvalidVector
	}
	if opts.Radius <= 0 {
		return nil, fmt.Errorf("%w, got %v", ErrNonPositiveRadius, opts.Radius)
	}

	results, err := db.store.RangeSearch(ctx, vector, opts.Radius, core.SearchOptions{
		Collection: opts.Collection,
		Filter:     opts.Filter,
		TopK:       opts.MaxResults,
	})
	if err != nil {
		return nil, fmt.Errorf("range search: %w", err)
	}
	return results, nil
}

// RangeSearchText embeds the query text and runs RangeSearchVector on it.
//
// Unlike SearchText there is no lexical fallback when no embedder is
// configured. A radius is a statement about distance in the vector space, and
// BM25 scores do not live in that space; quietly answering a range question
// with lexical hits would return rows that never cleared the bar the caller
// set. Asking without an embedder is a mistake worth reporting.
func (db *DB) RangeSearchText(ctx context.Context, query string, opts VectorRangeOptions) ([]core.ScoredEmbedding, error) {
	if query == "" {
		return nil, ErrEmptyText
	}
	if db.embedder == nil {
		return nil, ErrEmbedderNotConfigured
	}
	// Checked here as well as in RangeSearchVector, and before the embedder
	// rather than after it: embedding is the expensive half of this call — a
	// network round trip against a metered model for most deployments — and a
	// radius the caller got wrong is knowable without it. Refusing after
	// spending would bill somebody for an argument error.
	if opts.Radius <= 0 {
		return nil, fmt.Errorf("%w, got %v", ErrNonPositiveRadius, opts.Radius)
	}

	vector, err := db.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEmbeddingFailed, err)
	}
	return db.RangeSearchVector(ctx, vector, opts)
}

// ToolSearchVectorRangeRequest asks for every stored row within a radius of the
// query text.
type ToolSearchVectorRangeRequest struct {
	Query      string            `json:"query"`
	Radius     float64           `json:"radius"`
	Collection string            `json:"collection,omitempty"`
	Filter     map[string]string `json:"filter,omitempty"`
	MaxResults int               `json:"max_results,omitempty"`
}

// ToolSearchVectorRangeResponse returns the rows inside the radius.
type ToolSearchVectorRangeResponse struct {
	Matches []ToolChunk `json:"matches"`
	Count   int         `json:"count"`
	// Truncated reports that the radius matched more rows than max_results
	// allowed through. It is the one thing a range answer cannot leave out: a
	// caller deciding from "everything above the bar" needs to know when it is
	// not looking at everything, and a trimmed list is indistinguishable from a
	// complete one otherwise.
	Truncated bool `json:"truncated"`
}

// SearchVectorRange runs a radius query over stored vectors for a tool caller.
func (t *GraphRAGToolbox) SearchVectorRange(ctx context.Context, req ToolSearchVectorRangeRequest) (*ToolSearchVectorRangeResponse, error) {
	limit := req.MaxResults
	if limit <= 0 {
		limit = defaultVectorRangeToolLimit
	}

	// Ask for one row past the cap so the answer can say whether the radius
	// reached further than the cap allowed. The alternative — fetching
	// everything and counting — pays for rows nobody will read.
	results, err := t.db.RangeSearchText(ctx, req.Query, VectorRangeOptions{
		Radius:     float32(req.Radius),
		Collection: req.Collection,
		Filter:     req.Filter,
		MaxResults: limit + 1,
	})
	if err != nil {
		return nil, err
	}

	truncated := len(results) > limit
	if truncated {
		results = results[:limit]
	}

	matches := make([]ToolChunk, 0, len(results))
	for _, result := range results {
		matches = append(matches, ToolChunk{
			ID:         result.ID,
			DocumentID: result.DocID,
			Content:    result.Content,
			Score:      result.Score,
			Metadata:   result.Metadata,
		})
	}
	return &ToolSearchVectorRangeResponse{
		Matches:   matches,
		Count:     len(matches),
		Truncated: truncated,
	}, nil
}
