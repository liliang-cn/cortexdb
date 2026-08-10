package cortexdb

import (
	"fmt"
	"sort"
)

// OntologyChange is one difference between two schema versions.
type OntologyChange struct {
	Kind     string `json:"kind"`
	Target   string `json:"target"`
	Detail   string `json:"detail"`
	Breaking bool   `json:"breaking"`
}

// OntologyDiff reports what changed between two schema versions, and whether
// any change invalidates data already in the graph.
type OntologyDiff struct {
	Changes            []OntologyChange `json:"changes"`
	HasBreakingChanges bool             `json:"has_breaking_changes"`
}

// DiffOntologySchemas compares two schema versions. "Breaking" means objects
// or edges already written under `before` would no longer validate under
// `after` — which is exactly the class of change that silently corrupts a
// graph if applied without warning.
//
// Both sides are expanded first. Storage keeps a schema exactly as it was
// written, so an object type that names a shared property without restating
// its data type is stored as a bare placeholder; comparing two placeholders
// finds them identical and would hide the most far-reaching change of all —
// retyping a shared property retypes it on every object type that uses it.
//
// The comparison covers object types and link types, the two kinds that
// describe data already on disk. Interfaces, actions and saved object sets
// describe how that data is read and written rather than what shape it has,
// so changing them cannot invalidate a stored node or edge.
func DiffOntologySchemas(before OntologySchema, after OntologySchema) OntologyDiff {
	before = resolveSharedProperties(before)
	after = resolveSharedProperties(after)

	diff := OntologyDiff{Changes: make([]OntologyChange, 0)}

	beforeObjects := indexOntologyObjectTypes(before)
	afterObjects := indexOntologyObjectTypes(after)

	for key, beforeType := range beforeObjects {
		afterType, ok := afterObjects[key]
		if !ok {
			diff.add("object_type_removed", beforeType.APIName,
				"object type removed; existing objects of this type no longer validate", true)
			continue
		}
		diff.diffObjectType(beforeType, afterType)
	}
	for key, afterType := range afterObjects {
		if _, ok := beforeObjects[key]; !ok {
			diff.add("object_type_added", afterType.APIName, "object type added", false)
		}
	}

	beforeLinks := indexOntologyLinkTypes(before)
	afterLinks := indexOntologyLinkTypes(after)

	for key, beforeLink := range beforeLinks {
		afterLink, ok := afterLinks[key]
		if !ok {
			diff.add("link_type_removed", beforeLink.APIName,
				"link type removed; existing edges of this type no longer validate", true)
			continue
		}
		diff.diffLinkType(beforeLink, afterLink)
	}
	for key, afterLink := range afterLinks {
		if _, ok := beforeLinks[key]; !ok {
			diff.add("link_type_added", afterLink.APIName, "link type added", false)
		}
	}

	// The maps above iterate in a random order, so without this the same two
	// schemas would produce a differently ordered report on every call.
	sort.Slice(diff.Changes, func(i, j int) bool {
		if diff.Changes[i].Target != diff.Changes[j].Target {
			return diff.Changes[i].Target < diff.Changes[j].Target
		}
		return diff.Changes[i].Kind < diff.Changes[j].Kind
	})
	return diff
}

func (d *OntologyDiff) add(kind string, target string, detail string, breaking bool) {
	d.Changes = append(d.Changes, OntologyChange{Kind: kind, Target: target, Detail: detail, Breaking: breaking})
	if breaking {
		d.HasBreakingChanges = true
	}
}

func (d *OntologyDiff) diffObjectType(before OntologyObjectType, after OntologyObjectType) {
	if ontologyAPIKey(before.PrimaryKey) != ontologyAPIKey(after.PrimaryKey) {
		d.add("primary_key_changed", before.APIName,
			fmt.Sprintf("primary key %q -> %q re-identifies every existing object", before.PrimaryKey, after.PrimaryKey), true)
	}

	beforeProperties := indexOntologyProperties(before.Properties)
	afterProperties := indexOntologyProperties(after.Properties)

	for key, beforeProperty := range beforeProperties {
		target := before.APIName + "." + beforeProperty.APIName
		afterProperty, ok := afterProperties[key]
		if !ok {
			d.add("property_removed", target,
				"property removed; values stored under it are no longer described by the schema", true)
			continue
		}
		if beforeProperty.DataType.Kind != afterProperty.DataType.Kind {
			d.add("property_type_changed", target,
				fmt.Sprintf("data type %q -> %q; existing values may not parse", beforeProperty.DataType.Kind, afterProperty.DataType.Kind), true)
		}
		switch {
		case !beforeProperty.Required && afterProperty.Required:
			d.add("property_became_required", target,
				"property became required; objects without it no longer validate", true)
		case beforeProperty.Required && !afterProperty.Required:
			d.add("property_became_optional", target, "property became optional", false)
		}
	}
	for key, afterProperty := range afterProperties {
		if _, ok := beforeProperties[key]; ok {
			continue
		}
		target := before.APIName + "." + afterProperty.APIName
		if afterProperty.Required {
			d.add("required_property_added", target,
				"required property added; every existing object is missing it", true)
			continue
		}
		d.add("property_added", target, "optional property added", false)
	}
}

func (d *OntologyDiff) diffLinkType(before OntologyLinkType, after OntologyLinkType) {
	for _, pair := range []struct {
		label  string
		before OntologyLinkSide
		after  OntologyLinkSide
	}{
		{"a", before.A, after.A},
		{"b", before.B, after.B},
	} {
		target := before.APIName + "." + pair.label
		if ontologyAPIKey(pair.before.ObjectTypeAPIName) != ontologyAPIKey(pair.after.ObjectTypeAPIName) {
			d.add("link_side_retargeted", target,
				fmt.Sprintf("side object type %q -> %q; existing edges point at the wrong type", pair.before.ObjectTypeAPIName, pair.after.ObjectTypeAPIName), true)
		}
		switch {
		case pair.before.Cardinality == OntologyCardinalityMany && pair.after.Cardinality == OntologyCardinalityOne:
			d.add("cardinality_tightened", target,
				"cardinality MANY -> ONE; objects with several links no longer validate", true)
		case pair.before.Cardinality == OntologyCardinalityOne && pair.after.Cardinality == OntologyCardinalityMany:
			d.add("cardinality_relaxed", target, "cardinality ONE -> MANY", false)
		}
	}
}

func indexOntologyObjectTypes(schema OntologySchema) map[string]OntologyObjectType {
	index := make(map[string]OntologyObjectType, len(schema.ObjectTypes))
	for _, objectType := range schema.ObjectTypes {
		index[ontologyAPIKey(objectType.APIName)] = objectType
	}
	return index
}

func indexOntologyLinkTypes(schema OntologySchema) map[string]OntologyLinkType {
	index := make(map[string]OntologyLinkType, len(schema.LinkTypes))
	for _, linkType := range schema.LinkTypes {
		index[ontologyAPIKey(linkType.APIName)] = linkType
	}
	return index
}

func indexOntologyProperties(properties []OntologyProperty) map[string]OntologyProperty {
	index := make(map[string]OntologyProperty, len(properties))
	for _, property := range properties {
		index[ontologyAPIKey(property.APIName)] = property
	}
	return index
}
