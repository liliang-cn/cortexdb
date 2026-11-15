package importflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func toolJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Only the tool that writes is a write: the policy of a server that offers
// these tools is derived from the flag, and a planner marked as a write
// would be refused to a read-only key for nothing, a run marked as a read
// allowed to one.
func TestToolboxDeclaresWhichToolsWrite(t *testing.T) {
	want := map[string]bool{
		"importflow_plan":        false,
		"importflow_run":         true,
		"importflow_ddl_plan":    false,
		"importflow_ddl_plan_ai": false,
	}
	defs := NewToolbox(nil).Definitions()
	if len(defs) != len(want) {
		t.Fatalf("the toolbox defines %d tools, want %d", len(defs), len(want))
	}
	for _, d := range defs {
		mutates, ok := want[d.Name]
		if !ok {
			t.Errorf("%s is a tool nobody classified", d.Name)
			continue
		}
		if d.Mutates != mutates {
			t.Errorf("%s: Mutates = %v, want %v", d.Name, d.Mutates, mutates)
		}
		if d.Description == "" || d.InputSchema == nil {
			t.Errorf("%s has no description or schema", d.Name)
		}
	}
}

// A path is opened by the process that serves the tool. It is refused unless
// the toolbox was told that process is the caller's own, and the refusal
// says what to do instead.
func TestToolboxReadsAPathOnlyWithLocalFiles(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orders.csv")
	if err := os.WriteFile(path, []byte(ordersCSV), 0o600); err != nil {
		t.Fatal(err)
	}
	run := toolJSON(t, map[string]any{"format": "csv", "path": path, "plan": map[string]any{"tables": map[string]any{}}, "keep_tables": true})
	plan := toolJSON(t, map[string]any{"format": "csv", "path": path, "build_tables": true})

	remoteDB := testDB(t)
	remote := NewToolbox(New(remoteDB))
	for name, input := range map[string]json.RawMessage{"importflow_run": run, "importflow_plan": plan} {
		_, err := remote.Call(ctx, name, input)
		if !errors.Is(err, ErrLocalFilesDisabled) || !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("%s with a path and no local files: %v", name, err)
		}
		if !strings.Contains(err.Error(), "send its text as data") {
			t.Fatalf("%s: the refusal does not say what to do: %v", name, err)
		}
	}
	if tables, err := remoteDB.ListImportedTables(ctx); err != nil || len(tables) != 0 {
		t.Fatalf("a refused path left tables behind: %+v, %v", tables, err)
	}
	// A path that does not exist is refused the same way: the answer must
	// not tell a caller which files the server has.
	_, missing := remote.Call(ctx, "importflow_run", toolJSON(t, map[string]any{"format": "csv", "path": filepath.Join(t.TempDir(), "nope.csv"), "plan": map[string]any{}}))
	if !errors.Is(missing, ErrLocalFilesDisabled) || strings.Contains(missing.Error(), "no such file") {
		t.Fatalf("a missing path and no local files: %v", missing)
	}

	localDB := testDB(t)
	local := NewToolbox(New(localDB), WithLocalFiles())
	out, err := local.Call(ctx, "importflow_run", run)
	if err != nil {
		t.Fatalf("importflow_run with a path and local files: %v", err)
	}
	if rep := out.(*Report); rep.TablesStored != 1 || rep.TableRowsStored != 5 {
		t.Fatalf("report = %+v", rep)
	}
	// With no table name the CSV takes the file's.
	if got := tableQuery(t, localDB, `SELECT region, round(sum(total), 2) FROM orders GROUP BY region ORDER BY region`); got != `[["eu",60.5],["us",5.25]]` {
		t.Fatalf("totals = %s", got)
	}
	out, err = local.Call(ctx, "importflow_plan", plan)
	if err != nil || out.(*planResult).Tables["orders"].Table == nil {
		t.Fatalf("importflow_plan with a path and local files: %+v, %v", out, err)
	}

	// Both at once is a request nobody can read one way.
	both := toolJSON(t, map[string]any{"format": "csv", "path": path, "data": ordersCSV, "plan": map[string]any{}})
	if _, err := local.Call(ctx, "importflow_run", both); !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("data and path together: %v", err)
	}
	if _, err := local.Call(ctx, "importflow_run", toolJSON(t, map[string]any{"format": "csv", "path": filepath.Join(t.TempDir(), "nope.csv"), "plan": map[string]any{}})); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("a missing local path: %v", err)
	}

	// The definitions tell the two apart, so an agent reads the rule before
	// it meets the refusal.
	pathText := func(tb *Toolbox) string {
		for _, d := range tb.Definitions() {
			if d.Name == "importflow_run" {
				return d.InputSchema["properties"].(map[string]any)["path"].(map[string]any)["description"].(string)
			}
		}
		return ""
	}
	if !strings.Contains(pathText(remote), "refused") || strings.Contains(pathText(local), "refused") {
		t.Fatalf("path is described as %q without local files and %q with", pathText(remote), pathText(local))
	}
}

