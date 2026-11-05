package cortexdb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

func callTool[T any](t *testing.T, tools *GraphRAGToolbox, name string, in any) (T, error) {
	t.Helper()
	raw, _ := json.Marshal(in)
	out, err := tools.Call(context.Background(), name, raw)
	var zero T
	if err != nil {
		return zero, err
	}
	b, _ := json.Marshal(out)
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode %s: %v (%s)", name, err, b)
	}
	return v, nil
}

func iriTerm(v string) *KnowledgeGraphTerm {
	return &KnowledgeGraphTerm{Kind: graph.RDFTermIRI, Value: v}
}

// The four graph read tools take as_of; the knowledge graph ones see a triple
// the present has already lost, and a malformed instant is refused.
func TestKnowledgeGraphToolsReadTheTriplesThatHeldAsOf(t *testing.T) {
	for name, db := range temporalBrains(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			tools := db.GraphRAGTools()
			tr := graph.RDFTriple{Subject: *iriTerm("http://ex/leo"), Predicate: *iriTerm("http://ex/livesIn"), Object: *iriTerm("http://ex/beijing")}
			if _, err := db.UpsertKnowledgeGraph(ctx, KnowledgeGraphUpsertRequest{Triples: []KnowledgeGraphTriple{tr}}); err != nil {
				t.Fatal(err)
			}
			before := db.Graph().Now().Format(time.RFC3339Nano)
			if _, err := db.DeleteKnowledgeGraph(ctx, KnowledgeGraphDeleteRequest{Triples: []KnowledgeGraphTriple{tr}}); err != nil {
				t.Fatal(err)
			}
			pattern := KnowledgeGraphTriplePattern{Subject: iriTerm("http://ex/leo")}

			now, err := callTool[KnowledgeGraphFindResponse](t, tools, "knowledge_graph_find", KnowledgeGraphFindRequest{Pattern: pattern})
			if err != nil || len(now.Triples) != 0 {
				t.Fatalf("find now: %v %+v", err, now)
			}
			past, err := callTool[KnowledgeGraphFindResponse](t, tools, "knowledge_graph_find", KnowledgeGraphFindRequest{Pattern: pattern, AsOf: before})
			if err != nil || len(past.Triples) != 1 {
				t.Fatalf("find as of before: %v %+v", err, past)
			}

			const q = `SELECT ?c WHERE { <http://ex/leo> <http://ex/livesIn> ?c }`
			sNow, err := callTool[KnowledgeGraphQueryResponse](t, tools, "knowledge_graph_query", KnowledgeGraphQueryRequest{Query: q})
			if err != nil || len(sNow.Result.Bindings) != 0 {
				t.Fatalf("query now: %v %+v", err, sNow)
			}
			sPast, err := callTool[KnowledgeGraphQueryResponse](t, tools, "knowledge_graph_query", KnowledgeGraphQueryRequest{Query: q, AsOf: before})
			if err != nil || len(sPast.Result.Bindings) != 1 {
				t.Fatalf("query as of before: %v %+v", err, sPast)
			}

			// Reading the past is read-only.
			_, err = callTool[KnowledgeGraphQueryResponse](t, tools, "knowledge_graph_query", KnowledgeGraphQueryRequest{
				Query: `INSERT DATA { <http://ex/a> <http://ex/p> <http://ex/b> }`, AsOf: before})
			if err == nil {
				t.Errorf("a SPARQL update under as_of was accepted")
			}
			if n, _ := callTool[KnowledgeGraphFindResponse](t, tools, "knowledge_graph_find", KnowledgeGraphFindRequest{Pattern: KnowledgeGraphTriplePattern{Subject: iriTerm("http://ex/a")}}); len(n.Triples) != 0 {
				t.Errorf("the refused update wrote anyway")
			}

			// Never read as now.
			for _, tool := range []string{"knowledge_graph_find", "knowledge_graph_query"} {
				in := map[string]any{"as_of": "last tuesday", "query": q, "pattern": map[string]any{}}
				if _, err := tools.Call(ctx, tool, rawJSON(in)); err == nil || !strings.Contains(err.Error(), "RFC 3339") {
					t.Errorf("%s with a malformed as_of: %v", tool, err)
				}
			}
		})
	}
}

func rawJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// graph_cypher_query and expand_graph with as_of before an edge's retraction
// still see the edge.
func TestExpandGraphAndCypherReadAnEdgeAsOfBeforeItsRetraction(t *testing.T) {
	for name, db := range temporalBrains(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			tools := db.GraphRAGTools()
			if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
				{Name: "leo", Type: "Person"}, {Name: "beijing", Type: "City"},
			}}); err != nil {
				t.Fatal(err)
			}
			rel, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{
				{From: "leo", To: "beijing", Type: "lives_in"}}})
			if err != nil || len(rel.EdgeIDs) != 1 {
				t.Fatalf("relations: %v", err)
			}
			edges, err := db.Graph().GetEdgesBatch(ctx, rel.EdgeIDs)
			if err != nil || len(edges) != 1 {
				t.Fatalf("edge: %v", err)
			}
			edge := edges[0]
			before := db.Graph().Now().Format(time.RFC3339Nano)
			if err := db.Graph().DeleteEdge(ctx, rel.EdgeIDs[0]); err != nil {
				t.Fatal(err)
			}

			const cy = `MATCH (a)-[r:lives_in]->(b) RETURN id(r)`
			now, err := callTool[CypherQueryResponse](t, tools, "graph_cypher_query", CypherQueryRequest{Query: cy})
			if err != nil || now.RowCount != 0 {
				t.Fatalf("cypher now: %v %+v", err, now)
			}
			past, err := callTool[CypherQueryResponse](t, tools, "graph_cypher_query", CypherQueryRequest{Query: cy, AsOf: before})
			if err != nil || past.RowCount != 1 {
				t.Fatalf("cypher as of before: %v %+v", err, past)
			}

			exp := ToolExpandGraphRequest{NodeIDs: []string{edge.FromNodeID}, MaxHops: 1}
			eNow, err := callTool[ToolExpandGraphResponse](t, tools, "expand_graph", exp)
			if err != nil || len(eNow.Edges) != 0 {
				t.Fatalf("expand now: %v %+v", err, eNow)
			}
			exp.AsOf = before
			ePast, err := callTool[ToolExpandGraphResponse](t, tools, "expand_graph", exp)
			if err != nil || len(ePast.Edges) != 1 || len(ePast.Nodes) != 2 {
				t.Fatalf("expand as of before: %v edges=%d nodes=%d", err, len(ePast.Edges), len(ePast.Nodes))
			}
			exp.AsOf = "yesterday-ish"
			if _, err := tools.ExpandGraph(ctx, exp); err == nil {
				t.Errorf("expand_graph accepted a malformed as_of")
			}
		})
	}
}

// knowledge_graph_history is a read that names what each delete was, and
// vacuum_graph is what ends it.
func TestKnowledgeGraphHistoryToolAndVacuum(t *testing.T) {
	for name, db := range temporalBrains(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			tools := db.GraphRAGTools()
			tr := graph.RDFTriple{Subject: *iriTerm("http://ex/leo"), Predicate: *iriTerm("http://ex/livesIn"), Object: *iriTerm("http://ex/beijing")}
			if _, err := db.UpsertKnowledgeGraph(ctx, KnowledgeGraphUpsertRequest{Triples: []KnowledgeGraphTriple{tr}}); err != nil {
				t.Fatal(err)
			}
			if _, err := tools.QueryKnowledgeGraph(ctx, KnowledgeGraphQueryRequest{
				Query: `DELETE WHERE { <http://ex/leo> <http://ex/livesIn> ?c }`}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.UpsertKnowledgeGraph(ctx, KnowledgeGraphUpsertRequest{Triples: []KnowledgeGraphTriple{tr}}); err != nil {
				t.Fatal(err)
			}

			h, err := callTool[KnowledgeGraphHistoryResponse](t, tools, "knowledge_graph_history", KnowledgeGraphHistoryRequest{Subject: iriTerm("http://ex/leo")})
			if err != nil || len(h.Versions) != 2 {
				t.Fatalf("history: %v %+v", err, h)
			}
			if v := h.Versions[0]; v.Status != "ended" || v.Reason != graph.ReasonRetracted || v.Producer != graph.ProducerSPARQLUpdate {
				t.Errorf("first version: %+v", v)
			}
			if h.Versions[1].Status != "current" {
				t.Errorf("second version: %+v", h.Versions[1])
			}
			if _, err := tools.Call(ctx, "knowledge_graph_history", rawJSON(map[string]any{"from": "soon", "subject": iriTerm("http://ex/leo")})); err == nil {
				t.Errorf("a malformed window instant was accepted")
			}
			if _, err := tools.Call(ctx, "knowledge_graph_history", rawJSON(map[string]any{})); err == nil {
				t.Errorf("a history without a subject was accepted")
			}

			cutoff := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
			dry, err := callTool[graph.PurgeReport](t, tools, "vacuum_graph", VacuumGraphToolRequest{Before: cutoff, DryRun: true})
			if err != nil || dry.Triples != 1 {
				t.Fatalf("dry vacuum: %v %+v", err, dry)
			}
			done, err := callTool[graph.PurgeReport](t, tools, "vacuum_graph", VacuumGraphToolRequest{Before: cutoff})
			if err != nil || done.Triples != 1 {
				t.Fatalf("vacuum: %v %+v", err, done)
			}
			h, _ = callTool[KnowledgeGraphHistoryResponse](t, tools, "knowledge_graph_history", KnowledgeGraphHistoryRequest{Subject: iriTerm("http://ex/leo")})
			if len(h.Versions) != 1 || h.Versions[0].Status != "current" {
				t.Errorf("after vacuum: %+v", h)
			}
		})
	}
}
