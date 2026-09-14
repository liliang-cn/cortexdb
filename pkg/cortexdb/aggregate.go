package cortexdb

import (
	"context"

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
	return db.store.Aggregate(ctx, req)
}

// SearchWithFacets filters a vector search by metadata facets and, when asked,
// returns the counts for each facet value beside the hits.
//
// There is deliberately no tool for this. The facet filter language is nested
// and typed, which is a lot of schema for a model to get right, and the
// question a model actually asks of facets — what values does this field take,
// and how many of each — is already answered by aggregate_metadata with a
// group_by. Programs get the whole thing; agents get the part they can use.
func (db *DB) SearchWithFacets(ctx context.Context, query []float32, opts core.FacetedSearchOptions) ([]core.ScoredEmbedding, []core.FacetResult, error) {
	return db.store.SearchWithFacets(ctx, query, opts)
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
	return db.store.VectorAggregate(ctx, req)
}

// ToolAggregateMetadataRequest asks a question about a metadata field across
// the whole store rather than about one query's neighbourhood.
type ToolAggregateMetadataRequest struct {
	Type       string            `json:"type"`
	Field      string            `json:"field,omitempty"`
	GroupBy    []string          `json:"group_by,omitempty"`
	Filters    map[string]string `json:"filters,omitempty"`
	Collection string            `json:"collection,omitempty"`
	OrderBy    string            `json:"order_by,omitempty"`
	Limit      int               `json:"limit,omitempty"`
}

// ToolAggregateMetadataGroup is one row of the answer.
type ToolAggregateMetadataGroup struct {
	Keys  map[string]any `json:"keys,omitempty"`
	Value any            `json:"value,omitempty"`
	Count int            `json:"count"`
}

// ToolAggregateMetadataResponse carries the rows and how many there were.
type ToolAggregateMetadataResponse struct {
	Results []ToolAggregateMetadataGroup `json:"results"`
	Total   int                          `json:"total"`
}

// AggregateMetadata answers a counting question for a tool caller.
func (t *GraphRAGToolbox) AggregateMetadata(ctx context.Context, req ToolAggregateMetadataRequest) (*ToolAggregateMetadataResponse, error) {
	// The tool takes string values because that is what a metadata equality
	// filter compares against in storage; core's map is `any` for callers that
	// already hold typed values.
	filters := make(map[string]any, len(req.Filters))
	for k, v := range req.Filters {
		filters[k] = v
	}

	res, err := t.db.Aggregate(ctx, core.AggregationRequest{
		Type:       core.AggregationType(req.Type),
		Field:      req.Field,
		GroupBy:    req.GroupBy,
		Filters:    filters,
		Collection: req.Collection,
		OrderBy:    req.OrderBy,
		Limit:      req.Limit,
	})
	if err != nil {
		return nil, err
	}

	out := make([]ToolAggregateMetadataGroup, 0, len(res.Results))
	for _, r := range res.Results {
		out = append(out, ToolAggregateMetadataGroup{Keys: r.GroupKeys, Value: r.Value, Count: r.Count})
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
