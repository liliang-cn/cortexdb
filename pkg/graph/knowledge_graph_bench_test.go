package graph

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

func BenchmarkKnowledgeGraphRDFUpsert(b *testing.B) {
	ctx := context.Background()
	g := newBenchmarkGraphStore(b, 8)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		triple := RDFTriple{
			Subject:   NewIRI(fmt.Sprintf("https://example.com/person/%d", i)),
			Predicate: NewIRI("https://schema.org/name"),
			Object:    NewLiteral(fmt.Sprintf("Person %d", i)),
		}
		if err := g.UpsertTriple(ctx, &triple); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkKnowledgeGraphRDFFindByPredicate(b *testing.B) {
	ctx := context.Background()
	g := newBenchmarkGraphStore(b, 8)
	for i := 0; i < 1000; i++ {
		triple := RDFTriple{
			Subject:   NewIRI(fmt.Sprintf("https://example.com/person/%d", i)),
			Predicate: NewIRI("https://schema.org/name"),
			Object:    NewLiteral(fmt.Sprintf("Person %d", i)),
		}
		if err := g.UpsertTriple(ctx, &triple); err != nil {
			b.Fatal(err)
		}
	}

	predicate := NewIRI("https://schema.org/name")
	pattern := TriplePattern{Predicate: &predicate, Limit: 20}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.FindTriples(ctx, pattern); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkKnowledgeGraphSPARQLSelect(b *testing.B) {
	ctx := context.Background()
	g := newBenchmarkGraphStore(b, 8)
	seedBenchmarkPeopleGraph(b, ctx, g, 1000)

	query := `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

SELECT ?name WHERE {
	ex:person-42 schema:name ?name .
}
`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.ExecuteSPARQL(ctx, query); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkKnowledgeGraphSPARQLPropertyPath(b *testing.B) {
	ctx := context.Background()
	g := newBenchmarkGraphStore(b, 8)
	triples := make([]*RDFTriple, 0, 500)
	for i := 0; i < 500; i++ {
		triples = append(triples, &RDFTriple{
			Subject:   NewIRI(fmt.Sprintf("https://example.com/node-%d", i)),
			Predicate: NewIRI("https://example.com/knows"),
			Object:    NewIRI(fmt.Sprintf("https://example.com/node-%d", i+1)),
		})
	}
	if _, err := g.UpsertTriplesBatch(ctx, triples); err != nil {
		b.Fatal(err)
	}

	query := `
PREFIX ex: <https://example.com/>

SELECT ?reachable WHERE {
	ex:node-1 ex:knows+ ?reachable .
}
LIMIT 20
`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.ExecuteSPARQL(ctx, query); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkKnowledgeGraphSPARQLSubquery(b *testing.B) {
	ctx := context.Background()
	g := newBenchmarkGraphStore(b, 8)
	seedBenchmarkPeopleGraph(b, ctx, g, 500)

	query := `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

SELECT ?person ?friend_count WHERE {
	?person schema:name ?name .
	{
		SELECT ?person (COUNT(?friend) AS ?friend_count) WHERE {
			?person ex:knows ?friend .
		}
		GROUP BY ?person
	}
}
LIMIT 20
`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.ExecuteSPARQL(ctx, query); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkKnowledgeGraphRDFSFullRefresh(b *testing.B) {
	ctx := context.Background()
	g := newBenchmarkGraphStore(b, 8)
	seedBenchmarkRDFSGraph(b, ctx, g, 25)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.RefreshRDFSInferences(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkKnowledgeGraphRDFSIncrementalRefresh(b *testing.B) {
	ctx := context.Background()
	g := newBenchmarkGraphStore(b, 8)
	seedBenchmarkRDFSGraph(b, ctx, g, 25)
	if _, err := g.RefreshRDFSInferences(ctx); err != nil {
		b.Fatal(err)
	}
	seed := RDFTriple{
		Subject:   NewIRI("https://example.com/Class24"),
		Predicate: NewIRI(rdfsSubClassOfIRI),
		Object:    NewIRI("https://example.com/FinalClass"),
	}
	if err := g.UpsertTriple(ctx, &seed); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.RefreshRDFSInferencesIncremental(ctx, []RDFTriple{seed}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkKnowledgeGraphSHACLValidate(b *testing.B) {
	ctx := context.Background()
	g := newBenchmarkGraphStore(b, 8)
	triples := make([]*RDFTriple, 0, 1000)
	for i := 0; i < 500; i++ {
		person := NewIRI(fmt.Sprintf("https://example.com/person-%d", i))
		triples = append(triples,
			&RDFTriple{Subject: person, Predicate: NewIRI(rdfTypeIRI), Object: NewIRI("https://example.com/Person")},
			&RDFTriple{Subject: person, Predicate: NewIRI("https://example.com/age"), Object: NewTypedLiteral(fmt.Sprintf("%d", i%100), XSDNamespace+"integer")},
		)
	}
	if _, err := g.UpsertTriplesBatch(ctx, triples); err != nil {
		b.Fatal(err)
	}
	shapes := []RDFTriple{
		{Subject: NewIRI("https://example.com/PersonShape"), Predicate: NewIRI(RDFType), Object: NewIRI(SHACLNodeShape)},
		{Subject: NewIRI("https://example.com/PersonShape"), Predicate: NewIRI(SHACLTargetClass), Object: NewIRI("https://example.com/Person")},
		{Subject: NewIRI("https://example.com/PersonShape"), Predicate: NewIRI(SHACLProperty), Object: NewIRI("https://example.com/AgeShape")},
		{Subject: NewIRI("https://example.com/AgeShape"), Predicate: NewIRI(SHACLPath), Object: NewIRI("https://example.com/age")},
		{Subject: NewIRI("https://example.com/AgeShape"), Predicate: NewIRI(SHACLDatatype), Object: NewIRI(XSDNamespace + "integer")},
		{Subject: NewIRI("https://example.com/AgeShape"), Predicate: NewIRI(SHACLMinCount), Object: NewLiteral("1")},
		{Subject: NewIRI("https://example.com/AgeShape"), Predicate: NewIRI(SHACLMinInclusive), Object: NewLiteral("0")},
		{Subject: NewIRI("https://example.com/AgeShape"), Predicate: NewIRI(SHACLMaxInclusive), Object: NewLiteral("150")},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.ValidateSHACL(ctx, shapes); err != nil {
			b.Fatal(err)
		}
	}
}

func newBenchmarkGraphStore(b *testing.B, dim int) *GraphStore {
	b.Helper()
	store, err := core.New(filepath.Join(b.TempDir(), "kg-bench.db"), dim)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		b.Fatal(err)
	}
	graphStore := NewGraphStore(store)
	if err := graphStore.InitGraphSchema(ctx); err != nil {
		b.Fatal(err)
	}
	return graphStore
}

func seedBenchmarkPeopleGraph(b *testing.B, ctx context.Context, g *GraphStore, count int) {
	b.Helper()
	triples := make([]*RDFTriple, 0, count*2)
	for i := 0; i < count; i++ {
		person := NewIRI(fmt.Sprintf("https://example.com/person-%d", i))
		triples = append(triples,
			&RDFTriple{Subject: person, Predicate: NewIRI("https://schema.org/name"), Object: NewLiteral(fmt.Sprintf("Person %d", i))},
			&RDFTriple{Subject: person, Predicate: NewIRI("https://example.com/knows"), Object: NewIRI(fmt.Sprintf("https://example.com/person-%d", (i+1)%count))},
		)
	}
	if _, err := g.UpsertTriplesBatch(ctx, triples); err != nil {
		b.Fatal(err)
	}
}

func seedBenchmarkRDFSGraph(b *testing.B, ctx context.Context, g *GraphStore, count int) {
	b.Helper()
	triples := make([]*RDFTriple, 0, count*2)
	for i := 0; i < count; i++ {
		class := NewIRI(fmt.Sprintf("https://example.com/Class%d", i))
		nextClass := NewIRI(fmt.Sprintf("https://example.com/Class%d", i+1))
		instance := NewIRI(fmt.Sprintf("https://example.com/instance-%d", i))
		triples = append(triples,
			&RDFTriple{Subject: class, Predicate: NewIRI(rdfsSubClassOfIRI), Object: nextClass},
			&RDFTriple{Subject: instance, Predicate: NewIRI(rdfTypeIRI), Object: class},
		)
	}
	if _, err := g.UpsertTriplesBatch(ctx, triples); err != nil {
		b.Fatal(err)
	}
}
