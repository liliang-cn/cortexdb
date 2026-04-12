package graphflow

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

	server, err := NewMCPServer(db, FilesystemDetector{}, HeuristicExtractor{}, MCPServerOptions{})
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
	if len(toolList.Tools) == 0 {
		t.Fatal("expected graphflow tools")
	}
	foundRun := false
	for _, tool := range toolList.Tools {
		if tool.Name == "graphflow_run" {
			foundRun = true
			break
		}
	}
	if !foundRun {
		t.Fatalf("expected graphflow_run tool, got %+v", toolList.Tools)
	}
}
