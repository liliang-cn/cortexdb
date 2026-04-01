package graph

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

func TestRefreshRDFSInferencesAndExplain(t *testing.T) {
	dbPath := fmt.Sprintf("test_rdfs_%d.db", time.Now().UnixNano())
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
	explicit := []*RDFTriple{
		{Subject: NewIRI("https://example.com/Manager"), Predicate: NewIRI(rdfsSubClassOfIRI), Object: NewIRI("https://example.com/Employee")},
		{Subject: NewIRI("https://example.com/Employee"), Predicate: NewIRI(rdfsSubClassOfIRI), Object: NewIRI("https://example.com/Person")},
		{Subject: NewIRI("https://example.com/worksFor"), Predicate: NewIRI(rdfsSubPropertyOfIRI), Object: NewIRI("https://example.com/affiliatedWith")},
		{Subject: NewIRI("https://example.com/worksFor"), Predicate: NewIRI(rdfsDomainIRI), Object: NewIRI("https://example.com/Employee")},
		{Subject: NewIRI("https://example.com/worksFor"), Predicate: NewIRI(rdfsRangeIRI), Object: NewIRI("https://example.com/Company")},
		{Subject: NewIRI("https://example.com/alice"), Predicate: NewIRI(rdfTypeIRI), Object: NewIRI("https://example.com/Manager")},
		{Subject: NewIRI("https://example.com/alice"), Predicate: NewIRI("https://example.com/worksFor"), Object: NewIRI("https://example.com/acme")},
	}
	if _, err := graphStore.UpsertTriplesBatch(ctx, explicit); err != nil {
		t.Fatalf("upsert explicit triples: %v", err)
	}

	refresh, err := graphStore.RefreshRDFSInferences(ctx)
	if err != nil {
		t.Fatalf("refresh rdfs inferences: %v", err)
	}
	if refresh.InferredCount == 0 {
		t.Fatalf("expected inferred triples, got %+v", refresh)
	}

	inferredOnly := true
	inferredTriples, err := graphStore.FindTriples(ctx, TriplePattern{Inferred: &inferredOnly})
	if err != nil {
		t.Fatalf("find inferred triples: %v", err)
	}
	if len(inferredTriples) < 4 {
		t.Fatalf("expected several inferred triples, got %d", len(inferredTriples))
	}

	personType, err := graphStore.FindTriples(ctx, TriplePattern{
		Subject:   ptrRDFTerm(NewIRI("https://example.com/alice")),
		Predicate: ptrRDFTerm(NewIRI(rdfTypeIRI)),
		Object:    ptrRDFTerm(NewIRI("https://example.com/Person")),
		Inferred:  &inferredOnly,
	})
	if err != nil {
		t.Fatalf("find alice type person: %v", err)
	}
	if len(personType) != 1 {
		t.Fatalf("expected inferred alice type person triple, got %+v", personType)
	}

	explanation, err := graphStore.ExplainTriple(ctx, personType[0].ID)
	if err != nil {
		t.Fatalf("explain inferred triple: %v", err)
	}
	if explanation.Explicit {
		t.Fatalf("expected inferred explanation, got %+v", explanation)
	}
	if explanation.Rule == "" {
		t.Fatalf("expected inference rule, got %+v", explanation)
	}
	if len(explanation.SupportTripleIDs) == 0 {
		t.Fatalf("expected support ids, got %+v", explanation)
	}

	classOnly, err := graphStore.FindTriples(ctx, TriplePattern{
		Subject:   ptrRDFTerm(NewIRI("https://example.com/Manager")),
		Predicate: ptrRDFTerm(NewIRI(rdfTypeIRI)),
		Object:    ptrRDFTerm(NewIRI(rdfsClassIRI)),
		Inferred:  &inferredOnly,
	})
	if err != nil {
		t.Fatalf("find inferred class declaration: %v", err)
	}
	if len(classOnly) != 1 {
		t.Fatalf("expected inferred class declaration, got %+v", classOnly)
	}

	propertyOnly, err := graphStore.FindTriples(ctx, TriplePattern{
		Subject:   ptrRDFTerm(NewIRI("https://example.com/worksFor")),
		Predicate: ptrRDFTerm(NewIRI(rdfTypeIRI)),
		Object:    ptrRDFTerm(NewIRI(rdfPropertyIRI)),
		Inferred:  &inferredOnly,
	})
	if err != nil {
		t.Fatalf("find inferred property declaration: %v", err)
	}
	if len(propertyOnly) != 1 {
		t.Fatalf("expected inferred property declaration, got %+v", propertyOnly)
	}

	trace, err := graphStore.ExplainTripleTrace(ctx, personType[0].ID, 2)
	if err != nil {
		t.Fatalf("explain inferred trace: %v", err)
	}
	if len(trace) < 2 {
		t.Fatalf("expected recursive support trace, got %+v", trace)
	}
}

func ptrRDFTerm(term RDFTerm) *RDFTerm {
	return &term
}
