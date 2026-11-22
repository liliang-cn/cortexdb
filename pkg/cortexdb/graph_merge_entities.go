package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Entity merging by a table the caller gives.
//
// GraphStore.MergeEntities folds alias entities into a canonical one and
// keeps everything it touches in history, but the only way to it outside Go
// was cortexdb-mcp-stdio --resolve-entities, which picks the pairs itself. A
// client that already knows its aliases (a table it keeps, or one it built
// while folding two brains into one) had no tool that applies it.
// merge_entities applies one group of that table; unmerge_entities undoes
// it from the journal merge_entities returns.
//
// The journal names the merge: its canonical entity and its instant, with
// the aliases and the edges it took. It holds nothing, and the undo believes
// none of the two lists: GraphStore.MergeEntitiesTx closes everything a
// merge takes at one instant, with reason merged into the canonical entity,
// so unmerge_entities reads from history what that instant took, refuses a
// journal that says otherwise, and restores only what nothing has touched
// since. A journal can restore what its merge took and nothing else.
//
// Each of the two reads, checks and writes in one transaction
// (mergeTransaction): the merge or the undo happens whole or not at all, and
// what it read is what it wrote over.

const producerMergeEntities = "cortexdb.merge_entities"

// ToolMergeEntitiesRequest folds Aliases into Canonical. Names resolve as
// upsert_entities resolves them; a full node id ("entity:acme") passes as is.
type ToolMergeEntitiesRequest struct {
	Canonical string   `json:"canonical"`
	Aliases   []string `json:"aliases"`
	DryRun    bool     `json:"dry_run,omitempty"`
}

// ToolMergeEntitiesResponse is what the merge did, or would do.
type ToolMergeEntitiesResponse struct {
	Canonical      string             `json:"canonical"`
	Merged         []string           `json:"merged"`
	EdgesRepointed int                `json:"edges_repointed"`
	LoopsRemoved   int                `json:"loops_removed"`
	DryRun         bool               `json:"dry_run,omitempty"`
	Journal        EntityMergeJournal `json:"journal"`
}

// EntityMergeJournal names one merge, for unmerge_entities: the canonical
// entity and the instant, which find it in history, and the aliases and
// edges it took, which history must confirm. A dry run has no instant.
type EntityMergeJournal struct {
	Canonical string    `json:"canonical"`
	Aliases   []string  `json:"aliases"`
	Edges     []string  `json:"edges"`
	MergedAt  time.Time `json:"merged_at,omitzero"`
}

// ToolUnmergeEntitiesRequest undoes the merge a journal names.
type ToolUnmergeEntitiesRequest struct {
	Journal EntityMergeJournal `json:"journal"`
}

// ToolUnmergeEntitiesResponse is what the unmerge restored.
type ToolUnmergeEntitiesResponse struct {
	Restored      []string `json:"restored"`
	EdgesRestored int      `json:"edges_restored"`
}

