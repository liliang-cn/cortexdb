package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
	"github.com/liliang-cn/cortexdb/v2/pkg/rpcserver"
)

const importTestCSV = "id,region,total,ordered_at\n1,eu,10.50,2026-01-05\n2,eu,30,2026-01-20\n3,us,5.25,2026-02-01\n4,eu,20,2026-02-11\n"

// importTestBrain is this binary's server over a temp database: the facade's
// toolbox and importTools, behind three keys.
func importTestBrain(t *testing.T) (rpcv1.ToolsServiceClient, *cortexdb.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "brain.db")
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := authz.NewKeySet([]authz.Key{
		{ID: "operator", Secret: "op-secret", Clearance: authz.ReadWrite},
		{ID: "reader", Secret: "ro-secret", Clearance: authz.ReadOnly},
		{ID: "tenant", Secret: "tenant-secret", Clearance: authz.ReadWrite, Scope: authz.Scope{Collection: "acme"}},
		{ID: "hermes", Secret: "hermes-secret", Clearance: authz.ReadWrite, Scope: authz.Scope{UserID: "hermes"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := rpcserver.NewWithPolicy(db, rpcserver.Options{Keys: keys, DBPath: dbPath, ExtraTools: importTools(db)})
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
		_ = db.Close()
	})
	return rpcv1.NewToolsServiceClient(conn), db
}

func asImportKey(secret string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+secret)
}

