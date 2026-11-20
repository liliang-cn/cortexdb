package cortexdb

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// A merge closes every version it takes at one instant, with reason merged
// into its canonical entity (GraphStore.MergeEntitiesTx). The three reads
// below are what the undo knows of a merge, each over one history table and
// its live table.

// unmergeIDsPerStatement is how many ids one statement of the undo names: a
// merge of any size is undone, where one statement for all its edges would
// pass the variables a statement takes (32 766 on SQLite).
var unmergeIDsPerStatement = 999

// takenAt selects the ids a merge into one canonical entity closed at one
// instant. Arguments: the reason, the canonical entity, the instant.
func takenAt(table string) string {
	return `SELECT DISTINCT id FROM ` + table + ` WHERE reason = ? AND superseded_by = ? AND valid_to = ?`
}

// closedSince selects, among the ids given, those of which a version was
// closed after an instant: rewritten or deleted since, by anyone.
func closedSince(table, holes string) string {
	return `SELECT DISTINCT id FROM ` + table + ` WHERE id IN (` + holes + `) AND valid_to > ?`
}

// notLeftByTheMerge selects, among the edge ids given, those that are live
// and are not the version the merge opened at its instant: an edge the
// merge dropped as a loop and that was written again, which closes nothing.
const notLeftByTheMerge = `SELECT id FROM graph_edges WHERE id IN (%s) AND (valid_from IS NULL OR valid_from <> ?)`

// touchedSince is the first of ids that query finds, or "" when it finds
// none.
func (t *GraphRAGToolbox) touchedSince(ctx context.Context, tx *sql.Tx, ids []string, at time.Time, query func(holes string) string) (string, error) {
	for chunk := range slices.Chunk(ids, unmergeIDsPerStatement) {
		holes, args := sqlPlaceholders(chunk)
		found, err := collectIDsTx(ctx, t.db, tx, query(holes), append(args, at)...)
		if err != nil {
			return "", fmt.Errorf("read what changed since the merge: %w", err)
		}
		if len(found) > 0 {
			return found[0], nil
		}
	}
	return "", nil
}

// sameIDs reports whether the two lists name the same ids, in any order.
func sameIDs(a, b []string) bool {
	a, b = slices.Compact(slices.Sorted(slices.Values(a))), slices.Compact(slices.Sorted(slices.Values(b)))
	return slices.Equal(a, b)
}

// UnmergeEntities writes back what the journal's merge took, as it stood
// before the merge, when nothing has touched it since.
func (t *GraphRAGToolbox) UnmergeEntities(ctx context.Context, req ToolUnmergeEntitiesRequest) (*ToolUnmergeEntitiesResponse, error) {
	j := req.Journal
	if j.Canonical == "" || len(j.Aliases) == 0 || j.MergedAt.IsZero() {
		return nil, fmt.Errorf("unmerge_entities: the journal needs its canonical entity, its aliases and the instant of its merge")
	}
	at := j.MergedAt.UTC().Truncate(time.Microsecond)
	tx, inTx, err := t.mergeTransaction(ctx)
	if err != nil {
		return nil, fmt.Errorf("unmerge_entities: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	scope, _ := callerScopeOf(ctx)
	scoped := scope.Knowledge != nil || scope.Memory != nil
	// A confined caller reads every refusal as an entity that is not
	// there: which of the checks failed would tell it what another
	// collection holds or did.
	refuse := func(id, format string, args ...any) error {
		if scoped {
			return fmt.Errorf("unmerge_entities: entity %q not found", id)
		}
		return fmt.Errorf("unmerge_entities: "+format, args...)
	}

	// History names the merge: what was closed at that instant as merged
	// into that entity. The journal must say the same, whole.
	var taken [2][]string
	for i, table := range []string{"graph_node_history", "graph_edge_history"} {
		if taken[i], err = collectIDsTx(ctx, t.db, tx, takenAt(table), graph.ReasonMerged, j.Canonical, at); err != nil {
			return nil, fmt.Errorf("unmerge_entities: read the history of the merge: %w", err)
		}
	}
	aliases, edgeIDs := taken[0], taken[1]
	if !sameIDs(aliases, j.Aliases) || !sameIDs(edgeIDs, j.Edges) {
		return nil, refuse(j.Aliases[0], "history shows no merge into %q at %s that took the aliases and the edges the journal names", j.Canonical, at.Format(time.RFC3339Nano))
	}

	// Now: the canonical entity is there and the caller's, which the edges
	// written back to it need; no alias is, or the undo would write over an
	// entity that was created after the merge.
	ids := append([]string{j.Canonical}, aliases...)
	live, err := t.db.graph.GetNodesBatch(inTx, ids)
	if err != nil {
		return nil, fmt.Errorf("unmerge_entities: read the entities: %w", err)
	}
	var current *graph.GraphNode
	for _, node := range live {
		if node.ID != j.Canonical {
			return nil, refuse(node.ID, "entity %q exists again: it was written after the merge", node.ID)
		}
		current = node
	}
	if current == nil || scoped && !ownedByCaller(current, scope.Knowledge) {
		return nil, fmt.Errorf("unmerge_entities: entity %q not found", j.Canonical)
	}

	// Since: nothing the merge took has been written or deleted.
	for _, check := range []struct {
		ids   []string
		query func(string) string
	}{
		{aliases, func(holes string) string { return closedSince("graph_node_history", holes) }},
		{edgeIDs, func(holes string) string { return closedSince("graph_edge_history", holes) }},
		{edgeIDs, func(holes string) string { return fmt.Sprintf(notLeftByTheMerge, holes) }},
	} {
		if id, err := t.touchedSince(ctx, tx, check.ids, at, check.query); err != nil || id != "" {
			if err != nil {
				return nil, fmt.Errorf("unmerge_entities: %w", err)
			}
			return nil, refuse(j.Aliases[0], "%q was written or deleted after the merge", id)
		}
	}

	// Then: everything as it stood at the last instant before the merge.
	past := graph.AsOf(inTx, at.Add(-time.Microsecond))
	was, err := t.db.mergeableEntities(past, ids)
	if err != nil {
		return nil, fmt.Errorf("unmerge_entities: %w", err)
	}
	ops := &graph.BatchGraphOperation{}
	for chunk := range slices.Chunk(edgeIDs, unmergeIDsPerStatement) {
		edges, err := t.db.graph.GetEdgesBatch(past, chunk)
		if err != nil {
			return nil, fmt.Errorf("unmerge_entities: read the edges: %w", err)
		}
		ops.EdgeUpserts = append(ops.EdgeUpserts, edges...)
	}
	for _, edge := range ops.EdgeUpserts {
		edge.ValidFrom = time.Time{}
	}
	for _, id := range aliases {
		was[id].ValidFrom = time.Time{}
		ops.NodeUpserts = append(ops.NodeUpserts, was[id])
	}
	restored := slices.Clone(ops.NodeUpserts)
	// The names the merge recorded are the ones it would record again on
	// the entities as they stood; a name the canonical entity carried before
	// stays, and so does one recorded since.
	ops.NodeUpserts = append(ops.NodeUpserts, withEntityAliases(current, nil, mergeNames(was, j.Canonical, aliases)))
	if err := t.writeMerge(ctx, tx, ops, func() { t.db.graph.SyncUpsertedNodes(ctx, restored) }); err != nil {
		return nil, fmt.Errorf("unmerge_entities: %w", err)
	}
	return &ToolUnmergeEntitiesResponse{Restored: aliases, EdgesRestored: len(ops.EdgeUpserts)}, nil
}
