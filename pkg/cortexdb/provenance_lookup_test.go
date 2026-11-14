package cortexdb

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Every lookup that finds what one document put in the graph — the edges it
// asserted, its chunk and document nodes, the entities that list it as a
// source — is answered from one of the provenance indexes, never by reading
// the table. These are the lookups delete_document_graph and knowledge_delete
// make before they remove anything, once per document.
func TestEveryProvenanceLookupUsesAProvenanceIndex(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	upsertProseEntities(t, db, "a.md", []string{"DRBD"}, "a.md#0")

	lookups := map[string]struct {
		query string
		args  []any
	}{
		"edges of a document":           {edgesOfDocumentSQL(db), []any{"a.md"}},
		"edges of a document, in order": {edgesOfDocumentSQL(db) + ` ORDER BY id ASC`, []any{"a.md"}},
		"entities sourced from":         {entitiesSourcedFromSQL(db), []any{"a.md"}},
		"chunks of a document":          {chunksOfDocumentSQL(db), []any{"a.md"}},
		"the document's own nodes":      {documentNodesOfSQL(db), []any{"a.md", graphDocumentNodeID("a.md")}},
	}
	for name, l := range lookups {
		rows, err := db.SQL().QueryContext(ctx, "EXPLAIN QUERY PLAN "+l.query, l.args...)
		if err != nil {
			t.Fatalf("%s: explain: %v", name, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notUsed int
			var detail string
			if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		_ = rows.Close()
		text := strings.Join(plan, "\n")
		if strings.Contains(text, "SCAN graph_") || strings.Contains(text, "idx_nodes_type") ||
			!(strings.Contains(text, "idx_edges_document_id") || strings.Contains(text, "idx_nodes_document_id") ||
				strings.Contains(text, "graph_node_sources")) {
			t.Errorf("%s is not served by a provenance index:\n%s", name, text)
		}
	}
}

// The document-node lookup's rewrite (no index on node_type, so the planner
// takes the provenance index) still finds the node by either key and only a
// node of type document.
func TestTheDocumentNodeIsStillFoundByEitherKey(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	for _, stmt := range []struct {
		id, typ, props string
	}{
		{"doc:by-property", "document", `{"document_id":"p.md"}`},
		{graphDocumentNodeID("legacy.md"), "document", ``},
		{"chunk:p", "chunk", `{"document_id":"p.md"}`},
		{"untyped", "", `{"document_id":"p.md"}`},
	} {
		if _, err := db.SQL().ExecContext(ctx,
			`INSERT INTO graph_nodes (id, vector, node_type, properties) VALUES (?, x'', NULLIF(?, ''), ?)`,
			stmt.id, stmt.typ, stmt.props); err != nil {
			t.Fatal(err)
		}
	}
	for doc, want := range map[string]string{"p.md": "doc:by-property", "legacy.md": graphDocumentNodeID("legacy.md")} {
		ids, err := collectIDs(ctx, db, documentNodesOfSQL(db), doc, graphDocumentNodeID(doc))
		if err != nil || len(ids) != 1 || ids[0] != want {
			t.Errorf("document nodes of %s = %v (%v), want [%s]", doc, ids, err, want)
		}
	}
}

// explainPlan is SQLite's plan for one statement, its detail lines joined.
func explainPlan(t *testing.T, db *DB, query string, args []any) string {
	t.Helper()
	rows, err := db.SQL().QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain %s: %v", query, err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	return strings.Join(plan, "\n")
}

// The statements a real delete_document_graph and knowledge_delete run, not
// the helpers alone: every one that finds a document's edges, nodes or
// entities goes through a provenance index. A delete path that went back to
// its own SQL would scan graph_nodes or graph_edges here.
func TestEveryDeleteFindsTheDocumentThroughTheProvenanceIndexes(t *testing.T) {
	ctx := context.Background()
	db := openOntologyTestDB(t)
	for _, doc := range []string{"a.md", "b.md"} {
		if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{DocumentID: doc, Entities: []ToolEntityInput{
			{Name: "DRBD", Type: "entity", ChunkIDs: []string{doc + "#0"}},
			{Name: "Pacemaker", Type: "entity", ChunkIDs: []string{doc + "#0"}},
		}}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.GraphRAGTools().UpsertRelations(ctx, ToolUpsertRelationsRequest{DocumentID: doc, Relations: []ToolRelationInput{
			{From: "DRBD", To: "Pacemaker", Type: "related_to", ChunkIDs: []string{doc + "#0"}},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k1", Title: "k1", Content: "DRBD replicates block devices.",
		Entities: []ToolEntityInput{{Name: "DRBD", Type: "software"}}}); err != nil {
		t.Fatal(err)
	}

	type statement struct {
		query string
		args  []any
	}
	var seen []statement
	db.observeTxQuery = func(q string, args []any) { seen = append(seen, statement{q, args}) }
	if _, err := db.GraphRAGTools().DeleteDocumentGraph(ctx, ToolDeleteDocumentGraphRequest{DocumentID: "a.md"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "k1"}); err != nil {
		t.Fatal(err)
	}
	db.observeTxQuery = nil

	var provenance int
	for _, st := range seen {
		if !strings.Contains(st.query, "document_id") && !strings.Contains(st.query, "node_type") {
			continue
		}
		provenance++
		plan := explainPlan(t, db, st.query, st.args)
		if strings.Contains(plan, "SCAN graph_") || strings.Contains(plan, "idx_nodes_type") {
			t.Errorf("a delete reads a document's graph without a provenance index:\n%s\n%s", st.query, plan)
		}
	}
	// Edges, entities, chunks and the document node for delete_document_graph,
	// and at least the entities for knowledge_delete.
	if provenance < 5 {
		t.Errorf("saw %d provenance lookups in the two deletes, want at least 5", provenance)
	}
}

// The index finds the nodes; each node's own JSON decides. A row the source
// index holds for a node that no longer lists the document, whatever left it
// there, detaches and deletes nothing, on either delete path.
func TestTheDeletesTrustTheNodeOverTheSourceIndex(t *testing.T) {
	ctx := context.Background()
	for name, del := range map[string]func(db *DB) error{
		"delete_document_graph": func(db *DB) error {
			_, err := db.GraphRAGTools().DeleteDocumentGraph(ctx, ToolDeleteDocumentGraphRequest{DocumentID: "gone.md"})
			return err
		},
		"knowledge_delete": func(db *DB) error {
			_, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "gone.md"})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			db := openOntologyTestDB(t)
			upsertProseEntities(t, db, "kept.md", []string{"DRBD"}, "kept.md#0")
			var id, before string
			if err := db.SQL().QueryRowContext(ctx, `SELECT id, properties FROM graph_nodes WHERE node_type = 'entity'`).Scan(&id, &before); err != nil {
				t.Fatal(err)
			}
			// The document being deleted, with an entity of its own.
			if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "gone.md", Title: "gone", Content: "Corosync carries the cluster messages.",
				Entities: []ToolEntityInput{{Name: "Corosync", Type: "software"}}}); err != nil {
				t.Fatal(err)
			}
			// And a node that lists no source at all: read as "gone.md was its
			// last source", it would be deleted.
			if _, err := db.SQL().ExecContext(ctx, `INSERT INTO graph_nodes (id, vector, node_type, properties) VALUES ('unsourced', x'', 'entity', '{}')`); err != nil {
				t.Fatal(err)
			}
			for _, node := range []string{id, "unsourced"} {
				if _, err := db.SQL().ExecContext(ctx, `INSERT INTO graph_node_sources (document_id, node_id) VALUES ('gone.md', ?)`, node); err != nil {
					t.Fatal(err)
				}
			}
			// Any rewrite of those two nodes, even one that puts back the same
			// text, is a detach the rule forbids: a trigger counts them.
			for _, stmt := range []string{
				`CREATE TABLE rewrites (id TEXT)`,
				`CREATE TRIGGER count_rewrites AFTER UPDATE ON graph_nodes WHEN OLD.id IN ('unsourced', '` + id + `') BEGIN INSERT INTO rewrites VALUES (OLD.id); END`,
			} {
				if _, err := db.SQL().ExecContext(ctx, stmt); err != nil {
					t.Fatal(err)
				}
			}
			if err := del(db); err != nil {
				t.Fatal(err)
			}
			var after string
			if err := db.SQL().QueryRowContext(ctx, `SELECT properties FROM graph_nodes WHERE id = ?`, id).Scan(&after); err != nil {
				t.Fatalf("%s deleted %s, which never listed gone.md: %v", name, id, err)
			}
			if after != before {
				t.Errorf("%s rewrote %s: %s, was %s", name, id, after, before)
			}
			var n int
			if err := db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_nodes WHERE id = 'unsourced'`).Scan(&n); err != nil || n != 1 {
				t.Errorf("%s deleted a node that never listed gone.md (%d, %v)", name, n, err)
			}
			if err := db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM rewrites`).Scan(&n); err != nil || n != 0 {
				t.Errorf("%s rewrote %d node(s) that never listed gone.md (%v)", name, n, err)
			}
		})
	}
}

