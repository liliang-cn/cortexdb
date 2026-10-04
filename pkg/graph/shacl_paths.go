package graph

// SHACL property paths (§2.3.1) and their evaluation.

import (
	"context"
	"fmt"
)

type shaclPathOp int

const (
	shaclPathPredicate shaclPathOp = iota
	shaclPathInverse
	shaclPathSequence
	shaclPathAlternative
	shaclPathZeroOrMore
	shaclPathOneOrMore
	shaclPathZeroOrOne
)

// simple reports whether the path is a predicate or the inverse of one.
func (p *shaclPath) simple() bool {
	return p.op == shaclPathPredicate || (p.op == shaclPathInverse && p.kids[0].op == shaclPathPredicate)
}

func (sg *shaclShapesGraph) parsePath(obj RDFTerm) (*shaclPath, error) {
	return sg.parsePathDepth(obj, 0)
}

func (sg *shaclShapesGraph) parsePathDepth(obj RDFTerm, depth int) (*shaclPath, error) {
	if depth > 64 {
		return nil, fmt.Errorf("sh:path nests too deeply (a cycle?) at %s", obj)
	}
	switch obj.Kind {
	case RDFTermIRI:
		return &shaclPath{Term: obj, Predicate: obj, op: shaclPathPredicate}, nil
	case RDFTermBlankNode:
	default:
		return nil, fmt.Errorf("sh:path must be an IRI or blank node, got %s", obj)
	}
	if sg.isListNode(obj) {
		members, err := sg.parseList(obj, "sh:path sequence")
		if err != nil {
			return nil, err
		}
		if len(members) < 2 {
			return nil, fmt.Errorf("a sequence path needs at least two members, %s has %d", obj, len(members))
		}
		path := &shaclPath{Term: obj, op: shaclPathSequence}
		for _, m := range members {
			kid, err := sg.parsePathDepth(m, depth+1)
			if err != nil {
				return nil, err
			}
			path.kids = append(path.kids, kid)
		}
		return path, nil
	}
	triples := sg.bySubject[obj.String()]
	if len(triples) != 1 {
		return nil, fmt.Errorf("sh:path %s must be a list or have exactly one of sh:inversePath, sh:alternativePath, sh:zeroOrMorePath, sh:oneOrMorePath, sh:zeroOrOnePath", obj)
	}
	tr := triples[0]
	ops := map[string]shaclPathOp{
		SHACLInversePath:                   shaclPathInverse,
		SHACLNamespace + "zeroOrMorePath":  shaclPathZeroOrMore,
		SHACLNamespace + "oneOrMorePath":   shaclPathOneOrMore,
		SHACLNamespace + "zeroOrOnePath":   shaclPathZeroOrOne,
		SHACLNamespace + "alternativePath": shaclPathAlternative,
	}
	op, ok := ops[tr.Predicate.Value]
	if !ok {
		return nil, fmt.Errorf("sh:path %s: %s is not a path operator", obj, tr.Predicate)
	}
	path := &shaclPath{Term: obj, op: op}
	if op == shaclPathAlternative {
		members, err := sg.parseList(tr.Object, "sh:alternativePath")
		if err != nil {
			return nil, err
		}
		if len(members) < 2 {
			return nil, fmt.Errorf("sh:alternativePath needs at least two members, %s has %d", obj, len(members))
		}
		for _, m := range members {
			kid, err := sg.parsePathDepth(m, depth+1)
			if err != nil {
				return nil, err
			}
			path.kids = append(path.kids, kid)
		}
		return path, nil
	}
	kid, err := sg.parsePathDepth(tr.Object, depth+1)
	if err != nil {
		return nil, err
	}
	path.kids = []*shaclPath{kid}
	if op == shaclPathInverse && kid.op == shaclPathPredicate {
		path.Predicate, path.Inverse = kid.Predicate, true
	}
	return path, nil
}

// evalPath returns the nodes the path reaches from node, as a set in
// first-reached order; inverse walks it backwards.
func (v *shaclValidator) evalPath(ctx context.Context, path *shaclPath, node RDFTerm, inverse bool) ([]RDFTerm, error) {
	from := []RDFTerm{node}
	return v.evalPathFrom(ctx, path, from, inverse)
}

func (v *shaclValidator) evalPathFrom(ctx context.Context, path *shaclPath, from []RDFTerm, inverse bool) ([]RDFTerm, error) {
	switch path.op {
	case shaclPathPredicate:
		var out []RDFTerm
		for _, n := range from {
			step, err := v.step(ctx, path.Predicate, n, inverse)
			if err != nil {
				return nil, err
			}
			out = append(out, step...)
		}
		return uniqueSHACLTargets(out), nil
	case shaclPathInverse:
		return v.evalPathFrom(ctx, path.kids[0], from, !inverse)
	case shaclPathSequence:
		kids := path.kids
		cur := from
		for i := range kids {
			k := kids[i]
			if inverse {
				k = kids[len(kids)-1-i]
			}
			next, err := v.evalPathFrom(ctx, k, cur, inverse)
			if err != nil {
				return nil, err
			}
			cur = next
		}
		return cur, nil
	case shaclPathAlternative:
		var out []RDFTerm
		for _, k := range path.kids {
			next, err := v.evalPathFrom(ctx, k, from, inverse)
			if err != nil {
				return nil, err
			}
			out = append(out, next...)
		}
		return uniqueSHACLTargets(out), nil
	case shaclPathZeroOrOne:
		next, err := v.evalPathFrom(ctx, path.kids[0], from, inverse)
		if err != nil {
			return nil, err
		}
		return uniqueSHACLTargets(append(append([]RDFTerm(nil), from...), next...)), nil
	}
	// zero-or-more and one-or-more: the closure, breadth first. One-or-more
	// reaches a start node only through a cycle.
	var out []RDFTerm
	seen := map[string]bool{}
	if path.op == shaclPathZeroOrMore {
		for _, n := range from {
			if !seen[n.String()] {
				seen[n.String()] = true
				out = append(out, n)
			}
		}
	}
	frontier := from
	for len(frontier) > 0 {
		next, err := v.evalPathFrom(ctx, path.kids[0], frontier, inverse)
		if err != nil {
			return nil, err
		}
		frontier = frontier[:0:0]
		for _, n := range next {
			if !seen[n.String()] {
				seen[n.String()] = true
				out = append(out, n)
				frontier = append(frontier, n)
			}
		}
	}
	return out, nil
}

// step follows one predicate from node, forwards or backwards.
func (v *shaclValidator) step(ctx context.Context, predicate, node RDFTerm, inverse bool) ([]RDFTerm, error) {
	var pattern TriplePattern
	if inverse {
		pattern = TriplePattern{Predicate: &predicate, Object: &node}
	} else {
		if node.Kind == RDFTermLiteral {
			return nil, nil // a literal has no outgoing edges
		}
		pattern = TriplePattern{Subject: &node, Predicate: &predicate}
	}
	triples, err := v.findTriples(ctx, pattern)
	if err != nil {
		return nil, err
	}
	out := make([]RDFTerm, 0, len(triples))
	for _, tr := range triples {
		if inverse {
			// FindTriples matches a plain literal object against any
			// datatype; the value node must be exactly the focus term.
			if termsEqual(tr.Object, node) {
				out = append(out, tr.Subject)
			}
			continue
		}
		out = append(out, tr.Object)
	}
	return out, nil
}
