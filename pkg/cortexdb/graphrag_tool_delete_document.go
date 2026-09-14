package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Document-scoped graph deletion.
//
// Ingest is document-shaped: a document arrives, its chunks are embedded, its
// entities and relations are extracted and written. Deletion had no such shape
// — delete_entities removes entities by name, and nothing else removes
// anything — so re-ingesting a changed corpus could only add. Stores audited
// after a few rebuild cycles carried ~20% orphaned nodes: entities whose
// documents were long gone, kept alive by nothing but the absence of a way to
// ask "what did this document put in the graph?".
//
// This is that question, inverted into a delete. It relies on the provenance
// UpsertEntities records: source_document_ids on entity nodes, document_id on
// chunk nodes and relation edges. Entities asserted by other documents too are
// detached — this document's claim is removed — not deleted; an entity is only
// deleted when its last document lets go of it.

// ToolDeleteDocumentGraphRequest removes everything a document put in the graph.
type ToolDeleteDocumentGraphRequest struct {
	DocumentID string `json:"document_id"`
	// DryRun reports what would be removed without removing it.
	DryRun bool `json:"dry_run,omitempty"`
}

// ToolDeleteDocumentGraphResponse says what went, what stayed, and why.
type ToolDeleteDocumentGraphResponse struct {
	// EntityNodesDeleted counts entities whose only source was this document.
	EntityNodesDeleted int `json:"entity_nodes_deleted"`
	// EntityNodesDetached counts entities other documents also assert: this
	// document's claim was removed, the entity stays.
	EntityNodesDetached  int  `json:"entity_nodes_detached"`
	ChunkNodesDeleted    int  `json:"chunk_nodes_deleted"`
	DocumentNodeDeleted  bool `json:"document_node_deleted"`
	RelationEdgesDeleted int  `json:"relation_edges_deleted"`
	DryRun               bool `json:"dry_run,omitempty"`

	// deletedNodeIDs is what the vector index must stop serving, carried out
	// of the transaction so the sync happens after the commit and never for a
	// row a rollback put back. Unexported: it is this call's own bookkeeping
	// and not part of the answer.
	deletedNodeIDs []string
}

// DeleteDocumentGraph removes a document's chunk and document nodes, its
// relation edges, and the entities it alone asserted. Embeddings are not
// touched: they live in the caller's collection and the caller knows which
// they are; the graph does not.
// DeleteDocumentGraph removes one document's graph, in one transaction.
//
// The transaction is the whole point and it was missing. The removal is four
// write phases — the relation edges, an UPDATE per entity that survives with
// one fewer source, the nodes, and the vector index sync — and running them as
// four independent statements meant a failure between any two left a graph half
// removed: edges gone with their endpoints still standing, or some entities
// detached and the rest still naming a document that is no longer there.
// Nothing downstream can tell that state from a real one, and running the
// delete again does not restore what the first pass took.
//
// The index sync stays outside, after the commit, because it is not a database
// write: syncing ids whose rows a rollback restored would be the one thing
// worse than not syncing at all.
func (t *GraphRAGToolbox) DeleteDocumentGraph(ctx context.Context, req ToolDeleteDocumentGraphRequest) (*ToolDeleteDocumentGraphResponse, error) {
	tx, err := t.db.SQL().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("cortexdb: begin the delete of %s: %w", strings.TrimSpace(req.DocumentID), err)
	}
	defer func() { _ = tx.Rollback() }()

	resp, err := t.deleteDocumentGraphTx(ctx, tx, req)
	if err != nil {
		return nil, err
	}
	if req.DryRun {
		return resp, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("cortexdb: commit the delete of %s: %w", strings.TrimSpace(req.DocumentID), err)
	}
	if len(resp.deletedNodeIDs) > 0 {
		t.db.graph.SyncDeletedNodeIDs(ctx, resp.deletedNodeIDs)
	}
	return resp, nil
}

