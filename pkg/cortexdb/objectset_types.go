package cortexdb

import "fmt"

// maxSearchAroundDepth mirrors Foundry's documented runtime limit of three
// chained search-arounds per query. Beyond that the traversal fan-out stops
// being something a single request should carry.
const maxSearchAroundDepth = 3

// maxNearestNeighborsK mirrors Foundry's cap on a nearest-neighbours
// predicate. It bounds the vector index scan, not the final result.
const maxNearestNeighborsK = 100

// ObjectSetKind is the discriminator of the ObjectSet union type.
type ObjectSetKind string

const (
	ObjectSetBase          ObjectSetKind = "base"
	ObjectSetInterfaceBase ObjectSetKind = "interface_base"
	ObjectSetStatic        ObjectSetKind = "static"
	ObjectSetFilter        ObjectSetKind = "filter"
	ObjectSetUnion         ObjectSetKind = "union"
	ObjectSetIntersect     ObjectSetKind = "intersect"
	ObjectSetSubtract      ObjectSetKind = "subtract"
	ObjectSetSearchAround  ObjectSetKind = "search_around"
	ObjectSetReference     ObjectSetKind = "reference"
)

// PredicateOp is the discriminator of a filter predicate.
type PredicateOp string

const (
	PredicateEq               PredicateOp = "eq"
	PredicateLt               PredicateOp = "lt"
	PredicateLte              PredicateOp = "lte"
	PredicateGt               PredicateOp = "gt"
	PredicateGte              PredicateOp = "gte"
	PredicateIsNull           PredicateOp = "is_null"
	PredicateIn               PredicateOp = "in"
	PredicateContains         PredicateOp = "contains"
	PredicateStartsWith       PredicateOp = "starts_with"
	PredicateContainsAllTerms PredicateOp = "contains_all_terms"
	PredicateContainsAnyTerm  PredicateOp = "contains_any_term"
	PredicateNearestNeighbors PredicateOp = "nearest_neighbors"
	PredicateAnd              PredicateOp = "and"
	PredicateOr               PredicateOp = "or"
	PredicateNot              PredicateOp = "not"
)

// ObjectSetPredicate is the filter expression tree.
type ObjectSetPredicate struct {
	Op       PredicateOp          `json:"op"`
	Property string               `json:"property,omitempty"`
	Value    string               `json:"value,omitempty"`
	Values   []string             `json:"values,omitempty"`
	Operands []ObjectSetPredicate `json:"operands,omitempty"`
	// K bounds a nearest_neighbors predicate. Foundry caps this at 100.
	K int `json:"k,omitempty"`
	// Vector is the query vector for nearest_neighbors. When empty the
	// resolver embeds Value instead, which is what agents will normally send.
	Vector []float32 `json:"vector,omitempty"`
}

// ObjectSet is a composable description of a set of objects. It is the one
// place where vector search, full-text search and graph traversal are peers
// rather than three separate APIs.
//
// The type is recursive through Source and Operands. encoding/json handles
// that unaided because the self-reference goes through a pointer and a slice,
// so no custom marshaller is needed. What does not handle it is the MCP SDK's
// schema inference, so any tool carrying an ObjectSet has to declare its input
// schema rather than let the SDK reflect one.
type ObjectSet struct {
	Kind ObjectSetKind `json:"kind"`

	// base
	ObjectType string `json:"object_type,omitempty"`
	// interface_base
	InterfaceType string `json:"interface_type,omitempty"`
	// static
	ObjectIDs []string `json:"object_ids,omitempty"`
	// reference — a saved object set on the active schema
	Reference string `json:"reference,omitempty"`
	// filter / search_around
	Source *ObjectSet          `json:"source,omitempty"`
	Where  *ObjectSetPredicate `json:"where,omitempty"`
	// search_around: the link *side* api name to traverse
	Link string `json:"link,omitempty"`
	// union / intersect / subtract
	Operands []ObjectSet `json:"operands,omitempty"`
}

// OntologyNamedObjectSet is a saved, reusable object set definition.
type OntologyNamedObjectSet struct {
	APIName     string    `json:"api_name"`
	Description string    `json:"description,omitempty"`
	Definition  ObjectSet `json:"definition"`
}

