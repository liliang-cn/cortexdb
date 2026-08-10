package cortexdb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// ErrInvalidOntology marks every rejection from schema validation. Callers at
// a protocol boundary need to tell "you sent a bad schema" apart from "the
// server broke"; matching on it is reliable in a way that sniffing the message
// text is not.
var ErrInvalidOntology = errors.New("invalid ontology schema")

// validateOntologySchema checks a schema for internal consistency before it
// is persisted. Everything it rejects is a modelling mistake that would
// otherwise surface much later as a confusing write-time failure.
func validateOntologySchema(schema OntologySchema) error {
	if err := validateOntologySchemaRules(schema); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidOntology, err)
	}
	return nil
}

func validateOntologySchemaRules(schema OntologySchema) error {
	if strings.TrimSpace(schema.SchemaID) == "" {
		return fmt.Errorf("schema_id is required")
	}

	sharedSeen := make(map[string]struct{}, len(schema.SharedProperties))
	for _, property := range schema.SharedProperties {
		if err := validateOntologyProperty("shared", property); err != nil {
			return err
		}
		key := ontologyAPIKey(property.APIName)
		if _, exists := sharedSeen[key]; exists {
			return fmt.Errorf("duplicate shared property %q", property.APIName)
		}
		sharedSeen[key] = struct{}{}
	}

	// Validate the expanded form: a property that names a shared definition
	// carries no data type of its own until it is resolved.
	schema = resolveSharedProperties(schema)

	objectTypes := make(map[string]OntologyObjectType, len(schema.ObjectTypes))
	for _, objectType := range schema.ObjectTypes {
		if err := validateOntologyAPIName("object type", objectType.APIName); err != nil {
			return err
		}
		key := ontologyAPIKey(objectType.APIName)
		if _, exists := objectTypes[key]; exists {
			return fmt.Errorf("duplicate object type %q", objectType.APIName)
		}
		if err := validateOntologyObjectType(objectType); err != nil {
			return err
		}
		objectTypes[key] = objectType
	}

	interfaceTypes := make(map[string]OntologyInterfaceType, len(schema.InterfaceTypes))
	for _, interfaceType := range schema.InterfaceTypes {
		if err := validateOntologyAPIName("interface type", interfaceType.APIName); err != nil {
			return err
		}
		key := ontologyAPIKey(interfaceType.APIName)
		if _, exists := interfaceTypes[key]; exists {
			return fmt.Errorf("duplicate interface type %q", interfaceType.APIName)
		}
		if _, exists := objectTypes[key]; exists {
			return fmt.Errorf("interface type %q collides with an object type of the same name", interfaceType.APIName)
		}
		for _, property := range interfaceType.Properties {
			if err := validateOntologyProperty(interfaceType.APIName, property); err != nil {
				return err
			}
		}
		interfaceTypes[key] = interfaceType
	}

	for _, objectType := range schema.ObjectTypes {
		for _, implemented := range objectType.Implements {
			if _, exists := interfaceTypes[ontologyAPIKey(implemented)]; !exists {
				return fmt.Errorf("object type %q implements unknown interface %q", objectType.APIName, implemented)
			}
		}
	}

	linkTypes := make(map[string]struct{}, len(schema.LinkTypes))
	// A side api name is how a traversal names the hop it wants to take, so it
	// has to identify one link type unambiguously from the object type it
	// starts at. Two link types exposing the same side name on the same object
	// type would make that lookup a coin flip.
	sidesByObjectType := make(map[string]map[string]string, len(schema.ObjectTypes))
	for _, linkType := range schema.LinkTypes {
		if err := validateOntologyAPIName("link type", linkType.APIName); err != nil {
			return err
		}
		key := ontologyAPIKey(linkType.APIName)
		if _, exists := linkTypes[key]; exists {
			return fmt.Errorf("duplicate link type %q", linkType.APIName)
		}
		linkTypes[key] = struct{}{}

		for _, side := range []OntologyLinkSide{linkType.A, linkType.B} {
			if err := validateOntologyLinkSide(linkType.APIName, side, objectTypes); err != nil {
				return err
			}
			ownerKey := ontologyAPIKey(side.ObjectTypeAPIName)
			sideKey := ontologyAPIKey(side.APIName)
			if sidesByObjectType[ownerKey] == nil {
				sidesByObjectType[ownerKey] = make(map[string]string, 2)
			}
			if owner, exists := sidesByObjectType[ownerKey][sideKey]; exists {
				return fmt.Errorf("link types %q and %q both expose side %q on object type %q",
					owner, linkType.APIName, side.APIName, side.ObjectTypeAPIName)
			}
			sidesByObjectType[ownerKey][sideKey] = linkType.APIName
		}
		if ontologyAPIKey(linkType.A.APIName) == ontologyAPIKey(linkType.B.APIName) {
			return fmt.Errorf("link type %q needs distinct api names on each side", linkType.APIName)
		}
	}
	return nil
}

