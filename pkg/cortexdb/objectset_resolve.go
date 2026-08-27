package cortexdb

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// defaultNearestNeighborsK is the neighbourhood a nearest_neighbors predicate
// searches when the caller does not bound it.
const defaultNearestNeighborsK = 10

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
		// Lowered here to match the LOWER(node_type) below; an IN list cannot
		// carry the function on its own side.
		args = append(args, strings.ToLower(objectType))
	}

	// Case-insensitively because node_type is only canonicalised on
	// ontology-validated writes: rows written before a schema was activated
	// keep whatever spelling they arrived with, and they are the same objects.
	//
	// LOWER() on both sides rather than COLLATE NOCASE, which is SQLite's own
	// and which PostgreSQL refuses outright — `collation "nocase" for encoding
	// "UTF8" does not exist`, which took out every object-set resolution.
	rows, err := db.query(ctx,
		`SELECT id FROM graph_nodes WHERE LOWER(node_type) IN (`+placeholders+`)`, args...)
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

// applyObjectSetTextPredicate matches whole terms, which is what separates it
// from `contains`: "and" is inside "Sunderland" but is not a term of it.
//
// The matching is done in memory over the candidate set rather than by handing
// the query to FTS5. The candidates are already narrowed by the enclosing
// object set, and going through the index would tie the result to that index's
// tokenizer — which is trigram or unicode61 depending on the SQLite build.
func (db *DB) applyObjectSetTextPredicate(ctx context.Context, source objectSetResult, predicate ObjectSetPredicate) (objectSetResult, error) {
	values, err := db.loadNodePropertyValues(ctx, source, predicate.Property)
	if err != nil {
		return nil, err
	}
	terms := textPredicateTerms(predicate.Value)
	result := objectSetResult{}
	// A query that tokenizes to nothing selects nothing. Reading "all of no
	// terms" as vacuously true would turn a punctuation-only query into a
	// request for the entire source set.
	if len(terms) == 0 {
		return result, nil
	}

	for id := range source {
		haystack := make(map[string]struct{})
		for _, term := range textPredicateTerms(values[id]) {
			haystack[term] = struct{}{}
		}

		matched := predicate.Op == PredicateContainsAllTerms
		for _, term := range terms {
			_, contains := haystack[term]
			if predicate.Op == PredicateContainsAllTerms && !contains {
				matched = false
				break
			}
			if predicate.Op == PredicateContainsAnyTerm && contains {
				matched = true
				break
			}
		}
		if matched {
			result[id] = struct{}{}
		}
	}
	return result, nil
}

