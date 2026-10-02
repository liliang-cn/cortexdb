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

// A SHACL rule over the property graph, through the facade and through the
// tool, on both backends: a service that runs on a host typed node joins the
// cluster; one that runs on a vm does not.
func TestSHACLRulesOverThePropertyGraphInferExplainableClusterMembershipOnSQLite(t *testing.T) {
	dbPath := fmt.Sprintf("test_shacl_rules_%d.db", testname.Nano())
	defer func() { _ = os.Remove(dbPath) }()
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	runSHACLRulesFacadeScenario(t, db)
}

func TestSHACLRulesOverThePropertyGraphInferExplainableClusterMembershipOnPostgres(t *testing.T) {
	runSHACLRulesFacadeScenario(t, openPostgresBrain(t, 4))
}

func runSHACLRulesFacadeScenario(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	tools := db.GraphRAGTools()
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "ledger-svc", Type: "Service"},
		{Name: "risk-svc", Type: "Service"},
		{Name: "box1", Type: "node"},
		{Name: "vm7", Type: "vm"},
	}}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{
		{From: "ledger-svc", To: "box1", Type: "RUNS_ON"},
		{From: "risk-svc", To: "vm7", Type: "RUNS_ON"},
	}}); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}

	iri := graph.NewIRI
	rule, cond, prop := graph.NewBlankNode("rule"), graph.NewBlankNode("cond"), graph.NewBlankNode("prop")
	shapes := []KnowledgeGraphTriple{
		{Subject: iri("https://example.com/ClusterShape"), Predicate: iri(graph.SHACLTargetSubjectsOf), Object: iri("cxr:RUNS_ON")},
		{Subject: iri("https://example.com/ClusterShape"), Predicate: iri(graph.SHACLRule), Object: rule},
		{Subject: rule, Predicate: iri(graph.RDFType), Object: iri(graph.SHACLTripleRule)},
		{Subject: rule, Predicate: iri(graph.SHACLSubject), Object: iri(graph.SHACLThis)},
		{Subject: rule, Predicate: iri(graph.SHACLPredicate), Object: iri("cxr:in_cluster")},
		{Subject: rule, Predicate: iri(graph.SHACLObject), Object: iri("cxn:openclaw")},
		{Subject: rule, Predicate: iri(graph.SHACLCondition), Object: cond},
		{Subject: cond, Predicate: iri(graph.SHACLProperty), Object: prop},
		{Subject: prop, Predicate: iri(graph.SHACLPath), Object: iri("cxr:RUNS_ON")},
		{Subject: prop, Predicate: iri(graph.SHACLClass), Object: iri("cxt:node")},
	}

	dry, err := db.ApplyKnowledgeGraphSHACLRules(ctx, KnowledgeGraphSHACLRulesRequest{Shapes: shapes, DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.Result.DerivedCount != 1 || dry.Result.Added != 0 {
		t.Fatalf("dry run: %+v", dry.Result)
	}

	input, err := json.Marshal(map[string]any{"shapes": shapes})
	if err != nil {
		t.Fatal(err)
	}
	out, err := tools.Call(ctx, "knowledge_graph_shacl_rules", input)
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	resp := out.(*KnowledgeGraphSHACLRulesResponse)
	if resp.Result.Added != 1 || len(resp.Result.Derived) != 1 {
		t.Fatalf("tool run: %+v", resp.Result)
	}
	derived := resp.Result.Derived[0]
	if derived.Subject.Value != graph.PropertyGraphNodeIRI("entity:ledger_svc") ||
		derived.Object.Value != graph.PropertyNodeNamespace+"openclaw" ||
		derived.Rule != graph.SHACLTripleRuleNamePrefix+"https://example.com/ClusterShape" {
		t.Fatalf("derived %+v", derived)
	}

	explained, err := db.ExplainKnowledgeGraphInference(ctx, KnowledgeGraphInferenceExplainRequest{TripleID: derived.ID})
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if explained.Explanation.Explicit || len(explained.Explanation.SupportTripleIDs) == 0 {
		t.Fatalf("explanation: %+v", explained.Explanation)
	}
	hasEdge := false
	for _, id := range explained.Explanation.SupportTripleIDs {
		if strings.HasPrefix(id, "pg:edge:") {
			hasEdge = true
		}
	}
	if !hasEdge {
		t.Fatalf("the RUNS_ON edge must be among the supports: %v", explained.Explanation.SupportTripleIDs)
	}

	again, err := db.ApplyKnowledgeGraphSHACLRules(ctx, KnowledgeGraphSHACLRulesRequest{Shapes: shapes})
	if err != nil {
		t.Fatal(err)
	}
	if again.Result.Unchanged != 1 || again.Result.Added != 0 || again.Result.Retracted != 0 {
		t.Fatalf("re-run: %+v", again.Result)
	}
	cleared, err := db.ApplyKnowledgeGraphSHACLRules(ctx, KnowledgeGraphSHACLRulesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Result.Retracted != 1 {
		t.Fatalf("an empty rule set retracts: %+v", cleared.Result)
	}
}
