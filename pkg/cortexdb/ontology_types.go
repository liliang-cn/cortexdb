package cortexdb

import "time"

// OntologyStatus is the lifecycle stage of a type, mirroring Foundry's release status.
type OntologyStatus string

const (
	OntologyStatusActive       OntologyStatus = "active"
	OntologyStatusExperimental OntologyStatus = "experimental"
	OntologyStatusDeprecated   OntologyStatus = "deprecated"
)

// OntologyVisibility controls how prominently a type surfaces to callers.
type OntologyVisibility string

const (
	OntologyVisibilityNormal    OntologyVisibility = "normal"
	OntologyVisibilityProminent OntologyVisibility = "prominent"
	OntologyVisibilityHidden    OntologyVisibility = "hidden"
)

// OntologyDataKind enumerates the property base types CortexDB supports.
// This is Foundry's discriminator list minus the types that need a Foundry
// backend (attachment, mediaReference, timeseries, geotimeSeriesReference).
type OntologyDataKind string

const (
	OntologyDataString    OntologyDataKind = "string"
	OntologyDataInteger   OntologyDataKind = "integer"
	OntologyDataLong      OntologyDataKind = "long"
	OntologyDataDouble    OntologyDataKind = "double"
	OntologyDataDecimal   OntologyDataKind = "decimal"
	OntologyDataBoolean   OntologyDataKind = "boolean"
	OntologyDataDate      OntologyDataKind = "date"
	OntologyDataTimestamp OntologyDataKind = "timestamp"
	OntologyDataGeoPoint  OntologyDataKind = "geopoint"
	OntologyDataGeoShape  OntologyDataKind = "geoshape"
	OntologyDataVector    OntologyDataKind = "vector"
	OntologyDataArray     OntologyDataKind = "array"
	OntologyDataStruct    OntologyDataKind = "struct"
	OntologyDataMarking   OntologyDataKind = "marking"
)

// OntologyCardinality is the multiplicity of one side of a link type.
type OntologyCardinality string

const (
	OntologyCardinalityOne  OntologyCardinality = "ONE"
	OntologyCardinalityMany OntologyCardinality = "MANY"
)

// OntologyDataType describes a property's base type, including the nested
// element type for arrays, the field list for structs, and the dimension for
// vectors.
type OntologyDataType struct {
	Kind      OntologyDataKind   `json:"kind"`
	ItemType  *OntologyDataType  `json:"item_type,omitempty"`
	Fields    []OntologyProperty `json:"fields,omitempty"`
	Dimension int                `json:"dimension,omitempty"`
}

// OntologyProperty is a typed characteristic of an object type or interface.
type OntologyProperty struct {
	APIName     string           `json:"api_name"`
	DisplayName string           `json:"display_name,omitempty"`
	Description string           `json:"description,omitempty"`
	DataType    OntologyDataType `json:"data_type"`
	Required    bool             `json:"required,omitempty"`
	// Searchable routes the property value into FTS5 so ObjectSet text
	// predicates can match on it.
	Searchable bool `json:"searchable,omitempty"`
	// Vectorized routes the property value into the vector index so
	// ObjectSet nearest-neighbour predicates can match on it.
	Vectorized bool `json:"vectorized,omitempty"`
}

// OntologyObjectType is the schema definition of a real-world entity.
// PrimaryKey is mandatory: it is what gives objects a stable identity.
type OntologyObjectType struct {
	APIName           string             `json:"api_name"`
	DisplayName       string             `json:"display_name,omitempty"`
	PluralDisplayName string             `json:"plural_display_name,omitempty"`
	Description       string             `json:"description,omitempty"`
	Status            OntologyStatus     `json:"status,omitempty"`
	Visibility        OntologyVisibility `json:"visibility,omitempty"`
	Icon              string             `json:"icon,omitempty"`
	PrimaryKey        string             `json:"primary_key"`
	TitleProperty     string             `json:"title_property,omitempty"`
	Properties        []OntologyProperty `json:"properties,omitempty"`
	Implements        []string           `json:"implements,omitempty"`
	Aliases           []string           `json:"aliases,omitempty"`
}

// OntologyLinkSide is one end of a link type. Foundry models multiplicity
// per side rather than as a single one-to-many enum: a one-to-many link is
// one side ONE and one side MANY.
type OntologyLinkSide struct {
	APIName           string `json:"api_name"`
	DisplayName       string `json:"display_name,omitempty"`
	ObjectTypeAPIName string `json:"object_type_api_name"`
	// Cardinality is how many objects a traversal starting at ObjectTypeAPIName
	// reaches. A ONE side is the side that carries the foreign key.
	Cardinality        OntologyCardinality `json:"cardinality"`
	ForeignKeyProperty string              `json:"foreign_key_property,omitempty"`
}

// OntologyLinkType is a bidirectional relationship between two object types.
// Graph edges carry the link type APIName as their EdgeType; the side API
// names are traversal labels only.
type OntologyLinkType struct {
	APIName     string           `json:"api_name"`
	Description string           `json:"description,omitempty"`
	Status      OntologyStatus   `json:"status,omitempty"`
	A           OntologyLinkSide `json:"a"`
	B           OntologyLinkSide `json:"b"`
}

// OntologyInterfaceType is an abstract shape that object types implement,
// giving polymorphic retrieval across unrelated concrete types.
type OntologyInterfaceType struct {
	APIName     string             `json:"api_name"`
	DisplayName string             `json:"display_name,omitempty"`
	Description string             `json:"description,omitempty"`
	Extends     []string           `json:"extends,omitempty"`
	Properties  []OntologyProperty `json:"properties,omitempty"`
}

// OntologySchema is the full stored ontology.
type OntologySchema struct {
	SchemaID    string `json:"schema_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     int    `json:"version"`
	Active      bool   `json:"active"`
	// StrictActions closes the generic upsert tools once action types are
	// defined, making actions the only write path. Defaults to false.
	StrictActions    bool                     `json:"strict_actions,omitempty"`
	Metadata         map[string]string        `json:"metadata,omitempty"`
	ObjectTypes      []OntologyObjectType     `json:"object_types,omitempty"`
	LinkTypes        []OntologyLinkType       `json:"link_types,omitempty"`
	InterfaceTypes   []OntologyInterfaceType  `json:"interface_types,omitempty"`
	SharedProperties []OntologyProperty       `json:"shared_properties,omitempty"`
	ActionTypes      []OntologyActionType     `json:"action_types,omitempty"`
	ObjectSets       []OntologyNamedObjectSet `json:"object_sets,omitempty"`
	CreatedAt        time.Time                `json:"created_at"`
	UpdatedAt        time.Time                `json:"updated_at"`
}
