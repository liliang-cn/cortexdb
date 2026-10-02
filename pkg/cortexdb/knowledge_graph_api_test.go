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

func TestKnowledgeGraphAPI(t *testing.T) {
	dbPath := fmt.Sprintf("test_knowledge_graph_api_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if _, err := db.UpsertKnowledgeNamespace(ctx, KnowledgeGraphNamespaceUpsertRequest{
		Prefix: "ex",
		URI:    "https://example.com/",
	}); err != nil {
		t.Fatalf("upsert namespace: %v", err)
	}

	upsertResp, err := db.UpsertKnowledgeGraph(ctx, KnowledgeGraphUpsertRequest{
		Triples: []KnowledgeGraphTriple{
			{
				Subject:   graph.NewIRI("ex:alice"),
				Predicate: graph.NewIRI("rdf:type"),
				Object:    graph.NewIRI("schema:Person"),
			},
			{
				Subject:   graph.NewIRI("ex:alice"),
				Predicate: graph.NewIRI("schema:name"),
				Object:    graph.NewLiteral("Alice"),
			},
		},
	})
	if err != nil {
		t.Fatalf("upsert knowledge graph: %v", err)
	}
	if upsertResp.Count != 2 {
		t.Fatalf("expected 2 triple IDs, got %+v", upsertResp)
	}

	findResp, err := db.FindKnowledgeGraph(ctx, KnowledgeGraphFindRequest{
		Pattern: KnowledgeGraphTriplePattern{
			Subject: ptrKnowledgeTerm(graph.NewIRI("ex:alice")),
		},
	})
	if err != nil {
		t.Fatalf("find knowledge graph: %v", err)
	}
	if len(findResp.Triples) != 2 {
		t.Fatalf("expected 2 triples, got %d", len(findResp.Triples))
	}

	exportResp, err := db.ExportKnowledgeGraph(ctx, KnowledgeGraphExportRequest{
		Format: KnowledgeGraphFormatNQuads,
	})
	if err != nil {
		t.Fatalf("export knowledge graph: %v", err)
	}
	if !strings.Contains(exportResp.Content, "<https://example.com/alice>") {
		t.Fatalf("expected exported content to contain Alice, got %q", exportResp.Content)
	}

	deleteResp, err := db.DeleteKnowledgeGraph(ctx, KnowledgeGraphDeleteRequest{
		TripleIDs: []string{upsertResp.TripleIDs[1]},
	})
	if err != nil {
		t.Fatalf("delete knowledge graph: %v", err)
	}
	if deleteResp.Deleted != 1 {
		t.Fatalf("expected one deleted triple, got %+v", deleteResp)
	}

	importResp, err := db.ImportKnowledgeGraph(ctx, KnowledgeGraphImportRequest{
		Format: KnowledgeGraphFormatNTriples,
		Content: `<https://example.com/bob> <https://schema.org/name> "Bob" .
`,
	})
	if err != nil {
		t.Fatalf("import knowledge graph: %v", err)
	}
	if importResp.Count != 1 {
		t.Fatalf("expected one imported triple, got %+v", importResp)
	}

	namespacesResp, err := db.ListKnowledgeNamespaces(ctx)
	if err != nil {
		t.Fatalf("list knowledge namespaces: %v", err)
	}
	if len(namespacesResp.Namespaces) == 0 {
		t.Fatal("expected namespaces to be available")
	}

	trigResp, err := db.ExportKnowledgeGraph(ctx, KnowledgeGraphExportRequest{
		Format: KnowledgeGraphFormatTriG,
	})
	if err != nil {
		t.Fatalf("export knowledge graph trig: %v", err)
	}
	if trigResp.Content == "" {
		t.Fatal("expected trig export content")
	}

	queryResp, err := db.QueryKnowledgeGraph(ctx, KnowledgeGraphQueryRequest{
		Query: `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

SELECT ?name WHERE {
	<https://example.com/bob> schema:name ?name .
}
`,
	})
	if err != nil {
		t.Fatalf("query knowledge graph: %v", err)
	}
	if queryResp.Result.Count != 1 {
		t.Fatalf("expected one SPARQL row, got %+v", queryResp.Result)
	}
}

