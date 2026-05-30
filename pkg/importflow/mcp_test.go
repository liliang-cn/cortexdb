package importflow

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPServerListTools(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), fmt.Sprintf("%s.db", t.Name()))
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	server, err := NewMCPServer(New(db), MCPServerOptions{})
	if err != nil {
		t.Fatalf("new mcp server: %v", err)
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- server.Run(ctx, serverTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer func() { _ = session.Close() }()

	toolList, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range toolList.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"importflow_plan", "importflow_run"} {
		if !names[want] {
			t.Fatalf("expected %s tool, got %+v", want, toolList.Tools)
		}
	}
}
