package graph

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

func TestRDFTripleLifecycle(t *testing.T) {
	dbPath := fmt.Sprintf("test_rdf_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	store, err := core.New(dbPath, 32)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init store: %v", err)
	}

	graphStore := NewGraphStore(store)
	if err := graphStore.InitGraphSchema(ctx); err != nil {
		t.Fatalf("init graph schema: %v", err)
	}
	if err := graphStore.UpsertNamespace(ctx, Namespace{Prefix: "ex", URI: "https://example.com/"}); err != nil {
		t.Fatalf("upsert namespace: %v", err)
	}

	triples := []*RDFTriple{
		{
			Subject:   NewIRI("ex:alice"),
			Predicate: NewIRI("rdf:type"),
			Object:    NewIRI("schema:Person"),
		},
		{
			Subject:   NewIRI("ex:alice"),
			Predicate: NewIRI("schema:name"),
			Object:    NewLangLiteral("Alice", "en"),
		},
		{
			Subject:   NewIRI("ex:alice"),
			Predicate: NewIRI("schema:knows"),
			Object:    NewIRI("ex:bob"),
			Graph:     ptrTerm(NewIRI("ex:people")),
		},
	}

	result, err := graphStore.UpsertTriplesBatch(ctx, triples)
	if err != nil {
		t.Fatalf("upsert triples batch: %v", err)
	}
	if result.FailedCount != 0 {
		t.Fatalf("expected batch success, got %+v", result)
	}
	if triples[0].ID == "" {
		t.Fatal("expected triple ID to be populated")
	}

	found, err := graphStore.FindTriples(ctx, TriplePattern{
		Subject: ptrTerm(NewIRI("ex:alice")),
	})
	if err != nil {
		t.Fatalf("find triples: %v", err)
	}
	if len(found) != 3 {
		t.Fatalf("expected 3 triples, got %d", len(found))
	}

	edges, err := graphStore.GetEdges(ctx, rdfNodeID(NewIRI("https://example.com/alice")), "out")
	if err != nil {
		t.Fatalf("get rdf edges: %v", err)
	}
	if len(edges) != 3 {
		t.Fatalf("expected mirrored edges, got %d", len(edges))
	}

	var nquads bytes.Buffer
	if err := graphStore.ExportRDF(ctx, &nquads, RDFFormatNQuads); err != nil {
		t.Fatalf("export rdf: %v", err)
	}
	exported := nquads.String()
	if !strings.Contains(exported, "<https://example.com/alice>") {
		t.Fatalf("expected exported RDF to contain expanded subject, got %q", exported)
	}
	if !strings.Contains(exported, "\"Alice\"@en") {
		t.Fatalf("expected exported RDF to contain literal, got %q", exported)
	}

	if err := graphStore.DeleteTriple(ctx, *triples[1]); err != nil {
		t.Fatalf("delete triple: %v", err)
	}
	found, err = graphStore.FindTriples(ctx, TriplePattern{
		Subject: ptrTerm(NewIRI("ex:alice")),
	})
	if err != nil {
		t.Fatalf("find triples after delete: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("expected 2 triples after delete, got %d", len(found))
	}
}

func TestRDFTripleBatchPartialFailure(t *testing.T) {
	dbPath := fmt.Sprintf("test_rdf_partial_%d.db", time.Now().UnixNano())
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

	graphStore := NewGraphStore(store)
	if err := graphStore.InitGraphSchema(ctx); err != nil {
		t.Fatalf("init graph schema: %v", err)
	}

	triples := []*RDFTriple{
		{
			Subject:   NewIRI("https://example.com/alice"),
			Predicate: NewIRI("https://schema.org/name"),
			Object:    NewLiteral("Alice"),
		},
		{
			Subject:   NewLiteral("invalid-subject"),
			Predicate: NewIRI("https://schema.org/name"),
			Object:    NewLiteral("Broken"),
		},
	}

	result, err := graphStore.UpsertTriplesBatch(ctx, triples)
	if err != nil {
		t.Fatalf("upsert triples batch: %v", err)
	}
	if result.SuccessCount != 1 || result.FailedCount != 1 {
		t.Fatalf("expected one success and one failure, got %+v", result)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected one batch error, got %+v", result.Errors)
	}

	found, err := graphStore.FindTriples(ctx, TriplePattern{
		Subject: ptrTerm(NewIRI("https://example.com/alice")),
	})
	if err != nil {
		t.Fatalf("find persisted triple: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected valid triple to persist, got %+v", found)
	}
}

func TestRDFImportRoundTrip(t *testing.T) {
	dbPath := fmt.Sprintf("test_rdf_roundtrip_%d.db", time.Now().UnixNano())
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

	graphStore := NewGraphStore(store)
	payload := strings.TrimSpace(`
<https://example.com/alice> <https://schema.org/name> "Alice" .
<https://example.com/alice> <https://schema.org/knows> <https://example.com/bob> <https://example.com/people> .
`)
	count, err := graphStore.ImportRDF(ctx, strings.NewReader(payload), RDFFormatNQuads)
	if err != nil {
		t.Fatalf("import rdf: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 imported triples, got %d", count)
	}

	triples, err := graphStore.FindTriples(ctx, TriplePattern{})
	if err != nil {
		t.Fatalf("find imported triples: %v", err)
	}
	if len(triples) != 2 {
		t.Fatalf("expected 2 stored triples, got %d", len(triples))
	}
}

func TestRDFImportTurtleAndExportTriG(t *testing.T) {
	dbPath := fmt.Sprintf("test_rdf_turtle_trig_%d.db", time.Now().UnixNano())
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

	graphStore := NewGraphStore(store)
	if err := graphStore.UpsertNamespace(ctx, Namespace{Prefix: "ex", URI: "https://example.com/"}); err != nil {
		t.Fatalf("upsert namespace: %v", err)
	}

	turtlePayload := `
@prefix ex: <https://example.com/> .
@prefix schema: <https://schema.org/> .

ex:alice schema:knows [
	schema:name "Bob"
] .
`
	count, err := graphStore.ImportRDF(ctx, strings.NewReader(turtlePayload), RDFFormatTurtle)
	if err != nil {
		t.Fatalf("import turtle: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected turtle import to create 2 triples, got %d", count)
	}

	trigPayload := `
@prefix ex: <https://example.com/> .
@prefix schema: <https://schema.org/> .

ex:people {
	ex:alice schema:memberOf ex:team .
}
`
	count, err = graphStore.ImportRDF(ctx, strings.NewReader(trigPayload), RDFFormatTriG)
	if err != nil {
		t.Fatalf("import trig: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected trig import to create 1 quad, got %d", count)
	}

	var trig bytes.Buffer
	if err := graphStore.ExportRDF(ctx, &trig, RDFFormatTriG); err != nil {
		t.Fatalf("export trig: %v", err)
	}
	exported := trig.String()
	if !strings.Contains(exported, "ex:people {") {
		t.Fatalf("expected TriG export to contain graph block, got %q", exported)
	}
	if !strings.Contains(exported, "schema:memberOf") {
		t.Fatalf("expected TriG export to contain predicate, got %q", exported)
	}
}

func ptrTerm(term RDFTerm) *RDFTerm {
	return &term
}
