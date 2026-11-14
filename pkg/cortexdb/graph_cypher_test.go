package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func openCypherTestDB(t *testing.T) *DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), fmt.Sprintf("test_cypher_%d.db", testname.Nano()))
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, s := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + s)
		}
	})
	tb := db.GraphRAGTools()
	ctx := context.Background()
	call := func(name, body string) {
		if _, err := tb.Call(ctx, name, json.RawMessage(body)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// Written through the same tools an extraction agent uses, so the query
	// runs against the shapes those tools really produce.
	call("upsert_entities", `{"entities":[
		{"name":"CortexDB","type":"project"},
		{"name":"Athanor","type":"project"},
		{"name":"steward","type":"project"},
		{"name":"node-e","type":"host"},
		{"name":"cortexdb-grpc","type":"service"}]}`)
	call("upsert_relations", `{"relations":[
		{"from":"Athanor","to":"CortexDB","type":"depends_on"},
		{"from":"steward","to":"CortexDB","type":"depends_on"},
		{"from":"cortexdb-grpc","to":"node-e","type":"runs_on"}]}`)
	return db
}

func TestQueryCypherAnswersWhoDependsOnAProjectFromToolWrittenEntities(t *testing.T) {
	db := openCypherTestDB(t)
	res, err := db.QueryCypher(context.Background(), CypherQueryRequest{
		Query:  "MATCH (p:project)-[:depends_on]->(c {name: $name}) RETURN p.name AS project ORDER BY project",
		Params: map[string]any{"name": "CortexDB"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(res.Columns) != "[project]" || fmt.Sprint(res.Rows) != "[[Athanor] [steward]]" {
		t.Fatalf("got columns %v rows %v", res.Columns, res.Rows)
	}
}

func TestGraphCypherQueryToolRefusesWritesByName(t *testing.T) {
	db := openCypherTestDB(t)
	_, err := db.GraphRAGTools().Call(context.Background(), "graph_cypher_query", json.RawMessage(`{"query":"MATCH (n) DETACH DELETE n"}`))
	if err == nil || !strings.Contains(err.Error(), "read-only") || !strings.Contains(err.Error(), "DETACH") {
		t.Fatalf("want a read-only refusal naming DETACH, got %v", err)
	}
	// Nothing was deleted.
	res, err := db.QueryCypher(context.Background(), CypherQueryRequest{Query: "MATCH (n) RETURN count(*)"})
	if err != nil || fmt.Sprint(res.Rows) != "[[5]]" {
		t.Fatalf("graph changed: %v %v", res, err)
	}
}

func TestGraphCypherQueryIsCallableOverMCP(t *testing.T) {
	db := openCypherTestDB(t)
	server := db.NewMCPServer(MCPServerOptions{})
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "cypher-test"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	out, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "graph_cypher_query",
		Arguments: map[string]any{"query": "MATCH (s)-[r:runs_on]->(h:host) RETURN s.name, type(r), h.name"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.IsError {
		t.Fatalf("tool error: %+v", out.Content)
	}
	b, _ := json.Marshal(out.StructuredContent)
	if !strings.Contains(string(b), `"rows":[["cortexdb-grpc","runs_on","node-e"]]`) {
		t.Fatalf("unexpected result %s", b)
	}
}

// openOrdersTestDB holds orders whose created property is written the ways
// real ones are: RFC 3339 with and without an offset, a bare day, absent.
func openOrdersTestDB(t *testing.T) *DB {
	t.Helper()
	db := openCypherTestDB(t)
	for _, o := range []struct {
		id      string
		created any
		amount  any
	}{
		{"o1", "2025-12-30T23:30:00Z", "10.5"},
		{"o2", "2025-12-31", 20},
		{"o3", "2026-01-01T00:10:00+08:00", 30},
		{"o4", "2026-01-15", 40},
		{"o5", "2026-02-28T12:00:00Z", 50},
		{"o6", "2026-03-01", "60"},
		{"o7", nil, 70},
	} {
		props := map[string]any{"name": o.id, "amount": o.amount}
		if o.created != nil {
			props["created"] = o.created
		}
		if err := db.graph.UpsertNode(context.Background(), &graph.GraphNode{ID: "order:" + o.id, NodeType: "Order", Properties: props}); err != nil {
			t.Fatalf("order %s: %v", o.id, err)
		}
	}
	return db
}

const ordersByMonth = "MATCH (n:Order) RETURN date.truncate('month', date(n.created)) AS m, count(*) AS c, avg(toFloat(n.amount)) AS a ORDER BY m"

// December holds o1 and o2 (mean 15.25), January o3 — dated where it was
// written, not in UTC — and o4; the order with no date is the null bucket,
// last. The month comes back as an ISO date string.
const ordersByMonthRows = `[["2025-12-01",2,15.25],["2026-01-01",2,35],["2026-02-01",1,50],["2026-03-01",1,60],[null,1,70]]`

func TestGraphCypherQueryToolBucketsByMonthAndReturnsISODates(t *testing.T) {
	db := openOrdersTestDB(t)
	args, _ := json.Marshal(map[string]any{"query": ordersByMonth})
	out, err := db.GraphRAGTools().Call(context.Background(), "graph_cypher_query", args)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"columns":["m","c","a"]`) || !strings.Contains(string(b), `"rows":`+ordersByMonthRows) {
		t.Fatalf("unexpected result %s", b)
	}
	// In Go the bucket is a plain string too: nothing downstream needs to
	// know a temporal type.
	res, err := db.QueryCypher(context.Background(), CypherQueryRequest{Query: ordersByMonth})
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := res.Rows[0][0].(string); !ok || m != "2025-12-01" {
		t.Fatalf("first bucket: %#v", res.Rows[0][0])
	}
}

func TestGraphCypherQueryBucketsByMonthOverMCP(t *testing.T) {
	db := openOrdersTestDB(t)
	server := db.NewMCPServer(MCPServerOptions{})
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "cypher-test"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	call := func(query string) string {
		t.Helper()
		out, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "graph_cypher_query", Arguments: map[string]any{"query": query}})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(out.StructuredContent)
		if out.IsError {
			b, _ = json.Marshal(out.Content)
		}
		return string(b)
	}
	if got := call(ordersByMonth); !strings.Contains(got, `"rows":`+ordersByMonthRows) {
		t.Fatalf("unexpected result %s", got)
	}
	got := call("MATCH (n:Order) WITH n, toFloat(n.amount) AS x RETURN percentileCont(x, 0.5), percentileDisc(x, 0.9), round(stDevP(x), 3), count(n.created_at), duration.inDays(date('2025-12-30'), date('2026-03-01')).days")
	if !strings.Contains(got, `"rows":[[40,70,19.893,7,61]]`) {
		t.Fatalf("unexpected result %s", got)
	}
	// One unreadable date is an error that names it, not a quietly smaller bucket.
	if got := call("MATCH (n:Order) RETURN date(n.name)"); !strings.Contains(got, "o1") || !strings.Contains(got, "date()") {
		t.Fatalf("want an error naming the text, got %s", got)
	}
}
