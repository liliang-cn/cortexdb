package cortexdb

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ontologyAPINamePattern mirrors Foundry API names: an ASCII letter followed
// by letters, digits or underscores. Unlike the v1 normalizer this does not
// lowercase or rewrite the name, so Airport and airport stay distinguishable
// in display while still resolving to the same lookup key.
var ontologyAPINamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

func validateOntologyAPIName(kind string, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s api name is required", kind)
	}
	if !ontologyAPINamePattern.MatchString(name) {
		return fmt.Errorf("%s api name %q must match [A-Za-z][A-Za-z0-9_]*", kind, name)
	}
	return nil
}

// ontologyAPIKey is the case-insensitive lookup key for an API name.
func ontologyAPIKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// normalizeOntologyLabel is the legacy label canonicaliser retained for the
// RDFS inference engine, which folds free-form relation type labels ("works
// at", "Works-At") onto one key. It is deliberately distinct from
// ontologyAPIKey: ontology v2 API names are already constrained to a strict
// pattern and only need case folding, never rewriting.
func normalizeOntologyLabel(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.Join(strings.Fields(value), "_")
	return value
}

// parseOntologyPropertyValue checks that raw, as carried in a metadata map,
// is a legal value for the declared data type. Values arrive as strings
// because ToolEntityInput.Metadata is map[string]string.
func parseOntologyPropertyValue(dataType OntologyDataType, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("value is empty")
	}
	switch dataType.Kind {
	case OntologyDataString, OntologyDataMarking, OntologyDataGeoShape:
		return nil
	case OntologyDataInteger, OntologyDataLong:
		if _, err := strconv.ParseInt(raw, 10, 64); err != nil {
			return fmt.Errorf("value %q is not an %s", raw, dataType.Kind)
		}
	case OntologyDataDouble, OntologyDataDecimal:
		if _, err := strconv.ParseFloat(raw, 64); err != nil {
			return fmt.Errorf("value %q is not a %s", raw, dataType.Kind)
		}
	case OntologyDataBoolean:
		if raw != "true" && raw != "false" {
			return fmt.Errorf("value %q is not a boolean (want \"true\" or \"false\")", raw)
		}
	case OntologyDataDate:
		if _, err := time.Parse("2006-01-02", raw); err != nil {
			return fmt.Errorf("value %q is not a date (want YYYY-MM-DD)", raw)
		}
	case OntologyDataTimestamp:
		if _, err := time.Parse(time.RFC3339, raw); err != nil {
			return fmt.Errorf("value %q is not a timestamp (want RFC3339)", raw)
		}
	case OntologyDataGeoPoint:
		parts := strings.Split(raw, ",")
		if len(parts) != 2 {
			return fmt.Errorf("value %q is not a geopoint (want \"lat,lon\")", raw)
		}
		for _, part := range parts {
			if _, err := strconv.ParseFloat(strings.TrimSpace(part), 64); err != nil {
				return fmt.Errorf("value %q is not a geopoint (want \"lat,lon\")", raw)
			}
		}
	case OntologyDataVector, OntologyDataArray, OntologyDataStruct:
		// Composite values are carried out of band, not in the string
		// metadata map; presence is all that can be checked here.
		return nil
	default:
		return fmt.Errorf("unknown data type kind %q", dataType.Kind)
	}
	return nil
}
