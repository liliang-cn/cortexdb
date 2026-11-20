package cortexdb

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// mergeLtd merges ACME Ltd into Acme Corp as alpha and returns the journal.
func mergeLtd(t *testing.T, db *DB) EntityMergeJournal {
	t.Helper()
	merged, err := db.GraphRAGTools().MergeEntities(WithCallerScope(context.Background(), alphaScope()), ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}})
	if err != nil {
		t.Fatal(err)
	}
	return merged.Journal
}

// betaScope is a caller confined to collection beta.
func betaScope() CallerScope {
	return CallerScope{Key: "beta-key", Knowledge: func(c string) bool { return c == "beta" }}
}

// refusedAndWroteNothing applies journal and wants a refusal that left the
// graph, history included, as it was.
func refusedAndWroteNothing(t *testing.T, db *DB, ctx context.Context, journal EntityMergeJournal, what string) error {
	t.Helper()
	t0 := db.Graph().Now()
	_, err := db.GraphRAGTools().UnmergeEntities(ctx, ToolUnmergeEntitiesRequest{Journal: journal})
	if err == nil {
		t.Fatalf("%s: the journal was applied", what)
	}
	if diff := diffSince(t, db, t0); len(diff.Nodes)+len(diff.Edges) != 0 {
		t.Fatalf("%s: the refused undo wrote: %+v", what, diff)
	}
	return err
}

// An alias written again after the merge is another writer's entity now: the
// undo does not write over it. Alpha folds ACME Ltd, beta saves a document
// that names ACME Ltd, and alpha's undo is refused; beta's entity keeps its
// collection and its description. The refusal a confined caller reads is the
// one an entity that is not there gets.
func TestUnmergeEntitiesDoesNotWriteOverAnAliasWrittenAfterTheMerge(t *testing.T) {
	for backend, open := range mergeBrains {
		t.Run(backend, func(t *testing.T) {
			db := open(t)
			ctx := context.Background()
			journal := mergeLtd(t, db)
			b2 := graphChunkNodeID("b2", 0)
			// Beta writes as beta: a writer that may read alpha would follow
			// the alias into Acme Corp (knowledge_alias_follow.go).
			if _, err := db.SaveKnowledge(WithCallerScope(ctx, betaScope()), KnowledgeSaveRequest{
				KnowledgeID: "b2", Collection: "beta", Content: "ACME Ltd is a pension fund.",
				Entities: []ToolEntityInput{{Name: "ACME Ltd", Description: "beta's fund", ChunkIDs: []string{b2}}},
			}); err != nil {
				t.Fatal(err)
			}
			ltd := EntityNodeID("ACME Ltd")
			confined := refusedAndWroteNothing(t, db, WithCallerScope(ctx, alphaScope()), journal, "alpha")
			if want := fmt.Sprintf("unmerge_entities: entity %q not found", ltd); confined.Error() != want {
				t.Fatalf("alpha is told %q, want %q", confined, want)
			}
			if open := refusedAndWroteNothing(t, db, ctx, journal, "an open caller"); !strings.Contains(open.Error(), "exists again") {
				t.Fatalf("an open caller is told %q, want that the entity exists again", open)
			}
			node, err := db.Graph().GetNode(ctx, ltd)
			if err != nil || node.Properties["description"] != "beta's fund" || !slices.Equal(toStringSlice(node.Properties[entityCollectionsKey]), []string{"beta"}) {
				t.Fatalf("beta's entity reads %+v (%v)", node, err)
			}
		})
	}
}

