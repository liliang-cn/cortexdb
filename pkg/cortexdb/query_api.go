package cortexdb

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

const (
	QueryPrefetchVector  = "vector"
	QueryPrefetchLexical = "lexical"
	QueryPrefetchHybrid  = "hybrid"
	QueryPrefetchGraph   = "graph"

	QueryFusionRRF         = "rrf"
	QueryFusionWeightedRRF = "weighted_rrf"
	QueryFusionDBSF        = "dbsf"

	QueryFilterEqual    = "eq"
	QueryFilterNotEqual = "neq"
	QueryFilterIn       = "in"
	QueryFilterContains = "contains"
	QueryFilterGTE      = "gte"
	QueryFilterLTE      = "lte"
)

// QueryRequest is CortexDB's composable retrieval API. It runs one or more
// prefetch queries, fuses their candidate sets, then applies optional formula
// scoring for application-specific ranking signals.
type QueryRequest struct {
	Collection  string             `json:"collection,omitempty"`
	Query       string             `json:"query,omitempty"`
	QueryVector []float32          `json:"query_vector,omitempty"`
	EntityNames []string           `json:"entity_names,omitempty"`
	Prefetch    []QueryPrefetch    `json:"prefetch,omitempty"`
	Fusion      string             `json:"fusion,omitempty"`
	Filter      *QueryFilter       `json:"filter,omitempty"`
	Formula     *QueryScoreFormula `json:"formula,omitempty"`
	Limit       int                `json:"limit,omitempty"`
	RRFK        float64            `json:"rrf_k,omitempty"`
	IncludeRaw  bool               `json:"include_raw,omitempty"`
}

// QueryPrefetch describes one retrieval lane in a multi-stage query.
type QueryPrefetch struct {
	Name             string    `json:"name,omitempty"`
	Kind             string    `json:"kind,omitempty"`
	Query            string    `json:"query,omitempty"`
	QueryVector      []float32 `json:"query_vector,omitempty"`
	EntityNames      []string  `json:"entity_names,omitempty"`
	Weight           float64   `json:"weight,omitempty"`
	Limit            int       `json:"limit,omitempty"`
	MaxHops          int       `json:"max_hops,omitempty"`
	Keywords         []string  `json:"keywords,omitempty"`
	AlternateQueries []string  `json:"alternate_queries,omitempty"`
}

// QueryFilter is a small payload filter model for embeddings metadata plus a
// few top-level fields such as id, doc_id, collection, and content.
type QueryFilter struct {
	Must    []QueryCondition `json:"must,omitempty"`
	Should  []QueryCondition `json:"should,omitempty"`
	MustNot []QueryCondition `json:"must_not,omitempty"`
}

// QueryCondition checks one payload or top-level field.
type QueryCondition struct {
	Field     string   `json:"field"`
	Op        string   `json:"op,omitempty"`
	Value     string   `json:"value,omitempty"`
	Values    []string `json:"values,omitempty"`
	Number    float64  `json:"number,omitempty"`
	HasNumber bool     `json:"has_number,omitempty"`
}

// QueryScoreFormula layers deterministic ranking logic on top of fused scores.
type QueryScoreFormula struct {
	BaseWeight    float64             `json:"base_weight,omitempty"`
	FieldBoosts   []QueryFieldBoost   `json:"field_boosts,omitempty"`
	NumericBoosts []QueryNumericBoost `json:"numeric_boosts,omitempty"`
}

// QueryFieldBoost adds Weight when Field matches Equals or contains Contains.
type QueryFieldBoost struct {
	Field    string  `json:"field"`
	Equals   string  `json:"equals,omitempty"`
	Contains string  `json:"contains,omitempty"`
	Weight   float64 `json:"weight"`
}

// QueryNumericBoost adds a normalized numeric payload contribution.
type QueryNumericBoost struct {
	Field    string  `json:"field"`
	Weight   float64 `json:"weight"`
	MaxValue float64 `json:"max_value,omitempty"`
}

