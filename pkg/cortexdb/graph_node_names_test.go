package cortexdb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
)

type findNodesRun struct {
	answers       map[string]string
	index, scan   int
	matchedByName map[string]string
}

// findCountingStages asks for names in one call and returns, per name, the
// ids served in order with the strength of the match, and which read ran.
func findCountingStages(t *testing.T, tools *GraphRAGToolbox, req ToolFindNodesRequest) findNodesRun {
	t.Helper()
	ctx, stages := observability.WithStages(context.Background())
	resp, err := tools.FindNodes(ctx, req)
	if err != nil {
		t.Fatalf("find %v: %v", req.Names, err)
	}
	run := findNodesRun{answers: map[string]string{}, matchedByName: map[string]string{}}
	for _, m := range resp.Matches {
		ids := make([]string, 0, len(m.Nodes))
		for _, n := range m.Nodes {
			ids = append(ids, n.ID)
		}
		run.answers[m.Name] = m.Match + " " + strings.Join(ids, ",")
		run.matchedByName[m.Name] = m.Match
	}
	run.index, run.scan = stageCount(stages, "find-nodes-index"), stageCount(stages, "find-nodes-scan")
	return run
}

// Names that fold to three characters or more are read through the name
// index, and are answered as the scan answers them: the same nodes in the
// same order, with the same strength, for chunks too and with skip_chunks.
// The nodes were written before the index existed: the first call indexes
// them.
func TestFindNodesReadsTheNameIndexAndAnswersAsTheScan(t *testing.T) {
	db, tools, ctx := findNodesTestDB(t)
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "notes", Content: "Every two phase commit needs a coordinator; a limit on retries keeps it live."}); err != nil {
		t.Fatalf("save: %v", err)
	}
	names := []string{"Limit", "two-phase COMMIT", "TwoPhaseCommit", "极限", "Storage", "Quokka"}
	for _, skip := range []bool{false, true} {
		indexed := findCountingStages(t, tools, ToolFindNodesRequest{Names: names[:len(names)-3], SkipChunks: skip})
		if indexed.index != 1 || indexed.scan != 0 {
			t.Fatalf("skip=%v: index read ran %d times, scan %d, want 1 and 0", skip, indexed.index, indexed.scan)
		}
		// A two-character name has no trigram: the call scans, for every name.
		scanned := findCountingStages(t, tools, ToolFindNodesRequest{Names: append([]string{"极限"}, names[:len(names)-3]...), SkipChunks: skip})
		if scanned.index != 0 || scanned.scan != 1 {
			t.Fatalf("skip=%v: index read ran %d times, scan %d, want 0 and 1", skip, scanned.index, scanned.scan)
		}
		for name, want := range scanned.answers {
			if name == "极限" {
				continue
			}
			if got := indexed.answers[name]; got != want {
				t.Errorf("skip=%v %q: index %q, scan %q", skip, name, got, want)
			}
		}
		if !skip && !strings.Contains(indexed.answers["two-phase COMMIT"], "chunk:") {
			t.Errorf("the chunk that spells it is a match by containment: %q", indexed.answers["two-phase COMMIT"])
		}
		if skip && strings.Contains(indexed.answers["two-phase COMMIT"], "chunk:") {
			t.Errorf("skip_chunks: no chunk is served: %q", indexed.answers["two-phase COMMIT"])
		}
	}
	if m := findCountingStages(t, tools, ToolFindNodesRequest{Names: []string{"TwoPhaseCommit"}}).matchedByName["TwoPhaseCommit"]; m != "fold" {
		t.Errorf("TwoPhaseCommit against Two-Phase Commit: %q, want fold", m)
	}
}

