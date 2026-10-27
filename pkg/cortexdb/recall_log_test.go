package cortexdb

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
)

func openRecallLogBrain(t *testing.T, opts ...Option) *DB {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "recall-log.db")), opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func saveRecallLogMemories(t *testing.T, db *DB, ctx context.Context, byID map[string]string) {
	t.Helper()
	for id, content := range byID {
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: id, Scope: "global", Content: content}); err != nil {
			t.Fatal(err)
		}
	}
}

// The log is opt-in: queries hold whatever a person typed.
func TestTheRecallLogIsOffUnlessAskedFor(t *testing.T) {
	db := openRecallLogBrain(t)
	ctx := context.Background()
	saveRecallLogMemories(t, db, ctx, map[string]string{"m1": "drbd quorum lost on node-c"})
	if _, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: "drbd quorum"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ListRecalls(ctx, RecallLogListRequest{}); err == nil || !strings.Contains(err.Error(), "CORTEXDB_RECALL_LOG") {
		t.Fatalf("an off log answered: %v", err)
	}
	var n int
	if err := db.queryRow(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'recall_log'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("an off log created its table: %d %v", n, err)
	}
}

// A recall is logged with who asked, what came back and how, and its
// explain record; the per-record view counts it; a hit marked useful or
// noise comes out as a golden case.
func TestTheRecallLogKeepsTheTimelineAndFeedsTheGoldenSet(t *testing.T) {
	db := openRecallLogBrain(t, WithRecallLog(RecallLogOptions{}))
	ctx, call := observability.WithCall(context.Background(), "req-77")
	call.SetClient(observability.ClientInfo{Host: "Pro", Project: "CortexDB", Agent: "claude-code"})
	saveRecallLogMemories(t, db, ctx, map[string]string{
		"m-quorum": "drbd quorum lost on node-c after the switch reboot",
		"m-noise":  "drbd tuning notes: al-extents and c-plan-ahead",
		"m-idle":   "lunch place near the office closes on mondays",
	})
	for _, q := range []string{"drbd quorum", "quorum lost node-c"} {
		if _, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: q, Planner: "model"}); err != nil {
			t.Fatal(err)
		}
	}

	list, err := db.ListRecalls(ctx, RecallLogListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Recalls) != 2 || list.Recalls[0].Query != "quorum lost node-c" {
		t.Fatalf("timeline: %+v", list.Recalls)
	}
	first := list.Recalls[1]
	if first.RequestID != "req-77" || first.Host != "Pro" || first.Project != "CortexDB" || first.Agent != "claude-code" || first.Planner != "model" {
		t.Fatalf("who asked: %+v", first)
	}
	if len(first.Hits) == 0 || first.Hits[0].ID != "m-quorum" || first.Hits[0].NodeID != "memory:m-quorum" || first.Hits[0].Title == "" || len(first.Hits[0].Via) == 0 {
		t.Fatalf("hits: %+v", first.Hits)
	}
	if first.Explain != nil {
		t.Fatal("the list carries explain records")
	}

	got, err := db.GetRecall(ctx, RecallLogGetRequest{RecallID: first.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.Explain == nil || got.Explain.Plan.Query != "drbd quorum" || len(got.Explain.Final) == 0 {
		t.Fatalf("explain: %+v", got.Explain)
	}

	byRecord, err := db.ListRecalls(ctx, RecallLogListRequest{RecordID: "m-quorum"})
	if err != nil || len(byRecord.Recalls) != 2 {
		t.Fatalf("recalls of m-quorum: %v %+v", err, byRecord)
	}

	stats, err := db.RecallStats(ctx, RecallStatsRequest{RecordID: "m-quorum"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Recalls != 2 || len(stats.MostRecalled) == 0 || stats.MostRecalled[0].ID != "m-quorum" || stats.MostRecalled[0].Count != 2 {
		t.Fatalf("most recalled: %+v", stats)
	}
	if stats.Record == nil || len(stats.Record.Queries) != 2 {
		t.Fatalf("one record's history: %+v", stats.Record)
	}
	if !slices.ContainsFunc(stats.NeverRecalled, func(r NeverRecalledRecord) bool { return r.ID == "m-idle" }) {
		t.Fatalf("never recalled: %+v", stats.NeverRecalled)
	}

	if _, err := db.RecallFeedback(ctx, RecallFeedbackRequest{RecallID: first.ID, RecordID: "m-quorum", Verdict: "useful"}); err != nil {
		t.Fatal(err)
	}
	noise := ""
	for _, h := range first.Hits {
		if h.ID != "m-quorum" {
			noise = h.ID
		}
	}
	if noise != "" {
		if _, err := db.RecallFeedback(ctx, RecallFeedbackRequest{RecallID: first.ID, RecordID: noise, Verdict: "noise", Note: "tuning, not quorum"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.RecallFeedback(ctx, RecallFeedbackRequest{RecallID: first.ID, RecordID: "m-idle", Verdict: "useful"}); err == nil {
		t.Fatal("feedback on a hit the recall did not return was accepted")
	}
	again, _ := db.GetRecall(ctx, RecallLogGetRequest{RecallID: first.ID})
	if again.Hits[0].Verdict != "useful" {
		t.Fatalf("the verdict does not show on the hit: %+v", again.Hits[0])
	}

	export, err := db.ExportRecallFeedback(ctx, RecallFeedbackExportRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(export.Cases) != 1 {
		t.Fatalf("golden cases: %+v", export.Cases)
	}
	c := export.Cases[0]
	if c.Query != "drbd quorum" || !slices.Equal(c.Expected, []string{"m-quorum"}) || (noise != "" && !slices.Equal(c.Forbidden, []string{noise})) {
		t.Fatalf("golden case: %+v", c)
	}
	if line, _ := json.Marshal(c); !strings.Contains(string(line), `"expected":["m-quorum"]`) {
		t.Fatalf("golden line: %s", line)
	}
}

// A key with a scope sees, explains and marks only the recalls it made.
func TestAConfinedKeySeesOnlyItsOwnRecalls(t *testing.T) {
	db := openRecallLogBrain(t, WithRecallLog(RecallLogOptions{}))
	saveRecallLogMemories(t, db, context.Background(), map[string]string{"m1": "the openclaw gateway fails over to sds-a"})
	confined := func(key string) context.Context {
		return WithCallerScope(WithWriter(context.Background(), key), CallerScope{
			Key: key, Memory: func(MemoryBucket) bool { return true }, Knowledge: func(string) bool { return true },
		})
	}
	alice, bob := confined("alice"), confined("bob")
	if _, err := db.KnowledgeMemory().Recall(alice, KnowledgeMemoryRecallRequest{Query: "openclaw gateway"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.KnowledgeMemory().Recall(WithWriter(context.Background(), "operator"), KnowledgeMemoryRecallRequest{Query: "sds-a failover"}); err != nil {
		t.Fatal(err)
	}

	mine, err := db.ListRecalls(alice, RecallLogListRequest{Key: "operator"})
	if err != nil || len(mine.Recalls) != 1 || mine.Recalls[0].Key != "alice" {
		t.Fatalf("alice's view: %v %+v", err, mine)
	}
	theirs, err := db.ListRecalls(bob, RecallLogListRequest{})
	if err != nil || len(theirs.Recalls) != 0 {
		t.Fatalf("bob sees others' recalls: %v %+v", err, theirs)
	}
	if _, err := db.GetRecall(bob, RecallLogGetRequest{RecallID: mine.Recalls[0].ID}); err == nil {
		t.Fatal("bob read alice's recall")
	}
	if _, err := db.RecallFeedback(bob, RecallFeedbackRequest{RecallID: mine.Recalls[0].ID, RecordID: "m1", Verdict: "noise"}); err == nil {
		t.Fatal("bob marked alice's recall")
	}
	stats, err := db.RecallStats(bob, RecallStatsRequest{})
	if err != nil || stats.Recalls != 0 || len(stats.NeverRecalled) != 0 {
		t.Fatalf("bob's stats: %v %+v", err, stats)
	}
	all, err := db.ListRecalls(context.Background(), RecallLogListRequest{})
	if err != nil || len(all.Recalls) != 2 {
		t.Fatalf("an unconfined caller's view: %v %+v", err, all)
	}
}

// Recalls past the retention or past the row bound are dropped; feedback
// stays, because the golden set is built from it.
func TestTheRecallLogKeepsItsBounds(t *testing.T) {
	db := openRecallLogBrain(t, WithRecallLog(RecallLogOptions{MaxRows: 3}))
	ctx := context.Background()
	saveRecallLogMemories(t, db, ctx, map[string]string{"m1": "haify release publishes the knowledge base"})
	for i := 0; i < 6; i++ {
		if _, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: "haify release " + strings.Repeat("x", i)}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	list, _ := db.ListRecalls(ctx, RecallLogListRequest{})
	if _, err := db.RecallFeedback(ctx, RecallFeedbackRequest{RecallID: list.Recalls[5].ID, RecordID: "m1", Verdict: "useful"}); err != nil {
		t.Fatal(err)
	}
	if err := db.pruneRecallLog(ctx); err != nil {
		t.Fatal(err)
	}
	list, _ = db.ListRecalls(ctx, RecallLogListRequest{})
	if len(list.Recalls) != 3 {
		t.Fatalf("kept %d recalls, bound is 3", len(list.Recalls))
	}
	if export, _ := db.ExportRecallFeedback(ctx, RecallFeedbackExportRequest{}); len(export.Cases) != 1 {
		t.Fatalf("feedback lost with its recall: %+v", export)
	}

	db.recallLog.opts.Retention = time.Millisecond
	time.Sleep(5 * time.Millisecond)
	if err := db.pruneRecallLog(ctx); err != nil {
		t.Fatal(err)
	}
	if list, _ = db.ListRecalls(ctx, RecallLogListRequest{}); len(list.Recalls) != 0 {
		t.Fatalf("kept %d recalls past the retention", len(list.Recalls))
	}
}

// A process that does not keep the log reads the one another process
// sharing the file keeps: the live view opened on the MCP server's file.
func TestAReaderOfTheFileReadsTheLogAnotherProcessKeeps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	writer, err := Open(DefaultConfig(path), WithRecallLog(RecallLogOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx := context.Background()
	saveRecallLogMemories(t, writer, ctx, map[string]string{"m1": "zfs pool on dell is tank"})
	if _, err := writer.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: "zfs pool"}); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(DefaultConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	list, err := reader.ListRecalls(ctx, RecallLogListRequest{})
	if err != nil || len(list.Recalls) != 1 {
		t.Fatalf("reader: %v %+v", err, list)
	}
	if _, err := reader.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: "zfs tank"}); err != nil {
		t.Fatal(err)
	}
	if list, _ := reader.ListRecalls(ctx, RecallLogListRequest{}); len(list.Recalls) != 1 {
		t.Fatalf("a process with the log off wrote to it: %d", len(list.Recalls))
	}
}