func TestKnowledgeGraphToolboxCall(t *testing.T) {
	dbPath := fmt.Sprintf("test_knowledge_graph_toolbox_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	toolbox := db.GraphRAGTools()
	payload := []byte(`{"triples":[{"subject":{"kind":"iri","value":"https://example.com/alice"},"predicate":{"kind":"iri","value":"https://schema.org/name"},"object":{"kind":"literal","value":"Alice"}}]}`)
	resp, err := toolbox.Call(ctx, "knowledge_graph_upsert", payload)
	if err != nil {
		t.Fatalf("toolbox call: %v", err)
	}
	if resp == nil {
		t.Fatal("expected toolbox response")
	}

	queryPayload := []byte(`{"query":"PREFIX schema: <https://schema.org/> SELECT ?name WHERE { <https://example.com/alice> schema:name ?name . }"}`)
	queryResp, err := toolbox.Call(ctx, "knowledge_graph_query", queryPayload)
	if err != nil {
		t.Fatalf("toolbox sparql call: %v", err)
	}
	if queryResp == nil {
		t.Fatal("expected toolbox sparql response")
	}
}

func TestKnowledgeGraphInferenceAPI(t *testing.T) {
	dbPath := fmt.Sprintf("test_knowledge_graph_inference_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	_, err = db.UpsertKnowledgeGraph(ctx, KnowledgeGraphUpsertRequest{
		Triples: []KnowledgeGraphTriple{
			{Subject: graph.NewIRI("https://example.com/Manager"), Predicate: graph.NewIRI("http://www.w3.org/2000/01/rdf-schema#subClassOf"), Object: graph.NewIRI("https://example.com/Person")},
			{Subject: graph.NewIRI("https://example.com/alice"), Predicate: graph.NewIRI("http://www.w3.org/1999/02/22-rdf-syntax-ns#type"), Object: graph.NewIRI("https://example.com/Manager")},
		},
	})
	if err != nil {
		t.Fatalf("upsert graph for inference: %v", err)
	}

	refreshResp, err := db.RefreshKnowledgeGraphInference(ctx, KnowledgeGraphInferenceRefreshRequest{})
	if err != nil {
		t.Fatalf("refresh inference: %v", err)
	}
	if refreshResp.Result.InferredCount == 0 {
		t.Fatalf("expected inferred triples, got %+v", refreshResp)
	}

	findResp, err := db.FindKnowledgeGraph(ctx, KnowledgeGraphFindRequest{
		Pattern: KnowledgeGraphTriplePattern{
			Subject:   ptrKnowledgeTerm(graph.NewIRI("https://example.com/alice")),
			Predicate: ptrKnowledgeTerm(graph.NewIRI("http://www.w3.org/1999/02/22-rdf-syntax-ns#type")),
			Object:    ptrKnowledgeTerm(graph.NewIRI("https://example.com/Person")),
			Inferred:  ptrBool(true),
		},
	})
	if err != nil {
		t.Fatalf("find inferred triple: %v", err)
	}
	if len(findResp.Triples) != 1 {
		t.Fatalf("expected one inferred triple, got %+v", findResp)
	}

	explainResp, err := db.ExplainKnowledgeGraphInference(ctx, KnowledgeGraphInferenceExplainRequest{
		TripleID: findResp.Triples[0].ID,
		Depth:    2,
	})
	if err != nil {
		t.Fatalf("explain inferred triple: %v", err)
	}
	if explainResp.Explanation.Explicit {
		t.Fatalf("expected inferred explanation, got %+v", explainResp)
	}
	if explainResp.Explanation.Rule == "" {
		t.Fatalf("expected rule, got %+v", explainResp)
	}
	if len(explainResp.Trace) < 2 {
		t.Fatalf("expected recursive inference trace, got %+v", explainResp)
	}

	summaryResp, err := db.SummarizeKnowledgeGraphInference(ctx, KnowledgeGraphInferenceSummaryRequest{})
	if err != nil {
		t.Fatalf("summarize inference: %v", err)
	}
	if summaryResp.Result.InferredCount == 0 || len(summaryResp.Result.Rules) == 0 {
		t.Fatalf("expected inference summary, got %+v", summaryResp)
	}

	matchResp, err := db.ExplainKnowledgeGraphInferenceMatch(ctx, KnowledgeGraphInferenceExplainMatchRequest{
		Pattern: KnowledgeGraphTriplePattern{
			Subject:   ptrKnowledgeTerm(graph.NewIRI("https://example.com/alice")),
			Predicate: ptrKnowledgeTerm(graph.NewIRI("http://www.w3.org/1999/02/22-rdf-syntax-ns#type")),
			Object:    ptrKnowledgeTerm(graph.NewIRI("https://example.com/Person")),
			Inferred:  ptrBool(true),
		},
		Depth: 1,
	})
	if err != nil {
		t.Fatalf("explain inference match: %v", err)
	}
	if len(matchResp.Matches) != 1 {
		t.Fatalf("expected one inference match explanation, got %+v", matchResp)
	}
}