// Whatever writes a node, the next call sees it as written: a new node, a
// node whose content changed (no longer found by its old name), one replaced
// by INSERT OR REPLACE, one renamed by id, and one deleted.
func TestTheNameIndexFollowsEveryWrite(t *testing.T) {
	db, tools, ctx := findNodesTestDB(t)
	names := []string{"Adam Optimizer", "Infinite Limit", "Left-Hand Limit", "Two-Phase Commit", "Storage", "Blocked Write"}
	before := findCountingStages(t, tools, ToolFindNodesRequest{Names: names})
	if before.index != 1 || before.answers["Adam Optimizer"] != " " || before.answers["Storage"] == " " {
		t.Fatalf("before: %+v", before)
	}

	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{{Name: "Adam Optimizer", Type: "Method"}}}); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, name := range []string{"Infinite Limit", "Left-Hand Limit", "Two-Phase Commit", "Chapter 4: Storage"} {
		var id string
		if err := db.SQL().QueryRow(`SELECT id FROM graph_nodes WHERE content = ?`, name).Scan(&id); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		ids[name] = id
	}
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE graph_nodes SET content = 'Blocked Write' WHERE id = ?`, []any{ids["Infinite Limit"]}},
		{`INSERT OR REPLACE INTO graph_nodes (id, vector, content, node_type) SELECT id, vector, 'Right-Hand Limit', node_type FROM graph_nodes WHERE id = ?`, []any{ids["Left-Hand Limit"]}},
		{`UPDATE graph_nodes SET id = 'renamed:2pc' WHERE id = ?`, []any{ids["Two-Phase Commit"]}},
		{`DELETE FROM graph_nodes WHERE id = ?`, []any{ids["Chapter 4: Storage"]}},
	} {
		if _, err := db.SQL().Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("%s: %v", stmt.sql, err)
		}
	}

	after := findCountingStages(t, tools, ToolFindNodesRequest{Names: append(names, "Right-Hand Limit")})
	if after.index != 1 {
		t.Fatalf("after: index read ran %d times", after.index)
	}
	for name, want := range map[string]string{
		"Adam Optimizer":   "exact entity:",
		"Blocked Write":    "exact " + ids["Infinite Limit"],
		"Infinite Limit":   " ",
		"Left-Hand Limit":  " ",
		"Right-Hand Limit": "exact " + ids["Left-Hand Limit"],
		"Two-Phase Commit": "exact renamed:2pc",
		"Storage":          " ",
	} {
		if got := after.answers[name]; !strings.HasPrefix(got, want) {
			t.Errorf("%q: %q, want %q", name, got, want)
		}
	}
	// The deleted node took its row and its trigrams with it.
	var held, indexed, nodes int
	if err := db.SQL().QueryRow(`SELECT (SELECT count(*) FROM graph_node_folds), (SELECT count(*) FROM graph_node_folds_fts), (SELECT count(*) FROM graph_nodes)`).Scan(&held, &indexed, &nodes); err != nil || held != nodes || indexed != nodes {
		t.Fatalf("the index holds %d rows and %d folds for %d nodes (err %v)", held, indexed, nodes, err)
	}
}

// A store written in bulk is folded a batch at a time, every batch.
func TestTheNameIndexFoldsABulkWriteInBatches(t *testing.T) {
	db, tools, ctx := findNodesTestDB(t)
	entities := make([]ToolEntityInput, 0, nodeFoldsBatch+10)
	for i := range nodeFoldsBatch + 10 {
		entities = append(entities, ToolEntityInput{Name: fmt.Sprintf("Bulk Concept %05d", i), Type: "Concept"})
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: entities}); err != nil {
		t.Fatal(err)
	}
	if got := findCountingStages(t, tools, ToolFindNodesRequest{Names: []string{"Bulk Concept 01009"}}).answers["Bulk Concept 01009"]; !strings.HasPrefix(got, "exact ") {
		t.Fatalf("the last of the bulk: %q", got)
	}
	var pending int
	if err := db.SQL().QueryRow(`SELECT count(*) FROM graph_node_folds WHERE pending = 1`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("%d nodes left pending (err %v)", pending, err)
	}
}

// A store whose index cannot be built, or read, fails the call with the
// error, rather than answering from part of the nodes.
func TestFindNodesFailsWhenTheNameIndexFails(t *testing.T) {
	t.Run("build", func(t *testing.T) {
		db, tools, ctx := findNodesTestDB(t)
		if _, err := db.SQL().Exec(`CREATE TABLE graph_node_folds (node_id TEXT)`); err != nil {
			t.Fatal(err)
		}
		if _, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"Limit"}}); err == nil || !strings.Contains(err.Error(), "find_nodes name index") {
			t.Fatalf("err %v, want the build's", err)
		}
	})
	t.Run("fold", func(t *testing.T) {
		db, tools, ctx := findNodesTestDB(t)
		if _, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"Limit"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.SQL().Exec(`UPDATE graph_node_folds SET pending = 1; DROP TABLE graph_node_folds_fts`); err != nil {
			t.Fatal(err)
		}
		if _, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"Limit"}}); err == nil || !strings.Contains(err.Error(), "fold written nodes") {
			t.Fatalf("err %v, want the fold's", err)
		}
	})
	// Built, then broken: the read of the pending nodes, then the mark that
	// they are folded.
	for name, break_ := range map[string]string{
		"pending read": `UPDATE graph_node_folds SET pending = 1; ALTER TABLE graph_node_folds RENAME COLUMN pending TO pending_hidden`,
		"folded mark":  `UPDATE graph_node_folds SET pending = 1; ALTER TABLE graph_node_folds DROP COLUMN size`,
	} {
		t.Run(name, func(t *testing.T) {
			db, tools, ctx := findNodesTestDB(t)
			if _, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"Limit"}}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.SQL().Exec(break_); err != nil {
				t.Fatal(err)
			}
			if _, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"Limit"}}); err == nil || !strings.Contains(err.Error(), "fold written nodes") {
				t.Fatalf("err %v, want the fold's", err)
			}
		})
	}
	// The text of the candidates cannot be read: those as long as the name
	// ("Limit"), read with the index, or only longer ones ("Infinite"), read
	// a window at a time.
	for _, name := range []string{"Limit", "Infinite"} {
		t.Run("text of "+name, func(t *testing.T) {
			db, tools, ctx := findNodesTestDB(t)
			if _, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{name}}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.SQL().Exec(`ALTER TABLE graph_nodes RENAME COLUMN node_type TO node_type_hidden`); err != nil {
				t.Fatal(err)
			}
			if _, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{name}}); err == nil || !strings.Contains(err.Error(), "node_type") {
				t.Fatalf("err %v, want the read's", err)
			}
		})
	}
	// The scan, for a short name, cannot read the nodes either.
	t.Run("scan", func(t *testing.T) {
		db, tools, ctx := findNodesTestDB(t)
		if _, err := db.SQL().Exec(`ALTER TABLE graph_nodes RENAME COLUMN node_type TO node_type_hidden`); err != nil {
			t.Fatal(err)
		}
		if _, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"xy"}}); err == nil || !strings.Contains(err.Error(), "node_type") {
			t.Fatalf("err %v, want the scan's", err)
		}
	})
	// A length stored as something other than a number cannot be read.
	t.Run("lengths", func(t *testing.T) {
		db, tools, ctx := findNodesTestDB(t)
		if _, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"Limit"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.SQL().Exec(`UPDATE graph_node_folds SET folded_len = 'five'`); err != nil {
			t.Fatal(err)
		}
		if _, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"Limit"}}); err == nil {
			t.Fatal("want the read's error")
		}
	})
	// A node the scan cannot read: an id left NULL, which a TEXT primary key
	// allows once the triggers that refuse it are gone.
	t.Run("scan row", func(t *testing.T) {
		db, tools, ctx := findNodesTestDB(t)
		rows, err := db.SQL().Query(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND tbl_name = 'graph_nodes'`)
		if err != nil {
			t.Fatal(err)
		}
		var triggers []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			triggers = append(triggers, name)
		}
		_ = rows.Close()
		for _, name := range triggers {
			if _, err := db.SQL().Exec(`DROP TRIGGER "` + name + `"`); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.SQL().Exec(`INSERT INTO graph_nodes (id, vector, content, node_type) VALUES (NULL, x'', 'xy', 'Concept')`); err != nil {
			t.Fatal(err)
		}
		if _, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"xy"}}); err == nil {
			t.Fatal("want the scan's error")
		}
	})
	t.Run("transaction", func(t *testing.T) {
		db, _, _ := findNodesTestDB(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := db.inTx(ctx, func(*sql.Tx) error { return nil }); err == nil {
			t.Fatal("a cancelled context opens no transaction")
		}
	})
	t.Run("read", func(t *testing.T) {
		db, tools, ctx := findNodesTestDB(t)
		if _, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"Limit"}}); err != nil {
			t.Fatal(err)
		}
		// Nothing is pending, so only the read reaches the index, which
		// can no longer answer MATCH.
		if _, err := db.SQL().Exec(`DROP TABLE graph_node_folds_fts; CREATE TABLE graph_node_folds_fts (folded TEXT)`); err != nil {
			t.Fatal(err)
		}
		if _, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"Limit"}}); err == nil || strings.Contains(err.Error(), "find_nodes name index") {
			t.Fatalf("err %v, want the read's", err)
		}
	})
}

