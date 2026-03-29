package cortexdb

import (
	"context"
	"fmt"
)

// SaveOntologySchema stores or updates an ontology-lite schema.
func (db *DB) SaveOntologySchema(ctx context.Context, req OntologySaveRequest) (*OntologySaveResponse, error) {
	schema, err := db.saveOntologySchemaRecord(ctx, req)
	if err != nil {
		return nil, err
	}
	return &OntologySaveResponse{Schema: *schema}, nil
}

// GetOntologySchema fetches one ontology-lite schema by ID.
func (db *DB) GetOntologySchema(ctx context.Context, req OntologyGetRequest) (*OntologyGetResponse, error) {
	schema, err := db.loadOntologySchema(ctx, req.SchemaID)
	if err != nil {
		return nil, err
	}
	return &OntologyGetResponse{Schema: *schema}, nil
}

// ListOntologySchemas lists ontology-lite schemas.
func (db *DB) ListOntologySchemas(ctx context.Context, req OntologyListRequest) (*OntologyListResponse, error) {
	schemas, err := db.listOntologySchemaRecords(ctx, req.ActiveOnly)
	if err != nil {
		return nil, err
	}
	return &OntologyListResponse{Schemas: schemas}, nil
}

// DeleteOntologySchema deletes one ontology-lite schema.
func (db *DB) DeleteOntologySchema(ctx context.Context, req OntologyDeleteRequest) (*OntologyDeleteResponse, error) {
	if req.SchemaID == "" {
		return nil, fmt.Errorf("schema_id is required")
	}
	deleted, err := db.deleteOntologySchemaRecord(ctx, req.SchemaID)
	if err != nil {
		return nil, err
	}
	return &OntologyDeleteResponse{SchemaID: req.SchemaID, Deleted: deleted}, nil
}

// SaveOntologySchema stores or updates an ontology-lite schema through the tool surface.
func (t *GraphRAGToolbox) SaveOntologySchema(ctx context.Context, req OntologySaveRequest) (*OntologySaveResponse, error) {
	return t.db.SaveOntologySchema(ctx, req)
}

// GetOntologySchema fetches one ontology-lite schema through the tool surface.
func (t *GraphRAGToolbox) GetOntologySchema(ctx context.Context, req OntologyGetRequest) (*OntologyGetResponse, error) {
	return t.db.GetOntologySchema(ctx, req)
}

// ListOntologySchemas lists ontology-lite schemas through the tool surface.
func (t *GraphRAGToolbox) ListOntologySchemas(ctx context.Context, req OntologyListRequest) (*OntologyListResponse, error) {
	return t.db.ListOntologySchemas(ctx, req)
}

// DeleteOntologySchema deletes one ontology-lite schema through the tool surface.
func (t *GraphRAGToolbox) DeleteOntologySchema(ctx context.Context, req OntologyDeleteRequest) (*OntologyDeleteResponse, error) {
	return t.db.DeleteOntologySchema(ctx, req)
}
