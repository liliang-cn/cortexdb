package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// What an agent is told of a tool's effects, local and remote (#255): a read
// is read-only and safe to resend, an upsert on a stated id is idempotent, a
// delete is destructive, and a write that appends is neither.
var servedEffects = map[string]struct{ readOnly, idempotent, destructive bool }{
	"knowledge_memory_recall": {true, true, false},
	"memory_search":           {true, true, false},
	"memory_get":              {true, true, false},
	"memory_save":             {false, true, true},
	"memory_delete":           {false, true, true},
	"decision_record":         {false, false, false},
	"importflow_run":          {false, false, true},
	"importflow_plan":         {true, true, false},
}

func checkServedEffects(t *testing.T, tools map[string]*mcp.Tool) {
	t.Helper()
	for name, want := range servedEffects {
		tool := tools[name]
		if tool == nil {
			t.Errorf("%s is not served", name)
			continue
		}
		a := tool.Annotations
		if a == nil {
			t.Errorf("%s is served without annotations", name)
			continue
		}
		destructive := a.DestructiveHint != nil && *a.DestructiveHint
		if a.ReadOnlyHint != want.readOnly || a.IdempotentHint != want.idempotent || (!want.readOnly && destructive != want.destructive) {
			t.Errorf("%s is served as readOnly=%v idempotent=%v destructive=%v, want %+v", name, a.ReadOnlyHint, a.IdempotentHint, destructive, want)
		}
	}
}

func TestLocalModeServesToolEffects(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "effects.db")
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tools := agentTools(t, connectAgent(t, newLocalMCPServer(db, dbPath)))
	checkServedEffects(t, tools)
	// Every tool says something: none reaches the agent unannotated.
	for name, tool := range tools {
		if tool.Annotations == nil {
			t.Errorf("%s is served without annotations", name)
		}
	}
}

func TestRemoteModeServesToolEffects(t *testing.T) {
	const token = "shared-secret"
	addr, _ := startImportBrain(t, token)
	conn, err := dialCortexDB(addr, token)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	server, err := newRemoteMCPServer(context.Background(), rpcv1.NewToolsServiceClient(conn), addr)
	if err != nil {
		t.Fatal(err)
	}
	checkServedEffects(t, agentTools(t, connectAgent(t, server)))
}
