package cortexdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// The whole point, in one test: a brain that lives in PostgreSQL.
//
// Everything else on this branch is a part — a dialect, a store, an index, a
// query that runs on both. This is the assembly. Until Open accepts a
// postgres:// DSN and the thing that comes back can hold a document, an
// embedding, a graph edge and a search, "the user can choose SQLite or
// pgvector" was a claim about parts.
func TestABrainCanLiveOnPostgres(t *testing.T) {
	dsn := os.Getenv("CORTEXDB_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("CORTEXDB_TEST_POSTGRES unset — a PostgreSQL brain is NOT covered by this run")
	}
	ctx := context.Background()

	// Its own schema. `go test ./...` runs packages in parallel against one
	// database, and CREATE EXTENSION / CREATE TABLE IF NOT EXISTS are not
	// atomic in PostgreSQL: two packages initialising at the same moment race
	// and one loses with "duplicate key value violates unique constraint
	// pg_type_typname_nsp_index", which names nothing a reader could act on.
	// public stays in the search_path so the vector type resolves.
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	if _, err := admin.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		admin.Close()
		t.Fatalf("extension: %v", err)
	}
	schema := fmt.Sprintf("brain_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	dsn += sep + "search_path=" + schema + ",public"

	cfg := DefaultConfig(dsn)
	cfg.Dimensions = 4
	db, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open on a postgres DSN: %v", err)
	}
	defer db.Close()

	// The DSN decided the backend, and the seam reports it.
	if got := db.Dialect().Kind(); string(got) != "postgres" {
		t.Fatalf("dialect = %s, want postgres", got)
	}

	// A document, an embedding that belongs to it, and a search that finds it.
	tools := db.GraphRAGTools()
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{
		DocumentID: "handbook",
		Title:      "Handbook",
		Content:    "Leo works at LINBIT on the LINSTOR GUI.",
	}); err != nil {
		t.Fatalf("IngestDocument: %v", err)
	}

	// A graph the brain can walk.
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "Leo", Type: "Person"},
		{Name: "LINBIT", Type: "Company"},
	}}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	rel, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
		DocumentID: "handbook",
		Relations:  []ToolRelationInput{{From: "Leo", To: "LINBIT", Type: "works_at", ChunkIDs: []string{"handbook#1"}}},
	})
	if err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}

	// And provenance, which reads the graph back through the dialect.
	prov, err := db.FactProvenanceFor(ctx, rel.EdgeIDs[0], false)
	if err != nil {
		t.Fatalf("FactProvenanceFor: %v", err)
	}
	if prov.DocumentID != "handbook" || !prov.Cited() {
		t.Errorf("provenance did not survive the round trip: %+v", prov)
	}

	// Uncited sweep runs its dialect-built SQL against PostgreSQL.
	if _, err := db.UncitedFacts(ctx, 10); err != nil {
		t.Fatalf("UncitedFacts: %v", err)
	}
}

// A backend that opens but cannot be a brain must be refused by name at Open,
// not discovered by a panic in the middle of a request.
func TestAnUnknownDSNSchemeIsRefusedAtOpen(t *testing.T) {
	if _, err := Open(DefaultConfig("")); err == nil {
		t.Error("an empty path was accepted")
	}
}
