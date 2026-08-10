package cortexdb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func openOntologyTestDB(t *testing.T) *DB {
	t.Helper()
	dbPath := fmt.Sprintf("test_ontology_v2_%d.db", time.Now().UnixNano())
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	})
	return db
}

func TestSaveAndGetOntologySchema(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := validAviationSchema()
	saved, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.Schema.Version != 1 {
		t.Fatalf("expected version 1, got %d", saved.Schema.Version)
	}
	if !saved.Schema.Active {
		t.Fatal("expected schema to be active")
	}

	got, err := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Schema.ObjectTypes) != 2 {
		t.Fatalf("expected 2 object types, got %d", len(got.Schema.ObjectTypes))
	}
	if got.Schema.ObjectTypes[0].APIName != "Airport" {
		t.Fatalf("casing not preserved through storage: %q", got.Schema.ObjectTypes[0].APIName)
	}
	if got.Schema.LinkTypes[0].B.Cardinality != OntologyCardinalityOne {
		t.Fatalf("cardinality not preserved: %q", got.Schema.LinkTypes[0].B.Cardinality)
	}
}

func TestSaveOntologySchemaRejectsInvalidSchema(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := validAviationSchema()
	schema.ObjectTypes[0].PrimaryKey = ""

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema}); err == nil {
		t.Fatal("expected invalid schema to be rejected at save time")
	}
}

func TestSaveOntologySchemaAutoIncrementsVersion(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: validAviationSchema()}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	second, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: validAviationSchema()})
	if err != nil {
		t.Fatalf("second save: %v", err)
	}
	if second.Schema.Version != 2 {
		t.Fatalf("expected version 2, got %d", second.Schema.Version)
	}
}

func TestActivatingOneSchemaDeactivatesOthers(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	first := validAviationSchema()
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: first, Activate: true}); err != nil {
		t.Fatalf("save first: %v", err)
	}

	second := validAviationSchema()
	second.SchemaID = "aviation-2"
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: second, Activate: true}); err != nil {
		t.Fatalf("save second: %v", err)
	}

	listed, err := db.ListOntologySchemas(ctx, OntologyListRequest{ActiveOnly: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed.Schemas) != 1 {
		t.Fatalf("expected exactly 1 active schema, got %d", len(listed.Schemas))
	}
	if listed.Schemas[0].SchemaID != "aviation-2" {
		t.Fatalf("expected aviation-2 active, got %q", listed.Schemas[0].SchemaID)
	}
}

// Ported from the v1 suite: nothing else covers the explicit Deactivate path.
func TestOntologySchemaCanDeactivateExplicitly(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := validAviationSchema()
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("save active schema: %v", err)
	}
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Deactivate: true}); err != nil {
		t.Fatalf("deactivate schema: %v", err)
	}

	listed, err := db.ListOntologySchemas(ctx, OntologyListRequest{ActiveOnly: true})
	if err != nil {
		t.Fatalf("list active schemas after deactivate: %v", err)
	}
	if len(listed.Schemas) != 0 {
		t.Fatalf("expected no active schema after deactivate, got %+v", listed.Schemas)
	}
}

func TestDeleteOntologySchema(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: validAviationSchema()}); err != nil {
		t.Fatalf("save: %v", err)
	}
	deleted, err := db.DeleteOntologySchema(ctx, OntologyDeleteRequest{SchemaID: "aviation"})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !deleted.Deleted {
		t.Fatal("expected Deleted=true")
	}
	if _, err := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation"}); err == nil {
		t.Fatal("expected get after delete to fail")
	}
}

func TestOntologyV2UsesItsOwnTable(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	sql := db.store.GetDB()

	// Stand in for a database written by the v1 code, so the assertion below
	// is about leaving old rows alone rather than merely about v2 existing.
	if _, err := sql.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS graph_ontology_schemas (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, version INTEGER NOT NULL DEFAULT 1,
			description TEXT, metadata TEXT, definition TEXT NOT NULL,
			is_active INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		t.Fatalf("create v1 table: %v", err)
	}
	if _, err := sql.ExecContext(ctx,
		`INSERT INTO graph_ontology_schemas (id, name, version, description, metadata, definition, is_active)
		 VALUES ('aviation', 'v1 aviation', 7, 'legacy', '{}', '{"entity_types":[{"name":"airport"}]}', 1)`); err != nil {
		t.Fatalf("seed v1 row: %v", err)
	}

	// Same schema ID as the v1 row, so a v2 write that reused the old table
	// would overwrite it.
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: validAviationSchema(), Activate: true}); err != nil {
		t.Fatalf("save: %v", err)
	}

	var count int
	if err := sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='ontology_schemas_v2'`).Scan(&count); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 1 {
		t.Fatal("expected v2 schemas to live in ontology_schemas_v2")
	}

	var (
		name       string
		version    int
		definition string
		isActive   int
	)
	if err := sql.QueryRowContext(ctx,
		`SELECT name, version, definition, is_active FROM graph_ontology_schemas WHERE id = 'aviation'`).
		Scan(&name, &version, &definition, &isActive); err != nil {
		t.Fatalf("read v1 row back: %v", err)
	}
	if name != "v1 aviation" || version != 7 || isActive != 1 {
		t.Fatalf("v1 row was modified: name=%q version=%d active=%d", name, version, isActive)
	}
	if definition != `{"entity_types":[{"name":"airport"}]}` {
		t.Fatalf("v1 definition was rewritten: %s", definition)
	}

	var v1Rows int
	if err := sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_ontology_schemas`).Scan(&v1Rows); err != nil {
		t.Fatalf("count v1 rows: %v", err)
	}
	if v1Rows != 1 {
		t.Fatalf("v2 wrote into the v1 table: %d rows", v1Rows)
	}
}

func TestSaveOntologySchemaAdvancesVersionOnReadModifyWrite(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: validAviationSchema()}); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// The normal way to edit a schema: read it, change it, write it back. The
	// schema that comes back from Get carries a populated Version.
	for expected := 2; expected <= 4; expected++ {
		got, err := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation"})
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		edited := got.Schema
		edited.Description = fmt.Sprintf("revision %d", expected)

		saved, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: edited})
		if err != nil {
			t.Fatalf("save: %v", err)
		}
		if saved.Schema.Version != expected {
			t.Fatalf("expected version %d after read-modify-write, got %d", expected, saved.Schema.Version)
		}
	}
}

func TestSaveOntologySchemaRejectsActivateAndDeactivate(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	_, err := db.SaveOntologySchema(ctx, OntologySaveRequest{
		Schema:     validAviationSchema(),
		Activate:   true,
		Deactivate: true,
	})
	if err == nil {
		t.Fatal("expected activate+deactivate to be rejected")
	}
	if !strings.Contains(err.Error(), "cannot both be true") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSaveOntologySchemaTrimsSchemaID(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	padded := validAviationSchema()
	padded.SchemaID = "  aviation  "
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: padded}); err != nil {
		t.Fatalf("save padded: %v", err)
	}
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: validAviationSchema()}); err != nil {
		t.Fatalf("save trimmed: %v", err)
	}

	listed, err := db.ListOntologySchemas(ctx, OntologyListRequest{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed.Schemas) != 1 {
		t.Fatalf("padding created a second schema: %+v", listed.Schemas)
	}
	if listed.Schemas[0].SchemaID != "aviation" {
		t.Fatalf("expected the trimmed id, got %q", listed.Schemas[0].SchemaID)
	}
}

func TestStrictActionsRoundTrips(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := validAviationSchema()
	schema.StrictActions = true
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Schema.StrictActions {
		t.Fatal("strict_actions did not round-trip")
	}
}
