package cortexdb

import (
	"context"
	"fmt"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// Aggregating what is in the store, rather than retrieving from it.
//
// Everything else on this facade answers "what is relevant to this query". The
// three methods here answer questions about the collection as a whole: how many
// records carry each value of a metadata field, what the numbers in that field
// add up to, and — the one only a vector store can answer — which single
// record best represents a group of them.
//
// All three were already implemented in pkg/core and none of them were
// reachable from here, which by this repository's own rule meant they were not
// reachable at all.

// Aggregate counts, sums, averages, or groups over a metadata field.
//
// The request type is core's own because it is already the right shape and
// already carries the JSON names the tool surface uses; wrapping it here would
// only give a caller two spellings of the same thing to choose between.
//
// Metadata names in the request are checked before they reach SQL — a name is a
// column reference, not a value, and cannot be bound as a parameter. Anything
// outside a conservative identifier shape is refused rather than escaped. See
// validateAggregationRequest.
func (db *DB) Aggregate(ctx context.Context, req core.AggregationRequest) (*core.AggregationResponse, error) {
	want, statusFiltered := req.Filters[reviewStatusKey]
	if statusFiltered {
		// The status is read off the document, not the row, so it is a
		// condition of its own and takes a status, not an operator object.
		if _, isOperators := want.(map[string]interface{}); isOperators {
			return nil, fmt.Errorf("a filter on %s takes a plain status value, not an operator object", reviewStatusKey)
		}
		req.Filters = withoutKey(req.Filters, reviewStatusKey)
		req.OnlyDocuments, req.OnlyDocumentsArgs = db.reviewStatusIs(want)
	}
	req.ExcludeDocuments, req.ExcludeDocumentsArgs = db.hiddenDocumentsWhere(ctx, statusFiltered)
	return db.store.Aggregate(ctx, req)
}

// reviewStatusIs is the condition on the documents table that a filter on
// _review_status becomes: the decision lives on the record alone, so the
// filter reads the record, and a decision made by knowledge_update counts
// at once. accepted takes a document with no status too, as in a plan's
// filters. A row that belongs to no document has no status and is left out.
func (db *DB) reviewStatusIs(want any) (string, []interface{}) {
	expr := db.Dialect().JSONTextGuarded("metadata", reviewStatusKey)
	if fmt.Sprint(want) == ReviewAccepted {
		return "(" + expr + " IS NULL OR " + expr + " = '' OR " + expr + " = ?)", []interface{}{ReviewAccepted}
	}
	return expr + " = ?", []interface{}{fmt.Sprint(want)}
}

// withoutKey is a copy of m without key; the caller's map is left as it is.
func withoutKey[V any](m map[string]V, key string) map[string]V {
	out := make(map[string]V, len(m))
	for k, v := range m {
		if k != key {
			out[k] = v
		}
	}
	return out
}

// hiddenDocumentsWhere is the condition on the documents table that selects
// every document the gate hides, for the aggregates, whose rows the store
// counts and compares without loading them through a chunk path. It is one
// condition, not a list of ids, so the number of hidden documents never
// reaches the bound-parameter limit of either store. When the caller filters
// on the review status itself, the review gate hides nothing and the filter
// decides: that is how a curator lists what waits. Expired documents stay
// out either way.
func (db *DB) hiddenDocumentsWhere(ctx context.Context, statusFiltered bool) (string, []interface{}) {
	where := db.expiredPredicate()
	if review := db.reviewHiddenPredicateFor(ctx, ""); review != "" && !statusFiltered {
		where = `(` + where + ` OR ` + review + `)`
	}
	return where, []interface{}{instant(time.Now())}
}

// SearchWithFacets filters a vector search by metadata facets and, when asked,
// returns the counts for each facet value beside the hits.
//
// There is deliberately no tool for this. The facet filter language is nested
// and typed, which is a lot of schema for a model to get right, and the
// question a model actually asks of facets — what values does this field take,
// and how many of each — is already answered by aggregate_metadata with a
// group_by. Programs get the whole thing; agents get the part they can use.
//
// The hits pass the document gate like every search; the facet counts are the
// store's and count every row the facets match.
func (db *DB) SearchWithFacets(ctx context.Context, query []float32, opts core.FacetedSearchOptions) ([]core.ScoredEmbedding, []core.FacetResult, error) {
	var facets []core.FacetResult
	hits, err := db.gatedSearch(ctx, opts.TopK, func(k int) ([]core.ScoredEmbedding, error) {
		inner := opts
		inner.TopK = k
		hits, counted, err := db.store.SearchWithFacets(ctx, query, inner)
		facets = counted
		return hits, err
	})
	if err != nil {
		return nil, nil, err
	}
	return hits, facets, nil
}

// VectorAggregate reduces the vectors themselves: the centroid, the geometric
// median, or the medoid.
//
// The first two are points that need not correspond to anything stored. The
// medoid does — it is the stored record closest to all the others in its group,
// which is what makes it the one worth asking for by name. It answers "which of
// these near-duplicates is the canonical one" and "give me the one record that
// represents this cluster" with arithmetic and no model in the loop.
func (db *DB) VectorAggregate(ctx context.Context, req core.VectorAggregateRequest) (*core.VectorAggregateResponse, error) {
	want, statusFiltered := req.Filter[reviewStatusKey]
	if statusFiltered {
		req.Filter = withoutKey(req.Filter, reviewStatusKey)
		req.OnlyDocuments, req.OnlyDocumentsArgs = db.reviewStatusIs(want)
	}
	req.ExcludeDocuments, req.ExcludeDocumentsArgs = db.hiddenDocumentsWhere(ctx, statusFiltered)
	return db.store.VectorAggregate(ctx, req)
}

// ToolAggregateMetadataRequest asks a question about a metadata field across
// the whole store rather than about one query's neighbourhood.
type ToolAggregateMetadataRequest struct {
	// Type is the single aggregation; not needed when Measures is set.
	Type    string   `json:"type,omitempty"`
	Field   string   `json:"field,omitempty"`
	GroupBy []string `json:"group_by,omitempty"`
	// Filters maps a metadata field to a value it must equal, or to an
	// operator object such as {"gte": 0.5, "lt": 1}; see
	// core.AggregationRequest and aggregate_measures.go for the operators.
	Filters    map[string]any `json:"filters,omitempty"`
	Collection string         `json:"collection,omitempty"`
	OrderBy    string         `json:"order_by,omitempty"`
	Order      string         `json:"order,omitempty"`
	Limit      int            `json:"limit,omitempty"`
	// Measures are computed together per group, each reported under its
	// alias in the group's Values.
	Measures []core.AggregationMeasure `json:"measures,omitempty"`
	// TimeBucket groups by a timestamp truncated to day, week, month or year.
	TimeBucket *core.AggregationTimeBucket `json:"time_bucket,omitempty"`
	// CreatedSince (inclusive) and CreatedBefore (exclusive) bound the row's
	// own created-at time.
	CreatedSince  string `json:"created_since,omitempty"`
	CreatedBefore string `json:"created_before,omitempty"`
	// IncludeProposed counts the proposed documents too under the review
	// gate; rejected ones stay out.
	IncludeProposed bool `json:"include_proposed,omitempty"`
}

// ToolAggregateMetadataGroup is one row of the answer.
type ToolAggregateMetadataGroup struct {
	Keys  map[string]any `json:"keys,omitempty"`
	Value any            `json:"value,omitempty"`
	Count int            `json:"count"`
	// Values holds every measure by alias when the request asked for
	// measures, a time bucket, a created-at bound or an operator filter. A
	// measure over no numeric value is null.
	Values map[string]any `json:"values,omitempty"`
	// Skipped is, per measure alias, how many of the group's rows carried the
	// field with a value that is not a number and were left out of it.
	Skipped map[string]int `json:"skipped,omitempty"`
}

// ToolAggregateMetadataResponse carries the rows and how many there were.
type ToolAggregateMetadataResponse struct {
	Results []ToolAggregateMetadataGroup `json:"results"`
	Total   int                          `json:"total"`
}

// AggregateMetadata answers a counting question for a tool caller.
func (t *GraphRAGToolbox) AggregateMetadata(ctx context.Context, req ToolAggregateMetadataRequest) (*ToolAggregateMetadataResponse, error) {
	ctx = includeProposed(ctx, req.IncludeProposed)
	// A string is an equality filter, compared as text because that is what
	// metadata is in storage, and an object is an operator object. A bare
	// number or boolean is the eq operator, so that it compares by value: as
	// a plain equality it would be compared against the stored text and
	// match nothing.
	filters := make(map[string]any, len(req.Filters))
	for k, v := range req.Filters {
		switch v.(type) {
		case string, map[string]any:
			filters[k] = v
		case float64, int, int64, bool:
			if k == reviewStatusKey {
				filters[k] = v
				continue
			}
			filters[k] = map[string]any{"eq": v}
		default:
			return nil, fmt.Errorf("a filter value must be a string, a number, a boolean or an operator object such as {\"gte\": 1}")
		}
	}

	res, err := t.db.Aggregate(ctx, core.AggregationRequest{
		Type:          core.AggregationType(req.Type),
		Field:         req.Field,
		GroupBy:       req.GroupBy,
		Filters:       filters,
		Collection:    req.Collection,
		OrderBy:       req.OrderBy,
		Order:         req.Order,
		Limit:         req.Limit,
		Measures:      req.Measures,
		TimeBucket:    req.TimeBucket,
		CreatedSince:  req.CreatedSince,
		CreatedBefore: req.CreatedBefore,
	})
	if err != nil {
		return nil, err
	}

	out := make([]ToolAggregateMetadataGroup, 0, len(res.Results))
	for _, r := range res.Results {
		out = append(out, ToolAggregateMetadataGroup{Keys: r.GroupKeys, Value: r.Value, Count: r.Count, Values: r.Values, Skipped: r.Skipped})
	}
	return &ToolAggregateMetadataResponse{Results: out, Total: res.Total}, nil
}

// ToolRepresentativeRecordsRequest asks which stored record best represents
// each group.
type ToolRepresentativeRecordsRequest struct {
	GroupBy     string            `json:"group_by,omitempty"`
	Filter      map[string]string `json:"filter,omitempty"`
	Collection  string            `json:"collection,omitempty"`
	MaxPerGroup int               `json:"max_per_group,omitempty"`
}

// ToolRepresentativeRecord is one group's representative.
type ToolRepresentativeRecord struct {
	Group    string  `json:"group"`
	Count    int     `json:"count"`
	MemberID string  `json:"member_id"`
	Score    float64 `json:"score"`
	Content  string  `json:"content,omitempty"`
}

// ToolRepresentativeRecordsResponse lists one representative per group.
type ToolRepresentativeRecordsResponse struct {
	Groups []ToolRepresentativeRecord `json:"groups"`
	Count  int                        `json:"count"`
}

// RepresentativeRecords returns the medoid of each group, with its text.
//
// Only the medoid. The centroid and the geometric median are on the Go API and
// deliberately absent here: both are synthetic points, and the only way to hand
// one to a model is as several hundred floats, which costs a great deal of
// context and tells it nothing it can act on. A medoid is a record that exists,
// so it can be read, quoted and followed back to its source.
func (t *GraphRAGToolbox) RepresentativeRecords(ctx context.Context, req ToolRepresentativeRecordsRequest) (*ToolRepresentativeRecordsResponse, error) {
	res, err := t.db.VectorAggregate(ctx, core.VectorAggregateRequest{
		Kind:        core.VectorMedoid,
		Collection:  req.Collection,
		Filter:      req.Filter,
		GroupBy:     req.GroupBy,
		MaxPerGroup: req.MaxPerGroup,
	})
	if err != nil {
		return nil, err
	}

	groups := make([]ToolRepresentativeRecord, 0, len(res.Groups))
	for _, g := range res.Groups {
		record := ToolRepresentativeRecord{
			Group:    g.Group,
			Count:    g.Count,
			MemberID: g.MemberID,
			Score:    g.Score,
			// Already in hand: VectorAggregate read the row to pick it.
			Content: g.MemberContent,
		}
		groups = append(groups, record)
	}
	return &ToolRepresentativeRecordsResponse{Groups: groups, Count: len(groups)}, nil
}