// A pending node may already be in the index: another process folded it
// between this one's read of the pending nodes and its write. Folding it
// again replaces its trigrams.
func TestTheNameIndexRefoldsANodeAlreadyIndexed(t *testing.T) {
	db, tools, _ := findNodesTestDB(t)
	first := findCountingStages(t, tools, ToolFindNodesRequest{Names: []string{"Limit", "Two-Phase Commit"}})
	if _, err := db.SQL().Exec(`UPDATE graph_node_folds SET pending = 1`); err != nil {
		t.Fatal(err)
	}
	again := findCountingStages(t, tools, ToolFindNodesRequest{Names: []string{"Limit", "Two-Phase Commit"}})
	for name, want := range first.answers {
		if again.answers[name] != want {
			t.Errorf("%q: %q, then %q", name, want, again.answers[name])
		}
	}
}

// The index is built, and the nodes written before it marked, once per open
// store: the backfill reads every node id, which no later call should pay.
func TestTheNameIndexIsBuiltOncePerOpen(t *testing.T) {
	db, tools, _ := findNodesTestDB(t)
	findCountingStages(t, tools, ToolFindNodesRequest{Names: []string{"Limit"}})
	// Taken out by hand: only a second backfill would put it back.
	if _, err := db.SQL().Exec(`DELETE FROM graph_node_folds WHERE rid = (SELECT min(rid) FROM graph_node_folds)`); err != nil {
		t.Fatal(err)
	}
	findCountingStages(t, tools, ToolFindNodesRequest{Names: []string{"Limit"}})
	var held, nodes int
	if err := db.SQL().QueryRow(`SELECT (SELECT count(*) FROM graph_node_folds), (SELECT count(*) FROM graph_nodes)`).Scan(&held, &nodes); err != nil || held != nodes-1 {
		t.Fatalf("held %d of %d nodes (err %v), want the backfill not run again", held, nodes, err)
	}
}

