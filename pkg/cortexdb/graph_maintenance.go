package cortexdb

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// GraphMaintenanceOptions controls PruneJunkEntities and ReindexMemoryGraph.
type GraphMaintenanceOptions struct {
	// DryRun reports what would change without writing anything.
	DryRun bool
	// Limit caps how many rows are processed (0 = all).
	Limit int
}

// GraphPruneReport says what a junk-entity prune did, naming every node it
// removed — a deletion justified only by a count is not auditable.
type GraphPruneReport struct {
	Scanned      int      `json:"scanned"`
	Pruned       int      `json:"pruned"`
	EdgesRemoved int      `json:"edges_removed"`
	DryRun       bool     `json:"dry_run"`
	Names        []string `json:"names,omitempty"`
}

// PruneJunkEntities removes generic entity nodes whose names the current
// extraction rules would never produce.
//
// The old Title Case pattern collected English grammar — real stores grew
// entity nodes named "This", "Only", "Requires", "Measured" — and co-occurrence
// paired each with everything nearby, so every junk node radiated junk edges.
// The extraction fix stops new ones; this retires the stock. Only untyped
// nodes are candidates: a node somebody saved with a type ("host", "Flight")
// was declared, not scraped, and stays whatever its name looks like.
func (db *DB) PruneJunkEntities(ctx context.Context, opts GraphMaintenanceOptions) (*GraphPruneReport, error) {
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("prune junk entities: init schema: %w", err)
	}
	rows, err := db.store.GetDB().QueryContext(ctx, `
		SELECT id, content FROM graph_nodes
		WHERE id LIKE 'entity:%' AND (node_type = ? OR node_type = '' OR node_type IS NULL)
	`, genericEntityNodeType)
	if err != nil {
		return nil, fmt.Errorf("list entity nodes: %w", err)
	}
	type victim struct{ id, name string }
	var victims []victim
	report := &GraphPruneReport{DryRun: opts.DryRun}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan entity node: %w", err)
		}
		report.Scanned++
		if isJunkEntityName(name) {
			victims = append(victims, victim{id: id, name: name})
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if opts.Limit > 0 && len(victims) > opts.Limit {
		victims = victims[:opts.Limit]
	}

	sort.Slice(victims, func(i, j int) bool { return victims[i].name < victims[j].name })
	for _, v := range victims {
		report.Names = append(report.Names, v.name)
	}
	report.Pruned = len(victims)
	if opts.DryRun {
		return report, nil
	}

	for _, v := range victims {
		res, err := db.store.GetDB().ExecContext(ctx,
			`DELETE FROM graph_edges WHERE from_node_id = ? OR to_node_id = ?`, v.id, v.id)
		if err != nil {
			return report, fmt.Errorf("delete edges of %q: %w", v.id, err)
		}
		if n, err := res.RowsAffected(); err == nil {
			report.EdgesRemoved += int(n)
		}
		if _, err := db.store.GetDB().ExecContext(ctx,
			`DELETE FROM graph_nodes WHERE id = ?`, v.id); err != nil {
			return report, fmt.Errorf("delete node %q: %w", v.id, err)
		}
	}
	return report, nil
}

// isJunkEntityName reports whether a stored entity name would be rejected by
// the extraction rules today: empty, too short, or grammar all the way through.
func isJunkEntityName(name string) bool {
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 2 {
		return true
	}
	return allStopwords(name)
}

// GraphReindexReport summarizes a memory-graph backfill pass.
type GraphReindexReport struct {
	Memories     int  `json:"memories"`
	Indexed      int  `json:"indexed"`
	Skipped      int  `json:"skipped"`
	EntitiesSeen int  `json:"entities_seen"`
	DryRun       bool `json:"dry_run"`
}

// maxReindexEntitiesPerMemory bounds how many extracted entities one memory may
// assert. Past that the memory is a survey, and wiring it to everything it
// glances at makes every entity's neighbourhood into the same soup.
const maxReindexEntitiesPerMemory = 12

// ReindexMemoryGraph gives stored memories the graph presence new saves get.
//
// Memories saved before they had graph nodes — or without explicit entities —
// are unreachable through entity_names: the edges recall would walk were never
// written. This pass extracts entities from each memory's text with the current
// rules and writes the same memory-node-plus-mentions shape SaveMemory now
// writes, so the backlog becomes reachable the same way new memories are.
// Idempotent: everything it writes is an upsert keyed on stable ids.
func (db *DB) ReindexMemoryGraph(ctx context.Context, opts GraphMaintenanceOptions) (*GraphReindexReport, error) {
	memories, err := db.ListAllMemories(ctx)
	if err != nil {
		return nil, fmt.Errorf("reindex memory graph: %w", err)
	}
	if opts.Limit > 0 && len(memories) > opts.Limit {
		memories = memories[:opts.Limit]
	}
	report := &GraphReindexReport{Memories: len(memories), DryRun: opts.DryRun}
	tools := db.GraphRAGTools()
	for _, m := range memories {
		extracted := extractCorpusEntities(m.Content)
		if len(extracted) == 0 {
			report.Skipped++
			continue
		}
		if len(extracted) > maxReindexEntitiesPerMemory {
			extracted = extracted[:maxReindexEntitiesPerMemory]
		}
		report.EntitiesSeen += len(extracted)
		if opts.DryRun {
			report.Indexed++
			continue
		}
		nodeID := memoryGraphNodeID(m.ID)
		if err := db.upsertMemoryNode(ctx, tools, nodeID, m.Content); err != nil {
			return report, fmt.Errorf("memory %q: %w", m.ID, err)
		}
		entities := make([]ToolEntityInput, 0, len(extracted))
		for _, e := range extracted {
			entities = append(entities, ToolEntityInput{
				Name:     e.Name,
				Type:     e.Type,
				ChunkIDs: []string{nodeID},
			})
		}
		if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: entities}); err != nil {
			return report, fmt.Errorf("memory %q entities: %w", m.ID, err)
		}
		report.Indexed++
	}
	return report, nil
}
