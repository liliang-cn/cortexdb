package cortexdb

import (
	"context"
	"database/sql"
	"errors"
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
		return "", fmt.Errorf("%w: ontology does not define object type %q", ErrInvalidOntology, objectTypeName)
	}
	primaryKeyValue, err := resolveOntologyPrimaryKeyValue(compiled, objectType.APIName, entity)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidOntology, err)
	}
	return ontologyNodeID(objectType.APIName, primaryKeyValue), nil
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
	nodeID, ok, err := db.findOntologyNodeByName(ctx, endpoint)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	return nodeID, nil
}

func (db *DB) findOntologyNodeByName(ctx context.Context, name string) (string, bool, error) {
	// Restricted to entity nodes and ordered, because graph_nodes.content also
	// holds document titles and chunk text: an unordered match could pick a
	// chunk that happens to read the same as the entity, and a different one
	// on the next call.
	row := db.store.GetDB().QueryRowContext(ctx,
		`SELECT id FROM graph_nodes WHERE content = ? AND id LIKE 'entity:%' ORDER BY id LIMIT 1`, name)
	var nodeID string
	switch err := row.Scan(&nodeID); {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("resolve endpoint by name: %w", err)
	default:
		return nodeID, true, nil
	}
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
