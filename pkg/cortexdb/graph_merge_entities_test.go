package cortexdb

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// mergeBrain holds two collections. Alpha writes Acme Corp under three
// spellings, with an edge from each alias, one between the two aliases and
// one from an alias to the canonical name; beta writes an entity of its own
// and one it shares with alpha.
func mergeBrain(t *testing.T) *DB {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "merge.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return seedMergeBrain(t, db)
}

// mergeBrains are the brain above on each backend.
var mergeBrains = map[string]func(*testing.T) *DB{
	"sqlite":   mergeBrain,
	"postgres": func(t *testing.T) *DB { return seedMergeBrain(t, openPostgresBrain(t, 4)) },
}

func seedMergeBrain(t *testing.T, db *DB) *DB {
	t.Helper()
	ctx := context.Background()
	a1, b1 := graphChunkNodeID("a1", 0), graphChunkNodeID("b1", 0)
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "a1", Collection: "alpha", Content: "Acme Corp, also ACME and Acme Inc, makes widgets for Kestrel Vale.",
		Entities: []ToolEntityInput{
			{Name: "Acme Corp", Description: "the canonical name", ChunkIDs: []string{a1}},
			{Name: "ACME Ltd", Description: "an abbreviation", ChunkIDs: []string{a1}},
			{Name: "Acme Inc", ChunkIDs: []string{a1}},
			{Name: "Widget", ChunkIDs: []string{a1}},
			{Name: "Kestrel Vale", ChunkIDs: []string{a1}},
		},
		Relations: []ToolRelationInput{
			{From: "ACME Ltd", To: "Widget", Type: "makes", ChunkIDs: []string{a1}},
			{From: "Kestrel Vale", To: "Acme Inc", Type: "buys_from", ChunkIDs: []string{a1}},
			{From: "ACME Ltd", To: "Acme Inc", Type: "same_as", ChunkIDs: []string{a1}},
			{From: "Acme Inc", To: "Acme Corp", Type: "same_as", ChunkIDs: []string{a1}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "b1", Collection: "beta", Content: "Heron Mill buys from Kestrel Vale.",
		Entities: []ToolEntityInput{
			{Name: "Heron Mill", ChunkIDs: []string{b1}},
			{Name: "Kestrel Vale", ChunkIDs: []string{b1}},
		},
		Relations: []ToolRelationInput{{From: "Heron Mill", To: "Kestrel Vale", Type: "buys_from", ChunkIDs: []string{b1}}},
	}); err != nil {
		t.Fatal(err)
	}
	return db
}

// edgesTouching counts the live edges that touch any of ids, once each.
func edgesTouching(t *testing.T, db *DB, ids ...string) map[string]*graph.GraphEdge {
	t.Helper()
	out := map[string]*graph.GraphEdge{}
	for _, id := range ids {
		edges, err := db.Graph().GetEdges(context.Background(), id, "both")
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edges {
			out[e.ID] = e
		}
	}
	return out
}

