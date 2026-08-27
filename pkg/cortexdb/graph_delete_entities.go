package cortexdb

import (
	"context"
	"fmt"
	"strings"
)

// Entity deletion.
//
// The tool surface could create entities and relations but never remove one:
// upsert_entities has no counterpart, and the knowledge_graph_* deletes operate
// on RDF triples, not the GraphRAG entity graph. So a graph could only ever
// grow, and a wrong or junk entity — the kind deterministic extraction
// occasionally produces from a capitalized word in prose — was permanent.
//
// Deleting through here rather than with SQL matters: DeleteNode also drops the
// node from the vector index, which a direct DELETE would leave pointing at a
// row that no longer exists.

// ToolDeleteEntitiesRequest removes entities and every edge touching them.
type ToolDeleteEntitiesRequest struct {
	// Names are entity names or full node ids ("entity:foo"). Names are
	// resolved the same way upsert_entities resolves them, so a caller can pass
	// back exactly what it saw.
	Names []string `json:"names"`
	// DryRun reports what would be removed without removing it. Deletion is not
	// reversible, so a caller that is guessing should look first.
	DryRun bool `json:"dry_run,omitempty"`
}

// ToolDeleteEntitiesResponse says what went and what was not there.
type ToolDeleteEntitiesResponse struct {
	Deleted []string `json:"deleted,omitempty"`
	// NotFound names nothing was stored under, so a typo surfaces instead of
	// being reported as a successful deletion of nothing.
	NotFound     []string `json:"not_found,omitempty"`
	EdgesRemoved int      `json:"edges_removed"`
	DryRun       bool     `json:"dry_run,omitempty"`
}

// DeleteEntities removes entity nodes and their edges.
func (t *GraphRAGToolbox) DeleteEntities(ctx context.Context, req ToolDeleteEntitiesRequest) (*ToolDeleteEntitiesResponse, error) {
	if len(req.Names) == 0 {
		return &ToolDeleteEntitiesResponse{DryRun: req.DryRun}, nil
	}
	if err := t.db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}

	resp := &ToolDeleteEntitiesResponse{DryRun: req.DryRun}
	for _, raw := range req.Names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		id := resolveEntityNodeID("", name)
		if id == "" {
			continue
		}
		node, err := t.db.graph.GetNode(ctx, id)
		if err != nil || node == nil {
			resp.NotFound = append(resp.NotFound, name)
			continue
		}

		var edges int
		if err := t.db.queryRow(ctx,
			`SELECT COUNT(*) FROM graph_edges WHERE from_node_id = ? OR to_node_id = ?`,
			id, id).Scan(&edges); err != nil {
			return nil, fmt.Errorf("count edges for %q: %w", name, err)
		}

		if req.DryRun {
			resp.Deleted = append(resp.Deleted, name)
			resp.EdgesRemoved += edges
			continue
		}
		// Edges go with the node: graph_edges has ON DELETE CASCADE on both ends.
		if err := t.db.graph.DeleteNode(ctx, id); err != nil {
			return nil, fmt.Errorf("delete entity %q: %w", name, err)
		}
		resp.Deleted = append(resp.Deleted, name)
		resp.EdgesRemoved += edges
	}
	return resp, nil
}
