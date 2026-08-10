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
			Name:        "object_set_resolve",
			Description: "Evaluate an object set and return its members. An object set composes base/interface_base/static sources with filter, search_around (traverse a link side), union, intersect and subtract. Filters support eq/lt/lte/gt/gte/in/is_null/contains/starts_with/contains_all_terms/contains_any_term/nearest_neighbors plus and/or/not. At most 3 chained search_around hops.",
			InputSchema: toolObjectSchema(
				[]string{"object_set"},
				map[string]any{
					"object_set": map[string]any{
						"type":        "object",
						"description": "Object set definition. Requires kind: base|interface_base|static|reference|filter|search_around|union|intersect|subtract. base needs object_type; interface_base needs interface_type; static needs object_ids; reference needs the api name of a saved object set; filter needs source and where; search_around needs source and link (a link side api name); set operations need operands (>=2). A predicate is {op, property, value} or {op, property, values} for in, or {op, operands} for and/or/not; nearest_neighbors also takes k (<=100) and either value (embedded for you) or vector.",
					},
					"limit": toolIntegerSchema("Maximum objects to return. Total is reported separately."),
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
