package graph

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph/cypher"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

func TestTheNodePropertyIndexCatalog(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			for _, k := range []string{"status", "run_id", "run_id"} {
				if err := b.store.IndexNodeProperty(ctx, k); err != nil {
					t.Fatalf("IndexNodeProperty(%s): %v", k, err)
				}
			}
			for _, bad := range []string{"", "run-id", "x'y", "a b", "1st", strings.Repeat("k", 41)} {
				if err := b.store.IndexNodeProperty(ctx, bad); err == nil {
					t.Errorf("IndexNodeProperty(%q) accepted a key that would be spliced into SQL", bad)
				}
			}
			if got, err := b.store.NodePropertyIndexes(ctx); err != nil || !reflect.DeepEqual(got, []string{"run_id", "status"}) {
				t.Errorf("indexes = %v (%v), want [run_id status]", got, err)
			}

			// The index is evaluated on every write; a node with no
			// properties, or properties that are not JSON, must still write.
			for _, n := range []*GraphNode{
				{ID: "pi:bare", Content: "no properties"},
				{ID: "pi:run", Properties: map[string]any{"run_id": "r1"}},
			} {
				if err := b.store.UpsertNode(ctx, n); err != nil {
					t.Fatalf("UpsertNode %s with indexes in place: %v", n.ID, err)
				}
			}

			if err := b.store.DropNodePropertyIndex(ctx, "status"); err != nil {
				t.Fatalf("DropNodePropertyIndex: %v", err)
			}
			if err := b.store.DropNodePropertyIndex(ctx, "status"); err != nil {
				t.Errorf("dropping an index twice: %v", err)
			}
			if got, _ := b.store.NodePropertyIndexes(ctx); !reflect.DeepEqual(got, []string{"run_id"}) {
				t.Errorf("after drop, indexes = %v, want [run_id]", got)
			}
		})
	}
}

// execStep is one step of the seeded execution graph, kept so the test can
// compute every answer itself.
type execStep struct {
	id, run, typ, status string
	latency              any // int64, or a string where a producer wrote one
}

func seedExecutionGraph(t *testing.T, g *GraphStore, ctx context.Context) []execStep {
	t.Helper()
	types := []string{"ToolCall", "LLMCall", "Retrieval"}
	var steps []execStep
	var nodes []*GraphNode
	var edges []*GraphEdge
	for r := 0; r < 20; r++ {
		run := fmt.Sprintf("run:%02d", r)
		prev := ""
		for i := 1; i <= 10; i++ {
			s := execStep{
				id: fmt.Sprintf("%s/%02d", run, i), run: run, typ: types[i%3],
				status: "done", latency: int64((r*10 + i) * 37 % 6000),
			}
			if (r+i)%7 == 0 {
				s.status = "failed"
			}
			if r == 3 && i == 3 {
				s.latency = "5000" // a number written as a string
			}
			steps = append(steps, s)
			nodes = append(nodes, &GraphNode{ID: s.id, NodeType: s.typ, Properties: map[string]any{
				"run_id": s.run, "status": s.status, "latency_ms": s.latency}})
			if prev != "" {
				edges = append(edges, &GraphEdge{ID: prev + ">" + s.id, FromNodeID: prev, ToNodeID: s.id, EdgeType: "TRIGGERED", Weight: 1})
			}
			prev = s.id
		}
	}
	nodes = append(nodes, &GraphNode{ID: "unrelated"}) // no properties at all
	if res, err := g.UpsertNodesBatch(ctx, nodes); err != nil || res.Err() != nil {
		t.Fatalf("seed nodes: %v %v", err, res.Err())
	}
	if res, err := g.UpsertEdgesBatch(ctx, edges); err != nil || res.Err() != nil {
		t.Fatalf("seed edges: %v %v", err, res.Err())
	}
	return steps
}

