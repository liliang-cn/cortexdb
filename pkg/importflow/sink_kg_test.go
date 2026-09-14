package importflow

import (
	"context"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

func TestKGSinkFlush(t *testing.T) {
	db := testDB(t)
	sink := newKGSink(db, 100, nil)
	ctx := context.Background()

	triples := []graph.RDFTriple{
		{
			Subject:   graph.NewIRI("urn:cortexdb:Customer:c1"),
			Predicate: graph.NewIRI("urn:cortexdb:rel:purchased"),
			Object:    graph.NewIRI("urn:cortexdb:Product:p9"),
		},
	}
	if err := sink.add(ctx, triples); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := sink.flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if sink.count() != 1 {
		t.Fatalf("count = %d; want 1", sink.count())
	}
}

// TestAnImportedTripleSaysWhereItCameFrom is the gap this closed. A live
// database import wrote triples into the brain with nothing on them: the plan
// that was signed to read the table, the run that read it and the operator who
// authorized it were all in the ledger and on none of the facts. Asking one of
// those facts where it came from had no answer, which on a store whose claim
// is that every record can say how it knows is the claim failing at one door.
func TestAnImportedTripleSaysWhereItCameFrom(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	sink := newKGSink(db, 100, map[string]string{
		"_grade":    "asserted",
		"_producer": "livedb",
		"_source":   "athanor:livedb:plan-7",
	})
	if err := sink.add(ctx, []graph.RDFTriple{{
		Subject:   graph.RDFTerm{Kind: "iri", Value: "urn:row:1"},
		Predicate: graph.RDFTerm{Kind: "iri", Value: "urn:cortexdb:prop:hostname"},
		Object:    graph.RDFTerm{Kind: "literal", Value: "node-a"},
	}}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := sink.flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	props := readEdgeProperties(t, db, "urn:row:1")
	for key, want := range map[string]string{
		"_grade": "asserted", "_producer": "livedb", "_source": "athanor:livedb:plan-7",
	} {
		if got, _ := props[key].(string); got != want {
			t.Errorf("edge property %s = %v, want %q", key, props[key], want)
		}
	}
	// And it still says what it is. Provenance that could overwrite the
	// triple's own description would be a fact able to lie about itself
	// through the field meant to hold it to account.
	if got, _ := props["rdf"].(bool); !got {
		t.Error("the triple stopped describing itself as RDF")
	}
}

// TestProvenanceCannotOverwriteWhatATripleIs guards the merge direction.
func TestProvenanceCannotOverwriteWhatATripleIs(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	sink := newKGSink(db, 100, map[string]string{"inferred": "true", "triple_id": "forged"})
	if err := sink.add(ctx, []graph.RDFTriple{{
		Subject:   graph.RDFTerm{Kind: "iri", Value: "urn:row:2"},
		Predicate: graph.RDFTerm{Kind: "iri", Value: "urn:cortexdb:prop:rack"},
		Object:    graph.RDFTerm{Kind: "literal", Value: "r12"},
	}}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := sink.flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	props := readEdgeProperties(t, db, "urn:row:2")
	if got, _ := props["inferred"].(bool); got {
		t.Error("a stated triple was relabelled as inferred through its provenance map")
	}
	if got, _ := props["triple_id"].(string); got == "forged" {
		t.Error("provenance overwrote the triple's identity")
	}
}

// readEdgeProperties reads back the edge an imported triple became.
//
// Through the edge and not the triple, because that is where it matters: the
// knowledge contract — grade, producer, source — is read off edge properties.
// A provenance legible only in the triple table would be legible to nothing
// that audits this store.
func readEdgeProperties(t *testing.T, db *cortexdb.DB, subjectIRI string) map[string]any {
	t.Helper()
	ctx := context.Background()
	nodes, err := db.Graph().GetAllNodes(ctx, nil)
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	for _, n := range nodes {
		if v, _ := n.Properties["value"].(string); v != subjectIRI {
			continue
		}
		edges, err := db.Graph().GetEdges(ctx, n.ID, "out")
		if err != nil {
			t.Fatalf("edges of %s: %v", subjectIRI, err)
		}
		if len(edges) != 1 {
			t.Fatalf("subject %s has %d outgoing edges, want 1", subjectIRI, len(edges))
		}
		return edges[0].Properties
	}
	t.Fatalf("no graph node for RDF subject %s", subjectIRI)
	return nil
}