// A journal undoes the merge history shows at its instant, into the
// canonical entity it names, and nothing else. History, not the journal,
// says what that merge took: a journal that names another canonical entity,
// another instant, an entity or an edge the merge did not take, or less
// than it took, is refused, as a confined key and as an open caller. None
// of them rolls an entity back, brings a deleted one back or moves an edge.
func TestUnmergeEntitiesTakesOnlyAJournalAMergeBacks(t *testing.T) {
	for backend, open := range mergeBrains {
		t.Run(backend, func(t *testing.T) {
			db := open(t)
			ctx := context.Background()
			alpha := WithCallerScope(ctx, alphaScope())
			acme, ltd, widget := EntityNodeID("Acme Corp"), EntityNodeID("ACME Ltd"), EntityNodeID("Widget")
			// ACME Ltd is rewritten, Acme Inc is deleted, a rival is
			// superseded by Acme Corp at the instant "stood" plus one
			// write, and then ACME Ltd is merged.
			if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{{Name: "ACME Ltd", Description: "rewritten"}}}); err != nil {
				t.Fatal(err)
			}
			rival := &graph.GraphNode{ID: "entity:rival", NodeType: "entity", Content: "Rival", Properties: map[string]any{"name": "Rival", entityCollectionsKey: []string{"alpha"}}}
			if err := db.Graph().UpsertNode(ctx, rival); err != nil {
				t.Fatal(err)
			}
			if err := db.Graph().DeleteNode(graph.WithInvalidation(ctx, graph.Invalidation{Reason: graph.ReasonSuperseded, SupersededBy: acme}), rival.ID); err != nil {
				t.Fatal(err)
			}
			superseded, err := db.Graph().NodeHistory(ctx, rival.ID)
			if err != nil || len(superseded) != 1 {
				t.Fatal(superseded, err)
			}
			if err := db.Graph().DeleteNode(ctx, EntityNodeID("Acme Inc")); err != nil {
				t.Fatal(err)
			}
			deleted, err := db.Graph().NodeHistory(ctx, EntityNodeID("Acme Inc"))
			if err != nil || len(deleted) != 1 {
				t.Fatal(deleted, err)
			}
			journal := mergeLtd(t, db)
			left := firstEdge(t, edgesTouching(t, db, widget), acme, widget)
			for what, forged := range map[string]EntityMergeJournal{
				"an entity never merged":   {Canonical: acme, Aliases: []string{widget}, MergedAt: journal.MergedAt},
				"another canonical entity": {Canonical: widget, Aliases: journal.Aliases, Edges: journal.Edges, MergedAt: journal.MergedAt},
				"another instant":          {Canonical: acme, Aliases: journal.Aliases, Edges: journal.Edges, MergedAt: journal.MergedAt.Add(-time.Microsecond)},
				"an entity superseded":     {Canonical: acme, Aliases: []string{rival.ID}, MergedAt: superseded[0].ValidTo},
				"an entity deleted":        {Canonical: acme, Aliases: []string{EntityNodeID("Acme Inc")}, MergedAt: deleted[0].ValidTo},
				"an entity more":           {Canonical: acme, Aliases: append(slices.Clone(journal.Aliases), widget), Edges: journal.Edges, MergedAt: journal.MergedAt},
				"an edge more":             {Canonical: acme, Aliases: journal.Aliases, Edges: append(slices.Clone(journal.Edges), left+"-not"), MergedAt: journal.MergedAt},
				"an edge less":             {Canonical: acme, Aliases: journal.Aliases, Edges: journal.Edges[1:], MergedAt: journal.MergedAt},
				"no edge":                  {Canonical: acme, Aliases: journal.Aliases, MergedAt: journal.MergedAt},
			} {
				for who, caller := range map[string]context.Context{"alpha": alpha, "an open caller": ctx} {
					err := refusedAndWroteNothing(t, db, caller, forged, who+", "+what)
					if who == "alpha" && !strings.HasSuffix(err.Error(), "not found") {
						t.Fatalf("alpha, %s: told %q, want only that an entity is not found", what, err)
					}
				}
			}
			if _, err := db.Graph().GetNode(ctx, ltd); err == nil {
				t.Fatal("a refused journal brought ACME Ltd back")
			}
			if _, err := db.GraphRAGTools().UnmergeEntities(alpha, ToolUnmergeEntitiesRequest{Journal: journal}); err != nil {
				t.Fatalf("the journal of the merge itself: %v", err)
			}
			node, err := db.Graph().GetNode(ctx, ltd)
			if err != nil || node.Properties["description"] != "rewritten" {
				t.Fatalf("ACME Ltd came back as %+v (%v), want as it stood at the merge", node, err)
			}
		})
	}
}

