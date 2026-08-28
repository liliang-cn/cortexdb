package graph

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

func TestExecuteSPARQLSelectAndAsk(t *testing.T) {
	dbPath := fmt.Sprintf("test_sparql_%d.db", testname.Nano())
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

	triples := []*RDFTriple{
		{
			Subject:   NewIRI("https://example.com/alice"),
			Predicate: NewIRI("https://schema.org/name"),
			Object:    NewLiteral("Alice"),
		},
		{
			Subject:   NewIRI("https://example.com/alice"),
			Predicate: NewIRI("https://schema.org/memberOf"),
			Object:    NewIRI("https://example.com/team"),
			Graph:     ptrSPARQLTerm(NewIRI("https://example.com/people")),
		},
	}
	if _, err := graphStore.UpsertTriplesBatch(ctx, triples); err != nil {
		t.Fatalf("upsert triples: %v", err)
	}

	selectResult, err := graphStore.ExecuteSPARQL(ctx, `
prefix ex: <https://example.com/>
prefix schema: <https://schema.org/>

select ?name where {
	ex:alice schema:name ?name .
	filter(CONTAINS(LCASE(STR(?name)), "ali"))
} limit 1
`)
	if err != nil {
		t.Fatalf("execute select: %v", err)
	}
	if selectResult.QueryType != SPARQLQuerySelect {
		t.Fatalf("unexpected query type: %+v", selectResult)
	}
	if selectResult.Count != 1 {
		t.Fatalf("expected one binding, got %+v", selectResult)
	}
	if got := selectResult.Bindings[0]["name"].Value; got != "Alice" {
		t.Fatalf("unexpected selected name: %s", got)
	}

	askDefault, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

ASK WHERE {
	ex:alice schema:memberOf ex:team .
}
`)
	if err != nil {
		t.Fatalf("execute ask default graph: %v", err)
	}
	if askDefault.Boolean {
		t.Fatalf("expected default graph ASK to be false, got %+v", askDefault)
	}

	askNamed, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

ASK WHERE {
	GRAPH ex:people {
		ex:alice schema:memberOf ex:team .
	}
}
`)
	if err != nil {
		t.Fatalf("execute ask named graph: %v", err)
	}
	if !askNamed.Boolean {
		t.Fatalf("expected named graph ASK to be true, got %+v", askNamed)
	}

	selectGraph, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

SELECT ?g WHERE {
	GRAPH ?g {
		ex:alice schema:memberOf ex:team .
	}
}
`)
	if err != nil {
		t.Fatalf("execute select graph: %v", err)
	}
	if selectGraph.Count != 1 {
		t.Fatalf("expected one graph binding, got %+v", selectGraph)
	}
	if got := selectGraph.Bindings[0]["g"].Value; got != "https://example.com/people" {
		t.Fatalf("unexpected graph binding: %s", got)
	}
}

func TestExecuteSPARQLOptionalUnionAndOrderBy(t *testing.T) {
	dbPath := fmt.Sprintf("test_sparql_advanced_%d.db", testname.Nano())
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
	triples := []*RDFTriple{
		{Subject: NewIRI("https://example.com/alice"), Predicate: NewIRI("https://schema.org/name"), Object: NewLiteral("Alice")},
		{Subject: NewIRI("https://example.com/alice"), Predicate: NewIRI("https://schema.org/email"), Object: NewLiteral("alice@example.com")},
		{Subject: NewIRI("https://example.com/bob"), Predicate: NewIRI("https://schema.org/name"), Object: NewLiteral("Bob")},
		{Subject: NewIRI("https://example.com/carol"), Predicate: NewIRI("https://schema.org/name"), Object: NewLiteral("Carol")},
	}
	if _, err := graphStore.UpsertTriplesBatch(ctx, triples); err != nil {
		t.Fatalf("upsert triples: %v", err)
	}

	result, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

SELECT ?person ?name ?email WHERE {
	{
		?person schema:name ?name .
		FILTER(CONTAINS(LCASE(STR(?name)), "ali"))
	}
	UNION
	{
		?person schema:name ?name .
		FILTER(CONTAINS(LCASE(STR(?name)), "bob"))
	}
	OPTIONAL {
		?person schema:email ?email .
	}
}
ORDER BY DESC(?name)
`)
	if err != nil {
		t.Fatalf("execute advanced sparql: %v", err)
	}
	if result.Count != 2 {
		t.Fatalf("expected two rows, got %+v", result)
	}
	if got := result.Bindings[0]["name"].Value; got != "Bob" {
		t.Fatalf("expected Bob first after DESC ordering, got %s", got)
	}
	if got := result.Bindings[1]["name"].Value; got != "Alice" {
		t.Fatalf("expected Alice second after DESC ordering, got %s", got)
	}
	if _, ok := result.Bindings[0]["email"]; ok {
		t.Fatalf("expected Bob row to omit optional email binding, got %+v", result.Bindings[0])
	}
	if got := result.Bindings[1]["email"].Value; got != "alice@example.com" {
		t.Fatalf("expected Alice email binding, got %+v", result.Bindings[1])
	}
}

