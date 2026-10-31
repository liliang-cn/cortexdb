package graph

import (
	"bytes"
	"context"
	"encoding/binary"
	"log"
	"math"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/encoding"
)

// sqliteMirror is a SQLite graph store with a stand-in for graph_node_vectors
// and the mirror switched on. The bookkeeping that keeps the mirror is plain
// SQL both dialects run, so it is tested here without a PostgreSQL server;
// the search over the mirror is tested on PostgreSQL (pgvector_batch_test.go).
// refused is a node id the stand-in rejects, to make a mirror write fail.
func sqliteMirror(t *testing.T, refused string) *GraphStore {
	t.Helper()
	_, g, cleanup := setupTestGraph(t)
	t.Cleanup(cleanup)
	if _, err := g.db.Exec(`CREATE TABLE graph_node_vectors (
		node_id TEXT PRIMARY KEY CHECK (node_id <> '` + refused + `'), embedding TEXT)`); err != nil {
		t.Fatal(err)
	}
	g.vecCap = vectorCapability{Enabled: true, Dimensions: 4}
	return g
}

// mirrorable admits a vector of the table's width, or of any width when the
// table has none, and never an empty one or one pgvector refuses for a NaN or
// an infinity.
func TestMirrorable(t *testing.T) {
	for _, c := range []struct {
		width, dim int
		want       bool
	}{
		{4, 4, true}, {3, 4, false}, {5, 4, false}, {0, 4, false},
		{3, 0, true}, {0, 0, false},
	} {
		if got := mirrorable(make([]float32, c.width), c.dim); got != c.want {
			t.Errorf("width %d, table %d: %v", c.width, c.dim, got)
		}
	}
	nan, inf := float32(math.NaN()), float32(math.Inf(1))
	for _, v := range [][]float32{{nan, 0, 0, 0}, {0, 0, 0, inf}, {0, -inf, 0, 0}} {
		for _, dim := range []int{4, 0} {
			if mirrorable(v, dim) {
				t.Errorf("%v admitted at table width %d", v, dim)
			}
		}
	}
}

// logged runs f and returns what it logged.
func logged(f func()) string {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)
	f()
	return buf.String()
}

// mirrorRows is graph_node_vectors as node id to embedding literal.
func mirrorRows(t *testing.T, g *GraphStore) map[string]string {
	t.Helper()
	rows, err := g.db.Query(`SELECT node_id, embedding FROM graph_node_vectors`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, e string
		if err := rows.Scan(&id, &e); err != nil {
			t.Fatal(err)
		}
		out[id] = e
	}
	return out
}