// An entity rewritten by a read and a write that keeps the version's start,
// then merged, comes back as rewritten: the undo reads the last instant
// before the merge, which no journal chooses.
func TestUnmergeEntitiesRestoresTheVersionTheMergeClosed(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	ltd := EntityNodeID("ACME Ltd")
	node, err := db.Graph().GetNode(ctx, ltd)
	if err != nil {
		t.Fatal(err)
	}
	node.Properties["description"] = "rewritten"
	if err := db.Graph().UpsertNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	journal := mergeLtd(t, db)
	if _, err := db.GraphRAGTools().UnmergeEntities(ctx, ToolUnmergeEntitiesRequest{Journal: journal}); err != nil {
		t.Fatal(err)
	}
	if back, err := db.Graph().GetNode(ctx, ltd); err != nil || back.Properties["description"] != "rewritten" {
		t.Fatalf("ACME Ltd came back as %+v (%v)", back, err)
	}
}

// A confined key cannot use its own merge to move an edge another merge
// took: an open caller folds beta's Heron Mill into Acme Corp, beta writes
// Heron Mill again, alpha folds ACME Ltd. Neither alpha's journal with the
// other merge's edge, nor the other merge's journal, is taken from alpha.
func TestAConfinedUndoDoesNotReachAnotherMerge(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()
	alpha := WithCallerScope(ctx, alphaScope())
	heron := EntityNodeID("Heron Mill")
	other, err := tools.MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"Heron Mill"}})
	if err != nil {
		t.Fatal(err)
	}
	// Beta writes as beta: a writer that may read alpha would follow the
	// alias into Acme Corp (knowledge_alias_follow.go).
	if _, err := db.SaveKnowledge(WithCallerScope(ctx, betaScope()), KnowledgeSaveRequest{KnowledgeID: "b2", Collection: "beta", Content: "Heron Mill again.",
		Entities: []ToolEntityInput{{Name: "Heron Mill", ChunkIDs: []string{graphChunkNodeID("b2", 0)}}}}); err != nil {
		t.Fatal(err)
	}
	// Acme Corp is alpha's alone again for the test: the save above did
	// not name it.
	mine, err := tools.MergeEntities(alpha, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}})
	if err != nil {
		t.Fatal(err)
	}
	for what, forged := range map[string]EntityMergeJournal{
		"its journal with the other merge's edges":   {Canonical: mine.Journal.Canonical, Aliases: mine.Journal.Aliases, Edges: other.Journal.Edges, MergedAt: mine.Journal.MergedAt},
		"its journal at the other merge's instant":   {Canonical: mine.Journal.Canonical, Aliases: mine.Journal.Aliases, Edges: other.Journal.Edges, MergedAt: other.Journal.MergedAt},
		"the other merge's journal, Heron Mill live": other.Journal,
	} {
		refusedAndWroteNothing(t, db, alpha, forged, what)
	}
	if err := db.Graph().DeleteNode(ctx, heron); err != nil {
		t.Fatal(err)
	}
	err = refusedAndWroteNothing(t, db, alpha, other.Journal, "the other merge's journal")
	if want := fmt.Sprintf("unmerge_entities: entity %q not found", heron); err.Error() != want {
		t.Fatalf("alpha is told %q, want %q", err, want)
	}
}

// An edge the merge dropped as a loop and that was written again under the
// same id closes no version: the undo still does not write over it.
func TestUnmergeEntitiesDoesNotWriteOverALoopWrittenAgain(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	corp, inc, widget := EntityNodeID("Acme Corp"), EntityNodeID("Acme Inc"), EntityNodeID("Widget")
	loop := firstEdge(t, edgesTouching(t, db, inc), inc, corp)
	merged, err := db.GraphRAGTools().MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"Acme Inc"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Graph().UpsertEdge(ctx, &graph.GraphEdge{ID: loop, FromNodeID: widget, ToNodeID: corp, EdgeType: "supplies"}); err != nil {
		t.Fatal(err)
	}
	refusedAndWroteNothing(t, db, ctx, merged.Journal, "after the loop's id was written again")
}

// Two merges and their undos. A journal undoes its own merge, not a later
// one of the same entities; and a merge whose edges a later merge took and
// gave back is not undone, though nothing differs: the versions the first
// merge left were closed since.
func TestAnUndoFollowsItsOwnMergeOnly(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()
	req := ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}}
	first, err := tools.MergeEntities(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tools.UnmergeEntities(ctx, ToolUnmergeEntitiesRequest{Journal: first.Journal}); err != nil {
		t.Fatal(err)
	}
	second, err := tools.MergeEntities(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	bare := first.Journal
	bare.Edges = nil
	refusedAndWroteNothing(t, db, ctx, first.Journal, "the first journal after a second merge")
	refusedAndWroteNothing(t, db, ctx, bare, "the first journal without its edges")
	// Acme Corp itself is folded and given back: the edges the second
	// merge repointed onto it were closed and written again.
	outer, err := tools.MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Kestrel Vale", Aliases: []string{"Acme Corp"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tools.UnmergeEntities(ctx, ToolUnmergeEntitiesRequest{Journal: outer.Journal}); err != nil {
		t.Fatal(err)
	}
	if err := refusedAndWroteNothing(t, db, ctx, second.Journal, "the inner merge after the outer one was undone"); !strings.Contains(err.Error(), "written or deleted after") {
		t.Fatalf("the refusal reads %q", err)
	}
}