// On many matches, the index reads only the text of the candidates it serves,
// in order, and answers as the scan answers: the shortest first, past many
// windows, under every limit, a type filter, and an alias
// whose entity also contains the name, or folds to it.
func TestTheNameIndexAnswersManyMatchesAsTheScan(t *testing.T) {
	db, tools, ctx := aliasBrain(t)
	// Acme Corp carries "Acme", which its name contains, and "acme-corp",
	// which its name folds to, besides "ACME Ltd".
	node, err := db.Graph().GetNode(ctx, EntityNodeID("Acme Corp"))
	if err != nil {
		t.Fatal(err)
	}
	node.Properties["aliases"] = []string{"ACME Ltd", "Acme", "acme-corp"}
	if err := db.Graph().UpsertNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	// Two nodes spelling it so that it folds to "acmecorp", the longer
	// written first: the folded matches are served shortest first, whatever
	// the order written.
	for _, s := range [][2]string{{"spelling:long", "ACME -- CORP!!"}, {"spelling:short", "acme corp."}} {
		if err := db.Graph().UpsertNode(ctx, &graph.GraphNode{ID: s[0], Content: s[1], NodeType: "Concept", Vector: node.Vector}); err != nil {
			t.Fatal(err)
		}
	}
	entities := make([]ToolEntityInput, 0, 128)
	for i := range 128 {
		kind := "Concept"
		if i%3 == 0 {
			kind = "Tool"
		}
		entities = append(entities, ToolEntityInput{Name: "Acme" + strings.Repeat(" x", i%17) + fmt.Sprintf(" part %03d", i), Type: kind})
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: entities}); err != nil {
		t.Fatal(err)
	}
	for _, req := range []ToolFindNodesRequest{
		{Names: []string{"Acme", "acme ltd", "acme-corp", "Widget"}, Limit: 1},
		{Names: []string{"Acme", "acme ltd", "part 01", "Acme Corp"}, Limit: 7},
		{Names: []string{"Acme", "acme ltd", "acme-corp"}, Limit: 200},
		{Names: []string{"Acme", "acme ltd"}, Limit: 20, NodeTypes: []string{"Tool"}},
	} {
		indexed := findCountingStages(t, tools, req)
		scanReq := req
		scanReq.Names = append([]string{"极限"}, req.Names...)
		scanned := findCountingStages(t, tools, scanReq)
		if indexed.index != 1 || scanned.scan != 1 {
			t.Fatalf("%v: index ran %d times, scan %d", req.Names, indexed.index, scanned.scan)
		}
		for _, name := range req.Names {
			if indexed.answers[name] != scanned.answers[name] {
				t.Errorf("%q limit %d types %v:\n index %q\n scan  %q", name, req.Limit, req.NodeTypes, indexed.answers[name], scanned.answers[name])
			}
		}
	}
}