func TestKnowledgeGraphInferenceIncrementalAPI(t *testing.T) {
	dbPath := fmt.Sprintf("test_knowledge_graph_inference_incremental_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	_, err = db.UpsertKnowledgeGraph(ctx, KnowledgeGraphUpsertRequest{
		Triples: []KnowledgeGraphTriple{
			{Subject: graph.NewIRI("https://example.com/Manager"), Predicate: graph.NewIRI("http://www.w3.org/2000/01/rdf-schema#subClassOf"), Object: graph.NewIRI("https://example.com/Employee")},
			{Subject: graph.NewIRI("https://example.com/alice"), Predicate: graph.NewIRI("http://www.w3.org/1999/02/22-rdf-syntax-ns#type"), Object: graph.NewIRI("https://example.com/Manager")},
		},
	})
	if err != nil {
		t.Fatalf("seed graph for incremental inference: %v", err)
	}
	if _, err := db.RefreshKnowledgeGraphInference(ctx, KnowledgeGraphInferenceRefreshRequest{}); err != nil {
		t.Fatalf("initial full refresh: %v", err)
	}

	seed := KnowledgeGraphTriple{
		Subject:   graph.NewIRI("https://example.com/Employee"),
		Predicate: graph.NewIRI("http://www.w3.org/2000/01/rdf-schema#subClassOf"),
		Object:    graph.NewIRI("https://example.com/Person"),
	}
	if _, err := db.UpsertKnowledgeGraph(ctx, KnowledgeGraphUpsertRequest{Triples: []KnowledgeGraphTriple{seed}}); err != nil {
		t.Fatalf("upsert incremental seed triple: %v", err)
	}

	refreshResp, err := db.RefreshKnowledgeGraphInference(ctx, KnowledgeGraphInferenceRefreshRequest{
		Mode:    KnowledgeGraphInferenceRefreshModeIncremental,
		Triples: []KnowledgeGraphTriple{seed},
	})
	if err != nil {
		t.Fatalf("incremental refresh: %v", err)
	}
	if !refreshResp.Result.Incremental || refreshResp.Result.AffectedExplicitCount == 0 {
		t.Fatalf("expected incremental refresh metadata, got %+v", refreshResp)
	}
}

func TestKnowledgeGraphSHACLValidateAPI(t *testing.T) {
	dbPath := fmt.Sprintf("test_knowledge_graph_shacl_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	_, err = db.UpsertKnowledgeGraph(ctx, KnowledgeGraphUpsertRequest{
		Triples: []KnowledgeGraphTriple{
			{Subject: graph.NewIRI("https://example.com/alice"), Predicate: graph.NewIRI(graph.RDFType), Object: graph.NewIRI("https://example.com/Person")},
			{Subject: graph.NewIRI("https://example.com/alice"), Predicate: graph.NewIRI("https://example.com/age"), Object: graph.NewTypedLiteral("-1", graph.XSDNamespace+"integer")},
		},
	})
	if err != nil {
		t.Fatalf("seed graph for shacl: %v", err)
	}

	resp, err := db.ValidateKnowledgeGraphSHACL(ctx, KnowledgeGraphSHACLValidateRequest{
		Shapes: []KnowledgeGraphTriple{
			{Subject: graph.NewIRI("https://example.com/PersonShape"), Predicate: graph.NewIRI(graph.RDFType), Object: graph.NewIRI(graph.SHACLNodeShape)},
			{Subject: graph.NewIRI("https://example.com/PersonShape"), Predicate: graph.NewIRI(graph.SHACLTargetClass), Object: graph.NewIRI("https://example.com/Person")},
			{Subject: graph.NewIRI("https://example.com/PersonShape"), Predicate: graph.NewIRI(graph.SHACLProperty), Object: graph.NewIRI("https://example.com/AgeShape")},
			{Subject: graph.NewIRI("https://example.com/AgeShape"), Predicate: graph.NewIRI(graph.SHACLPath), Object: graph.NewIRI("https://example.com/age")},
			{Subject: graph.NewIRI("https://example.com/AgeShape"), Predicate: graph.NewIRI(graph.SHACLMinInclusive), Object: graph.NewLiteral("0")},
		},
	})
	if err != nil {
		t.Fatalf("validate shacl: %v", err)
	}
	if resp.Report.Conforms {
		t.Fatalf("expected SHACL violation, got %+v", resp.Report)
	}
	if len(resp.Report.Results) != 1 {
		t.Fatalf("expected one SHACL result, got %+v", resp.Report.Results)
	}
}

