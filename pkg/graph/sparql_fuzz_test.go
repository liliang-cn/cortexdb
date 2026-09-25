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
		"SELECT ?s FROM <g> FROM NAMED <h> WHERE { GRAPH ?g { ?s ?p ?o } }",
		"ASK FROM NAMED <g> { ?s ?p ?o }", "CONSTRUCT FROM <g> WHERE { ?s ?p ?o }",
		"CONSTRUCT { GRAPH ?g { ?s ?p ?o } } WHERE { GRAPH ?g { ?s ?p ?o } }",
		`SELECT (SUBSTR("李小龙", 2, 1) AS ?v) (REPLACE(?n, "(a)", "$1$2", "iq") AS ?r) WHERE { ?s ?p ?n }`,
		`SELECT ?n WHERE { ?s ?p ?n FILTER(REGEX(?n, "((((", "x") || ?n NOT IN (1, "a", ABS(?n))) }`,
		`SELECT (COUNT(DISTINCT *) AS ?c) (GROUP_CONCAT(DISTINCT ?n; SEPARATOR="|") AS ?g) WHERE { ?s ?p ?n } GROUP BY ?s ORDER BY DESC(?c)`,
		"SELECT REDUCED ?x WHERE { VALUES ?x { -1 +2 3.5 } BIND(ROUND(?x) / 0 AS ?y) FILTER regex(STR(?y), \"1\") }",
		"SELECT (IF(BOUND(?x), STRLEN(?x), NOW()) AS ?v) (TIMEZONE(?x) AS ?t) WHERE { FILTER NOT EXISTS { ?x ?p ?o } }",
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
