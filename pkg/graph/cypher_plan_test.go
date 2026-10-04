package graph

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph/cypher"
)

// What SQLite's planner does with a Cypher query anchored on an id, asserted
// rather than timed: the store never runs ANALYZE, so the plan does not depend
// on how many rows there are, and a small graph shows the plan a large one
// gets. On a 20,000-step execution graph the plans this test forbids took
// 11–67ms per query and grew with the graph; the ones it requires stay under
// 2ms at any size.

// planBackend records the plan of every statement the engine runs.
type planBackend struct {
	cypherBackend
	plans *[]string
}

func (b planBackend) Query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if rows, err := b.g.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+b.g.dialect.Rebind(q), args...); err == nil {
		var plan []string
		for rows.Next() {
			var id, parent, notUsed int
			var detail string
			if rows.Scan(&id, &parent, &notUsed, &detail) == nil {
				plan = append(plan, detail)
			}
		}
		_ = rows.Close()
		*b.plans = append(*b.plans, strings.Join(plan, "\n"))
	}
	return b.cypherBackend.Query(ctx, q, args...)
}

// seedChains writes runs of ten steps chained by TRIGGERED, each step also
// hanging off its run by HAS_STEP, so a type filter alone matches most edges.
func seedChains(t *testing.T, g *GraphStore, ctx context.Context, runs int) {
	t.Helper()
	var nodes []*GraphNode
	var edges []*GraphEdge
	for r := 0; r < runs; r++ {
		run := fmt.Sprintf("run:%03d", r)
		nodes = append(nodes, &GraphNode{ID: run, NodeType: "AgentRun"})
		prev := ""
		for i := 1; i <= 10; i++ {
			id := fmt.Sprintf("%s/step-%02d", run, i)
			nodes = append(nodes, &GraphNode{ID: id, NodeType: "Step", Properties: map[string]any{"seq": i}})
			edges = append(edges, &GraphEdge{ID: run + "->" + id, FromNodeID: run, ToNodeID: id, EdgeType: "HAS_STEP", Weight: 1})
			if prev != "" {
				edges = append(edges, &GraphEdge{ID: prev + "->" + id, FromNodeID: prev, ToNodeID: id, EdgeType: "TRIGGERED", Weight: 1})
			}
			prev = id
		}
	}
	if res, err := g.UpsertNodesBatch(ctx, nodes); err != nil || res.Err() != nil {
		t.Fatalf("seed nodes: %v %v", err, res.Err())
	}
	if res, err := g.UpsertEdgesBatch(ctx, edges); err != nil || res.Err() != nil {
		t.Fatalf("seed edges: %v %v", err, res.Err())
	}
}

// fullScan matches a plan line that reads a whole pattern table: a node (n0),
// a fixed relationship (e1) or a traversal seed (sn). The CTE's own rows (v,
// vl0) are small and scanned by design.
var fullScan = regexp.MustCompile(`\bSCAN (n|e|sn)\d*\b`)

func TestAnAnchoredCypherQueryWalksFromItsAnchor(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()
	seedChains(t, g, ctx, 30)

	params := map[string]any{"last": "run:000/step-10", "first": "run:000/step-01"}
	for _, c := range []struct {
		name, query string
		want        any
	}{
		{"back along a typed trail", `MATCH (s)-[:TRIGGERED*1..6]->(t) WHERE id(t) = $last RETURN count(DISTINCT s)`, int64(6)},
		{"forward along a typed trail", `MATCH (s)-[:TRIGGERED*1..6]->(t) WHERE id(s) = $first RETURN count(DISTINCT t)`, int64(6)},
		{"either way along a typed trail", `MATCH (s)-[:TRIGGERED*1..6]-(t) WHERE id(t) = $last RETURN count(DISTINCT s)`, int64(6)},
		{"one typed hop back", `MATCH (s)-[:TRIGGERED]->(t) WHERE id(t) = $last RETURN id(s)`, "run:000/step-09"},
		{"two typed hops back", `MATCH (s)-[:TRIGGERED]->(m)-[:TRIGGERED]->(t) WHERE id(t) = $last RETURN id(s)`, "run:000/step-08"},
		{"anchored by an earlier clause", `MATCH (t) WHERE id(t) = $last MATCH (s)-[:TRIGGERED]->(t) RETURN id(s)`, "run:000/step-09"},
		{"anchored on a typed label check", `MATCH (s)-[r]->(t) WHERE id(t) = $last AND r:TRIGGERED RETURN id(s)`, "run:000/step-09"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var plans []string
			res, err := cypher.Execute(ctx, planBackend{cypherBackend{g}, &plans}, c.query, cypher.Options{Params: params})
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			if len(res.Rows) != 1 || res.Rows[0][0] != c.want {
				t.Errorf("rows = %v, want [[%v]]", res.Rows, c.want)
			}
			for _, plan := range plans {
				if strings.Contains(plan, "idx_edges_type") {
					t.Errorf("a hop is driven from the type index, not the anchor's adjacency:\n%s", plan)
				}
				if l := fullScan.FindString(plan); l != "" {
					t.Errorf("the plan scans a whole table (%s):\n%s", l, plan)
				}
			}
		})
	}
}

// With no anchor, a type is the most selective thing a query has, and the
// type index must stay available to it.
func TestAnUnanchoredTypedQueryStillUsesTheTypeIndex(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()
	seedChains(t, g, ctx, 3)

	var plans []string
	res, err := cypher.Execute(ctx, planBackend{cypherBackend{g}, &plans},
		`MATCH (s)-[:TRIGGERED]->(t) RETURN count(*)`, cypher.Options{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] != int64(27) {
		t.Errorf("rows = %v, want [[27]]", res.Rows)
	}
	if len(plans) == 0 || !strings.Contains(strings.Join(plans, "\n"), "idx_edges_type") {
		t.Errorf("an unanchored typed match no longer uses the type index:\n%s", strings.Join(plans, "\n---\n"))
	}
}