// The undo is one transaction, and it never writes over what came after the
// merge. Widget is deleted after the merge, and with it the edge the merge
// had repointed: the undo is refused whole. ACME Ltd is not restored
// beside a canonical entity that still answers to its name.
func TestUnmergeEntitiesIsRefusedWholeWhenAnEdgeWasDeletedAfterTheMerge(t *testing.T) {
	for backend, open := range mergeBrains {
		t.Run(backend, func(t *testing.T) {
			db := open(t)
			ctx := context.Background()
			journal := mergeLtd(t, db)
			if err := db.Graph().DeleteNode(ctx, EntityNodeID("Widget")); err != nil {
				t.Fatal(err)
			}
			err := refusedAndWroteNothing(t, db, ctx, journal, "after Widget was deleted")
			if !strings.Contains(err.Error(), "written or deleted after") {
				t.Fatalf("the refusal reads %q", err)
			}
			if got := db.resolveEntityNameToNode(ctx, "ACME Ltd"); got != EntityNodeID("Acme Corp") {
				t.Fatalf("ACME Ltd resolves to %q after the refused undo, want the canonical entity alone", got)
			}
		})
	}
}

// An edge the merge repointed and that was rewritten since is not written
// back over.
func TestUnmergeEntitiesIsRefusedWhenAnEdgeWasRewrittenAfterTheMerge(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	journal := mergeLtd(t, db)
	edge := edgesTouching(t, db, EntityNodeID("Acme Corp"))[firstEdge(t, edgesTouching(t, db, EntityNodeID("Acme Corp")), EntityNodeID("Acme Corp"), EntityNodeID("Widget"))]
	edge.Weight, edge.ValidFrom = 7, time.Time{}
	if err := db.Graph().UpsertEdge(ctx, edge); err != nil {
		t.Fatal(err)
	}
	refusedAndWroteNothing(t, db, ctx, journal, "after an edge was rewritten")
	if after, err := db.Graph().GetEdgesBatch(ctx, []string{edge.ID}); err != nil || len(after) != 1 || after[0].Weight != 7 {
		t.Fatalf("the rewritten edge reads %+v (%v)", after, err)
	}
}

