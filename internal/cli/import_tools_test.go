package cli

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
	"github.com/liliang-cn/cortexdb/v2/pkg/rpcserver"
)

const importCSV = "id,region,total\n1,eu,10.50\n2,eu,30\n3,us,5.25\n4,eu,20\n"

var importToolNames = []string{"importflow_plan", "importflow_run", "importflow_ddl_plan", "importflow_ddl_plan_ai"}

// connectAgent serves server in memory and returns an agent's session on it.
func connectAgent(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "agent"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// agentCall calls a tool and returns its text and whether it failed. A local
// tool fails as a tool result marked as an error; a forwarded one fails as an
// error of the call, which is how the bridge reports every remote failure.
func agentCall(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err.Error(), true
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text, res.IsError
}

func agentTools(t *testing.T, session *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	list, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*mcp.Tool{}
	for _, tool := range list.Tools {
		out[tool.Name] = tool
	}
	return out
}

// importAndQuery is the agent's whole errand, the same in both modes: plan,
// run with the plan, total the table.
func importAndQuery(t *testing.T, session *mcp.ClientSession) {
	t.Helper()
	planText, failed := agentCall(t, session, "importflow_plan", map[string]any{"format": "csv", "table": "orders", "data": importCSV, "build_tables": true})
	var plan map[string]any
	if err := json.Unmarshal([]byte(planText), &plan); failed || err != nil || plan["mode"] != "no_model" {
		t.Fatalf("plan = %s (%v)", planText, err)
	}
	if text, failed := agentCall(t, session, "importflow_run", map[string]any{"format": "csv", "table": "orders", "data": importCSV, "plan": plan}); failed ||
		!strings.Contains(text, `"TablesStored":1`) || !strings.Contains(text, `"TableRowsStored":4`) {
		t.Fatalf("run = %s", text)
	}
	if text, failed := agentCall(t, session, "imported_tables_query", map[string]any{
		"sql": "SELECT region, count(*), sum(total) FROM orders GROUP BY region ORDER BY region",
	}); failed || !strings.Contains(text, `"rows":[["eu",3,60.5],["us",1,5.25]]`) {
		t.Fatalf("query = %s", text)
	}
	if text, failed := agentCall(t, session, "importflow_ddl_plan_ai", map[string]any{"ddl": "CREATE TABLE t (id INT PRIMARY KEY, name TEXT);"}); failed ||
		!strings.Contains(text, `"llm_used":false`) {
		t.Fatalf("ddl plan with no model = %s", text)
	}
}

// Local mode: the binary's own server offers the import tools beside the
// facade's, they work with no model configured, and — the process being the
// agent's own — a call may name a file.
func TestLocalModeImportsACSVAndQueriesIt(t *testing.T) {
	for _, key := range []string{"CORTEXDB_LLM_BASE_URL", "CORTEXDB_LLM_URL"} {
		t.Setenv(key, "")
	}
	dbPath := filepath.Join(t.TempDir(), "local.db")
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	session := connectAgent(t, newLocalMCPServer(db, dbPath))

	tools := agentTools(t, session)
	for _, name := range append([]string{"imported_tables_list", "imported_tables_describe", "imported_tables_query", "global_search"}, importToolNames...) {
		if tools[name] == nil {
			t.Fatalf("%s is not offered in local mode", name)
		}
	}
	importAndQuery(t, session)

	path := filepath.Join(t.TempDir(), "returns.csv")
	if err := os.WriteFile(path, []byte(importCSV), 0o600); err != nil {
		t.Fatal(err)
	}
	if text, failed := agentCall(t, session, "importflow_run", map[string]any{"format": "csv", "path": path, "plan": map[string]any{}, "keep_tables": true}); failed ||
		!strings.Contains(text, `"TableRowsStored":4`) {
		t.Fatalf("run by path = %s", text)
	}
	if text, failed := agentCall(t, session, "imported_tables_query", map[string]any{"sql": "SELECT count(*) FROM returns"}); failed || !strings.Contains(text, `"rows":[[4]]`) {
		t.Fatalf("query of the table imported by path = %s", text)
	}
}

// startImportBrain is a brain as cmd/cortexdb-grpc serves it: the facade's
// toolbox and importflow's tools beside it, with no local files and no model.
// The adapter there is a main package this one cannot import, so the wiring
// is repeated: the toolbox's definitions and its Call.
func startImportBrain(t *testing.T, token string) (string, *cortexdb.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "brain.db")
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	toolbox := importflow.NewToolbox(importflow.New(db))
	var extras []rpcserver.Tool
	for _, definition := range toolbox.Definitions() {
		name := definition.Name
		extras = append(extras, rpcserver.Tool{Definition: definition, Call: func(ctx context.Context, args json.RawMessage) (any, error) {
			return toolbox.Call(ctx, name, args)
		}})
	}
	srv, err := rpcserver.NewWithPolicy(db, rpcserver.Options{Token: token, DBPath: dbPath, ExtraTools: extras})
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = db.Close()
	})
	return lis.Addr().String(), db
}

// Remote mode: the bridge finds the import tools in the brain's catalogue and
// forwards them, so the same errand ends in a table in the shared database —
// and a path, which the brain would have to open on its own disk, is refused.
func TestRemoteModeImportsACSVAndQueriesIt(t *testing.T) {
	const token = "shared-secret"
	addr, db := startImportBrain(t, token)
	conn, err := dialCortexDB(addr, token)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	server, err := newRemoteMCPServer(context.Background(), rpcv1.NewToolsServiceClient(conn), addr)
	if err != nil {
		t.Fatal(err)
	}
	session := connectAgent(t, server)

	tools := agentTools(t, session)
	for _, name := range append([]string{"imported_tables_list", "imported_tables_describe", "imported_tables_query"}, importToolNames...) {
		if tools[name] == nil {
			t.Fatalf("%s is not offered in remote mode", name)
		}
	}
	// The agent reads the same contract as in local mode, but for the path.
	schema, _ := json.Marshal(tools["importflow_run"].InputSchema)
	for _, want := range []string{`"keep_tables"`, `"data"`, `"plan"`, "a call that names a path is refused"} {
		if !strings.Contains(string(schema), want) {
			t.Errorf("importflow_run's schema lacks %s: %s", want, schema)
		}
	}

	importAndQuery(t, session)
	tables, err := db.ListImportedTables(context.Background())
	if err != nil || len(tables) != 1 || tables[0].Name != "orders" || tables[0].RowCount != 4 {
		t.Fatalf("the brain's tables = %+v, %v", tables, err)
	}

	// A file on the agent's machine, named by path: the brain does not open
	// paths, says so, and imports nothing.
	path := filepath.Join(t.TempDir(), "returns.csv")
	if err := os.WriteFile(path, []byte(importCSV), 0o600); err != nil {
		t.Fatal(err)
	}
	text, failed := agentCall(t, session, "importflow_run", map[string]any{"format": "csv", "path": path, "plan": map[string]any{}, "keep_tables": true})
	if !failed || !strings.Contains(text, "send its text as data") {
		t.Fatalf("run by path in remote mode: failed=%v %s", failed, text)
	}
	if tables, err := db.ListImportedTables(context.Background()); err != nil || len(tables) != 1 {
		t.Fatalf("the refused path changed the brain's tables: %+v, %v", tables, err)
	}
}
