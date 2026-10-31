package graph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/pgtest"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// The nearest nodes come back as many as asked for. vectorCandidates asks for
// at least 50, and through the HNSW index a scan stopped at hnsw.ef_search
// rows, 40 unless set: every graph search on PostgreSQL started from 40.
func TestPgVectorCandidatesReturnTheirLimit(t *testing.T) {
	db := pgtest.Open(t, "graph_nearest")
	if db == nil {
		t.Skip(pgtest.EnvDSN + " is unset — the nearest-node count on PostgreSQL is NOT covered by this run")
	}
	cfg := core.DefaultConfig()
	cfg.VectorDim = 16
	g := NewGraphStoreOn(db, sqldialect.For(sqldialect.Postgres), testHost{cfg: cfg})
	ctx := context.Background()
	if err := g.InitGraphSchema(ctx); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRowContext(ctx, "SELECT current_setting('server_version_num')::int").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version < 150000 {
		t.Skipf("PostgreSQL %d has no pg_stat_force_next_flush — the nearest-node count is NOT covered by this run", version)
	}
	if !g.vecCap.Indexed {
		t.Fatalf("no index on graph_node_vectors: %s", g.vecCap.Reason)
	}

	r := rand.New(rand.NewSource(1))
	nodes := make([]*GraphNode, 1500)
	for i := range nodes {
		v := make([]float32, 16)
		for j := range v {
			v[j] = float32(r.NormFloat64())
		}
		nodes[i] = &GraphNode{ID: fmt.Sprintf("n%d", i), NodeType: "thing", Content: "x", Vector: v}
	}
	// One at a time: UpsertNode is the write that mirrors the vector into
	// graph_node_vectors.
	for _, n := range nodes {
		if err := g.UpsertNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}

	// One connection, on which the planner may not sort when the index can
	// serve the order.
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "SET enable_sort = off"); err != nil {
		t.Fatal(err)
	}
	// The scans of the HNSW index PostgreSQL has counted. This connection's
	// counts are flushed when a transaction ends, and only if asked for.
	indexScans := func() int64 {
		t.Helper()
		if _, err := db.ExecContext(ctx, "SELECT pg_stat_force_next_flush()"); err != nil {
			t.Fatal(err)
		}
		var n int64
		if err := db.QueryRowContext(ctx, `SELECT idx_scan FROM pg_stat_user_indexes
			WHERE indexrelname = 'idx_graph_node_vectors_cos' AND schemaname = current_schema()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	q := &HybridQuery{Vector: nodes[7].Vector, TopK: 10}
	for _, limit := range []int{10, 50, 1001} {
		before := indexScans()
		got, err := g.pgVectorCandidates(ctx, q, limit)
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		// Up to 1000, the most hnsw.ef_search allows, the index answers.
		if used := indexScans() > before; used != (limit <= 1000) {
			t.Errorf("limit %d: HNSW index read: %v", limit, used)
		}
		if len(got) != limit {
			t.Fatalf("limit %d: %d nodes", limit, len(got))
		}
		if got[0].ID != "n7" {
			t.Fatalf("limit %d: the node searched for by its own vector came back as %s", limit, got[0].ID)
		}
	}
	// A vector of another dimension is refused by pgvector, and the refusal
	// comes back rather than an empty list.
	if _, err := g.pgVectorCandidates(ctx, &HybridQuery{Vector: []float32{1, 0}}, 10); err == nil {
		t.Fatal("a 2-dimension vector against 16-dimension nodes was not refused")
	}
}

// The nearest nodes are read in a transaction of their own; with the context
// done, the read stops there with its error. No database is needed.
func TestPgVectorCandidatesStopWithTheirContext(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://nobody@127.0.0.1:1/none")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	g := NewGraphStoreOn(db, sqldialect.For(sqldialect.Postgres), testHost{cfg: core.DefaultConfig()})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.pgVectorCandidates(ctx, &HybridQuery{Vector: []float32{1, 0}}, 50); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v, want the context's error", err)
	}
}
