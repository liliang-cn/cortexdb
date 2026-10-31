package graph

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/encoding"
	"github.com/liliang-cn/cortexdb/v2/internal/pgtest"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// mirrorNodes is n nodes of dimension 4, from a fixed seed.
func mirrorNodes(prefix string, n int, seed int64) []*GraphNode {
	r := rand.New(rand.NewSource(seed))
	nodes := make([]*GraphNode, n)
	for i := range nodes {
		v := make([]float32, 4)
		for j := range v {
			v[j] = float32(r.NormFloat64())
		}
		nodes[i] = &GraphNode{ID: fmt.Sprintf("%s%d", prefix, i), NodeType: "thing", Content: "x", Vector: v}
	}
	return nodes
}

// selfFound counts the nodes a vector search finds among the top 5 for their
// own vector.
func selfFound(t *testing.T, g *GraphStore, nodes []*GraphNode) int {
	t.Helper()
	found := 0
	for _, n := range nodes {
		res, err := g.HybridSearch(context.Background(), &HybridQuery{Vector: n.Vector, TopK: 5})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res {
			if r.Node.ID == n.ID {
				found++
				break
			}
		}
	}
	return found
}

// unmirrored lists the nodes of graph_nodes that have a vector the table can
// hold (dim wide) and no row in graph_node_vectors, and the mirror rows of
// nodes that have none: the two ways the in-database search and the nodes can
// disagree.
func unmirrored(t *testing.T, db *sql.DB, dim int) (missing, stale []string) {
	t.Helper()
	ctx := context.Background()
	mirrored := map[string]bool{}
	rows, err := db.QueryContext(ctx, `SELECT node_id FROM graph_node_vectors`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		mirrored[id] = true
	}
	rows.Close()
	rows, err = db.QueryContext(ctx, `SELECT id, vector FROM graph_nodes`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			t.Fatal(err)
		}
		v, err := encoding.DecodeVector(blob)
		if err != nil {
			t.Fatal(err)
		}
		// Written out rather than calling mirrorable, so the check does not
		// move with the code it checks.
		holdable := len(v) == dim
		for _, x := range v {
			if x != x || x > math.MaxFloat32 || x < -math.MaxFloat32 {
				holdable = false
			}
		}
		switch {
		case holdable && !mirrored[id]:
			missing = append(missing, id)
		case !holdable && mirrored[id]:
			stale = append(stale, id)
		}
	}
	return missing, stale
}

// mirrorGraph is a PostgreSQL graph store of dimension 4 on a schema of its
// own, or a loud skip.
func mirrorGraph(t *testing.T) (*GraphStore, *sql.DB) {
	t.Helper()
	db := pgtest.Open(t, "graph_mirror")
	if db == nil {
		t.Skip(pgtest.EnvDSN + " is unset — the PostgreSQL vector mirror is NOT covered by this run")
	}
	return openMirror(t, db, 4), db
}

// openMirror opens a graph store on db with the given configured dimension,
// 0 for none, as each open of a brain does.
func openMirror(t *testing.T, db *sql.DB, dim int) *GraphStore {
	t.Helper()
	cfg := core.DefaultConfig()
	cfg.VectorDim = dim
	g := NewGraphStoreOn(db, sqldialect.For(sqldialect.Postgres), testHost{cfg: cfg})
	if err := g.InitGraphSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !g.vecCap.Enabled {
		t.Fatalf("no vector mirror: %s", g.vecCap.Reason)
	}
	return g
}

// wideNodes is n nodes of the given width, from a fixed seed.
func wideNodes(prefix string, n, width int, seed int64) []*GraphNode {
	r := rand.New(rand.NewSource(seed))
	nodes := make([]*GraphNode, n)
	for i := range nodes {
		v := make([]float32, width)
		for j := range v {
			v[j] = float32(r.NormFloat64())
		}
		nodes[i] = &GraphNode{ID: fmt.Sprintf("%s%d", prefix, i), NodeType: "thing", Content: "x", Vector: v}
	}
	return nodes
}

