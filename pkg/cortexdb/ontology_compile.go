package cortexdb

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

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

// vocabularyMode reports whether this schema canonicalizes without gating
// writes. See OntologyEnforcementVocabulary.
func (c *compiledOntology) vocabularyMode() bool {
	return c.schema.Enforcement == OntologyEnforcementVocabulary
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

// actionType walks the declared slice rather than a lookup map: action types
// are few, and a schema that declares none is the common case, so the map
// would cost every compile to save a scan almost nobody makes.
func (c *compiledOntology) actionType(apiName string) (OntologyActionType, bool) {
	key := ontologyAPIKey(apiName)
	for _, action := range c.schema.ActionTypes {
		if ontologyAPIKey(action.APIName) == key {
			return action, true
		}
	}
	return OntologyActionType{}, false
}

func (c *compiledOntology) property(objectTypeAPIName string, propertyAPIName string) (OntologyProperty, bool) {
	byName, ok := c.properties[ontologyAPIKey(objectTypeAPIName)]
	if !ok {
		return OntologyProperty{}, false
	}
	property, ok := byName[ontologyAPIKey(propertyAPIName)]
	return property, ok
}

// ontologyLinkTraversal is one hop: the link type to follow, the side a
// traversal starts from, and the side it lands on.
type ontologyLinkTraversal struct {
	linkType OntologyLinkType
	near     OntologyLinkSide
	far      OntologyLinkSide
}

// linkTraversalsBySide finds every hop a traversal side name could denote.
// Side names are unique per object type but not globally — two link types may
// each expose an "origin" from different object types — so every match is
// returned and the caller walks them together, letting the object types on the
// far end sort out which one applies.
func (c *compiledOntology) linkTraversalsBySide(sideAPIName string) []ontologyLinkTraversal {
	key := ontologyAPIKey(sideAPIName)
	// Over the declared slice rather than the lookup map, so two schemas that
	// differ only in declaration order do not resolve hops in different orders.
	traversals := make([]ontologyLinkTraversal, 0, 1)
	for _, linkType := range c.schema.LinkTypes {
		switch key {
		case ontologyAPIKey(linkType.A.APIName):
			traversals = append(traversals, ontologyLinkTraversal{linkType, linkType.A, linkType.B})
		case ontologyAPIKey(linkType.B.APIName):
			traversals = append(traversals, ontologyLinkTraversal{linkType, linkType.B, linkType.A})
		}
	}
	return traversals
}

// orientLink decides which side of a link type a concrete edge runs from and
// to, given the object types of its endpoints. A link type is bidirectional,
// so the endpoint types are what fix the direction.
//
// A side may name an interface, so the match is against the object types the
// side stands for rather than against its name. Comparing names would reject
// every edge of a polymorphic relation: a Snapshot endpoint is not spelled
// "Protector", and the write would be refused as connecting the wrong types.
// Partial overlap between the two sides is rejected at validation, so at most
// one of these arms can be the honest reading.
func (c *compiledOntology) orientLink(linkType OntologyLinkType, fromObjectType string, toObjectType string) (OntologyLinkSide, OntologyLinkSide, error) {
	fromKey := ontologyAPIKey(fromObjectType)
	toKey := ontologyAPIKey(toObjectType)
	a := c.typeClosureKeys(linkType.A.ObjectTypeAPIName)
	b := c.typeClosureKeys(linkType.B.ObjectTypeAPIName)

	_, fromIsA := a[fromKey]
	_, toIsB := b[toKey]
	_, fromIsB := b[fromKey]
	_, toIsA := a[toKey]

	switch {
	case fromIsA && toIsB:
		return linkType.A, linkType.B, nil
	case fromIsB && toIsA:
		return linkType.B, linkType.A, nil
	default:
		return OntologyLinkSide{}, OntologyLinkSide{}, fmt.Errorf(
			"link type %q connects %s and %s, not %s and %s",
			linkType.APIName, linkType.A.ObjectTypeAPIName, linkType.B.ObjectTypeAPIName,
			fromObjectType, toObjectType)
	}
}

// vectorizedText spells out the text an object's Vectorized properties hold,
// in a stable order, or "" when its type declares none. The name leads so an
// object with an empty vectorized value still embeds as itself.
func vectorizedText(compiled *compiledOntology, entity ToolEntityInput) string {
	if compiled == nil || strings.TrimSpace(entity.Type) == "" {
		return ""
	}
	byName, ok := compiled.properties[ontologyAPIKey(entity.Type)]
	if !ok {
		return ""
	}
	names := make([]string, 0, len(byName))
	for name, property := range byName {
		if property.Vectorized {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names)+1)
	if name := strings.TrimSpace(entity.Name); name != "" {
		parts = append(parts, name)
	}
	for _, name := range names {
		for key, value := range entity.Metadata {
			if ontologyAPIKey(key) == name && strings.TrimSpace(value) != "" {
				parts = append(parts, strings.TrimSpace(value))
			}
		}
	}
	return strings.Join(parts, "\n")
}

// embedVectorizedEntities returns, by index, an embedding for every entity
// whose object type declares a Vectorized property — the write side of the
// nearest_neighbors predicate.
//
// The flag used to be stored, validated and inherited, and nothing ever
// embedded the property: every entity node carried a lexical hash vector, so
// the predicate compared a real embedding of the query against hash vectors
// and matched nothing. With no embedder there is nothing to embed with and the
// map is empty; the caller keeps the lexical vector, and the write goes
// through. A dimension that does not match the store's is an operator's
// misconfiguration and is refused rather than padded, because a vector of the
// wrong width in the graph index is a node that can never be found.
func (db *DB) embedVectorizedEntities(ctx context.Context, compiled *compiledOntology, entities []ToolEntityInput, vectorDim int) (map[int][]float32, error) {
	out := make(map[int][]float32)
	if db.embedder == nil || compiled == nil {
		return out, nil
	}
	indexes := make([]int, 0)
	texts := make([]string, 0)
	for i, entity := range entities {
		if text := vectorizedText(compiled, entity); text != "" {
			indexes = append(indexes, i)
			texts = append(texts, text)
		}
	}
	if len(texts) == 0 {
		return out, nil
	}
	vectors, err := db.embedder.EmbedBatch(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed vectorized properties: %w", err)
	}
	if len(vectors) != len(texts) {
		return nil, fmt.Errorf("embed vectorized properties: got %d vectors for %d texts", len(vectors), len(texts))
	}
	for n, i := range indexes {
		if len(vectors[n]) != vectorDim {
			return nil, fmt.Errorf("embed vectorized properties: embedder returned %d dimensions, the store holds %d", len(vectors[n]), vectorDim)
		}
		out[i] = vectors[n]
	}
	return out, nil
}
