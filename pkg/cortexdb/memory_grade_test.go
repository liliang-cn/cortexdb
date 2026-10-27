package cortexdb

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func openGradeBrain(t *testing.T) *DB {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "grade.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func gradeOf(t *testing.T, db *DB, id string) MemoryRecord {
	t.Helper()
	got, err := db.GetMemory(context.Background(), MemoryGetRequest{MemoryID: id})
	if err != nil {
		t.Fatal(err)
	}
	return got.Memory
}

// A memory says how it is known; one that says nothing is untagged, not a
// guess, and a claim of verified names what checked it.
func TestAMemoryCarriesItsGrade(t *testing.T) {
	db := openGradeBrain(t)
	ctx := context.Background()
	save := func(req MemorySaveRequest) error {
		req.Scope = "global"
		_, err := db.SaveMemory(ctx, req)
		return err
	}
	if err := save(MemorySaveRequest{MemoryID: "fix", Content: "the resize fix works", Grade: "Verified", GradeWhy: "reverted it and TestResize went red"}); err != nil {
		t.Fatal(err)
	}
	if err := save(MemorySaveRequest{MemoryID: "guess", Content: "the satellite probably misses the update", Grade: GradeAsserted}); err != nil {
		t.Fatal(err)
	}
	if err := save(MemorySaveRequest{MemoryID: "plain", Content: "lunch is at noon"}); err != nil {
		t.Fatal(err)
	}
	if err := save(MemorySaveRequest{MemoryID: "meta", Content: "graded through metadata", Metadata: map[string]any{KeyGrade: "verified", KeyWhy: "measured"}}); err != nil {
		t.Fatal(err)
	}

	if m := gradeOf(t, db, "fix"); m.Grade != GradeVerified || m.GradeWhy != "reverted it and TestResize went red" {
		t.Fatalf("verified memory: %+v", m)
	}
	if m := gradeOf(t, db, "guess"); m.Grade != GradeAsserted {
		t.Fatalf("asserted memory: %+v", m)
	}
	if m := gradeOf(t, db, "plain"); m.Grade != "" {
		t.Fatalf("an ungraded memory reads %q, want untagged", m.Grade)
	}
	if m := gradeOf(t, db, "meta"); m.Grade != GradeVerified || m.GradeWhy != "measured" {
		t.Fatalf("metadata grade: %+v", m)
	}

	for name, req := range map[string]MemorySaveRequest{
		"verified without why": {MemoryID: "x1", Content: "x", Grade: GradeVerified},
		"held without why":     {MemoryID: "x2", Content: "x", Grade: GradeHeld},
		"unknown grade":        {MemoryID: "x3", Content: "x", Grade: "probably"},
		"why without grade":    {MemoryID: "x4", Content: "x", GradeWhy: "because"},
	} {
		if err := save(req); err == nil {
			t.Errorf("%s was saved", name)
		}
		if _, err := db.GetMemory(ctx, MemoryGetRequest{MemoryID: req.MemoryID}); err == nil {
			t.Errorf("%s: a refused save left a row", name)
		}
	}
}

// An update changes the grade only when asked, and holds it to the same
// rule; a new grade without a reason does not keep the old reason.
func TestUpdatingAGrade(t *testing.T) {
	db := openGradeBrain(t)
	ctx := context.Background()
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "m", Scope: "global", Content: "suspected cause", Grade: GradeAsserted, GradeWhy: "read the code"}); err != nil {
		t.Fatal(err)
	}
	imp := 0.9
	if _, err := db.UpdateMemory(ctx, MemoryUpdateRequest{MemoryID: "m", Importance: &imp}); err != nil {
		t.Fatal(err)
	}
	if m := gradeOf(t, db, "m"); m.Grade != GradeAsserted || m.GradeWhy != "read the code" {
		t.Fatalf("an update that did not touch the grade changed it: %+v", m)
	}
	verified := GradeVerified
	if _, err := db.UpdateMemory(ctx, MemoryUpdateRequest{MemoryID: "m", Grade: &verified}); err == nil {
		t.Fatal("verified without a new reason was accepted on the old one")
	}
	why := "reproduced on lg1 and fixed"
	if _, err := db.UpdateMemory(ctx, MemoryUpdateRequest{MemoryID: "m", Grade: &verified, GradeWhy: &why}); err != nil {
		t.Fatal(err)
	}
	if m := gradeOf(t, db, "m"); m.Grade != GradeVerified || m.GradeWhy != why {
		t.Fatalf("after update: %+v", m)
	}
}

