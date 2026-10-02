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