func mergeToolDefinitions() []ToolDefinition {
	journal := map[string]any{"type": "object", "description": "The journal merge_entities returned.", "properties": map[string]any{
		"canonical": toolStringSchema("The canonical entity node id."),
		"aliases":   toolStringArraySchema("The alias node ids the merge folded."),
		"edges":     toolStringArraySchema("The ids of the edges the merge repointed or dropped."),
		"merged_at": toolStringSchema("The instant of the merge, RFC 3339."),
	}}
	return []ToolDefinition{
		{
			Name:        "merge_entities",
			Mutates:     true,
			Destructive: true,
			Description: "Fold alias entities into one canonical entity, by a table the caller gives: every edge of an alias is repointed " +
				"onto the canonical entity, the self-loops this makes are dropped, and each alias and edge is kept in history as it " +
				"stood, marked merged into the canonical entity. The names folded, with the aliases a folded entity carried itself, " +
				"are recorded on the canonical entity, so a later call that names one reaches it. The merge is one transaction, and " +
				"an entity that is not there refuses the whole call. Returns the journal unmerge_entities takes to undo it. Use " +
				"dry_run to see what would change.",
			InputSchema: toolObjectSchema(
				[]string{"canonical", "aliases"},
				map[string]any{
					"canonical": toolStringSchema("Name or node id of the entity that remains."),
					"aliases":   toolStringArraySchema("Names or node ids of the entities folded into it."),
					"dry_run":   toolBooleanSchema("Report what would be merged, with the journal, without merging."),
				},
			),
		},
		{
			Name:        "unmerge_entities",
			Mutates:     true,
			Destructive: true,
			Description: "Undo a merge_entities from its journal, in one transaction: each alias and each edge the merge took is " +
				"written back as it stood before the merge, read from history, and the names the merge recorded are taken off the " +
				"canonical entity. The whole call is refused when history does not show a merge into the journal's canonical entity " +
				"at the journal's instant taking exactly the aliases and edges the journal names, when an alias exists again, or " +
				"when an alias or an edge of the merge has been written or deleted since, by a later merge or its undo included: " +
				"an undo never writes over what came after. A merge is undone once, and the journal of a dry run undoes nothing.",
			InputSchema: toolObjectSchema([]string{"journal"}, map[string]any{"journal": journal}),
		},
	}
}

// mergeTransaction opens the transaction a merge or an undo runs in, after
// the guard the other graph writes pass, and returns it with the context
// that reads inside it. Its first statement keeps every other writer of the
// graph out until the commit (graphWriteLock), so what the call reads is
// what it writes over: no entity is written, and no edge added, changed or
// deleted, between the two.
func (t *GraphRAGToolbox) mergeTransaction(ctx context.Context) (*sql.Tx, context.Context, error) {
	if err := t.db.guardStrictActions(ctx); err != nil {
		return nil, nil, err
	}
	tx, err := t.db.SQL().BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("begin: %w", err)
	}
	if _, err := t.db.txExec(ctx, tx, graphWriteLock(t.db.Dialect().Kind())); err != nil {
		_ = tx.Rollback()
		return nil, nil, fmt.Errorf("lock the graph: %w", err)
	}
	return tx, graph.WithReadTx(ctx, tx), nil
}

// graphWriteLock is the statement that makes a transaction the only writer
// of the graph's nodes and edges until it ends; readers are not held. On
// PostgreSQL it locks the two tables against writes. On SQLite a
// transaction becomes the writer at its first write, and one that read
// first can be refused the turn if another process wrote meanwhile: a
// delete that matches no row takes the turn before any read.
func graphWriteLock(kind sqldialect.Kind) string {
	if kind == sqldialect.Postgres {
		return `LOCK TABLE graph_nodes, graph_edges IN SHARE ROW EXCLUSIVE MODE`
	}
	return `DELETE FROM graph_nodes WHERE id IS NULL`
}

// commitMerge commits tx and then brings the vector index up to date, in
// the order of the commits (ReserveIndexSync).
func (t *GraphRAGToolbox) commitMerge(tx *sql.Tx, sync func()) error {
	syncInOrder := t.db.store.ReserveIndexSync()
	defer syncInOrder(nil)
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	syncInOrder(sync)
	return nil
}