func TestExecuteSPARQLConstructDistinctOffsetAndCompare(t *testing.T) {
	dbPath := fmt.Sprintf("test_sparql_construct_%d.db", testname.Nano())
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
	triples := []*RDFTriple{
		{Subject: NewIRI("https://example.com/alice"), Predicate: NewIRI("https://schema.org/name"), Object: NewLiteral("Alice")},
		{Subject: NewIRI("https://example.com/alice"), Predicate: NewIRI("https://schema.org/score"), Object: NewTypedLiteral("10", builtinNamespaces["xsd"]+"integer")},
		{Subject: NewIRI("https://example.com/bob"), Predicate: NewIRI("https://schema.org/name"), Object: NewLiteral("Bob")},
		{Subject: NewIRI("https://example.com/bob"), Predicate: NewIRI("https://schema.org/score"), Object: NewTypedLiteral("20", builtinNamespaces["xsd"]+"integer")},
		{Subject: NewIRI("https://example.com/bob"), Predicate: NewIRI("https://schema.org/alias"), Object: NewLiteral("Bob")},
	}
	if _, err := graphStore.UpsertTriplesBatch(ctx, triples); err != nil {
		t.Fatalf("upsert triples: %v", err)
	}

	selectResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX schema: <https://schema.org/>

SELECT DISTINCT ?name WHERE {
	?person schema:name ?name ;
		schema:score ?score .
	FILTER(?score >= 10)
}
ORDER BY ?name
OFFSET 1
`)
	if err != nil {
		t.Fatalf("execute distinct/offset query: %v", err)
	}
	if selectResult.Count != 1 {
		t.Fatalf("expected one row after DISTINCT/OFFSET, got %+v", selectResult)
	}
	if got := selectResult.Bindings[0]["name"].Value; got != "Bob" {
		t.Fatalf("expected Bob after OFFSET, got %s", got)
	}

	constructResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX schema: <https://schema.org/>
PREFIX ex: <https://example.com/>

CONSTRUCT {
	?person schema:label ?name .
}
WHERE {
	?person schema:name ?name ;
		schema:score ?score .
	FILTER(?score > 10)
}
`)
	if err != nil {
		t.Fatalf("execute construct query: %v", err)
	}
	if constructResult.QueryType != SPARQLQueryConstruct {
		t.Fatalf("unexpected construct query type: %+v", constructResult)
	}
	if len(constructResult.Triples) != 1 {
		t.Fatalf("expected one constructed triple, got %+v", constructResult.Triples)
	}
	if got := constructResult.Triples[0].Object.Value; got != "Bob" {
		t.Fatalf("expected constructed object Bob, got %s", got)
	}
}