// QueryResult is one ranked result from Query.
type QueryResult struct {
	ID           string             `json:"id"`
	Collection   string             `json:"collection,omitempty"`
	Content      string             `json:"content,omitempty"`
	DocID        string             `json:"doc_id,omitempty"`
	Metadata     map[string]string  `json:"metadata,omitempty"`
	Score        float64            `json:"score"`
	BaseScore    float64            `json:"base_score,omitempty"`
	SourceRanks  map[string]int     `json:"source_ranks,omitempty"`
	SourceScores map[string]float64 `json:"source_scores,omitempty"`
}

// QueryResponse returns fused and reranked retrieval results.
type QueryResponse struct {
	Query      string        `json:"query,omitempty"`
	Fusion     string        `json:"fusion"`
	Results    []QueryResult `json:"results"`
	Prefetches []string      `json:"prefetches,omitempty"`
}

type queryCandidate struct {
	embedding    core.ScoredEmbedding
	sourceRanks  map[string]int
	sourceScores map[string]float64
	score        float64
}

type queryPrefetchResult struct {
	name    string
	weight  float64
	results []core.ScoredEmbedding
}

// Query runs a composable retrieval request over CortexDB embeddings.
func (db *DB) Query(ctx context.Context, req QueryRequest) (*QueryResponse, error) {
	if req.Limit <= 0 {
		req.Limit = 10
	}
	if strings.TrimSpace(req.Fusion) == "" {
		req.Fusion = QueryFusionRRF
	}
	if req.RRFK <= 0 {
		req.RRFK = 60
	}

	prefetches := normalizeQueryPrefetches(req)
	if len(prefetches) == 0 {
		return nil, errors.New("query requires at least one prefetch or query")
	}

	lists := make([]queryPrefetchResult, 0, len(prefetches))
	for i, prefetch := range prefetches {
		results, err := db.runQueryPrefetch(ctx, req, prefetch)
		if err != nil {
			return nil, err
		}
		results = filterQueryScoredEmbeddings(results, req.Filter)
		if len(results) == 0 {
			continue
		}
		name := strings.TrimSpace(prefetch.Name)
		if name == "" {
			name = fmt.Sprintf("%s_%d", strings.TrimSpace(prefetch.Kind), i+1)
		}
		weight := prefetch.Weight
		if weight <= 0 {
			weight = 1
		}
		lists = append(lists, queryPrefetchResult{name: name, weight: weight, results: results})
	}

	candidates := fuseQueryResults(lists, req.Fusion, req.RRFK)
	results := rankQueryCandidates(candidates, req.Formula, req.Limit, req.IncludeRaw)

	names := make([]string, 0, len(lists))
	for _, list := range lists {
		names = append(names, list.name)
	}
	return &QueryResponse{
		Query:      req.Query,
		Fusion:     req.Fusion,
		Results:    results,
		Prefetches: names,
	}, nil
}

func normalizeQueryPrefetches(req QueryRequest) []QueryPrefetch {
	if len(req.Prefetch) > 0 {
		out := make([]QueryPrefetch, len(req.Prefetch))
		copy(out, req.Prefetch)
		return out
	}
	if len(req.QueryVector) > 0 && strings.TrimSpace(req.Query) != "" {
		prefetches := []QueryPrefetch{
			{Name: "vector", Kind: QueryPrefetchVector, QueryVector: req.QueryVector},
			{Name: "lexical", Kind: QueryPrefetchLexical, Query: req.Query},
		}
		if len(req.EntityNames) > 0 {
			prefetches = append(prefetches, QueryPrefetch{Name: "graph", Kind: QueryPrefetchGraph, EntityNames: req.EntityNames})
		}
		return prefetches
	}
	if len(req.QueryVector) > 0 {
		prefetches := []QueryPrefetch{{Name: "vector", Kind: QueryPrefetchVector, QueryVector: req.QueryVector}}
		if len(req.EntityNames) > 0 {
			prefetches = append(prefetches, QueryPrefetch{Name: "graph", Kind: QueryPrefetchGraph, EntityNames: req.EntityNames})
		}
		return prefetches
	}
	if strings.TrimSpace(req.Query) == "" {
		if len(req.EntityNames) > 0 {
			return []QueryPrefetch{{Name: "graph", Kind: QueryPrefetchGraph, EntityNames: req.EntityNames}}
		}
		return nil
	}
	if req.Query != "" {
		prefetches := []QueryPrefetch{{Name: "hybrid", Kind: QueryPrefetchHybrid, Query: req.Query}}
		if len(req.EntityNames) > 0 {
			prefetches = append(prefetches, QueryPrefetch{Name: "graph", Kind: QueryPrefetchGraph, EntityNames: req.EntityNames})
		}
		return prefetches
	}
	return nil
}