// diffSince is what the whole graph changed since at.
func diffSince(t *testing.T, db *DB, at time.Time) *graph.GraphDiffResult {
	t.Helper()
	diff, err := db.GraphDiff(context.Background(), at, db.Graph().Now(), graph.DiffOptions{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return diff
}

// A merge repoints every edge of the aliases onto the canonical entity,
// drops the self-loops it makes, and keeps each alias and each edge as it
// stood before in history, marked merged into the canonical entity.
func TestMergeEntitiesRepointsTheEdgesAndKeepsTheVersionBefore(t *testing.T) {
	for backend, open := range mergeBrains {
		t.Run(backend, func(t *testing.T) { mergeRepoints(t, open(t)) })
	}
}

func mergeRepoints(t *testing.T, db *DB) {
	ctx := context.Background()
	acme, ltd, inc := EntityNodeID("Acme Corp"), EntityNodeID("ACME Ltd"), EntityNodeID("Acme Inc")
	before := edgesTouching(t, db, ltd, inc)

	resp, err := db.GraphRAGTools().MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd", "Acme Inc", "Acme Corp", "acme ltd", " "}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Canonical != acme || !slices.Equal(resp.Merged, []string{ltd, inc}) {
		t.Fatalf("merged %v into %s, want the two aliases once each, the canonical name left out: %+v", resp.Merged, resp.Canonical, resp)
	}
	// ACME Ltd -> Acme Inc and Acme Inc -> Acme Corp become loops.
	if resp.LoopsRemoved != 2 || resp.EdgesRepointed != len(before)-2 {
		t.Fatalf("repointed %d and removed %d loops, want %d and 2", resp.EdgesRepointed, resp.LoopsRemoved, len(before)-2)
	}
	if got := slices.Sorted(slices.Values(resp.Journal.Edges)); !slices.Equal(got, slices.Sorted(mapsKeys(before))) {
		t.Fatalf("the journal names the edges %v, want every edge the merge touched %v", got, slices.Sorted(mapsKeys(before)))
	}
	for _, alias := range []string{ltd, inc} {
		if _, err := db.Graph().GetNode(ctx, alias); err == nil {
			t.Fatalf("%s is still a live node", alias)
		}
	}
	after := edgesTouching(t, db, acme)
	if e := after[firstEdge(t, before, ltd, EntityNodeID("Widget"))]; e == nil || e.FromNodeID != acme {
		t.Fatalf("ACME Ltd makes Widget was not repointed onto Acme Corp: %+v", e)
	}
	for _, e := range after {
		if e.FromNodeID == e.ToNodeID {
			t.Fatalf("the merge left a self-loop: %+v", e)
		}
	}
	// The history: each alias as it stood, retracted as merged into Acme Corp
	// by this tool.
	diff := diffSince(t, db, resp.Journal.MergedAt.Add(-time.Microsecond))
	retracted := 0
	for _, change := range diff.Nodes {
		if change.ID != ltd && change.ID != inc {
			continue
		}
		retracted++
		inv := change.Invalidation
		if change.Kind != graph.DiffRetracted || inv == nil || inv.Reason != graph.ReasonMerged || inv.SupersededBy != acme || inv.Producer != producerMergeEntities {
			t.Fatalf("%s left the graph as %+v, want retracted, merged into %s by %s", change.ID, change, acme, producerMergeEntities)
		}
	}
	if retracted != 2 {
		t.Fatalf("the diff shows %d of the two aliases retracted: %+v", retracted, diff.Nodes)
	}
	old, err := db.Graph().GetNode(graph.AsOf(ctx, resp.Journal.MergedAt.Add(-time.Microsecond)), ltd)
	if err != nil || old.Properties["description"] != "an abbreviation" {
		t.Fatalf("the version of ACME Ltd before the merge is not readable: %+v, %v", old, err)
	}
}

func mapsKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func firstEdge(t *testing.T, edges map[string]*graph.GraphEdge, from, to string) string {
	t.Helper()
	for id, e := range edges {
		if e.FromNodeID == from && e.ToNodeID == to {
			return id
		}
	}
	t.Fatalf("no edge %s -> %s", from, to)
	return ""
}

// dry_run answers what the merge would do, journal included, and writes
// nothing: the graph is the same afterwards, history included.
func TestMergeEntitiesDryRunWritesNothing(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	req := ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd", "Acme Inc"}, DryRun: true}
	start := db.Graph().Now()
	dry, err := db.GraphRAGTools().MergeEntities(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !dry.DryRun || !dry.Journal.MergedAt.IsZero() {
		t.Fatalf("the report does not say it was a dry run: %+v", dry)
	}
	if diff := diffSince(t, db, start); len(diff.Nodes)+len(diff.Edges) != 0 {
		t.Fatalf("a dry run changed the graph: %+v", diff)
	}
	req.DryRun = false
	wet, err := db.GraphRAGTools().MergeEntities(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if wet.EdgesRepointed != dry.EdgesRepointed || wet.LoopsRemoved != dry.LoopsRemoved || !slices.Equal(wet.Merged, dry.Merged) || !slices.Equal(wet.Journal.Edges, dry.Journal.Edges) {
		t.Fatalf("the dry run said %+v, the merge did %+v", dry, wet)
	}
}

// A merge names its canonical entity and at least one alias, and an entity
// that is not there refuses the whole call before anything is written.
func TestMergeEntitiesRefusesAnIncompleteOrUnknownTable(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	t0 := db.Graph().Now()
	for _, req := range []ToolMergeEntitiesRequest{
		{Aliases: []string{"ACME Ltd"}},
		{Canonical: "Acme Corp"},
		{Canonical: "Acme Corp", Aliases: []string{"Acme Corp", " "}},
		{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd", "No Such Firm"}},
		{Canonical: "No Such Firm", Aliases: []string{"ACME Ltd"}},
	} {
		if _, err := db.GraphRAGTools().MergeEntities(ctx, req); err == nil {
			t.Fatalf("%+v was merged", req)
		}
	}
	if diff := diffSince(t, db, t0); len(diff.Nodes)+len(diff.Edges) != 0 {
		t.Fatalf("a refused merge wrote: %+v", diff)
	}
	db.Close()
	if _, err := db.GraphRAGTools().MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}}); err == nil {
		t.Fatal("a merge on a closed brain succeeded")
	}
}

// Under a caller scope a merge takes only entities every collection of which
// the caller may read: an entity of another collection, one shared with
// another collection and one that does not exist are refused alike, so the
// refusal does not tell the caller what another collection holds. A caller
// with no knowledge rule, confined on memory alone, merges nothing.
func TestMergeEntitiesUnderACallerScopeTakesOnlyTheCallersEntities(t *testing.T) {
	db := mergeBrain(t)
	alpha := WithCallerScope(context.Background(), alphaScope())
	// An entity no collection lists, as upsert_entities writes one without a
	// chunk, is no caller's.
	if _, err := db.GraphRAGTools().UpsertEntities(context.Background(), ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{{Name: "Loose End"}}}); err != nil {
		t.Fatal(err)
	}
	t0 := db.Graph().Now()
	refusals := map[string]string{}
	for _, alias := range []string{"Heron Mill", "Kestrel Vale", "Loose End", "No Such Firm"} {
		_, err := db.GraphRAGTools().MergeEntities(alpha, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{alias}})
		if err == nil {
			t.Fatalf("alpha merged %s", alias)
		}
		refusals[alias] = strings.ReplaceAll(err.Error(), EntityNodeID(alias), "X")
	}
	if refusals["Heron Mill"] != refusals["No Such Firm"] || refusals["Kestrel Vale"] != refusals["No Such Firm"] || refusals["Loose End"] != refusals["No Such Firm"] {
		t.Fatalf("the refusals tell the entities apart: %v", refusals)
	}
	if _, err := db.GraphRAGTools().MergeEntities(alpha, ToolMergeEntitiesRequest{Canonical: "Heron Mill", Aliases: []string{"ACME Ltd"}}); err == nil {
		t.Fatal("alpha merged its entity into one of beta's")
	}
	memoryOnly := WithCallerScope(context.Background(), CallerScope{Key: "hermes", Memory: func(MemoryBucket) bool { return true }})
	if _, err := db.GraphRAGTools().MergeEntities(memoryOnly, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}}); err == nil {
		t.Fatal("a caller confined on memory merged entities")
	}
	if diff := diffSince(t, db, t0); len(diff.Nodes)+len(diff.Edges) != 0 {
		t.Fatalf("a refused merge wrote: %+v", diff)
	}
	resp, err := db.GraphRAGTools().MergeEntities(alpha, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}})
	if err != nil || len(resp.Merged) != 1 {
		t.Fatalf("alpha could not merge its own entities: %+v, %v", resp, err)
	}
}