func validateOntologyObjectType(objectType OntologyObjectType) error {
	if strings.TrimSpace(objectType.PrimaryKey) == "" {
		return fmt.Errorf("object type %q must declare primary_key", objectType.APIName)
	}

	properties := make(map[string]OntologyProperty, len(objectType.Properties))
	for _, property := range objectType.Properties {
		if err := validateOntologyProperty(objectType.APIName, property); err != nil {
			return err
		}
		key := ontologyAPIKey(property.APIName)
		if _, exists := properties[key]; exists {
			return fmt.Errorf("object type %q has duplicate property %q", objectType.APIName, property.APIName)
		}
		properties[key] = property
	}

	primaryKey, ok := properties[ontologyAPIKey(objectType.PrimaryKey)]
	if !ok {
		return fmt.Errorf("object type %q declares primary_key %q which is not a declared property", objectType.APIName, objectType.PrimaryKey)
	}
	if !primaryKey.Required {
		return fmt.Errorf("object type %q primary key property %q must be required", objectType.APIName, objectType.PrimaryKey)
	}
	switch primaryKey.DataType.Kind {
	case OntologyDataString, OntologyDataInteger, OntologyDataLong:
	default:
		return fmt.Errorf("object type %q primary key property %q must be string, integer or long, got %q",
			objectType.APIName, objectType.PrimaryKey, primaryKey.DataType.Kind)
	}

	if strings.TrimSpace(objectType.TitleProperty) != "" {
		if _, ok := properties[ontologyAPIKey(objectType.TitleProperty)]; !ok {
			return fmt.Errorf("object type %q declares title_property %q which is not a declared property", objectType.APIName, objectType.TitleProperty)
		}
	}
	return nil
}

func validateOntologyProperty(owner string, property OntologyProperty) error {
	if err := validateOntologyAPIName(fmt.Sprintf("%s property", owner), property.APIName); err != nil {
		return err
	}
	return validateOntologyDataType(owner, property.APIName, property.DataType)
}