func importArgs(t *testing.T, v map[string]any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Every tool of the toolbox is served and has its own reason for a confined
// key: a tool added to importflow later fails here until someone says why.
func TestEveryImportToolIsServedAndClassified(t *testing.T) {
	client, db := importTestBrain(t)
	defs := importflow.NewToolbox(nil).Definitions()
	if len(defs) != len(importToolRefusals) {
		t.Errorf("importflow defines %d tools and %d have a refusal", len(defs), len(importToolRefusals))
	}
	writes := map[string]bool{"importflow_run": true}
	for _, d := range defs {
		if importToolRefusals[d.Name] == "" {
			t.Errorf("%s has no reason to give a confined key", d.Name)
		}
		if d.Mutates != writes[d.Name] {
			t.Errorf("%s: Mutates = %v", d.Name, d.Mutates)
		}
	}

	list, err := client.ListTools(asImportKey("ro-secret"), &rpcv1.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(list.GetTools()), len(db.GraphRAGTools().Definitions())+len(defs); got != want {
		t.Errorf("the server lists %d tools, want the toolbox's and importflow's: %d", got, want)
	}
	schemas := map[string]string{}
	for _, tool := range list.GetTools() {
		schemas[tool.GetName()] = tool.GetInputSchemaJson()
	}
	for _, d := range defs {
		if !strings.Contains(schemas[d.Name], `"type":"object"`) {
			t.Errorf("%s is not listed with a schema: %q", d.Name, schemas[d.Name])
		}
	}
	// The server reads no files, and its catalogue says so.
	if !strings.Contains(schemas["importflow_run"], "a call that names a path is refused") {
		t.Errorf("importflow_run's path is described as %s", schemas["importflow_run"])
	}
}

// The point of it all: through the shared brain's own RPCs, a CSV sent inline
// becomes a table and the table answers a GROUP BY.
func TestAFullKeyImportsACSVAndQueriesIt(t *testing.T) {
	client, _ := importTestBrain(t)
	op := asImportKey("op-secret")

	resp, err := client.CallTool(op, &rpcv1.CallToolRequest{Name: "importflow_plan",
		ArgsJson: importArgs(t, map[string]any{"format": "csv", "table": "orders", "data": importTestCSV, "build_tables": true})})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.GetResultJson(), `"mode":"no_model"`) || !strings.Contains(resp.GetResultJson(), `"orders":{"table":{}}`) {
		t.Fatalf("plan = %s", resp.GetResultJson())
	}
	resp, err = client.CallTool(op, &rpcv1.CallToolRequest{Name: "importflow_run",
		ArgsJson: importArgs(t, map[string]any{"format": "csv", "table": "orders", "data": importTestCSV, "plan": json.RawMessage(resp.GetResultJson())})})
	if err != nil {
		t.Fatal(err)
	}
	var rep importflow.Report
	if err := json.Unmarshal([]byte(resp.GetResultJson()), &rep); err != nil || rep.RowsRead != 4 || rep.TablesStored != 1 || rep.TableRowsStored != 4 || len(rep.Errors) != 0 {
		t.Fatalf("report = %s (%v)", resp.GetResultJson(), err)
	}

	resp, err = client.CallTool(op, &rpcv1.CallToolRequest{Name: "imported_tables_query",
		ArgsJson: `{"sql":"SELECT region, count(*), sum(total) FROM orders GROUP BY region ORDER BY region"}`})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.GetResultJson(); !strings.Contains(got, `"rows":[["eu",3,60.5],["us",1,5.25]]`) {
		t.Fatalf("query = %s", got)
	}
	resp, err = client.CallTool(op, &rpcv1.CallToolRequest{Name: "imported_tables_describe", ArgsJson: `{"table":"orders"}`})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"name":"id","type":"integer"`, `"name":"total","type":"number"`, `"name":"region","type":"text"`} {
		if !strings.Contains(resp.GetResultJson(), want) {
			t.Errorf("describe lacks %s: %s", want, resp.GetResultJson())
		}
	}

	// A plain RAG import through the same tool lands in the knowledge store.
	if _, err := client.CallTool(op, &rpcv1.CallToolRequest{Name: "importflow_run",
		ArgsJson: importArgs(t, map[string]any{"format": "csv", "table": "people", "data": "id,bio\n1,Ada wrote the first program\n",
			"plan": map[string]any{"tables": map[string]any{"people": map[string]any{"rag": map[string]any{"content_tmpl": "{bio}", "id_column": "id"}}}}})}); err != nil {
		t.Fatal(err)
	}
	resp, err = client.CallTool(op, &rpcv1.CallToolRequest{Name: "knowledge_search", ArgsJson: `{"query":"first program","top_k":3}`})
	if err != nil || !strings.Contains(resp.GetResultJson(), "Ada wrote the first program") {
		t.Fatalf("the imported row is not searchable: %s, %v", resp.GetResultJson(), err)
	}
}

func TestAReadOnlyKeyMayPlanAnImportButNotRunIt(t *testing.T) {
	client, db := importTestBrain(t)
	ro := asImportKey("ro-secret")

	_, err := client.CallTool(ro, &rpcv1.CallToolRequest{Name: "importflow_run",
		ArgsJson: importArgs(t, map[string]any{"format": "csv", "table": "orders", "data": importTestCSV, "plan": map[string]any{}, "keep_tables": true})})
	if status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), "read-only and tool importflow_run writes") {
		t.Fatalf("a read-only key ran an import: %v", err)
	}
	if tables, err := db.ListImportedTables(context.Background()); err != nil || len(tables) != 0 {
		t.Fatalf("the refused import left tables: %+v, %v", tables, err)
	}

	const ddl = "CREATE TABLE orders (id INT PRIMARY KEY, customer_id INT REFERENCES customers(id));"
	for name, args := range map[string]string{
		"importflow_plan":        importArgs(t, map[string]any{"format": "csv", "table": "orders", "data": importTestCSV, "build_rag": true}),
		"importflow_ddl_plan":    importArgs(t, map[string]any{"ddl": ddl}),
		"importflow_ddl_plan_ai": importArgs(t, map[string]any{"ddl": ddl}),
	} {
		resp, err := client.CallTool(ro, &rpcv1.CallToolRequest{Name: name, ArgsJson: args})
		if err != nil {
			t.Errorf("a read-only key was refused %s, which writes nothing: %v", name, err)
			continue
		}
		// This binary configures no model: each planner says so.
		switch name {
		case "importflow_plan":
			if !strings.Contains(resp.GetResultJson(), `"mode":"no_model"`) {
				t.Errorf("%s = %s", name, resp.GetResultJson())
			}
		case "importflow_ddl_plan_ai":
			if !strings.Contains(resp.GetResultJson(), `"llm_used":false`) || !strings.Contains(resp.GetResultJson(), "no model is configured") {
				t.Errorf("%s = %s", name, resp.GetResultJson())
			}
		}
	}
}

func TestAConfinedKeyIsRefusedEveryImportToolWithItsReason(t *testing.T) {
	client, db := importTestBrain(t)
	for _, secret := range []string{"tenant-secret", "hermes-secret"} {
		for name, reason := range importToolRefusals {
			_, err := client.CallTool(asImportKey(secret), &rpcv1.CallToolRequest{Name: name,
				ArgsJson: importArgs(t, map[string]any{"format": "csv", "data": importTestCSV, "ddl": "CREATE TABLE t (id INT PRIMARY KEY);",
					"plan": map[string]any{}, "keep_tables": true, "collection": "acme", "user_id": "hermes"})})
			if status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), "is confined and "+name) || !strings.Contains(err.Error(), reason) {
				t.Errorf("%s and %s: %v", secret, name, err)
			}
		}
	}
	if tables, err := db.ListImportedTables(context.Background()); err != nil || len(tables) != 0 {
		t.Fatalf("a refused import left tables: %+v, %v", tables, err)
	}
}

// A path would be opened on the server's own disk: it is refused to every
// key, the full one included, whether or not the file exists.
func TestTheServerReadsNoFileForACaller(t *testing.T) {
	client, db := importTestBrain(t)
	real := filepath.Join(t.TempDir(), "secret.csv")
	if err := os.WriteFile(real, []byte("token\nnot-for-callers\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var messages []string
	for _, path := range []string{real, filepath.Join(t.TempDir(), "missing.csv"), "/etc/passwd"} {
		for name, extra := range map[string]map[string]any{
			"importflow_run":  {"plan": map[string]any{}, "keep_tables": true},
			"importflow_plan": {"build_tables": true},
		} {
			args := map[string]any{"format": "csv", "path": path}
			for k, v := range extra {
				args[k] = v
			}
			resp, err := client.CallTool(asImportKey("op-secret"), &rpcv1.CallToolRequest{Name: name, ArgsJson: importArgs(t, args)})
			if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "send its text as data") {
				t.Fatalf("%s with path %s: %v (%s)", name, path, err, resp.GetResultJson())
			}
			if strings.Contains(err.Error(), "not-for-callers") || strings.Contains(err.Error(), "no such file") {
				t.Fatalf("the refusal says something about the file: %v", err)
			}
			messages = append(messages, strings.ReplaceAll(status.Convert(err).Message(), path, "<path>"))
		}
	}
	// A file that exists and one that does not are refused in the same words.
	for _, m := range messages[1:] {
		if m != messages[0] {
			t.Fatalf("the refusals differ:\n%s\n%s", messages[0], m)
		}
	}
	if tables, err := db.ListImportedTables(context.Background()); err != nil || len(tables) != 0 {
		t.Fatalf("a refused path left tables: %+v, %v", tables, err)
	}
}

func TestOversizeInlineDataIsRefusedWhole(t *testing.T) {
	client, db := importTestBrain(t)
	op := asImportKey("op-secret")
	run := func(data string) error {
		_, err := client.CallTool(op, &rpcv1.CallToolRequest{Name: "importflow_run",
			ArgsJson: importArgs(t, map[string]any{"format": "csv", "table": "big", "data": data, "plan": map[string]any{}, "keep_tables": true})})
		return err
	}
	// Over the tool's limit and under the transport's: the tool refuses it.
	err := run("n\n" + strings.Repeat("x", importflow.MaxInlineBytes))
	if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), fmt.Sprintf("the limit is %d", importflow.MaxInlineBytes)) {
		t.Fatalf("one over the inline limit: %v", err)
	}
	// Over the transport's 4 MiB: gRPC refuses the message before the tool.
	if err := run("n\n" + strings.Repeat("x", 5<<20)); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("a message over the gRPC limit: %v", err)
	}
	if tables, err := db.ListImportedTables(context.Background()); err != nil || len(tables) != 0 {
		t.Fatalf("an oversize import left tables: %+v, %v", tables, err)
	}
	// At the limit it is imported.
	if err := run("n\n" + strings.Repeat("x", importflow.MaxInlineBytes-2)); err != nil {
		t.Fatalf("exactly the inline limit: %v", err)
	}
	// The limit leaves room for the client's escaping: a CSV of emoji at the
	// limit, sent by a client that writes every non-ASCII rune as a \u
	// surrogate pair (Python's json.dumps does), triples on the wire and still
	// reaches the tool.
	const emoji = "\U0001F600" // four bytes of UTF-8, twelve escaped
	text := "n\n" + strings.Repeat(emoji, (importflow.MaxInlineBytes-2)/len(emoji))
	var escaped strings.Builder
	for _, r := range text {
		switch {
		case r == '\n':
			escaped.WriteString(`\n`)
		case r > 0xFFFF:
			r1, r2 := utf16.EncodeRune(r)
			fmt.Fprintf(&escaped, `\u%04x\u%04x`, r1, r2)
		default:
			escaped.WriteRune(r)
		}
	}
	if escaped.Len() < 3*importflow.MaxInlineBytes-(3*importflow.MaxInlineBytes)/10 {
		t.Fatalf("the escaped text is only %d bytes", escaped.Len())
	}
	_, err = client.CallTool(op, &rpcv1.CallToolRequest{Name: "importflow_run",
		ArgsJson: `{"format":"csv","table":"emoji","plan":{},"keep_tables":true,"data":"` + escaped.String() + `"}`})
	if err != nil {
		t.Fatalf("an escaped text at the limit did not reach the tool: %v", err)
	}
}

func TestImportToolErrorsCarryTheCallersCode(t *testing.T) {
	if got := status.Code(importToolStatus(fmt.Errorf("wrapped: %w", cortexdb.ErrImportedTablesUnsupported))); got != codes.FailedPrecondition {
		t.Errorf("an unsupported backend is %s", got)
	}
	if got := status.Code(importToolStatus(fmt.Errorf("wrapped: %w", importflow.ErrInputTooLarge))); got != codes.InvalidArgument {
		t.Errorf("an oversize request is %s", got)
	}
	if err := importToolStatus(nil); err != nil {
		t.Errorf("no error became %v", err)
	}
}
