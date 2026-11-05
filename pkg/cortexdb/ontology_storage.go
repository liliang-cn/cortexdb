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

// History. ontology_schemas_v2 holds only the CURRENT row of each schema, and
// every save overwrote its definition, so an earlier version was gone the
// moment the next one was saved. Two append-only tables, written in the same
// transaction as the row they describe, keep what that overwrote:
//
//   - ontology_schema_versions: every saved version in full (definition,
//     name, metadata, who saved it and when) plus a computed summary of what
//     changed against the version before it.
//   - ontology_activations: the intervals during which a schema (at a
//     version) was THE active validator, and who switched it. Saving an
//     active schema closes the interval of its previous version and opens one
//     for the new version at the same instant, so an instant always resolves
//     to the definition that validated writes then.
//
// Deleting a schema deletes its current row and closes its open interval, and
// nothing else: its versions and intervals stay readable through
// ontology_history and ontology_get(version / as_of). The only call that
// destroys history in CortexDB is vacuum_graph. Saving a schema again under a
// deleted id continues the numbering from its history rather than restarting
// at 1, so (schema_id, version) never names two different definitions.
//
// Reverting is a save of an old definition: it appends a new version equal to
// the old one, like git revert. History is never rewritten.
//
// The hot path is untouched: loadActiveOntologySchema still reads the one
// current row and never joins either history table.

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
	if err := db.ensureOntologyHistoryTables(ctx); err != nil {
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
	// The history table is consulted as well as the live row: a schema that
	// was deleted and is being saved again has no live row, and numbering it
	// from 1 would collide with the versions its history already holds.
	version := 1
	if existing != nil {
		version = existing.Version + 1
	}
	if latest, err := db.latestOntologyVersionTx(ctx, tx, schema.SchemaID); err != nil {
		return nil, err
	} else if latest >= version {
		version = latest + 1
	}
	now := ontologyHistoryNow()

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

	previous := existing
	if previous == nil {
		if previous, err = db.getOntologyVersionSchemaTx(ctx, tx, schema.SchemaID, version-1); err != nil && !errors.Is(err, core.ErrNotFound) {
			return nil, err
		}
	}
	summary := summarizeOntologyChange(previous, schema, string(definitionJSON))
	if err := db.recordOntologyVersionTx(ctx, tx, schema.SchemaID, version, name, strings.TrimSpace(schema.Description),
		schema.StrictActions, string(metadataJSON), string(definitionJSON), now, writerOf(ctx), summary); err != nil {
		return nil, err
	}
	if err := db.recordOntologyActivationTx(ctx, tx, schema.SchemaID, version,
		existing != nil && existing.Active, active, now, writerOf(ctx)); err != nil {
		return nil, err
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
	tx, err := db.store.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin ontology transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := db.getOntologySchemaTx(ctx, tx, schemaID)
	if errors.Is(err, core.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Only the live row goes. The versions and activation intervals stay: they
	// are the record of what this schema said and when it was in force.
	if existing.Active {
		if err := db.closeOntologyActivationsTx(ctx, tx, schemaID, false, ontologyHistoryNow(), writerOf(ctx), ontologyEndDeleted); err != nil {
			return false, err
		}
	}
	if _, err := db.txExec(ctx, tx, `DELETE FROM ontology_schemas_v2 WHERE id = ?`, schemaID); err != nil {
		return false, fmt.Errorf("delete ontology schema: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit ontology delete: %w", err)
	}
	return true, nil
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

	return buildOntologySchema(&schema, description, strictActions != 0, metadataJSON, definitionRaw,
		isActive != 0, createdAt, updatedAt)
}

// buildOntologySchema fills schema from the stored columns; the live table
// and the version table share it so a version decodes exactly as the live row
// does.
func buildOntologySchema(schema *OntologySchema, description sql.NullString, strictActions bool,
	metadataJSON sql.NullString, definitionRaw string, active bool, createdAt, updatedAt time.Time) (*OntologySchema, error) {
	schema.Description = description.String
	schema.StrictActions = strictActions
	schema.Active = active
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
	return schema, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