// A node folded again leaves no trigram of its old text, and a write that
// changes neither its id nor its text does not fold it again: the index
// keeps one fold per node however often nodes are written.
func TestTheNameIndexReplacesAFoldAndSkipsAnUnchangedWrite(t *testing.T) {
	db, tools, ctx := findNodesTestDB(t)
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{{Name: "Quokka Alpha", Type: "Animal"}}}); err != nil {
		t.Fatal(err)
	}
	findCountingStages(t, tools, ToolFindNodesRequest{Names: []string{"Quokka Alpha"}})
	if _, err := db.SQL().Exec(`UPDATE graph_nodes SET content = 'Other One' WHERE id = ?`, EntityNodeID("Quokka Alpha")); err != nil {
		t.Fatal(err)
	}
	findCountingStages(t, tools, ToolFindNodesRequest{Names: []string{"Other One"}})
	var stale int
	if err := db.SQL().QueryRow(`SELECT count(*) FROM graph_node_folds_fts WHERE graph_node_folds_fts MATCH '"quokka"'`).Scan(&stale); err != nil || stale != 0 {
		t.Fatalf("%d folds still match the old name (err %v)", stale, err)
	}

	size := func() int {
		var n int
		if err := db.SQL().QueryRow(`SELECT count(*) FROM graph_node_folds_fts_data`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := size()
	for range 5 {
		if _, err := db.SQL().Exec(`UPDATE graph_nodes SET content = content`); err != nil {
			t.Fatal(err)
		}
		var pending int
		if err := db.SQL().QueryRow(`SELECT count(*) FROM graph_node_folds WHERE pending = 1`).Scan(&pending); err != nil || pending != 0 {
			t.Fatalf("an unchanged write left %d nodes to fold (err %v)", pending, err)
		}
		findCountingStages(t, tools, ToolFindNodesRequest{Names: []string{"Other One"}})
	}
	if after := size(); after != before {
		t.Fatalf("unchanged writes grew the index from %d to %d rows", before, after)
	}
}

// Candidates are read in bounded batches, so a limit past the bind limit
// answers as the scan does.
func TestTheNameIndexReadsCandidatesInBatches(t *testing.T) {
	saved := nodeCandidatesPerRead
	nodeCandidatesPerRead = 2
	t.Cleanup(func() { nodeCandidatesPerRead = saved })
	_, tools, ctx := findNodesTestDB(t)
	entities := make([]ToolEntityInput, 0, 11)
	for i := range 11 {
		entities = append(entities, ToolEntityInput{Name: fmt.Sprintf("Wombat part %02d", i), Type: "Concept"})
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: entities}); err != nil {
		t.Fatal(err)
	}
	req := ToolFindNodesRequest{Names: []string{"Wombat"}, Limit: 50}
	indexed := findCountingStages(t, tools, req)
	scanned := findCountingStages(t, tools, ToolFindNodesRequest{Names: []string{"极限", "Wombat"}, Limit: 50})
	if indexed.index != 1 || scanned.scan != 1 || indexed.answers["Wombat"] != scanned.answers["Wombat"] || strings.Count(indexed.answers["Wombat"], "entity:") != 11 {
		t.Fatalf("index %q\nscan  %q", indexed.answers["Wombat"], scanned.answers["Wombat"])
	}
}
