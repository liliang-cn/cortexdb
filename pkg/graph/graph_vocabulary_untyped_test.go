package graph

import (
	"context"
	"testing"
)

// Asking for the untyped bucket by the only name it has must find it.
//
// Every read here presents a NULL type column as "" — the SELECTs wrap it in
// COALESCE — so "" is the name a caller is given for the untyped bucket and the
// only name it can ask back for. EdgeShapes and EdgeEndpointPairs filtered on
// the raw column, where NULL equals nothing at all, so a caller that read ""
// out of EdgeTypeCounts and passed it straight back got no rows: not an error,
// an empty answer that reads as "there are none of those" about rows the
// previous call had just counted. NodePropertyKeys never had this — it already
// filtered on COALESCE(n.node_type, ”) — and that is the shape the other two
// now follow.
func TestFilteringByTheUntypedEdgeBucketFindsUntypedEdges(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedVocabularyGraph(t, b.store)
			if err := b.store.UpsertEdge(ctx, &GraphEdge{
				ID: "e5", FromNodeID: "pool", ToNodeID: "flag", Weight: 1,
			}); err != nil {
				t.Fatalf("UpsertEdge: %v", err)
			}
			// UpsertEdge stores an unset type as "", not NULL, so the bug is
			// not reachable through it — which is exactly why this went
			// unnoticed. NULLs arrive from everything else that has ever
			// written this column: an older schema, a migration, a direct
			// statement. The SELECTs COALESCE precisely because the authors
			// expected them, so the filter has to as well; forcing one here is
			// the only honest way to test the case the COALESCE exists for.
			if _, err := b.store.db.ExecContext(ctx, `UPDATE graph_edges SET edge_type = NULL WHERE id = 'e5'`); err != nil {
				t.Fatalf("force NULL: %v", err)
			}

			counts, err := b.store.EdgeTypeCounts(ctx)
			if err != nil {
				t.Fatalf("EdgeTypeCounts: %v", err)
			}
			if counts[""] != 1 {
				t.Fatalf("the fixture has %d untyped edges by EdgeTypeCounts, want 1: %v", counts[""], counts)
			}

			shapes, err := b.store.EdgeShapes(ctx, "")
			if err != nil {
				t.Fatalf("EdgeShapes: %v", err)
			}
			if len(shapes) != 1 || shapes[0].EdgeType != "" || shapes[0].Count != 1 {
				t.Fatalf("EdgeShapes(\"\") = %+v, want the one untyped edge that EdgeTypeCounts just reported", shapes)
			}

			pairs, err := b.store.EdgeEndpointPairs(ctx, "")
			if err != nil {
				t.Fatalf("EdgeEndpointPairs: %v", err)
			}
			if len(pairs) != 1 || pairs[0].EdgeType != "" {
				t.Fatalf("EdgeEndpointPairs(\"\") = %+v, want the one untyped edge", pairs)
			}

			// A named type must still filter to itself, so the fix cannot be
			// "match everything".
			named, err := b.store.EdgeShapes(ctx, "accepts")
			if err != nil {
				t.Fatalf("EdgeShapes(accepts): %v", err)
			}
			if len(named) != 1 || named[0].EdgeType != "accepts" {
				t.Fatalf("EdgeShapes(\"accepts\") = %+v, want only the accepts edge", named)
			}
		})
	}
}
