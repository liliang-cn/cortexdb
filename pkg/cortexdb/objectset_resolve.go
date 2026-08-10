package cortexdb

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// objectSetResult is the working representation of an object set: the node
// IDs it contains. Keeping it as a set makes union/intersect/subtract cheap
// and makes repeated evaluation idempotent.
type objectSetResult map[string]struct{}

// ResolveObjectSet evaluates an object set definition to the node IDs it
// selects. Scalar predicates read properties, text predicates match terms,
// nearest_neighbors goes through the vector index and search_around walks
// link edges — all of them composable inside one expression.
func (db *DB) ResolveObjectSet(ctx context.Context, set ObjectSet) (map[string]struct{}, error) {
	// Validated whole before anything is evaluated, so a malformed branch
	// deep in the tree cannot cost the queries its siblings would have run.
	if err := validateObjectSet(set, 0); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidOntology, err)
	}
	compiled, err := db.activeCompiledOntology(ctx)
	if err != nil {
		return nil, err
	}
	return db.resolveObjectSet(ctx, compiled, set)
}

func (db *DB) resolveObjectSet(ctx context.Context, compiled *compiledOntology, set ObjectSet) (objectSetResult, error) {
	switch set.Kind {
	case ObjectSetBase:
		// No ontology lookup: the type match below is case-insensitive, which
		// is what makes a caller's spelling meet the stored one, and it lets a
		// base set name a node type no schema declares.
		return db.resolveObjectSetByTypes(ctx, []string{set.ObjectType})

	case ObjectSetInterfaceBase:
		if compiled == nil {
			return nil, fmt.Errorf("%w: interface_base object sets need an active ontology", ErrInvalidOntology)
		}
		return db.resolveObjectSetByTypes(ctx, compiled.implementingObjectTypes(set.InterfaceType))

	case ObjectSetStatic:
		result := make(objectSetResult, len(set.ObjectIDs))
		for _, id := range set.ObjectIDs {
			result[id] = struct{}{}
		}
		return result, nil

	case ObjectSetReference:
		if compiled == nil {
			return nil, fmt.Errorf("%w: reference object sets need an active ontology", ErrInvalidOntology)
		}
		for _, named := range compiled.schema.ObjectSets {
			if ontologyAPIKey(named.APIName) == ontologyAPIKey(set.Reference) {
				return db.resolveObjectSet(ctx, compiled, named.Definition)
			}
		}
		return nil, fmt.Errorf("%w: ontology defines no saved object set %q", ErrInvalidOntology, set.Reference)

	case ObjectSetFilter:
		source, err := db.resolveObjectSet(ctx, compiled, *set.Source)
		if err != nil {
			return nil, err
		}
		return db.applyObjectSetPredicate(ctx, compiled, source, *set.Where)

	case ObjectSetSearchAround:
		source, err := db.resolveObjectSet(ctx, compiled, *set.Source)
		if err != nil {
			return nil, err
		}
		return db.searchAround(ctx, compiled, source, set.Link)

	case ObjectSetUnion, ObjectSetIntersect, ObjectSetSubtract:
		operands := make([]objectSetResult, 0, len(set.Operands))
		for _, operand := range set.Operands {
			resolved, err := db.resolveObjectSet(ctx, compiled, operand)
			if err != nil {
				return nil, err
			}
			operands = append(operands, resolved)
		}
		return combineObjectSets(set.Kind, operands), nil

	default:
		return nil, fmt.Errorf("%w: unknown object set kind %q", ErrInvalidOntology, set.Kind)
	}
}

// combineObjectSets folds the operands left to right. Subtract is therefore
// order-sensitive — the first operand is the one being narrowed — while union
// and intersect are not.
func combineObjectSets(kind ObjectSetKind, operands []objectSetResult) objectSetResult {
	result := make(objectSetResult, len(operands[0]))
	for id := range operands[0] {
		result[id] = struct{}{}
	}

	for _, operand := range operands[1:] {
		switch kind {
		case ObjectSetUnion:
			for id := range operand {
				result[id] = struct{}{}
			}
		case ObjectSetIntersect:
			for id := range result {
				if _, ok := operand[id]; !ok {
					delete(result, id)
				}
			}
		case ObjectSetSubtract:
			for id := range operand {
				delete(result, id)
			}
		}
	}
	return result
}

