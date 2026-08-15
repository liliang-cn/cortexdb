package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
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
}

// DeleteDocumentGraph removes a document's chunk and document nodes, its
// relation edges, and the entities it alone asserted. Embeddings are not
// touched: they live in the caller's collection and the caller knows which
// they are; the graph does not.
func (t *GraphRAGToolbox) DeleteDocumentGraph(ctx context.Context, req ToolDeleteDocumentGraphRequest) (*ToolDeleteDocumentGraphResponse, error) {
	documentID := strings.TrimSpace(req.DocumentID)
	if documentID == "" {
		return nil, fmt.Errorf("document_id is required")
	}
	if err := t.db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}
	resp := &ToolDeleteDocumentGraphResponse{DryRun: req.DryRun}
	sqldb := t.db.SQL()

	// 1. Relation edges asserted by this document. Their ids and properties
	// both carry the document id (relationEdgeID appends :doc:<id>), but the
	// property is the query key: the id format has changed before and old rows
	// keep their old ids.
	// json_valid guards every JSON predicate here: edges and nodes written
	// without properties carry an empty string, and json_extract on one is an
	// error, not a miss.
	edgeIDs, err := collectIDs(ctx, sqldb,
		`SELECT id FROM graph_edges WHERE json_valid(properties) AND json_extract(properties, '$.document_id') = ?`, documentID)
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
	rows, err := sqldb.QueryContext(ctx, `
		SELECT id, properties FROM graph_nodes
		WHERE json_valid(properties) AND EXISTS (
			SELECT 1 FROM json_each(json_extract(graph_nodes.properties, '$.source_document_ids')) je
			WHERE je.value = ?
		)`, documentID)
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
	chunkIDs, err := collectIDs(ctx, sqldb,
		`SELECT id FROM graph_nodes WHERE node_type = 'chunk' AND json_valid(properties) AND json_extract(properties, '$.document_id') = ?`, documentID)
	if err != nil {
		return nil, fmt.Errorf("find chunk nodes: %w", err)
	}
	docNodeIDs, err := collectIDs(ctx, sqldb,
		`SELECT id FROM graph_nodes WHERE node_type = 'document' AND ((json_valid(properties) AND json_extract(properties, '$.document_id') = ?) OR id = ?)`,
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

	if len(edgeIDs) > 0 {
		if _, err := t.db.graph.DeleteEdgesBatch(ctx, edgeIDs); err != nil {
			return nil, fmt.Errorf("delete relation edges: %w", err)
		}
	}
	for _, d := range detached {
		remainingJSON, err := json.Marshal(d.remaining)
		if err != nil {
			return nil, fmt.Errorf("encode remaining sources of %s: %w", d.id, err)
		}
		if _, err := sqldb.ExecContext(ctx,
			`UPDATE graph_nodes SET properties = json_set(properties, '$.source_document_ids', json(?)), updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			string(remainingJSON), d.id); err != nil {
			return nil, fmt.Errorf("detach %s: %w", d.id, err)
		}
	}
	// Nodes go through the batch API and then the index sync, so the vector
	// index does not keep serving ids whose rows are gone. Edges go with their
	// nodes via ON DELETE CASCADE.
	nodeIDs := make([]string, 0, len(doomed)+len(chunkIDs)+len(docNodeIDs))
	nodeIDs = append(nodeIDs, doomed...)
	nodeIDs = append(nodeIDs, chunkIDs...)
	nodeIDs = append(nodeIDs, docNodeIDs...)
	if len(nodeIDs) > 0 {
		if _, err := t.db.graph.DeleteNodesBatch(ctx, nodeIDs); err != nil {
			return nil, fmt.Errorf("delete document nodes: %w", err)
		}
		t.db.graph.SyncDeletedNodeIDs(ctx, nodeIDs)
	}
	return resp, nil
}

// collectIDs runs a single-column id query and returns the ids.
func collectIDs(ctx context.Context, sqldb *sql.DB, query string, args ...any) ([]string, error) {
	rows, err := sqldb.QueryContext(ctx, query, args...)
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
