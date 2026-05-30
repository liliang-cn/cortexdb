// Command kg_e2e is a runnable, fully-printed end-to-end walkthrough of the
// CortexDB RDF / Knowledge Graph stack through the public pkg/cortexdb facade.
//
// Unlike the unit tests, this prints the ACTUAL text and effects at every
// stage so you can see what each capability produces:
//
//  1. seed triples            -> write counts
//  2. RDFS inference          -> inferred triples + human-readable provenance
//  3. SPARQL                  -> SELECT tables, property paths, subquery, ASK
//  4. SHACL                   -> a real violation report, then a clean pass
//  5. Turtle / TriG export    -> the serialized text itself
//  6. round-trip into a new DB-> proof the serialization re-imports & queries
//  7. tool / MCP layer        -> the same query driven by a JSON tool call
//
// Run:  go run ./examples/kg_e2e
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

const (
	ex           = "https://example.com/"
	rdfType      = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
	rdfsSubClass = "http://www.w3.org/2000/01/rdf-schema#subClassOf"
	xsdInteger   = graph.XSDNamespace + "integer"
)

func main() {
	dbPath := "kg_e2e.db"
	_ = os.Remove(dbPath)
	defer func() { _ = os.Remove(dbPath) }()

	ctx := context.Background()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatal(err)
	}

	// Register the ex: prefix so exports come out compact and readable.
	if _, err := db.UpsertKnowledgeNamespace(ctx, cortexdb.KnowledgeGraphNamespaceUpsertRequest{
		Prefix: "ex", URI: ex,
	}); err != nil {
		log.Fatal(err)
	}

	stageSeed(ctx, db)
	stageInference(ctx, db)
	stageSPARQL(ctx, db)
	stageSHACL(ctx, db)
	exported := stageExport(ctx, db)

	if err := db.Close(); err != nil {
		log.Fatal(err)
	}
	stageRoundTrip(ctx, exported)
	stageToolLayer(ctx)
}

func iri(local string) cortexdb.KnowledgeGraphTerm { return graph.NewIRI(ex + local) }

func banner(title string) {
	fmt.Printf("\n\033[1;36m=== %s ===\033[0m\n", title)
}

// ----------------------------------------------------------------------------
// Stage 1: seed
// ----------------------------------------------------------------------------

func stageSeed(ctx context.Context, db *cortexdb.DB) {
	banner("1. SEED — write the org graph")

	seed := []cortexdb.KnowledgeGraphTriple{
		{Subject: iri("Manager"), Predicate: graph.NewIRI(rdfsSubClass), Object: iri("Employee")},
		{Subject: iri("Employee"), Predicate: graph.NewIRI(rdfsSubClass), Object: iri("Person")},
		{Subject: iri("alice"), Predicate: graph.NewIRI(rdfType), Object: iri("Manager")},
		{Subject: iri("alice"), Predicate: graph.NewIRI(ex + "age"), Object: graph.NewTypedLiteral("34", xsdInteger)},
		{Subject: iri("alice"), Predicate: graph.NewIRI(ex + "knows"), Object: iri("bob")},
		{Subject: iri("bob"), Predicate: graph.NewIRI(rdfType), Object: iri("Employee")},
		{Subject: iri("bob"), Predicate: graph.NewIRI(ex + "age"), Object: graph.NewTypedLiteral("29", xsdInteger)},
		{Subject: iri("bob"), Predicate: graph.NewIRI(ex + "knows"), Object: iri("carol")},
		{Subject: iri("carol"), Predicate: graph.NewIRI(rdfType), Object: iri("Person")},
	}
	resp, err := db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{Triples: seed})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("class hierarchy : Manager ⊑ Employee ⊑ Person\n")
	fmt.Printf("individuals     : alice(Manager) → knows → bob(Employee) → knows → carol(Person)\n")
	fmt.Printf("wrote %d explicit triples\n", resp.Count)
}

// ----------------------------------------------------------------------------
// Stage 2: RDFS inference + provenance
// ----------------------------------------------------------------------------

