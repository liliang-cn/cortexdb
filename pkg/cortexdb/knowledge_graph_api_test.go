package cortexdb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

func TestKnowledgeGraphAPI(t *testing.T) {
	dbPath := fmt.Sprintf("test_knowledge_graph_api_%d.db", time.Now().UnixNano())
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
	dbPath := fmt.Sprintf("test_knowledge_graph_toolbox_%d.db", time.Now().UnixNano())
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
	dbPath := fmt.Sprintf("test_knowledge_graph_inference_%d.db", time.Now().UnixNano())
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
}

func ptrKnowledgeTerm(term KnowledgeGraphTerm) *KnowledgeGraphTerm {
	return &term
}

func ptrBool(value bool) *bool {
	return &value
}