func TestExecuteSPARQLDescribeValuesAndRegex(t *testing.T) {
	dbPath := fmt.Sprintf("test_sparql_describe_%d.db", testname.Nano())
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
	triples := []*RDFTriple{
		{Subject: NewIRI("https://example.com/alice"), Predicate: NewIRI("https://schema.org/name"), Object: NewLangLiteral("Alice", "en")},
		{Subject: NewIRI("https://example.com/alice"), Predicate: NewIRI("https://schema.org/code"), Object: NewTypedLiteral("A-1", builtinNamespaces["xsd"]+"string")},
		{Subject: NewIRI("https://example.com/team"), Predicate: NewIRI("https://schema.org/member"), Object: NewIRI("https://example.com/alice")},
		{Subject: NewIRI("https://example.com/bob"), Predicate: NewIRI("https://schema.org/name"), Object: NewLangLiteral("Bobby", "en")},
	}
	if _, err := graphStore.UpsertTriplesBatch(ctx, triples); err != nil {
		t.Fatalf("upsert triples: %v", err)
	}

	valuesResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

SELECT ?person ?name WHERE {
	VALUES ?person { ex:alice ex:bob }
	?person schema:name ?name .
	FILTER(REGEX(STR(?name), "^A", "i"))
}
`)
	if err != nil {
		t.Fatalf("execute values/regex query: %v", err)
	}
	if valuesResult.Count != 1 {
		t.Fatalf("expected one VALUES/REGEX row, got %+v", valuesResult)
	}
	if got := valuesResult.Bindings[0]["person"].Value; got != "https://example.com/alice" {
		t.Fatalf("expected Alice binding, got %s", got)
	}

	langResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

SELECT ?lang ?dt WHERE {
	ex:alice schema:name ?name ;
		schema:code ?code .
	FILTER(LANG(?name) = "en")
	FILTER(DATATYPE(?code) = <http://www.w3.org/2001/XMLSchema#string>)
}
`)
	if err != nil {
		t.Fatalf("execute lang/datatype query: %v", err)
	}
	if langResult.Count != 1 {
		t.Fatalf("expected one LANG/DATATYPE row, got %+v", langResult)
	}

	describeResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>

DESCRIBE ex:alice
`)
	if err != nil {
		t.Fatalf("execute describe query: %v", err)
	}
	if describeResult.QueryType != SPARQLQueryDescribe {
		t.Fatalf("unexpected describe query type: %+v", describeResult)
	}
	if len(describeResult.Triples) < 3 {
		t.Fatalf("expected describe to return neighborhood triples, got %+v", describeResult.Triples)
	}
}

func TestExecuteSPARQLMinus(t *testing.T) {
	dbPath := fmt.Sprintf("test_sparql_minus_%d.db", testname.Nano())
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
	triples := []*RDFTriple{
		{Subject: NewIRI("https://example.com/alice"), Predicate: NewIRI("https://schema.org/name"), Object: NewLiteral("Alice")},
		{Subject: NewIRI("https://example.com/alice"), Predicate: NewIRI("https://schema.org/email"), Object: NewLiteral("alice@example.com")},
		{Subject: NewIRI("https://example.com/bob"), Predicate: NewIRI("https://schema.org/name"), Object: NewLiteral("Bob")},
	}
	if _, err := graphStore.UpsertTriplesBatch(ctx, triples); err != nil {
		t.Fatalf("upsert triples: %v", err)
	}

	result, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX schema: <https://schema.org/>

SELECT ?person WHERE {
	?person schema:name ?name .
	MINUS {
		?person schema:email ?email .
	}
}
`)
	if err != nil {
		t.Fatalf("execute minus query: %v", err)
	}
	if result.Count != 1 {
		t.Fatalf("expected one row after MINUS, got %+v", result)
	}
	if got := result.Bindings[0]["person"].Value; got != "https://example.com/bob" {
		t.Fatalf("unexpected remaining person: %s", got)
	}
}

func TestExecuteSPARQLUpdates(t *testing.T) {
	dbPath := fmt.Sprintf("test_sparql_updates_%d.db", testname.Nano())
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

	insertResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

INSERT DATA {
	ex:alice schema:name "Alice" .
	GRAPH ex:people {
		ex:alice schema:memberOf ex:team .
	}
}
`)
	if err != nil {
		t.Fatalf("insert data: %v", err)
	}
	if insertResult.Count != 2 {
		t.Fatalf("expected 2 inserted triples, got %+v", insertResult)
	}

	deleteDataResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

DELETE DATA {
	ex:alice schema:name "Alice" .
}
`)
	if err != nil {
		t.Fatalf("delete data: %v", err)
	}
	if deleteDataResult.Count != 1 {
		t.Fatalf("expected 1 deleted triple, got %+v", deleteDataResult)
	}

	remaining, err := graphStore.FindTriples(ctx, TriplePattern{})
	if err != nil {
		t.Fatalf("find triples after delete data: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining triple after delete data, got %d", len(remaining))
	}

	if _, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

