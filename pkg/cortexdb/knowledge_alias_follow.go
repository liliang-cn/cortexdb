package cortexdb

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// knowledge_save, knowledge_update, ingest_document (its media form, which
// extracts entities) and InsertGraphDocument follow the aliases entity
// resolution records, as upsert_entities and upsert_relations do
// (entity_alias_follow.go). They derived each entity id from the name, so a
// document that still spoke of a folded entity brought it back beside the
// entity it was folded into, and every re-save of a document written before
// the merge did it again.
//
// The rule is upsert_entities': a name a live node still stands under is
// written to that node; otherwise a name an entity carries as an alias lands
// on that entity, which keeps its own name, content, description, type and
// vector and gains the writer's document, collection and mentions. An id the
// writer states, or one an ontology keys by type, is written as given, and a
// relation follows the alias at either end; one between an alias and the
// entity carrying it is dropped as a loop. A write builds its nodes and
// edges first (the plan) and follows the aliases when it writes them: each
// node written under an alias is folded into the node of the entity it lands
// on: the entry under the entity's own name wins, and an alias entry adds
// documents, mentions and collections only. Each edge at it is moved there and named after its new ends.
//
// knowledge_save writes in one transaction, and the aliases are read in it,
// once, through idx_graph_nodes_aliased, before the document's old graph is
// retired: an entity that document alone held is deleted by the retirement
// and written again from what the transaction read, its aliases with it. The
// nodes the names derive are read after the retirement, so a folded entity
// an earlier save brought back, and that only this document held, is gone by
// then and its name lands on the canonical entity.
//
// Under a caller scope with a knowledge rule, an alias is followed only into
// an entity one of the caller's collections lists, as upsert_entities
// follows it: a confined writer cannot reach, by a name it may not know is
// the same thing, an entity of a collection it cannot read, nor learn from
// the write where that name went. Its write keeps the name's own id.
//
// One difference from upsert_entities, where the entries are written one by
// one: knowledge_save keys its planned nodes by id, so a request that states
// an id and also names it bare writes one node, and the stated id makes the
// name stand under it for the whole request; the bare name is not followed.

// entityNameWrites is what a write derived from names: for each entity id
// derived from a name, the names it was derived from, and the ids the writer
// stated or an ontology keyed, which are written as given.
type entityNameWrites struct {
	names  map[string][]string
	stated map[string]bool
}

// named records that the write derived id from name.
func (w *entityNameWrites) named(name, id string) {
	if id == "" || id != graphEntityNodeID(name) {
		return
	}
	if w.names == nil {
		w.names = map[string][]string{}
	}
	if !slices.Contains(w.names[id], name) {
		w.names[id] = append(w.names[id], name)
	}
}

// namedEntities records the names extraction derived its ids from.
func (w *entityNameWrites) namedEntities(entities map[string]GraphEntity) {
	for id, entity := range entities {
		w.named(entity.Name, id)
	}
}

// statedID records an id the writer stated or an ontology keyed.
func (w *entityNameWrites) statedID(id string) {
	if w.stated == nil {
		w.stated = map[string]bool{}
	}
	w.stated[id] = true
}

// entityAliasFollow is what the aliases say of a write's names: the id each
// derived id would land on, and the stored entities it lands on.
type entityAliasFollow struct {
	to      map[string]string
	targets map[string]*graph.GraphNode
}

// nodeBatchReader reads graph nodes by id: the store's or a transaction's.
type nodeBatchReader func(ctx context.Context, ids []string) ([]*graph.GraphNode, error)

// txNodeReader reads inside tx (graph.WithReadTx): on a pool of one
// connection, which the transaction holds, a read through the pool would
// wait for ever.
func (db *DB) txNodeReader(tx *sql.Tx) nodeBatchReader {
	return func(ctx context.Context, ids []string) ([]*graph.GraphNode, error) {
		return db.graph.GetNodesBatch(graph.WithReadTx(ctx, tx), ids)
	}
}