// MergeEntities applies one group of an alias table.
func (t *GraphRAGToolbox) MergeEntities(ctx context.Context, req ToolMergeEntitiesRequest) (*ToolMergeEntitiesResponse, error) {
	canonical := resolveEntityNodeID("", strings.TrimSpace(req.Canonical))
	var aliases []string
	for _, name := range req.Aliases {
		if id := resolveEntityNodeID("", strings.TrimSpace(name)); id != "" && id != canonical && !slices.Contains(aliases, id) {
			aliases = append(aliases, id)
		}
	}
	if canonical == "" || len(aliases) == 0 {
		return nil, fmt.Errorf("merge_entities: a canonical entity and at least one other alias are required")
	}
	ids := append([]string{canonical}, aliases...)
	tx, inTx, err := t.mergeTransaction(ctx)
	if err != nil {
		return nil, fmt.Errorf("merge_entities: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	nodes, err := t.db.mergeableEntities(inTx, ids)
	if err != nil {
		return nil, err
	}
	edges, err := t.db.mergeTouches(inTx, canonical, aliases)
	if err != nil {
		return nil, err
	}
	resp := &ToolMergeEntitiesResponse{Canonical: canonical, Merged: aliases, DryRun: req.DryRun,
		Journal: EntityMergeJournal{Canonical: canonical, Aliases: aliases}}
	for _, edge := range edges {
		resp.Journal.Edges = append(resp.Journal.Edges, edge.ID)
		if mergeLoop(edge, canonical, aliases) {
			resp.LoopsRemoved++
		} else {
			resp.EdgesRepointed++
		}
	}
	if req.DryRun {
		return resp, nil
	}
	resp.Journal.MergedAt, err = t.db.graph.MergeEntitiesTx(graph.WithInvalidation(ctx, graph.Invalidation{Producer: producerMergeEntities}), tx, canonical, aliases)
	if err != nil {
		return nil, fmt.Errorf("merge_entities: %w", err)
	}
	kept := withEntityAliases(nodes[canonical], mergeNames(nodes, canonical, aliases), nil)
	if err := t.writeMerge(ctx, tx, &graph.BatchGraphOperation{NodeUpserts: []*graph.GraphNode{kept}}, func() {
		t.db.graph.SyncDeletedNodeIDs(ctx, aliases)
	}); err != nil {
		return nil, fmt.Errorf("merge_entities: %w", err)
	}
	return resp, nil
}

// writeMerge writes ops inside tx, commits, and syncs the vector index.
func (t *GraphRAGToolbox) writeMerge(ctx context.Context, tx *sql.Tx, ops *graph.BatchGraphOperation, sync func()) error {
	result, err := t.db.graph.ExecuteBatchTx(ctx, tx, ops)
	if err == nil {
		// A row the store refused is counted, not returned.
		err = result.Err()
	}
	if err != nil {
		return err
	}
	return t.commitMerge(tx, sync)
}

// mergeNames are the names a merge records on the canonical entity: each
// folded node's name, then the aliases that node carried from a merge of its
// own, in the order of the aliases, once per spelling that entity resolution
// tells apart, leaving out what the canonical entity is already called or
// already carries. The undo computes them again from the same nodes as they
// stood before the merge, and takes back exactly these.
func mergeNames(nodes map[string]*graph.GraphNode, canonical string, aliases []string) []string {
	seen := map[string]struct{}{}
	for _, name := range append([]string{entityNodeName(nodes[canonical])}, toStringSlice(nodes[canonical].Properties["aliases"])...) {
		seen[entityAliasKey(name)] = struct{}{}
	}
	var names []string
	for _, id := range aliases {
		for _, name := range append([]string{entityNodeName(nodes[id])}, toStringSlice(nodes[id].Properties["aliases"])...) {
			key := entityAliasKey(name)
			if _, ok := seen[key]; ok || name == "" {
				continue
			}
			seen[key] = struct{}{}
			names = append(names, name)
		}
	}
	return names
}

// entityNodeName is the name an entity node was written under.
func entityNodeName(node *graph.GraphNode) string {
	name, _ := node.Properties["name"].(string)
	return firstNonEmpty(strings.TrimSpace(name), node.Content)
}

// withEntityAliases is node with add put on, then drop taken off, the alias
// list entity resolution reads, kept sorted. It opens a version only if the
// list changed: the store keeps the version of a node rewritten unchanged.
func withEntityAliases(node *graph.GraphNode, add, drop []string) *graph.GraphNode {
	before := toStringSlice(node.Properties["aliases"])
	after := slices.DeleteFunc(slices.Clone(before), func(a string) bool { return slices.Contains(drop, a) })
	for _, name := range add {
		if !slices.Contains(after, name) {
			after = append(after, name)
		}
	}
	slices.Sort(after)
	properties := make(map[string]any, len(node.Properties)+1)
	maps.Copy(properties, node.Properties)
	properties["aliases"] = after
	if len(after) == 0 {
		delete(properties, "aliases")
	}
	written := *node
	written.Properties, written.ValidFrom = properties, time.Time{}
	return &written
}

// mergeableEntities checks that each id is a node, read as of the context's
// instant, and that the caller may merge it: under a caller scope, an entity
// every collection of which the scope permits, so the merge moves no other
// collection's edges. Each refusal reads the same, so it does not tell an
// entity of another collection from one that is not there. It returns the
// nodes it read, by id.
func (db *DB) mergeableEntities(ctx context.Context, ids []string) (map[string]*graph.GraphNode, error) {
	nodes, err := db.graph.GetNodesBatch(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read the entities to merge: %w", err)
	}
	found := make(map[string]*graph.GraphNode, len(nodes))
	for _, node := range nodes {
		found[node.ID] = node
	}
	scope, _ := callerScopeOf(ctx)
	scoped := scope.Knowledge != nil || scope.Memory != nil
	for _, id := range ids {
		node := found[id]
		if node == nil || scoped && !ownedByCaller(node, scope.Knowledge) {
			return nil, fmt.Errorf("entity %q not found", id)
		}
	}
	return found, nil
}

// ownedByCaller reports whether every collection that lists the entity is
// one permits allows. An entity that lists none is no caller's.
func ownedByCaller(node *graph.GraphNode, permits func(string) bool) bool {
	collections := toStringSlice(node.Properties[entityCollectionsKey])
	return permits != nil && len(collections) > 0 && !slices.ContainsFunc(collections, func(c string) bool { return !permits(c) })
}

// mergeTouches is every edge the merge repoints or drops, once each: the
// edges of the aliases and the self-loops the canonical entity already has.
func (db *DB) mergeTouches(ctx context.Context, canonical string, aliases []string) ([]*graph.GraphEdge, error) {
	var out []*graph.GraphEdge
	seen := map[string]struct{}{}
	for _, id := range append([]string{canonical}, aliases...) {
		edges, err := db.graph.GetEdges(ctx, id, "both")
		if err != nil {
			return nil, fmt.Errorf("read the edges of %q: %w", id, err)
		}
		for _, edge := range edges {
			if _, dup := seen[edge.ID]; !dup && (id != canonical || mergeLoop(edge, canonical, nil)) {
				seen[edge.ID] = struct{}{}
				out = append(out, edge)
			}
		}
	}
	return out, nil
}

// mergeLoop reports whether the merge leaves edge a self-loop: both its ends
// are the canonical entity or one of the aliases.
func mergeLoop(edge *graph.GraphEdge, canonical string, aliases []string) bool {
	folded := func(id string) bool { return id == canonical || slices.Contains(aliases, id) }
	return folded(edge.FromNodeID) && folded(edge.ToNodeID)
}

// callMergeTool dispatches the two from JSON, the shape callTemporalTool
// uses.
func (t *GraphRAGToolbox) callMergeTool(ctx context.Context, name string, input json.RawMessage) (any, bool, error) {
	switch name {
	case "merge_entities":
		var req ToolMergeEntitiesRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.MergeEntities(ctx, req)
		return resp, true, err
	case "unmerge_entities":
		var req ToolUnmergeEntitiesRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, true, fmt.Errorf("decode %s: %w", name, err)
		}
		resp, err := t.UnmergeEntities(ctx, req)
		return resp, true, err
	}
	return nil, false, nil
}

// addMergeMCPTools registers the two beside their definitions, for the
// reason addTemporalMCPTools gives.
func addMergeMCPTools(server *mcp.Server, definitions map[string]ToolDefinition, toolbox *GraphRAGToolbox) {
	addGraphRAGMCPTool(server, definitions["merge_entities"], toolbox.MergeEntities)
	addGraphRAGMCPTool(server, definitions["unmerge_entities"], toolbox.UnmergeEntities)
}