// sourcesWithout keeps the order of the sources and tells whether the
// document was among them.
func TestSourcesWithout(t *testing.T) {
	for _, c := range []struct {
		props, doc string
		remaining  []string
		listed     bool
	}{
		{`{"source_document_ids":["a","b"]}`, "a", []string{"b"}, true},
		{`{"source_document_ids":["a"]}`, "a", []string{}, true},
		{`{"source_document_ids":["b"]}`, "a", []string{"b"}, false},
		{`{}`, "a", []string{}, false},
		// A bare string, which PostgreSQL's @> matches.
		{`{"source_document_ids":"a"}`, "a", []string{}, true},
		{`{"source_document_ids":"b"}`, "a", []string{}, false},
	} {
		remaining, listed, err := sourcesWithout(c.props, c.doc)
		sort.Strings(remaining)
		if err != nil || listed != c.listed || strings.Join(remaining, ",") != strings.Join(c.remaining, ",") {
			t.Errorf("sourcesWithout(%s, %s) = %v, %v, %v", c.props, c.doc, remaining, listed, err)
		}
	}
	if _, _, err := sourcesWithout("not json", "a"); err == nil {
		t.Error("sourcesWithout of text that is not JSON: no error")
	}
}

// On PostgreSQL, whose @> matches a list written as a bare string, both
// deletes still remove a node whose only source is the deleted document, as
// on main.
func TestTheDeletesStillRemoveABareStringSourceOnPostgres(t *testing.T) {
	ctx := context.Background()
	for name, del := range map[string]func(db *DB) (int, error){
		"delete_document_graph": func(db *DB) (int, error) {
			r, err := db.GraphRAGTools().DeleteDocumentGraph(ctx, ToolDeleteDocumentGraphRequest{DocumentID: "gone.md"})
			if err != nil {
				return 0, err
			}
			return r.EntityNodesDeleted, nil
		},
		"knowledge_delete": func(db *DB) (int, error) {
			_, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "gone.md"})
			return -1, err
		},
	} {
		t.Run(name, func(t *testing.T) {
			db := openPostgresBrain(t, 4)
			if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "gone.md", Title: "gone", Content: "Corosync carries the cluster messages."}); err != nil {
				t.Fatal(err)
			}
			if err := db.graph.UpsertNode(ctx, &graph.GraphNode{ID: "bare", NodeType: "entity",
				Properties: map[string]any{"source_document_ids": "gone.md"}}); err != nil {
				t.Fatal(err)
			}
			deleted, err := del(db)
			if err != nil {
				t.Fatal(err)
			}
			var n int
			if err := db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_nodes WHERE id = 'bare'`).Scan(&n); err != nil || n != 0 {
				t.Errorf("%s kept the node whose only source was gone.md (%d, %v)", name, n, err)
			}
			if deleted >= 0 && deleted < 1 {
				t.Errorf("%s counted %d entities deleted, want the bare-string node among them", name, deleted)
			}
		})
	}
}

// A source list Go cannot read stops delete_document_graph before it changes
// anything, as it stops knowledge_delete: the entity is found through the
// index, and its JSON is valid for SQLite but holds a number past float64.
func TestDeleteDocumentGraphStopsOnASourceListItCannotRead(t *testing.T) {
	ctx := context.Background()
	db := openOntologyTestDB(t)
	upsertProseEntities(t, db, "a.md", []string{"DRBD"}, "a.md#0")
	if _, err := db.SQL().ExecContext(ctx, `UPDATE graph_nodes SET properties = json_set(properties, '$.weight', json('1e400')) WHERE node_type = 'entity'`); err != nil {
		t.Fatal(err)
	}
	_, err := db.GraphRAGTools().DeleteDocumentGraph(ctx, ToolDeleteDocumentGraphRequest{DocumentID: "a.md"})
	if err == nil || !strings.Contains(err.Error(), "decode properties of") {
		t.Fatalf("delete over an unreadable source list: %v, want it to stop and say so", err)
	}
	var n int
	if err := db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_nodes WHERE node_type = 'entity'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("the stopped delete changed the entities: %d left (%v)", n, err)
	}
}
