package graphflow

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func openLearningTestDB(t *testing.T) (*cortexdb.DB, context.Context) {
	t.Helper()
	dbPath := fmt.Sprintf("test_learning_%d.db", time.Now().UnixNano())
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
	return db, context.Background()
}

// mathGraph: 导数 requires 极限 requires 函数; 积分 requires 极限.
func seedMathGraph(t *testing.T, db *cortexdb.DB, ctx context.Context) {
	t.Helper()
	lg := LearningGraph{
		Subject: "math",
		Concepts: []LearningConcept{
			{Name: "函数", Type: "concept", Difficulty: 1},
			{Name: "极限", Type: "concept", Difficulty: 2},
			{Name: "导数", Type: "concept", Difficulty: 3},
			{Name: "积分", Type: "concept", Difficulty: 3},
		},
		Relations: []LearningRelation{
			{From: "极限", To: "函数", Type: "requires"},
			{From: "导数", To: "极限", Type: "requires"},
			{From: "积分", To: "极限", Type: "requires"},
		},
	}
	if _, err := ImportLearningGraph(ctx, db, lg); err != nil {
		t.Fatalf("import: %v", err)
	}
}

func names(cs []LearningConcept) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}

// TestLearningPathOrdersPrerequisitesFirst verifies the study plan is a valid
// topological order: every prerequisite appears before what needs it.
func TestLearningPathOrdersPrerequisitesFirst(t *testing.T) {
	db, ctx := openLearningTestDB(t)
	seedMathGraph(t, db, ctx)

	path, err := LearningPath(ctx, db, "导数", []string{})
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if path.Missing {
		t.Fatalf("target should exist")
	}
	got := names(path.Steps)
	want := []string{"函数", "极限", "导数"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("expected %v, got %v", want, got)
	}
	// 积分 is not a prerequisite of 导数 — it must not appear.
	for _, n := range got {
		if n == "积分" {
			t.Fatalf("unrelated concept 积分 leaked into the path: %v", got)
		}
	}
}

// TestLearningPathSkipsMastered verifies already-known concepts drop out.
func TestLearningPathSkipsMastered(t *testing.T) {
	db, ctx := openLearningTestDB(t)
	seedMathGraph(t, db, ctx)

	path, err := LearningPath(ctx, db, "导数", []string{"函数", "极限"})
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if got := names(path.Steps); strings.Join(got, ",") != "导数" {
		t.Fatalf("expected only 导数 left to study, got %v", got)
	}
	if len(path.Known) != 2 {
		t.Fatalf("expected 2 known concepts reported, got %v", path.Known)
	}
}

// TestMarkMasteredDrivesPath verifies mastery persisted on the graph is picked
// up when the caller passes nil (no explicit known set).
func TestMarkMasteredDrivesPath(t *testing.T) {
	db, ctx := openLearningTestDB(t)
	seedMathGraph(t, db, ctx)

	marked, unknown, err := MarkMastered(ctx, db, []string{"函数", "不存在的概念"}, time.Now())
	if err != nil {
		t.Fatalf("mark: %v", err)
	}
	if len(marked) != 1 || marked[0] != "函数" {
		t.Fatalf("expected 函数 marked, got %v", marked)
	}
	if len(unknown) != 1 {
		t.Fatalf("expected the bogus name reported as unknown, got %v", unknown)
	}

	path, err := LearningPath(ctx, db, "导数", nil) // nil → load mastery from graph
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if got := names(path.Steps); strings.Join(got, ",") != "极限,导数" {
		t.Fatalf("expected 极限,导数 after mastering 函数, got %v", got)
	}
}

// TestNextConceptsReturnsFrontier verifies the learnable frontier: concepts
// whose prerequisites are all mastered.
func TestNextConceptsReturnsFrontier(t *testing.T) {
	db, ctx := openLearningTestDB(t)
	seedMathGraph(t, db, ctx)

	// Knowing only 函数, the frontier is 极限 (导数/积分 still need 极限).
	next, err := NextConcepts(ctx, db, []string{"函数"}, 0)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if got := names(next); strings.Join(got, ",") != "极限" {
		t.Fatalf("expected frontier 极限, got %v", got)
	}

	// Knowing 函数+极限, both 导数 and 积分 open up.
	next, err = NextConcepts(ctx, db, []string{"函数", "极限"}, 0)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	got := names(next)
	if len(got) != 2 || !containsString(got, "导数") || !containsString(got, "积分") {
		t.Fatalf("expected 导数 and 积分 in the frontier, got %v", got)
	}
}

// TestMissingPrerequisites answers "why am I stuck": everything before target.
func TestMissingPrerequisites(t *testing.T) {
	db, ctx := openLearningTestDB(t)
	seedMathGraph(t, db, ctx)

	missing, err := MissingPrerequisites(ctx, db, "导数", []string{})
	if err != nil {
		t.Fatalf("missing: %v", err)
	}
	if got := names(missing); strings.Join(got, ",") != "函数,极限" {
		t.Fatalf("expected 函数,极限 missing, got %v", got)
	}
}

// TestLearningPathBreaksCycle verifies a cyclic prerequisite graph terminates,
// still emits every concept, and reports the cycle instead of hanging.
func TestLearningPathBreaksCycle(t *testing.T) {
	db, ctx := openLearningTestDB(t)
	lg := LearningGraph{
		Subject: "physics",
		Concepts: []LearningConcept{
			{Name: "A", Difficulty: 1}, {Name: "B", Difficulty: 2}, {Name: "C", Difficulty: 3},
		},
		Relations: []LearningRelation{
			{From: "A", To: "B", Type: "requires"},
			{From: "B", To: "C", Type: "requires"},
			{From: "C", To: "A", Type: "requires"}, // cycle
		},
	}
	if _, err := ImportLearningGraph(ctx, db, lg); err != nil {
		t.Fatalf("import: %v", err)
	}

	done := make(chan *LearningPathResult, 1)
	go func() {
		p, err := LearningPath(ctx, db, "A", []string{})
		if err != nil {
			done <- nil
			return
		}
		done <- p
	}()
	select {
	case p := <-done:
		if p == nil {
			t.Fatalf("cyclic path returned an error")
		}
		if len(p.Steps) != 3 {
			t.Fatalf("expected all 3 concepts emitted despite the cycle, got %v", names(p.Steps))
		}
		if len(p.Cycles) == 0 {
			t.Fatalf("expected the cycle to be reported")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("LearningPath hung on a cyclic prerequisite graph")
	}
}

// TestLearningPathMissingTarget verifies an unknown target is reported, not an error.
func TestLearningPathMissingTarget(t *testing.T) {
	db, ctx := openLearningTestDB(t)
	seedMathGraph(t, db, ctx)
	path, err := LearningPath(ctx, db, "拓扑学", []string{})
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if !path.Missing || len(path.Steps) != 0 {
		t.Fatalf("expected Missing=true with no steps, got %+v", path)
	}
}
