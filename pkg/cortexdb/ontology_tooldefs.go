package cortexdb

func ontologyToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "ontology_save",
			Description: "Store or update an ontology-lite schema that validates entity and relation writes. Mark one schema active to make it the default validator.",
			InputSchema: toolObjectSchema(
				[]string{"schema_id"},
				map[string]any{
					"schema_id":      toolStringSchema("Stable ontology schema ID."),
					"name":           toolStringSchema("Optional display name."),
					"description":    toolStringSchema("Optional schema description."),
					"version":        toolIntegerSchema("Optional explicit schema version. Omit to auto-increment."),
					"activate":       toolBooleanSchema("Set true to make this the active ontology schema."),
					"deactivate":     toolBooleanSchema("Set true to deactivate this schema without deleting it."),
					"metadata":       toolMapSchema("Optional schema metadata."),
					"entity_types":   toolOntologyEntityTypesSchema(),
					"relation_types": toolOntologyRelationTypesSchema(),
				},
			),
		},
		{
			Name:        "ontology_get",
			Description: "Fetch one ontology-lite schema by ID.",
			InputSchema: toolObjectSchema(
				[]string{"schema_id"},
				map[string]any{
					"schema_id": toolStringSchema("Ontology schema ID."),
				},
			),
		},
		{
			Name:        "ontology_list",
			Description: "List ontology-lite schemas. Use active_only=true to inspect the current validator.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"active_only": toolBooleanSchema("When true, return only the active ontology schema."),
				},
			),
		},
		{
			Name:        "ontology_delete",
			Description: "Delete one ontology-lite schema by ID.",
			InputSchema: toolObjectSchema(
				[]string{"schema_id"},
				map[string]any{
					"schema_id": toolStringSchema("Ontology schema ID."),
				},
			),
		},
	}
}

func toolOntologyEntityTypesSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": toolObjectSchema(
			[]string{"name"},
			map[string]any{
				"name":                toolStringSchema("Canonical entity type name, such as person or organization."),
				"description":         toolStringSchema("Optional entity type description."),
				"required_properties": toolStringArraySchema("Metadata keys that must be present when entities of this type are written."),
			},
		),
	}
}

func toolOntologyRelationTypesSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": toolObjectSchema(
			[]string{"name"},
			map[string]any{
				"name":                toolStringSchema("Canonical relation type name, such as works_at."),
				"description":         toolStringSchema("Optional relation type description."),
				"allowed_from_types":  toolStringArraySchema("Optional allowed source entity types."),
				"allowed_to_types":    toolStringArraySchema("Optional allowed target entity types."),
				"required_properties": toolStringArraySchema("Metadata keys that must be present when this relation is written."),
			},
		),
	}
}
