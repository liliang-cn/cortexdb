package graph

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// FuzzExecuteSPARQL throws arbitrary text at the SPARQL engine. A malformed
// query must return an error, never panic — the parser/executor is a large
// surface and untrusted input (agent- or user-authored queries) reaches it.
func FuzzExecuteSPARQL(f *testing.F) {
	dbPath := fmt.Sprintf("fuzz_sparql_%d.db", testname.Nano())
	store, err := core.New(dbPath, 16)
	if err != nil {
		f.Fatalf("store: %v", err)
	}
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		f.Fatalf("init: %v", err)
	}
	f.Cleanup(func() {
		_ = store.Close()
		for _, s := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + s)
		}
	})
	g := NewGraphStore(store)
	_ = g.UpsertNamespace(ctx, Namespace{Prefix: "ex", URI: "https://example.com/"})
	_, _ = g.UpsertTriplesBatch(ctx, []*RDFTriple{{
		Subject:   NewIRI("https://example.com/alice"),
		Predicate: NewIRI("https://schema.org/name"),
		Object:    NewLiteral("Alice"),
	}})

	for _, seed := range []string{
		"", "SELECT", "SELECT ?x WHERE {", "ASK { ?s ?p ?o }",
		"SELECT ?x WHERE { ?x ?y ?z } LIMIT", "prefix : <", "{{{{{",
		"SELECT ?a WHERE { ?a <p> ?b . FILTER(", "CONSTRUCT WHERE { }",
		"SELECT (COUNT(?x) AS ?c) WHERE { ?x ?p ?o } GROUP BY",
		"SELECT ?x WHERE { ?x (<a>|<b>)+ ?y }", "DESCRIBE <x>",
		"SELECT ?x { ?x ?p ?o } ORDER BY ?x OFFSET -1",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, query string) {
		// Any result or error is fine; a panic is a defect. Bound runaway
		// queries with a context so pathological inputs can't hang the fuzzer.
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ExecuteSPARQL(%q) panicked: %v", query, r)
			}
		}()
		_, _ = g.ExecuteSPARQL(cctx, query)
	})
}
