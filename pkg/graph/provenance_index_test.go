package graph

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// sourcedFrom answers "which nodes list doc among their sources" twice: through
// NodeSourcedFrom, and through the JSON predicate it stands in for. The two
// must agree on every row, whatever the row holds.
func sourcedFrom(t *testing.T, g *GraphStore, doc string) (got, oracle []string) {
	t.Helper()
	ctx := context.Background()
	read := func(cond string) []string {
		rows, err := g.query(ctx, `SELECT id FROM graph_nodes WHERE `+cond+` ORDER BY id`, doc)
		if err != nil {
			t.Fatalf("query %q: %v", cond, err)
		}
		defer func() { _ = rows.Close() }()
		out := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			out = append(out, id)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	return read(NodeSourcedFrom(g.dialect)),
		read(g.dialect.JSONArrayContains("graph_nodes.properties", "source_document_ids"))
}

func checkSourcedFrom(t *testing.T, g *GraphStore, step string, want map[string][]string) {
	t.Helper()
	if g.dialect.Kind() != sqldialect.Postgres {
		// The table itself, not only the answers: no row for a node that is
		// gone, which a later node of the same id would inherit.
		var stale int
		if err := g.queryRow(context.Background(), `SELECT COUNT(*) FROM graph_node_sources WHERE node_id NOT IN (SELECT id FROM graph_nodes)`).Scan(&stale); err != nil || stale != 0 {
			t.Errorf("%s: %d source rows for nodes that are gone (%v)", step, stale, err)
		}
	}
	for doc, ids := range want {
		got, oracle := sourcedFrom(t, g, doc)
		sort.Strings(ids)
		if !reflect.DeepEqual(got, ids) || !reflect.DeepEqual(oracle, ids) {
			t.Errorf("%s: sourced from %q = %v, JSON predicate = %v, want %v", step, doc, got, oracle, ids)
		}
	}
}

// Every way a node's sources can be written, changed and removed, read back
// through the index and through the JSON it indexes.
func TestNodeSourcedFromFollowsEveryWriteOfTheSources(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			if err := g.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			for _, n := range []*GraphNode{
				{ID: "src:two", Properties: map[string]any{"source_document_ids": []string{"a", "b"}}},
				{ID: "src:b", Properties: map[string]any{"source_document_ids": []string{"b"}}},
				// Not an array: the JSON predicate matches nothing, so neither may the index.
				{ID: "src:string", Properties: map[string]any{"source_document_ids": "a"}},
				{ID: "src:bare", Content: "no properties"},
				// A number is not the document "1".
				{ID: "src:mixed", Properties: map[string]any{"source_document_ids": []any{1, "a"}}},
				{ID: "src:twice", Properties: map[string]any{"source_document_ids": []string{"a", "a"}}},
				{ID: "src:other", Properties: map[string]any{"chunk_ids": []string{"a"}}},
				// A list that is an object: the predicate reads arrays only.
				{ID: "src:object", Properties: map[string]any{"source_document_ids": map[string]any{"x": "a"}}},
				// A member that is a list. Only a document id that is itself
				// the JSON text of a list could name it; the index leaves it.
				{ID: "src:nested", Properties: map[string]any{"source_document_ids": []any{[]string{"n"}}}},
			} {
				if err := g.UpsertNode(ctx, n); err != nil {
					t.Fatalf("UpsertNode %s: %v", n.ID, err)
				}
			}
			// Properties that are not JSON at all, as an older writer could
			// leave them: SQLite keeps the row and indexes nothing for it.
			// PostgreSQL's predicate fails the whole lookup on such a row, on
			// main as here, so the row is written on SQLite only.
			if g.dialect.Kind() != sqldialect.Postgres {
				if _, err := g.exec(ctx, `UPDATE graph_nodes SET properties = 'not json' WHERE id = 'src:other'`); err != nil {
					t.Fatal(err)
				}
				if got, _ := sourcedFrom(t, g, `["n"]`); len(got) != 0 {
					t.Errorf("a nested list is indexed: sourced from [\"n\"] = %v", got)
				}
			}
			// A list written as a bare string: SQLite's predicate reads only
			// arrays, PostgreSQL's @> also takes "a" as containing "a". The
			// index keeps each backend's answer; it does not settle the
			// difference.
			bare := []string{}
			if g.dialect.Kind() == sqldialect.Postgres {
				bare = []string{"src:string"}
			}
			checkSourcedFrom(t, g, "written", map[string][]string{
				"a": append([]string{"src:two", "src:mixed", "src:twice"}, bare...),
				"b": {"src:two", "src:b"},
				"1": {},
				"c": {},
			})

			// Rewritten whole, by an upsert.
			if err := g.UpsertNode(ctx, &GraphNode{ID: "src:two", Properties: map[string]any{"source_document_ids": []string{"c"}}}); err != nil {
				t.Fatal(err)
			}
			// One field set in place, the way a document is detached from an entity.
			if _, err := g.exec(ctx, `UPDATE graph_nodes SET properties = `+g.dialect.JSONSet("properties", "source_document_ids")+
				` WHERE id = ?`, `["b"]`, "src:twice"); err != nil {
				t.Fatalf("detach: %v", err)
			}
			// Renamed.
			if _, err := g.exec(ctx, `UPDATE graph_nodes SET id = 'src:renamed' WHERE id = 'src:mixed'`); err != nil {
				t.Fatal(err)
			}
			if err := g.DeleteNode(ctx, "src:b"); err != nil {
				t.Fatal(err)
			}
			checkSourcedFrom(t, g, "changed", map[string][]string{
				"a": append([]string{"src:renamed"}, bare...),
				"b": {"src:twice"},
				"c": {"src:two"},
			})
			// Deleted, then written again under the same id with other
			// sources: nothing of the first node may come back with it.
			if err := g.UpsertNode(ctx, &GraphNode{ID: "src:b", Properties: map[string]any{"source_document_ids": []string{"c"}}}); err != nil {
				t.Fatal(err)
			}
			checkSourcedFrom(t, g, "written again", map[string][]string{
				"b": {"src:twice"},
				"c": {"src:b", "src:two"},
			})
			// An update that names a document twice, on a node that is
			// there: main takes it, and so does every writer once a brain
			// has the triggers, an older binary included.
			if err := g.UpsertNode(ctx, &GraphNode{ID: "src:two", Properties: map[string]any{"source_document_ids": []string{"c", "c"}}}); err != nil {
				t.Fatalf("update with a repeated source: %v", err)
			}
			if err := g.UpsertNode(ctx, &GraphNode{ID: "src:two", Properties: map[string]any{"source_document_ids": []string{"d", "c", "d", "d"}}}); err != nil {
				t.Fatalf("update with a repeated new source: %v", err)
			}
			// A member leaves and one comes in the same write.
			if err := g.UpsertNode(ctx, &GraphNode{ID: "src:b", Properties: map[string]any{"source_document_ids": []string{"e", "e"}}}); err != nil {
				t.Fatal(err)
			}
			checkSourcedFrom(t, g, "repeated", map[string][]string{
				"b": {"src:twice"},
				"c": {"src:two"},
				"d": {"src:two"},
				"e": {"src:b"},
			})
		})
	}
}

