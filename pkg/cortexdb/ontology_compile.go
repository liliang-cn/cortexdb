package cortexdb

import "fmt"

// resolveSharedProperties expands object type properties that reference a
// shared property by name alone. A shared property is defined once and reused
// across object types; the local declaration keeps its own Required flag and
// may override any field it states explicitly.
func resolveSharedProperties(schema OntologySchema) OntologySchema {
	if len(schema.SharedProperties) == 0 {
		return schema
	}
	shared := make(map[string]OntologyProperty, len(schema.SharedProperties))
	for _, property := range schema.SharedProperties {
		shared[ontologyAPIKey(property.APIName)] = property
	}

	objectTypes := make([]OntologyObjectType, 0, len(schema.ObjectTypes))
	for _, objectType := range schema.ObjectTypes {
		properties := make([]OntologyProperty, 0, len(objectType.Properties))
		for _, property := range objectType.Properties {
			properties = append(properties, mergeSharedProperty(shared, property))
		}
		objectType.Properties = properties
		objectTypes = append(objectTypes, objectType)
	}
	schema.ObjectTypes = objectTypes

	interfaceTypes := make([]OntologyInterfaceType, 0, len(schema.InterfaceTypes))
	for _, interfaceType := range schema.InterfaceTypes {
		properties := make([]OntologyProperty, 0, len(interfaceType.Properties))
		for _, property := range interfaceType.Properties {
			properties = append(properties, mergeSharedProperty(shared, property))
		}
		interfaceType.Properties = properties
		interfaceTypes = append(interfaceTypes, interfaceType)
	}
	schema.InterfaceTypes = interfaceTypes
	return schema
}

func mergeSharedProperty(shared map[string]OntologyProperty, local OntologyProperty) OntologyProperty {
	definition, ok := shared[ontologyAPIKey(local.APIName)]
	if !ok {
		return local
	}
	// Only fill in what the local declaration left blank. Required is
	// deliberately never inherited: whether a property is mandatory is a
	// per-object-type decision, not a property of the shared definition.
	if local.DataType.Kind == "" {
		local.DataType = definition.DataType
	}
	if local.DisplayName == "" {
		local.DisplayName = definition.DisplayName
	}
	if local.Description == "" {
		local.Description = definition.Description
	}
	if !local.Searchable {
		local.Searchable = definition.Searchable
	}
	if !local.Vectorized {
		local.Vectorized = definition.Vectorized
	}
	return local
}

// compiledOntology is the read-optimised form of a schema. Every validation
// and resolution path goes through it so name lookup rules stay in one place.
type compiledOntology struct {
	schema      OntologySchema
	objectTypes map[string]OntologyObjectType
	linkTypes   map[string]OntologyLinkType
	interfaces  map[string]OntologyInterfaceType
	properties  map[string]map[string]OntologyProperty
}

func compileOntology(schema OntologySchema) *compiledOntology {
	schema = resolveSharedProperties(schema)
	compiled := &compiledOntology{
		schema:      schema,
		objectTypes: make(map[string]OntologyObjectType, len(schema.ObjectTypes)),
		linkTypes:   make(map[string]OntologyLinkType, len(schema.LinkTypes)),
		interfaces:  make(map[string]OntologyInterfaceType, len(schema.InterfaceTypes)),
		properties:  make(map[string]map[string]OntologyProperty, len(schema.ObjectTypes)),
	}

	for _, objectType := range schema.ObjectTypes {
		key := ontologyAPIKey(objectType.APIName)
		compiled.objectTypes[key] = objectType

		byName := make(map[string]OntologyProperty, len(objectType.Properties))
		for _, property := range objectType.Properties {
			byName[ontologyAPIKey(property.APIName)] = property
		}
		compiled.properties[key] = byName
	}
	for _, linkType := range schema.LinkTypes {
		compiled.linkTypes[ontologyAPIKey(linkType.APIName)] = linkType
	}
	for _, interfaceType := range schema.InterfaceTypes {
		compiled.interfaces[ontologyAPIKey(interfaceType.APIName)] = interfaceType
	}
	return compiled
}

func (c *compiledOntology) isEmpty() bool {
	return len(c.objectTypes) == 0 && len(c.linkTypes) == 0 && len(c.interfaces) == 0
}

func (c *compiledOntology) objectType(apiName string) (OntologyObjectType, bool) {
	objectType, ok := c.objectTypes[ontologyAPIKey(apiName)]
	return objectType, ok
}

func (c *compiledOntology) linkType(apiName string) (OntologyLinkType, bool) {
	linkType, ok := c.linkTypes[ontologyAPIKey(apiName)]
	return linkType, ok
}

func (c *compiledOntology) interfaceType(apiName string) (OntologyInterfaceType, bool) {
	interfaceType, ok := c.interfaces[ontologyAPIKey(apiName)]
	return interfaceType, ok
}

func (c *compiledOntology) property(objectTypeAPIName string, propertyAPIName string) (OntologyProperty, bool) {
	byName, ok := c.properties[ontologyAPIKey(objectTypeAPIName)]
	if !ok {
		return OntologyProperty{}, false
	}
	property, ok := byName[ontologyAPIKey(propertyAPIName)]
	return property, ok
}

// orientLink decides which side of a link type a concrete edge runs from and
// to, given the object types of its endpoints. A link type is bidirectional,
// so the endpoint types are what fix the direction.
func (c *compiledOntology) orientLink(linkType OntologyLinkType, fromObjectType string, toObjectType string) (OntologyLinkSide, OntologyLinkSide, error) {
	fromKey := ontologyAPIKey(fromObjectType)
	toKey := ontologyAPIKey(toObjectType)
	aKey := ontologyAPIKey(linkType.A.ObjectTypeAPIName)
	bKey := ontologyAPIKey(linkType.B.ObjectTypeAPIName)

	switch {
	case fromKey == aKey && toKey == bKey:
		return linkType.A, linkType.B, nil
	case fromKey == bKey && toKey == aKey:
		return linkType.B, linkType.A, nil
	default:
		return OntologyLinkSide{}, OntologyLinkSide{}, fmt.Errorf(
			"link type %q connects %s and %s, not %s and %s",
			linkType.APIName, linkType.A.ObjectTypeAPIName, linkType.B.ObjectTypeAPIName,
			fromObjectType, toObjectType)
	}
}
