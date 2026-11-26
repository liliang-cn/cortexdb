package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
	"google.golang.org/grpc"
)

// The sweep command, as a timer would run it: a bad flag is usage (2), a dry
// run lists the expired document and memory as JSON and removes nothing, the
// real run removes them and says so in text, and a brain that cannot be opened is 1.
func TestTheSweepCommandReportsAndRemovesExpiredRows(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := sweepExpiredCommand([]string{"--bogus"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage:") {
		t.Fatalf("bad flag: code %d, stderr %q", code, errOut.String())
	}

	// The machine's own brain, local or shared, is never the one a test
	// sweeps: every variable that names one is cleared first.
	dir := t.TempDir()
	path := filepath.Join(dir, "brain.db")
	t.Setenv("CORTEXDB_PATH", path)
	t.Setenv("CORTEXDB_REMOTE", "")
	t.Setenv("CORTEXDB_GRPC_TOKEN", "")
	withoutTheMachinesModels(t)
	db, err := openBrainDB(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if _, err := db.GraphRAGTools().IngestDocument(context.Background(), cortexdb.ToolIngestDocumentRequest{
		DocumentID: "old", Content: "An expired note about the ledger.", ExpiresAt: &past,
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if _, err := db.SaveMemory(context.Background(), cortexdb.MemorySaveRequest{
		MemoryID: "m-old", UserID: "u", Scope: "user", Content: "an expired memory", TTLSeconds: 1,
	}); err != nil {
		t.Fatalf("save memory: %v", err)
	}
	// The memory's expiry is set in the past instead of waiting for its ttl:
	// the command sweeps at the wall clock's instant.
	if _, err := db.SQL().Exec(`UPDATE messages SET metadata = json_set(metadata, '$.expires_at', '2001-01-01T00:00:00Z') WHERE id = 'm-old'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errOut.Reset()
	if code := sweepExpiredCommand([]string{"--dry-run", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("dry run: code %d, stderr %q", code, errOut.String())
	}
	var report cortexdb.ExpiredSweepReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("dry run output is not JSON: %v: %q", err, out.String())
	}
	if !report.DryRun || strings.Join(report.Documents, ",") != "old" || strings.Join(report.Memories, ",") != "m-old" {
		t.Fatalf("dry run report: %+v", report)
	}

	// A delete that fails is exit 1, and the report still says what was done.
	blockDeletes := func(sql string) {
		db, err := openBrainDB(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.SQL().Exec(sql); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	blockDeletes(`CREATE TRIGGER block_documents BEFORE DELETE ON documents BEGIN SELECT RAISE(ABORT, 'blocked'); END`)
	out.Reset()
	errOut.Reset()
	if code := sweepExpiredCommand(nil, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "sweep document old") || !strings.Contains(out.String(), "documents=0 memories=0") {
		t.Fatalf("blocked sweep: code %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
	blockDeletes(`DROP TRIGGER block_documents`)

	out.Reset()
	errOut.Reset()
	if code := sweepExpiredCommand(nil, &out, &errOut); code != 0 {
		t.Fatalf("sweep: code %d, stderr %q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "documents=1 memories=1 dryRun=false") || !strings.Contains(out.String(), "  document old") || !strings.Contains(out.String(), "  memory m-old") {
		t.Fatalf("sweep output: %q", out.String())
	}

	out.Reset()
	if code := sweepExpiredCommand([]string{"--json"}, &out, &errOut); code != 0 {
		t.Fatalf("second sweep: code %d", code)
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || len(report.Documents)+len(report.Memories) != 0 {
		t.Fatalf("second sweep still found %+v (%v)", report, err)
	}

	// A path that is a directory cannot be opened as a brain.
	t.Setenv("CORTEXDB_PATH", dir)
	errOut.Reset()
	if code := sweepExpiredCommand(nil, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "open") {
		t.Fatalf("unopenable brain: code %d, stderr %q", code, errOut.String())
	}

	// Without CORTEXDB_PATH the sweep reads the default brain under $HOME.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CORTEXDB_PATH", "")
	out.Reset()
	if code := sweepExpiredCommand([]string{"--dry-run"}, &out, &errOut); code != 0 {
		t.Fatalf("default brain: code %d, stderr %q", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".cortexdb", "cortexdb.db")); err != nil {
		t.Fatalf("the default brain was not opened under $HOME: %v", err)
	}
}

// On a machine that talks to a shared brain, the sweep runs on that brain,
// through the sweep_expired tool over gRPC, and the local file, if any, is
// not touched: a dry run lists the shared brain's expired document, the real
// run removes it, and a bad token or a bad address is 1.
func TestTheSweepRunsOnTheSharedBrainWhenRemoteIsSet(t *testing.T) {
	ctx := context.Background()
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	path := filepath.Join(t.TempDir(), "brain.db")
	t.Setenv("CORTEXDB_PATH", path)
	withoutTheMachinesModels(t)
	local, err := openBrainDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := local.GraphRAGTools().IngestDocument(ctx, cortexdb.ToolIngestDocumentRequest{DocumentID: "local-old", Content: "An expired local note.", ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}

	const token = "shared-secret"
	addr, shared := startTestBrain(t, token)
	if _, err := shared.GraphRAGTools().IngestDocument(ctx, cortexdb.ToolIngestDocumentRequest{DocumentID: "old", Content: "An expired shared note.", ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CORTEXDB_REMOTE", addr)
	t.Setenv("CORTEXDB_GRPC_TOKEN", token)

	var out, errOut bytes.Buffer
	if code := sweepExpiredCommand([]string{"--dry-run", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("dry run: code %d, stderr %q", code, errOut.String())
	}
	var report cortexdb.ExpiredSweepReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || !report.DryRun || strings.Join(report.Documents, ",") != "old" {
		t.Fatalf("dry run report: %+v (%v) %q", report, err, out.String())
	}
	if _, err := shared.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "old"}); err != nil {
		t.Fatalf("the dry run removed the shared document: %v", err)
	}

	out.Reset()
	if code := sweepExpiredCommand(nil, &out, &errOut); code != 0 || !strings.Contains(out.String(), "documents=1 memories=0 dryRun=false") || !strings.Contains(out.String(), "  document old") {
		t.Fatalf("sweep: code %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
	if _, err := shared.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "old"}); err == nil {
		t.Fatal("the shared brain still holds the expired document")
	}

	local, err = openBrainDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = local.Close() }()
	if _, err := local.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "local-old"}); err != nil {
		t.Fatalf("the local file was swept: %v", err)
	}

	t.Setenv("CORTEXDB_GRPC_TOKEN", "wrong")
	errOut.Reset()
	if code := sweepExpiredCommand(nil, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "sweep expired on "+addr) {
		t.Fatalf("wrong token: code %d, stderr %q", code, errOut.String())
	}
	t.Setenv("CORTEXDB_REMOTE", "ftp://brain.example")
	errOut.Reset()
	if code := sweepExpiredCommand(nil, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "ftp://brain.example") {
		t.Fatalf("bad address: code %d, stderr %q", code, errOut.String())
	}
}

// A server that answers the sweep with something that is not a report is a
// failure, not a sweep that found nothing.
type brokenSweepServer struct {
	rpcv1.UnimplementedToolsServiceServer
}

func (brokenSweepServer) CallTool(context.Context, *rpcv1.CallToolRequest) (*rpcv1.CallToolResponse, error) {
	return &rpcv1.CallToolResponse{ResultJson: "not a report"}, nil
}

func TestAnUnreadableReportFromTheSharedBrainIsAFailure(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	rpcv1.RegisterToolsServiceServer(srv, brokenSweepServer{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	t.Setenv("CORTEXDB_REMOTE", lis.Addr().String())
	t.Setenv("CORTEXDB_GRPC_TOKEN", "")
	var out, errOut bytes.Buffer
	if code := sweepExpiredCommand([]string{"--json"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "read report") || out.Len() != 0 {
		t.Fatalf("unreadable report: code %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
}

// withoutTheMachinesModels clears every variable openBrainDB reads besides
// the brain's address, so a test opens a brain with no embedder, no query
// transformer and the default recall floor, whatever the machine has set.
func withoutTheMachinesModels(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"CORTEXDB_EMBED_BASE_URL", "OPENAI_BASE_URL", "CORTEXDB_EMBED_API_KEY", "OPENAI_API_KEY",
		"CORTEXDB_EMBED_MODEL", "CORTEXDB_EMBED_DIM", "CORTEXDB_EMBED_BATCH_SIZE", "CORTEXDB_EMBED_TIMEOUT_SECONDS",
		"CORTEXDB_LLM_BASE_URL", "CORTEXDB_LLM_URL", "CORTEXDB_LLM_API_KEY", "CORTEXDB_LLM_MODEL",
		"CORTEXDB_MEMORY_SEMANTIC_FLOOR",
	} {
		t.Setenv(name, "")
	}
}