func TestToolboxRefusesBadSources(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	tb := NewToolbox(New(db))

	// One byte over the limit is refused whole, and nothing is imported.
	big := "n\n" + strings.Repeat("x", MaxInlineBytes-2) + "y"
	if len(big) != MaxInlineBytes+1 {
		t.Fatalf("test data is %d bytes", len(big))
	}
	for name, input := range map[string]json.RawMessage{
		"importflow_run":         toolJSON(t, map[string]any{"format": "csv", "table": "big", "data": big, "plan": map[string]any{}, "keep_tables": true}),
		"importflow_plan":        toolJSON(t, map[string]any{"format": "csv", "table": "big", "data": big, "build_tables": true}),
		"importflow_ddl_plan":    toolJSON(t, map[string]any{"ddl": big}),
		"importflow_ddl_plan_ai": toolJSON(t, map[string]any{"ddl": big}),
	} {
		_, err := tb.Call(ctx, name, input)
		if !errors.Is(err, ErrInputTooLarge) || !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s with %d bytes inline: %v", name, len(big), err)
		}
	}
	if tables, err := db.ListImportedTables(ctx); err != nil || len(tables) != 0 {
		t.Fatalf("an oversize import left tables behind: %+v, %v", tables, err)
	}
	// Exactly the limit is taken.
	atLimit := big[:MaxInlineBytes]
	out, err := tb.Call(ctx, "importflow_run", toolJSON(t, map[string]any{"format": "csv", "table": "big", "data": atLimit, "plan": map[string]any{}, "keep_tables": true}))
	if err != nil || out.(*Report).TableRowsStored != 1 {
		t.Fatalf("an import of exactly the limit: %+v, %v", out, err)
	}

	for want, input := range map[string]map[string]any{
		"data is required":   {"format": "csv", "plan": map[string]any{}},
		"unsupported format": {"format": "xlsx", "data": "a\n1\n", "plan": map[string]any{}},
		"empty csv":          {"format": "csv", "data": "\n", "plan": map[string]any{}},
	} {
		if _, err := tb.Call(ctx, "importflow_run", toolJSON(t, input)); !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	if _, err := tb.Call(ctx, "importflow_run", json.RawMessage(`{"format":`)); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("malformed arguments: %v", err)
	}
	if _, err := tb.Call(ctx, "importflow_ddl_plan", json.RawMessage(`{"ddl":"SELECT 1;"}`)); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("DDL with no CREATE TABLE: %v", err)
	}
}

