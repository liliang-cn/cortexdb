package cortexdb

func ontologyToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "ontology_save",
			Mutates:     true,
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
			Description: "Fetch one ontology schema by ID: the current stored one, or with version a stored earlier version's definition, or with as_of the schema that was ACTIVE at that instant (its version is in the result, with the activation interval). version and as_of are exclusive; a malformed as_of is refused, never read as now.",
			InputSchema: toolObjectSchema(
				[]string{"schema_id"},
				map[string]any{
					"schema_id": toolStringSchema("Ontology schema ID. With as_of it may be left empty to mean whichever schema was active then."),
					"version":   toolIntegerSchema("Return this stored version's definition instead of the current one."),
					"as_of":     toolStringSchema("RFC 3339 instant (e.g. 2026-03-05T15:00:00Z). Return the schema version that was active then."),
				},
			),
		},
		{
			Name:        "ontology_history",
			Description: "List every stored version of an ontology schema, newest first (oldest_first=true reverses), each with who saved it and when, a summary of what it changed against the version before (change counts, breaking count, by kind), and the intervals during which it was the active validator (active_from, active_to, who switched it, why it ended). Versions survive deletion of the schema. Read a version's definition with ontology_get(version); compare two with ontology_diff(from_version, to_version). To roll back, ontology_save the old definition: that appends a new version, history is never rewritten.",
			InputSchema: toolObjectSchema(
				[]string{"schema_id"},
				map[string]any{
					"schema_id":    toolStringSchema("Ontology schema ID."),
					"limit":        toolIntegerSchema("Versions per page (default 50, max 500)."),
					"offset":       toolIntegerSchema("Versions to skip, for paging."),
					"oldest_first": toolBooleanSchema("List oldest version first instead of newest."),
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
			Name:        "ontology_diff",
			Description: "Compare a candidate ontology schema against the stored schema of the same ID and report what changed, flagging the changes that would invalidate objects or edges already in the graph: a removed object or link type, a removed property, a changed property data type, a property that became required, a new required property, a changed primary key, a retargeted link side, and a cardinality tightened from MANY to ONE. Run this before ontology_save when a schema is already in use. Instead of a candidate, give from_version (and optionally to_version, default the latest) to compare two stored versions of the schema.",
			InputSchema: toolObjectSchema(
				[]string{"schema_id"},
				map[string]any{
					"schema_id": toolStringSchema("ID of the stored schema to compare against. It is the 'before' side."),
					"candidate": map[string]any{
						"type":        "object",
						"description": "The proposed schema, in the same shape ontology_save takes. Required unless from_version is given; not allowed with it.",
					},
					"from_version": toolIntegerSchema("Compare two stored versions: the 'before' version."),
					"to_version":   toolIntegerSchema("The 'after' version; omitted means the schema's latest."),
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
			Name:        "ontology_action_list",
			Description: "List the action types callable on the active ontology, with their parameters, rules and submission criteria. When the schema sets strict_actions, these are the only writes allowed.",
			InputSchema: toolObjectSchema(nil, map[string]any{}),
		},
		{
			Name: "ontology_action_apply",
			// validate_only does not write, but the default does, and every
			// applied action also appends to the audit trail. Classified by
			// what the tool can do, not by the argument that might stop it.
			Mutates:     true,
			Description: "Run one action type. Set validate_only to check parameters and submission criteria without writing, or return_edits to get the graph edits back (the two are mutually exclusive). Every applied action is recorded in an audit trail.",
			InputSchema: toolObjectSchema(
				[]string{"action"},
				map[string]any{
					"action":        toolStringSchema("Action type api name."),
					"parameters":    toolMapSchema("Action parameter values, keyed by parameter api name. Values are strings; they are parsed against each parameter's declared data type."),
					"validate_only": toolBooleanSchema("Validate without writing. Checks parameters and submission criteria only — it never consults the graph, so it does not check primary key uniqueness."),
					"return_edits":  toolBooleanSchema("Return the graph edits the action made."),
					"actor":         toolStringSchema("Who is running the action; recorded in the audit trail and resolves current_user value sources."),
				},
			),
		},
		{
			Name: "ontology_draft",
			// Reads the graph's vocabulary and hands back a proposal. It
			// stores nothing — the point of the tool is that a person, not a
			// deriver, decides what the ontology says.
			Description: "Propose a first ontology schema from what the graph already contains, with the reasoning and the open questions beside it. Returns three things: the draft schema (enforcement=vocabulary, every type experimental, nothing active), a report saying which rule bucketed each node type as domain / bookkeeping / unclassified and why each edge type was included or excluded, and a to-decide list — spelling collisions that were NOT merged, primary keys that are guesses, and relations whose one-ness looks like cardinality and is not evidence of it. Saves nothing: pass the corrected schema to ontology_save.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"schema_id":         toolStringSchema("ID to give the draft (default \"draft\"). Nothing is saved under it; it is for comparing drafts and for ontology_diff."),
					"min_nodes":         toolIntegerSchema("Keep node types with fewer nodes than this out of the draft. They stay in the report."),
					"min_edges":         toolIntegerSchema("Keep edge types with fewer edges than this out of the draft. They stay in the report."),
					"domain_types":      toolStringArraySchema("Node types to treat as real-world object types whatever the rules say."),
					"bookkeeping_types": toolStringArraySchema("Node types to treat as record kinds whatever the rules say."),
				},
			),
		},
		{
			Name:        "ontology_delete",
			Mutates:     true,
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
