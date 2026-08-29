package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
	"github.com/liliang-cn/cortexdb/v2/pkg/rpcserver"
)

// startTestBrain runs a real cortexdb-grpc server over a temp database and
// returns its address plus the underlying DB, so a test can assert that what
// the remote bridge writes actually lands in the shared file.
func startTestBrain(t *testing.T, token string) (string, *cortexdb.DB) {
	t.Helper()
	dbPath := fmt.Sprintf("test_remote_%d.db", time.Now().UnixNano())
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := rpcserver.New(db, rpcserver.Options{Token: token, DBPath: dbPath})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = db.Close()
		for _, s := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + s)
		}
	})
	return lis.Addr().String(), db
}

// TestRemoteBridgeDiscoversAndProxiesTools is the shared-brain contract: a
// client with no local database discovers the full tool surface over gRPC and a
// tool call lands in the central database.
func TestRemoteBridgeDiscoversAndProxiesTools(t *testing.T) {
	const token = "shared-secret"
	addr, db := startTestBrain(t, token)
	ctx := context.Background()

	conn, err := dialCortexDB(addr, token)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	client := rpcv1.NewToolsServiceClient(conn)

	list, err := client.ListTools(ctx, &rpcv1.ListToolsRequest{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(list.GetTools()) < 10 {
		t.Fatalf("expected the full tool surface, got %d tools", len(list.GetTools()))
	}
	// Every tool must carry a usable schema so the MCP client sees the same
	// contract as in local mode.
	for _, d := range list.GetTools() {
		if d.GetName() == "memory_save" {
			var schema any
			if err := json.Unmarshal([]byte(d.GetInputSchemaJson()), &schema); err != nil {
				t.Fatalf("memory_save schema is not valid JSON: %v", err)
			}
		}
	}

	// A write through the proxy must land in the central database.
	args := `{"memory_id":"remote-write","scope":"global","content":"written through the remote bridge"}`
	if _, err := client.CallTool(ctx, &rpcv1.CallToolRequest{Name: "memory_save", ArgsJson: args}); err != nil {
		t.Fatalf("call tool: %v", err)
	}
	var content string
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT content FROM messages WHERE id = ?`, "remote-write").Scan(&content); err != nil {
		t.Fatalf("the remote write did not reach the shared database: %v", err)
	}
	if content != "written through the remote bridge" {
		t.Fatalf("unexpected stored content: %q", content)
	}
}

// TestRemoteBridgeRejectsBadToken verifies the shared brain is not readable
// without the token — the only thing protecting a plaintext connection.
func TestRemoteBridgeRejectsBadToken(t *testing.T) {
	addr, _ := startTestBrain(t, "right-token")

	conn, err := dialCortexDB(addr, "wrong-token")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := rpcv1.NewToolsServiceClient(conn).ListTools(ctx, &rpcv1.ListToolsRequest{}); err == nil {
		t.Fatalf("expected an authentication error with a wrong token")
	}
}

// A node's label is often the first line of the text it came from, and that
// text has newlines in it. They used to survive into every view that printed
// the label, breaking the layout of whatever panel was showing it.
func TestClipLabelCollapsesWhitespace(t *testing.T) {
	got := clipLabel("Situation: 帮我记住\n\tTools used")
	if strings.ContainsAny(got, "\n\t") {
		t.Errorf("clipLabel kept a line break: %q", got)
	}
	if got != "Situation: 帮我记住 Tools used" {
		t.Errorf("clipLabel = %q", got)
	}
}

func TestClipLabelClipsByRunesNotBytes(t *testing.T) {
	got := clipLabel(strings.Repeat("记", 60))
	if r := []rune(got); len(r) != 49 { // 48 kept plus the ellipsis
		t.Errorf("clipLabel returned %d runes, want 49", len(r))
	}
}
