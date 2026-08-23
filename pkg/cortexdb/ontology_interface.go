package cortexdb

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// interfaceClosure returns the API keys of an interface and every interface
// it extends, transitively. Foundry allows an interface to extend several
// parents, and object types to implement several interfaces, so a plain
// parent pointer is not enough.
func (c *compiledOntology) interfaceClosure(apiName string) map[string]struct{} {
	closure := make(map[string]struct{})
	c.collectInterfaceClosure(apiName, closure)
	return closure
}

func (c *compiledOntology) collectInterfaceClosure(apiName string, closure map[string]struct{}) {
	key := ontologyAPIKey(apiName)
	// The seen check is also what stops a cyclic schema from recursing
	// forever. Validation rejects cycles, but this runs on stored schemas too.
	if _, seen := closure[key]; seen {
		return
	}
	interfaceType, ok := c.interfaceType(apiName)
	if !ok {
		return
	}
	closure[key] = struct{}{}
	for _, parent := range interfaceType.Extends {
		c.collectInterfaceClosure(parent, closure)
	}
}

// implementingObjectTypes returns the declared API names of every object type
// that implements the interface, directly or through interface inheritance.
func (c *compiledOntology) implementingObjectTypes(interfaceAPIName string) []string {
	if _, ok := c.interfaceType(interfaceAPIName); !ok {
		return nil
	}
	target := ontologyAPIKey(interfaceAPIName)

	names := make([]string, 0, len(c.objectTypes))
	for _, objectType := range c.objectTypes {
		for _, implemented := range objectType.Implements {
			if _, ok := c.interfaceClosure(implemented)[target]; ok {
				names = append(names, objectType.APIName)
				break
			}
		}
	}
	// Sorted because the source is a map: an unsorted result would reorder a
	// caller's type filter, and with it the order of retrieved nodes.
	sort.Strings(names)
	return names
}

// resolveTypeClosure turns a type name used in a query into the concrete
// object type names it should match. An interface expands to its
// implementors; anything else passes through unchanged so callers can keep
// querying by object type or by a legacy node type.
func (c *compiledOntology) resolveTypeClosure(typeAPIName string) []string {
	if _, ok := c.interfaceType(typeAPIName); ok {
		return c.implementingObjectTypes(typeAPIName)
	}
	if objectType, ok := c.objectType(typeAPIName); ok {
		return []string{objectType.APIName}
	}
	return []string{typeAPIName}
}

// expandOntologyTypeFilter rewrites a caller-supplied list of type names into
// the concrete object types to match on, so a query for an interface hits
// every implementor. Without an active ontology the list passes through, which
// is what keeps ontology-free deployments retrieving exactly as before.
func (db *DB) expandOntologyTypeFilter(ctx context.Context, typeNames []string) ([]string, error) {
	if len(typeNames) == 0 {
		return typeNames, nil
	}
	compiled, err := db.activeCompiledOntology(ctx)
	if err != nil || compiled == nil {
		return typeNames, err
	}

	seen := make(map[string]struct{}, len(typeNames))
	expanded := make([]string, 0, len(typeNames))
	for _, typeName := range typeNames {
		for _, resolved := range compiled.resolveTypeClosure(typeName) {
			key := ontologyAPIKey(resolved)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			expanded = append(expanded, resolved)
		}
	}
	return expanded, nil
}

// typeClosureKeys is resolveTypeClosure as a lookup set: the concrete object
// types a name stands for, api-key folded.
func (c *compiledOntology) typeClosureKeys(typeAPIName string) map[string]struct{} {
	resolved := c.resolveTypeClosure(typeAPIName)
	keys := make(map[string]struct{}, len(resolved))
	for _, name := range resolved {
		keys[ontologyAPIKey(name)] = struct{}{}
	}
	return keys
}

// validateOntologyLinkSideNames checks that a side name identifies one hop from
// every concrete object type it is reachable on.
//
// A side api name is how a traversal names the hop it wants to take, so it has
// to identify one link type unambiguously from the object type it starts at.
// Two link types exposing the same side name on the same object type would make
// that lookup a coin flip.
//
// This used to compare the declared side owners directly, which was enough while
// an owner was always one object type. An interface owner makes the overlap
// indirect: a link hanging "protects" off the Protector interface and another
// hanging "protects" off Snapshot do not collide by name, but a traversal
// starting at a Snapshot — which implements Protector — matches both. So the
// comparison is over the closure, and it runs after the schema is compiled.
func validateOntologyLinkSideNames(schema OntologySchema, compiled *compiledOntology) error {
	// owner object type key → side name key → the link type that claimed it.
	claimed := make(map[string]map[string]string, len(schema.ObjectTypes))
	for _, linkType := range schema.LinkTypes {
		for _, side := range []OntologyLinkSide{linkType.A, linkType.B} {
			sideKey := ontologyAPIKey(side.APIName)
			// Sorted so a schema with two collisions always reports the same
			// one; the closure comes back sorted, and map order would make the
			// error flap between runs.
			for _, owner := range compiled.resolveTypeClosure(side.ObjectTypeAPIName) {
				ownerKey := ontologyAPIKey(owner)
				if claimed[ownerKey] == nil {
					claimed[ownerKey] = make(map[string]string, 2)
				}
				if other, exists := claimed[ownerKey][sideKey]; exists && other != linkType.APIName {
					return fmt.Errorf("link types %q and %q both expose side %q on object type %q",
						other, linkType.APIName, side.APIName, owner)
				}
				claimed[ownerKey][sideKey] = linkType.APIName
			}
		}
	}
	return nil
}