// A brain written before the index existed has its sources indexed the first
// time it is opened, and only then: a second open writes nothing.
func TestABrainWrittenBeforeTheSourceIndexIsIndexedWhenOpened(t *testing.T) {
	ctx := context.Background()
	store, g, cleanup := setupTestGraph(t)
	t.Cleanup(cleanup)
	for _, n := range []*GraphNode{
		{ID: "old:1", Properties: map[string]any{"source_document_ids": []string{"a", "b"}}},
		{ID: "old:2", Properties: map[string]any{"source_document_ids": []string{"a"}}},
		// The same document twice: the fill keeps one row and opens.
		{ID: "old:4", Properties: map[string]any{"source_document_ids": []string{"c", "c"}}},
	} {
		if err := g.UpsertNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	// What a brain from before this release looks like: no table, no triggers.
	for _, stmt := range []string{
		`DROP TRIGGER graph_node_sources_ins`, `DROP TRIGGER graph_node_sources_upd`,
		`DROP TRIGGER graph_node_sources_del`, `DROP TABLE graph_node_sources`,
	} {
		if _, err := g.exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if _, err := g.exec(ctx, `INSERT INTO graph_nodes (id, vector, properties) VALUES ('old:3', x'', '{"source_document_ids":["b"]}')`); err != nil {
		t.Fatal(err)
	}

	reopened := NewGraphStore(store)
	if err := reopened.InitGraphSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}
	checkSourcedFrom(t, reopened, "reopened", map[string][]string{
		"a": {"old:1", "old:2"},
		"b": {"old:1", "old:3"},
		"c": {"old:4"},
	})

	// The table is built once. A row the index lost on purpose stays lost
	// across a second open, which proves the second open did not rebuild.
	if _, err := g.exec(ctx, `DELETE FROM graph_node_sources WHERE node_id = 'old:3'`); err != nil {
		t.Fatal(err)
	}
	again := NewGraphStore(store)
	if err := again.InitGraphSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := sourcedFrom(t, again, "b"); !reflect.DeepEqual(got, []string{"old:1"}) {
		t.Errorf("second open rebuilt the source index: sourced from b = %v", got)
	}
}

// A brain whose index table exists but lost one trigger gets it back at the
// next open, and a table that is right again: while the trigger was gone the
// writes it would have caught went past the table.
func TestTheSourceTriggersComeBackWhenOneIsMissing(t *testing.T) {
	ctx := context.Background()
	store, g, cleanup := setupTestGraph(t)
	t.Cleanup(cleanup)
	if err := g.UpsertNode(ctx, &GraphNode{ID: "kept", Properties: map[string]any{"source_document_ids": []string{"a"}}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		trigger, missed string
		want            map[string][]string
	}{
		{"graph_node_sources_ins", `INSERT INTO graph_nodes (id, vector, properties) VALUES ('added', x'', '{"source_document_ids":["a"]}')`,
			map[string][]string{"a": {"added", "kept"}}},
		{"graph_node_sources_upd", `UPDATE graph_nodes SET properties = '{"source_document_ids":["b"]}' WHERE id = 'added'`,
			map[string][]string{"a": {"kept"}, "b": {"added"}}},
		{"graph_node_sources_del", `DELETE FROM graph_nodes WHERE id = 'added'`,
			map[string][]string{"b": {}}},
	} {
		if _, err := g.exec(ctx, `DROP TRIGGER `+c.trigger); err != nil {
			t.Fatal(err)
		}
		if _, err := g.exec(ctx, c.missed); err != nil {
			t.Fatalf("%s: %v", c.missed, err)
		}
		reopened := NewGraphStore(store)
		if err := reopened.InitGraphSchema(ctx); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := g.queryRow(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?`, c.trigger).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s after reopen: %d (%v), want 1", c.trigger, n, err)
		}
		checkSourcedFrom(t, reopened, "reopened without "+c.trigger, c.want)
		// The table itself, not only the answer: no row left for a node
		// that is gone, which a later node of the same id would inherit.
		var stale int
		if err := g.queryRow(ctx, `SELECT COUNT(*) FROM graph_node_sources WHERE node_id NOT IN (SELECT id FROM graph_nodes)`).Scan(&stale); err != nil || stale != 0 {
			t.Errorf("reopened without %s: %d rows for nodes that are gone (%v)", c.trigger, stale, err)
		}
	}
}

// The node_id index of the table comes back too: without it every update and
// delete of a node reads the whole table inside its trigger.
func TestTheSourceIndexComesBackWithItsNodeIndex(t *testing.T) {
	ctx := context.Background()
	store, g, cleanup := setupTestGraph(t)
	t.Cleanup(cleanup)
	if _, err := g.exec(ctx, `DROP INDEX idx_graph_node_sources_node`); err != nil {
		t.Fatal(err)
	}
	if err := NewGraphStore(store).InitGraphSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if plan := explain(t, g, `DELETE FROM graph_node_sources WHERE node_id = ?`, "x"); !strings.Contains(plan, "idx_graph_node_sources_node") {
		t.Errorf("the trigger's delete has no index after reopen:\n%s", plan)
	}
}

// PostgreSQL takes and opens what it took on main: a string holding a NUL
// (valid JSON, which jsonb refuses) on a node and an edge, and a row whose
// properties are not JSON at all.
func TestTheProvenanceIndexesRefuseNoRowMainTakes(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			if err := g.InitGraphSchema(ctx); err != nil {
				t.Fatal(err)
			}
			for _, n := range []*GraphNode{
				{ID: "nul:1", Properties: map[string]any{"note": "a\x00b"}},
				{ID: "nul:2", Properties: map[string]any{"document_id": "d"}},
			} {
				if err := g.UpsertNode(ctx, n); err != nil {
					t.Fatalf("node %s: %v", n.ID, err)
				}
			}
			if err := g.UpsertEdge(ctx, &GraphEdge{ID: "nul:e", FromNodeID: "nul:1", ToNodeID: "nul:2", EdgeType: "x", Weight: 1,
				Properties: map[string]any{"note": "a\x00b"}}); err != nil {
				t.Fatalf("edge: %v", err)
			}
			if _, err := g.exec(ctx, `UPDATE graph_nodes SET properties = 'not json' WHERE id = 'nul:2'`); err != nil {
				t.Fatalf("non-JSON properties: %v", err)
			}
			// Opened again: the schema step runs over those rows.
			g.schemaMu.Lock()
			g.schemaReady = false
			g.schemaMu.Unlock()
			if err := g.InitGraphSchema(ctx); err != nil {
				t.Errorf("reopen over those rows: %v", err)
			}
		})
	}
}

// The schema step fails loudly when it cannot build what it needs.
func TestTheSourceIndexReportsWhatStopsIt(t *testing.T) {
	ctx := context.Background()
	store, g, cleanup := setupTestGraph(t)
	t.Cleanup(cleanup)
	if _, err := g.exec(ctx, `DROP TABLE graph_node_sources`); err != nil {
		t.Fatal(err)
	}
	// A view under the table's name: CREATE TABLE IF NOT EXISTS sees the name
	// taken and does nothing, and the backfill then has nowhere to write.
	if _, err := g.exec(ctx, `CREATE VIEW graph_node_sources AS SELECT 1 AS document_id, 1 AS node_id`); err != nil {
		t.Fatal(err)
	}
	if err := NewGraphStore(store).InitGraphSchema(ctx); err == nil || !strings.Contains(err.Error(), "source index") {
		t.Errorf("schema over a broken source index: %v, want an error naming it", err)
	}
}

// An index name already taken by a table stops the schema with an error that
// names the provenance indexes.
func TestTheProvenanceIndexesReportWhatStopsThem(t *testing.T) {
	ctx := context.Background()
	store, g, cleanup := setupTestGraph(t)
	t.Cleanup(cleanup)
	for _, stmt := range []string{`DROP INDEX idx_edges_document_id`, `CREATE TABLE idx_edges_document_id (x)`} {
		if _, err := g.exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := NewGraphStore(store).InitGraphSchema(ctx); err == nil || !strings.Contains(err.Error(), "provenance index") {
		t.Errorf("schema over a taken index name: %v, want an error naming the provenance index", err)
	}
}

// A brain that cannot be read stops the source index before it writes anything.
func TestTheSourceIndexReportsAClosedBrain(t *testing.T) {
	ctx := context.Background()
	store, g, cleanup := setupTestGraph(t)
	t.Cleanup(cleanup)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := g.createNodeSources(ctx); err == nil || !strings.Contains(err.Error(), "source index") {
		t.Errorf("source index on a closed brain: %v, want an error naming it", err)
	}
}

// The lookups a document's delete makes are answered from an index.
func TestProvenanceLookupsHaveAnIndex(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	t.Cleanup(cleanup)
	docID := g.dialect.JSONTextGuarded("properties", "document_id")
	for name, q := range map[string]string{
		"edges of a document": `SELECT id FROM graph_edges WHERE ` + docID + ` = ?`,
		"nodes of a document": `SELECT id FROM graph_nodes WHERE ` + docID + ` = ?`,
		"nodes sourced from":  `SELECT id FROM graph_nodes WHERE ` + NodeSourcedFrom(g.dialect),
	} {
		if plan := explain(t, g, q, "doc"); strings.Contains(plan, "SCAN graph_") {
			t.Errorf("%s scans:\n%s", name, plan)
		}
	}
}

// The update trigger writes only when the id or the list changed: a write
// that leaves the list as it was touches no source row. An entity many
// documents name has its other properties written far more often than its
// list. A changed list is written again whole.
func TestTheSourceTriggerWritesOnlyWhatChanged(t *testing.T) {
	ctx := context.Background()
	_, g, cleanup := setupTestGraph(t)
	t.Cleanup(cleanup)
	if err := g.UpsertNode(ctx, &GraphNode{ID: "e", Content: "first", Properties: map[string]any{"source_document_ids": []string{"a", "b", "c"}, "name": "E"}}); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE source_writes (what TEXT, document_id TEXT, node_id TEXT)`,
		`CREATE TRIGGER source_writes_ins AFTER INSERT ON graph_node_sources BEGIN INSERT INTO source_writes VALUES ('+', NEW.document_id, NEW.node_id); END`,
		`CREATE TRIGGER source_writes_del AFTER DELETE ON graph_node_sources BEGIN INSERT INTO source_writes VALUES ('-', OLD.document_id, OLD.node_id); END`,
	} {
		if _, err := g.exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	writes := func() []string {
		t.Helper()
		rows, err := g.query(ctx, `SELECT what || document_id || '@' || node_id FROM source_writes ORDER BY 1`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		out := []string{}
		for rows.Next() {
			var w string
			if err := rows.Scan(&w); err != nil {
				t.Fatal(err)
			}
			out = append(out, w)
		}
		if _, err := g.exec(ctx, `DELETE FROM source_writes`); err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, c := range []struct {
		step  string
		write func() error
		want  []string
	}{
		{"the same list, other properties", func() error {
			return g.UpsertNode(ctx, &GraphNode{ID: "e", Content: "second", Properties: map[string]any{"source_document_ids": []string{"a", "b", "c"}, "name": "E2"}})
		}, []string{}},
		{"one member leaves, one comes", func() error {
			return g.UpsertNode(ctx, &GraphNode{ID: "e", Properties: map[string]any{"source_document_ids": []string{"a", "c", "d", "d"}}})
		}, []string{"+a@e", "+c@e", "+d@e", "-a@e", "-b@e", "-c@e"}},
		{"the list is no longer a list", func() error {
			_, err := g.exec(ctx, `UPDATE graph_nodes SET properties = 'not json' WHERE id = 'e'`)
			return err
		}, []string{"-a@e", "-c@e", "-d@e"}},
		{"a list again", func() error {
			_, err := g.exec(ctx, `UPDATE graph_nodes SET properties = '{"source_document_ids":["a"]}' WHERE id = 'e'`)
			return err
		}, []string{"+a@e"}},
		{"the same list in another order of keys", func() error {
			_, err := g.exec(ctx, `UPDATE graph_nodes SET properties = '{"name":"E","source_document_ids":["a"]}' WHERE id = 'e'`)
			return err
		}, []string{}},
		{"renamed", func() error {
			_, err := g.exec(ctx, `UPDATE graph_nodes SET id = 'f' WHERE id = 'e'`)
			return err
		}, []string{"+a@f", "-a@e"}},
	} {
		if err := c.write(); err != nil {
			t.Fatalf("%s: %v", c.step, err)
		}
		if got := writes(); !slices.Equal(got, c.want) {
			t.Errorf("%s: source rows written %v, want %v", c.step, got, c.want)
		}
	}
	checkSourcedFrom(t, g, "after the writes", map[string][]string{"a": {"f"}, "b": {}, "c": {}, "d": {}})
}

// A trigger whose body a release corrected is installed again: a brain keeps
// the triggers it was opened with otherwise, since their names are there.
func TestACorrectedSourceTriggerReplacesTheOneInTheFile(t *testing.T) {
	ctx := context.Background()
	store, g, cleanup := setupTestGraph(t)
	t.Cleanup(cleanup)
	if err := g.UpsertNode(ctx, &GraphNode{ID: "kept", Properties: map[string]any{"source_document_ids": []string{"a"}}}); err != nil {
		t.Fatal(err)
	}
	for name := range nodeSourcesTriggers {
		// The same name with another body: here one that indexes nothing.
		for _, stmt := range []string{`DROP TRIGGER ` + name, `CREATE TRIGGER ` + name + ` AFTER DELETE ON graph_edges BEGIN SELECT 1; END`} {
			if _, err := g.exec(ctx, stmt); err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
		// A write the stand-in misses.
		if err := g.UpsertNode(ctx, &GraphNode{ID: "missed-" + name, Properties: map[string]any{"source_document_ids": []string{"m"}}}); err != nil {
			t.Fatal(err)
		}
		reopened := NewGraphStore(store)
		if err := reopened.InitGraphSchema(ctx); err != nil {
			t.Fatal(err)
		}
		var body string
		if err := g.queryRow(ctx, `SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?`, name).Scan(&body); err != nil || body != installedTrigger(name) {
			t.Errorf("%s after reopen (%v):\n got %s\nwant %s", name, err, body, installedTrigger(name))
		}
	}
	checkSourcedFrom(t, g, "reopened", map[string][]string{"a": {"kept"}, "m": {"missed-graph_node_sources_del", "missed-graph_node_sources_ins", "missed-graph_node_sources_upd"}})
}

// A brain that has the triggers of this version is opened without a write.
func TestOpeningABrainThatHasTheSourceIndexWritesNothing(t *testing.T) {
	ctx := context.Background()
	store, g, cleanup := setupTestGraph(t)
	t.Cleanup(cleanup)
	if err := g.UpsertNode(ctx, &GraphNode{ID: "kept", Properties: map[string]any{"source_document_ids": []string{"a"}}}); err != nil {
		t.Fatal(err)
	}
	// A row the table should not have: a rebuild would remove it.
	if _, err := g.exec(ctx, `INSERT INTO graph_node_sources VALUES ('left', 'alone')`); err != nil {
		t.Fatal(err)
	}
	if err := NewGraphStore(store).InitGraphSchema(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := g.queryRow(ctx, `SELECT COUNT(*) FROM graph_node_sources WHERE document_id = 'left'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("the table was rebuilt on a brain that had its triggers (%d, %v)", n, err)
	}
}

// sourcePairs is graph_node_sources against the lists it indexes: the pairs
// only the table holds and the pairs only the lists hold.
func sourcePairs(t *testing.T, g *GraphStore) (stale, missing []string) {
	t.Helper()
	read := func(q string) map[string]bool {
		rows, err := g.db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		pairs := map[string]bool{}
		for rows.Next() {
			var doc, node string
			if err := rows.Scan(&doc, &node); err != nil {
				t.Fatal(err)
			}
			pairs[doc+" @ "+node] = true
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return pairs
	}
	table := read(`SELECT document_id, node_id FROM graph_node_sources`)
	lists := read(`SELECT je.value, n.id FROM graph_nodes n, json_each(` + sourceListOf("n") + `) je WHERE je.type = 'text'`)
	for pair := range table {
		if !lists[pair] {
			stale = append(stale, pair)
		}
	}
	for pair := range lists {
		if !table[pair] {
			missing = append(missing, pair)
		}
	}
	sort.Strings(stale)
	sort.Strings(missing)
	return stale, missing
}

// A row that takes the place of another fires no delete trigger for it. The
// sources of the row replaced must not stay under the id.
func TestARowThatReplacesAnotherLeavesNoSourceOfIt(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()
	if err := g.InitGraphSchema(ctx); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO graph_nodes (id, vector, content, node_type, properties) VALUES ('e', x'', '', 't', '{"source_document_ids":["a","b"]}')`,
		`INSERT OR REPLACE INTO graph_nodes (id, vector, content, node_type, properties) VALUES ('e', x'', '', 't', '{"source_document_ids":["c"]}')`,
		`INSERT INTO graph_nodes (id, vector, content, node_type, properties) VALUES ('f', x'', '', 't', '{"source_document_ids":["d","c"]}')`,
		`UPDATE OR REPLACE graph_nodes SET id = 'f' WHERE id = 'e'`,
		`INSERT OR REPLACE INTO graph_nodes (id, vector, content, node_type, properties) VALUES ('f', x'', '', 't', '{}')`,
	} {
		if _, err := g.exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
		if stale, missing := sourcePairs(t, g); len(stale)+len(missing) != 0 {
			t.Fatalf("after %s\nonly in the table: %v\nonly in the lists: %v", stmt, stale, missing)
		}
	}
}

// Every kind of write, drawn at random on a few ids: after each one the
// table holds the pairs the lists hold, and no write fails.
func TestTheSourceIndexEqualsTheListsAfterRandomWrites(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()
	if err := g.InitGraphSchema(ctx); err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(1))
	members := []string{`"a"`, `"b"`, `"c"`, `"A"`, `" a"`, `""`, `1`, `null`, `["a"]`, `{"a":"b"}`}
	list := func() string {
		picked := make([]string, rng.Intn(6))
		for i := range picked {
			picked[i] = members[rng.Intn(len(members))]
		}
		return "[" + strings.Join(picked, ",") + "]"
	}
	properties := func() string {
		switch rng.Intn(8) {
		case 0:
			return `{}`
		case 1:
			return `{"source_document_ids":"a"}`
		case 2:
			return `not json`
		}
		return `{"source_document_ids":` + list() + `}`
	}
	id := func() string { return fmt.Sprintf("n%d", rng.Intn(6)) }
	const upsert = `INSERT INTO graph_nodes (id, vector, content, node_type, properties) VALUES (?, x'', '', 't', ?)
		ON CONFLICT(id) DO UPDATE SET properties = excluded.properties`
	for op := 0; op < 1500; op++ {
		var what string
		var err error
		switch rng.Intn(8) {
		case 0, 1:
			what = "upsert"
			_, err = g.exec(ctx, upsert, id(), properties())
		case 2:
			what = "replace"
			_, err = g.exec(ctx, `INSERT OR REPLACE INTO graph_nodes (id, vector, content, node_type, properties) VALUES (?, x'', '', 't', ?)`, id(), properties())
		case 3:
			what = "list set in place"
			_, err = g.exec(ctx, `UPDATE graph_nodes SET properties = json_set(properties, '$.source_document_ids', json(?)) WHERE id = ? AND json_valid(properties)`, list(), id())
		case 4:
			what = "member added"
			_, err = g.exec(ctx, `UPDATE graph_nodes SET properties = json_insert(properties, '$.source_document_ids[#]', json(?)) WHERE id = ? AND json_valid(properties)`, members[rng.Intn(len(members))], id())
		case 5:
			what = "another property"
			_, err = g.exec(ctx, `UPDATE graph_nodes SET content = ?, properties = json_set(properties, '$.seen', ?) WHERE id = ? AND json_valid(properties)`, what, op, id())
		case 6:
			what = "delete"
			_, err = g.exec(ctx, `DELETE FROM graph_nodes WHERE id = ?`, id())
		case 7:
			what = "rename"
			_, err = g.exec(ctx, `UPDATE OR REPLACE graph_nodes SET id = ? WHERE id = ?`, id(), id())
		}
		if err != nil {
			t.Fatalf("operation %d (%s): %v", op, what, err)
		}
		if stale, missing := sourcePairs(t, g); len(stale)+len(missing) != 0 {
			t.Fatalf("after operation %d (%s)\nonly in the table: %v\nonly in the lists: %v", op, what, stale, missing)
		}
	}
}