// Merging then unmerging from the journal gives the graph back: the diff
// between the instant before the merge and after the unmerge is empty, and
// a second unmerge changes nothing.
func TestMergeThenUnmergeGivesTheGraphBack(t *testing.T) {
	for backend, open := range mergeBrains {
		t.Run(backend, func(t *testing.T) { mergeThenUnmerge(t, open(t)) })
	}
}

func mergeThenUnmerge(t *testing.T, db *DB) {
	ctx := context.Background()
	tools := db.GraphRAGTools()
	// A loop the canonical entity had before the merge, which the merge
	// drops with the ones it makes.
	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{{From: "Acme Corp", To: "Acme Corp", Type: "owns"}}}); err != nil {
		t.Fatal(err)
	}
	t0 := db.Graph().Now()
	merged, err := tools.MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd", "Acme Inc"}})
	if err != nil {
		t.Fatal(err)
	}
	if merged.LoopsRemoved != 3 {
		t.Fatalf("removed %d loops, want the two the merge made and the one Acme Corp had", merged.LoopsRemoved)
	}
	merging := db.Graph().Now()
	// The journal travels as JSON, the way a client keeps it.
	raw, err := json.Marshal(merged.Journal)
	if err != nil {
		t.Fatal(err)
	}
	var journal EntityMergeJournal
	if err := json.Unmarshal(raw, &journal); err != nil {
		t.Fatal(err)
	}
	undone, err := tools.UnmergeEntities(ctx, ToolUnmergeEntitiesRequest{Journal: journal})
	if err != nil {
		t.Fatal(err)
	}
	if !sameIDs(undone.Restored, merged.Merged) || undone.EdgesRestored != len(journal.Edges) {
		t.Fatalf("restored %+v, want %v and %d edges", undone, merged.Merged, len(journal.Edges))
	}
	if diff := diffSince(t, db, t0); len(diff.Nodes)+len(diff.Edges) != 0 {
		raw, _ := json.Marshal(diff)
		t.Fatalf("the graph differs from the one before the merge: %s", raw)
	}
	// The aliases and edges come back from now on: the past keeps the
	// merge, and the restored versions are not backdated.
	for _, alias := range merged.Merged {
		if _, err := db.Graph().GetNode(graph.AsOf(ctx, merging), alias); err == nil {
			t.Fatalf("%s reads as live while it was merged", alias)
		}
		node, err := db.Graph().GetNode(ctx, alias)
		if err != nil || !node.ValidFrom.After(merging) {
			t.Fatalf("%s restored as %+v, want valid from after the merge at %v: %v", alias, node, merging, err)
		}
	}
	edges, err := db.Graph().GetEdgesBatch(ctx, journal.Edges)
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range edges {
		if !edge.ValidFrom.After(merging) {
			t.Fatalf("edge %s restored valid from %v, want after the merge at %v", edge.ID, edge.ValidFrom, merging)
		}
	}
	// A merge is undone once: the journal played again finds the aliases
	// there, and writes nothing.
	undoing := db.Graph().Now()
	if _, err := tools.UnmergeEntities(ctx, ToolUnmergeEntitiesRequest{Journal: journal}); err == nil || !strings.Contains(err.Error(), "exists again") {
		t.Fatalf("the journal was applied a second time: %v", err)
	}
	if diff := diffSince(t, db, undoing); len(diff.Nodes)+len(diff.Edges) != 0 {
		t.Fatalf("the refused second undo wrote: %+v", diff)
	}
	// Merged again and undone again, from the second journal.
	again, err := tools.MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd", "Acme Inc"}})
	if err != nil {
		t.Fatal(err)
	}
	// The first journal names versions the first undo wrote over: refused.
	if _, err := tools.UnmergeEntities(ctx, ToolUnmergeEntitiesRequest{Journal: journal}); err == nil {
		t.Fatal("the first journal undid the second merge")
	}
	if _, err := tools.UnmergeEntities(ctx, ToolUnmergeEntitiesRequest{Journal: again.Journal}); err != nil {
		t.Fatalf("the second merge could not be undone from its own journal: %v", err)
	}
	if diff := diffSince(t, db, t0); len(diff.Nodes)+len(diff.Edges) != 0 {
		raw, _ := json.Marshal(diff)
		t.Fatalf("after the second undo the graph differs from the one before the first merge: %s", raw)
	}
}

