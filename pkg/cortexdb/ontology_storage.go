package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

type ontologySchemaDefinition struct {
	EntityTypes   []OntologyEntityType   `json:"entity_types,omitempty"`
	RelationTypes []OntologyRelationType `json:"relation_types,omitempty"`
}

func (db *DB) ensureOntologySchemaTable(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS graph_ontology_schemas (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		version INTEGER NOT NULL DEFAULT 1,
		description TEXT,
		metadata TEXT,
		definition TEXT NOT NULL,
		is_active INTEGER NOT NULL DEFAULT 0,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_graph_ontology_schemas_active ON graph_ontology_schemas(is_active);
	`
	_, err := db.store.GetDB().ExecContext(ctx, schema)
	return err
}

func (db *DB) saveOntologySchemaRecord(ctx context.Context, req OntologySaveRequest) (*OntologySchema, error) {
	if err := validateOntologySaveRequest(req); err != nil {
		return nil, err
	}
	if err := db.ensureOntologySchemaTable(ctx); err != nil {
		return nil, fmt.Errorf("init ontology schema table: %w", err)
	}

	entityTypes := normalizeOntologyEntityTypes(req.EntityTypes)
	relationTypes := normalizeOntologyRelationTypes(req.RelationTypes)
	definitionJSON, err := json.Marshal(ontologySchemaDefinition{
		EntityTypes:   entityTypes,
		RelationTypes: relationTypes,
	})
	if err != nil {
		return nil, fmt.Errorf("encode ontology definition: %w", err)
	}

	metadataJSON, err := json.Marshal(cloneStringMap(req.Metadata))
	if err != nil {
		return nil, fmt.Errorf("encode ontology metadata: %w", err)
	}

	tx, err := db.store.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin ontology transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := db.getOntologySchemaTx(ctx, tx, req.SchemaID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}

	version := req.Version
	if version <= 0 {
		version = 1
		if existing != nil {
			version = existing.Version + 1
		}
	}

	active := req.Activate
	if existing != nil && !req.Activate {
		active = existing.Active
	}
	if active {
		if _, err := tx.ExecContext(ctx, `UPDATE graph_ontology_schemas SET is_active = 0, updated_at = CURRENT_TIMESTAMP WHERE is_active = 1 AND id <> ?`, req.SchemaID); err != nil {
			return nil, fmt.Errorf("deactivate ontology schemas: %w", err)
		}
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = req.SchemaID
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO graph_ontology_schemas (id, name, version, description, metadata, definition, is_active, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			version = excluded.version,
			description = excluded.description,
			metadata = excluded.metadata,
			definition = excluded.definition,
			is_active = excluded.is_active,
			updated_at = CURRENT_TIMESTAMP
	`, req.SchemaID, name, version, strings.TrimSpace(req.Description), string(metadataJSON), string(definitionJSON), boolToInt(active))
	if err != nil {
		return nil, fmt.Errorf("upsert ontology schema: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit ontology schema: %w", err)
	}
	return db.loadOntologySchema(ctx, req.SchemaID)
}

func (db *DB) loadOntologySchema(ctx context.Context, schemaID string) (*OntologySchema, error) {
	if strings.TrimSpace(schemaID) == "" {
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

	query := `
		SELECT id, name, version, description, metadata, definition, is_active, created_at, updated_at
		FROM graph_ontology_schemas
	`
	args := []any{}
	if activeOnly {
		query += ` WHERE is_active = 1`
	}
	query += ` ORDER BY is_active DESC, updated_at DESC, id ASC`

	rows, err := db.store.GetDB().QueryContext(ctx, query, args...)
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ontology schemas: %w", err)
	}
	return schemas, nil
}

func (db *DB) deleteOntologySchemaRecord(ctx context.Context, schemaID string) (bool, error) {
	if strings.TrimSpace(schemaID) == "" {
		return false, fmt.Errorf("schema_id is required")
	}
	if err := db.ensureOntologySchemaTable(ctx); err != nil {
		return false, fmt.Errorf("init ontology schema table: %w", err)
	}

	res, err := db.store.GetDB().ExecContext(ctx, `DELETE FROM graph_ontology_schemas WHERE id = ?`, schemaID)
	if err != nil {
		return false, fmt.Errorf("delete ontology schema: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete ontology schema rows affected: %w", err)
	}
	return rowsAffected > 0, nil
}

func (db *DB) loadActiveOntologySchema(ctx context.Context) (*OntologySchema, error) {
	if err := db.ensureOntologySchemaTable(ctx); err != nil {
		return nil, fmt.Errorf("init ontology schema table: %w", err)
	}

	row := db.store.GetDB().QueryRowContext(ctx, `
		SELECT id, name, version, description, metadata, definition, is_active, created_at, updated_at
		FROM graph_ontology_schemas
		WHERE is_active = 1
		ORDER BY updated_at DESC, id ASC
		LIMIT 1
	`)

	schema, err := scanOntologySchema(row)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return schema, nil
}

func (db *DB) getOntologySchemaTx(ctx context.Context, querier ontologyRowQuerier, schemaID string) (*OntologySchema, error) {
	row := querier.QueryRowContext(ctx, `
		SELECT id, name, version, description, metadata, definition, is_active, created_at, updated_at
		FROM graph_ontology_schemas
		WHERE id = ?
	`, schemaID)
	return scanOntologySchema(row)
}

type ontologyRowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type ontologyRowScanner interface {
	Scan(...any) error
}

func scanOntologySchema(scanner ontologyRowScanner) (*OntologySchema, error) {
	var schema OntologySchema
	var metadataJSON sql.NullString
	var definitionJSON string
	var active int

	err := scanner.Scan(
		&schema.SchemaID,
		&schema.Name,
		&schema.Version,
		&schema.Description,
		&metadataJSON,
		&definitionJSON,
		&active,
		&schema.CreatedAt,
		&schema.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, core.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan ontology schema: %w", err)
	}

	if metadataJSON.Valid && metadataJSON.String != "" {
		if err := json.Unmarshal([]byte(metadataJSON.String), &schema.Metadata); err != nil {
			return nil, fmt.Errorf("decode ontology metadata: %w", err)
		}
	}

	var definition ontologySchemaDefinition
	if err := json.Unmarshal([]byte(definitionJSON), &definition); err != nil {
		return nil, fmt.Errorf("decode ontology definition: %w", err)
	}
	schema.EntityTypes = definition.EntityTypes
	schema.RelationTypes = definition.RelationTypes
	schema.Active = active == 1

	sort.Slice(schema.EntityTypes, func(i, j int) bool { return schema.EntityTypes[i].Name < schema.EntityTypes[j].Name })
	sort.Slice(schema.RelationTypes, func(i, j int) bool { return schema.RelationTypes[i].Name < schema.RelationTypes[j].Name })
	return &schema, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
