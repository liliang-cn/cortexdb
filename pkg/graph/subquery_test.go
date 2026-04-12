package graph

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

func TestSPARQLSubquery(t *testing.T) {
	dbPath := fmt.Sprintf("test_subquery_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	store, err := core.New(dbPath, 16)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init store: %v", err)
	}

	g := NewGraphStore(store)

	// Add some test data
	triples := []*RDFTriple{
		{Subject: NewIRI("alice"), Predicate: NewIRI("type"), Object: NewIRI("Person")},
		{Subject: NewIRI("bob"), Predicate: NewIRI("type"), Object: NewIRI("Person")},
		{Subject: NewIRI("charlie"), Predicate: NewIRI("type"), Object: NewIRI("Person")},
		{Subject: NewIRI("alice"), Predicate: NewIRI("knows"), Object: NewIRI("bob")},
		{Subject: NewIRI("alice"), Predicate: NewIRI("knows"), Object: NewIRI("charlie")},
		{Subject: NewIRI("bob"), Predicate: NewIRI("knows"), Object: NewIRI("charlie")},
	}
	if _, err := g.UpsertTriplesBatch(ctx, triples); err != nil {
		t.Fatalf("upsert triples: %v", err)
	}

	// Simple subquery: find people who know someone, and also their total friend count via subquery
	query := `
		SELECT ?x ?cnt WHERE {
			?x <type> <Person> .
			{ SELECT ?x (COUNT(?y) AS ?cnt) WHERE { ?x <knows> ?y } GROUP BY ?x }
		}
	`
	result, err := g.ExecuteSPARQL(ctx, query)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if result.Count != 2 {
		t.Errorf("Expected 2 results, got %d", result.Count)
	}

	foundAlice := false
	foundBob := false
	for _, b := range result.Bindings {
		x := b["x"].Value
		cnt := b["cnt"].Value
		if x == "alice" {
			foundAlice = true
			if cnt != "2" {
				t.Errorf("Alice expected count 2, got %v", cnt)
			}
		}
		if x == "bob" {
			foundBob = true
			if cnt != "1" {
				t.Errorf("Bob expected count 1, got %v", cnt)
			}
		}
	}

	if !foundAlice || !foundBob {
		t.Errorf("Missing expected results: alice:%v, bob:%v", foundAlice, foundBob)
	}
}

func TestSPARQLSubqueryDistinctLimit(t *testing.T) {
	dbPath := fmt.Sprintf("test_subquery_limit_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	store, err := core.New(dbPath, 16)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init store: %v", err)
	}

	g := NewGraphStore(store)

	// Add some test data
	triples := []*RDFTriple{
		{Subject: NewIRI("alice"), Predicate: NewIRI("knows"), Object: NewIRI("bob")},
		{Subject: NewIRI("alice"), Predicate: NewIRI("knows"), Object: NewIRI("charlie")},
		{Subject: NewIRI("bob"), Predicate: NewIRI("knows"), Object: NewIRI("charlie")},
		{Subject: NewIRI("bob"), Predicate: NewIRI("knows"), Object: NewIRI("alice")},
	}
	if _, err := g.UpsertTriplesBatch(ctx, triples); err != nil {
		t.Fatalf("upsert triples: %v", err)
	}

	// Subquery with LIMIT 1: for each person, get one person they know
	query := `
		SELECT ?x ?y WHERE {
			?x <knows> ?y .
			{ SELECT ?y WHERE { ?y <knows> ?z } LIMIT 1 }
		}
	`
	// Wait, the subquery here is evaluated ONCE.
	// So SELECT ?y WHERE { ?y <knows> ?z } LIMIT 1 will return exactly ONE ?y.
	// Then that ?y is joined with ?x <knows> ?y.
	
	result, err := g.ExecuteSPARQL(ctx, query)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	// The subquery will return either alice or bob (since they both know someone).
	// Let's say it returns alice. Then ?x <knows> "alice" matches (bob).
	// If it returns bob. Then ?x <knows> "bob" matches (alice).
	
	if result.Count != 1 {
		t.Errorf("Expected 1 result due to LIMIT 1 in subquery, got %d", result.Count)
	}
}