// Without a model importflow_plan still answers, from the column list, and
// says that is what it did.
func TestToolboxPlanWithoutAModel(t *testing.T) {
	ctx := context.Background()
	for name, tb := range map[string]*Toolbox{
		"no inferer":       NewToolbox(New(testDB(t))),
		"nil llm":          NewToolbox(New(testDB(t), WithLLM(nil))),
		"inferer no model": NewToolbox(New(testDB(t), WithMappingInferer(LLMInferer{}))),
	} {
		out, err := tb.Call(ctx, "importflow_plan", toolJSON(t, map[string]any{
			"format": "csv", "table": "orders", "data": ordersCSV, "build_rag": true, "build_kg": true, "build_tables": true,
		}))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		res := out.(*planResult)
		if res.Mode != "no_model" {
			t.Fatalf("%s: mode = %q", name, res.Mode)
		}
		tp := res.Tables["orders"]
		if tp.Table == nil || tp.KG != nil || tp.RAG == nil {
			t.Fatalf("%s: plan = %+v", name, tp)
		}
		if tp.RAG.ContentTmpl != "{id} {customer_id} {region} {total} {ordered_at} {note}" {
			t.Fatalf("%s: content_tmpl = %q", name, tp.RAG.ContentTmpl)
		}
		notes := strings.Join(res.Notes, "\n")
		for _, want := range []string{"no model is configured", "build_rag", "importflow_ddl_plan"} {
			if !strings.Contains(notes, want) {
				t.Errorf("%s: notes do not mention %q: %s", name, want, notes)
			}
		}
		// The result, whole, is importflow_run's plan.
		raw := toolJSON(t, res)
		if !strings.Contains(string(raw), `"tables":{"orders"`) || !strings.Contains(string(raw), `"mode":"no_model"`) {
			t.Fatalf("%s: result JSON = %s", name, raw)
		}
		run, err := tb.Call(ctx, "importflow_run", toolJSON(t, map[string]any{"format": "csv", "table": "orders", "data": ordersCSV, "plan": json.RawMessage(raw)}))
		if err != nil {
			t.Fatalf("%s: run the plan: %v", name, err)
		}
		if rep := run.(*Report); rep.TablesStored != 1 || rep.ChunksIndexed != 5 || rep.TriplesCreated != 0 || len(rep.Errors) != 0 {
			t.Fatalf("%s: report = %+v", name, rep)
		}
	}

	// Nothing asked for is nothing planned: the table is named and empty.
	out, err := NewToolbox(nil).Call(ctx, "importflow_plan", toolJSON(t, map[string]any{"format": "csv", "table": "orders", "data": ordersCSV}))
	if err != nil {
		t.Fatal(err)
	}
	if tp, ok := out.(*planResult).Tables["orders"]; !ok || tp.RAG != nil || tp.KG != nil || tp.Table != nil {
		t.Fatalf("a plan with no goal = %+v", out)
	}
}