INSERT DATA {
	ex:bob schema:name "Bob" .
	ex:bob schema:email "bob@example.com" .
}
`); err != nil {
		t.Fatalf("insert more data: %v", err)
	}

	deleteWhereResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

DELETE WHERE {
	ex:bob ?p ?o .
}
`)
	if err != nil {
		t.Fatalf("delete where: %v", err)
	}
	if deleteWhereResult.Count != 2 {
		t.Fatalf("expected 2 deleted triples from delete where, got %+v", deleteWhereResult)
	}

	insertWhereResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

INSERT {
	ex:alice schema:label ?name .
}
WHERE {
	GRAPH ex:people {
		ex:alice schema:memberOf ex:team .
	}
	BIND("Alice" AS ?name)
}
`)
	if err != nil {
		t.Fatalf("insert where: %v", err)
	}
	if insertWhereResult.Count != 1 {
		t.Fatalf("expected 1 inserted triple from insert where, got %+v", insertWhereResult)
	}

	if _, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

INSERT DATA {
	ex:alice schema:name "Alice" .
}
`); err != nil {
		t.Fatalf("reinsert name for modify test: %v", err)
	}

	modifyResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

DELETE {
	ex:alice schema:name ?old_name .
}
INSERT {
	ex:alice schema:name "Alice Updated" .
}
WHERE {
	ex:alice schema:name ?old_name .
}
`)
	if err != nil {
		t.Fatalf("delete/insert where: %v", err)
	}
	if modifyResult.Count != 2 {
		t.Fatalf("expected 2 changes from modify query, got %+v", modifyResult)
	}

	if _, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

INSERT DATA {
	GRAPH ex:g1 {
		ex:carol schema:name "Carol" .
	}
	GRAPH ex:g2 {
		ex:carol schema:name "Carol" .
	}
}
`); err != nil {
		t.Fatalf("insert graph data for WITH/USING test: %v", err)
	}

	withResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

WITH ex:g1
DELETE {
	ex:carol schema:name ?old .
}
INSERT {
	ex:carol schema:name "Carol v2" .
}
WHERE {
	ex:carol schema:name ?old .
}
`)
	if err != nil {
		t.Fatalf("with modify query: %v", err)
	}
	if withResult.Count != 2 {
		t.Fatalf("expected WITH modify to apply 2 changes, got %+v", withResult)
	}

	usingResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

DELETE {
	ex:carol schema:name ?old .
}
USING ex:g2
WHERE {
	ex:carol schema:name ?old .
}
`)
	if err != nil {
		t.Fatalf("using delete query: %v", err)
	}
	if usingResult.Count != 1 {
		t.Fatalf("expected USING delete to delete 1 triple, got %+v", usingResult)
	}
}

func TestExecuteSPARQLGroupByHavingCountAndBind(t *testing.T) {
	dbPath := fmt.Sprintf("test_sparql_groupby_%d.db", testname.Nano())
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
	triples := []*RDFTriple{
		{Subject: NewIRI("https://example.com/bob"), Predicate: NewIRI("https://schema.org/alias"), Object: NewLiteral("Bob")},
		{Subject: NewIRI("https://example.com/bob"), Predicate: NewIRI("https://schema.org/alias"), Object: NewLiteral("BOB")},
		{Subject: NewIRI("https://example.com/alice"), Predicate: NewIRI("https://schema.org/alias"), Object: NewLiteral("Alice")},
	}
	if _, err := graphStore.UpsertTriplesBatch(ctx, triples); err != nil {
		t.Fatalf("upsert triples: %v", err)
	}

	result, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX schema: <https://schema.org/>

SELECT ?person ?normalized (COUNT(?alias) AS ?alias_count) WHERE {
	?person schema:alias ?alias .
	BIND(LCASE(STR(?alias)) AS ?normalized)
}
GROUP BY ?person ?normalized
HAVING (COUNT(?alias) > 1)
ORDER BY DESC(COUNT(?alias))
`)
	if err != nil {
		t.Fatalf("group by/having/count/bind query: %v", err)
	}
	if result.Count != 1 {
		t.Fatalf("expected one grouped row, got %+v", result)
	}
	if got := result.Bindings[0]["person"].Value; got != "https://example.com/bob" {
		t.Fatalf("expected bob group, got %s", got)
	}
	if got := result.Bindings[0]["normalized"].Value; got != "bob" {
		t.Fatalf("expected normalized alias bob, got %s", got)
	}
	if got := result.Bindings[0]["alias_count"].Value; got != "2" {
		t.Fatalf("expected alias_count 2, got %s", got)
	}

	aggResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX schema: <https://schema.org/>

SELECT
	(SUM(?score) AS ?sum)
	(AVG(?score) AS ?avg)
	(MIN(?score) AS ?min)
	(MAX(?score) AS ?max)
WHERE {
	VALUES ?score { 10 20 30 }
}
`)
	if err != nil {
		t.Fatalf("aggregate query: %v", err)
	}
	if aggResult.Count != 1 {
		t.Fatalf("expected one aggregate row, got %+v", aggResult)
	}
	if aggResult.Bindings[0]["sum"].Value != "60" {
		t.Fatalf("unexpected SUM: %+v", aggResult.Bindings[0])
	}
	if aggResult.Bindings[0]["avg"].Value != "20" {
		t.Fatalf("unexpected AVG: %+v", aggResult.Bindings[0])
	}
	if aggResult.Bindings[0]["min"].Value != "10" {
		t.Fatalf("unexpected MIN: %+v", aggResult.Bindings[0])
	}
	if aggResult.Bindings[0]["max"].Value != "30" {
		t.Fatalf("unexpected MAX: %+v", aggResult.Bindings[0])
	}

	groupConcatResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX schema: <https://schema.org/>