// validateObjectSet rejects a definition before any of it is evaluated, so a
// malformed set costs no queries. searchAroundDepth counts hops along the
// branch being walked, not across the whole tree: the limit exists to bound
// fan-out per path, which is what Foundry bounds.
func validateObjectSet(set ObjectSet, searchAroundDepth int) error {
	if searchAroundDepth > maxSearchAroundDepth {
		return fmt.Errorf("object set exceeds the %d search-around limit", maxSearchAroundDepth)
	}

	switch set.Kind {
	case ObjectSetBase:
		if set.ObjectType == "" {
			return fmt.Errorf("object set kind %q requires object_type", set.Kind)
		}
		return nil
	case ObjectSetInterfaceBase:
		if set.InterfaceType == "" {
			return fmt.Errorf("object set kind %q requires interface_type", set.Kind)
		}
		return nil
	case ObjectSetStatic:
		if len(set.ObjectIDs) == 0 {
			return fmt.Errorf("object set kind %q requires object_ids", set.Kind)
		}
		return nil
	case ObjectSetReference:
		if set.Reference == "" {
			return fmt.Errorf("object set kind %q requires reference", set.Kind)
		}
		return nil
	case ObjectSetFilter:
		if set.Source == nil {
			return fmt.Errorf("object set kind %q requires source", set.Kind)
		}
		if set.Where == nil {
			return fmt.Errorf("object set kind %q requires where", set.Kind)
		}
		if err := validateObjectSetPredicate(*set.Where); err != nil {
			return err
		}
		return validateObjectSet(*set.Source, searchAroundDepth)
	case ObjectSetSearchAround:
		if set.Source == nil {
			return fmt.Errorf("object set kind %q requires source", set.Kind)
		}
		if set.Link == "" {
			return fmt.Errorf("object set kind %q requires link", set.Kind)
		}
		return validateObjectSet(*set.Source, searchAroundDepth+1)
	case ObjectSetUnion, ObjectSetIntersect, ObjectSetSubtract:
		if len(set.Operands) < 2 {
			return fmt.Errorf("object set kind %q requires at least 2 operands", set.Kind)
		}
		for _, operand := range set.Operands {
			if err := validateObjectSet(operand, searchAroundDepth); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown object set kind %q", set.Kind)
	}
}

func validateObjectSetPredicate(predicate ObjectSetPredicate) error {
	switch predicate.Op {
	case PredicateAnd, PredicateOr:
		if len(predicate.Operands) < 2 {
			return fmt.Errorf("predicate %q requires at least 2 operands", predicate.Op)
		}
		return validateObjectSetPredicateOperands(predicate.Operands)
	case PredicateNot:
		if len(predicate.Operands) != 1 {
			return fmt.Errorf("predicate %q requires exactly 1 operand", predicate.Op)
		}
		return validateObjectSetPredicateOperands(predicate.Operands)
	case PredicateIsNull:
		if predicate.Property == "" {
			return fmt.Errorf("predicate %q requires property", predicate.Op)
		}
		return nil
	case PredicateIn:
		if predicate.Property == "" || len(predicate.Values) == 0 {
			return fmt.Errorf("predicate %q requires property and values", predicate.Op)
		}
		return nil
	case PredicateNearestNeighbors:
		if predicate.Property == "" {
			return fmt.Errorf("predicate %q requires property", predicate.Op)
		}
		if predicate.Value == "" && len(predicate.Vector) == 0 {
			return fmt.Errorf("predicate %q requires value or vector", predicate.Op)
		}
		// Zero means "use the default", so only a negative k or one past the
		// cap is a mistake.
		if predicate.K < 0 || predicate.K > maxNearestNeighborsK {
			return fmt.Errorf("predicate %q requires k between 0 and %d, got %d",
				predicate.Op, maxNearestNeighborsK, predicate.K)
		}
		return nil
	case PredicateEq, PredicateLt, PredicateLte, PredicateGt, PredicateGte,
		PredicateContains, PredicateStartsWith, PredicateContainsAllTerms, PredicateContainsAnyTerm:
		if predicate.Property == "" || predicate.Value == "" {
			return fmt.Errorf("predicate %q requires property and value", predicate.Op)
		}
		return nil
	default:
		return fmt.Errorf("unknown predicate op %q", predicate.Op)
	}
}

func validateObjectSetPredicateOperands(operands []ObjectSetPredicate) error {
	for _, operand := range operands {
		if err := validateObjectSetPredicate(operand); err != nil {
			return err
		}
	}
	return nil
}