func (db *DB) runQueryPrefetch(ctx context.Context, req QueryRequest, prefetch QueryPrefetch) ([]core.ScoredEmbedding, error) {
	kind := strings.TrimSpace(prefetch.Kind)
	if kind == "" {
		kind = QueryPrefetchHybrid
	}
	limit := prefetch.Limit
	if limit <= 0 {
		limit = req.Limit * 4
	}
	if req.Filter != nil && limit < req.Limit*8 {
		limit = req.Limit * 8
	}

	query := firstNonEmpty(prefetch.Query, req.Query)
	vector := prefetch.QueryVector
	if len(vector) == 0 {
		vector = req.QueryVector
	}

	switch kind {
	case QueryPrefetchVector:
		if len(vector) == 0 {
			if db.embedder == nil {
				return nil, ErrEmbedderNotConfigured
			}
			if strings.TrimSpace(query) == "" {
				return nil, ErrEmptyText
			}
			embedded, err := db.embedder.Embed(ctx, query)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrEmbeddingFailed, err)
			}
			vector = embedded
		}
		return db.store.Search(ctx, vector, core.SearchOptions{
			Collection: req.Collection,
			TopK:       limit,
			QueryText:  query,
		})
	case QueryPrefetchLexical:
		if strings.TrimSpace(query) == "" {
			return nil, ErrEmptyText
		}
		return db.SearchTextOnly(ctx, query, TextSearchOptions{
			Collection:       req.Collection,
			TopK:             limit,
			Keywords:         prefetch.Keywords,
			AlternateQueries: prefetch.AlternateQueries,
		})
	case QueryPrefetchHybrid:
		if strings.TrimSpace(query) == "" {
			return nil, ErrEmptyText
		}
		if len(vector) == 0 && db.embedder != nil {
			embedded, err := db.embedder.Embed(ctx, query)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrEmbeddingFailed, err)
			}
			vector = embedded
		}
		return db.store.HybridSearch(ctx, vector, query, core.HybridSearchOptions{
			SearchOptions: core.SearchOptions{
				Collection: req.Collection,
				TopK:       limit,
				QueryText:  query,
			},
		})
	case QueryPrefetchGraph:
		entityNames := prefetch.EntityNames
		if len(entityNames) == 0 {
			entityNames = req.EntityNames
		}
		return db.runGraphQueryPrefetch(ctx, entityNames, limit, prefetch.MaxHops)
	default:
		return nil, fmt.Errorf("unsupported query prefetch kind %q", kind)
	}
}

