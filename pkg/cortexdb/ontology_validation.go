package cortexdb

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// ErrInvalidOntology marks every ontology rejection: a schema that does not
// hold together, and a write that does not conform to the active schema.
// Callers at a protocol boundary need to tell "you sent something bad" apart
// from "the server broke"; matching on it is reliable in a way that sniffing
// the message text is not.
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

	switch schema.Enforcement {
	case "", OntologyEnforcementStrict, OntologyEnforcementVocabulary:
	default:
		return fmt.Errorf("enforcement must be %q or %q, got %q",
			OntologyEnforcementStrict, OntologyEnforcementVocabulary, schema.Enforcement)
	}
	// StrictActions makes governed actions the only write path; a vocabulary
	// schema promises not to gate writes at all. Both at once is a schema that
	// contradicts itself, and whichever the code happened to honour would look
	// like a bug to whoever expected the other.
	if schema.StrictActions && schema.Enforcement == OntologyEnforcementVocabulary {
		return fmt.Errorf("strict_actions and enforcement=vocabulary are contradictory: one closes the generic write path, the other promises never to")
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

	// Last, because these are the rules that need the whole schema resolved
	// into its lookup form. The name checks above run first so a misspelled
	// interface is reported as such rather than as an unsatisfied contract.
	compiled := compileOntology(schema)
	if err := validateOntologyInterfaces(schema, compiled); err != nil {
		return err
	}
	// Actions and object sets are checked after the types they reference, so a
	// rule pointing at a broken object type reports the broken type first.
	if err := validateOntologyActionTypes(schema, compiled); err != nil {
		return err
	}
	return validateOntologyObjectSets(schema)
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

// validateEntityInputs enforces the active ontology against entity writes.
// With no active schema every write is allowed, which is what keeps
// ontology-free deployments working unchanged.
func (db *DB) validateEntityInputs(ctx context.Context, entities []ToolEntityInput) error {
	compiled, err := db.activeCompiledOntology(ctx)
	if err != nil || compiled == nil {
		return err
	}
	// A vocabulary schema does not gate writes. Identity still consults it —
	// declared types with a primary key get typed node IDs — but an entity
	// that cannot conform is stored the way it would be with no schema at all.
	if compiled.vocabularyMode() {
		return nil
	}
	for _, entity := range entities {
		if err := validateOntologyEntity(compiled, entity); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidOntology, err)
		}
	}
	return nil
}

// activeCompiledOntology returns the compiled active schema, or nil when
// there is nothing to enforce. A schema that declares no types is treated as
// nothing rather than as "reject everything".
func (db *DB) activeCompiledOntology(ctx context.Context) (*compiledOntology, error) {
	schema, err := db.loadActiveOntologySchema(ctx)
	if err != nil || schema == nil {
		return nil, err
	}
	compiled := compileOntology(*schema)
	if compiled.isEmpty() {
		return nil, nil
	}
	return compiled, nil
}

func validateOntologyEntity(compiled *compiledOntology, entity ToolEntityInput) error {
	// The write path drops entities with neither a name nor an ID, so
	// rejecting one here would fail a write that was never going to happen.
	if strings.TrimSpace(entity.Name) == "" && strings.TrimSpace(entity.ID) == "" {
		return nil
	}

	objectTypeName := firstNonEmpty(entity.Type, "entity")
	objectType, ok := compiled.objectType(objectTypeName)
	if !ok {
		return fmt.Errorf("ontology does not define object type %q", objectTypeName)
	}

	if _, err := resolveOntologyPrimaryKeyValue(compiled, objectType.APIName, entity); err != nil {
		return err
	}

	supplied := make(map[string]string, len(entity.Metadata))
	suppliedNames := make([]string, 0, len(entity.Metadata))
	for key, value := range entity.Metadata {
		supplied[ontologyAPIKey(key)] = value
		suppliedNames = append(suppliedNames, key)
	}
	// Sorted so that an entity breaking two rules always names the same one;
	// map order would otherwise make the error flap between runs.
	sort.Strings(suppliedNames)

	for _, name := range suppliedNames {
		property, ok := compiled.property(objectType.APIName, name)
		if !ok {
			// Reported with the caller's spelling, not the lookup key, so the
			// name in the error can be found in the payload that was sent.
			return fmt.Errorf("object type %q has no property %q", objectType.APIName, name)
		}
		if err := parseOntologyPropertyValue(property.DataType, entity.Metadata[name]); err != nil {
			return fmt.Errorf("object type %q property %q: %w", objectType.APIName, property.APIName, err)
		}
	}

	for _, property := range objectType.Properties {
		if !property.Required {
			continue
		}
		if ontologyAPIKey(property.APIName) == ontologyAPIKey(objectType.PrimaryKey) {
			// Already resolved above, and may legitimately arrive as the name.
			continue
		}
		if _, ok := supplied[ontologyAPIKey(property.APIName)]; !ok {
			return fmt.Errorf("object type %q is missing required property %q", objectType.APIName, property.APIName)
		}
	}
	return nil
}