func TestKnowledgeGraphSHACLAdvancedValidateAPI(t *testing.T) {
	dbPath := fmt.Sprintf("test_knowledge_graph_shacl_advanced_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	_, err = db.UpsertKnowledgeGraph(ctx, KnowledgeGraphUpsertRequest{
		Triples: []KnowledgeGraphTriple{
			{Subject: graph.NewIRI("https://example.com/boss1"), Predicate: graph.NewIRI(graph.RDFType), Object: graph.NewIRI("https://example.com/Employee")},
			{Subject: graph.NewIRI("https://example.com/alice"), Predicate: graph.NewIRI(graph.RDFType), Object: graph.NewIRI("https://example.com/Person")},
			{Subject: graph.NewIRI("https://example.com/alice"), Predicate: graph.NewIRI("https://example.com/manager"), Object: graph.NewIRI("https://example.com/outsider")},
			{Subject: graph.NewIRI("https://example.com/alice"), Predicate: graph.NewIRI("https://example.com/homepage"), Object: graph.NewLiteral("https://example.com/alice")},
			{Subject: graph.NewIRI("https://example.com/alice"), Predicate: graph.NewIRI("https://example.com/status"), Object: graph.NewLiteral("blocked")},
		},
	})
	if err != nil {
		t.Fatalf("seed graph for advanced shacl: %v", err)
	}

	resp, err := db.ValidateKnowledgeGraphSHACL(ctx, KnowledgeGraphSHACLValidateRequest{
		Shapes: []KnowledgeGraphTriple{
			{Subject: graph.NewIRI("https://example.com/PersonShape"), Predicate: graph.NewIRI(graph.RDFType), Object: graph.NewIRI(graph.SHACLNodeShape)},
			{Subject: graph.NewIRI("https://example.com/PersonShape"), Predicate: graph.NewIRI(graph.SHACLTargetClass), Object: graph.NewIRI("https://example.com/Person")},
			{Subject: graph.NewIRI("https://example.com/PersonShape"), Predicate: graph.NewIRI(graph.SHACLProperty), Object: graph.NewIRI("https://example.com/ManagerShape")},
			{Subject: graph.NewIRI("https://example.com/PersonShape"), Predicate: graph.NewIRI(graph.SHACLProperty), Object: graph.NewIRI("https://example.com/HomepageShape")},
			{Subject: graph.NewIRI("https://example.com/PersonShape"), Predicate: graph.NewIRI(graph.SHACLProperty), Object: graph.NewIRI("https://example.com/StatusShape")},

			{Subject: graph.NewIRI("https://example.com/ManagerShape"), Predicate: graph.NewIRI(graph.SHACLPath), Object: graph.NewIRI("https://example.com/manager")},
			{Subject: graph.NewIRI("https://example.com/ManagerShape"), Predicate: graph.NewIRI(graph.SHACLClass), Object: graph.NewIRI("https://example.com/Employee")},

			{Subject: graph.NewIRI("https://example.com/HomepageShape"), Predicate: graph.NewIRI(graph.SHACLPath), Object: graph.NewIRI("https://example.com/homepage")},
			{Subject: graph.NewIRI("https://example.com/HomepageShape"), Predicate: graph.NewIRI(graph.SHACLNodeKind), Object: graph.NewIRI(graph.SHACLIRI)},

			{Subject: graph.NewIRI("https://example.com/StatusShape"), Predicate: graph.NewIRI(graph.SHACLPath), Object: graph.NewIRI("https://example.com/status")},
			{Subject: graph.NewIRI("https://example.com/StatusShape"), Predicate: graph.NewIRI(graph.SHACLIn), Object: graph.NewLiteral("active")},
			{Subject: graph.NewIRI("https://example.com/StatusShape"), Predicate: graph.NewIRI(graph.SHACLIn), Object: graph.NewLiteral("pending")},
		},
	})
	if err != nil {
		t.Fatalf("validate advanced shacl: %v", err)
	}
	if resp.Report.Conforms {
		t.Fatalf("expected advanced SHACL violations, got %+v", resp.Report)
	}
	if len(resp.Report.Results) != 3 {
		t.Fatalf("expected three advanced SHACL violations, got %+v", resp.Report.Results)
	}
}

