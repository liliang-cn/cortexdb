package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// e2e IRIs for the test domain.
const (
	e2eEx         = "https://example.com/"
	e2eRDFType    = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
	e2eSubClassOf = "http://www.w3.org/2000/01/rdf-schema#subClassOf"
	e2eXSDInteger = graph.XSDNamespace + "integer"
)

// TestKnowledgeGraphE2E walks the full RDF/KG lifecycle through the public
// pkg/cortexdb facade only: seed -> RDFS inference -> SPARQL (property paths,
// subquery, EXISTS, aggregates, ASK) -> SHACL (conform + violate) -> provenance
// -> export, reopen a fresh DB, import, and confirm the round-trip preserves
// both explicit data and queryability.
func TestKnowledgeGraphE2E(t *testing.T) {
	dbPath := fmt.Sprintf("test_kg_e2e_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath) }()

	ctx := context.Background()

	// ---- Stage 1: open and seed a small org domain ------------------------
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	iri := func(local string) KnowledgeGraphTerm { return graph.NewIRI(e2eEx + local) }
	seed := []KnowledgeGraphTriple{
		// class hierarchy: Manager ⊑ Employee ⊑ Person
		{Subject: iri("Manager"), Predicate: graph.NewIRI(e2eSubClassOf), Object: iri("Employee")},
		{Subject: iri("Employee"), Predicate: graph.NewIRI(e2eSubClassOf), Object: iri("Person")},
		// individuals
		{Subject: iri("alice"), Predicate: graph.NewIRI(e2eRDFType), Object: iri("Manager")},
		{Subject: iri("alice"), Predicate: graph.NewIRI(e2eEx + "age"), Object: graph.NewTypedLiteral("34", e2eXSDInteger)},
		{Subject: iri("alice"), Predicate: graph.NewIRI(e2eEx + "knows"), Object: iri("bob")},
		{Subject: iri("bob"), Predicate: graph.NewIRI(e2eRDFType), Object: iri("Employee")},
		{Subject: iri("bob"), Predicate: graph.NewIRI(e2eEx + "age"), Object: graph.NewTypedLiteral("29", e2eXSDInteger)},
		{Subject: iri("bob"), Predicate: graph.NewIRI(e2eEx + "knows"), Object: iri("carol")},
		{Subject: iri("carol"), Predicate: graph.NewIRI(e2eRDFType), Object: iri("Person")},
	}
	upsertResp, err := db.UpsertKnowledgeGraph(ctx, KnowledgeGraphUpsertRequest{Triples: seed})
	if err != nil {
		t.Fatalf("seed upsert: %v", err)
	}
	if upsertResp.Count != len(seed) {
		t.Fatalf("expected %d triples written, got %d", len(seed), upsertResp.Count)
	}

	// ---- Stage 2: RDFS inference -----------------------------------------
	// alice is a Manager; via Manager ⊑ Employee ⊑ Person she must be a Person.
	refresh, err := db.RefreshKnowledgeGraphInference(ctx, KnowledgeGraphInferenceRefreshRequest{})
	if err != nil {
		t.Fatalf("refresh inference: %v", err)
	}
	if refresh.Result.InferredCount == 0 {
		t.Fatalf("expected inferred triples, got %+v", refresh.Result)
	}

	// The inferred "alice rdf:type Person" must be findable and flagged inferred.
	inferred, err := db.FindKnowledgeGraph(ctx, KnowledgeGraphFindRequest{
		Pattern: KnowledgeGraphTriplePattern{
			Subject:   ptrKnowledgeTerm(iri("alice")),
			Predicate: ptrKnowledgeTerm(graph.NewIRI(e2eRDFType)),
			Object:    ptrKnowledgeTerm(iri("Person")),
			Inferred:  ptrBool(true),
		},
	})
	if err != nil {
		t.Fatalf("find inferred triple: %v", err)
	}
	if len(inferred.Triples) != 1 {
		t.Fatalf("expected alice⊑Person inferred once, got %d", len(inferred.Triples))
	}

	// ---- Stage 3: SPARQL — property path + subquery + EXISTS + aggregate --
	// alice ex:knows+ ?reachable should reach both bob and carol (transitive).
	selectResp, err := db.QueryKnowledgeGraph(ctx, KnowledgeGraphQueryRequest{
		Query: `
PREFIX ex: <https://example.com/>
PREFIX rdf: <http://www.w3.org/1999/02/22-rdf-syntax-ns#>

SELECT ?reachable WHERE {
  ex:alice ex:knows+ ?reachable .
  ?reachable rdf:type ex:Person .
  FILTER EXISTS { ?reachable ex:age ?age . }
}
ORDER BY ?reachable
`,
	})
	if err != nil {
		t.Fatalf("sparql property-path select: %v", err)
	}
	gotReach := bindingValues(selectResp.Result.Bindings, "reachable")
	// bob is inferred Person via Employee⊑Person; carol is an explicit Person.
	// carol has no ex:age, so EXISTS filters her out -> only bob qualifies.
	if want := []string{e2eEx + "bob"}; !equalStrings(gotReach, want) {
		t.Fatalf("property-path+EXISTS reachable mismatch: got %v want %v", gotReach, want)
	}

	// Aggregate via subquery: count how many people each subject knows.
	aggResp, err := db.QueryKnowledgeGraph(ctx, KnowledgeGraphQueryRequest{
		Query: `
PREFIX ex: <https://example.com/>
SELECT ?person ?c WHERE {
  { SELECT ?person (COUNT(?f) AS ?c) WHERE { ?person ex:knows ?f . } GROUP BY ?person }
}
ORDER BY ?person
`,
	})
	if err != nil {
		t.Fatalf("sparql subquery aggregate: %v", err)
	}
	if len(aggResp.Result.Bindings) != 2 { // alice and bob each know exactly one
		t.Fatalf("expected 2 aggregate rows, got %d (%+v)", len(aggResp.Result.Bindings), aggResp.Result.Bindings)
	}

	// ASK: does alice (transitively) know carol?
	askResp, err := db.QueryKnowledgeGraph(ctx, KnowledgeGraphQueryRequest{
		Query: `
PREFIX ex: <https://example.com/>
ASK { ex:alice ex:knows+ ex:carol . }
`,
	})
	if err != nil {
		t.Fatalf("sparql ask: %v", err)
	}
	if !askResp.Result.Boolean {
		t.Fatalf("expected ASK alice knows+ carol = true")
	}

	// ---- Stage 4: SHACL — conforming and violating ------------------------
	// Shape: every Person must have at least one ex:age >= 0.
	shapes := []KnowledgeGraphTriple{
		{Subject: iri("PersonShape"), Predicate: graph.NewIRI(e2eRDFType), Object: graph.NewIRI(graph.SHACLNodeShape)},
		{Subject: iri("PersonShape"), Predicate: graph.NewIRI(graph.SHACLTargetClass), Object: iri("Person")},
		{Subject: iri("PersonShape"), Predicate: graph.NewIRI(graph.SHACLProperty), Object: iri("AgeShape")},
		{Subject: iri("AgeShape"), Predicate: graph.NewIRI(graph.SHACLPath), Object: iri("age")},
		{Subject: iri("AgeShape"), Predicate: graph.NewIRI(graph.SHACLMinCount), Object: graph.NewLiteral("1")},
		{Subject: iri("AgeShape"), Predicate: graph.NewIRI(graph.SHACLMinInclusive), Object: graph.NewLiteral("0")},
	}
	// carol is a Person (explicit) with no ex:age -> minCount violation.
	report, err := db.ValidateKnowledgeGraphSHACL(ctx, KnowledgeGraphSHACLValidateRequest{Shapes: shapes})
	if err != nil {
		t.Fatalf("shacl validate (expect violation): %v", err)
	}
	if report.Report.Conforms {
		t.Fatalf("expected SHACL violation for carol (missing age), got conforms=true")
	}
	if !shaclMentions(report.Report, e2eEx+"carol") {
		t.Fatalf("expected violation focused on carol, got %+v", report.Report.Results)
	}

	// Fix carol, re-validate -> should now conform.
	if _, err := db.UpsertKnowledgeGraph(ctx, KnowledgeGraphUpsertRequest{
		Triples: []KnowledgeGraphTriple{
			{Subject: iri("carol"), Predicate: graph.NewIRI(e2eEx + "age"), Object: graph.NewTypedLiteral("41", e2eXSDInteger)},
		},
	}); err != nil {
		t.Fatalf("upsert carol age: %v", err)
	}
	report2, err := db.ValidateKnowledgeGraphSHACL(ctx, KnowledgeGraphSHACLValidateRequest{Shapes: shapes})
	if err != nil {
		t.Fatalf("shacl validate (expect conform): %v", err)
	}
	if !report2.Report.Conforms {
		t.Fatalf("expected SHACL conform after fixing carol, got %+v", report2.Report.Results)
	}

	// ---- Stage 5: provenance for the inferred triple ----------------------
	explain, err := db.ExplainKnowledgeGraphInference(ctx, KnowledgeGraphInferenceExplainRequest{
		TripleID: inferred.Triples[0].ID,
		Depth:    3,
	})
	if err != nil {
		t.Fatalf("explain inference: %v", err)
	}
	if explain.Explanation.Explicit {
		t.Fatalf("expected alice⊑Person to be inferred, not explicit")
	}
	if explain.Explanation.Rule == "" || len(explain.Trace) < 2 {
		t.Fatalf("expected a multi-step inference trace, got rule=%q trace=%d", explain.Explanation.Rule, len(explain.Trace))
	}

	// ---- Stage 6: export, close, reopen fresh DB, import, re-query --------
	export, err := db.ExportKnowledgeGraph(ctx, KnowledgeGraphExportRequest{Format: KnowledgeGraphFormatNTriples})
	if err != nil {
		t.Fatalf("export n-triples: %v", err)
	}
	if strings.TrimSpace(export.Content) == "" {
		t.Fatalf("export produced empty content")
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close source db: %v", err)
	}

	// Fresh DB, import the serialized graph.
	dbPath2 := fmt.Sprintf("test_kg_e2e_roundtrip_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath2) }()
	db2, err := Open(DefaultConfig(dbPath2))
	if err != nil {
		t.Fatalf("open roundtrip db: %v", err)
	}
	defer func() { _ = db2.Close() }()

	imp, err := db2.ImportKnowledgeGraph(ctx, KnowledgeGraphImportRequest{
		Format:  KnowledgeGraphFormatNTriples,
		Content: export.Content,
	})
	if err != nil {
		t.Fatalf("import n-triples: %v", err)
	}
	if imp.Count == 0 {
		t.Fatalf("import wrote 0 triples")
	}

	// The round-tripped graph must answer the same ASK.
	askResp2, err := db2.QueryKnowledgeGraph(ctx, KnowledgeGraphQueryRequest{
		Query: `
PREFIX ex: <https://example.com/>
ASK { ex:alice ex:knows+ ex:carol . }
`,
	})
	if err != nil {
		t.Fatalf("roundtrip ask: %v", err)
	}
	if !askResp2.Result.Boolean {
		t.Fatalf("expected roundtrip ASK alice knows+ carol = true")
	}
}

// bindingValues extracts the .Value of one variable across all SPARQL rows.
func bindingValues(rows []map[string]KnowledgeGraphTerm, varName string) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if term, ok := row[varName]; ok {
			out = append(out, term.Value)
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestGraphRAGToolSchemasNoNullRequired guards against tool InputSchemas that
// serialize "required": null. Strict function-calling validators (OpenAI /
// DeepSeek) reject null for the required array, which breaks agents that expose
// the CortexDB toolbox (e.g. via AgentGo).
func TestGraphRAGToolSchemasNoNullRequired(t *testing.T) {
	dbPath := fmt.Sprintf("test_kg_schema_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, def := range db.GraphRAGTools().Definitions() {
		raw, err := json.Marshal(def.InputSchema)
		if err != nil {
			t.Fatalf("marshal schema for %s: %v", def.Name, err)
		}
		if strings.Contains(string(raw), `"required":null`) {
			t.Fatalf("tool %q emits \"required\": null (rejected by strict validators): %s", def.Name, raw)
		}
	}
}

// shaclMentions reports whether any validation result focuses on the given node.
func shaclMentions(report KnowledgeGraphSHACLReport, focusValue string) bool {
	for _, r := range report.Results {
		if r.FocusNode.Value == focusValue {
			return true
		}
	}
	return false
}

// TestKnowledgeGraphTurtleRoundTrip exercises the Turtle serialization path,
// which on import goes through the external 0x51-dev/rdf parser (not the
// internal line parser used by N-Triples). It seeds a graph with a prefix, a
// typed literal, and a language literal, exports Turtle, imports the text into
// a fresh DB, and confirms every term type survives via SPARQL + Find.
func TestKnowledgeGraphTurtleRoundTrip(t *testing.T) {
	dbPath := fmt.Sprintf("test_kg_turtle_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath) }()
	ctx := context.Background()

	src, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open src db: %v", err)
	}
	if _, err := src.UpsertKnowledgeNamespace(ctx, KnowledgeGraphNamespaceUpsertRequest{Prefix: "ex", URI: e2eEx}); err != nil {
		t.Fatalf("upsert namespace: %v", err)
	}
	tIRI := func(local string) KnowledgeGraphTerm { return graph.NewIRI(e2eEx + local) }
	seed := []KnowledgeGraphTriple{
		{Subject: tIRI("alice"), Predicate: graph.NewIRI(e2eRDFType), Object: tIRI("Person")},
		{Subject: tIRI("alice"), Predicate: graph.NewIRI(e2eEx + "age"), Object: graph.NewTypedLiteral("34", e2eXSDInteger)},
		{Subject: tIRI("alice"), Predicate: graph.NewIRI(e2eEx + "label"), Object: graph.NewLangLiteral("Alice", "en")},
		{Subject: tIRI("alice"), Predicate: graph.NewIRI(e2eEx + "knows"), Object: tIRI("bob")},
	}
	if _, err := src.UpsertKnowledgeGraph(ctx, KnowledgeGraphUpsertRequest{Triples: seed}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	export, err := src.ExportKnowledgeGraph(ctx, KnowledgeGraphExportRequest{Format: KnowledgeGraphFormatTurtle})
	if err != nil {
		t.Fatalf("export turtle: %v", err)
	}
	if !strings.Contains(export.Content, "@prefix") {
		t.Fatalf("expected Turtle prefixes in export, got:\n%s", export.Content)
	}
	if err := src.Close(); err != nil {
		t.Fatalf("close src: %v", err)
	}

	// Import the Turtle text into a clean DB via the external parser.
	dbPath2 := fmt.Sprintf("test_kg_turtle_rt_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath2) }()
	dst, err := Open(DefaultConfig(dbPath2))
	if err != nil {
		t.Fatalf("open dst db: %v", err)
	}
	defer func() { _ = dst.Close() }()

	imp, err := dst.ImportKnowledgeGraph(ctx, KnowledgeGraphImportRequest{
		Format:  KnowledgeGraphFormatTurtle,
		Content: export.Content,
	})
	if err != nil {
		t.Fatalf("import turtle: %v", err)
	}
	if imp.Count != len(seed) {
		t.Fatalf("turtle round-trip count mismatch: imported %d want %d", imp.Count, len(seed))
	}

	// Typed literal must survive: SELECT alice's age.
	ageResp, err := dst.QueryKnowledgeGraph(ctx, KnowledgeGraphQueryRequest{
		Query: `PREFIX ex: <https://example.com/> SELECT ?age WHERE { ex:alice ex:age ?age . }`,
	})
	if err != nil {
		t.Fatalf("query age: %v", err)
	}
	if got := bindingValues(ageResp.Result.Bindings, "age"); !equalStrings(got, []string{"34"}) {
		t.Fatalf("typed literal lost in turtle round-trip: got %v", got)
	}

	// Language literal must survive with its language tag.
	labelResp, err := dst.FindKnowledgeGraph(ctx, KnowledgeGraphFindRequest{
		Pattern: KnowledgeGraphTriplePattern{
			Subject:   ptrKnowledgeTerm(tIRI("alice")),
			Predicate: ptrKnowledgeTerm(graph.NewIRI(e2eEx + "label")),
		},
	})
	if err != nil {
		t.Fatalf("find label: %v", err)
	}
	if len(labelResp.Triples) != 1 {
		t.Fatalf("expected one label triple, got %d", len(labelResp.Triples))
	}
	if lang := labelResp.Triples[0].Object.Language; lang != "en" {
		t.Fatalf("language tag lost in turtle round-trip: got %q", lang)
	}
}

// TestKnowledgeGraphTriGRoundTrip exercises the TriG / N-Quads named-graph
// path. It seeds quads in two named graphs, exports TriG, re-imports into a
// fresh DB, and confirms the graph names are preserved (a triple lands in the
// correct named graph, not the default graph).
func TestKnowledgeGraphTriGRoundTrip(t *testing.T) {
	dbPath := fmt.Sprintf("test_kg_trig_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath) }()
	ctx := context.Background()

	src, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open src db: %v", err)
	}
	tIRI := func(local string) KnowledgeGraphTerm { return graph.NewIRI(e2eEx + local) }
	gPublic := tIRI("graph/public")
	gPrivate := tIRI("graph/private")
	seed := []KnowledgeGraphTriple{
		{Subject: tIRI("alice"), Predicate: graph.NewIRI(e2eEx + "knows"), Object: tIRI("bob"), Graph: &gPublic},
		{Subject: tIRI("alice"), Predicate: graph.NewIRI(e2eEx + "salary"), Object: graph.NewTypedLiteral("100000", e2eXSDInteger), Graph: &gPrivate},
	}
	if _, err := src.UpsertKnowledgeGraph(ctx, KnowledgeGraphUpsertRequest{Triples: seed}); err != nil {
		t.Fatalf("seed quads: %v", err)
	}
	export, err := src.ExportKnowledgeGraph(ctx, KnowledgeGraphExportRequest{Format: KnowledgeGraphFormatTriG})
	if err != nil {
		t.Fatalf("export trig: %v", err)
	}
	if strings.TrimSpace(export.Content) == "" {
		t.Fatalf("empty trig export")
	}
	if err := src.Close(); err != nil {
		t.Fatalf("close src: %v", err)
	}

	dbPath2 := fmt.Sprintf("test_kg_trig_rt_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath2) }()
	dst, err := Open(DefaultConfig(dbPath2))
	if err != nil {
		t.Fatalf("open dst db: %v", err)
	}
	defer func() { _ = dst.Close() }()

	imp, err := dst.ImportKnowledgeGraph(ctx, KnowledgeGraphImportRequest{
		Format:  KnowledgeGraphFormatTriG,
		Content: export.Content,
	})
	if err != nil {
		t.Fatalf("import trig: %v", err)
	}
	if imp.Count != len(seed) {
		t.Fatalf("trig round-trip count mismatch: imported %d want %d", imp.Count, len(seed))
	}

	// The private salary quad must be confined to ex:graph/private.
	privateMatch, err := dst.FindKnowledgeGraph(ctx, KnowledgeGraphFindRequest{
		Pattern: KnowledgeGraphTriplePattern{
			Subject:   ptrKnowledgeTerm(tIRI("alice")),
			Predicate: ptrKnowledgeTerm(graph.NewIRI(e2eEx + "salary")),
			Graph:     ptrKnowledgeTerm(gPrivate),
		},
	})
	if err != nil {
		t.Fatalf("find in private graph: %v", err)
	}
	if len(privateMatch.Triples) != 1 {
		t.Fatalf("expected salary quad in ex:graph/private, got %d", len(privateMatch.Triples))
	}

	// And it must NOT appear in the public graph.
	wrongGraph, err := dst.FindKnowledgeGraph(ctx, KnowledgeGraphFindRequest{
		Pattern: KnowledgeGraphTriplePattern{
			Predicate: ptrKnowledgeTerm(graph.NewIRI(e2eEx + "salary")),
			Graph:     ptrKnowledgeTerm(gPublic),
		},
	})
	if err != nil {
		t.Fatalf("find in public graph: %v", err)
	}
	if len(wrongGraph.Triples) != 0 {
		t.Fatalf("salary quad leaked into ex:graph/public: got %d", len(wrongGraph.Triples))
	}
}
