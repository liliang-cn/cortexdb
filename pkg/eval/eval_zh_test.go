package eval_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/eval"
)

// Chinese sentence queries with no embedder: the path a default install takes.
//
// Before the CJK bigram path a query like 微信机器人为什么从来不主动给我发提醒 was
// one token to the word index, and on a snapshot of the shared brain five of six
// such queries returned nothing at all. This set is invented, so it can live in
// the repository, and written the same way: questions in different words from
// the notes they answer. Its negatives each share one word with a note at most —
// 推荐 with the coffee shop, 周末去 likewise — which is exactly what the path
// must not mistake for an answer.
func TestChineseLexicalRetrievalQuality(t *testing.T) {
	ds, err := eval.BuiltinChinese()
	if err != nil {
		t.Fatalf("load dataset: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), fmt.Sprintf("test_eval_zh_%d.db", testname.Nano()))
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, s := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + s)
		}
	})
	if db.HasEmbedder() {
		t.Fatal("this measures the no-embedder path")
	}

	ctx := context.Background()
	for _, d := range ds.Documents {
		if _, err := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
			KnowledgeID: d.ID,
			Title:       d.Title,
			Content:     d.Content,
		}); err != nil {
			t.Fatalf("save %q: %v", d.ID, err)
		}
	}

	search := func(ctx context.Context, query string, k int) ([]string, error) {
		resp, err := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{
			Query:         query,
			RetrievalMode: cortexdb.RetrievalModeLexical,
			TopK:          k,
		})
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(resp.Results))
		for _, hit := range resp.Results {
			ids = append(ids, hit.KnowledgeID)
		}
		return ids, nil
	}

	rep, err := eval.Run(ctx, ds, eval.RetrieverFunc(search), 1, 3, 5)
	if err != nil {
		t.Fatalf("run eval: %v", err)
	}
	t.Logf("chinese lexical retrieval quality:\n%s", rep.Summary())
	for _, q := range rep.PerQuery {
		if q.RR == 0 {
			t.Logf("miss %s: %v", q.QueryID, q.Retrieved)
		}
	}

	// Floors a little under what this path measures today; see the log above.
	if rep.RecallAtK[5] < floorZhRecall5 {
		t.Errorf("recall@5 = %.3f, below floor %.2f", rep.RecallAtK[5], floorZhRecall5)
	}
	if rep.MRR < floorZhMRR {
		t.Errorf("MRR = %.3f, below floor %.2f", rep.MRR, floorZhMRR)
	}

	for _, n := range ds.Negatives {
		got, err := search(ctx, n.Text, 5)
		if err != nil {
			t.Fatalf("negative %s: %v", n.ID, err)
		}
		if len(got) != 0 {
			t.Errorf("negative %s (%s) returned %v; the corpus holds nothing about it", n.ID, n.Text, got)
		}
	}
}

const (
	floorZhRecall5 = 0.90
	floorZhMRR     = 0.85
)
