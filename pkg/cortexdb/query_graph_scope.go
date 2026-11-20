package cortexdb

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// The graph lane of a query confined to a collection.
//
// Entity nodes are shared by every collection, and so are the relations
// between them. Keeping only the collection's chunks at the end of the walk
// (runGraphQueryPrefetch) left the walk itself free to cross what another
// collection wrote: to start from an entity only another collection knows,
// to follow a relation only another collection states, to pass through
// another collection's chunks or through a node no collection holds, such
// as a memory or a decision. A chunk of the collection reached that way
// tells the caller that the other collection's entity or relation exists.
// With a collection, the lane walks only what the collection may see
// (laneScope.mayEnter).

// graphLaneWalk is the walk of the graph lane: the whole graph with no
// collection, the collection's graph with one.
func (db *DB) graphLaneWalk(collection string) func(context.Context, string, graph.TraversalOptions) ([]*graph.GraphNode, error) {
	if collection == "" {
		return db.graph.Neighbors
	}
	return func(ctx context.Context, start string, opts graph.TraversalOptions) ([]*graph.GraphNode, error) {
		return db.neighborsInCollection(ctx, collection, start, opts)
	}
}

// laneStep is one edge of a walk and the node it leads to. The seed is a
// step with no edge.
type laneStep struct {
	edge *graph.GraphEdge
	next string
}

// laneScope is what one round of a walk confined to a collection reads: the
// nodes its steps lead to, the edges an inferred edge among them rests on,
// the chunks all those edges name, and which of the documents met have a
// chunk in the collection.
type laneScope struct {
	collection string
	nodes      map[string]graphNodeSummary
	docs       map[string]struct{}
	supports   map[string]*graph.GraphEdge
}

// edgeProvenance is what an edge names as its provenance: its document_id,
// and the chunks of chunk_ids and source_chunk_id. A step with no edge, the
// seed, names none.
func edgeProvenance(edge *graph.GraphEdge) (string, []string) {
	if edge == nil {
		return "", nil
	}
	doc, _ := edge.Properties["document_id"].(string)
	chunks := stringsFromAny(edge.Properties["chunk_ids"])
	if src, _ := edge.Properties["source_chunk_id"].(string); src != "" {
		chunks = append(chunks, src)
	}
	return doc, chunks
}

// edgeSupports is the edges an edge rests on: the support_edge_ids that
// inference (apply_inference, rules_apply) writes on what it derives.
func edgeSupports(edge *graph.GraphEdge) []string {
	if edge == nil {
		return nil
	}
	return stringsFromAny(edge.Properties["support_edge_ids"])
}

// mayEnter reports whether a walk confined to the collection may take the
// step. The node must be a chunk or a document of the collection, or an
// entity the collection knows (entityKnownIn): an entity is a node under
// EntityNodeIDPrefix, whatever its declared type. Any other node (a memory,
// a decision, a run, a node written under an id of its own) is never
// entered: no collection holds it, and its text is another caller's. The
// edge must be one the collection states (stated).
func (s laneScope) mayEnter(step laneStep) bool {
	node, ok := s.nodes[step.next]
	if !ok {
		return false
	}
	switch {
	case isDocumentNodeID(step.next, node.DocumentID):
		if _, in := s.docs[node.DocumentID]; !in {
			return false
		}
	case strings.HasPrefix(step.next, EntityNodeIDPrefix):
		if !entityKnownIn(node.Collections, s.collection) {
			return false
		}
	default:
		return false
	}
	return s.stated(step.edge, true)
}

// stated reports whether the collection may follow the edge. An edge that
// names its provenance is followed when it names one of the collection's
// documents or chunks, whatever it cites as support: it is then the
// collection's own statement. An edge that names none and rests on others,
// as one that inference derives from premises of several documents (it
// names a document only when every premise shares it), is followed only
// when each of them is, one level down (mayRest): a premise that rests on
// others in turn, or that the round could not read, is not. An edge that
// names neither, as a relation written by upsert_relations without them, is
// followed, as an entity with no list is known everywhere.
func (s laneScope) stated(edge *graph.GraphEdge, mayRest bool) bool {
	doc, chunks := edgeProvenance(edge)
	if doc != "" || len(chunks) > 0 {
		return s.namesTheCollection(doc, chunks)
	}
	for _, id := range edgeSupports(edge) {
		support, read := s.supports[id]
		if !mayRest || !read || !s.stated(support, false) {
			return false
		}
	}
	return true
}

// namesTheCollection reports whether the document, or one of the chunks,
// is the collection's.
func (s laneScope) namesTheCollection(doc string, chunks []string) bool {
	if _, in := s.docs[doc]; in {
		return true
	}
	for _, chunk := range chunks {
		if _, in := s.docs[s.nodes[chunk].DocumentID]; in {
			return true
		}
	}
	return false
}