// unmerge_entities reads what it restores from history, as of the instant
// the journal names, so a journal restores only what a merge took: an alias
// or an edge that did not exist then, or an edge that touched none of the
// aliases, refuses the whole call. Under a caller scope the aliases are held
// to the rule a merge follows.
func TestUnmergeEntitiesRestoresOnlyWhatTheMergeTook(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()
	merged, err := tools.MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}})
	if err != nil {
		t.Fatal(err)
	}
	other := firstEdge(t, edgesTouching(t, db, EntityNodeID("Heron Mill")), EntityNodeID("Heron Mill"), EntityNodeID("Kestrel Vale"))
	j := merged.Journal
	t0 := db.Graph().Now()
	for name, journal := range map[string]EntityMergeJournal{
		"empty":            {},
		"no instant":       {Canonical: j.Canonical, Aliases: j.Aliases, Edges: j.Edges},
		"no alias":         {Canonical: j.Canonical, Edges: j.Edges, MergedAt: j.MergedAt},
		"no edge":          {Canonical: j.Canonical, Aliases: j.Aliases, MergedAt: j.MergedAt},
		"another instant":  {Canonical: j.Canonical, Aliases: j.Aliases, Edges: j.Edges, MergedAt: j.MergedAt.Add(time.Microsecond)},
		"unknown alias":    {Canonical: j.Canonical, Aliases: []string{EntityNodeID("No Such Firm")}, Edges: j.Edges, MergedAt: j.MergedAt},
		"unknown edge":     {Canonical: j.Canonical, Aliases: j.Aliases, Edges: append(slices.Clone(j.Edges), "no-such-edge"), MergedAt: j.MergedAt},
		"unrelated edge":   {Canonical: j.Canonical, Aliases: j.Aliases, Edges: append(slices.Clone(j.Edges), other), MergedAt: j.MergedAt},
		"beta's entity":    {Canonical: j.Canonical, Aliases: []string{EntityNodeID("Heron Mill")}, MergedAt: j.MergedAt},
		"shared with beta": {Canonical: j.Canonical, Aliases: []string{EntityNodeID("Kestrel Vale")}, MergedAt: j.MergedAt},
	} {
		scoped := ctx
		if strings.Contains(name, "beta") {
			scoped = WithCallerScope(ctx, alphaScope())
		}
		if _, err := tools.UnmergeEntities(scoped, ToolUnmergeEntitiesRequest{Journal: journal}); err == nil {
			t.Fatalf("%s: the journal was applied", name)
		}
	}
	if diff := diffSince(t, db, t0); len(diff.Nodes)+len(diff.Edges) != 0 {
		t.Fatalf("a refused unmerge wrote: %+v", diff)
	}
	if _, err := tools.UnmergeEntities(WithCallerScope(ctx, alphaScope()), ToolUnmergeEntitiesRequest{Journal: j}); err != nil {
		t.Fatalf("alpha could not undo its own merge: %v", err)
	}
	db.Close()
	if _, err := tools.UnmergeEntities(ctx, ToolUnmergeEntitiesRequest{Journal: j}); err == nil {
		t.Fatal("an unmerge on a closed brain succeeded")
	}
}

