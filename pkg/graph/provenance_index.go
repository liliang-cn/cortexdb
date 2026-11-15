package graph

// Indexes on provenance: what a document put in the graph.
//
// Removing a document's graph (delete_document_graph, knowledge_delete) first
// asks four questions: which edges carry its document_id, which chunk and
// document nodes do, and which entities list it in source_document_ids. All
// four lived in the properties JSON with no index, so each was a read of the
// whole table. On a brain of 396,000 nodes and 1,043,819 edges that is about
// 900 MB read per document removed: 4.0 s a delete with the tables in the page
// cache, 8.7 s without, and a corpus refresh that removes a thousand documents
// spent hours on it.
//
// document_id is a plain value, so an expression index on the same guarded
// expression the lookups filter with serves it, the way IndexNodeProperty
// serves an opted-in key.
//
// source_document_ids is a list, and SQLite cannot index the members of a
// JSON array. graph_node_sources holds one row per (document, node) pair,
// written by triggers on graph_nodes so that every writer keeps it current
// without knowing it exists: upserts, batches, merges, and the in-place JSON
// edit that detaches a document from an entity. The triggers read the list
// through the same guard as the JSON predicate (an array of a valid document,
// else nothing) and keep text members only, which is what that predicate
// matches when it compares a member with a bound string; NodeSourcedFrom is
// then the same answer, read from the table, for every document id that is
// not itself the JSON text of a list or an object.
//
// All of this is SQLite only. On PostgreSQL an index on these expressions
// reads properties as jsonb, which refuses rows the table takes today (text
// that is not JSON, a \u0000 inside a string): writes would fail, and a brain
// holding one such row would no longer open. PostgreSQL keeps its predicates.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// NodeSourcedFrom is the condition, on graph_nodes, that a node lists the
// document bound to its one placeholder among its source_document_ids.
func NodeSourcedFrom(d sqldialect.Dialect) string {
	if d.Kind() == sqldialect.Postgres {
		return d.JSONArrayContains("graph_nodes.properties", "source_document_ids")
	}
	return `graph_nodes.id IN (SELECT node_id FROM graph_node_sources WHERE document_id = ?)`
}