func stageInference(ctx context.Context, db *cortexdb.DB) {
	banner("2. RDFS INFERENCE — derive types up the class hierarchy")

	refresh, err := db.RefreshKnowledgeGraphInference(ctx, cortexdb.KnowledgeGraphInferenceRefreshRequest{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("explicit=%d  inferred=%d\n\n", refresh.Result.ExplicitCount, refresh.Result.InferredCount)

	// List every inferred rdf:type triple.
	found, err := db.FindKnowledgeGraph(ctx, cortexdb.KnowledgeGraphFindRequest{
		Pattern: cortexdb.KnowledgeGraphTriplePattern{
			Predicate: ptr(graph.NewIRI(rdfType)),
			Inferred:  ptrBool(true),
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("inferred type assertions (not stated, derived):")
	for _, tr := range found.Triples {
		fmt.Printf("  • %s  a  %s\n", short(tr.Subject), short(tr.Object))
	}

	// Explain ONE inferred triple in human terms.
	if len(found.Triples) > 0 {
		target := found.Triples[0]
		exp, err := db.ExplainKnowledgeGraphInference(ctx, cortexdb.KnowledgeGraphInferenceExplainRequest{
			TripleID: target.ID, Depth: 4,
		})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("\nwhy is  %s a %s ?\n", short(target.Subject), short(target.Object))
		fmt.Printf("  rule: %s\n", exp.Explanation.Rule)
		for _, step := range exp.Trace {
			indent := strings.Repeat("  ", step.Depth+1)
			tr := step.Explanation.Triple
			kind := "inferred"
			if step.Explanation.Explicit {
				kind = "stated"
			}
			fmt.Printf("%s└─ [%s] %s %s %s\n", indent, kind, short(tr.Subject), short(tr.Predicate), short(tr.Object))
		}
	}
}

// ----------------------------------------------------------------------------
// Stage 3: SPARQL
// ----------------------------------------------------------------------------

func stageSPARQL(ctx context.Context, db *cortexdb.DB) {
	banner("3. SPARQL — property paths, subquery, EXISTS, ASK")

	// (a) transitive reachability via property path + EXISTS filter
	fmt.Println("\n(a) who can alice reach via knows+ that is a Person WITH an age?")
	printSelect(ctx, db, `
PREFIX ex: <https://example.com/>
PREFIX rdf: <http://www.w3.org/1999/02/22-rdf-syntax-ns#>
SELECT ?reachable WHERE {
  ex:alice ex:knows+ ?reachable .
  ?reachable rdf:type ex:Person .
  FILTER EXISTS { ?reachable ex:age ?age . }
}
ORDER BY ?reachable`)

	// (b) aggregate via subquery
	fmt.Println("\n(b) how many people does each person know? (subquery + COUNT)")
	printSelect(ctx, db, `
PREFIX ex: <https://example.com/>
SELECT ?person ?known WHERE {
  { SELECT ?person (COUNT(?f) AS ?known) WHERE { ?person ex:knows ?f . } GROUP BY ?person }
}
ORDER BY ?person`)

	// (c) ASK with transitive path
	fmt.Println("\n(c) ASK: does alice transitively know carol?")
	ask, err := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
		Query: `PREFIX ex: <https://example.com/> ASK { ex:alice ex:knows+ ex:carol . }`,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  → %v\n", ask.Result.Boolean)
}

func printSelect(ctx context.Context, db *cortexdb.DB, query string) {
	res, err := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{Query: query})
	if err != nil {
		log.Fatal(err)
	}
	if len(res.Result.Bindings) == 0 {
		fmt.Println("  (no rows)")
		return
	}
	for _, row := range res.Result.Bindings {
		parts := make([]string, 0, len(res.Result.Vars))
		for _, v := range res.Result.Vars {
			parts = append(parts, fmt.Sprintf("%s=%s", v, short(row[v])))
		}
		fmt.Printf("  %s\n", strings.Join(parts, "  "))
	}
}

// ----------------------------------------------------------------------------
// Stage 4: SHACL
// ----------------------------------------------------------------------------

func stageSHACL(ctx context.Context, db *cortexdb.DB) {
	banner("4. SHACL — constraint validation (violate, then conform)")

	shapes := []cortexdb.KnowledgeGraphTriple{
		{Subject: iri("PersonShape"), Predicate: graph.NewIRI(rdfType), Object: graph.NewIRI(graph.SHACLNodeShape)},
		{Subject: iri("PersonShape"), Predicate: graph.NewIRI(graph.SHACLTargetClass), Object: iri("Person")},
		{Subject: iri("PersonShape"), Predicate: graph.NewIRI(graph.SHACLProperty), Object: iri("AgeShape")},
		{Subject: iri("AgeShape"), Predicate: graph.NewIRI(graph.SHACLPath), Object: iri("age")},
		{Subject: iri("AgeShape"), Predicate: graph.NewIRI(graph.SHACLMinCount), Object: graph.NewLiteral("1")},
		{Subject: iri("AgeShape"), Predicate: graph.NewIRI(graph.SHACLMinInclusive), Object: graph.NewLiteral("0")},
	}
	fmt.Println("shape: every Person must have ≥1 ex:age and age ≥ 0")

	// carol is an (explicit) Person with no age -> violation
	rep, err := db.ValidateKnowledgeGraphSHACL(ctx, cortexdb.KnowledgeGraphSHACLValidateRequest{Shapes: shapes})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nbefore fix: conforms=%v  violations=%d\n", rep.Report.Conforms, len(rep.Report.Results))
	for _, r := range rep.Report.Results {
		fmt.Printf("  ✗ %s  (path %s)  %s\n", short(r.FocusNode), short(r.Path), r.Message)
	}

	// give carol an age
	if _, err := db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{
		Triples: []cortexdb.KnowledgeGraphTriple{
			{Subject: iri("carol"), Predicate: graph.NewIRI(ex + "age"), Object: graph.NewTypedLiteral("41", xsdInteger)},
		},
	}); err != nil {
		log.Fatal(err)
	}
	rep2, err := db.ValidateKnowledgeGraphSHACL(ctx, cortexdb.KnowledgeGraphSHACLValidateRequest{Shapes: shapes})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nafter giving carol age=41: conforms=%v  violations=%d  ✓\n", rep2.Report.Conforms, len(rep2.Report.Results))
}

// ----------------------------------------------------------------------------
// Stage 5: export (show the serialized text)
// ----------------------------------------------------------------------------

func stageExport(ctx context.Context, db *cortexdb.DB) string {
	banner("5. EXPORT — serialize the graph (you can read it)")

	for _, format := range []string{cortexdb.KnowledgeGraphFormatTurtle, cortexdb.KnowledgeGraphFormatNTriples} {
		exp, err := db.ExportKnowledgeGraph(ctx, cortexdb.KnowledgeGraphExportRequest{Format: format})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("\n--- %s (first 12 lines) ---\n", strings.ToUpper(format))
		lines := strings.Split(strings.TrimSpace(exp.Content), "\n")
		for i, ln := range lines {
			if i >= 12 {
				fmt.Printf("... (%d more lines)\n", len(lines)-12)
				break
			}
			fmt.Println(ln)
		}
	}

	// return N-Triples for the round-trip stage
	exp, err := db.ExportKnowledgeGraph(ctx, cortexdb.KnowledgeGraphExportRequest{Format: cortexdb.KnowledgeGraphFormatNTriples})
	if err != nil {
		log.Fatal(err)
	}
	return exp.Content
}

// ----------------------------------------------------------------------------
// Stage 6: round-trip into a brand-new DB
// ----------------------------------------------------------------------------

func stageRoundTrip(ctx context.Context, ntriples string) {
	banner("6. ROUND-TRIP — import into a FRESH db and re-query")

	dbPath := "kg_e2e_roundtrip.db"
	_ = os.Remove(dbPath)
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	imp, err := db.ImportKnowledgeGraph(ctx, cortexdb.KnowledgeGraphImportRequest{
		Format:  cortexdb.KnowledgeGraphFormatNTriples,
		Content: ntriples,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("imported %d triples into a clean database\n", imp.Count)

	ask, err := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
		Query: `PREFIX ex: <https://example.com/> ASK { ex:alice ex:knows+ ex:carol . }`,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("same ASK (alice knows+ carol) on the re-imported graph → %v ✓\n", ask.Result.Boolean)
}

// ----------------------------------------------------------------------------
// Stage 7: tool / MCP layer — same query, driven by a JSON tool call
// ----------------------------------------------------------------------------

func stageToolLayer(ctx context.Context) {
	banner("7. TOOL / MCP LAYER — agent-callable JSON path")

	dbPath := "kg_e2e_tools.db"
	_ = os.Remove(dbPath)
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	tools := db.GraphRAGTools()

	// upsert via raw JSON, exactly as an LLM tool call would send it
	upsertPayload := []byte(`{
      "triples": [
        {"subject":{"kind":"iri","value":"https://example.com/x"},
         "predicate":{"kind":"iri","value":"https://example.com/knows"},
         "object":{"kind":"iri","value":"https://example.com/y"}}
      ]
    }`)
	if _, err := tools.Call(ctx, "knowledge_graph_upsert", upsertPayload); err != nil {
		log.Fatal(err)
	}
	fmt.Println(`tool call: knowledge_graph_upsert  {x knows y}  → ok`)

	queryPayload := []byte(`{"query":"PREFIX ex: <https://example.com/> SELECT ?o WHERE { ex:x ex:knows ?o . }"}`)
	out, err := tools.Call(ctx, "knowledge_graph_query", queryPayload)
	if err != nil {
		log.Fatal(err)
	}
	pretty, _ := json.MarshalIndent(out, "", "  ")
	fmt.Printf("tool call: knowledge_graph_query → JSON result:\n%s\n", string(pretty))
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

// short compacts a term for display: drops the example namespace, keeps the rest.
func short(t cortexdb.KnowledgeGraphTerm) string {
	v := t.Value
	v = strings.TrimPrefix(v, ex)
	v = strings.TrimPrefix(v, "http://www.w3.org/1999/02/22-rdf-syntax-ns#")
	v = strings.TrimPrefix(v, "http://www.w3.org/2000/01/rdf-schema#")
	return v
}

func ptr(t cortexdb.KnowledgeGraphTerm) *cortexdb.KnowledgeGraphTerm { return &t }
func ptrBool(b bool) *bool                                           { return &b }