func validateOntologyDataType(owner string, propertyName string, dataType OntologyDataType) error {
	// An empty kind almost always means the property meant to reference a
	// shared property that is missing or misspelled, since that is the only
	// way a property legitimately arrives here without its own data type.
	if dataType.Kind == "" {
		return fmt.Errorf("%s property %q must declare data_type.kind, or name a declared shared property", owner, propertyName)
	}
	switch dataType.Kind {
	case OntologyDataString, OntologyDataInteger, OntologyDataLong, OntologyDataDouble,
		OntologyDataDecimal, OntologyDataBoolean, OntologyDataDate, OntologyDataTimestamp,
		OntologyDataGeoPoint, OntologyDataGeoShape, OntologyDataMarking:
		return nil
	case OntologyDataVector:
		if dataType.Dimension <= 0 {
			return fmt.Errorf("%s property %q is a vector and must declare a positive dimension", owner, propertyName)
		}
		return nil
	case OntologyDataArray:
		if dataType.ItemType == nil {
			return fmt.Errorf("%s property %q is an array and must declare item_type", owner, propertyName)
		}
		return validateOntologyDataType(owner, propertyName, *dataType.ItemType)
	case OntologyDataStruct:
		if len(dataType.Fields) == 0 {
			return fmt.Errorf("%s property %q is a struct and must declare fields", owner, propertyName)
		}
		for _, field := range dataType.Fields {
			if err := validateOntologyProperty(owner, field); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("%s property %q has unknown data type kind %q", owner, propertyName, dataType.Kind)
	}
}

func validateOntologyLinkSide(linkTypeName string, side OntologyLinkSide, objectTypes map[string]OntologyObjectType) error {
	if err := validateOntologyAPIName(fmt.Sprintf("link type %s side", linkTypeName), side.APIName); err != nil {
		return err
	}
	objectType, ok := objectTypes[ontologyAPIKey(side.ObjectTypeAPIName)]
	if !ok {
		return fmt.Errorf("link type %q side %q references unknown object type %q", linkTypeName, side.APIName, side.ObjectTypeAPIName)
	}
	switch side.Cardinality {
	case OntologyCardinalityOne, OntologyCardinalityMany:
	default:
		return fmt.Errorf("link type %q side %q must declare cardinality ONE or MANY, got %q", linkTypeName, side.APIName, side.Cardinality)
	}

	if strings.TrimSpace(side.ForeignKeyProperty) == "" {
		return nil
	}
	// Foundry backs a ONE side with a foreign key on the object; a MANY side
	// has nowhere to put one, so accepting it would silently do nothing.
	if side.Cardinality != OntologyCardinalityOne {
		return fmt.Errorf("link type %q side %q declares a foreign key but has cardinality MANY", linkTypeName, side.APIName)
	}
	for _, property := range objectType.Properties {
		if ontologyAPIKey(property.APIName) == ontologyAPIKey(side.ForeignKeyProperty) {
			return nil
		}
	}
	return fmt.Errorf("link type %q side %q declares foreign key %q which is not a property of %q",
		linkTypeName, side.APIName, side.ForeignKeyProperty, objectType.APIName)
}

// The write-path admission checks below are deliberately pass-through for
// phase 1. Phase 1 only teaches CortexDB to store a v2 schema; the v1 checks
// they replace were built on entity/relation types that no longer exist, and
// enforcing v2 needs primary-key identity and cardinality counting that land
// in phase 2 (plan tasks 6-9). Keeping the call sites live and inert means
// phase 2 changes one function body each instead of re-threading the callers.

// TODO(phase2): replace with the v2 entity admission check (plan task 7):
// unknown object type, missing required property, and per-property data type
// parsing via parseOntologyPropertyValue.
func (db *DB) validateEntityInputs(_ context.Context, _ []ToolEntityInput) error { return nil }

// TODO(phase2): replace with the v2 relation admission check (plan task 8):
// unknown link type, endpoint object types matched via compiledOntology.orientLink,
// and ONE-side cardinality enforcement.
func (db *DB) validateRelationInputs(_ context.Context, _ []ToolRelationInput) error { return nil }

// TODO(phase2): replace alongside validateRelationInputs (plan task 8). The
// resolver argument exists so callers can supply entity types for nodes that
// are only planned, not yet written; keep that shape.
func (db *DB) validateRelationInputsWithResolver(_ context.Context, _ []ToolRelationInput, _ func(context.Context, []string) (map[string]string, error)) error {
	return nil
}

// TODO(phase2): replace with the v2 extracted-graph check (plan task 8), which
// validates entities and relationships from an extractor before they are written.
func (db *DB) validateExtractedGraphData(_ context.Context, _ map[string]GraphEntity, _ map[string]graph.GraphEdge) error {
	return nil
}

// loadOntologyNodeTypes reads the stored node type of each ID. It survives the
// v1-to-v2 rewrite unchanged because it only asks the graph what type a node
// already has, which is independent of how the schema models types.
func (db *DB) loadOntologyNodeTypes(ctx context.Context, nodeIDs []string) (map[string]string, error) {
	nodes, err := db.graph.GetNodesBatch(ctx, nodeIDs)
	if err != nil {
		return nil, fmt.Errorf("load ontology validation nodes: %w", err)
	}

	nodeTypes := make(map[string]string, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		nodeTypes[node.ID] = node.NodeType
	}
	return nodeTypes, nil
}
