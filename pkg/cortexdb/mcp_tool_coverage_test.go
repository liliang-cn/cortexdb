package cortexdb

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestEveryToolDefinitionIsReachableOverMCP guards a gap that is invisible from
// either side on its own.
//
// `Definitions()` and the registrations in NewMCPServer are two hand-kept lists,
// and nothing made them agree. A tool added to the first and forgotten in the
// second builds, tests, ships and is documented — and is simply absent from
// every host that speaks MCP rather than the Go API. That is how find_nodes went
// out in 2.62.0 unreachable, and it is why LearningPath and UpdateGraphFromText,
// both shipped in 2.61.0, can only be run from the CLI.
//
// Nothing about the symptom points at the cause: a host asking for the tool is
// told it does not exist, by a server whose release notes say it does.
func TestEveryToolDefinitionIsReachableOverMCP(t *testing.T) {
	dbPath := fmt.Sprintf("test_mcp_coverage_%d.db", testname.Nano())
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	})

	server := db.NewMCPServer(MCPServerOptions{})
	ctx := context.Background()

	// Ask the server what it exposes, the way a host does, rather than reading
	// the registration list — the question is what a client can actually reach.
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() { _ = serverSession.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "coverage-test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = clientSession.Close() }()

	listed, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	reachable := make(map[string]struct{}, len(listed.Tools))
	for _, tool := range listed.Tools {
		reachable[tool.Name] = struct{}{}
	}

	missing := make([]string, 0)
	for _, definition := range db.GraphRAGTools().Definitions() {
		if _, ok := reachable[definition.Name]; !ok {
			missing = append(missing, definition.Name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("defined but not reachable over MCP: %v — add the registration in NewMCPServer", missing)
	}
}
