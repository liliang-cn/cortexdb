package cortexdb

func ontologyToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "ontology_save",
			Description: "Store or update an ontology schema: object types with typed properties and a mandatory primary key, and link types with per-side cardinality. Mark one schema active to make it the write-time validator.",
			InputSchema: toolObjectSchema(
				[]string{"schema"},
				map[string]any{
					"schema": map[string]any{
						"type":        "object",
						"description": "Full ontology schema. Required keys: schema_id. Optional: name, description, version, strict_actions, metadata, object_types, link_types, interface_types, shared_properties. Each object_type needs api_name, primary_key and properties[]; each property needs api_name and data_type.kind (string|integer|long|double|decimal|boolean|date|timestamp|geopoint|geoshape|vector|array|struct|marking). Each link_type needs api_name plus sides a and b, each with api_name, object_type_api_name and cardinality (ONE|MANY).",
					},
					"activate":   toolBooleanSchema("Set true to make this the active ontology schema."),
					"deactivate": toolBooleanSchema("Set true to deactivate this schema without deleting it."),
				},
			),
		},
		{
			Name:        "ontology_get",
			Description: "Fetch one ontology schema by ID.",
			InputSchema: toolObjectSchema(
				[]string{"schema_id"},
				map[string]any{
					"schema_id": toolStringSchema("Ontology schema ID."),
				},
			),
		},
		{
			Name:        "ontology_list",
			Description: "List ontology schemas. Use active_only=true to inspect the current validator.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"active_only": toolBooleanSchema("When true, return only the active ontology schema."),
				},
			),
		},
		{
			Name:        "ontology_delete",
			Description: "Delete one ontology schema by ID.",
			InputSchema: toolObjectSchema(
				[]string{"schema_id"},
				map[string]any{
					"schema_id": toolStringSchema("Ontology schema ID."),
				},
			),
		},
	}
}
