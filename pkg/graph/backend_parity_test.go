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