SELECT
	(SAMPLE(?alias) AS ?sample_alias)
	(GROUP_CONCAT(?alias; SEPARATOR=",") AS ?aliases)
WHERE {
	?person schema:alias ?alias .
}
GROUP BY ?person
HAVING (COUNT(?alias) > 1)
`)
	if err != nil {
		t.Fatalf("group concat query: %v", err)
	}
	if groupConcatResult.Count != 1 {
		t.Fatalf("expected one group concat row, got %+v", groupConcatResult)
	}
	if groupConcatResult.Bindings[0]["sample_alias"].Value == "" {
		t.Fatalf("expected SAMPLE value, got %+v", groupConcatResult.Bindings[0])
	}
	if aliases := groupConcatResult.Bindings[0]["aliases"].Value; aliases != "Bob,BOB" && aliases != "BOB,Bob" {
		t.Fatalf("unexpected GROUP_CONCAT value: %+v", groupConcatResult.Bindings[0])
	}

	exprResult, err := graphStore.ExecuteSPARQL(ctx, `
SELECT (?a + ?b AS ?sum) (IF((?a + ?b > 12) && !(?a < 10), "big", COALESCE(?missing, "fallback")) AS ?label) WHERE {
	VALUES (?a ?b) { (10 5) }
}
`)
	if err != nil {
		t.Fatalf("expression query: %v", err)
	}
	if exprResult.Count != 1 {
		t.Fatalf("expected one expression row, got %+v", exprResult)
	}
	if exprResult.Bindings[0]["sum"].Value != "15" {
		t.Fatalf("unexpected arithmetic result: %+v", exprResult.Bindings[0])
	}
	if exprResult.Bindings[0]["label"].Value != "big" {
		t.Fatalf("unexpected IF/COALESCE result: %+v", exprResult.Bindings[0])
	}
}

func TestExecuteSPARQLExistsAndNotExists(t *testing.T) {
	dbPath := fmt.Sprintf("test_sparql_exists_%d.db", testname.Nano())
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
	triples := []*RDFTriple{
		{Subject: NewIRI("https://example.com/alice"), Predicate: NewIRI("https://schema.org/name"), Object: NewLiteral("Alice")},
		{Subject: NewIRI("https://example.com/alice"), Predicate: NewIRI("https://schema.org/email"), Object: NewLiteral("alice@example.com")},
		{Subject: NewIRI("https://example.com/bob"), Predicate: NewIRI("https://schema.org/name"), Object: NewLiteral("Bob")},
	}
	if _, err := graphStore.UpsertTriplesBatch(ctx, triples); err != nil {
		t.Fatalf("upsert triples: %v", err)
	}

	existsResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX schema: <https://schema.org/>

SELECT ?person WHERE {
	?person schema:name ?name .
	FILTER EXISTS {
		?person schema:email ?email .
	}
}
`)
	if err != nil {
		t.Fatalf("exists query: %v", err)
	}
	if existsResult.Count != 1 {
		t.Fatalf("expected one EXISTS match, got %+v", existsResult)
	}
	if got := existsResult.Bindings[0]["person"].Value; got != "https://example.com/alice" {
		t.Fatalf("expected alice EXISTS match, got %s", got)
	}

	notExistsResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX schema: <https://schema.org/>

