package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// ensureOntologySchemaTable creates the v2 table. The v1 table
// graph_ontology_schemas is deliberately left alone: v2 neither reads nor
// writes nor drops it, so an upgrade cannot corrupt or lose old rows.
func (db *DB) ensureOntologySchemaTable(ctx context.Context) error {
	db.ontologySchemaMu.Lock()
	defer db.ontologySchemaMu.Unlock()
	if db.ontologySchemaReady {
		return nil
	}

	schema := `
	CREATE TABLE IF NOT EXISTS ontology_schemas_v2 (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		version INTEGER NOT NULL DEFAULT 1,
		description TEXT,
		strict_actions INTEGER NOT NULL DEFAULT 0,
		metadata TEXT,
		definition TEXT NOT NULL,
		is_active INTEGER NOT NULL DEFAULT 0,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_ontology_schemas_v2_active ON ontology_schemas_v2(is_active);
	`
	if _, err := db.exec(ctx, schema); err != nil {
		// Deliberately not latched: a transient failure such as a cancelled
		// context must not permanently disable ontology storage.
		return err
	}
	db.ontologySchemaReady = true
	return nil
}

// ontologySchemaDefinition is the JSON blob stored in the definition column.
// The scalar columns above are duplicated here only where they are cheap;
// everything structural lives in this blob so adding type kinds needs no
// migration.
type ontologySchemaDefinition struct {
	// Enforcement rides in the blob rather than a column: nothing filters or
	// sorts on it, and the blob needs no migration.
	Enforcement      OntologyEnforcement      `json:"enforcement,omitempty"`
	ObjectTypes      []OntologyObjectType     `json:"object_types,omitempty"`
	LinkTypes        []OntologyLinkType       `json:"link_types,omitempty"`
	InterfaceTypes   []OntologyInterfaceType  `json:"interface_types,omitempty"`
	SharedProperties []OntologyProperty       `json:"shared_properties,omitempty"`
	ActionTypes      []OntologyActionType     `json:"action_types,omitempty"`
	ObjectSets       []OntologyNamedObjectSet `json:"object_sets,omitempty"`
}

func (db *DB) saveOntologySchemaRecord(ctx context.Context, req OntologySaveRequest) (*OntologySchema, error) {
	schema := req.Schema
	// Trim before validating and storing: the ID is the primary key, so
	// " aviation" would otherwise sit alongside "aviation" as a separate row
	// that no caller could tell apart.
	schema.SchemaID = strings.TrimSpace(schema.SchemaID)
	if req.Activate && req.Deactivate {
		return nil, fmt.Errorf("activate and deactivate cannot both be true")
	}
	if err := validateOntologySchema(schema); err != nil {
		return nil, err
	}
	if err := db.ensureOntologySchemaTable(ctx); err != nil {
		return nil, fmt.Errorf("init ontology schema table: %w", err)
	}

	// The unexpanded schema is what gets stored: shared properties exist to be
	// managed centrally, so expanding them at write time would freeze today's
	// definition and make later edits to it invisible.
	definitionJSON, err := json.Marshal(ontologySchemaDefinition{
		Enforcement:      schema.Enforcement,
		ObjectTypes:      schema.ObjectTypes,
		LinkTypes:        schema.LinkTypes,
		InterfaceTypes:   schema.InterfaceTypes,
		SharedProperties: schema.SharedProperties,
		ActionTypes:      schema.ActionTypes,
		ObjectSets:       schema.ObjectSets,
	})
	if err != nil {
		return nil, fmt.Errorf("encode ontology definition: %w", err)
	}
	metadataJSON, err := json.Marshal(cloneStringMap(schema.Metadata))
	if err != nil {
		return nil, fmt.Errorf("encode ontology metadata: %w", err)
	}

	tx, err := db.store.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin ontology transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := db.getOntologySchemaTx(ctx, tx, schema.SchemaID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}

	// Version is always derived, never taken from the request. A schema that
	// came back from Get carries a populated Version, so honouring it would
	// pin the version forever on the read-modify-write round trip that is the
	// normal way to edit a schema. Explicit versioning, if it is ever wanted,
	// belongs on the request as an ExpectedVersion precondition.
	version := 1
	if existing != nil {
		version = existing.Version + 1
	}

	active := false
	if existing != nil {
		active = existing.Active
	}
	if req.Activate {
		active = true
	}
	if req.Deactivate {
		active = false
	}
	if active {
		if _, err := db.txExec(ctx, tx,
			`UPDATE ontology_schemas_v2 SET is_active = 0, updated_at = CURRENT_TIMESTAMP WHERE is_active = 1 AND id <> ?`,
			schema.SchemaID); err != nil {
			return nil, fmt.Errorf("deactivate ontology schemas: %w", err)
		}
	}

	name := strings.TrimSpace(schema.Name)
	if name == "" {
		name = schema.SchemaID
	}

	if _, err := db.txExec(ctx, tx, `
		INSERT INTO ontology_schemas_v2 (id, name, version, description, strict_actions, metadata, definition, is_active, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			version = excluded.version,
			description = excluded.description,
			strict_actions = excluded.strict_actions,
			metadata = excluded.metadata,
			definition = excluded.definition,
			is_active = excluded.is_active,
			updated_at = CURRENT_TIMESTAMP
	`, schema.SchemaID, name, version, strings.TrimSpace(schema.Description),
		boolToInt(schema.StrictActions), string(metadataJSON), string(definitionJSON),
		boolToInt(active)); err != nil {
		return nil, fmt.Errorf("upsert ontology schema: %w", err)
	}

	// Read back inside the transaction. Reading after the commit would race a
	// concurrent writer and could return someone else's version as if it were
	// the one this call just wrote.
	saved, err := db.getOntologySchemaTx(ctx, tx, schema.SchemaID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit ontology schema: %w", err)
	}
	return saved, nil
}

