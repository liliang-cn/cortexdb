package cortexdb

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

func openFactsDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "facts.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func putEntity(t *testing.T, db *DB, name string) string {
	t.Helper()
	id := EntityNodeID(name)
	res, err := db.graph.UpsertNodesBatch(context.Background(), []*graph.GraphNode{{
		ID: id, Vector: []float32{0.1, 0.2, 0.3}, Content: name, NodeType: "entity",
	}})
	if err == nil {
		err = res.Err()
	}
	if err != nil {
		t.Fatalf("put entity %s: %v", name, err)
	}
	return id
}

func putEdge(t *testing.T, db *DB, id, from, to, edgeType string) {
	t.Helper()
	res, err := db.graph.UpsertEdgesBatch(context.Background(), []*graph.GraphEdge{{
		ID: id, FromNodeID: from, ToNodeID: to, EdgeType: edgeType, Weight: 1,
	}})
	if err == nil {
		err = res.Err()
	}
	if err != nil {
		t.Fatalf("put edge %s: %v", id, err)
	}
}

// Edge ids carry their provenance, so one fact asserted by two chunks is two
// rows. The pack printed it twice and the cap counted it twice.
func TestCollectGraphFactsDeduplicatesByTriple(t *testing.T) {
	db := openFactsDB(t)
	ctx := context.Background()
	a, b := putEntity(t, db, "CortexDB"), putEntity(t, db, "SQLite")
	putEdge(t, db, "edge:rel:chunk1:"+a+":"+b+":uses", a, b, "uses")
	putEdge(t, db, "edge:rel:chunk2:"+a+":"+b+":uses", a, b, "uses")

	facts := db.KnowledgeMemory().collectGraphFacts(ctx, "", []string{"CortexDB"})
	n := 0
	for _, f := range facts {
		if f.Predicate == "uses" && f.SubjectID == a && f.ObjectID == b {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the same triple appears %d times: %+v", n, facts)
	}
}

// co_occurs is generated for every adjacent pair of names, so it outnumbers
// everything; a budget spent on it buys nothing a reader can act on.
func TestCollectGraphFactsRanksTypedRelationsAboveCoOccurrence(t *testing.T) {
	db := openFactsDB(t)
	ctx := context.Background()
	hub := putEntity(t, db, "CortexDB")
	for i, name := range []string{"Alpha", "Beta", "Gamma", "Delta"} {
		other := putEntity(t, db, name)
		putEdge(t, db, "edge:co:"+string(rune('a'+i)), hub, other, "co_occurs")
	}
	dep := putEntity(t, db, "oss-agent")
	putEdge(t, db, "edge:rel:dep", dep, hub, "depends_on")

	facts := db.KnowledgeMemory().collectGraphFacts(ctx, "", []string{"CortexDB"})
	if len(facts) == 0 {
		t.Fatal("no facts collected")
	}
	if facts[0].Predicate != "depends_on" {
		t.Errorf("a typed relation should outrank co_occurs, got %q first: %+v",
			facts[0].Predicate, facts)
	}
}

// Weak edges still get through when they are all the graph has: a sparse graph
// saying two things were mentioned together beats saying nothing.
func TestCollectGraphFactsKeepsCoOccurrenceWhenNothingElseExists(t *testing.T) {
	db := openFactsDB(t)
	ctx := context.Background()
	a, b := putEntity(t, db, "CortexDB"), putEntity(t, db, "Lima")
	putEdge(t, db, "edge:co:only", a, b, "co_occurs")

	facts := db.KnowledgeMemory().collectGraphFacts(ctx, "", []string{"CortexDB"})
	if len(facts) != 1 || facts[0].Predicate != "co_occurs" {
		t.Errorf("expected the one weak fact to survive, got %+v", facts)
	}
}