SELECT ?person WHERE {
	?person schema:name ?name .
	FILTER NOT EXISTS {
		?person schema:email ?email .
	}
}
`)
	if err != nil {
		t.Fatalf("not exists query: %v", err)
	}
	if notExistsResult.Count != 1 {
		t.Fatalf("expected one NOT EXISTS match, got %+v", notExistsResult)
	}
	if got := notExistsResult.Bindings[0]["person"].Value; got != "https://example.com/bob" {
		t.Fatalf("expected bob NOT EXISTS match, got %s", got)
	}
}

func TestExecuteSPARQLPropertyPaths(t *testing.T) {
	dbPath := fmt.Sprintf("test_sparql_paths_%d.db", testname.Nano())
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
	triples := []*RDFTriple{
		{Subject: NewIRI("https://example.com/alice"), Predicate: NewIRI("https://example.com/knows"), Object: NewIRI("https://example.com/bob")},
		{Subject: NewIRI("https://example.com/bob"), Predicate: NewIRI("https://example.com/knows"), Object: NewIRI("https://example.com/carol")},
		{Subject: NewIRI("https://example.com/alice"), Predicate: NewIRI("https://example.com/parent"), Object: NewIRI("https://example.com/dana")},
		{Subject: NewIRI("https://example.com/alice"), Predicate: NewIRI("https://schema.org/name"), Object: NewLiteral("Alice")},
		{Subject: NewIRI("https://example.com/alice"), Predicate: NewIRI("https://example.com/nickname"), Object: NewLiteral("Al")},
	}
	if _, err := graphStore.UpsertTriplesBatch(ctx, triples); err != nil {
		t.Fatalf("upsert triples: %v", err)
	}

	plusResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>

SELECT ?person WHERE {
	ex:alice ex:knows+ ?person .
}
ORDER BY ?person
`)
	if err != nil {
		t.Fatalf("plus path query: %v", err)
	}
	if plusResult.Count != 2 {
		t.Fatalf("expected two + path matches, got %+v", plusResult)
	}
	if plusResult.Bindings[0]["person"].Value != "https://example.com/bob" || plusResult.Bindings[1]["person"].Value != "https://example.com/carol" {
		t.Fatalf("unexpected + path results: %+v", plusResult.Bindings)
	}

	starResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>

SELECT ?person WHERE {
	ex:alice ex:knows* ?person .
}
ORDER BY ?person
`)
	if err != nil {
		t.Fatalf("star path query: %v", err)
	}
	if starResult.Count != 3 {
		t.Fatalf("expected three * path matches, got %+v", starResult)
	}
	if starResult.Bindings[0]["person"].Value != "https://example.com/alice" {
		t.Fatalf("expected zero-length * path match for alice, got %+v", starResult.Bindings)
	}

	inverseResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>

SELECT ?child WHERE {
	?child ^ex:parent ex:alice .
}
`)
	if err != nil {
		t.Fatalf("inverse path query: %v", err)
	}
	if inverseResult.Count != 1 || inverseResult.Bindings[0]["child"].Value != "https://example.com/dana" {
		t.Fatalf("unexpected inverse path results: %+v", inverseResult.Bindings)
	}

	altResult, err := graphStore.ExecuteSPARQL(ctx, `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

SELECT ?label WHERE {
	ex:alice ex:nickname|schema:name ?label .
}
ORDER BY ?label
`)
	if err != nil {
		t.Fatalf("alternative path query: %v", err)
	}
	if altResult.Count != 2 {
		t.Fatalf("expected two alternative path matches, got %+v", altResult)
	}
	if altResult.Bindings[0]["label"].Value != "Al" || altResult.Bindings[1]["label"].Value != "Alice" {
		t.Fatalf("unexpected alternative path results: %+v", altResult.Bindings)
	}
}

func ptrSPARQLTerm(term RDFTerm) *RDFTerm {
	return &term
}