// A node written in a batch is found by its vector, on both backends. On
// PostgreSQL the search reads graph_node_vectors, which only UpsertNode kept:
// once one node was written alone, the search trusted the mirror and nodes
// written in a batch were never found. Ingest writes a document's node alone
// and its chunks and entities in a batch.
func TestNodesWrittenInABatchAreFoundByTheirVector(t *testing.T) {
	ctx := context.Background()
	for _, write := range []struct {
		name string
		run  func(g *GraphStore, nodes []*GraphNode) error
	}{
		{"UpsertNodesBatch", func(g *GraphStore, nodes []*GraphNode) error {
			res, err := g.UpsertNodesBatch(ctx, nodes)
			if err == nil {
				err = res.Err()
			}
			return err
		}},
		{"ExecuteBatch", func(g *GraphStore, nodes []*GraphNode) error {
			res, err := g.ExecuteBatch(ctx, &BatchGraphOperation{NodeUpserts: nodes})
			if err == nil {
				err = res.Err()
			}
			return err
		}},
	} {
		for _, b := range backends(t) {
			t.Run(write.name+"/"+b.name, func(t *testing.T) {
				nodes := mirrorNodes("n", 200, 1)
				if err := write.run(b.store, nodes); err != nil {
					t.Fatal(err)
				}
				if err := b.store.UpsertNode(ctx, mirrorNodes("alone", 1, 2)[0]); err != nil {
					t.Fatal(err)
				}
				if got := selfFound(t, b.store, nodes[:20]); got != 20 {
					t.Fatalf("%d of 20 nodes written in a batch found by their own vector", got)
				}
				if b.name == "postgres" {
					if missing, stale := unmirrored(t, b.store.db, 4); len(missing)+len(stale) > 0 {
						t.Fatalf("%d nodes without their mirror row, %d mirror rows without a vector", len(missing), len(stale))
					}
				}
			})
		}
	}
}

// A node rewritten in a batch without a vector leaves the mirror, as one
// rewritten alone does: a vector a node no longer has must not keep it
// findable.
func TestANodeRewrittenInABatchWithoutAVectorLeavesTheMirror(t *testing.T) {
	g, db := mirrorGraph(t)
	ctx := context.Background()
	nodes := mirrorNodes("n", 10, 1)
	for _, write := range []func() (*BatchResult, error){
		func() (*BatchResult, error) { return g.UpsertNodesBatch(ctx, nodes) },
		func() (*BatchResult, error) {
			return g.ExecuteBatch(ctx, &BatchGraphOperation{NodeUpserts: []*GraphNode{
				{ID: "n0", NodeType: "thing", Content: "no vector now"},
			}})
		},
		func() (*BatchResult, error) {
			return g.UpsertNodesBatch(ctx, []*GraphNode{{ID: "n1", NodeType: "thing", Content: "no vector now"}})
		},
	} {
		if res, err := write(); err != nil || res.FailedCount != 0 {
			t.Fatalf("%+v %v", res, err)
		}
	}
	if missing, stale := unmirrored(t, db, 4); len(missing)+len(stale) > 0 {
		t.Fatalf("missing %v, stale %v", missing, stale)
	}
}