// With a model the plan is the model's, and the importer refines with it.
func TestToolboxPlanWithAModel(t *testing.T) {
	gen := fakeGen{Out: []byte(`{"tables":{"orders":{"rag":{"content_tmpl":"{region}"}}}}`)}
	im := New(testDB(t), WithLLM(gen))
	if _, ok := im.refiner.(LLMRefiner); !ok {
		t.Fatalf("WithLLM set refiner %T", im.refiner)
	}
	out, err := NewToolbox(im).Call(context.Background(), "importflow_plan", toolJSON(t, map[string]any{
		"format": "csv", "table": "orders", "data": ordersCSV, "build_rag": true, "build_tables": true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	res := out.(*planResult)
	if res.Mode != "model" || len(res.Notes) != 0 || res.Tables["orders"].RAG.ContentTmpl != "{region}" || res.Tables["orders"].Table == nil {
		t.Fatalf("plan = %+v", res)
	}
}

// A row that fails is reported by its message, not as an empty object.
func TestReportJSONCarriesErrorMessages(t *testing.T) {
	rep := Report{RowsRead: 2, Errors: []error{errors.New("row 1: bad value"), nil}}
	raw := toolJSON(t, &rep)
	if !strings.Contains(string(raw), `"Errors":["row 1: bad value"]`) || !strings.Contains(string(raw), `"RowsRead":2`) {
		t.Fatalf("report JSON = %s", raw)
	}
	var back Report
	if err := json.Unmarshal(raw, &back); err != nil || back.RowsRead != 2 || len(back.Errors) != 1 || back.Errors[0].Error() != "row 1: bad value" {
		t.Fatalf("round trip = %+v, %v", back, err)
	}
}

// mcpSession serves server and returns a client session on it.
func mcpSession(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = server.Run(ctx, serverTransport) }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1.0.0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// mcpText is the text of a tool result, failing the test on a tool error.
func mcpText(t *testing.T, res *mcp.CallToolResult, err error) string {
	t.Helper()
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	if res.IsError {
		t.Fatalf("tool error: %s", text)
	}
	return text
}

// The local MCP path, with no model anywhere: a CSV sent inline becomes a
// table with typed columns, and the facade's own tool totals it.
func TestMCPToolsImportACSVIntoAQueryableTableWithNoModel(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	// As cortexdb-mcp-stdio builds it: the facade's server, and these beside.
	server := db.NewMCPServer(cortexdb.MCPServerOptions{})
	AddMCPTools(server, NewToolbox(New(db, WithLLM(nil)), WithLocalFiles()))
	session := mcpSession(t, server)

	list, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, tool := range list.Tools {
		listed[tool.Name] = true
	}
	for _, name := range []string{"importflow_plan", "importflow_run", "importflow_ddl_plan", "importflow_ddl_plan_ai", "imported_tables_query"} {
		if !listed[name] {
			t.Fatalf("%s is not listed: %v", name, listed)
		}
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "importflow_plan", Arguments: map[string]any{
		"format": "csv", "table": "orders", "data": ordersCSV, "build_tables": true,
	}})
	planText := mcpText(t, res, err)
	var plan map[string]any
	if err := json.Unmarshal([]byte(planText), &plan); err != nil || plan["mode"] != "no_model" {
		t.Fatalf("plan = %s (%v)", planText, err)
	}

	// The plan as the tool returned it, mode and notes included, is the plan
	// the run takes.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "importflow_run", Arguments: map[string]any{
		"format": "csv", "table": "orders", "data": ordersCSV, "plan": plan,
	}})
	if text := mcpText(t, res, err); !strings.Contains(text, `"TablesStored":1`) || !strings.Contains(text, `"TableRowsStored":5`) {
		t.Fatalf("run = %s", text)
	}

	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "imported_tables_query", Arguments: map[string]any{
		"sql": "SELECT region, count(*), round(sum(total), 2) FROM orders GROUP BY region ORDER BY region",
	}})
	if text := mcpText(t, res, err); !strings.Contains(text, `"rows":[["eu",3,60.5],["us",2,5.25]]`) {
		t.Fatalf("query = %s", text)
	}

	// keep_tables with an empty plan, the shortest form, and the DDL planners.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "importflow_run", Arguments: map[string]any{
		"format": "csv", "table": "orders_again", "data": ordersCSV, "plan": map[string]any{"tables": map[string]any{}}, "keep_tables": true,
	}})
	if text := mcpText(t, res, err); !strings.Contains(text, `"TablesStored":1`) {
		t.Fatalf("run with keep_tables = %s", text)
	}
	ddl := "CREATE TABLE orders (id INT PRIMARY KEY, customer_id INT REFERENCES customers(id), total NUMERIC);"
	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "importflow_ddl_plan", Arguments: map[string]any{"ddl": ddl}})
	if text := mcpText(t, res, err); !strings.Contains(text, `"mapping_plan"`) || !strings.Contains(text, `"customers"`) {
		t.Fatalf("ddl plan = %s", text)
	}
	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "importflow_ddl_plan_ai", Arguments: map[string]any{"ddl": ddl}})
	if text := mcpText(t, res, err); !strings.Contains(text, `"llm_used":false`) || !strings.Contains(text, "no model is configured") {
		t.Fatalf("ddl plan with no model = %s", text)
	}

	// An error reaches the MCP client as a tool error with the message.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "importflow_run", Arguments: map[string]any{
		"format": "csv", "data": ordersCSV, "path": "/etc/hosts", "plan": map[string]any{},
	}})
	if err != nil || !res.IsError {
		t.Fatalf("data and path together over MCP: %+v, %v", res, err)
	}
}

// NewMCPServer serves every tool of the toolbox, and reads a path only when
// told the server is the caller's own.
func TestMCPServerServesTheWholeToolbox(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orders.csv")
	if err := os.WriteFile(path, []byte(ordersCSV), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, localFiles := range []bool{false, true} {
		db := testDB(t)
		server, err := NewMCPServer(New(db), MCPServerOptions{LocalFiles: localFiles})
		if err != nil {
			t.Fatal(err)
		}
		serverTransport, clientTransport := mcp.NewInMemoryTransports()
		runCtx, cancel := context.WithCancel(ctx)
		go func() { _ = server.Run(runCtx, serverTransport) }()
		session, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1.0.0"}, nil).Connect(runCtx, clientTransport, nil)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		list, err := session.ListTools(ctx, &mcp.ListToolsParams{})
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		var names []string
		for _, tool := range list.Tools {
			names = append(names, tool.Name)
		}
		if want := len(NewToolbox(nil).Definitions()); len(names) != want {
			t.Errorf("the server lists %v, the toolbox defines %d tools", names, want)
		}
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "importflow_run", Arguments: map[string]any{
			"format": "csv", "path": path, "plan": map[string]any{}, "keep_tables": true,
		}})
		if err != nil || res.IsError == localFiles {
			t.Errorf("LocalFiles=%v: a run by path: IsError=%v, %v", localFiles, res != nil && res.IsError, err)
		}
		_ = session.Close()
		cancel()
	}
}