// laneScopeOf reads what mayEnter needs for one round of steps.
func (db *DB) laneScopeOf(ctx context.Context, collection string, steps []laneStep) (laneScope, error) {
	ids := make([]string, 0, len(steps))
	edges := make([]*graph.GraphEdge, 0, len(steps))
	for _, step := range steps {
		ids = append(ids, step.next)
		edges = append(edges, step.edge)
	}
	supports := map[string]*graph.GraphEdge{}
	for _, batch := range premiseBatches(steps) {
		read, err := db.graph.GetEdgesBatch(ctx, batch)
		if err != nil {
			return laneScope{}, err
		}
		for _, edge := range read {
			supports[edge.ID] = edge
			edges = append(edges, edge)
		}
	}
	var docIDs []string
	for _, edge := range edges {
		doc, chunks := edgeProvenance(edge)
		ids = append(ids, chunks...)
		docIDs = append(docIDs, doc)
	}
	nodes, err := db.graphNodeSummariesByIDs(ctx, ids)
	if err != nil {
		return laneScope{}, err
	}
	for _, node := range nodes {
		docIDs = append(docIDs, node.DocumentID)
	}
	docs, err := db.documentsInCollection(ctx, collection, docIDs)
	if err != nil {
		return laneScope{}, err
	}
	return laneScope{collection: collection, nodes: nodes, docs: docs, supports: supports}, nil
}

// documentsInCollectionQuery is the SQL that selects, among docIDs, the
// documents with a chunk in the collection. The unary plus keeps the planner
// off the collection index: given hundreds of documents, it preferred one
// pass over every chunk of the collection to that many doc_id lookups.
func documentsInCollectionQuery(collection string, docIDs []string) (string, []any) {
	placeholders, args := sqlPlaceholders(docIDs)
	return `SELECT DISTINCT e.doc_id FROM embeddings e JOIN collections c ON c.id = +e.collection_id WHERE c.name = ? AND e.doc_id IN (` + placeholders + `)`,
		append([]any{collection}, args...)
}

// premiseBatches is the edges the steps' edges rest on, in batches for
// GetEdgesBatch, which a read as of a time gives asOfVariables more.
func premiseBatches(steps []laneStep) [][]string {
	var ids []string
	for _, step := range steps {
		ids = append(ids, edgeSupports(step.edge)...)
	}
	return laneIDBatches(ids, asOfVariables)
}

// asOfVariables is what a graph read as of a time adds to its own
// variables (graph.GraphStore.EdgeSource).
const asOfVariables = 8

// laneIDBatches is ids without repeats or empty ids, in batches that leave
// reserved variables of the SQLite cap to the rest of the query.
func laneIDBatches(ids []string, reserved int) [][]string {
	ids = slices.DeleteFunc(slices.Compact(slices.Sorted(slices.Values(ids))), func(id string) bool { return id == "" })
	return slices.Collect(slices.Chunk(ids, sqliteMaxVariables-reserved))
}

// documentsInCollection is the set of documents among docIDs with a chunk in
// the collection. It fails as chunksInScope does when the collections cannot
// be read: the walk then stops rather than walk past them.
func (db *DB) documentsInCollection(ctx context.Context, collection string, docIDs []string) (map[string]struct{}, error) {
	docs := make(map[string]struct{}, len(docIDs))
	for _, batch := range laneIDBatches(docIDs, 1) {
		query, args := documentsInCollectionQuery(collection, batch)
		rows, err := db.query(ctx, query, args...)
		if err == nil {
			err = eachRow(rows, func() error {
				var id string
				err := rows.Scan(&id)
				docs[id] = struct{}{}
				return err
			})
		}
		if err != nil {
			return nil, fmt.Errorf("load walked chunks: %w", err)
		}
	}
	return docs, nil
}

// neighborsInCollection is graph.Neighbors for a walk confined to the
// collection: breadth first from start, up to opts.MaxDepth edges away (one
// when it is not set), over the edges of opts.EdgeTypes (all when none is
// named), it takes only the steps mayEnter allows, stops at opts.Limit
// nodes reached, and returns those of opts.NodeTypes (all when none is
// named). The start itself must be an entity the collection knows. It
// differs from graph.Neighbors in three ways, none of which the graph lane
// meets: it walks both directions whatever opts.Direction says; it does not
// score by opts.Relations; and it reads a whole round before it checks the
// limit, since mayEnter judges a step by what the round read.
func (db *DB) neighborsInCollection(ctx context.Context, collection, start string, opts graph.TraversalOptions) ([]*graph.GraphNode, error) {
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 1
	}
	visited := map[string]struct{}{}
	var reached []string
	steps := []laneStep{{next: start}}
walk:
	for depth := 0; len(steps) > 0; depth++ {
		scope, err := db.laneScopeOf(ctx, collection, steps)
		if err != nil {
			return nil, err
		}
		frontier := make([]string, 0, len(steps))
		for _, step := range steps {
			if _, seen := visited[step.next]; seen || !scope.mayEnter(step) {
				continue
			}
			visited[step.next] = struct{}{}
			frontier = append(frontier, step.next)
			if depth == 0 {
				continue
			}
			if len(opts.NodeTypes) == 0 || slices.Contains(opts.NodeTypes, scope.nodes[step.next].NodeType) {
				reached = append(reached, step.next)
			}
			if opts.Limit > 0 && len(visited)-1 >= opts.Limit {
				break walk
			}
		}
		if depth == opts.MaxDepth {
			break
		}
		steps = nil
		for _, id := range frontier {
			edges, err := db.graph.GetEdges(ctx, id, "both")
			if err != nil {
				return nil, err
			}
			for _, edge := range edges {
				if len(opts.EdgeTypes) > 0 && !slices.Contains(opts.EdgeTypes, edge.EdgeType) {
					continue
				}
				next := edge.FromNodeID
				if next == id {
					next = edge.ToNodeID
				}
				steps = append(steps, laneStep{edge: edge, next: next})
			}
		}
	}
	return db.graph.GetNodesBatch(ctx, reached)
}
