package graph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// The graph layer over both databases, proved rather than asserted.
//
// One body of SQL is only cheaper than two implementations if the two really
// do behave the same, and the only thing that keeps that true over time is a
// suite that runs twice. Prose in a design document does not fail CI.
//
// PostgreSQL is opt-in through CORTEXDB_TEST_POSTGRES so `go test ./...` on a
// laptop with no database still passes; it is skipped loudly, not silently, so
// a green run cannot be mistaken for coverage it does not have.
//
//	docker run -d -e POSTGRES_PASSWORD=cortex -e POSTGRES_DB=cortex -p 47829:5432 postgres:16
//	CORTEXDB_TEST_POSTGRES='postgres://postgres:cortex@localhost:47829/cortex?sslmode=disable' go test ./pkg/graph/

type backend struct {
	name  string
	store *GraphStore
}

// testHost supplies the vector-side settings a graph store asks its host for.
// The PostgreSQL backend has no SQLiteStore behind it, which is the whole
// reason GraphStore takes an interface.
type testHost struct{ cfg core.Config }

func (h testHost) GetSimilarityFunc() core.SimilarityFunc { return core.CosineSimilarity }
func (h testHost) Config() core.Config                    { return h.cfg }

func backends(t *testing.T) []backend {
	t.Helper()
	_, sqliteGraph, cleanup := setupTestGraph(t)
	t.Cleanup(cleanup)
	out := []backend{{name: "sqlite", store: sqliteGraph}}

	dsn := os.Getenv("CORTEXDB_TEST_POSTGRES")
	if dsn == "" {
		t.Log("CORTEXDB_TEST_POSTGRES is unset — PostgreSQL parity NOT covered by this run")
		return out
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}
	// A clean slate per run: these tests assert on counts.
	for _, table := range []string{"graph_edges", "graph_nodes", "kg_triples", "kg_namespaces"} {
		if _, err := db.Exec("DROP TABLE IF EXISTS " + table + " CASCADE"); err != nil {
			t.Fatalf("reset %s: %v", table, err)
		}
	}
	t.Cleanup(func() { db.Close() })

	cfg := core.DefaultConfig()
	cfg.VectorDim = 4
	out = append(out, backend{
		name:  "postgres",
		store: NewGraphStoreOn(db, sqldialect.For(sqldialect.Postgres), testHost{cfg: cfg}),
	})
	return out
}

func TestGraphSchemaIsIdempotentOnBothBackends(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.createGraphSchema(ctx); err != nil {
				t.Fatalf("first schema creation: %v", err)
			}
			// The second call is the one that used to break: the migration's
			// ALTER TABLE ADD COLUMN hits an existing column and the error has
			// to be recognised as harmless — in this database's wording.
			if err := b.store.createGraphSchema(ctx); err != nil {
				t.Fatalf("second schema creation (the migration path): %v", err)
			}
		})
	}
}

func TestTriplesRoundTripOnBothBackends(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}

			for i := 0; i < 3; i++ {
				tr := &RDFTriple{
					Subject:   RDFTerm{Kind: RDFTermIRI, Value: fmt.Sprintf("http://ex/s%d", i)},
					Predicate: RDFTerm{Kind: RDFTermIRI, Value: "http://ex/knows"},
					Object:    RDFTerm{Kind: RDFTermIRI, Value: fmt.Sprintf("http://ex/o%d", i)},
				}
				if err := b.store.UpsertTriple(ctx, tr); err != nil {
					t.Fatalf("UpsertTriple %d: %v", i, err)
				}
			}

			// A predicate filter is a placeholder query — the thing the
			// dialect had to rewrite for PostgreSQL.
			got, err := b.store.FindTriples(ctx, TriplePattern{
				Predicate: &RDFTerm{Kind: RDFTermIRI, Value: "http://ex/knows"},
			})
			if err != nil {
				t.Fatalf("FindTriples: %v", err)
			}
			if len(got) != 3 {
				t.Fatalf("found %d triples, want 3", len(got))
			}
			for _, tr := range got {
				if tr.Predicate.Value != "http://ex/knows" {
					t.Errorf("filter leaked an unrelated predicate: %s", tr.Predicate.Value)
				}
			}
		})
	}
}