func (db *DB) runGraphQueryPrefetch(ctx context.Context, entityNames []string, limit int, maxHops int) ([]core.ScoredEmbedding, error) {
	if len(entityNames) == 0 {
		return nil, nil
	}
	resp, err := db.GraphRAGTools().SearchChunksByEntities(ctx, ToolSearchChunksByEntitiesRequest{
		EntityNames: entityNames,
		TopK:        limit,
		MaxHops:     maxHops,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil || len(resp.Chunks) == 0 {
		return nil, nil
	}
	results := make([]core.ScoredEmbedding, 0, len(resp.Chunks))
	for _, chunk := range resp.Chunks {
		results = append(results, core.ScoredEmbedding{
			Embedding: core.Embedding{
				ID:       chunk.ID,
				Content:  chunk.Content,
				DocID:    chunk.DocumentID,
				Metadata: chunk.Metadata,
			},
			Score: chunk.Score,
		})
	}
	return results, nil
}

func fuseQueryResults(lists []queryPrefetchResult, method string, rrfK float64) map[string]*queryCandidate {
	switch method {
	case QueryFusionDBSF:
		return fuseQueryResultsDBSF(lists)
	case QueryFusionWeightedRRF, QueryFusionRRF, "":
		return fuseQueryResultsRRF(lists, rrfK, method == QueryFusionRRF)
	default:
		return fuseQueryResultsRRF(lists, rrfK, false)
	}
}

func fuseQueryResultsRRF(lists []queryPrefetchResult, rrfK float64, ignoreWeights bool) map[string]*queryCandidate {
	out := make(map[string]*queryCandidate)
	for _, list := range lists {
		weight := list.weight
		if ignoreWeights {
			weight = 1
		}
		for rank, result := range list.results {
			candidate := ensureQueryCandidate(out, result)
			sourceRank := rank + 1
			score := weight / (rrfK + float64(sourceRank))
			candidate.score += score
			candidate.sourceRanks[list.name] = sourceRank
			candidate.sourceScores[list.name] = result.Score
		}
	}
	return out
}

func fuseQueryResultsDBSF(lists []queryPrefetchResult) map[string]*queryCandidate {
	out := make(map[string]*queryCandidate)
	for _, list := range lists {
		stats := scoreDistribution(list.results)
		for rank, result := range list.results {
			candidate := ensureQueryCandidate(out, result)
			normalized := 0.5
			if stats.stddev > 0 {
				z := (result.Score - stats.mean) / stats.stddev
				if z < -3 {
					z = -3
				}
				if z > 3 {
					z = 3
				}
				normalized = (z + 3) / 6
			}
			candidate.score += normalized * list.weight
			candidate.sourceRanks[list.name] = rank + 1
			candidate.sourceScores[list.name] = result.Score
		}
	}
	return out
}

type queryScoreStats struct {
	mean   float64
	stddev float64
}

func scoreDistribution(results []core.ScoredEmbedding) queryScoreStats {
	if len(results) == 0 {
		return queryScoreStats{}
	}
	var sum float64
	for _, result := range results {
		sum += result.Score
	}
	mean := sum / float64(len(results))
	var variance float64
	for _, result := range results {
		delta := result.Score - mean
		variance += delta * delta
	}
	return queryScoreStats{
		mean:   mean,
		stddev: math.Sqrt(variance / float64(len(results))),
	}
}

func ensureQueryCandidate(candidates map[string]*queryCandidate, result core.ScoredEmbedding) *queryCandidate {
	candidate, ok := candidates[result.ID]
	if ok {
		if candidate.embedding.Content == "" && result.Content != "" {
			candidate.embedding = result
		}
		return candidate
	}
	candidate = &queryCandidate{
		embedding:    result,
		sourceRanks:  make(map[string]int),
		sourceScores: make(map[string]float64),
	}
	candidates[result.ID] = candidate
	return candidate
}

func rankQueryCandidates(candidates map[string]*queryCandidate, formula *QueryScoreFormula, limit int, includeRaw bool) []QueryResult {
	results := make([]QueryResult, 0, len(candidates))
	for _, candidate := range candidates {
		baseScore := candidate.score
		finalScore := applyQueryFormula(baseScore, candidate.embedding, formula)
		result := QueryResult{
			ID:         candidate.embedding.ID,
			Collection: candidate.embedding.Collection,
			Content:    candidate.embedding.Content,
			DocID:      candidate.embedding.DocID,
			Metadata:   candidate.embedding.Metadata,
			Score:      finalScore,
		}
		if includeRaw {
			result.BaseScore = baseScore
			result.SourceRanks = candidate.sourceRanks
			result.SourceScores = candidate.sourceScores
		}
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].ID < results[j].ID
		}
		return results[i].Score > results[j].Score
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results
}

func applyQueryFormula(baseScore float64, emb core.ScoredEmbedding, formula *QueryScoreFormula) float64 {
	if formula == nil {
		return baseScore
	}
	baseWeight := formula.BaseWeight
	if baseWeight == 0 {
		baseWeight = 1
	}
	score := baseScore * baseWeight
	for _, boost := range formula.FieldBoosts {
		value, ok := queryFieldValue(emb.Embedding, boost.Field)
		if !ok {
			continue
		}
		if boost.Equals != "" && value == boost.Equals {
			score += boost.Weight
		}
		if boost.Contains != "" && strings.Contains(strings.ToLower(value), strings.ToLower(boost.Contains)) {
			score += boost.Weight
		}
	}
	for _, boost := range formula.NumericBoosts {
		value, ok := queryFieldValue(emb.Embedding, boost.Field)
		if !ok {
			continue
		}
		number, err := strconv.ParseFloat(value, 64)
		if err != nil {
			continue
		}
		if boost.MaxValue > 0 {
			number = number / boost.MaxValue
			if number > 1 {
				number = 1
			}
		}
		score += number * boost.Weight
	}
	return score
}

func filterQueryScoredEmbeddings(results []core.ScoredEmbedding, filter *QueryFilter) []core.ScoredEmbedding {
	if filter == nil {
		return results
	}
	filtered := make([]core.ScoredEmbedding, 0, len(results))
	for _, result := range results {
		if queryFilterMatches(result.Embedding, filter) {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

func queryFilterMatches(emb core.Embedding, filter *QueryFilter) bool {
	for _, condition := range filter.Must {
		if !queryConditionMatches(emb, condition) {
			return false
		}
	}
	for _, condition := range filter.MustNot {
		if queryConditionMatches(emb, condition) {
			return false
		}
	}
	if len(filter.Should) == 0 {
		return true
	}
	for _, condition := range filter.Should {
		if queryConditionMatches(emb, condition) {
			return true
		}
	}
	return false
}

func queryConditionMatches(emb core.Embedding, condition QueryCondition) bool {
	value, ok := queryFieldValue(emb, condition.Field)
	if !ok {
		return false
	}
	op := condition.Op
	if op == "" {
		op = QueryFilterEqual
	}
	switch op {
	case QueryFilterEqual:
		return value == condition.Value
	case QueryFilterNotEqual:
		return value != condition.Value
	case QueryFilterIn:
		for _, candidate := range condition.Values {
			if value == candidate {
				return true
			}
		}
		return false
	case QueryFilterContains:
		return strings.Contains(strings.ToLower(value), strings.ToLower(condition.Value))
	case QueryFilterGTE, QueryFilterLTE:
		actual, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return false
		}
		expected := condition.Number
		if !condition.HasNumber {
			expected, err = strconv.ParseFloat(condition.Value, 64)
			if err != nil {
				return false
			}
		}
		if op == QueryFilterGTE {
			return actual >= expected
		}
		return actual <= expected
	default:
		return false
	}
}

func queryFieldValue(emb core.Embedding, field string) (string, bool) {
	switch strings.TrimSpace(field) {
	case "id":
		return emb.ID, emb.ID != ""
	case "doc_id", "docId":
		return emb.DocID, emb.DocID != ""
	case "collection":
		return emb.Collection, emb.Collection != ""
	case "content":
		return emb.Content, emb.Content != ""
	default:
		if emb.Metadata == nil {
			return "", false
		}
		value, ok := emb.Metadata[field]
		return value, ok
	}
}