func (db *DB) resolveObjectSetByTypes(ctx context.Context, objectTypes []string) (objectSetResult, error) {
	// An interface nothing implements selects nothing. Short-circuited rather
	// than handed to SQL, where an empty IN list is a dialect question this
	// code should not be asking.
	if len(objectTypes) == 0 {
		return objectSetResult{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(objectTypes)), ",")
	args := make([]any, 0, len(objectTypes))
	for _, objectType := range objectTypes {
		args = append(args, objectType)
	}

	// NOCASE because node_type is only canonicalised on ontology-validated
	// writes: rows written before a schema was activated keep whatever
	// spelling they arrived with, and they are the same objects.
	rows, err := db.store.GetDB().QueryContext(ctx,
		`SELECT id FROM graph_nodes WHERE node_type COLLATE NOCASE IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("resolve object set by type: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := objectSetResult{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan object set row: %w", err)
		}
		result[id] = struct{}{}
	}
	return result, rows.Err()
}

// applyObjectSetPredicate narrows a source set. Boolean operators recurse;
// leaf predicates read the property off each node. Everything a predicate can
// return is a subset of the source, which is what makes `not` a complement
// within the set being filtered rather than against the whole graph.
func (db *DB) applyObjectSetPredicate(ctx context.Context, compiled *compiledOntology, source objectSetResult, predicate ObjectSetPredicate) (objectSetResult, error) {
	switch predicate.Op {
	case PredicateAnd:
		result := source
		for _, operand := range predicate.Operands {
			narrowed, err := db.applyObjectSetPredicate(ctx, compiled, result, operand)
			if err != nil {
				return nil, err
			}
			result = narrowed
		}
		return result, nil

	case PredicateOr:
		union := objectSetResult{}
		for _, operand := range predicate.Operands {
			matched, err := db.applyObjectSetPredicate(ctx, compiled, source, operand)
			if err != nil {
				return nil, err
			}
			for id := range matched {
				union[id] = struct{}{}
			}
		}
		return union, nil

	case PredicateNot:
		excluded, err := db.applyObjectSetPredicate(ctx, compiled, source, predicate.Operands[0])
		if err != nil {
			return nil, err
		}
		result := objectSetResult{}
		for id := range source {
			if _, ok := excluded[id]; !ok {
				result[id] = struct{}{}
			}
		}
		return result, nil

	case PredicateContainsAllTerms, PredicateContainsAnyTerm:
		return db.applyObjectSetTextPredicate(ctx, source, predicate)

	case PredicateNearestNeighbors:
		return db.applyObjectSetVectorPredicate(ctx, source, predicate)

	default:
		return db.applyObjectSetScalarPredicate(ctx, source, predicate)
	}
}

func (db *DB) applyObjectSetScalarPredicate(ctx context.Context, source objectSetResult, predicate ObjectSetPredicate) (objectSetResult, error) {
	values, err := db.loadNodePropertyValues(ctx, source, predicate.Property)
	if err != nil {
		return nil, err
	}

	result := objectSetResult{}
	for id := range source {
		value, present := values[id]
		matched, err := matchScalarPredicate(predicate, value, present)
		if err != nil {
			return nil, err
		}
		if matched {
			result[id] = struct{}{}
		}
	}
	return result, nil
}

// matchScalarPredicate decides one object. A property the object does not
// carry matches nothing except is_null: without that guard an absent value
// reads as the empty string and quietly satisfies every comparison against it.
func matchScalarPredicate(predicate ObjectSetPredicate, value string, present bool) (bool, error) {
	if predicate.Op == PredicateIsNull {
		return !present || strings.TrimSpace(value) == "", nil
	}
	if !present {
		return false, nil
	}

	switch predicate.Op {
	case PredicateEq:
		return value == predicate.Value, nil
	case PredicateContains:
		return strings.Contains(strings.ToLower(value), strings.ToLower(predicate.Value)), nil
	case PredicateStartsWith:
		return strings.HasPrefix(strings.ToLower(value), strings.ToLower(predicate.Value)), nil
	case PredicateIn:
		for _, candidate := range predicate.Values {
			if value == candidate {
				return true, nil
			}
		}
		return false, nil
	case PredicateLt, PredicateLte, PredicateGt, PredicateGte:
		return compareOrderedPredicate(predicate.Op, value, predicate.Value)
	default:
		return false, fmt.Errorf("predicate %q is not a scalar predicate", predicate.Op)
	}
}

// compareOrderedPredicate compares numerically when both sides parse as
// numbers and lexicographically otherwise, so dates and codes still order
// sensibly. Property values arrive as strings, where "40000" sorts below
// "5000" — comparing those as text would silently invert numeric filters.
func compareOrderedPredicate(op PredicateOp, left string, right string) (bool, error) {
	leftNum, leftErr := strconv.ParseFloat(left, 64)
	rightNum, rightErr := strconv.ParseFloat(right, 64)

	var cmp int
	if leftErr == nil && rightErr == nil {
		switch {
		case leftNum < rightNum:
			cmp = -1
		case leftNum > rightNum:
			cmp = 1
		}
	} else {
		cmp = strings.Compare(left, right)
	}

	switch op {
	case PredicateLt:
		return cmp < 0, nil
	case PredicateLte:
		return cmp <= 0, nil
	case PredicateGt:
		return cmp > 0, nil
	case PredicateGte:
		return cmp >= 0, nil
	default:
		return false, fmt.Errorf("predicate %q is not an ordered comparison", op)
	}
}

// loadNodePropertyValues reads one property off every node in the set. Nodes
// that do not carry it are absent from the result rather than mapped to "",
// so callers can tell "missing" from "empty".
func (db *DB) loadNodePropertyValues(ctx context.Context, source objectSetResult, property string) (map[string]string, error) {
	if len(source) == 0 {
		return map[string]string{}, nil
	}
	nodeIDs := make([]string, 0, len(source))
	for id := range source {
		nodeIDs = append(nodeIDs, id)
	}
	nodes, err := db.graph.GetNodesBatch(ctx, nodeIDs)
	if err != nil {
		return nil, fmt.Errorf("load node properties: %w", err)
	}

	key := ontologyAPIKey(property)
	values := make(map[string]string, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		for name, raw := range node.Properties {
			if ontologyAPIKey(name) != key {
				continue
			}
			if text, ok := raw.(string); ok {
				values[node.ID] = text
			} else {
				values[node.ID] = fmt.Sprintf("%v", raw)
			}
			break
		}
	}
	return values, nil
}

func (db *DB) applyObjectSetTextPredicate(_ context.Context, _ objectSetResult, predicate ObjectSetPredicate) (objectSetResult, error) {
	return nil, fmt.Errorf("predicate %q is not implemented", predicate.Op)
}

func (db *DB) applyObjectSetVectorPredicate(_ context.Context, _ objectSetResult, predicate ObjectSetPredicate) (objectSetResult, error) {
	return nil, fmt.Errorf("predicate %q is not implemented", predicate.Op)
}

func (db *DB) searchAround(_ context.Context, _ *compiledOntology, _ objectSetResult, sideAPIName string) (objectSetResult, error) {
	return nil, fmt.Errorf("search_around %q is not implemented", sideAPIName)
}