// readEntityAliasFollow reads the aliases among the names of a write and the
// entities that carry them, through q and nodes. Nil when no name of the
// write is an alias the caller may follow.
func (db *DB) readEntityAliasFollow(ctx context.Context, q queryContexter, nodes nodeBatchReader, w entityNameWrites) (*entityAliasFollow, error) {
	if len(w.names) == 0 {
		return nil, nil
	}
	table, err := db.entityAliasTableIn(ctx, q)
	if err != nil {
		return nil, err
	}
	to := map[string]string{}
	for id, names := range w.names {
		if w.stated[id] {
			continue
		}
		names = slices.Sorted(slices.Values(names))
		for _, name := range names {
			if canonical := table[entityAliasKey(name)]; canonical != "" && canonical != id {
				to[id] = canonical
				break
			}
		}
	}
	if len(to) == 0 {
		return nil, nil
	}
	ids := slices.Compact(slices.Sorted(maps.Values(to)))
	stored, err := nodes(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read the entities the aliases name: %w", err)
	}
	// Under a caller scope, only into an entity one of the caller's
	// collections lists, as followEntityAliases.
	targets := make(map[string]*graph.GraphNode, len(stored))
	scope, _ := callerScopeOf(ctx)
	for _, node := range stored {
		if node != nil && (scope.Knowledge == nil || slices.ContainsFunc(toStringSlice(node.Properties[entityCollectionsKey]), scope.Knowledge)) {
			targets[node.ID] = node
		}
	}
	for id, canonical := range to {
		if targets[canonical] == nil {
			delete(to, id)
		}
	}
	if len(to) == 0 {
		return nil, nil
	}
	return &entityAliasFollow{to: to, targets: targets}, nil
}

// standing drops the names a live node stands under, read through nodes.
func (f *entityAliasFollow) standing(ctx context.Context, nodes nodeBatchReader) error {
	ids := slices.Sorted(maps.Keys(f.to))
	live, err := nodes(ctx, ids)
	if err != nil {
		return fmt.Errorf("read the entities named: %w", err)
	}
	for _, node := range live {
		if node != nil {
			delete(f.to, node.ID)
		}
	}
	return nil
}

// apply moves the write's nodes and edges onto the entities their names
// land on, and returns them with the new id of each edge it renamed and
// the old ids of the loops it dropped.
func (f *entityAliasFollow) apply(nodes []*graph.GraphNode, edges []*graph.GraphEdge) ([]*graph.GraphNode, []*graph.GraphEdge, map[string]string, map[string]bool) {
	if len(f.to) == 0 {
		return nodes, edges, nil, nil
	}
	// The nodes that stay first, so that a node written under an alias
	// finds the canonical entity's own node of the same request, whichever
	// comes first, and merges into it rather than over it.
	out := make([]*graph.GraphNode, 0, len(nodes))
	at := make(map[string]*graph.GraphNode, len(nodes))
	for _, node := range nodes {
		if f.to[node.ID] == "" {
			out = append(out, node)
			at[node.ID] = node
		}
	}
	for _, node := range nodes {
		canonical := f.to[node.ID]
		if canonical == "" {
			continue
		}
		node.ID = canonical
		landOnCanonical(node, f.targets[canonical])
		if into := at[canonical]; into != nil {
			mergeFoldedWrite(into, node)
			continue
		}
		out = append(out, node)
		at[canonical] = node
	}

	renamed := map[string]string{}
	dropped := map[string]bool{}
	kept := make([]*graph.GraphEdge, 0, len(edges))
	byID := make(map[string]*graph.GraphEdge, len(edges))
	for _, edge := range edges {
		from, to := f.to[edge.FromNodeID], f.to[edge.ToNodeID]
		if from != "" || to != "" {
			oldFrom, oldTo, oldID := edge.FromNodeID, edge.ToNodeID, edge.ID
			edge.FromNodeID, edge.ToNodeID = firstNonEmpty(from, oldFrom), firstNonEmpty(to, oldTo)
			// A relation between an alias and the entity carrying it is a
			// loop once followed, which a merge drops and upsert_relations
			// does not write; a loop the writer stated is kept.
			if edge.FromNodeID == edge.ToNodeID && oldFrom != oldTo {
				dropped[oldID] = true
				continue
			}
			edge.ID = rekeyFollowedEdge(edge, oldFrom, oldTo)
			renamed[oldID] = edge.ID
		}
		if seen := byID[edge.ID]; seen != nil {
			if seen.Properties == nil {
				seen.Properties = map[string]any{}
			}
			mergeRelationProperties(seen.Properties, edge.Properties)
			continue
		}
		byID[edge.ID] = edge
		kept = append(kept, edge)
	}
	return out, kept, renamed, dropped
}