func ptrKnowledgeTerm(term KnowledgeGraphTerm) *KnowledgeGraphTerm {
	return &term
}

func ptrBool(value bool) *bool {
	return &value
}

// A delete reports what it removed, not how many things it was asked to
// remove. The count is what a caller — often a model — reads to decide that a
// fact is gone, so a triple that was never stored must add nothing to it.
func TestADeleteReportsOnlyTheTriplesItRemoved(t *testing.T) {
	dbPath := fmt.Sprintf("test_knowledge_graph_delete_count_%d.db", testname.Nano())
	t.Cleanup(func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	})
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	stored := KnowledgeGraphTriple{
		Subject:   graph.NewIRI("https://example.com/alice"),
		Predicate: graph.NewIRI("https://schema.org/name"),
		Object:    graph.NewLiteral("Alice"),
	}
	neverStored := KnowledgeGraphTriple{
		Subject:   graph.NewIRI("https://example.com/nobody"),
		Predicate: graph.NewIRI("https://schema.org/name"),
		Object:    graph.NewLiteral("Nobody"),
	}
	if _, err := db.UpsertKnowledgeGraph(ctx, KnowledgeGraphUpsertRequest{Triples: []KnowledgeGraphTriple{stored}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	resp, err := db.DeleteKnowledgeGraph(ctx, KnowledgeGraphDeleteRequest{
		Triples: []KnowledgeGraphTriple{stored, neverStored, stored},
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if resp.Deleted != 1 {
		t.Fatalf("deleted = %d, want 1: one of the three was stored, and it can only be removed once", resp.Deleted)
	}
}

// JSON-LD arrives the way a model sends it: through the tool, spelled either
// way, naming schema.org's context by URL. That context is answered from
// memory, so the facts land and SPARQL can read them back.
func TestJSONLDImportsThroughTheToolAndAnswersSPARQL(t *testing.T) {
	dbPath := fmt.Sprintf("test_knowledge_graph_jsonld_%d.db", testname.Nano())
	t.Cleanup(func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	})
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	tools := db.GraphRAGTools()

	doc := `{"@context":"https://schema.org/","@id":"https://example.com/ada","@type":"Person","name":"Ada Lovelace","worksFor":{"@id":"https://example.com/engine","@type":"Organization","name":"Analytical Engine"}}`
	args, _ := json.Marshal(map[string]string{"format": "json-ld", "content": doc})
	raw, err := tools.Call(ctx, "knowledge_graph_import", args)
	if err != nil {
		t.Fatalf("import through the tool: %v", err)
	}
	imported, ok := raw.(*KnowledgeGraphImportResponse)
	if !ok {
		t.Fatalf("dispatch returned %T", raw)
	}
	if imported.Format != KnowledgeGraphFormatJSONLD || imported.Count != 5 {
		t.Fatalf("import = %+v, want format jsonld and 5 triples (2 types, 2 names, 1 worksFor)", imported)
	}

	res, err := db.QueryKnowledgeGraph(ctx, KnowledgeGraphQueryRequest{Query: `
		SELECT ?org WHERE { <https://example.com/ada> schema:worksFor ?o . ?o schema:name ?org }`})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(res.Result.Bindings) != 1 || res.Result.Bindings[0]["org"].Value != "Analytical Engine" {
		t.Fatalf("bindings = %+v, want the one organization Ada works for", res.Result.Bindings)
	}

	remote, _ := json.Marshal(map[string]string{"format": "jsonld", "content": `{"@context":"https://example.org/ctx.jsonld","name":"x"}`})
	if _, err := tools.Call(ctx, "knowledge_graph_import", remote); err == nil || !strings.Contains(err.Error(), "https://example.org/ctx.jsonld") {
		t.Fatalf("a remote context must be refused by name, got %v", err)
	}
}

// A contradiction found by inference reaches the model through the tool, as
// JSON, with the ids of the statements that conflict.
func TestTheInferenceToolReportsContradictions(t *testing.T) {
	dbPath := fmt.Sprintf("test_knowledge_graph_inconsistency_%d.db", testname.Nano())
	t.Cleanup(func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	})
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	if _, err := db.QueryKnowledgeGraph(ctx, KnowledgeGraphQueryRequest{Query: `INSERT DATA {
		<https://example.com/Host> owl:disjointWith <https://example.com/Project> .
		<https://example.com/x> a <https://example.com/Host> .
		<https://example.com/x> a <https://example.com/Project> }`}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	raw, err := db.GraphRAGTools().Call(ctx, "knowledge_graph_infer_refresh", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("refresh through the tool: %v", err)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var decoded struct {
		Result struct {
			Inconsistencies []struct {
				Rule    string `json:"rule"`
				Triples []struct {
					ID string `json:"id"`
				} `json:"triples"`
			} `json:"inconsistencies"`
			InconsistencyCount int `json:"inconsistency_count"`
		} `json:"result"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode %s: %v", encoded, err)
	}
	got := decoded.Result
	if got.InconsistencyCount != 1 || len(got.Inconsistencies) != 1 || got.Inconsistencies[0].Rule != "cax-dw" || len(got.Inconsistencies[0].Triples) != 3 {
		t.Fatalf("tool output %s, want one cax-dw with three triples", encoded)
	}
	for _, triple := range got.Inconsistencies[0].Triples {
		if triple.ID == "" {
			t.Errorf("a conflicting triple has no id: %s", encoded)
		}
	}
}

// A model sets the sameAs cap through the tool and reads the report back from
// the same call; both have to survive the JSON round trip.
func TestTheInferenceToolTakesTheSameAsCapAndReportsWhatItSkipped(t *testing.T) {
	dbPath := fmt.Sprintf("test_knowledge_graph_sameas_cap_%d.db", testname.Nano())
	t.Cleanup(func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	})
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	if _, err := db.QueryKnowledgeGraph(ctx, KnowledgeGraphQueryRequest{Query: `INSERT DATA {
		<https://example.com/a> owl:sameAs <https://example.com/b> .
		<https://example.com/b> owl:sameAs <https://example.com/c> .
		<https://example.com/a> schema:name "A" }`}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	raw, err := db.GraphRAGTools().Call(ctx, "knowledge_graph_infer_refresh", json.RawMessage(`{"max_same_as_class_size":2}`))
	if err != nil {
		t.Fatalf("refresh through the tool: %v", err)
	}
	res, ok := raw.(*KnowledgeGraphInferenceRefreshResponse)
	if !ok {
		t.Fatalf("dispatch returned %T", raw)
	}
	if got := res.Result.OversizedSameAsClasses; len(got) != 1 || got[0].Size != 3 || got[0].Cap != 2 {
		t.Fatalf("oversized = %+v, want the one class of 3 over a cap of 2", got)
	}

	// Nothing from the skipped class was copied: c never gained a's name.
	names, err := db.QueryKnowledgeGraph(ctx, KnowledgeGraphQueryRequest{Query: `SELECT ?n WHERE { <https://example.com/c> schema:name ?n }`})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(names.Result.Bindings) != 0 {
		t.Fatalf("c has names %+v; a class over the cap must not be materialized", names.Result.Bindings)
	}

	if _, err := db.GraphRAGTools().Call(ctx, "knowledge_graph_infer_refresh", json.RawMessage(`{"max_same_as_class_size":-1}`)); err == nil {
		t.Fatal("a negative cap was accepted")
	}
}