// A node whose vector is of another width than the table's, or holds a NaN
// or an infinity, is written and left out of the mirror; one of another width
// is found by the search in Go. pgvector refuses such a vector, and inside a
// batch the refusal would abort the transaction: the whole batch, and on
// ExecuteBatch its edges, would be lost.
func TestANodeOfAnotherWidthIsWrittenAndFoundInGo(t *testing.T) {
	g, db := mirrorGraph(t)
	ctx := context.Background()
	for _, write := range []struct {
		name string
		run  func(nodes []*GraphNode) (*BatchResult, error)
	}{
		{"UpsertNodesBatch", func(nodes []*GraphNode) (*BatchResult, error) { return g.UpsertNodesBatch(ctx, nodes) }},
		{"ExecuteBatch", func(nodes []*GraphNode) (*BatchResult, error) {
			return g.ExecuteBatch(ctx, &BatchGraphOperation{NodeUpserts: nodes,
				EdgeUpserts: []*GraphEdge{{ID: nodes[0].ID + "-e", FromNodeID: nodes[0].ID, ToNodeID: nodes[1].ID, EdgeType: "rel"}}})
		}},
	} {
		nodes := append(wideNodes(write.name+"-four", 2, 4, 1), wideNodes(write.name+"-three", 1, 3, 2)[0], wideNodes(write.name+"-five", 1, 5, 3)[0],
			&GraphNode{ID: write.name + "-nan", NodeType: "thing", Vector: []float32{float32(math.NaN()), 0, 0, 0}},
			&GraphNode{ID: write.name + "-inf", NodeType: "thing", Vector: []float32{0, float32(math.Inf(-1)), 0, 0}})
		res, err := write.run(nodes)
		if err != nil || res.FailedCount != 0 {
			t.Fatalf("%s: %+v %v", write.name, res, err)
		}
		for _, n := range nodes {
			if got, err := g.GetNode(ctx, n.ID); err != nil || got == nil {
				t.Fatalf("%s: %s not written: %v", write.name, n.ID, err)
			}
		}
		if missing, stale := unmirrored(t, db, 4); len(missing)+len(stale) > 0 {
			t.Fatalf("%s: missing %v, stale %v", write.name, missing, stale)
		}
		// A NaN or infinite vector scores NaN against any query in Go too, so
		// only the finite ones can be found by their own vector.
		if got := selfFound(t, g, nodes[:4]); got != 4 {
			t.Fatalf("%s: %d of 4 nodes found by their own vector", write.name, got)
		}
	}
	// A node rewritten at another width, or with a NaN at the table's width,
	// leaves the mirror, alone or in a batch.
	if err := g.UpsertNode(ctx, &GraphNode{ID: "UpsertNodesBatch-four0", NodeType: "thing", Vector: []float32{1, 0, 0}}); err != nil {
		t.Fatal(err)
	}
	if res, err := g.UpsertNodesBatch(ctx, []*GraphNode{{ID: "UpsertNodesBatch-four1", NodeType: "thing",
		Vector: []float32{0, float32(math.NaN()), 0, 0}}}); err != nil || res.FailedCount != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if missing, stale := unmirrored(t, db, 4); len(missing)+len(stale) > 0 {
		t.Fatalf("after the rewrites: missing %v, stale %v", missing, stale)
	}
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM graph_node_vectors WHERE node_id IN ('UpsertNodesBatch-four0', 'UpsertNodesBatch-four1')`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("%d mirror rows left for the rewritten nodes, %v", rows, err)
	}
	// A query pgvector would refuse, of the table's width but not finite, is
	// scored in Go rather than failing the search.
	if _, err := g.HybridSearch(ctx, &HybridQuery{Vector: []float32{float32(math.Inf(1)), 0, 0, 0}, TopK: 5}); err != nil {
		t.Fatalf("a non-finite query: %v", err)
	}
}

// A store opened without a dimension makes its table at 64, the width of
// the RDF term vectors, whatever the embedder writes. With an 8-wide
// embedder, every graph search failed ("expected 64 dimensions, not 8"), and
// a batch that mirrored its nodes would have failed with it. Writes and
// searches of another width now pass the table by.
func TestAStoreWithoutADimensionWritesAndSearchesOtherWidths(t *testing.T) {
	db := pgtest.Open(t, "graph_mirror")
	if db == nil {
		t.Skip(pgtest.EnvDSN + " is unset — the PostgreSQL vector mirror is NOT covered by this run")
	}
	g := openMirror(t, db, 0)
	if g.vecCap.Dimensions != 64 {
		t.Fatalf("table width %d, want 64", g.vecCap.Dimensions)
	}
	ctx := context.Background()
	nodes := wideNodes("n", 20, 8, 1)
	if res, err := g.UpsertNodesBatch(ctx, nodes[:19]); err != nil || res.FailedCount != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if err := g.UpsertNode(ctx, nodes[19]); err != nil {
		t.Fatal(err)
	}
	if got := selfFound(t, g, nodes); got != 20 {
		t.Fatalf("%d of 20 nodes found by their own vector", got)
	}
}

// The table keeps the width it was made at, and a store reopened with another
// configured dimension reads it from the column: its vectors of the new
// width pass the table by rather than fail in it.
func TestATableKeepsTheWidthItWasMadeAt(t *testing.T) {
	g, db := mirrorGraph(t)
	ctx := context.Background()
	if _, err := g.UpsertNodesBatch(ctx, wideNodes("four", 5, 4, 1)); err != nil {
		t.Fatal(err)
	}
	reopened := openMirror(t, db, 8)
	if reopened.vecCap.Dimensions != 4 {
		t.Fatalf("width %d after reopening at 8, want the column's 4", reopened.vecCap.Dimensions)
	}
	eight := wideNodes("eight", 5, 8, 2)
	if res, err := reopened.UpsertNodesBatch(ctx, eight); err != nil || res.FailedCount != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := selfFound(t, reopened, eight); got != 5 {
		t.Fatalf("%d of 5 nodes of the new width found by their own vector", got)
	}
}

// A store written before batches kept the mirror is mirrored when it is
// opened: its nodes have their vectors in graph_nodes and no mirror row.
func TestAStoreWrittenWithoutTheMirrorIsMirroredWhenOpened(t *testing.T) {
	old := mirrorBackfillPage
	mirrorBackfillPage = 7 // several pages for 30 nodes
	defer func() { mirrorBackfillPage = old }()

	g, db := mirrorGraph(t)
	ctx := context.Background()
	nodes := mirrorNodes("n", 30, 1)
	nodes[3].Vector = nil
	if _, err := g.UpsertNodesBatch(ctx, nodes); err != nil {
		t.Fatal(err)
	}
	// What an earlier version left: nodes with vectors and no mirror, and one
	// whose stored vector the mirror column cannot take, which stays out.
	bad, err := encoding.EncodeVector([]float32{1, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE graph_nodes SET vector = $1 WHERE id = 'n5'`, bad); err != nil {
		t.Fatal(err)
	}
	// A NaN an earlier version wrote without an error: pgvector refuses it,
	// and the pass must step over it rather than stop there on every open.
	nan, err := encoding.EncodeVector([]float32{0, float32(math.NaN()), 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE graph_nodes SET vector = $1 WHERE id = 'n1'`, nan); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM graph_node_vectors`); err != nil {
		t.Fatal(err)
	}

	reopened := NewGraphStoreOn(db, sqldialect.For(sqldialect.Postgres), testHost{cfg: g.store.Config()})
	if err := reopened.InitGraphSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if missing, stale := unmirrored(t, db, 4); len(missing)+len(stale) > 0 {
		t.Fatalf("after reopening: missing %v, stale %v", missing, stale)
	}
	if err := reopened.UpsertNode(ctx, mirrorNodes("alone", 1, 2)[0]); err != nil {
		t.Fatal(err)
	}
	var withVectors []*GraphNode
	for _, n := range nodes {
		if n.Vector != nil && n.ID != "n5" && n.ID != "n1" {
			withVectors = append(withVectors, n)
		}
	}
	if got := selfFound(t, reopened, withVectors); got != len(withVectors) {
		t.Fatalf("%d of %d nodes found by their own vector after reopening", got, len(withVectors))
	}
}

