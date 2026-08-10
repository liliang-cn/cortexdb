package cortexdb

import "context"

// OntologySaveRequest stores or updates a v2 ontology schema.
type OntologySaveRequest struct {
	Schema     OntologySchema `json:"schema"`
	Activate   bool           `json:"activate,omitempty"`
	Deactivate bool           `json:"deactivate,omitempty"`
}

// OntologySaveResponse returns the persisted schema.
type OntologySaveResponse struct {
	Schema OntologySchema `json:"schema"`
}

// OntologyGetRequest fetches one ontology schema by ID.
type OntologyGetRequest struct {
	SchemaID string `json:"schema_id"`
}

// OntologyGetResponse returns one ontology schema.
type OntologyGetResponse struct {
	Schema OntologySchema `json:"schema"`
}

// OntologyListRequest lists ontology schemas.
type OntologyListRequest struct {
	ActiveOnly bool `json:"active_only,omitempty"`
}

// OntologyListResponse returns ontology schemas.
type OntologyListResponse struct {
	Schemas []OntologySchema `json:"schemas"`
}

// OntologyDeleteRequest deletes one ontology schema by ID.
type OntologyDeleteRequest struct {
	SchemaID string `json:"schema_id"`
}

// OntologyDeleteResponse confirms ontology deletion.
type OntologyDeleteResponse struct {
	SchemaID string `json:"schema_id"`
	Deleted  bool   `json:"deleted"`
}

func (db *DB) SaveOntologySchema(ctx context.Context, req OntologySaveRequest) (*OntologySaveResponse, error) {
	schema, err := db.saveOntologySchemaRecord(ctx, req)
	if err != nil {
		return nil, err
	}
	return &OntologySaveResponse{Schema: *schema}, nil
}

func (db *DB) GetOntologySchema(ctx context.Context, req OntologyGetRequest) (*OntologyGetResponse, error) {
	schema, err := db.loadOntologySchema(ctx, req.SchemaID)
	if err != nil {
		return nil, err
	}
	return &OntologyGetResponse{Schema: *schema}, nil
}

func (db *DB) ListOntologySchemas(ctx context.Context, req OntologyListRequest) (*OntologyListResponse, error) {
	schemas, err := db.listOntologySchemaRecords(ctx, req.ActiveOnly)
	if err != nil {
		return nil, err
	}
	return &OntologyListResponse{Schemas: schemas}, nil
}

func (db *DB) DeleteOntologySchema(ctx context.Context, req OntologyDeleteRequest) (*OntologyDeleteResponse, error) {
	deleted, err := db.deleteOntologySchemaRecord(ctx, req.SchemaID)
	if err != nil {
		return nil, err
	}
	return &OntologyDeleteResponse{SchemaID: req.SchemaID, Deleted: deleted}, nil
}

func (t *GraphRAGToolbox) SaveOntologySchema(ctx context.Context, req OntologySaveRequest) (*OntologySaveResponse, error) {
	return t.db.SaveOntologySchema(ctx, req)
}

func (t *GraphRAGToolbox) GetOntologySchema(ctx context.Context, req OntologyGetRequest) (*OntologyGetResponse, error) {
	return t.db.GetOntologySchema(ctx, req)
}

func (t *GraphRAGToolbox) ListOntologySchemas(ctx context.Context, req OntologyListRequest) (*OntologyListResponse, error) {
	return t.db.ListOntologySchemas(ctx, req)
}

func (t *GraphRAGToolbox) DeleteOntologySchema(ctx context.Context, req OntologyDeleteRequest) (*OntologyDeleteResponse, error) {
	return t.db.DeleteOntologySchema(ctx, req)
}
