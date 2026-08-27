package cortexdb

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

// ontologyNodeID derives a graph node ID from an object type and a primary
// key value. Prefixing with the object type is what stops two unrelated
// objects that happen to share a key from colliding, which is exactly what
// the old name-derived IDs could not prevent.
func ontologyNodeID(objectTypeAPIName string, primaryKeyValue string) string {
	return fmt.Sprintf("entity:%s:%s",
		normalizeOntologyIDPart(objectTypeAPIName),
		normalizeOntologyIDPart(primaryKeyValue))
}

func normalizeOntologyIDPart(value string) string {
	lowered := strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range lowered {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('_')
		}
	}
	return b.String()
}

// resolveOntologyPrimaryKeyValue pulls an entity's primary key value out of
// its metadata. When the primary key property doubles as the object's title
// the caller may supply it as the entity name instead, which is the common
// shape coming out of extraction.
func resolveOntologyPrimaryKeyValue(compiled *compiledOntology, objectTypeAPIName string, entity ToolEntityInput) (string, error) {
	objectType, ok := compiled.objectType(objectTypeAPIName)
	if !ok {
		return "", fmt.Errorf("unknown object type %q", objectTypeAPIName)
	}

	primaryKeyKey := ontologyAPIKey(objectType.PrimaryKey)
	for key, value := range entity.Metadata {
		if ontologyAPIKey(key) == primaryKeyKey && strings.TrimSpace(value) != "" {
			return value, nil
		}
	}

	// An entity referenced by its ontology node ID has already stated its
	// primary key, because that ID is derived from it. Extractors hand nodes
	// over that way, with no metadata attached.
	if prefix := ontologyNodeID(objectType.APIName, ""); strings.HasPrefix(entity.ID, prefix) {
		if value := strings.TrimPrefix(entity.ID, prefix); value != "" {
			return value, nil
		}
	}

	if ontologyPrimaryKeyArrivesAsName(objectType) && strings.TrimSpace(entity.Name) != "" {
		return entity.Name, nil
	}

	return "", fmt.Errorf("entity of type %q is missing primary key property %q (supply it in metadata)",
		objectType.APIName, objectType.PrimaryKey)
}

// ontologyEntityNodeID returns the node ID an entity should be written to.
// With an active ontology this is objectType+primaryKey; without one it
// falls back to the legacy name-derived ID so existing graphs keep working.
func ontologyEntityNodeID(compiled *compiledOntology, entity ToolEntityInput) (string, error) {
	if compiled == nil {
		return resolveEntityNodeID(entity.ID, entity.Name), nil
	}
	objectTypeName := firstNonEmpty(entity.Type, "entity")
	objectType, ok := compiled.objectType(objectTypeName)
	if !ok {
		if compiled.vocabularyMode() {
			return resolveEntityNodeID(entity.ID, entity.Name), nil
		}
		return "", fmt.Errorf("%w: ontology does not define object type %q", ErrInvalidOntology, objectTypeName)
	}
	primaryKeyValue, err := resolveOntologyPrimaryKeyValue(compiled, objectType.APIName, entity)
	if err != nil {
		// A vocabulary schema still hands out typed IDs when it can — an entity
		// arriving with its primary key keys to the same node either way — but
		// one that cannot state a key (LLM extraction has none to give) falls
		// back to the name-derived ID instead of being refused.
		if compiled.vocabularyMode() {
			return resolveEntityNodeID(entity.ID, entity.Name), nil
		}
		return "", fmt.Errorf("%w: %w", ErrInvalidOntology, err)
	}
	return ontologyNodeID(objectType.APIName, primaryKeyValue), nil
}

// ontologyCanonicalNodeType returns the spelling an object type should be
// stored under. Node IDs already fold case, so "airport" and "Airport" are one
// object whose node_type would otherwise be whichever spelling wrote last —
// and retrieval, which expands an interface to the *declared* names, would
// then miss it. Unknown types and ontology-free writes pass through untouched.
func ontologyCanonicalNodeType(compiled *compiledOntology, typeName string) string {
	if compiled == nil {
		return typeName
	}
	if objectType, ok := compiled.objectType(typeName); ok {
		return objectType.APIName
	}
	return typeName
}

