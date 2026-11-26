package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// CORTEXDB_REVIEW_GATE turns the gate on for the brain this binary opens: with
// 1 a proposed document is left out of search, with 0 or unset it is served,
// and a value that is not a boolean is refused.
func TestTheReviewGateIsReadFromTheEnvironment(t *testing.T) {
	withoutTheMachinesModels(t)
	t.Setenv("CORTEXDB_REVIEW_GATE", "maybe")
	if _, err := openBrainDB(filepath.Join(t.TempDir(), "x.db")); err == nil || !strings.Contains(err.Error(), "CORTEXDB_REVIEW_GATE") {
		t.Fatalf("a bad CORTEXDB_REVIEW_GATE was accepted: %v", err)
	}
	for _, tc := range []struct {
		value  string
		hidden bool
	}{{"1", true}, {"0", false}, {"", false}} {
		t.Setenv("CORTEXDB_REVIEW_GATE", tc.value)
		db, err := openBrainDB(filepath.Join(t.TempDir(), "brain.db"))
		if err != nil {
			t.Fatalf("open with %q: %v", tc.value, err)
		}
		ctx := context.Background()
		if _, err := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{KnowledgeID: "k-prop", Content: "The heron protocol governs the export.",
			ReviewStatus: cortexdb.ReviewProposed}); err != nil {
			t.Fatal(err)
		}
		resp, err := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{Query: "heron protocol", TopK: 5})
		if err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
		if served := len(resp.Results) > 0; served == tc.hidden {
			t.Fatalf("CORTEXDB_REVIEW_GATE=%q: proposed document served=%v", tc.value, served)
		}
	}
}