// ontologyTypeResolver maps a node ID to its object type API name. It is a
// parameter rather than a fixed graph lookup so callers can answer for nodes
// that are only planned, not yet written.
type ontologyTypeResolver func(context.Context, []string) (map[string]string, error)

func (db *DB) validateRelationInputs(ctx context.Context, relations []ToolRelationInput) error {
	return db.validateRelationInputsWithResolver(ctx, relations, db.loadOntologyNodeTypes)
}

func (db *DB) validateRelationInputsWithResolver(ctx context.Context, relations []ToolRelationInput, resolve ontologyTypeResolver) error {
	compiled, err := db.activeCompiledOntology(ctx)
	if err != nil || compiled == nil {
		return err
	}
	if compiled.vocabularyMode() {
		return nil
	}

	// Endpoints are resolved the same way the write path will resolve them, so
	// validation and the write it guards cannot disagree about which node an
	// endpoint names.
	endpointIDs := make(map[string]string, len(relations)*2)
	nodeIDSet := make(map[string]struct{}, len(relations)*2)
	for _, relation := range relations {
		for _, endpoint := range []string{relation.From, relation.To} {
			if _, seen := endpointIDs[endpoint]; seen {
				continue
			}
			nodeID, err := db.lookupOntologyRelationEndpointNodeID(ctx, compiled, endpoint)
			if err != nil {
				return err
			}
			endpointIDs[endpoint] = nodeID
			if nodeID != "" {
				nodeIDSet[nodeID] = struct{}{}
			}
		}
	}
	nodeTypes, err := resolve(ctx, sortedKeysFromSet(nodeIDSet))
	if err != nil {
		return err
	}

	for _, relation := range relations {
		if err := db.validateOntologyRelation(ctx, compiled, relation, endpointIDs, nodeTypes); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidOntology, err)
		}
	}
	return nil
}

func (db *DB) validateOntologyRelation(ctx context.Context, compiled *compiledOntology, relation ToolRelationInput, endpointIDs map[string]string, nodeTypes map[string]string) error {
	linkTypeName := firstNonEmpty(relation.Type, "related_to")
	linkType, ok := compiled.linkType(linkTypeName)
	if !ok {
		return fmt.Errorf("ontology does not define link type %q", linkTypeName)
	}

	if strings.TrimSpace(relation.From) == "" || strings.TrimSpace(relation.To) == "" {
		return fmt.Errorf("relation endpoints are required")
	}
	fromID := endpointIDs[relation.From]
	toID := endpointIDs[relation.To]

	fromType, ok := nodeTypes[fromID]
	if !ok {
		return fmt.Errorf("ontology validation could not resolve source entity %s", relation.From)
	}
	toType, ok := nodeTypes[toID]
	if !ok {
		return fmt.Errorf("ontology validation could not resolve target entity %s", relation.To)
	}

	fromSide, toSide, err := compiled.orientLink(linkType, fromType, toType)
	if err != nil {
		return err
	}
	return db.checkOntologyCardinality(ctx, linkType, fromID, fromSide, toID, toSide)
}

