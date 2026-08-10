package cortexdb

import (
	"fmt"
	"sort"
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
