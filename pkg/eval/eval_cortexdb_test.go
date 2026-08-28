package eval_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/eval"
)

// TestLexicalRetrievalQuality indexes the bundled dataset into CortexDB and
// measures the no-embedder lexical retrieval path. It prints the metrics (run
// with -v) and guards a floor so retrieval quality can't silently regress.
func TestLexicalRetrievalQuality(t *testing.T) {
	ds, err := eval.Builtin()
	if err != nil {
		t.Fatalf("load dataset: %v", err)
	}

	dbPath := fmt.Sprintf("test_eval_%d.db", testname.Nano())
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

	retriever := eval.RetrieverFunc(func(ctx context.Context, query string, k int) ([]string, error) {
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
	})

	rep, err := eval.Run(ctx, ds, retriever, 1, 3, 5, 10)
	if err != nil {
		t.Fatalf("run eval: %v", err)
	}
	t.Logf("lexical retrieval quality:\n%s", rep.Summary())

	// Regression floors. Deliberately conservative — tighten as retrieval
	// improves; a drop below these means a real regression.
	if rep.RecallAtK[10] < 0.85 {
		t.Errorf("recall@10 = %.3f, below floor 0.85", rep.RecallAtK[10])
	}
	if rep.MRR < 0.80 {
		t.Errorf("MRR = %.3f, below floor 0.80", rep.MRR)
	}
}