// checkOntologyCardinality refuses a second edge into a side declared ONE.
// The check runs against edges already in the graph; a batch that violates
// cardinality within itself is caught on the second relation.
func (db *DB) checkOntologyCardinality(ctx context.Context, linkType OntologyLinkType, fromID string, fromSide OntologyLinkSide, toID string, toSide OntologyLinkSide) error {
	// An edge from A to B occupies one slot on B's side as seen from A, and
	// vice versa. A ONE side means the *other* endpoint may hold at most one
	// edge of this link type.
	if toSide.Cardinality == OntologyCardinalityOne {
		count, err := db.countOntologyLinks(ctx, linkType.APIName, toID, fromID)
		if err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("link type %q side %q has cardinality ONE: %s already has a %s link",
				linkType.APIName, toSide.APIName, toID, linkType.APIName)
		}
	}
	if fromSide.Cardinality == OntologyCardinalityOne {
		count, err := db.countOntologyLinks(ctx, linkType.APIName, fromID, toID)
		if err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("link type %q side %q has cardinality ONE: %s already has a %s link",
				linkType.APIName, fromSide.APIName, fromID, linkType.APIName)
		}
	}
	return nil
}

// countOntologyLinks counts edges of a link type touching nodeID, excluding
// edges to excludeNodeID so that re-asserting the same link is idempotent.
//
// The edge type is matched case-insensitively because API names resolve that
// way everywhere else; an exact match would let a caller spell the link type
// differently and slip a second edge past a ONE side.
func (db *DB) countOntologyLinks(ctx context.Context, linkTypeAPIName string, nodeID string, excludeNodeID string) (int, error) {
	row := db.store.GetDB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM graph_edges
		WHERE edge_type = ? COLLATE NOCASE
		  AND (from_node_id = ? OR to_node_id = ?)
		  AND from_node_id <> ? AND to_node_id <> ?
	`, linkTypeAPIName, nodeID, nodeID, excludeNodeID, excludeNodeID)

	var count int
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("count ontology links: %w", err)
	}
	return count, nil
}

// validateExtractedGraphData admits a whole extraction: the entities and the
// relationships between them, before any of it is written.
func (db *DB) validateExtractedGraphData(ctx context.Context, entities map[string]GraphEntity, relationships map[string]graph.GraphEdge) error {
	if len(entities) == 0 && len(relationships) == 0 {
		return nil
	}

	// Sorted so that an extraction breaking several rules always reports the
	// same one; map order would otherwise make the error flap between runs.
	entityInputs := make([]ToolEntityInput, 0, len(entities))
	batchTypes := make(map[string]string, len(entities))
	for _, entityID := range sortedMapKeys(entities) {
		entity := entities[entityID]
		objectType := firstNonEmpty(entity.Type, "entity")
		entityInputs = append(entityInputs, ToolEntityInput{ID: entityID, Name: entity.Name, Type: objectType})
		batchTypes[entityID] = objectType
	}
	if err := db.validateEntityInputs(ctx, entityInputs); err != nil {
		return err
	}

	relationInputs := make([]ToolRelationInput, 0, len(relationships))
	for _, key := range sortedMapKeys(relationships) {
		relation := relationships[key]
		relationInputs = append(relationInputs, ToolRelationInput{
			From:     relation.FromNodeID,
			To:       relation.ToNodeID,
			Type:     relation.EdgeType,
			Metadata: anyMapToStringMap(relation.Properties),
		})
	}

	// Resolve from the graph first, then fall back to types declared in this
	// same batch. v1 only did the former, so a batch that created both a node
	// and an edge in one call failed on the edge.
	return db.validateRelationInputsWithResolver(ctx, relationInputs, func(ctx context.Context, nodeIDs []string) (map[string]string, error) {
		resolved, err := db.loadOntologyNodeTypes(ctx, nodeIDs)
		if err != nil {
			return nil, err
		}
		for _, nodeID := range nodeIDs {
			if _, ok := resolved[nodeID]; ok {
				continue
			}
			if objectType, ok := batchTypes[nodeID]; ok {
				resolved[nodeID] = objectType
			}
		}
		return resolved, nil
	})
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