// Both batch writes keep the mirror as UpsertNode does: a node with a vector
// gets its row, a node rewritten without one loses it, and a mirror write
// that fails counts as a failed node.
func TestBatchWritesKeepTheVectorMirror(t *testing.T) {
	ctx := context.Background()
	for _, write := range []struct {
		name string
		run  func(g *GraphStore, nodes []*GraphNode) *BatchResult
	}{
		{"UpsertNodesBatch", func(g *GraphStore, nodes []*GraphNode) *BatchResult {
			res, err := g.UpsertNodesBatch(ctx, nodes)
			if err != nil {
				t.Fatal(err)
			}
			return res
		}},
		{"ExecuteBatch", func(g *GraphStore, nodes []*GraphNode) *BatchResult {
			res, err := g.ExecuteBatch(ctx, &BatchGraphOperation{NodeUpserts: nodes})
			if err != nil {
				t.Fatal(err)
			}
			return res
		}},
	} {
		t.Run(write.name, func(t *testing.T) {
			g := sqliteMirror(t, "refused")
			// A node whose own write fails gets no mirror row either.
			if _, err := g.db.Exec(`CREATE TRIGGER block_node BEFORE INSERT ON graph_nodes
				WHEN NEW.id = 'blocked' BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
				t.Fatal(err)
			}
			res := write.run(g, []*GraphNode{
				{ID: "a", NodeType: "thing", Vector: []float32{1, 0, 0, 0}},
				{ID: "b", NodeType: "thing", Vector: []float32{0, 1, 0, 0}},
				{ID: "c", NodeType: "thing"},
				{ID: "three", NodeType: "thing", Vector: []float32{1, 0, 0}},
				{ID: "five", NodeType: "thing", Vector: []float32{1, 0, 0, 0, 0}},
				{ID: "blocked", NodeType: "thing", Vector: []float32{0, 0, 0, 1}},
			})
			if res.SuccessCount != 5 || res.FailedCount != 1 {
				t.Fatalf("%+v", res)
			}
			if got := mirrorRows(t, g); len(got) != 2 || got["a"] != "[1,0,0,0]" || got["b"] != "[0,1,0,0]" {
				t.Fatalf("mirror after the first batch: %v", got)
			}
			res = write.run(g, []*GraphNode{
				{ID: "a", NodeType: "thing"},
				{ID: "b", NodeType: "thing", Vector: []float32{0, 1, 0}},
				{ID: "c", NodeType: "thing", Vector: []float32{0, 0, 1, 0}},
				{ID: "refused", NodeType: "thing", Vector: []float32{0, 0, 1, 0}},
			})
			// SQLite goes on after a failed statement; PostgreSQL aborts the
			// transaction, as for a failed node insert.
			if res.SuccessCount != 3 || res.FailedCount != 1 {
				t.Fatalf("a refused mirror row was not a failed node: %+v", res)
			}
			if got := mirrorRows(t, g); len(got) != 1 || got["c"] != "[0,0,1,0]" {
				t.Fatalf("mirror after the second batch: %v", got)
			}
		})
	}
}

// UpsertNode, one node at a time, still keeps the mirror, and reports a
// mirror write that fails.
func TestUpsertNodeKeepsTheVectorMirror(t *testing.T) {
	g := sqliteMirror(t, "refused")
	ctx := context.Background()
	if err := g.UpsertNode(ctx, &GraphNode{ID: "a", NodeType: "thing", Vector: []float32{1, 0, 0, 0}}); err != nil {
		t.Fatal(err)
	}
	if got := mirrorRows(t, g); got["a"] != "[1,0,0,0]" {
		t.Fatalf("%v", got)
	}
	if err := g.UpsertNode(ctx, &GraphNode{ID: "refused", NodeType: "thing", Vector: []float32{1, 0, 0, 0}}); err == nil {
		t.Fatal("a refused mirror row was not reported")
	}
}

// The nodes a triple writes for its terms keep the mirror too, and a mirror
// write that fails fails the triple.
func TestRDFTermNodesKeepTheVectorMirror(t *testing.T) {
	refused := rdfNodeID(NewIRI("urn:refused"))
	g := sqliteMirror(t, refused)
	g.vecCap.Dimensions = g.rdfVectorDim() // a term's vector is this wide
	ctx := context.Background()
	s, o := NewIRI("urn:s"), NewLiteral("o")
	if err := g.UpsertTriple(ctx, &RDFTriple{Subject: s, Predicate: NewIRI("urn:p"), Object: o}); err != nil {
		t.Fatal(err)
	}
	got := mirrorRows(t, g)
	for _, term := range []RDFTerm{s, o} {
		if got[rdfNodeID(term)] == "" {
			t.Errorf("%s not mirrored: %v", rdfNodeID(term), got)
		}
	}
	if err := g.UpsertTriple(ctx, &RDFTriple{Subject: NewIRI("urn:refused"), Predicate: NewIRI("urn:p"), Object: o}); err == nil {
		t.Fatal("a refused mirror row was not reported")
	}
}

// Opening a store mirrors the stored vectors an earlier version left out,
// page by page. It reads only the vectors whose stored length is the table's
// width, skips one whose header disagrees, and leaves a row already there as
// it is.
func TestStoredVectorsAreMirroredPageByPage(t *testing.T) {
	old := mirrorBackfillPage
	mirrorBackfillPage = 3
	defer func() { mirrorBackfillPage = old }()

	g := sqliteMirror(t, "refused")
	ctx := context.Background()
	nodes := mirrorNodes("n", 9, 1)
	nodes[2].Vector = nil
	if _, err := g.UpsertNodesBatch(ctx, nodes); err != nil {
		t.Fatal(err)
	}
	three, err := encoding.EncodeVector([]float32{1, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	// 20 bytes, the length of a 4-wide vector, under a header that says 3.
	corrupt := append(binary.LittleEndian.AppendUint32(nil, 3), make([]byte, 16)...)
	five, err := encoding.EncodeVector([]float32{1, 0, 0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	for id, blob := range map[string][]byte{"n4": three, "n5": corrupt, "n6": five} {
		if _, err := g.db.Exec(`UPDATE graph_nodes SET vector = ? WHERE id = ?`, blob, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := g.db.Exec(`DELETE FROM graph_node_vectors`); err != nil {
		t.Fatal(err)
	}
	// A concurrent write gets to n8 between the pass's read and its write,
	// with a vector the pass did not read.
	if _, err := g.db.Exec(`CREATE TRIGGER concurrent_write BEFORE INSERT ON graph_node_vectors
		WHEN NEW.node_id = 'n8' AND NEW.embedding <> 'fresh'
		BEGIN INSERT INTO graph_node_vectors (node_id, embedding) VALUES ('n8', 'fresh'); END`); err != nil {
		t.Fatal(err)
	}

	mirrored, skipped, err := g.pgBackfillVectors(ctx, 4)
	if err != nil || mirrored != 4 || skipped != 1 {
		t.Fatalf("mirrored %d, skipped %d, %v; want 4, 1", mirrored, skipped, err)
	}
	got := mirrorRows(t, g)
	for _, n := range nodes {
		_, ok := got[n.ID]
		if want := n.Vector != nil && n.ID != "n4" && n.ID != "n5" && n.ID != "n6"; ok != want {
			t.Errorf("%s mirrored: %v", n.ID, ok)
		}
	}
	if got["n8"] != "fresh" {
		t.Errorf("the row a concurrent write put in first was replaced: %q", got["n8"])
	}
	// Only the corrupt row is left to read, a full page of one: the pass
	// moves past it rather than reading it again.
	mirrorBackfillPage = 1
	if mirrored, skipped, err := g.pgBackfillVectors(ctx, 4); err != nil || mirrored != 0 || skipped != 1 {
		t.Fatalf("second pass: mirrored %d, skipped %d, %v", mirrored, skipped, err)
	}
	// A pass that only skipped says so.
	if out := logged(func() { g.mirrorStoredVectors(ctx) }); !strings.Contains(out, "0 stored vectors mirrored for search, 1 corrupt or not finite skipped") {
		t.Fatalf("logged %q", out)
	}
	// With no width to keep to, the 3-wide and 5-wide vectors are taken.
	mirrorBackfillPage = 3
	if mirrored, skipped, err := g.pgBackfillVectors(ctx, 0); err != nil || mirrored != 3 || skipped != 0 {
		t.Fatalf("no width: mirrored %d, skipped %d, %v", mirrored, skipped, err)
	}
	// What the pass at open logs: nothing when there is nothing to do.
	if out := logged(func() { g.mirrorStoredVectors(ctx) }); out != "" {
		t.Fatalf("logged %q with nothing to do", out)
	}
	if _, err := g.db.Exec(`DELETE FROM graph_node_vectors WHERE node_id = 'n0'`); err != nil {
		t.Fatal(err)
	}
	if out := logged(func() { g.mirrorStoredVectors(ctx) }); !strings.Contains(out, "1 stored vectors mirrored for search, 0 corrupt or not finite skipped") {
		t.Fatalf("logged %q", out)
	}
}

// A pass that cannot read or write stops with the error.
func TestMirroringStoredVectorsStopsAtAnError(t *testing.T) {
	ctx := context.Background()

	g := sqliteMirror(t, "refused")
	if _, err := g.UpsertNodesBatch(ctx, []*GraphNode{{ID: "refused", NodeType: "thing", Vector: []float32{1, 0, 0, 0}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.pgBackfillVectors(ctx, 4); err == nil {
		t.Fatal("a refused mirror row was not reported")
	}
	if out := logged(func() { g.mirrorStoredVectors(ctx) }); !strings.Contains(out, "mirroring stored vectors for search") {
		t.Fatalf("logged %q", out)
	}

	if _, err := g.db.Exec(`DROP TABLE graph_node_vectors`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.pgBackfillVectors(ctx, 4); err == nil {
		t.Fatal("a missing mirror table was not reported")
	}
}