// The nodes a triple writes for its terms have vectors, and on PostgreSQL
// they reach the mirror like any other node.
func TestRDFTermNodesAreMirroredOnPostgres(t *testing.T) {
	g, db := mirrorGraph(t)
	ctx := context.Background()
	if err := g.UpsertTriple(ctx, &RDFTriple{Subject: NewIRI("urn:s"), Predicate: NewIRI("urn:p"), Object: NewLiteral("o")}); err != nil {
		t.Fatal(err)
	}
	missing, stale := unmirrored(t, db, 4)
	if len(missing)+len(stale) > 0 {
		t.Fatalf("missing %v, stale %v", missing, stale)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM graph_node_vectors`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("%d mirror rows, %v; want 2", n, err)
	}
}

// A table too wide to index is still a mirror, and the pass at open fills it.
func TestAnUnindexedTableIsMirroredWhenOpened(t *testing.T) {
	db := pgtest.Open(t, "graph_mirror")
	if db == nil {
		t.Skip(pgtest.EnvDSN + " is unset — the PostgreSQL vector mirror is NOT covered by this run")
	}
	g := openMirror(t, db, pgvectorMaxIndexedDims+1)
	if g.vecCap.Indexed {
		t.Fatal("an index on a table wider than pgvector indexes")
	}
	ctx := context.Background()
	if _, err := g.UpsertNodesBatch(ctx, wideNodes("n", 3, pgvectorMaxIndexedDims+1, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM graph_node_vectors`); err != nil {
		t.Fatal(err)
	}
	openMirror(t, db, pgvectorMaxIndexedDims+1)
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM graph_node_vectors`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("%d mirror rows after reopening, %v; want 3", n, err)
	}
}

// A table made by an earlier version with a column of no width takes any
// width; the store's configured dimension then keeps it to one, so a batch
// of another width is written and left out rather than mixed in.
func TestAColumnWithoutAWidthKeepsTheConfiguredOne(t *testing.T) {
	db := pgtest.Open(t, "graph_mirror")
	if db == nil {
		t.Skip(pgtest.EnvDSN + " is unset — the PostgreSQL vector mirror is NOT covered by this run")
	}
	ctx := context.Background()
	cfg := core.DefaultConfig()
	cfg.VectorDim = 4
	g := NewGraphStoreOn(db, sqldialect.For(sqldialect.Postgres), testHost{cfg: cfg})
	if err := g.createGraphSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE graph_node_vectors (node_id TEXT PRIMARY KEY REFERENCES graph_nodes(id) ON DELETE CASCADE, embedding vector NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := g.InitGraphSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if g.vecCap.Dimensions != 4 {
		t.Fatalf("width %d on a column without one, want the configured 4", g.vecCap.Dimensions)
	}
	nodes := append(wideNodes("four", 3, 4, 1), wideNodes("three", 2, 3, 2)...)
	if res, err := g.UpsertNodesBatch(ctx, nodes); err != nil || res.FailedCount != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if missing, stale := unmirrored(t, db, 4); len(missing)+len(stale) > 0 {
		t.Fatalf("missing %v, stale %v", missing, stale)
	}
	if got := selfFound(t, g, nodes); got != len(nodes) {
		t.Fatalf("%d of %d nodes found by their own vector", got, len(nodes))
	}
}
