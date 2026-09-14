package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// openAggregateBrain builds a store whose vectors are laid out on purpose: two
// tight clusters under two kinds, and one far outlier inside the first. That
// shape is what separates the three aggregations from each other — a centroid
// is dragged toward the outlier, a medoid is not, and a plain count cannot see
// either.
func openAggregateBrain(t *testing.T) *DB {
	t.Helper()
	path := fmt.Sprintf("test_aggregate_%d.db", testname.Nano())
	config := DefaultConfig(path)
	config.Dimensions = 3
	config.SimilarityFn = core.CosineSimilarity
	// Flat, because these tests assert which record is nearest and an
	// approximate index is allowed to be approximate about that.
	config.IndexType = core.IndexTypeFlat

	db, err := Open(config)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(path + suffix)
		}
	})

	rows := []struct {
		id      string
		vec     []float32
		kind    string
		content string
	}{
		{"n1", []float32{1, 0, 0}, "note", "the backup vault is offline"},
		{"n2", []float32{0.99, 0.01, 0}, "note", "backup vault offline since Tuesday"},
		{"n3", []float32{0.98, 0.02, 0}, "note", "the vault that holds backups is down"},
		{"n4", []float32{0, 0, 1}, "note", "unrelated: the coffee machine"},
		{"s1", []float32{0, 1, 0}, "spec", "retention is ninety days"},
		{"s2", []float32{0.01, 0.99, 0}, "spec", "keep records for ninety days"},
	}
	ctx := context.Background()
	for _, r := range rows {
		if err := db.Vector().Upsert(ctx, &core.Embedding{
			ID: r.id, Vector: r.vec, Content: r.content,
			Metadata: map[string]string{"kind": r.kind},
		}); err != nil {
			t.Fatalf("upsert %s: %v", r.id, err)
		}
	}
	return db
}

// The facade can now answer a question about the whole store, which is what it
// could not do before: Aggregate, SearchWithFacets and VectorAggregate lived on
// *SQLiteStore, outside the Store interface, and DB holds the interface.
func TestTheFacadeCanAggregate(t *testing.T) {
	db := openAggregateBrain(t)
	ctx := context.Background()

	t.Run("counting by a metadata field", func(t *testing.T) {
		got, err := db.Aggregate(ctx, core.AggregationRequest{
			Type: core.AggregationGroupBy, GroupBy: []string{"kind"},
		})
		if err != nil {
			t.Fatalf("aggregate: %v", err)
		}
		counts := map[string]int{}
		for _, r := range got.Results {
			counts[fmt.Sprint(r.GroupKeys["kind"])] = r.Count
		}
		if counts["note"] != 4 || counts["spec"] != 2 {
			t.Fatalf("counts = %v, want note 4 and spec 2", counts)
		}
	})

	t.Run("a facet search filters and counts", func(t *testing.T) {
		hits, facets, err := db.SearchWithFacets(ctx, []float32{1, 0, 0}, core.FacetedSearchOptions{
			SearchOptions: core.SearchOptions{TopK: 10},
			Facets: map[string]core.FacetFilter{
				"kind": {Type: core.FilterTypeEquals, Values: []interface{}{"spec"}},
			},
			ReturnFacets: true,
		})
		if err != nil {
			t.Fatalf("faceted search: %v", err)
		}
		for _, h := range hits {
			if !strings.HasPrefix(h.ID, "s") {
				t.Fatalf("a note came back through a spec facet: %s", h.ID)
			}
		}
		if len(facets) == 0 {
			t.Fatal("ReturnFacets asked for counts and got none")
		}
	})
}

// The medoid is the point of the vector aggregations: it is the only one of the
// three that names a record instead of a coordinate.
func TestTheMedoidIsARealRecordAndTheCentroidIsNot(t *testing.T) {
	db := openAggregateBrain(t)
	ctx := context.Background()

	medoids, err := db.VectorAggregate(ctx, core.VectorAggregateRequest{
		Kind: core.VectorMedoid, GroupBy: "kind",
	})
	if err != nil {
		t.Fatalf("medoid: %v", err)
	}
	byGroup := map[string]core.VectorAggregateGroup{}
	for _, g := range medoids.Groups {
		byGroup[g.Group] = g
	}

	// Three of the four notes are the same claim about the same vault and the
	// fourth is about a coffee machine, so the representative must be one of
	// the three — an answer of n4 would mean the aggregation is picking by
	// something other than closeness.
	note := byGroup["note"]
	if note.MemberID == "" || note.MemberID == "n4" {
		t.Fatalf("note medoid = %q, want one of the three vault records", note.MemberID)
	}
	if note.MemberContent == "" {
		t.Fatal("the medoid came back without its text; a caller would need a second lookup for the thing it asked about")
	}
	if note.Count != 4 {
		t.Fatalf("note group counted %d, want 4", note.Count)
	}

	centroids, err := db.VectorAggregate(ctx, core.VectorAggregateRequest{
		Kind: core.VectorCentroid, GroupBy: "kind",
	})
	if err != nil {
		t.Fatalf("centroid: %v", err)
	}
	for _, g := range centroids.Groups {
		if len(g.Vector) != 3 {
			t.Fatalf("centroid of %q has %d dimensions, want 3", g.Group, len(g.Vector))
		}
		if g.MemberID != "" {
			t.Fatalf("centroid of %q named a record %q; it is a computed point and naming one would be a lie", g.Group, g.MemberID)
		}
	}
}

// Both new tools have to survive the trip a real caller makes: JSON in, JSON
// out, through the same dispatch the MCP server uses.
func TestTheNewToolsAnswerThroughTheDispatch(t *testing.T) {
	db := openAggregateBrain(t)
	tools := db.GraphRAGTools()
	ctx := context.Background()

	t.Run("aggregate_metadata", func(t *testing.T) {
		raw, err := tools.Call(ctx, "aggregate_metadata", json.RawMessage(`{"type":"group_by","group_by":["kind"],"order_by":"count"}`))
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		res, ok := raw.(*ToolAggregateMetadataResponse)
		if !ok {
			t.Fatalf("dispatch returned %T", raw)
		}
		if res.Total != 2 {
			t.Fatalf("total = %d, want 2 groups", res.Total)
		}
	})

	t.Run("representative_records", func(t *testing.T) {
		raw, err := tools.Call(ctx, "representative_records", json.RawMessage(`{"group_by":"kind"}`))
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		res, ok := raw.(*ToolRepresentativeRecordsResponse)
		if !ok {
			t.Fatalf("dispatch returned %T", raw)
		}
		if res.Count != 2 {
			t.Fatalf("count = %d, want one representative per kind", res.Count)
		}
		for _, g := range res.Groups {
			if g.MemberID == "" || g.Content == "" {
				t.Fatalf("group %q came back as %+v; a representative with no id or no text is not usable", g.Group, g)
			}
		}
	})

	// The name checks live in pkg/core, but a tool caller is the reason they
	// exist: this is the path where the string comes from a model.
	t.Run("a name that is not a name is refused through the tool", func(t *testing.T) {
		_, err := tools.Call(ctx, "aggregate_metadata",
			json.RawMessage(`{"type":"group_by","group_by":["kind"],"order_by":"(SELECT COUNT(*) FROM embeddings)"}`))
		if err == nil {
			t.Fatal("accepted: a model-supplied string reached the statement text")
		}
		if !strings.Contains(err.Error(), "order_by is not a valid metadata name") {
			t.Fatalf("refused for the wrong reason: %v", err)
		}
	})
}
