package rpcserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// The imported-table tools are toolbox tools, so a remote client gets them
// through ListTools and CallTool with no code of their own on either side.
func TestImportedTableToolsAreServedRemotely(t *testing.T) {
	ctx := context.Background()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "remote.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{KnowledgeID: "k", Content: "not for the table tool"}); err != nil {
		t.Fatal(err)
	}
	w, err := db.BeginImportedTable(ctx, cortexdb.ImportedTableSpec{SourceTable: "orders", Columns: []cortexdb.ImportedColumn{
		{Name: "region", Type: cortexdb.ImportedTypeText}, {Name: "total", Type: cortexdb.ImportedTypeNumber},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(ctx, [][]any{{"eu", 10.0}, {"eu", 5.5}, {"us", 1.0}}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	svc := &toolsService{db: db}
	list, err := svc.ListTools(ctx, &rpcv1.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, tool := range list.GetTools() {
		listed[tool.GetName()] = true
	}
	for _, name := range []string{"imported_tables_list", "imported_tables_describe", "imported_tables_query"} {
		if !listed[name] {
			t.Errorf("%s is not listed", name)
		}
	}

	resp, err := svc.CallTool(ctx, &rpcv1.CallToolRequest{Name: "imported_tables_query",
		ArgsJson: `{"sql":"SELECT region, sum(total) FROM orders GROUP BY region ORDER BY region"}`})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.GetResultJson(); !strings.Contains(got, `"rows":[["eu",15.5],["us",1]]`) {
		t.Fatalf("result = %s", got)
	}
	if _, err := svc.CallTool(ctx, &rpcv1.CallToolRequest{Name: "imported_tables_query", ArgsJson: `{"sql":"SELECT content FROM documents"}`}); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("reading documents remotely: %v", err)
	}
}