func TestAnIndexedPropertyAnswersTheSameAndIsLookedUp(t *testing.T) {
	type query struct {
		name, cypher string
		want         func([]execStep) int64
		// Asserted on SQLite: the plan reads a property index, and it reads
		// no whole table. A query may have a better index than the property
		// one — node_type, for a labelled match — so the two are separate.
		indexUsed, noScan bool
	}
	count := func(steps []execStep, keep func(execStep) bool) int64 {
		var n int64
		for _, s := range steps {
			if keep(s) {
				n++
			}
		}
		return n
	}
	slow := func(s execStep) bool { v, ok := s.latency.(int64); return ok && v > 4000 }
	queries := []query{
		{"equality", `MATCH (s) WHERE s.run_id = 'run:03' RETURN count(s)`,
			func(st []execStep) int64 { return count(st, func(s execStep) bool { return s.run == "run:03" }) }, true, true},
		{"IN", `MATCH (s) WHERE s.status IN ['failed', 'paused'] RETURN count(s)`,
			func(st []execStep) int64 { return count(st, func(s execStep) bool { return s.status == "failed" }) }, true, true},
		{"inline map seeding a typed trail", `MATCH (l:LLMCall {run_id: 'run:03'})-[:TRIGGERED*1..2]->(t:ToolCall) RETURN count(*)`,
			func(st []execStep) int64 {
				var n int64
				for i, s := range st {
					if s.run != "run:03" || s.typ != "LLMCall" {
						continue
					}
					for d := 1; d <= 2 && i+d < len(st) && st[i+d].run == s.run; d++ {
						if st[i+d].typ == "ToolCall" {
							n++
						}
					}
				}
				return n
			}, true, true},
		{"numeric range", `MATCH (t:ToolCall) WHERE t.latency_ms > 4000 RETURN count(t)`,
			func(st []execStep) int64 {
				return count(st, func(s execStep) bool { return s.typ == "ToolCall" && slow(s) })
			}, false, true},
		{"numeric range, reversed", `MATCH (t) WHERE 4000 < t.latency_ms RETURN count(t)`,
			func(st []execStep) int64 { return count(st, slow) }, true, true},
		{"range and equality", `MATCH (t) WHERE t.status = 'done' AND t.latency_ms <= 300 RETURN count(t)`,
			func(st []execStep) int64 {
				return count(st, func(s execStep) bool {
					v, ok := s.latency.(int64)
					return s.status == "done" && ok && v <= 300
				})
			}, true, true},
		{"unindexed key, as before", `MATCH (t) WHERE t.kind = 'x' RETURN count(t)`,
			func([]execStep) int64 { return 0 }, false, false},
	}

	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			steps := seedExecutionGraph(t, b.store, ctx)
			for _, k := range []string{"run_id", "status", "latency_ms"} {
				if err := b.store.IndexNodeProperty(ctx, k); err != nil {
					t.Fatalf("IndexNodeProperty(%s): %v", k, err)
				}
			}
			sqlite := b.store.dialect.Kind() == sqldialect.SQLite

			indexed := map[string]any{}
			for _, q := range queries {
				var plans []string
				res, err := cypher.Execute(ctx, planBackend{cypherBackend{b.store}, &plans}, q.cypher, cypher.Options{})
				if err != nil {
					t.Fatalf("%s: %v", q.name, err)
				}
				if want := q.want(steps); len(res.Rows) != 1 || res.Rows[0][0] != want {
					t.Errorf("%s: rows = %v, want [[%d]]", q.name, res.Rows, want)
				}
				indexed[q.name] = res.Rows
				if !sqlite {
					continue
				}
				all := strings.Join(plans, "\n")
				if q.indexUsed && !strings.Contains(all, "idx_graph_nodes_prop_") {
					t.Errorf("%s: no property index in the plan:\n%s", q.name, all)
				}
				if q.noScan && fullScan.MatchString(all) {
					t.Errorf("%s: the plan still scans a whole table:\n%s", q.name, all)
				}
			}

			// GraphFilter.Properties uses the index with no change of its own.
			nodes, err := b.store.ListNodes(ctx, &GraphFilter{Properties: map[string]string{"run_id": "run:07"}})
			if err != nil || len(nodes) != 10 {
				t.Errorf("ListNodes by run_id = %d nodes (%v), want 10", len(nodes), err)
			}
			if sqlite {
				where, args := b.store.nodeWhere(&GraphFilter{Properties: map[string]string{"run_id": "run:07"}})
				plan := explain(t, b.store, `SELECT id FROM graph_nodes`+where, args...)
				if !strings.Contains(plan, "idx_graph_nodes_prop_run_id") {
					t.Errorf("GraphFilter.Properties does not use the index:\n%s", plan)
				}
			}

			// The same answers with every index gone: the index changes how a
			// query is answered, never what it answers.
			for _, k := range []string{"run_id", "status", "latency_ms"} {
				if err := b.store.DropNodePropertyIndex(ctx, k); err != nil {
					t.Fatalf("DropNodePropertyIndex(%s): %v", k, err)
				}
			}
			for _, q := range queries {
				res, err := b.store.QueryCypher(ctx, CypherRequest{Query: q.cypher})
				if err != nil {
					t.Fatalf("%s unindexed: %v", q.name, err)
				}
				if !reflect.DeepEqual(res.Rows, indexed[q.name]) {
					t.Errorf("%s: %v indexed, %v without the index", q.name, indexed[q.name], res.Rows)
				}
			}
		})
	}
}

func explain(t *testing.T, g *GraphStore, q string, args ...any) string {
	t.Helper()
	rows, err := g.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+g.dialect.Rebind(q), args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var lines []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("explain scan: %v", err)
		}
		lines = append(lines, detail)
	}
	return strings.Join(lines, "\n")
}