// validateOntologyLinkEnds rejects a link whose two ends overlap without being
// identical.
//
// A link type is bidirectional and the edge is stored in whichever direction it
// was asserted, so an edge's direction is recovered by matching its endpoint
// types against the two sides. That works when the sides are disjoint, and when
// they are the same type — a self-link like `conflicts_with`, where either
// orientation is the same statement. It does not work in between: with A over
// {Snapshot, Backup} and B over {Backup, Volume}, an edge between two Backups
// matches both readings, and which one wins is decided by the order the sides
// happen to be tried. Partial overlap is only reachable through interfaces,
// which is why this arrives with them.
func validateOntologyLinkEnds(schema OntologySchema, compiled *compiledOntology) error {
	for _, linkType := range schema.LinkTypes {
		a := compiled.typeClosureKeys(linkType.A.ObjectTypeAPIName)
		b := compiled.typeClosureKeys(linkType.B.ObjectTypeAPIName)

		shared := make([]string, 0, len(a))
		for key := range a {
			if _, both := b[key]; both {
				shared = append(shared, key)
			}
		}
		if len(shared) == 0 || (len(shared) == len(a) && len(shared) == len(b)) {
			continue
		}
		sort.Strings(shared)
		return fmt.Errorf("link type %q has ends %q and %q that overlap on %s without being the same set: an edge between two of those has no unambiguous direction",
			linkType.APIName, linkType.A.ObjectTypeAPIName, linkType.B.ObjectTypeAPIName, strings.Join(shared, ", "))
	}
	return nil
}

// validateOntologyInterfaces checks the interface graph is acyclic and that
// every implementor actually satisfies the shape it claims.
func validateOntologyInterfaces(schema OntologySchema, compiled *compiledOntology) error {
	for _, interfaceType := range schema.InterfaceTypes {
		for _, parent := range interfaceType.Extends {
			if _, ok := compiled.interfaceType(parent); !ok {
				// Left unchecked this is silent: the closure skips the missing
				// parent, so its required properties go unenforced on every
				// implementor rather than the schema being rejected.
				return fmt.Errorf("interface type %q extends unknown interface %q", interfaceType.APIName, parent)
			}
		}
	}
	if err := checkInterfaceCycles(compiled); err != nil {
		return err
	}

	for _, objectType := range schema.ObjectTypes {
		required := map[string]OntologyProperty{}
		for _, implemented := range objectType.Implements {
			for key := range compiled.interfaceClosure(implemented) {
				interfaceType, ok := compiled.interfaces[key]
				if !ok {
					continue
				}
				for _, property := range interfaceType.Properties {
					if property.Required {
						required[ontologyAPIKey(property.APIName)] = property
					}
				}
			}
		}

		// Sorted so an object type breaking two interface contracts always
		// names the same one; map order would make the error flap between runs.
		for _, key := range sortedMapKeys(required) {
			interfaceProperty := required[key]
			objectProperty, ok := compiled.property(objectType.APIName, key)
			if !ok {
				return fmt.Errorf("object type %q implements an interface requiring property %q but does not declare it",
					objectType.APIName, interfaceProperty.APIName)
			}
			if objectProperty.DataType.Kind != interfaceProperty.DataType.Kind {
				return fmt.Errorf("object type %q property %q is %q but the interface requires %q",
					objectType.APIName, interfaceProperty.APIName,
					objectProperty.DataType.Kind, interfaceProperty.DataType.Kind)
			}
		}
	}
	return nil
}

func checkInterfaceCycles(compiled *compiledOntology) error {
	const (
		unvisited = 0
		inStack   = 1
		done      = 2
	)
	state := make(map[string]int, len(compiled.interfaces))

	var visit func(key string) error
	visit = func(key string) error {
		switch state[key] {
		case inStack:
			return fmt.Errorf("interface inheritance cycle detected at %q", key)
		case done:
			return nil
		}
		state[key] = inStack
		if interfaceType, ok := compiled.interfaces[key]; ok {
			for _, parent := range interfaceType.Extends {
				if err := visit(ontologyAPIKey(parent)); err != nil {
					return err
				}
			}
		}
		state[key] = done
		return nil
	}

	// Sorted so a schema with two cycles always reports the same one.
	keys := make([]string, 0, len(compiled.interfaces))
	for key := range compiled.interfaces {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}
