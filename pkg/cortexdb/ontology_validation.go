package cortexdb

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

type compiledOntologySchema struct {
	entityTypes   map[string]OntologyEntityType
	relationTypes map[string]OntologyRelationType
}

func validateOntologySaveRequest(req OntologySaveRequest) error {
	if strings.TrimSpace(req.SchemaID) == "" {
		return fmt.Errorf("schema_id is required")
	}
	if req.Activate && req.Deactivate {
		return fmt.Errorf("activate and deactivate cannot both be true")
	}

	entityTypes := normalizeOntologyEntityTypes(req.EntityTypes)
	relationTypes := normalizeOntologyRelationTypes(req.RelationTypes)
	entitySet := make(map[string]struct{}, len(entityTypes))

	for _, entityType := range entityTypes {
		if entityType.Name == "" {
			return fmt.Errorf("entity type name is required")
		}
		if _, exists := entitySet[entityType.Name]; exists {
			return fmt.Errorf("duplicate entity type: %s", entityType.Name)
		}
		entitySet[entityType.Name] = struct{}{}
	}

	relationSet := make(map[string]struct{}, len(relationTypes))
	for _, relationType := range relationTypes {
		if relationType.Name == "" {
			return fmt.Errorf("relation type name is required")
		}
		if _, exists := relationSet[relationType.Name]; exists {
			return fmt.Errorf("duplicate relation type: %s", relationType.Name)
		}
		relationSet[relationType.Name] = struct{}{}

		for _, fromType := range relationType.AllowedFromTypes {
			if _, exists := entitySet[fromType]; !exists {
				return fmt.Errorf("relation type %s references unknown source entity type %s", relationType.Name, fromType)
			}
		}
		for _, toType := range relationType.AllowedToTypes {
			if _, exists := entitySet[toType]; !exists {
				return fmt.Errorf("relation type %s references unknown target entity type %s", relationType.Name, toType)
			}
		}
	}
	return nil
}