// textPredicateTerms splits on everything that is not a letter or a digit, so
// punctuation in a name or a query never becomes part of a term.
func textPredicateTerms(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// applyObjectSetVectorPredicate intersects a k-nearest-neighbour search with
// the candidate set, which is what makes vector search an operator inside the
// algebra rather than a separate API.
//
// CortexDB keeps one embedding per object, so Property records which
// vectorized property the caller means while the search itself runs over that
// embedding.
func (db *DB) applyObjectSetVectorPredicate(ctx context.Context, source objectSetResult, predicate ObjectSetPredicate) (objectSetResult, error) {
	queryVector := predicate.Vector
	if len(queryVector) == 0 {
		// Said plainly rather than returning an empty set: "no embedder" and
		// "no similar objects" are answers a caller must be able to tell apart.
		if db.embedder == nil {
			return nil, fmt.Errorf("predicate %q needs either an explicit vector or a configured embedder", predicate.Op)
		}
		embedded, err := db.embedder.Embed(ctx, predicate.Value)
		if err != nil {
			return nil, fmt.Errorf("embed nearest_neighbors query: %w", err)
		}
		queryVector = embedded
	}

	k := predicate.K
	if k <= 0 {
		k = defaultNearestNeighborsK
	}
	// HybridSearch with no start node is a pure vector search over graph
	// nodes; it uses the HNSW index when one is enabled and scans otherwise.
	neighbours, err := db.graph.HybridSearch(ctx, &graph.HybridQuery{Vector: queryVector, TopK: k})
	if err != nil {
		return nil, fmt.Errorf("nearest neighbours: %w", err)
	}

	result := objectSetResult{}
	for _, neighbour := range neighbours {
		if neighbour == nil || neighbour.Node == nil {
			continue
		}
		if _, ok := source[neighbour.Node.ID]; ok {
			result[neighbour.Node.ID] = struct{}{}
		}
	}
	return result, nil
}

// searchAround traverses one link side from every object in the source set.
// The side name fixes both which link type to follow and which direction, so
// "departures" and "origin" walk the same edges opposite ways.
func (db *DB) searchAround(ctx context.Context, compiled *compiledOntology, source objectSetResult, sideAPIName string) (objectSetResult, error) {
	if compiled == nil {
		return nil, fmt.Errorf("%w: search_around object sets need an active ontology", ErrInvalidOntology)
	}
	traversals := compiled.linkTraversalsBySide(sideAPIName)
	if len(traversals) == 0 {
		return nil, fmt.Errorf("%w: ontology defines no link side %q", ErrInvalidOntology, sideAPIName)
	}

	result := objectSetResult{}
	if len(source) == 0 {
		return result, nil
	}
	// Sorted so the query arguments — and so the query plan and any logged
	// statement — do not depend on Go's map iteration order.
	nodeIDs := sortedKeysFromSet(source)

	for _, traversal := range traversals {
		reached, err := db.linkedNodeIDs(ctx, traversal.linkType.APIName, nodeIDs)
		if err != nil {
			return nil, err
		}
		// A link type is bidirectional and the edge is stored in whichever
		// direction it was asserted, so the query above reaches both ends.
		// Keeping only objects of the far side's type is what makes the side
		// name — not just the link type — decide the direction.
		if err := db.addNodesOfObjectType(ctx, compiled, result, reached, traversal.far.ObjectTypeAPIName); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// linkedNodeIDs returns every node joined to one of nodeIDs by an edge of the
// link type, from either end.
//
// The edge type is matched case-insensitively for the same reason the
// cardinality check does it: api names resolve that way everywhere else.
func (db *DB) linkedNodeIDs(ctx context.Context, linkTypeAPIName string, nodeIDs []string) ([]string, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(nodeIDs)), ",")
	// One argument list for the two halves of the union, in the order the
	// statement reads them: link type, node ids, link type, node ids.
	args := make([]any, 0, 2*len(nodeIDs)+2)
	args = append(args, linkTypeAPIName)
	for _, id := range nodeIDs {
		args = append(args, id)
	}
	args = append(args, linkTypeAPIName)
	for _, id := range nodeIDs {
		args = append(args, id)
	}

	rows, err := db.query(ctx, `
		SELECT to_node_id AS other FROM graph_edges
		WHERE LOWER(edge_type) = LOWER(?) AND from_node_id IN (`+placeholders+`)
		UNION
		SELECT from_node_id AS other FROM graph_edges
		WHERE LOWER(edge_type) = LOWER(?) AND to_node_id IN (`+placeholders+`)
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("search around: %w", err)
	}
	defer func() { _ = rows.Close() }()

	reached := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan search around row: %w", err)
		}
		reached = append(reached, id)
	}
	return reached, rows.Err()
}

// addNodesOfObjectType adds the nodes a type name stands for to a result set,
// leaving the rest out.
//
// The name may be an interface, in which case it stands for its implementors —
// the same expansion a type filter gets on FindNodes and search. Matching the
// name literally, as this did, made a link side naming an interface keep only
// nodes whose node_type was that interface's name, which nothing is ever stored
// as: every traversal of a polymorphic relation returned the empty set, and an
// empty set is what the graph also says about a subject it knows nothing about.
func (db *DB) addNodesOfObjectType(ctx context.Context, compiled *compiledOntology, result objectSetResult, nodeIDs []string, objectTypeAPIName string) error {
	if len(nodeIDs) == 0 {
		return nil
	}
	nodes, err := db.graph.GetNodesBatch(ctx, nodeIDs)
	if err != nil {
		return fmt.Errorf("filter search around results: %w", err)
	}
	targets := map[string]struct{}{ontologyAPIKey(objectTypeAPIName): {}}
	if compiled != nil {
		targets = compiled.typeClosureKeys(objectTypeAPIName)
	}
	for _, node := range nodes {
		if node == nil {
			continue
		}
		if _, ok := targets[ontologyAPIKey(node.NodeType)]; ok {
			result[node.ID] = struct{}{}
		}
	}
	return nil
}
