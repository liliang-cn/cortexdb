package cortexdb

import (
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

	if ontologyPrimaryKeyArrivesAsName(objectType) && strings.TrimSpace(entity.Name) != "" {
		return entity.Name, nil
	}

	return "", fmt.Errorf("entity of type %q is missing primary key property %q (supply it in metadata)",
		objectType.APIName, objectType.PrimaryKey)
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