func normalizeOntologyEntityTypes(types []OntologyEntityType) []OntologyEntityType {
	if len(types) == 0 {
		return nil
	}
	out := make([]OntologyEntityType, 0, len(types))
	for _, entityType := range types {
		name := normalizeOntologyLabel(entityType.Name)
		if name == "" {
			continue
		}
		out = append(out, OntologyEntityType{
			Name:               name,
			Description:        strings.TrimSpace(entityType.Description),
			RequiredProperties: normalizeOntologyProperties(entityType.RequiredProperties),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func normalizeOntologyRelationTypes(types []OntologyRelationType) []OntologyRelationType {
	if len(types) == 0 {
		return nil
	}
	out := make([]OntologyRelationType, 0, len(types))
	for _, relationType := range types {
		name := normalizeOntologyLabel(relationType.Name)
		if name == "" {
			continue
		}
		out = append(out, OntologyRelationType{
			Name:               name,
			Description:        strings.TrimSpace(relationType.Description),
			AllowedFromTypes:   normalizeOntologyProperties(relationType.AllowedFromTypes),
			AllowedToTypes:     normalizeOntologyProperties(relationType.AllowedToTypes),
			RequiredProperties: normalizeOntologyProperties(relationType.RequiredProperties),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func normalizeOntologyProperties(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := normalizeOntologyLabel(value)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

func normalizeOntologyLabel(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.Join(strings.Fields(value), "_")
	return value
}

func compileOntologySchema(schema *OntologySchema) *compiledOntologySchema {
	if schema == nil {
		return &compiledOntologySchema{
			entityTypes:   map[string]OntologyEntityType{},
			relationTypes: map[string]OntologyRelationType{},
		}
	}

	compiled := &compiledOntologySchema{
		entityTypes:   make(map[string]OntologyEntityType, len(schema.EntityTypes)),
		relationTypes: make(map[string]OntologyRelationType, len(schema.RelationTypes)),
	}
	for _, entityType := range schema.EntityTypes {
		compiled.entityTypes[normalizeOntologyLabel(entityType.Name)] = entityType
	}
	for _, relationType := range schema.RelationTypes {
		compiled.relationTypes[normalizeOntologyLabel(relationType.Name)] = relationType
	}
	return compiled
}

func (db *DB) validateEntityInputs(ctx context.Context, entities []ToolEntityInput) error {
	schema, err := db.loadActiveOntologySchema(ctx)
	if err != nil || schema == nil {
		return err
	}

	compiled := compileOntologySchema(schema)
	if len(compiled.entityTypes) == 0 {
		return nil
	}

	for _, entity := range entities {
		if strings.TrimSpace(entity.Name) == "" && strings.TrimSpace(entity.ID) == "" {
			continue
		}
		entityType := normalizeOntologyLabel(firstNonEmpty(entity.Type, "entity"))
		definition, ok := compiled.entityTypes[entityType]
		if !ok {
			return fmt.Errorf("ontology schema %s does not define entity type %q", schema.SchemaID, entityType)
		}
		if err := validateOntologyRequiredProperties("entity", firstNonEmpty(entity.Name, entity.ID), definition.RequiredProperties, entity.Metadata); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) validateRelationInputs(ctx context.Context, relations []ToolRelationInput) error {
	return db.validateRelationInputsWithResolver(ctx, relations, db.loadOntologyNodeTypes)
}

func (db *DB) validateRelationInputsWithResolver(ctx context.Context, relations []ToolRelationInput, resolver func(context.Context, []string) (map[string]string, error)) error {
	schema, err := db.loadActiveOntologySchema(ctx)
	if err != nil || schema == nil {
		return err
	}

	compiled := compileOntologySchema(schema)
	if len(compiled.relationTypes) == 0 {
		return nil
	}

	nodeIDSet := make(map[string]struct{}, len(relations)*2)
	for _, relation := range relations {
		fromID := resolveEntityNodeID("", relation.From)
		toID := resolveEntityNodeID("", relation.To)
		if fromID != "" {
			nodeIDSet[fromID] = struct{}{}
		}
		if toID != "" {
			nodeIDSet[toID] = struct{}{}
		}
	}

	nodeIDs := sortedKeysFromSet(nodeIDSet)
	nodeTypes, err := resolver(ctx, nodeIDs)
	if err != nil {
		return err
	}

	for _, relation := range relations {
		fromID := resolveEntityNodeID("", relation.From)
		toID := resolveEntityNodeID("", relation.To)
		if fromID == "" || toID == "" {
			return fmt.Errorf("relation endpoints are required")
		}

		relationType := normalizeOntologyLabel(firstNonEmpty(relation.Type, "related_to"))
		definition, ok := compiled.relationTypes[relationType]
		if !ok {
			return fmt.Errorf("ontology schema %s does not define relation type %q", schema.SchemaID, relationType)
		}

		fromType, ok := nodeTypes[fromID]
		if !ok {
			return fmt.Errorf("ontology validation could not resolve source entity %s", relation.From)
		}
		toType, ok := nodeTypes[toID]
		if !ok {
			return fmt.Errorf("ontology validation could not resolve target entity %s", relation.To)
		}

		fromType = normalizeOntologyLabel(fromType)
		toType = normalizeOntologyLabel(toType)
		if len(definition.AllowedFromTypes) > 0 && !containsStringValue(definition.AllowedFromTypes, fromType) {
			return fmt.Errorf("relation type %s does not allow source entity type %s", relationType, fromType)
		}
		if len(definition.AllowedToTypes) > 0 && !containsStringValue(definition.AllowedToTypes, toType) {
			return fmt.Errorf("relation type %s does not allow target entity type %s", relationType, toType)
		}
		if err := validateOntologyRequiredProperties("relation", relationType, definition.RequiredProperties, relation.Metadata); err != nil {
			return err
		}
	}
	return nil
}

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

func (db *DB) validateExtractedGraphData(ctx context.Context, entities map[string]GraphEntity, relationships map[string]graph.GraphEdge) error {
	if len(entities) == 0 && len(relationships) == 0 {
		return nil
	}

	entityInputs := make([]ToolEntityInput, 0, len(entities))
	entityTypes := make(map[string]string, len(entities))
	for entityID, entity := range entities {
		entityType := firstNonEmpty(entity.Type, "entity")
		entityInputs = append(entityInputs, ToolEntityInput{
			ID:   entityID,
			Name: entity.Name,
			Type: entityType,
		})
		entityTypes[entityID] = entityType
	}
	if err := db.validateEntityInputs(ctx, entityInputs); err != nil {
		return err
	}

	relationInputs := make([]ToolRelationInput, 0, len(relationships))
	for _, relation := range relationships {
		relationInputs = append(relationInputs, ToolRelationInput{
			From:     relation.FromNodeID,
			To:       relation.ToNodeID,
			Type:     relation.EdgeType,
			Metadata: anyMapToStringMap(relation.Properties),
		})
	}
	return db.validateRelationInputsWithResolver(ctx, relationInputs, func(_ context.Context, nodeIDs []string) (map[string]string, error) {
		resolved := make(map[string]string, len(nodeIDs))
		for _, nodeID := range nodeIDs {
			entityType, ok := entityTypes[nodeID]
			if ok {
				resolved[nodeID] = entityType
			}
		}
		return resolved, nil
	})
}

func validateOntologyRequiredProperties(kind string, name string, required []string, metadata map[string]string) error {
	if len(required) == 0 {
		return nil
	}

	normalizedMetadata := make(map[string]struct{}, len(metadata))
	for key := range metadata {
		normalizedMetadata[normalizeOntologyLabel(key)] = struct{}{}
	}

	for _, property := range required {
		if _, exists := normalizedMetadata[normalizeOntologyLabel(property)]; !exists {
			return fmt.Errorf("%s %s is missing required property %s", kind, name, property)
		}
	}
	return nil
}

func containsStringValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