// ontologyRelationEndpointNodeID resolves a relation endpoint for a write.
// Endpoints are referenced by name or ID only, so with an active ontology the
// node must already exist — its primary key is not recoverable from a name.
func (db *DB) ontologyRelationEndpointNodeID(ctx context.Context, compiled *compiledOntology, endpoint string) (string, error) {
	nodeID, err := db.lookupOntologyRelationEndpointNodeID(ctx, compiled, endpoint)
	if err != nil {
		return "", err
	}
	if nodeID == "" {
		// With a vocabulary schema an unresolved endpoint degrades to the
		// no-ontology behaviour: derive the ID from the name and let the edge
		// write succeed or be rejected on the node's existence, loudly, rather
		// than fail the whole request here.
		if compiled != nil && compiled.vocabularyMode() {
			return resolveEntityNodeID("", endpoint), nil
		}
		return "", fmt.Errorf("%w: relation endpoint %q does not resolve to an existing object; create it first or reference it by node ID",
			ErrInvalidOntology, endpoint)
	}
	return nodeID, nil
}

// lookupOntologyRelationEndpointNodeID is the same resolution without the
// verdict: an endpoint that names nothing comes back as "". Validation needs
// that split so it can report a wrong link type — the more useful complaint —
// before it complains about the endpoint.
func (db *DB) lookupOntologyRelationEndpointNodeID(ctx context.Context, compiled *compiledOntology, endpoint string) (string, error) {
	if compiled == nil {
		return resolveEntityNodeID("", endpoint), nil
	}
	if strings.HasPrefix(endpoint, "entity:") {
		return endpoint, nil
	}
	nodeID, ok, err := db.findOntologyNodeByName(ctx, compiled, endpoint)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	return nodeID, nil
}

func (db *DB) findOntologyNodeByName(ctx context.Context, compiled *compiledOntology, name string) (string, bool, error) {
	// Restricted to entity nodes and ordered, because graph_nodes.content also
	// holds document titles and chunk text: an unordered match could pick a
	// chunk that happens to read the same as the entity, and a different one
	// on the next call.
	//
	// A name can belong to more than one entity, and then ordering by id alone
	// is deterministic without being right. A store holding a prose graph and a
	// code graph had a "Snapshot" of each — the domain's entity and a Go type —
	// and the id that happened to sort first won, attaching what a runbook says
	// about snapshots to a struct. The schema already says which of the two the
	// domain is about, so a declared object type is preferred; among equally
	// declared (or equally undeclared) candidates the id still decides, so a
	// store without an ontology behaves exactly as before.
	rows, err := db.query(ctx,
		`SELECT id, COALESCE(node_type,'') FROM graph_nodes
		 WHERE content = ? AND id LIKE 'entity:%' ORDER BY id`, name)
	if err != nil {
		return "", false, fmt.Errorf("resolve endpoint by name: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var first, declared string
	for rows.Next() {
		var nodeID, nodeType string
		if err := rows.Scan(&nodeID, &nodeType); err != nil {
			return "", false, fmt.Errorf("resolve endpoint by name: %w", err)
		}
		if first == "" {
			first = nodeID
		}
		if declared == "" && compiled != nil {
			if _, ok := compiled.objectType(nodeType); ok {
				declared = nodeID
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", false, fmt.Errorf("resolve endpoint by name: %w", err)
	}
	if declared != "" {
		return declared, true, nil
	}
	if first != "" {
		return first, true, nil
	}
	return "", false, nil
}

// ontologyPrimaryKeyArrivesAsName reports whether an entity's Name field may
// stand in for its primary key. Extraction hands the human-readable label over
// as Name and never as metadata, so a title-shaped primary key would otherwise
// be unsatisfiable. A code-shaped key such as iataCode must not be quietly
// filled from a display name, or "London Heathrow" and "Heathrow Airport"
// would key two different airports again.
func ontologyPrimaryKeyArrivesAsName(objectType OntologyObjectType) bool {
	primaryKeyKey := ontologyAPIKey(objectType.PrimaryKey)
	if titleKey := ontologyAPIKey(objectType.TitleProperty); titleKey != "" {
		return titleKey == primaryKeyKey
	}
	// With no declared title property, only a name-shaped primary key is
	// taken to be the object's human-readable name.
	return primaryKeyKey == "name" || strings.HasSuffix(primaryKeyKey, "name")
}