// min_grade leaves out what is known less well, whichever retriever found
// it, and says so in the explain record.
func TestMinGradeKeepsOnlyWhatWasChecked(t *testing.T) {
	db := openGradeBrain(t)
	ctx := context.Background()
	for _, m := range []MemorySaveRequest{
		{MemoryID: "checked", Content: "drbd quorum fix verified on node-c", Grade: GradeVerified, GradeWhy: "failover drill passed"},
		{MemoryID: "guess", Content: "drbd quorum loss probably from the switch on node-c", Grade: GradeAsserted},
		{MemoryID: "untagged", Content: "drbd quorum notes for node-c"},
	} {
		m.Scope = "global"
		if _, err := db.SaveMemory(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(hits []MemorySearchHit) []string {
		var out []string
		for _, h := range hits {
			out = append(out, h.Memory.ID)
		}
		slices.Sort(out)
		return out
	}
	all, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "drbd quorum node-c", Scope: "global", TopK: 5})
	if err != nil || len(all.Results) != 3 {
		t.Fatalf("without min_grade: %v %v", err, ids(all.Results))
	}
	only, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "drbd quorum node-c", Scope: "global", TopK: 5, MinGrade: GradeVerified})
	if err != nil || !slices.Equal(ids(only.Results), []string{"checked"}) {
		t.Fatalf("min_grade verified: %v %v", err, ids(only.Results))
	}
	asserted, _ := db.SearchMemory(ctx, MemorySearchRequest{Query: "drbd quorum node-c", Scope: "global", TopK: 5, MinGrade: GradeAsserted})
	if len(asserted.Results) != 3 {
		t.Fatalf("min_grade asserted keeps untagged and asserted: %v", ids(asserted.Results))
	}
	if _, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "x", Scope: "global", MinGrade: "sure"}); err == nil {
		t.Fatal("an unknown min_grade was accepted")
	}

	resp, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: "drbd quorum node-c", MinGrade: GradeVerified, Explain: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(resp.Memories), []string{"checked"}) {
		t.Fatalf("recall min_grade: %v", ids(resp.Memories))
	}
	if resp.Explain.Memory.DroppedTotal[dropBelowMinGrade] == 0 {
		t.Fatalf("explain does not say what min_grade dropped: %+v", resp.Explain.Memory.DroppedTotal)
	}
	if resp.Explain.Final[0].Grade != GradeVerified {
		t.Fatalf("explain hit grade: %+v", resp.Explain.Final[0])
	}
}

// The context pack sets a guess apart under its own heading; a verified
// memory says what checked it; an untagged one reads as it always did.
func TestTheContextPackSetsGuessesApart(t *testing.T) {
	db := openGradeBrain(t)
	ctx := context.Background()
	for _, m := range []MemorySaveRequest{
		{MemoryID: "checked", Content: "haify failover moves the VIP in 4s", Grade: GradeVerified, GradeWhy: "timed in the drill on 2026-10-20"},
		{MemoryID: "guess", Content: "haify failover is slow because of drbd resync", Grade: GradeAsserted},
		{MemoryID: "untagged", Content: "haify failover runbook lives in the wiki"},
	} {
		m.Scope = "global"
		if _, err := db.SaveMemory(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: "haify failover", DisableKnowledge: true})
	if err != nil {
		t.Fatal(err)
	}
	var memories, unverified KnowledgeMemoryContextSection
	for _, s := range resp.ContextPack.Sections {
		switch s.Kind {
		case "memories":
			memories = s
		case "unverified_memories":
			unverified = s
		}
	}
	if !strings.Contains(memories.Text, "[checked] haify failover moves the VIP in 4s (verified: timed in the drill on 2026-10-20)") ||
		!strings.Contains(memories.Text, "[untagged] haify failover runbook lives in the wiki") || strings.Contains(memories.Text, "[guess]") {
		t.Fatalf("memories section:\n%s", memories.Text)
	}
	if unverified.Title == "" || !strings.Contains(unverified.Text, "[guess] haify failover is slow because of drbd resync (asserted)") {
		t.Fatalf("unverified section: %+v", unverified)
	}
	if !strings.Contains(resp.ContextPack.Text, "[UNVERIFIED_MEMORIES]") {
		t.Fatalf("pack text does not label the guesses:\n%s", resp.ContextPack.Text)
	}
}
