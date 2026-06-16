package connector

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCPServerListsConnectorTools stands up the connector MCP server over an
// in-memory transport, confirms all four connector tools are advertised, and
// drives connector_unmask end-to-end through the MCP layer (vault round-trip).
func TestMCPServerListsConnectorTools(t *testing.T) {
	dir := t.TempDir()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "kb.db")))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	vault, err := OpenSQLiteVault(filepath.Join(dir, "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()

	tb := NewToolbox(db, ToolboxOptions{Vault: vault, KeyProvider: testKP(), Tenant: "t"})
	server, err := NewMCPServer(tb, MCPServerOptions{})
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
	defer session.Close()

	toolList, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range toolList.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"connector_introspect", "connector_plan", "connector_run", "connector_unmask"} {
		if !names[want] {
			t.Fatalf("expected %s tool, got %+v", want, toolList.Tools)
		}
	}

	// Drive connector_unmask through MCP: a token minted in the vault must come
	// back as the original via the tool call.
	tok, err := vault.Put(ctx, "t", PiiName, "张三", testKP())
	if err != nil {
		t.Fatal(err)
	}
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "connector_unmask",
		Arguments: map[string]any{"tokens": []string{tok}},
	})
	if err != nil {
		t.Fatalf("call connector_unmask: %v", err)
	}
	out, _ := json.Marshal(res.StructuredContent)
	if indexOf(string(out), "张三") < 0 {
		t.Fatalf("unmask via MCP did not return original: %s", out)
	}
}