// Both tools are reachable through Call and over MCP, the way a port and a
// host reach them, and both are writes.
func TestMergeAndUnmergeAreReachableAsTools(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	out, err := db.GraphRAGTools().Call(ctx, "merge_entities", json.RawMessage(`{"canonical":"Acme Corp","aliases":["ACME Ltd"],"dry_run":true}`))
	if err != nil {
		t.Fatal(err)
	}
	journal := out.(*ToolMergeEntitiesResponse).Journal
	raw, _ := json.Marshal(map[string]any{"journal": journal})
	// A dry run merged nothing, so its journal undoes nothing.
	if _, err := db.GraphRAGTools().Call(ctx, "unmerge_entities", raw); err == nil {
		t.Fatal("the journal of a dry run was applied")
	}
	for _, name := range []string{"merge_entities", "unmerge_entities"} {
		if _, err := db.GraphRAGTools().Call(ctx, name, json.RawMessage(`{"canonical":`)); err == nil {
			t.Fatalf("%s took malformed JSON", name)
		}
	}
	merged, err := callOverMCP(t, db, "merge_entities", map[string]any{"canonical": "Acme Corp", "aliases": []string{"ACME Ltd"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := callOverMCP(t, db, "unmerge_entities", map[string]any{"journal": merged["journal"]}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Graph().GetNode(ctx, EntityNodeID("ACME Ltd")); err != nil {
		t.Fatalf("the unmerge over MCP did not restore ACME Ltd: %v", err)
	}
	if _, err := callOverMCP(t, db, "merge_entities", map[string]any{"canonical": "No Such Firm", "aliases": []string{"ACME Ltd"}}); err == nil {
		t.Fatal("merge_entities over MCP merged into an entity that does not exist")
	}
	if _, err := callOverMCP(t, db, "unmerge_entities", map[string]any{"journal": map[string]any{}}); err == nil {
		t.Fatal("unmerge_entities over MCP took an empty journal")
	}
}

// Each read and write a merge or an unmerge makes can fail, and the call
// then says so and leaves the merge or the unmerge undone.
func TestMergeAndUnmergeReportEveryFailedReadOrWrite(t *testing.T) {
	ctx := context.Background()
	merge := ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}}
	t.Run("edges of the entities", func(t *testing.T) {
		db := mergeBrain(t)
		renameColumn(t, db, "graph_edges", "edge_type", "edge_type_hidden")
		if _, err := db.GraphRAGTools().MergeEntities(ctx, merge); err == nil || !strings.Contains(err.Error(), "read the edges") {
			t.Fatalf("a merge whose edges cannot be read: %v", err)
		}
	})
	t.Run("the merge", func(t *testing.T) {
		db := mergeBrain(t)
		if _, err := db.SQL().Exec(`CREATE TRIGGER block_repoint BEFORE UPDATE ON graph_edges BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.GraphRAGTools().MergeEntities(ctx, merge); err == nil || !strings.Contains(err.Error(), "blocked") {
			t.Fatalf("a merge whose repoint fails: %v", err)
		}
		if _, err := db.Graph().GetNode(ctx, EntityNodeID("ACME Ltd")); err != nil {
			t.Fatalf("the failed merge took ACME Ltd: %v", err)
		}
	})
	for name, failure := range map[string]func(db *DB){
		"edges as they are":        func(db *DB) { renameColumn(t, db, "graph_edges", "valid_from", "valid_from_hidden") },
		"entities as they are":     func(db *DB) { renameColumn(t, db, "graph_nodes", "vector", "vector_hidden") },
		"history of the aliases":   func(db *DB) { renameColumn(t, db, "graph_node_history", "reason", "reason_hidden") },
		"history of the edges":     func(db *DB) { renameColumn(t, db, "graph_edge_history", "reason", "reason_hidden") },
		"aliases as they stood":    func(db *DB) { renameColumn(t, db, "graph_node_history", "vector", "vector_hidden") },
		"edges as they stood":      func(db *DB) { renameColumn(t, db, "graph_edge_history", "weight", "weight_hidden") },
		"version an edge replaces": func(db *DB) { renameColumn(t, db, "graph_edge_history", "producer", "producer_hidden") },
		"restore an alias": func(db *DB) {
			if _, err := db.SQL().Exec(`CREATE TRIGGER block_restore BEFORE INSERT ON graph_nodes BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
				t.Fatal(err)
			}
		},
		"restore an edge": func(db *DB) {
			if _, err := db.SQL().Exec(`CREATE TRIGGER block_restore BEFORE INSERT ON graph_edges BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			db := mergeBrain(t)
			merged, err := db.GraphRAGTools().MergeEntities(ctx, merge)
			if err != nil {
				t.Fatal(err)
			}
			undoing := db.Graph().Now()
			failure(db)
			if _, err := db.GraphRAGTools().UnmergeEntities(ctx, ToolUnmergeEntitiesRequest{Journal: merged.Journal}); err == nil {
				t.Fatalf("%s failed and the unmerge succeeded", name)
			}
			// Whatever failed, the undo wrote nothing: the trigger cases can
			// still read the graph and show it.
			if strings.HasPrefix(name, "restore") {
				if diff := diffSince(t, db, undoing); len(diff.Nodes)+len(diff.Edges) != 0 {
					t.Fatalf("%s failed and the undo left: %+v", name, diff)
				}
			}
		})
	}
}

// entityAliases is the alias list a node carries, as entity resolution
// records it.
func entityAliases(t *testing.T, db *DB, id string) []string {
	t.Helper()
	node, err := db.Graph().GetNode(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return toStringSlice(node.Properties["aliases"])
}

// A merge records the names it folded on the canonical entity, the place
// --resolve-entities records them, so a later call that names an alias
// reaches the canonical entity instead of an entity that is no longer there.
func TestMergeEntitiesRecordsTheFoldedNamesOnTheCanonicalEntity(t *testing.T) {
	for backend, open := range mergeBrains {
		t.Run(backend, func(t *testing.T) { mergeRecordsNames(t, open(t)) })
	}
}

func mergeRecordsNames(t *testing.T, db *DB) {
	ctx := context.Background()
	acme := EntityNodeID("Acme Corp")
	if _, err := db.GraphRAGTools().MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"acme ltd", "Acme Inc"}}); err != nil {
		t.Fatal(err)
	}
	if got := entityAliases(t, db, acme); !slices.Equal(got, []string{"ACME Ltd", "Acme Inc"}) {
		t.Fatalf("the canonical entity carries the aliases %v, want each folded node's name once, a spelling that resolves alike left out", got)
	}
	for _, name := range []string{"ACME Ltd", "Acme Inc"} {
		if got := db.resolveEntityNameToNode(ctx, name); got != acme {
			t.Fatalf("%q resolves to %q after the merge, want %q", name, got, acme)
		}
	}
}

// A dry run records no name.
func TestMergeEntitiesDryRunRecordsNoName(t *testing.T) {
	db := mergeBrain(t)
	if _, err := db.GraphRAGTools().MergeEntities(context.Background(), ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if got := entityAliases(t, db, EntityNodeID("Acme Corp")); len(got) != 0 {
		t.Fatalf("a dry run recorded the aliases %v", got)
	}
}

// Undoing a merge takes back the names it recorded and only those: a name
// the canonical entity carried before the merge stays, and a folded name
// that resolves alike (another case) is not recorded beside it.
func TestUnmergeEntitiesTakesBackOnlyTheNamesTheMergeRecorded(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	acme := EntityNodeID("Acme Corp")
	node, err := db.Graph().GetNode(ctx, acme)
	if err != nil {
		t.Fatal(err)
	}
	node.Properties["aliases"] = []string{"ACME INC"}
	if err := db.Graph().UpsertNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	resp, err := db.GraphRAGTools().MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd", "Acme Inc"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := entityAliases(t, db, acme); !slices.Equal(got, []string{"ACME INC", "ACME Ltd"}) {
		t.Fatalf("after the merge the canonical entity carries %v, want the alias it had and the one name the merge added", got)
	}
	if _, err := db.GraphRAGTools().UnmergeEntities(ctx, ToolUnmergeEntitiesRequest{Journal: resp.Journal}); err != nil {
		t.Fatal(err)
	}
	if got := entityAliases(t, db, acme); !slices.Equal(got, []string{"ACME INC"}) {
		t.Fatalf("after the undo the canonical entity carries %v, want the alias it had before", got)
	}
}

// A failed write of the names is reported, by the merge and by the undo,
// and takes the whole merge or the whole undo with it: the names are
// written in the transaction that folds or restores.
func TestAFailedWriteOfTheNamesUndoesTheMergeOrTheUndo(t *testing.T) {
	ctx := context.Background()
	merge := ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}}
	block := func(t *testing.T, db *DB) {
		t.Helper()
		if _, err := db.SQL().Exec(`CREATE TRIGGER block_names BEFORE INSERT ON graph_nodes WHEN NEW.id = '` + EntityNodeID("Acme Corp") + `' BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("merge", func(t *testing.T) {
		db := mergeBrain(t)
		block(t, db)
		t0 := db.Graph().Now()
		if _, err := db.GraphRAGTools().MergeEntities(ctx, merge); err == nil || !strings.Contains(err.Error(), "blocked") {
			t.Fatalf("a merge whose names cannot be written: %v", err)
		}
		if diff := diffSince(t, db, t0); len(diff.Nodes)+len(diff.Edges) != 0 {
			t.Fatalf("the merge whose names could not be written folded: %+v", diff)
		}
	})
	t.Run("undo", func(t *testing.T) {
		db := mergeBrain(t)
		merged, err := db.GraphRAGTools().MergeEntities(ctx, merge)
		if err != nil {
			t.Fatal(err)
		}
		block(t, db)
		t0 := db.Graph().Now()
		if _, err := db.GraphRAGTools().UnmergeEntities(ctx, ToolUnmergeEntitiesRequest{Journal: merged.Journal}); err == nil || !strings.Contains(err.Error(), "blocked") {
			t.Fatalf("an undo whose names cannot be taken back: %v", err)
		}
		if diff := diffSince(t, db, t0); len(diff.Nodes)+len(diff.Edges) != 0 {
			t.Fatalf("the undo whose names could not be taken back restored: %+v", diff)
		}
	})
}

// An undo whose canonical entity is gone is refused before it writes
// anything: no alias comes back.
func TestUnmergeEntitiesIsRefusedWhenTheCanonicalEntityIsGone(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	merged, err := db.GraphRAGTools().MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Graph().DeleteNode(ctx, EntityNodeID("Acme Corp")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GraphRAGTools().UnmergeEntities(ctx, ToolUnmergeEntitiesRequest{Journal: merged.Journal}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("an undo without its canonical entity: %v", err)
	}
	if nodes, err := db.Graph().GetNodesBatch(ctx, []string{EntityNodeID("ACME Ltd")}); err != nil || len(nodes) != 0 {
		t.Fatalf("the refused undo brought back %v (%v)", nodes, err)
	}
}

// A merge that records no new name leaves the canonical entity as it was.
func TestMergeEntitiesWithNoNewNameLeavesTheCanonicalEntityAlone(t *testing.T) {
	db := mergeBrain(t)
	ctx := context.Background()
	acme := EntityNodeID("Acme Corp")
	node, err := db.Graph().GetNode(ctx, acme)
	if err != nil {
		t.Fatal(err)
	}
	node.Properties["aliases"] = []string{"ACME Ltd"}
	if err := db.Graph().UpsertNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	before, err := db.Graph().GetNode(ctx, acme)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := db.GraphRAGTools().MergeEntities(ctx, ToolMergeEntitiesRequest{Canonical: "Acme Corp", Aliases: []string{"ACME Ltd"}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := db.Graph().GetNode(ctx, acme)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Merged) != 1 || !after.ValidFrom.Equal(before.ValidFrom) || !slices.Equal(toStringSlice(after.Properties["aliases"]), []string{"ACME Ltd"}) {
		t.Fatalf("merged %v, version %v then %v, aliases %v: want no name added and the entity unwritten", resp.Merged, before.ValidFrom, after.ValidFrom, after.Properties["aliases"])
	}
}
