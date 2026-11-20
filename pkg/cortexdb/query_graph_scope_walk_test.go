package cortexdb

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// The walk ends when no step is left, not at the depth asked for: a caller
// may send any max_hops, and a walk that went on round after round over an
// exhausted graph would never return for a depth such as math.MaxInt.
func TestAScopedWalkEndsWhenNothingIsLeftToWalk(t *testing.T) {
	db, ctx := openLaneScopeBrain(t)
	start := EntityNodeID("Kestrel Vale")
	ids := func(depth int) string {
		t.Helper()
		nodes, err := db.neighborsInCollection(ctx, "alpha", start, graph.TraversalOptions{MaxDepth: depth})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, node := range nodes {
			out = append(out, node.ID)
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	if deep, all := ids(math.MaxInt), ids(64); deep != all {
		t.Errorf("walk at depth math.MaxInt =\n%s, want what depth 64 reaches\n%s", deep, all)
	}
}

// The scoped walk keeps the options of graph.Neighbors that the lane uses:
// the node types it returns and the number of nodes it reaches.
func TestTheScopedWalkKeepsNodeTypesAndLimit(t *testing.T) {
	db, ctx := openLaneScopeBrain(t)
	start := EntityNodeID("Kestrel Vale")
	all, err := db.neighborsInCollection(ctx, "alpha", start, graph.TraversalOptions{MaxDepth: 3})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, node := range all {
		ids = append(ids, node.ID)
	}
	sort.Strings(ids)
	// Every node alpha may see within three hops, of every type, and not
	// the seed.
	want := []string{graphChunkNodeID("a1", 0), graphChunkNodeID("a10", 0), graphChunkNodeID("a11", 0), graphChunkNodeID("a4", 0), graphChunkNodeID("a9", 0),
		graphDocumentNodeID("a1"), graphDocumentNodeID("a11"), graphDocumentNodeID("a4"),
		EntityNodeID("Crane Lake"), EntityNodeID("Finch Dale"), EntityNodeID("Lark Weir"), EntityNodeID("Swift Brook")}
	sort.Strings(want)
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("walk with no type filter =\n%v, want\n%v", ids, want)
	}
	limited, err := db.neighborsInCollection(ctx, "alpha", start, graph.TraversalOptions{MaxDepth: 3, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 {
		t.Errorf("walk with limit 2 = %d nodes, want 2", len(limited))
	}
	chunks, err := db.neighborsInCollection(ctx, "alpha", start, graph.TraversalOptions{MaxDepth: 3, NodeTypes: []string{"chunk"}})
	if err != nil {
		t.Fatal(err)
	}
	var chunkIDs []string
	for _, node := range chunks {
		chunkIDs = append(chunkIDs, node.ID)
	}
	sort.Strings(chunkIDs)
	wantChunks := []string{graphChunkNodeID("a1", 0), graphChunkNodeID("a10", 0), graphChunkNodeID("a11", 0), graphChunkNodeID("a4", 0), graphChunkNodeID("a9", 0)}
	sort.Strings(wantChunks)
	if strings.Join(chunkIDs, ",") != strings.Join(wantChunks, ",") {
		t.Errorf("walk for chunks = %v, want a1, a10, a11, a4, a9", chunkIDs)
	}
}

// documentsInCollectionQuery is the SQL that tells which documents have a
// chunk in the collection.
func TestDocumentsInCollectionQuery(t *testing.T) {
	query, args := documentsInCollectionQuery("alpha", []string{"a1", "b1"})
	want := `SELECT DISTINCT e.doc_id FROM embeddings e JOIN collections c ON c.id = +e.collection_id WHERE c.name = ? AND e.doc_id IN (?,?)`
	if query != want || len(args) != 3 || args[0] != "alpha" || args[1] != "a1" || args[2] != "b1" {
		t.Errorf("query = %q %v", query, args)
	}
}

// The documents a walk reached are looked up by their own index. On a
// collection that holds most of the store, the collection index names
// nearly every chunk, and the lookup read the whole table at each step of
// the walk: 1.3 s of a 1.5 s knowledge_search on a 270,000-chunk brain.
func TestDocumentsInCollectionReadsOnlyTheNamedDocuments(t *testing.T) {
	db, ctx := openLaneScopeBrain(t)
	// A walk reaches hundreds of documents; the planner weighs that many
	// lookups against one pass over the collection.
	ids := make([]string, 400)
	for i := range ids {
		ids[i] = fmt.Sprintf("doc-%d", i)
	}
	query, args := documentsInCollectionQuery("alpha", ids)
	rows, err := db.SQL().QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
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
	if !strings.Contains(text, "idx_embeddings_doc_id") || strings.Contains(text, "idx_embeddings_collection_id") {
		t.Errorf("the documents of a walk are not looked up by their index:\n%s", text)
	}
}

// A store that cannot be read stops the scoped walk with its error, at each
// read: the nodes, the documents' collections, the edges, the nodes
// returned.
func TestAScopedWalkThatCannotReadTheStoreFails(t *testing.T) {
	for _, c := range []struct{ table, column string }{
		{"graph_nodes", "properties"},
		{"embeddings", "doc_id"},
		{"graph_edges", "properties"},
		{"graph_nodes", "vector"},
	} {
		db, ctx := openLaneScopeBrain(t)
		if _, err := db.SQL().Exec(`ALTER TABLE ` + c.table + ` RENAME COLUMN ` + c.column + ` TO ` + c.column + `_hidden`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.neighborsInCollection(ctx, "alpha", EntityNodeID("Kestrel Vale"), graph.TraversalOptions{MaxDepth: 2}); err == nil {
			t.Errorf("%s.%s renamed: the walk did not fail", c.table, c.column)
		}
	}
	// The edges an inferred edge rests on are read in the round, and only
	// that read touches the edges here.
	db, ctx := openLaneScopeBrain(t)
	if _, err := db.SQL().Exec(`ALTER TABLE graph_edges RENAME COLUMN properties TO properties_hidden`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.laneScopeOf(ctx, "alpha", []laneStep{{edge: &graph.GraphEdge{Properties: map[string]any{"support_edge_ids": []any{"e1"}}}, next: "entity:x"}}); err == nil {
		t.Error("graph_edges.properties renamed: reading the supports of an inferred edge did not fail")
	}
	// The documents are read only when there are some to read.
	db, ctx = openLaneScopeBrain(t)
	docs, err := db.documentsInCollection(ctx, "alpha", nil)
	if err != nil || len(docs) != 0 {
		t.Errorf("no documents: %v, %v", docs, err)
	}
}

// mayEnter on its own: a step to a node the round did not read is refused,
// and source_chunk_id counts as provenance, like chunk_ids. A document is
// entered when it is the collection's; an entity of a declared type with
// no list is known everywhere; a node that is neither a chunk, a document
// nor an entity is never entered, whatever its type.
func TestALaneStepIsJudgedByItsNodeAndItsProvenance(t *testing.T) {
	scope := laneScope{
		collection: "alpha",
		nodes: map[string]graphNodeSummary{
			"entity:x":     {ID: "entity:x", NodeType: "entity", Collections: []string{"alpha"}},
			"entity:typed": {ID: "entity:typed", NodeType: "Person"},
			"chunk:a:000":  {ID: "chunk:a:000", NodeType: "chunk", DocumentID: "a"},
			"chunk:b:000":  {ID: "chunk:b:000", NodeType: "chunk", DocumentID: "b"},
			"doc:a":        {ID: "doc:a", NodeType: "document", DocumentID: "a"},
			"doc:b":        {ID: "doc:b", NodeType: "document", DocumentID: "b"},
			"decision:d":   {ID: "decision:d", NodeType: DecisionNodeType},
			"run:r":        {ID: "run:r", NodeType: RunNodeType},
			"memory:m":     {ID: "memory:m", NodeType: "memory"},
		},
		docs: map[string]struct{}{"a": {}},
		supports: map[string]*graph.GraphEdge{
			"s-a":      {ID: "s-a", Properties: map[string]any{"chunk_ids": []any{"chunk:a:000"}}},
			"s-b":      {ID: "s-b", Properties: map[string]any{"document_id": "b"}},
			"s-none":   {ID: "s-none", Properties: map[string]any{}},
			"s-nested": {ID: "s-nested", Properties: map[string]any{"support_edge_ids": []any{"s-a"}}},
		},
	}
	relation := func(src string) *graph.GraphEdge {
		return &graph.GraphEdge{Properties: map[string]any{"source_chunk_id": src}}
	}
	inferred := func(supports ...any) *graph.GraphEdge {
		return &graph.GraphEdge{Properties: map[string]any{"inferred": true, "support_edge_ids": supports}}
	}
	for _, c := range []struct {
		step laneStep
		want bool
	}{
		{laneStep{next: "entity:missing"}, false},
		{laneStep{edge: relation("chunk:a:000"), next: "entity:x"}, true},
		{laneStep{edge: relation("chunk:b:000"), next: "entity:x"}, false},
		{laneStep{next: "doc:a"}, true},
		{laneStep{next: "doc:b"}, false},
		{laneStep{next: "entity:typed"}, true},
		{laneStep{next: "decision:d"}, false},
		{laneStep{next: "run:r"}, false},
		{laneStep{next: "memory:m"}, false},
		// A relation two collections state is the collection's when any of
		// its chunks is, whichever comes first.
		{laneStep{edge: &graph.GraphEdge{Properties: map[string]any{"chunk_ids": []any{"chunk:b:000", "chunk:a:000"}}}, next: "entity:x"}, true},
		// An inferred edge is followed when every edge it rests on is.
		{laneStep{edge: inferred("s-a"), next: "entity:x"}, true},
		{laneStep{edge: inferred("s-a", "s-none"), next: "entity:x"}, true},
		{laneStep{edge: inferred("s-a", "s-b"), next: "entity:x"}, false},
		{laneStep{edge: inferred("s-a", "s-missing"), next: "entity:x"}, false},
		{laneStep{edge: inferred("s-nested"), next: "entity:x"}, false},
		// An edge that names the collection's own document or chunk is
		// the collection's, whatever it cites as support; one that names
		// another collection's is not, whatever it cites.
		{laneStep{edge: &graph.GraphEdge{Properties: map[string]any{"chunk_ids": []any{"chunk:a:000"}, "support_edge_ids": []any{"s-b"}}}, next: "entity:x"}, true},
		{laneStep{edge: &graph.GraphEdge{Properties: map[string]any{"document_id": "a", "support_edge_ids": []any{"s-missing"}}}, next: "entity:x"}, true},
		{laneStep{edge: &graph.GraphEdge{Properties: map[string]any{"document_id": "b", "support_edge_ids": []any{"s-a"}}}, next: "entity:x"}, false},
	} {
		if got := scope.mayEnter(c.step); got != c.want {
			t.Errorf("mayEnter(%+v) = %v, want %v", c.step, got, c.want)
		}
	}
}

// The scoped walk takes the depth and the edge types of graph.Neighbors: a
// depth of 0 is one hop, and with edge types named it follows only those.
func TestTheScopedWalkKeepsTheDepthAndEdgeTypesOfNeighbors(t *testing.T) {
	db, ctx := openLaneScopeBrain(t)
	start := EntityNodeID("Kestrel Vale")
	walk := func(opts graph.TraversalOptions) string {
		t.Helper()
		nodes, err := db.neighborsInCollection(ctx, "alpha", start, opts)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(nodes))
		for _, node := range nodes {
			ids = append(ids, node.ID)
		}
		sort.Strings(ids)
		return strings.Join(ids, ",")
	}
	one := walk(graph.TraversalOptions{MaxDepth: 1})
	if want := graphChunkNodeID("a1", 0) + "," + EntityNodeID("Finch Dale") + "," + EntityNodeID("Lark Weir"); one != want {
		t.Errorf("walk at one hop = %s, want %s", one, want)
	}
	if got := walk(graph.TraversalOptions{}); got != one {
		t.Errorf("walk at depth 0 = %q, want the walk at one hop, %s", got, one)
	}
	if got, want := walk(graph.TraversalOptions{MaxDepth: 3, EdgeTypes: []string{"drains", "feeds"}}), EntityNodeID("Lark Weir")+","+EntityNodeID("Swift Brook"); got != want {
		t.Errorf("walk over drains and feeds = %s, want %s", got, want)
	}
}

// The documents a round asks about are read without repeats or empty ids,
// in batches that leave one variable of the SQLite cap to the collection.
func TestDocumentsInCollectionBatches(t *testing.T) {
	ids := []string{"", "d1", "d1", ""}
	for i := range 1500 {
		ids = append(ids, "doc"+strconv.Itoa(i))
	}
	batches := laneIDBatches(ids, 1)
	if len(batches) != 2 || len(batches[0]) != sqliteMaxVariables-1 || len(batches[1]) != 1501-(sqliteMaxVariables-1) {
		t.Fatalf("batches of %d", len(batches))
	}
	seen := map[string]bool{}
	for _, batch := range batches {
		if _, args := documentsInCollectionQuery("alpha", batch); len(args) > sqliteMaxVariables {
			t.Errorf("a batch takes %d variables, over the cap of %d", len(args), sqliteMaxVariables)
		}
		for _, id := range batch {
			if id == "" || seen[id] {
				t.Errorf("batch holds %q twice or empty", id)
			}
			seen[id] = true
		}
	}
	// The edges an inferred edge rests on are read with eight variables
	// to spare, which a read as of a time adds.
	if edges := laneIDBatches(ids, 8); len(edges) != 2 || len(edges[0]) != sqliteMaxVariables-8 {
		t.Errorf("edge batches of %d, first of %d", len(edges), len(edges[0]))
	}
	if laneIDBatches([]string{""}, 1) != nil {
		t.Error("only empty ids: want no batch")
	}
}

// A row the read of the documents cannot step over stops the walk with the
// error of the read, like a query that cannot run. The view fails on the
// row of a1, after the row of a2.
func TestDocumentsInCollectionFailsOnARowItCannotRead(t *testing.T) {
	db, ctx := openLaneScopeBrain(t)
	for _, q := range []string{
		`ALTER TABLE embeddings RENAME TO embeddings_stored`,
		`CREATE VIEW embeddings AS SELECT id, collection_id, CASE WHEN doc_id = 'a1' THEN json('broken') ELSE doc_id END AS doc_id FROM embeddings_stored`,
	} {
		if _, err := db.SQL().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.documentsInCollection(ctx, "alpha", []string{"a2", "a1"}); err == nil || !strings.Contains(err.Error(), "load walked chunks") {
		t.Errorf("unreadable row: err = %v, want load walked chunks", err)
	}
}

// The limit counts every node the walk reaches, as graph.Neighbors does,
// also when only some node types are returned: the lane asks for chunks
// under a limit. With node types, the walk returns the nodes of those types
// among what the same walk without them returns.
func TestTheScopedWalkCountsEveryNodeTowardTheLimit(t *testing.T) {
	db, ctx := openLaneScopeBrain(t)
	start := EntityNodeID("Kestrel Vale")
	ids := func(nodes []*graph.GraphNode, nodeType string) string {
		var out []string
		for _, node := range nodes {
			if nodeType == "" || node.NodeType == nodeType {
				out = append(out, node.ID)
			}
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	for _, limit := range []int{2, 4, 6} {
		all, err := db.neighborsInCollection(ctx, "alpha", start, graph.TraversalOptions{MaxDepth: 3, Limit: limit})
		if err != nil {
			t.Fatal(err)
		}
		chunks, err := db.neighborsInCollection(ctx, "alpha", start, graph.TraversalOptions{MaxDepth: 3, Limit: limit, NodeTypes: []string{"chunk"}})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := ids(chunks, ""), ids(all, "chunk"); got != want {
			t.Errorf("limit %d, chunks only = %s, want the chunks of the walk with no type, %s", limit, got, want)
		}
	}
}

// An inferred edge whose premises come from two collections names no
// document (the rule engine stamps one only when every premise shares it),
// only the edges it rests on. alpha states Kestrel Vale -funds-> Osprey,
// beta states Osprey -pays-> Heron Mill, and the rule funds then pays gives
// Kestrel Vale -backs-> Heron Mill: alpha's lane may not take it to a3. A
// rule over two of alpha's relations, from two documents, is followed.
func TestTheGraphLaneFollowsAnInferredEdgeOnlyOverTheCollectionsPremises(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "inferred.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	save := func(id, collection, content string, names []string, relations ...ToolRelationInput) {
		t.Helper()
		chunk := graphChunkNodeID(id, 0)
		entities := make([]ToolEntityInput, 0, len(names))
		for _, name := range names {
			entities = append(entities, ToolEntityInput{Name: name, ChunkIDs: []string{chunk}})
		}
		for i := range relations {
			relations[i].ChunkIDs = []string{chunk}
		}
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: id, Collection: collection, Content: content, Entities: entities, Relations: relations}); err != nil {
			t.Fatal(err)
		}
	}
	save("a1", "alpha", "kestrel vale funds the osprey.", []string{"Kestrel Vale", "Osprey"},
		ToolRelationInput{From: "Kestrel Vale", To: "Osprey", Type: "funds"})
	save("a2", "alpha", "the osprey nests near alpha.", []string{"Osprey"})
	save("a3", "alpha", "the heron mill grinds alpha grain.", []string{"Heron Mill"})
	save("a4", "alpha", "the osprey feeds the lark weir.", []string{"Osprey", "Lark Weir"},
		ToolRelationInput{From: "Osprey", To: "Lark Weir", Type: "feeds"})
	save("a5", "alpha", "the lark weir floods alpha fields.", []string{"Lark Weir"})
	save("b1", "beta", "the osprey pays the heron mill, betasecret.", []string{"Osprey", "Heron Mill"},
		ToolRelationInput{From: "Osprey", To: "Heron Mill", Type: "pays"})
	if _, err := db.ApplyInferenceRules(ctx, ApplyInferenceRequest{Rules: []InferenceRule{
		{RuleID: "backs", LeftRelationType: "funds", RightRelationType: "pays", ResultRelationType: "backs"},
		{RuleID: "sponsors", LeftRelationType: "funds", RightRelationType: "feeds", ResultRelationType: "sponsors"},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(graphLaneDocs(t, db, ctx, "alpha", "Kestrel Vale", 2), ","); got != "a1,a2,a4,a5" {
		t.Errorf("alpha, seed Kestrel Vale, 2 hops: %q, want a1,a2,a4,a5", got)
	}
	// Without a collection, the inferred edge leads to a3.
	if got := graphLaneDocs(t, db, ctx, "", "Kestrel Vale", 2); !slices.Contains(got, "a3") {
		t.Errorf("unscoped, seed Kestrel Vale, 2 hops: %v, want a3 among them", got)
	}
}

// The provenance keys of an edge are the engine's: a rule's metadata may not
// set chunk_ids or source_chunk_id on what it derives, as it may not set
// document_id. A rule over alpha's funds and beta's pays, whose metadata
// names alpha's chunk, gives a derived edge with no provenance of its own,
// which alpha's lane does not take to a3.
func TestARuleMayNotGiveWhatItDerivesTheCollectionsProvenance(t *testing.T) {
	for _, key := range []string{"source_chunk_id", "chunk_ids"} {
		db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "rulemeta.db")))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		ctx := context.Background()
		for _, d := range []struct {
			id, collection, content string
			names                   []string
			relations               []ToolRelationInput
		}{
			{"a1", "alpha", "kestrel vale funds the osprey.", []string{"Kestrel Vale", "Osprey"}, []ToolRelationInput{{From: "Kestrel Vale", To: "Osprey", Type: "funds"}}},
			{"a2", "alpha", "the osprey nests near alpha.", []string{"Osprey"}, nil},
			{"a3", "alpha", "the heron mill grinds alpha grain.", []string{"Heron Mill"}, nil},
			{"b1", "beta", "the osprey pays the heron mill, betasecret.", []string{"Osprey", "Heron Mill"}, []ToolRelationInput{{From: "Osprey", To: "Heron Mill", Type: "pays"}}},
		} {
			chunk := graphChunkNodeID(d.id, 0)
			entities := make([]ToolEntityInput, 0, len(d.names))
			for _, name := range d.names {
				entities = append(entities, ToolEntityInput{Name: name, ChunkIDs: []string{chunk}})
			}
			for i := range d.relations {
				d.relations[i].ChunkIDs = []string{chunk}
			}
			if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: d.id, Collection: d.collection, Content: d.content, Entities: entities, Relations: d.relations}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.ApplyRules(ctx, RulesApplyRequest{Rules: []RuleDefinition{{
			ID: "backs", Text: "IF funds(?x, ?y) AND pays(?y, ?z) THEN backs(?x, ?z)",
			Metadata: map[string]string{key: graphChunkNodeID("a2", 0)},
		}}}); err != nil {
			t.Fatal(err)
		}
		edges, err := db.graph.GetEdges(ctx, EntityNodeID("Kestrel Vale"), "out")
		if err != nil {
			t.Fatal(err)
		}
		derived := 0
		for _, edge := range edges {
			if edge.EdgeType != "backs" {
				continue
			}
			derived++
			if doc, chunks := edgeProvenance(edge); doc != "" || len(chunks) > 0 {
				t.Errorf("metadata %s: the derived edge names %q %v", key, doc, chunks)
			}
		}
		if derived != 1 {
			t.Fatalf("metadata %s: %d derived edges, want 1", key, derived)
		}
		if got := strings.Join(graphLaneDocs(t, db, ctx, "alpha", "Kestrel Vale", 2), ","); got != "a1,a2" {
			t.Errorf("metadata %s: alpha, seed Kestrel Vale, 2 hops: %q, want a1,a2", key, got)
		}
	}
}

// The premises of a round are read in batches that leave a read as of a
// time the variables it adds (graph.GraphStore.EdgeSource).
func TestThePremisesOfARoundLeaveTheVariablesOfAnAsOfRead(t *testing.T) {
	db, ctx := openLaneScopeBrain(t)
	_, args := db.graph.EdgeSource(graph.WithReadOptions(ctx, graph.ReadOptions{AsOf: time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)}))
	if len(args) != asOfVariables {
		t.Errorf("a read as of a time adds %d variables, the walk keeps %d", len(args), asOfVariables)
	}
	steps := make([]laneStep, 0, 992)
	for i := range 992 {
		steps = append(steps, laneStep{edge: &graph.GraphEdge{Properties: map[string]any{"support_edge_ids": []any{"e" + strconv.Itoa(i)}}}})
	}
	if batches := premiseBatches(steps); len(batches) != 2 || len(batches[0]) != sqliteMaxVariables-asOfVariables {
		t.Errorf("992 premises in %d batches", len(batches))
	}
}