// provenanceIndexDDL is the document_id indexes, every statement idempotent.
func provenanceIndexDDL(d sqldialect.Dialect) []string {
	docID := d.JSONTextGuarded("properties", "document_id")
	return []string{
		`CREATE INDEX IF NOT EXISTS idx_edges_document_id ON graph_edges ((` + docID + `))`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_document_id ON graph_nodes ((` + docID + `))`,
	}
}

// sourceListOf is the source list of the node named row (NEW or OLD in a
// trigger, or an alias of graph_nodes), as JSON text, and '[]' when the node
// has none. The guard is the one in the SQLite JSONArrayContains.
func sourceListOf(row string) string {
	return fmt.Sprintf(`CASE WHEN json_valid(%[1]s.properties) = 1 AND json_type(%[1]s.properties, '$.source_document_ids') = 'array' THEN json_extract(%[1]s.properties, '$.source_document_ids') ELSE '[]' END`, row)
}

// nodeSourcesOf is the (document, node) rows of the node named row; from is
// the FROM clause that gives graph_nodes its alias, when row is one. A pair
// the table holds, or one the list names twice, is passed over by the
// statement's own ON CONFLICT DO NOTHING. An OR IGNORE would not do: the
// DO UPDATE half of an upsert runs under ABORT, and the conflict rule of the
// statement that fires a trigger overrides the one written inside it.
func nodeSourcesOf(row, from string) string {
	return fmt.Sprintf(`SELECT je.value, %[1]s.id FROM %[2]sjson_each(%[3]s) je WHERE je.type = 'text' ON CONFLICT (document_id, node_id) DO NOTHING`, row, from, sourceListOf(row))
}

// nodeSourcesTriggers keeps graph_node_sources equal to the lists in
// graph_nodes. AFTER UPDATE OF id as well as properties: a renamed node keeps
// its sources under its new id. The update trigger fires only when the id or
// the list changed: an entity many documents name has its other properties
// written far more often than its list. The guard is in WHEN, read once per
// write; in the WHERE of a statement of the body it is read once per row.
//
// A row that takes the place of another (INSERT OR REPLACE, UPDATE OR
// REPLACE) fires no delete trigger for the one it replaced, so the insert
// trigger clears the id it writes and the update trigger clears both ids.
//
// None of the three builds an ephemeral table
// (TestTheFeedTriggersBuildNoEphemeralTables), which is why a changed list
// is written again whole: taking out only the members that left needs a
// NOT IN over the new list, and that is a b-tree per write.
var nodeSourcesTriggers = map[string]string{
	"graph_node_sources_ins": `CREATE TRIGGER IF NOT EXISTS graph_node_sources_ins AFTER INSERT ON graph_nodes BEGIN
		DELETE FROM graph_node_sources WHERE node_id = NEW.id;
		INSERT INTO graph_node_sources (document_id, node_id) ` + nodeSourcesOf("NEW", "") + `;
	END`,
	"graph_node_sources_upd": `CREATE TRIGGER IF NOT EXISTS graph_node_sources_upd AFTER UPDATE OF id, properties ON graph_nodes
	WHEN OLD.id IS NOT NEW.id OR ` + sourceListOf("OLD") + ` IS NOT ` + sourceListOf("NEW") + ` BEGIN
		DELETE FROM graph_node_sources WHERE node_id = OLD.id;
		DELETE FROM graph_node_sources WHERE node_id = NEW.id;
		INSERT INTO graph_node_sources (document_id, node_id) ` + nodeSourcesOf("NEW", "") + `;
	END`,
	"graph_node_sources_del": `CREATE TRIGGER IF NOT EXISTS graph_node_sources_del AFTER DELETE ON graph_nodes BEGIN
		DELETE FROM graph_node_sources WHERE node_id = OLD.id;
	END`,
}

// installedTrigger is the text sqlite_master keeps for a trigger created by
// its statement in nodeSourcesTriggers: the statement, without IF NOT EXISTS.
func installedTrigger(name string) string {
	return strings.Replace(nodeSourcesTriggers[name], "CREATE TRIGGER IF NOT EXISTS ", "CREATE TRIGGER ", 1)
}

// createProvenanceIndexes installs the indexes, on SQLite. Callers hold
// schemaMu.
func (g *GraphStore) createProvenanceIndexes(ctx context.Context) error {
	if g.dialect.Kind() == sqldialect.Postgres {
		return nil
	}
	for _, stmt := range provenanceIndexDDL(g.dialect) {
		if _, err := g.exec(ctx, stmt); err != nil {
			return fmt.Errorf("cortexdb/graph: provenance index: %w", err)
		}
	}
	return g.createNodeSources(ctx)
}

// createNodeSources builds graph_node_sources and its triggers and fills the
// table from the nodes already written. It rebuilds the table too when a
// trigger is missing or is not the one this version installs, since the table
// may have missed or miswritten what that trigger would have caught: a
// trigger lives in the file, so a body a release corrects is installed again
// here, and not kept for its name. It rebuilds as well when the node_id index
// is missing, without which every node update and delete reads the whole
// table. Read first: on a brain that has all of it, opening writes nothing.
func (g *GraphStore) createNodeSources(ctx context.Context) error {
	// One transaction: a table without its triggers, or not yet filled, would
	// answer wrongly from then on. Every step's error leaves by the one return
	// at the end.
	var table, index int
	err := g.queryRow(ctx, `SELECT
		COUNT(CASE WHEN type = 'table' THEN 1 END),
		COUNT(CASE WHEN type = 'index' THEN 1 END)
		FROM sqlite_master WHERE name IN ('graph_node_sources', 'idx_graph_node_sources_node')`,
	).Scan(&table, &index)
	current := 0
	for name := range nodeSourcesTriggers {
		var body string
		if err == nil && g.queryRow(ctx, `SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?`, name).Scan(&body) == nil && body == installedTrigger(name) {
			current++
		}
	}
	if err == nil && table == 1 && index == 1 && current == len(nodeSourcesTriggers) {
		return nil
	}
	var tx *sql.Tx
	if err == nil {
		tx, err = g.db.BeginTx(ctx, nil)
	}
	if err == nil {
		defer func() { _ = tx.Rollback() }()
		for _, stmt := range nodeSourcesBuild() {
			if _, err = tx.ExecContext(ctx, stmt); err != nil {
				break
			}
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return fmt.Errorf("cortexdb/graph: source index: %w", err)
	}
	return nil
}

// nodeSourcesBuild is the table, its index and triggers, and its fill from
// the nodes already written, every statement safe to run again.
func nodeSourcesBuild() []string {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS graph_node_sources (
			document_id TEXT NOT NULL,
			node_id TEXT NOT NULL,
			PRIMARY KEY (document_id, node_id)
		) WITHOUT ROWID`,
		`CREATE INDEX IF NOT EXISTS idx_graph_node_sources_node ON graph_node_sources (node_id)`,
	}
	for _, name := range []string{"graph_node_sources_ins", "graph_node_sources_upd", "graph_node_sources_del"} {
		// Dropped first: IF NOT EXISTS would keep a body of the same name.
		stmts = append(stmts, `DROP TRIGGER IF EXISTS `+name, nodeSourcesTriggers[name])
	}
	return append(stmts, `DELETE FROM graph_node_sources`,
		`INSERT INTO graph_node_sources (document_id, node_id) `+nodeSourcesOf("n", "graph_nodes n, "))
}