// Nearest-neighbour search must agree across backends.
//
// The point of moving top-k into SQL is speed, not different answers. If
// PostgreSQL ranked these differently from the in-process path, every result
// in the product would depend on where it was deployed.
func TestNearestNeighboursAgreeAcrossBackends(t *testing.T) {
	// Four nodes on a line, so "closest to [1,0,0,0]" has one obvious order.
	vectors := map[string][]float32{
		"near":   {1, 0, 0, 0},
		"close":  {0.9, 0.1, 0, 0},
		"middle": {0.5, 0.5, 0, 0},
		"far":    {0, 1, 0, 0},
	}
	want := []string{"near", "close", "middle", "far"}

	ranked := map[string][]string{}
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			for id, v := range vectors {
				if err := b.store.UpsertNode(ctx, &GraphNode{ID: id, Vector: v, NodeType: "point"}); err != nil {
					t.Fatalf("UpsertNode %s: %v", id, err)
				}
			}

			results, err := b.store.HybridSearch(ctx, &HybridQuery{
				Vector: []float32{1, 0, 0, 0},
				TopK:   4,
			})
			if err != nil {
				t.Fatalf("HybridSearch: %v", err)
			}
			got := make([]string, 0, len(results))
			for _, r := range results {
				got = append(got, r.Node.ID)
			}
			if len(got) < len(want) {
				t.Fatalf("got %d results (%v), want %d", len(got), got, len(want))
			}
			got = got[:len(want)]
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("ranking = %v, want %v", got, want)
				}
			}
			ranked[b.name] = got
		})
	}

	if len(ranked) == 2 && ranked["sqlite"] != nil && ranked["postgres"] != nil {
		for i := range ranked["sqlite"] {
			if ranked["sqlite"][i] != ranked["postgres"][i] {
				t.Errorf("backends disagree: sqlite %v vs postgres %v", ranked["sqlite"], ranked["postgres"])
				break
			}
		}
	}
}

// The dimension cap, stated by the code rather than discovered in production.
//
// pgvector refuses to index a `vector` column past 2000 dimensions, and the
// qwen3-embedding model on the t2m gateway is 4096. A brain configured that
// way must still work — exact search is a real answer — and must say that it
// is unindexed, because the symptom otherwise is only latency.
func TestTheDimensionCapIsReportedNotHidden(t *testing.T) {
	dsn := os.Getenv("CORTEXDB_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("CORTEXDB_TEST_POSTGRES unset")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, tc := range []struct {
		name        string
		dim         int
		wantIndexed bool
	}{
		{"768 (embeddinggemma)", 768, true},
		{"2000 (exactly at the limit)", 2000, true},
		{"4096 (qwen3-embedding)", 4096, false},
		{"0 (dimension not yet known)", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			table := fmt.Sprintf("graph_node_vectors")
			if _, err := db.Exec("DROP TABLE IF EXISTS " + table + " CASCADE"); err != nil {
				t.Fatalf("reset: %v", err)
			}
			cfg := core.DefaultConfig()
			g := NewGraphStoreOn(db, sqldialect.For(sqldialect.Postgres), testHost{cfg: cfg})
			capability := g.initPgVector(context.Background(), tc.dim)

			if !capability.Enabled {
				t.Fatalf("pgvector should be enabled: %s", capability.Reason)
			}
			if capability.Indexed != tc.wantIndexed {
				t.Errorf("Indexed = %v, want %v (reason: %q)", capability.Indexed, tc.wantIndexed, capability.Reason)
			}
			if !capability.Indexed && capability.Reason == "" {
				t.Error("unindexed with no reason given — the operator has nothing to act on")
			}
		})
	}
}
