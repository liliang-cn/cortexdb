package memoryflow

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Definitions() and the registrations inside NewMCPServer are two hand-kept
// lists, and nothing made them agree. A tool added to the first and forgotten in
// the second builds, tests, ships and is documented — and is simply absent from
// every host that speaks MCP rather than the Go API. That is how find_nodes went
// out unreachable in 2.62.0. memoryflow has the same two-list shape and, until
// this test, none of the same protection.

func coverageService(t *testing.T) *Service {
	t.Helper()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "cov.db")))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc, err := New(db, stubPlanner{plan: &cortexdb.RetrievalPlan{Query: "x"}}, stubExtractor{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc
}

func TestEveryMemoryflowToolIsReachableOverMCP(t *testing.T) {
	svc := coverageService(t)

	server, err := svc.NewMCPServer(MCPServerOptions{})
	if err != nil {
		t.Fatalf("new mcp server: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() { _ = server.Run(ctx, serverTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "coverage", Version: "v1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	list, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	exposed := make(map[string]bool, len(list.Tools))
	for _, tool := range list.Tools {
		exposed[tool.Name] = true
	}

	toolbox, err := NewToolbox(svc)
	if err != nil {
		t.Fatalf("new toolbox: %v", err)
	}
	for _, def := range toolbox.Definitions() {
		if !exposed[def.Name] {
			t.Errorf("%s is defined but not reachable over MCP — add it to NewMCPServer", def.Name)
		}
	}
}

// The apply path is exposed rather than the propose path on purpose: whoever
// calls this tool is already a model that has read the conversation, so making
// it decide the edits is both cheaper and better informed than a second LLM
// round-trip inside the server.
func TestApplyMemoryEditsToolIsCallable(t *testing.T) {
	ctx := context.Background()
	svc := coverageService(t)
	toolbox, err := NewToolbox(svc)
	if err != nil {
		t.Fatalf("new toolbox: %v", err)
	}

	in, _ := json.Marshal(map[string]any{
		"edits": []map[string]any{{"op": "add", "content": "从工具调用写进去的一条"}},
	})
	got, err := toolbox.Call(ctx, "memoryflow_apply_memory_edits", in)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	rep, ok := got.(*MemoryEditReport)
	if !ok {
		t.Fatalf("unexpected result type %T", got)
	}
	if rep.Added != 1 {
		t.Errorf("edit not applied: %+v", rep)
	}
}

// Retiring a memory through the tool must be as opt-in as it is through the Go
// API — a tool call is exactly where a confused model reaches first.
func TestApplyMemoryEditsToolKeepsSupersedeOptIn(t *testing.T) {
	ctx := context.Background()
	svc := coverageService(t)
	toolbox, _ := NewToolbox(svc)

	if _, err := svc.db.SaveMemory(ctx, cortexdb.MemorySaveRequest{
		MemoryID: "old", Content: "旧的", Scope: "global",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	in, _ := json.Marshal(map[string]any{
		"edits": []map[string]any{{"op": "supersede", "memory_id": "old", "content": "新的"}},
	})
	got, err := toolbox.Call(ctx, "memoryflow_apply_memory_edits", in)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	rep := got.(*MemoryEditReport)
	if rep.Superseded != 0 || len(rep.Skipped) == 0 {
		t.Errorf("supersede must not apply without allow_supersede: %+v", rep)
	}
}