const ontologySchemaColumns = `id, name, version, description, strict_actions, metadata, definition, is_active, created_at, updated_at`

func (db *DB) loadOntologySchema(ctx context.Context, schemaID string) (*OntologySchema, error) {
	schemaID = strings.TrimSpace(schemaID)
	if schemaID == "" {
		return nil, fmt.Errorf("schema_id is required")
	}
	if err := db.ensureOntologySchemaTable(ctx); err != nil {
		return nil, fmt.Errorf("init ontology schema table: %w", err)
	}
	return db.getOntologySchemaTx(ctx, db.store.GetDB(), schemaID)
}

func (db *DB) listOntologySchemaRecords(ctx context.Context, activeOnly bool) ([]OntologySchema, error) {
	if err := db.ensureOntologySchemaTable(ctx); err != nil {
		return nil, fmt.Errorf("init ontology schema table: %w", err)
	}

	query := `SELECT ` + ontologySchemaColumns + ` FROM ontology_schemas_v2`
	if activeOnly {
		query += ` WHERE is_active = 1`
	}
	query += ` ORDER BY id`

	rows, err := db.query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list ontology schemas: %w", err)
	}
	defer func() { _ = rows.Close() }()

	schemas := make([]OntologySchema, 0)
	for rows.Next() {
		schema, err := scanOntologySchema(rows)
		if err != nil {
			return nil, err
		}
		schemas = append(schemas, *schema)
	}
	return schemas, rows.Err()
}

func (db *DB) deleteOntologySchemaRecord(ctx context.Context, schemaID string) (bool, error) {
	schemaID = strings.TrimSpace(schemaID)
	if schemaID == "" {
		return false, fmt.Errorf("schema_id is required")
	}
	if err := db.ensureOntologySchemaTable(ctx); err != nil {
		return false, fmt.Errorf("init ontology schema table: %w", err)
	}
	result, err := db.exec(ctx, `DELETE FROM ontology_schemas_v2 WHERE id = ?`, schemaID)
	if err != nil {
		return false, fmt.Errorf("delete ontology schema: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete ontology schema rows: %w", err)
	}
	return affected > 0, nil
}

func (db *DB) loadActiveOntologySchema(ctx context.Context) (*OntologySchema, error) {
	schemas, err := db.listOntologySchemaRecords(ctx, true)
	if err != nil {
		return nil, err
	}
	if len(schemas) == 0 {
		return nil, nil
	}
	return &schemas[0], nil
}

type ontologyRowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type ontologyRowScanner interface {
	Scan(dest ...any) error
}

func (db *DB) getOntologySchemaTx(ctx context.Context, querier ontologyRowQuerier, schemaID string) (*OntologySchema, error) {
	row := db.querierQueryRow(ctx, querier,
		`SELECT `+ontologySchemaColumns+` FROM ontology_schemas_v2 WHERE id = ?`, schemaID)
	schema, err := scanOntologySchema(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, core.ErrNotFound
	}
	return schema, err
}

func scanOntologySchema(scanner ontologyRowScanner) (*OntologySchema, error) {
	var (
		schema        OntologySchema
		description   sql.NullString
		metadataJSON  sql.NullString
		definitionRaw string
		strictActions int
		isActive      int
		createdAt     time.Time
		updatedAt     time.Time
	)
	if err := scanner.Scan(&schema.SchemaID, &schema.Name, &schema.Version, &description,
		&strictActions, &metadataJSON, &definitionRaw, &isActive, &createdAt, &updatedAt); err != nil {
		return nil, err
	}

	schema.Description = description.String
	schema.StrictActions = strictActions != 0
	schema.Active = isActive != 0
	schema.CreatedAt = createdAt
	schema.UpdatedAt = updatedAt

	if metadataJSON.Valid && metadataJSON.String != "" {
		if err := json.Unmarshal([]byte(metadataJSON.String), &schema.Metadata); err != nil {
			return nil, fmt.Errorf("decode ontology metadata: %w", err)
		}
	}

	var definition ontologySchemaDefinition
	if err := json.Unmarshal([]byte(definitionRaw), &definition); err != nil {
		return nil, fmt.Errorf("decode ontology definition: %w", err)
	}
	schema.Enforcement = definition.Enforcement
	schema.ObjectTypes = definition.ObjectTypes
	schema.LinkTypes = definition.LinkTypes
	schema.InterfaceTypes = definition.InterfaceTypes
	schema.SharedProperties = definition.SharedProperties
	schema.ActionTypes = definition.ActionTypes
	schema.ObjectSets = definition.ObjectSets
	return &schema, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