// deleteDocumentGraphTx is the work, scoped to a transaction the caller owns.
//
// Exported only to the package's own tests, which prove the guarantee by doing
// the work and rolling back: if the graph is unchanged afterwards, every write
// was inside the transaction.
func (t *GraphRAGToolbox) deleteDocumentGraphTx(ctx context.Context, tx *sql.Tx, req ToolDeleteDocumentGraphRequest) (*ToolDeleteDocumentGraphResponse, error) {
	documentID := strings.TrimSpace(req.DocumentID)
	if documentID == "" {
		return nil, fmt.Errorf("document_id is required")
	}
	if err := t.db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}
	resp := &ToolDeleteDocumentGraphResponse{DryRun: req.DryRun}
	// 1. Relation edges asserted by this document. Their ids and properties
	// both carry the document id (relationEdgeID appends :doc:<id>), but the
	// property is the query key: the id format has changed before and old rows
	// keep their old ids.
	// Every JSON predicate here goes through the dialect. The guard matters —
	// edges and nodes written without properties carry an empty string, and
	// reading a JSON field out of one is an error rather than a miss — and so
	// does the spelling, which is not the same on the two backends.
	edgeIDs, err := collectIDsTx(ctx, t.db, tx,
		`SELECT id FROM graph_edges WHERE `+docIDIs(t.db), documentID)
	if err != nil {
		return nil, fmt.Errorf("find relation edges: %w", err)
	}

	// 2. Entities that list this document as a source: deleted when it was the
	// only one, detached otherwise.
	type detachment struct {
		id        string
		remaining []string
	}
	var doomed []string
	var detached []detachment
	rows, err := t.db.txQuery(ctx, tx, `
		SELECT id, properties FROM graph_nodes
		WHERE `+t.db.Dialect().JSONArrayContains("graph_nodes.properties", "source_document_ids"), documentID)
	if err != nil {
		return nil, fmt.Errorf("find entity provenance: %w", err)
	}
	for rows.Next() {
		var id, propertiesJSON string
		if err := rows.Scan(&id, &propertiesJSON); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan entity provenance: %w", err)
		}
		var properties map[string]interface{}
		if err := json.Unmarshal([]byte(propertiesJSON), &properties); err != nil {
			rows.Close()
			return nil, fmt.Errorf("decode properties of %s: %w", id, err)
		}
		remaining := make([]string, 0)
		for _, source := range toStringSlice(properties["source_document_ids"]) {
			if source != documentID {
				remaining = append(remaining, source)
			}
		}
		if len(remaining) == 0 {
			doomed = append(doomed, id)
		} else {
			detached = append(detached, detachment{id: id, remaining: remaining})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 3. The document's own nodes: its chunks (real and stub) and the document
	// node itself. Matched by property, and the document node also by its
	// derived id for graphs written before the property existed.
	chunkIDs, err := collectIDsTx(ctx, t.db, tx,
		`SELECT id FROM graph_nodes WHERE node_type = 'chunk' AND `+docIDIs(t.db), documentID)
	if err != nil {
		return nil, fmt.Errorf("find chunk nodes: %w", err)
	}
	docNodeIDs, err := collectIDsTx(ctx, t.db, tx,
		`SELECT id FROM graph_nodes WHERE node_type = 'document' AND ((`+docIDIs(t.db)+`) OR id = ?)`,
		documentID, graphDocumentNodeID(documentID))
	if err != nil {
		return nil, fmt.Errorf("find document node: %w", err)
	}

	resp.RelationEdgesDeleted = len(edgeIDs)
	resp.EntityNodesDeleted = len(doomed)
	resp.EntityNodesDetached = len(detached)
	resp.ChunkNodesDeleted = len(chunkIDs)
	resp.DocumentNodeDeleted = len(docNodeIDs) > 0
	if req.DryRun {
		return resp, nil
	}

	for _, d := range detached {
		remainingJSON, err := json.Marshal(d.remaining)
		if err != nil {
			return nil, fmt.Errorf("encode remaining sources of %s: %w", d.id, err)
		}
		if _, err := t.db.txExec(ctx, tx,
			`UPDATE graph_nodes SET properties = `+t.db.Dialect().JSONSet("properties", "source_document_ids")+
				`, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			string(remainingJSON), d.id); err != nil {
			return nil, fmt.Errorf("detach %s: %w", d.id, err)
		}
	}
	// Edges and nodes go together through the one batch call that takes a
	// transaction. Edges first is not load-bearing — a node's edges go with it
	// via ON DELETE CASCADE — but the relation edges this document asserted
	// include ones whose endpoints survive, and those have to be named.
	nodeIDs := make([]string, 0, len(doomed)+len(chunkIDs)+len(docNodeIDs))
	nodeIDs = append(nodeIDs, doomed...)
	nodeIDs = append(nodeIDs, chunkIDs...)
	nodeIDs = append(nodeIDs, docNodeIDs...)
	if len(edgeIDs) > 0 || len(nodeIDs) > 0 {
		if _, err := t.db.graph.ExecuteBatchTx(ctx, tx, &graph.BatchGraphOperation{
			EdgeDeletes: edgeIDs, NodeDeletes: nodeIDs,
		}); err != nil {
			return nil, fmt.Errorf("delete the graph of %s: %w", documentID, err)
		}
	}
	// Carried out rather than synced here: the vector index must not stop
	// serving ids whose rows a rollback is about to restore.
	resp.deletedNodeIDs = nodeIDs
	return resp, nil
}

// collectIDsTx is collectIDs against a transaction, so the plan is read from
// the same snapshot the writes will change.
func collectIDsTx(ctx context.Context, db *DB, tx *sql.Tx, query string, args ...any) ([]string, error) {
	rows, err := db.txQuery(ctx, tx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// collectIDs runs a single-column id query and returns the ids.
// collectIDs runs a one-column query against whichever backend db speaks.
//
// It takes the *DB rather than the raw *sql.DB because the raw handle skips
// Rebind: on PostgreSQL a `?` that never became `$1` is a syntax error, and
// this file's queries are all parameterised.
func collectIDs(ctx context.Context, db *DB, query string, args ...any) ([]string, error) {
	rows, err := db.query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// docIDIs is the predicate "this row's properties name documentID", written
// once because four queries in this file need it and each one spelled it out.
func docIDIs(db *DB) string {
	return db.Dialect().JSONTextGuarded("properties", "document_id") + " = ?"
}