// landOnCanonical gives a node written under an alias what upsert_entities
// gives one (keepCanonicalIdentity): the stored entity's content, type,
// vector and properties, its aliases among them, with the documents and
// collections of the write added.
func landOnCanonical(node, target *graph.GraphNode) {
	sources := unionStrings(target.Properties["source_document_ids"], node.Properties["source_document_ids"])
	collections := unionStrings(target.Properties[entityCollectionsKey], node.Properties[entityCollectionsKey])
	keepCanonicalIdentity([]*graph.GraphNode{node}, map[string]*graph.GraphNode{node.ID: target}, map[string]bool{node.ID: true})
	if node.Properties == nil {
		node.Properties = map[string]any{}
	}
	setStrings(node.Properties, "source_document_ids", sources)
	setStrings(node.Properties, entityCollectionsKey, collections)
}

// mergeFoldedWrite folds a node written under an alias into the node the
// same write makes for the entity it lands on. That node, the entry under
// the entity's own name or an earlier alias entry, is the one written; the
// alias adds its documents and collections and changes nothing else.
func mergeFoldedWrite(into, folded *graph.GraphNode) {
	if into.Properties == nil {
		into.Properties = map[string]any{}
	}
	sources := unionStrings(into.Properties["source_document_ids"], folded.Properties["source_document_ids"])
	collections := unionStrings(into.Properties[entityCollectionsKey], folded.Properties[entityCollectionsKey])
	setStrings(into.Properties, "source_document_ids", sources)
	setStrings(into.Properties, entityCollectionsKey, collections)
}

func setStrings(properties map[string]any, key string, values []string) {
	if len(values) > 0 {
		properties[key] = values
	}
}

// rekeyFollowedEdge names a moved edge after its new ends the way the write
// named it after its old ones: a mention, an extracted relation (by its
// chunk) or a stated relation (by its document). An id of another shape is
// kept.
func rekeyFollowedEdge(edge *graph.GraphEdge, oldFrom, oldTo string) string {
	if edge.ID == fmt.Sprintf("edge:mention:%s:%s", oldFrom, oldTo) {
		return fmt.Sprintf("edge:mention:%s:%s", edge.FromNodeID, edge.ToNodeID)
	}
	chunk, _ := edge.Properties["source_chunk_id"].(string)
	if chunk != "" && edge.ID == fmt.Sprintf("edge:rel:%s:%s:%s:%s", chunk, oldFrom, oldTo, edge.EdgeType) {
		return fmt.Sprintf("edge:rel:%s:%s:%s:%s", chunk, edge.FromNodeID, edge.ToNodeID, edge.EdgeType)
	}
	doc, _ := edge.Properties["document_id"].(string)
	if edge.ID == relationEdgeID(oldFrom, oldTo, edge.EdgeType, doc) {
		return relationEdgeID(edge.FromNodeID, edge.ToNodeID, edge.EdgeType, doc)
	}
	return edge.ID
}

// followedIDs maps ids through the follow, keeping their order and leaving
// out the dropped ones.
func (f *entityAliasFollow) followedIDs(ids []string, renamed map[string]string, dropped map[string]bool) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !dropped[id] {
			out = append(out, firstNonEmpty(f.to[id], renamed[id], id))
		}
	}
	return out
}