// The merge is one transaction: when the second alias cannot be folded, the
// first is not folded either.
func TestMergeEntitiesFoldsEveryAliasOrNone(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	if _, err := db.SQL().Exec(`CREATE TRIGGER block_second BEFORE DELETE ON graph_nodes WHEN OLD.id = '` + EntityNodeID("Acme Inc") + `' BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	t0 := db.Graph().Now()
	if _, err := db.GraphRAGTools().MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd", "Acme Inc"}}); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("a merge whose second alias cannot be folded: %v", err)
	}
	if diff := diffSince(t, db, t0); len(diff.Nodes)+len(diff.Edges) != 0 {
		t.Fatalf("the failed merge folded part of the table: %+v", diff)
	}
	if got := entityAliases(t, db, EntityNodeID("Acme Corp")); len(got) != 0 {
		t.Fatalf("the failed merge recorded %v", got)
	}
}

// A folded entity that carried aliases of its own, from a merge into it,
// hands them on: they are recorded on the canonical entity with its name,
// so the names folded earlier still reach an entity, and the undo takes
// back exactly what the merge recorded.
func TestMergeEntitiesHandsOnTheAliasesOfAFoldedEntity(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()
	acme := EntityNodeID("Acme Corp")
	if _, err := tools.MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "ACME Ltd", Aliases: []string{"Acme Inc"}}); err != nil {
		t.Fatal(err)
	}
	merged, err := tools.MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := entityAliases(t, db, acme); !slices.Equal(got, []string{"ACME Ltd", "Acme Inc"}) {
		t.Fatalf("the canonical entity carries %v, want the folded name and the alias it carried", got)
	}
	if got := db.resolveEntityNameToNode(ctx, "Acme Inc"); got != acme {
		t.Fatalf("Acme Inc resolves to %q, want %q", got, acme)
	}
	if _, err := tools.UnmergeEntities(ctx, ToolUnmergeEntitiesRequest{Journal: merged.Journal}); err != nil {
		t.Fatal(err)
	}
	if got := entityAliases(t, db, acme); len(got) != 0 {
		t.Fatalf("after the undo the canonical entity carries %v", got)
	}
	if got := entityAliases(t, db, EntityNodeID("ACME Ltd")); !slices.Equal(got, []string{"Acme Inc"}) {
		t.Fatalf("ACME Ltd came back carrying %v, want the alias it had", got)
	}
}

// A name recorded on the canonical entity after the merge, by another
// merge, is not one the undo of the first takes back.
func TestUnmergeEntitiesLeavesANameRecordedSince(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()
	first, err := tools.MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{{Name: "Acme Holdings", ChunkIDs: []string{graphChunkNodeID("a1", 0)}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"Acme Holdings"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.UnmergeEntities(ctx, ToolUnmergeEntitiesRequest{Journal: first.Journal}); err != nil {
		t.Fatal(err)
	}
	if got := entityAliases(t, db, EntityNodeID("Acme Corp")); !slices.Equal(got, []string{"Acme Holdings"}) {
		t.Fatalf("the canonical entity carries %v, want the name the second merge recorded", got)
	}
}

// Under an ontology that closes the generic writes, the two are closed like
// upsert_entities.
func TestMergeAndUnmergeAreClosedUnderStrictActions(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	journal := mergeLtd(t, db)
	schema := aviationSchemaWithActions()
	schema.StrictActions = true
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GraphRAGTools().MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"Acme Inc"}}); err == nil || !strings.Contains(err.Error(), "strict_actions") {
		t.Fatalf("a merge under strict_actions: %v", err)
	}
	if err := refusedAndWroteNothing(t, db, ctx, journal, "under strict_actions"); !strings.Contains(err.Error(), "strict_actions") {
		t.Fatalf("an undo under strict_actions: %v", err)
	}
}

// A merge and an undo that cannot commit say so, and nothing of them stays.
// The commit is made to fail by a deferred constraint that a trigger breaks
// on the first row either of them writes.
func TestMergeAndUnmergeReportAFailedCommit(t *testing.T) {
	ctx := context.Background()
	breakCommit := func(t *testing.T, db *DB, event string) {
		t.Helper()
		for _, stmt := range []string{
			`CREATE TABLE zz_orphans (parent TEXT REFERENCES graph_nodes(id) DEFERRABLE INITIALLY DEFERRED)`,
			`CREATE TRIGGER zz_orphan AFTER ` + event + ` ON graph_nodes BEGIN INSERT INTO zz_orphans VALUES ('entity:nobody'); END`,
		} {
			if _, err := db.SQL().Exec(stmt); err != nil {
				t.Fatal(err)
			}
		}
	}
	merge := ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}}
	t.Run("merge", func(t *testing.T) {
		db := mergeBrain(t)
		breakCommit(t, db, "DELETE")
		t0 := db.Graph().Now()
		if _, err := db.GraphRAGTools().MergeEntities(ctx, merge); err == nil || !strings.Contains(err.Error(), "commit") {
			t.Fatalf("a merge that cannot commit: %v", err)
		}
		if diff := diffSince(t, db, t0); len(diff.Nodes)+len(diff.Edges) != 0 {
			t.Fatalf("the merge that could not commit left: %+v", diff)
		}
	})
	t.Run("undo", func(t *testing.T) {
		db := mergeBrain(t)
		merged, err := db.GraphRAGTools().MergeEntities(ctx, merge)
		if err != nil {
			t.Fatal(err)
		}
		breakCommit(t, db, "INSERT")
		if err := refusedAndWroteNothing(t, db, ctx, merged.Journal, "an undo that cannot commit"); !strings.Contains(err.Error(), "commit") {
			t.Fatalf("an undo that cannot commit: %v", err)
		}
	})
}

// A merge that cannot open its transaction says so: here another writer
// holds the brain's write turn for longer than the caller waits.
func TestMergeEntitiesReportsATransactionItCannotOpen(t *testing.T) {
	db := mergeBrain(t)
	held, err := db.SQL().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Rollback() }()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := db.GraphRAGTools().MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}}); err == nil || !strings.Contains(err.Error(), "merge_entities: begin") {
		t.Fatalf("a merge behind a held write turn: %v", err)
	}
}

// A merge that cannot keep the other writers out does not start.
func TestMergeEntitiesReportsALockItCannotTake(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	t0 := db.Graph().Now()
	restore := renameColumn(t, db, "graph_nodes", "id", "id_hidden")
	_, err := db.GraphRAGTools().MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}})
	restore()
	if err == nil || !strings.Contains(err.Error(), "lock the graph") {
		t.Fatalf("a merge that cannot take its lock: %v", err)
	}
	if diff := diffSince(t, db, t0); len(diff.Nodes)+len(diff.Edges) != 0 {
		t.Fatalf("it wrote: %+v", diff)
	}
}

// The undo names its ids a statement's worth at a time. With one id to a
// statement, a merge of several edges is undone whole, and an edge written
// since is found wherever it falls among them.
func TestUnmergeEntitiesReadsItsIDsInParts(t *testing.T) {
	defer func(n int) { unmergeIDsPerStatement = n }(unmergeIDsPerStatement)
	unmergeIDsPerStatement = 1
	ctx := context.Background()
	merge := ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd", "Acme Inc"}}
	db := mergeBrain(t)
	t0 := db.Graph().Now()
	merged, err := db.GraphRAGTools().MergeEntities(ctx, merge)
	if err != nil || len(merged.Journal.Edges) < 3 {
		t.Fatalf("%+v, %v: want a merge of several edges", merged, err)
	}
	undone, err := db.GraphRAGTools().UnmergeEntities(ctx, ToolUnmergeEntitiesRequest{Journal: merged.Journal})
	if err != nil || undone.EdgesRestored != len(merged.Journal.Edges) {
		t.Fatalf("%+v, %v: want every edge back", undone, err)
	}
	if diff := diffSince(t, db, t0); len(diff.Nodes)+len(diff.Edges) != 0 {
		t.Fatalf("the graph differs from the one before the merge: %+v", diff)
	}
	// Each live edge the merge repointed, rewritten in turn, refuses the undo.
	for _, id := range merged.Journal.Edges {
		db := mergeBrain(t)
		again, err := db.GraphRAGTools().MergeEntities(ctx, merge)
		if err != nil {
			t.Fatal(err)
		}
		live, err := db.Graph().GetEdgesBatch(ctx, []string{id})
		if err != nil || len(live) == 0 {
			continue
		}
		live[0].Weight, live[0].ValidFrom = 9, time.Time{}
		if err := db.Graph().UpsertEdge(ctx, live[0]); err != nil {
			t.Fatal(err)
		}
		refusedAndWroteNothing(t, db, ctx, again.Journal, "after "+id+" was rewritten")
	}
}

// The statement that keeps other writers out is the table lock on
// PostgreSQL and a write that takes the turn on SQLite.
func TestGraphWriteLockPerBackend(t *testing.T) {
	if got := graphWriteLock(sqldialect.Postgres); !strings.HasPrefix(got, "LOCK TABLE graph_nodes, graph_edges") {
		t.Fatalf("on PostgreSQL the transaction starts with %q", got)
	}
	if got := graphWriteLock(sqldialect.SQLite); !strings.HasPrefix(got, "DELETE FROM graph_nodes WHERE id IS NULL") {
		t.Fatalf("on SQLite the transaction starts with %q", got)
	}
}

// While a merge or an undo holds its transaction, no other writer changes
// the graph: a write waits for it, on both backends, and goes through once
// it ends.
func TestTheMergeTransactionKeepsOtherWritersOut(t *testing.T) {
	for backend, open := range mergeBrains {
		t.Run(backend, func(t *testing.T) {
			db := open(t)
			ctx := context.Background()
			tx, _, err := db.GraphRAGTools().mergeTransaction(ctx)
			if err != nil {
				t.Fatal(err)
			}
			write := func(wait time.Duration) error {
				waiting, cancel := context.WithTimeout(ctx, wait)
				defer cancel()
				return db.Graph().UpsertNode(waiting, &graph.GraphNode{ID: EntityNodeID("ACME Ltd"), NodeType: "entity", Content: "written meanwhile"})
			}
			if err := write(300 * time.Millisecond); err == nil {
				_ = tx.Rollback()
				t.Fatal("another writer wrote an entity while the merge held its transaction")
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := write(10 * time.Second); err != nil {
				t.Fatalf("after the merge ended the writer is still refused: %v", err)
			}
		})
	}
}

// Two merges into one canonical entity are two instants. The journal of
// the second with an edge the first took is refused, and its own journal
// undoes the second alone.
func TestAnUndoTakesItsOwnInstantNotAnEarlierMergeIntoTheSameEntity(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()
	first, err := tools.MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"Acme Inc"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := tools.MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}})
	if err != nil {
		t.Fatal(err)
	}
	forged := second.Journal
	forged.Edges = append(slices.Clone(forged.Edges), first.Journal.Edges...)
	refusedAndWroteNothing(t, db, ctx, forged, "the second journal with the first merge's edges")
	undone, err := tools.UnmergeEntities(ctx, ToolUnmergeEntitiesRequest{Journal: second.Journal})
	if err != nil || !slices.Equal(undone.Restored, []string{EntityNodeID("ACME Ltd")}) {
		t.Fatalf("the second merge alone: %+v, %v", undone, err)
	}
	if _, err := db.Graph().GetNode(ctx, EntityNodeID("Acme Inc")); err == nil {
		t.Fatal("undoing the second merge brought back the alias of the first")
	}
}

// The canonical entity must be the caller's when the undo runs: once
// another collection names it, a confined undo would write an entity the
// key cannot read whole, and is refused as on an entity that is not there.
func TestUnmergeEntitiesIsRefusedOnceTheCanonicalEntityIsShared(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	journal := mergeLtd(t, db)
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "b3", Collection: "beta", Content: "Acme Corp runs a fund.",
		Entities: []ToolEntityInput{{Name: "Acme Corp", ChunkIDs: []string{graphChunkNodeID("b3", 0)}}},
	}); err != nil {
		t.Fatal(err)
	}
	err := refusedAndWroteNothing(t, db, WithCallerScope(ctx, alphaScope()), journal, "alpha, canonical shared")
	if want := fmt.Sprintf("unmerge_entities: entity %q not found", EntityNodeID("Acme Corp")); err.Error() != want {
		t.Fatalf("alpha is told %q, want %q", err, want)
	}
	if _, err := db.GraphRAGTools().UnmergeEntities(ctx, ToolUnmergeEntitiesRequest{Journal: journal}); err != nil {
		t.Fatalf("an open caller's undo of the same journal: %v", err)
	}
}

// An alias written again and deleted again after the merge is not there
// now, and is still not what the merge left.
func TestUnmergeEntitiesIsRefusedWhenAnAliasCameAndWentSince(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	journal := mergeLtd(t, db)
	ltd := EntityNodeID("ACME Ltd")
	if err := db.Graph().UpsertNode(ctx, &graph.GraphNode{ID: ltd, NodeType: "entity", Content: "ACME Ltd", Properties: map[string]any{"name": "ACME Ltd"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Graph().DeleteNode(ctx, ltd); err != nil {
		t.Fatal(err)
	}
	if err := refusedAndWroteNothing(t, db, ctx, journal, "after the alias came and went"); !strings.Contains(err.Error(), "written or deleted after") {
		t.Fatalf("the refusal reads %q", err)
	}
}

// The merge and the undo read inside their transaction: on a pool of one
// connection, which the transaction holds, a read through the pool would
// wait for ever.
func TestMergeAndUnmergeNeedNoSecondConnection(t *testing.T) {
	db := mergeBrain(t)
	db.SQL().SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	merged, err := db.GraphRAGTools().MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}})
	if err != nil {
		t.Fatalf("merge on one connection: %v", err)
	}
	// The instant read back in another zone names the same merge.
	merged.Journal.MergedAt = merged.Journal.MergedAt.In(time.FixedZone("east", 3600))
	if _, err := db.GraphRAGTools().UnmergeEntities(ctx, ToolUnmergeEntitiesRequest{Journal: merged.Journal}); err != nil {
		t.Fatalf("undo on one connection: %v", err)
	}
}
