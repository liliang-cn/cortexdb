package cortexdb

import "time"

// OntologyEntityType declares a canonical entity type and its required metadata.
type OntologyEntityType struct {
	Name               string   `json:"name"`
	Description        string   `json:"description,omitempty"`
	RequiredProperties []string `json:"required_properties,omitempty"`
}

// OntologyRelationType declares a relation type plus legal source and target entity types.
type OntologyRelationType struct {
	Name               string   `json:"name"`
	Description        string   `json:"description,omitempty"`
	AllowedFromTypes   []string `json:"allowed_from_types,omitempty"`
	AllowedToTypes     []string `json:"allowed_to_types,omitempty"`
	RequiredProperties []string `json:"required_properties,omitempty"`
}

// OntologySchema is the stored ontology-lite schema used to validate graph writes.
type OntologySchema struct {
	SchemaID      string                 `json:"schema_id"`
	Name          string                 `json:"name"`
	Description   string                 `json:"description,omitempty"`
	Version       int                    `json:"version"`
	Active        bool                   `json:"active"`
	Metadata      map[string]string      `json:"metadata,omitempty"`
	EntityTypes   []OntologyEntityType   `json:"entity_types,omitempty"`
	RelationTypes []OntologyRelationType `json:"relation_types,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

// OntologySaveRequest stores or updates an ontology-lite schema.
type OntologySaveRequest struct {
	SchemaID      string                 `json:"schema_id"`
	Name          string                 `json:"name,omitempty"`
	Description   string                 `json:"description,omitempty"`
	Version       int                    `json:"version,omitempty"`
	Activate      bool                   `json:"activate,omitempty"`
	Deactivate    bool                   `json:"deactivate,omitempty"`
	Metadata      map[string]string      `json:"metadata,omitempty"`
	EntityTypes   []OntologyEntityType   `json:"entity_types,omitempty"`
	RelationTypes []OntologyRelationType `json:"relation_types,omitempty"`
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
