package importflow

import (
	"context"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

func TestKGSinkFlush(t *testing.T) {
	db := testDB(t)
	sink := newKGSink(db, 100)
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
