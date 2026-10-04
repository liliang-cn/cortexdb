package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// SHACL rules (SHACL Advanced Features §8, sh:TripleRule).
//
// SHACL 1.2 Rules is still a Working Draft; the triple rule of the SHACL-AF
// note is the part of it that every draft has kept, so that is what this file
// implements, and nothing it accepts should mean something different once the
// 1.2 vocabulary is final.
//
// Supported, exactly:
//
//   - sh:rule on any shape, with rules of type sh:TripleRule. A rule of any
//     other type (sh:SPARQLRule, sh:JSRule, untyped) is an error, as §8.4
//     requires of an engine that cannot run it.
//   - Focus nodes from the shape's targets: sh:targetClass (with subclasses),
//     sh:targetNode, sh:targetSubjectsOf, sh:targetObjectsOf, and the implicit
//     class target of a shape that is also an rdfs:Class or owl:Class.
//   - sh:condition (the focus node must conform to every condition shape,
//     using the SHACL Core subset ValidateSHACL supports), sh:order on rules
//     and on shapes, sh:deactivated on rules and on shapes.
//   - Node expressions for sh:subject / sh:predicate / sh:object: sh:this,
//     constant IRIs and literals, path expressions (sh:path with a predicate
//     or sh:inversePath, optional sh:nodes), filter shape expressions
//     (sh:filterShape + sh:nodes), sh:intersection, sh:union, and function
//     expressions whose function the caller registers in
//     SHACLRuleOptions.Functions. SHACL-SPARQL functions are not evaluated.
//
// Execution follows the single pass of §8.4 — shapes by sh:order, then rules
// by sh:order, with triples inferred by equal-order shapes or rules invisible
// to each other — and repeats that pass until it infers nothing new. The note
// leaves iteration "to future work"; a fixpoint is the only answer that does
// not depend on how a rule set happened to be ordered, as long as conditions
// are monotone. With non-monotone conditions (sh:not, sh:maxCount, sh:closed,
// …) a triple inferred later can no longer retract one inferred earlier,
// so the result is the inflationary one and order does matter there.

// SHACL rule vocabulary.
const (
	SHACLRule         = SHACLNamespace + "rule"
	SHACLTripleRule   = SHACLNamespace + "TripleRule"
	SHACLSPARQLRule   = SHACLNamespace + "SPARQLRule"
	SHACLSubject      = SHACLNamespace + "subject"
	SHACLPredicate    = SHACLNamespace + "predicate"
	SHACLObject       = SHACLNamespace + "object"
	SHACLCondition    = SHACLNamespace + "condition"
	SHACLOrder        = SHACLNamespace + "order"
	SHACLDeactivated  = SHACLNamespace + "deactivated"
	SHACLThis         = SHACLNamespace + "this"
	SHACLNodes        = SHACLNamespace + "nodes"
	SHACLFilterShape  = SHACLNamespace + "filterShape"
	SHACLIntersection = SHACLNamespace + "intersection"
	SHACLUnion        = SHACLNamespace + "union"

	rdfsClassForSHACL = "http://www.w3.org/2000/01/rdf-schema#Class"
	owlClassForSHACL  = "http://www.w3.org/2002/07/owl#Class"

	// SHACLTripleRuleNamePrefix starts the inference rule name of every
	// triple a SHACL rule inferred. The rest is the rule's IRI, or for a
	// blank-node rule the shape that holds it. The prefix is also what
	// ApplySHACLRules owns: every stored triple whose rule starts with it is
	// the output of the previous run, and is retracted if this run does not
	// infer it again.
	SHACLTripleRuleNamePrefix = "shacl_triple_rule:"
)

// SHACLFunction evaluates a function expression for one combination of
// argument values. SHACL-AF defines functions in SPARQL; this engine has no
// SPARQL function runtime, so a caller that needs one (the note's ex:multiply,
// a string concatenation) supplies it in Go. Returning no terms yields no
// triple for that combination.
type SHACLFunction func(args []RDFTerm) ([]RDFTerm, error)

// SHACLRuleOptions tunes ApplySHACLRules.
type SHACLRuleOptions struct {
	// DryRun computes the inferred triples without writing or retracting
	// anything, so a rule set can be checked against real data first.
	DryRun bool `json:"dry_run,omitempty"`
	// MaxIterations bounds the passes over all rules. A rule set that is
	// still inferring after this many passes is an error rather than a
	// partial result. Zero means 64.
	MaxIterations int `json:"max_iterations,omitempty"`
	// MaxDerived bounds the number of inferred triples. Zero means 500000.
	MaxDerived int `json:"max_derived,omitempty"`
	// Functions maps a function IRI to its implementation for function
	// expressions. A function expression naming anything else is refused.
	Functions map[string]SHACLFunction `json:"-"`
}

func (o SHACLRuleOptions) maxIterations() int {
	if o.MaxIterations > 0 {
		return o.MaxIterations
	}
	return 64
}

func (o SHACLRuleOptions) maxDerived() int {
	if o.MaxDerived > 0 {
		return o.MaxDerived
	}
	return 500000
}

// SHACLRuleResult reports one run of ApplySHACLRules.
type SHACLRuleResult struct {
	DryRun bool `json:"dry_run,omitempty"`
	// Rules is the number of active (not deactivated) triple rules.
	Rules int `json:"rules"`
	// Iterations counts passes over all rules, including the last one,
	// which inferred nothing new.
	Iterations int `json:"iterations"`
	// Derived lists every inferred triple with its ID, rule name and
	// support IDs, in the order it was inferred.
	Derived      []RDFTriple    `json:"derived,omitempty"`
	DerivedCount int            `json:"derived_count"`
	PerRule      map[string]int `json:"per_rule,omitempty"`
	// Added counts inferred triples that were not stored before this run,
	// Updated those stored before under another rule name or support, and
	// Retracted the stored SHACL-rule triples this run no longer infers.
	// All three are zero in a dry run.
	Added     int `json:"added"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Retracted int `json:"retracted"`
	// SkippedIllFormed counts combinations that would make a triple RDF does
	// not allow (a literal subject, a non-IRI predicate); the note says to
	// infer every combination, and these are the ones that cannot be stored.
	SkippedIllFormed int `json:"skipped_ill_formed,omitempty"`
}

// ApplySHACLRules runs the sh:TripleRule rules found in shapeTriples against
// the data graph and persists what they infer as inferred triples.
//
// The shapes passed are taken as the complete SHACL rule set: a run first
// computes, from the data with every earlier SHACL-rule triple hidden, what
// the rules infer now, then retracts the stored SHACL-rule triples that are
// no longer inferred and writes the rest. That makes a re-run idempotent and
// makes removing a rule and re-running retract exactly what only that rule
// inferred, without letting a removed rule's output keep justifying itself
// through the rules that remain. A caller with two independent rule sets
// passes their union.
//
// Every inferred triple carries rule name SHACLTripleRuleNamePrefix+<rule>
// and, as support, the triples that made the focus node a target, the
// triples the sh:condition checks read, and the triples that produced the
// subject, predicate and object values, so ExplainTriple and
// ExplainTripleTrace can walk it back to asserted data.
//
// A full RefreshRDFSInferences clears every inferred triple, these included,
// and RDFS reads only asserted triples; run the rules again after a refresh.
func (g *GraphStore) ApplySHACLRules(ctx context.Context, shapeTriples []RDFTriple, opts SHACLRuleOptions) (*SHACLRuleResult, error) {
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}
	namespaces, err := g.ListNamespaces(ctx)
	if err != nil {
		return nil, err
	}
	expanded := make([]RDFTriple, len(shapeTriples))
	for i, tr := range shapeTriples {
		tr.Subject = expandSHACLTerm(tr.Subject, namespaces)
		tr.Predicate = expandSHACLTerm(tr.Predicate, namespaces)
		tr.Object = expandSHACLTerm(tr.Object, namespaces)
		expanded[i] = tr
	}
	program, err := parseSHACLRules(expanded, opts)
	if err != nil {
		return nil, fmt.Errorf("parse shacl rules: %v", err)
	}

	engine := newSHACLRuleEngine(g, program, namespaces, opts)
	if err := engine.run(ctx); err != nil {
		return nil, err
	}

	result := &SHACLRuleResult{
		DryRun:           opts.DryRun,
		Rules:            len(program.rules),
		Iterations:       engine.iterations,
		DerivedCount:     len(engine.derived),
		PerRule:          map[string]int{},
		SkippedIllFormed: engine.skipped,
	}
	result.Derived = make([]RDFTriple, 0, len(engine.derived))
	for _, d := range engine.derived {
		result.Derived = append(result.Derived, d.triple)
		result.PerRule[d.triple.Rule]++
	}
	if opts.DryRun {
		return result, nil
	}
	if err := g.persistSHACLRuleTriples(ctx, engine.derived, result); err != nil {
		return nil, err
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Parsing

type shaclRuleProgram struct {
	shapes *shaclShapesGraph
	rules  []*shaclTripleRule
}

type shaclTripleRule struct {
	Name       string
	Node       RDFTerm
	Shape      *shaclShape
	ShapeOrder float64
	Order      float64
	// shapeSeq and seq keep equal orders in the order the shapes graph
	// wrote them, so a run is reproducible.
	shapeSeq    int
	seq         int
	implicitTgt bool
	Subject     *shaclNodeExpr
	Predicate   *shaclNodeExpr
	Object      *shaclNodeExpr
	Conditions  []RDFTerm
}

type shaclNodeExprKind int

const (
	shaclExprThis shaclNodeExprKind = iota
	shaclExprConstant
	shaclExprPath
	shaclExprFilter
	shaclExprIntersection
	shaclExprUnion
	shaclExprFunction
)

type shaclNodeExpr struct {
	kind    shaclNodeExprKind
	term    RDFTerm // constant, or the function IRI
	path    *shaclPath
	nodes   *shaclNodeExpr // input of a path or filter expression; nil = sh:this
	shape   RDFTerm        // sh:filterShape
	members []*shaclNodeExpr
	fn      SHACLFunction
}

func parseSHACLRules(triples []RDFTriple, opts SHACLRuleOptions) (*shaclRuleProgram, error) {
	sg, err := parseSHACLShapes(triples)
	if err != nil {
		return nil, err
	}
	program := &shaclRuleProgram{shapes: sg}

	var shapeOrder []string
	ruleRefs := map[string][]RDFTerm{}
	for _, tr := range triples {
		if tr.Predicate.Value != SHACLRule {
			continue
		}
		key := tr.Subject.String()
		if _, ok := ruleRefs[key]; !ok {
			shapeOrder = append(shapeOrder, key)
		}
		ruleRefs[key] = append(ruleRefs[key], tr.Object)
	}

	for shapeSeq, key := range shapeOrder {
		subjectTriples := sg.bySubject[key]
		shapeID := subjectTriples[0].Subject
		shape, err := sg.shape(shapeID)
		if err != nil {
			return nil, err
		}
		shapeOrderValue, deactivated, err := sg.orderAndDeactivated(shapeID)
		if err != nil {
			return nil, fmt.Errorf("shape %s: %w", shapeID, err)
		}
		if deactivated {
			continue
		}
		implicit := sg.isImplicitClassTarget(shapeID)

		refs := uniqueSHACLTargets(ruleRefs[key])
		blankRules := 0
		for _, ref := range refs {
			if ref.Kind == RDFTermBlankNode {
				blankRules++
			}
		}
		blankSeq := 0
		for seq, ref := range refs {
			if ref.Kind == RDFTermLiteral {
				return nil, fmt.Errorf("shape %s: sh:rule must be an IRI or blank node, got %s", shapeID, ref)
			}
			name := SHACLTripleRuleNamePrefix + shaclRuleTermName(ref)
			if ref.Kind == RDFTermBlankNode {
				blankSeq++
				name = SHACLTripleRuleNamePrefix + shaclRuleTermName(shapeID)
				if blankRules > 1 {
					name += "#" + strconv.Itoa(blankSeq)
				}
			}
			rule, err := sg.parseTripleRule(ref, opts)
			if err != nil {
				return nil, fmt.Errorf("rule %s of shape %s: %w", ref, shapeID, err)
			}
			if rule == nil {
				continue // deactivated
			}
			rule.Name = name
			rule.Shape = shape
			rule.ShapeOrder = shapeOrderValue
			rule.shapeSeq = shapeSeq
			rule.seq = seq
			rule.implicitTgt = implicit
			program.rules = append(program.rules, rule)
		}
	}

	// Condition and filter shapes are parsed on demand, after the checks
	// parseSHACLShapes ran over the shapes it reached from roots; run them
	// again so a recursive condition is refused instead of looping.
	for _, shape := range sg.shapes {
		for _, ref := range shape.Properties {
			if sg.shapes[ref.String()].Path == nil {
				return nil, fmt.Errorf("property shape %s (sh:property of %s) has no sh:path", ref, shape.ID)
			}
		}
	}
	if err := sg.refuseRecursion(); err != nil {
		return nil, err
	}
	return program, nil
}

func shaclRuleTermName(term RDFTerm) string {
	if term.Kind == RDFTermBlankNode {
		return "_:" + term.Value
	}
	return term.Value
}

// orderAndDeactivated reads sh:order (default 0) and sh:deactivated of a rule
// or shape. Both are single-valued in the note; a second value is an error
// because picking one would make the run depend on triple order.
func (sg *shaclShapesGraph) orderAndDeactivated(node RDFTerm) (float64, bool, error) {
	order, deactivated := 0.0, false
	seenOrder, seenDeactivated := false, false
	for _, tr := range sg.bySubject[node.String()] {
		switch tr.Predicate.Value {
		case SHACLOrder:
			if seenOrder {
				return 0, false, fmt.Errorf("more than one sh:order")
			}
			seenOrder = true
			n, ok := shaclNumber(tr.Object)
			if !ok || tr.Object.Kind != RDFTermLiteral {
				return 0, false, fmt.Errorf("sh:order must be a numeric literal, got %s", tr.Object)
			}
			order = n
		case SHACLDeactivated:
			if seenDeactivated {
				return 0, false, fmt.Errorf("more than one sh:deactivated")
			}
			seenDeactivated = true
			b, err := parseSHACLBool(tr.Object, "sh:deactivated")
			if err != nil {
				return 0, false, err
			}
			deactivated = b
		}
	}
	return order, deactivated, nil
}

// isImplicitClassTarget is SHACL Core §2.1.3.3: a shape that is also a class
// targets that class's instances. The note's first example relies on it —
// ex:Rectangle is both, and has no sh:targetClass.
func (sg *shaclShapesGraph) isImplicitClassTarget(node RDFTerm) bool {
	if node.Kind != RDFTermIRI {
		return false
	}
	isShape, isClass := false, false
	for _, tr := range sg.bySubject[node.String()] {
		if tr.Predicate.Value != RDFType {
			continue
		}
		switch tr.Object.Value {
		case SHACLNodeShape, SHACLPropertyShape:
			isShape = true
		case rdfsClassForSHACL, owlClassForSHACL:
			isClass = true
		}
	}
	return isShape && isClass
}

func (sg *shaclShapesGraph) parseTripleRule(node RDFTerm, opts SHACLRuleOptions) (*shaclTripleRule, error) {
	order, deactivated, err := sg.orderAndDeactivated(node)
	if err != nil {
		return nil, err
	}
	var types []string
	var subj, pred, obj []RDFTerm
	var conditions []RDFTerm
	for _, tr := range sg.bySubject[node.String()] {
		switch tr.Predicate.Value {
		case RDFType:
			types = append(types, tr.Object.Value)
		case SHACLSubject:
			subj = append(subj, tr.Object)
		case SHACLPredicate:
			pred = append(pred, tr.Object)
		case SHACLObject:
			obj = append(obj, tr.Object)
		case SHACLCondition:
			if tr.Object.Kind == RDFTermLiteral {
				return nil, fmt.Errorf("sh:condition must name a shape, got %s", tr.Object)
			}
			if _, err := sg.shape(tr.Object); err != nil {
				return nil, err
			}
			conditions = append(conditions, tr.Object)
		}
	}
	isTriple := false
	for _, t := range types {
		if t == SHACLTripleRule {
			isTriple = true
		}
	}
	if !isTriple {
		if len(types) == 0 {
			return nil, fmt.Errorf("rule has no rdf:type; only sh:TripleRule is supported")
		}
		return nil, fmt.Errorf("unsupported rule type %s; only sh:TripleRule is supported", strings.Join(types, ", "))
	}
	if deactivated {
		return nil, nil
	}
	rule := &shaclTripleRule{Node: node, Order: order, Conditions: conditions}
	for _, part := range []struct {
		name   string
		values []RDFTerm
		dst    **shaclNodeExpr
	}{
		{"sh:subject", subj, &rule.Subject},
		{"sh:predicate", pred, &rule.Predicate},
		{"sh:object", obj, &rule.Object},
	} {
		if len(part.values) != 1 {
			return nil, fmt.Errorf("a triple rule needs exactly one %s, has %d", part.name, len(part.values))
		}
		expr, err := sg.parseNodeExpr(part.values[0], opts, map[string]bool{})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", part.name, err)
		}
		*part.dst = expr
	}
	return rule, nil
}

// parseNodeExpr parses one node expression (SHACL-AF §6). Anything that is
// not one of the supported forms is an error naming it, because an
// expression silently read as "no nodes" turns a rule into one that never
// fires, and nobody notices a rule that never fires.
func (sg *shaclShapesGraph) parseNodeExpr(node RDFTerm, opts SHACLRuleOptions, visiting map[string]bool) (*shaclNodeExpr, error) {
	switch node.Kind {
	case RDFTermIRI:
		if node.Value == SHACLThis {
			return &shaclNodeExpr{kind: shaclExprThis}, nil
		}
		return &shaclNodeExpr{kind: shaclExprConstant, term: node}, nil
	case RDFTermLiteral:
		return &shaclNodeExpr{kind: shaclExprConstant, term: node}, nil
	case RDFTermBlankNode:
	default:
		return nil, fmt.Errorf("unsupported node expression %s", node)
	}

	key := node.String()
	if visiting[key] {
		return nil, fmt.Errorf("node expression %s contains itself", node)
	}
	visiting[key] = true
	defer delete(visiting, key)

	triples := sg.bySubject[key]
	values := map[string][]RDFTerm{}
	for _, tr := range triples {
		values[tr.Predicate.Value] = append(values[tr.Predicate.Value], tr.Object)
	}
	nested := func(term RDFTerm) (*shaclNodeExpr, error) {
		return sg.parseNodeExpr(term, opts, visiting)
	}
	one := func(pred, name string) (RDFTerm, error) {
		if len(values[pred]) != 1 {
			return RDFTerm{}, fmt.Errorf("node expression %s needs exactly one %s, has %d", node, name, len(values[pred]))
		}
		return values[pred][0], nil
	}
	list := func(pred, name string) ([]*shaclNodeExpr, error) {
		head, err := one(pred, name)
		if err != nil {
			return nil, err
		}
		items, err := sg.parseList(head, name)
		if err != nil {
			return nil, err
		}
		if len(items) < 2 {
			return nil, fmt.Errorf("%s needs at least two members, has %d", name, len(items))
		}
		members := make([]*shaclNodeExpr, 0, len(items))
		for _, item := range items {
			m, err := nested(item)
			if err != nil {
				return nil, err
			}
			members = append(members, m)
		}
		return members, nil
	}

	switch {
	case len(values[SHACLPath]) > 0:
		pathTerm, err := one(SHACLPath, "sh:path")
		if err != nil {
			return nil, err
		}
		path, err := sg.parsePath(pathTerm)
		if err != nil {
			return nil, err
		}
		if !path.simple() {
			return nil, fmt.Errorf("path expression %s: rules support a predicate or [ sh:inversePath <iri> ] only", node)
		}
		expr := &shaclNodeExpr{kind: shaclExprPath, path: path}
		if len(values[SHACLNodes]) > 1 {
			return nil, fmt.Errorf("path expression %s has more than one sh:nodes", node)
		}
		if len(values[SHACLNodes]) == 1 {
			if expr.nodes, err = nested(values[SHACLNodes][0]); err != nil {
				return nil, err
			}
		}
		return expr, nil
	case len(values[SHACLFilterShape]) > 0:
		shapeRef, err := one(SHACLFilterShape, "sh:filterShape")
		if err != nil {
			return nil, err
		}
		if shapeRef.Kind == RDFTermLiteral {
			return nil, fmt.Errorf("sh:filterShape must name a shape, got %s", shapeRef)
		}
		if _, err := sg.shape(shapeRef); err != nil {
			return nil, err
		}
		input, err := one(SHACLNodes, "sh:nodes")
		if err != nil {
			return nil, err
		}
		inner, err := nested(input)
		if err != nil {
			return nil, err
		}
		return &shaclNodeExpr{kind: shaclExprFilter, shape: shapeRef, nodes: inner}, nil
	case len(values[SHACLIntersection]) > 0:
		members, err := list(SHACLIntersection, "sh:intersection")
		if err != nil {
			return nil, err
		}
		return &shaclNodeExpr{kind: shaclExprIntersection, members: members}, nil
	case len(values[SHACLUnion]) > 0:
		members, err := list(SHACLUnion, "sh:union")
		if err != nil {
			return nil, err
		}
		return &shaclNodeExpr{kind: shaclExprUnion, members: members}, nil
	case len(triples) == 1 && (triples[0].Object.Kind == RDFTermBlankNode || triples[0].Object.Value == rdfNilIRI):
		fnIRI := triples[0].Predicate.Value
		items, err := sg.parseList(triples[0].Object, "function arguments")
		if err != nil {
			return nil, err
		}
		fn := opts.Functions[fnIRI]
		if fn == nil {
			return nil, fmt.Errorf("function expression calls <%s>, which is not registered in SHACLRuleOptions.Functions (SHACL-SPARQL functions are not evaluated)", fnIRI)
		}
		expr := &shaclNodeExpr{kind: shaclExprFunction, term: NewIRI(fnIRI), fn: fn}
		for _, item := range items {
			m, err := nested(item)
			if err != nil {
				return nil, err
			}
			expr.members = append(expr.members, m)
		}
		return expr, nil
	}
	return nil, fmt.Errorf("unsupported node expression %s: expected sh:this, a constant, sh:path, sh:filterShape, sh:intersection, sh:union or a function call", node)
}

// ---------------------------------------------------------------------------
// Evaluation

// shaclDerived is one inferred triple with its content key.
type shaclDerived struct {
	key    string
	triple RDFTriple
}

// shaclTripleIndex holds inferred triples in memory, indexed the way the
// validator asks for them, so a later rule (and a condition) sees them
// before anything is written.
type shaclTripleIndex struct {
	byKey       map[string]struct{}
	all         []RDFTriple
	bySubject   map[string][]int
	byPredicate map[string][]int
	byObject    map[string][]int
}

func newSHACLTripleIndex() *shaclTripleIndex {
	return &shaclTripleIndex{
		byKey:       map[string]struct{}{},
		bySubject:   map[string][]int{},
		byPredicate: map[string][]int{},
		byObject:    map[string][]int{},
	}
}

func shaclLooseKey(term RDFTerm) string { return term.Kind + "\x00" + term.Value }

func (idx *shaclTripleIndex) has(key string) bool {
	_, ok := idx.byKey[key]
	return ok
}

func (idx *shaclTripleIndex) add(key string, tr RDFTriple) {
	if idx.has(key) {
		return
	}
	idx.byKey[key] = struct{}{}
	i := len(idx.all)
	idx.all = append(idx.all, tr)
	idx.bySubject[shaclLooseKey(tr.Subject)] = append(idx.bySubject[shaclLooseKey(tr.Subject)], i)
	idx.byPredicate[tr.Predicate.Value] = append(idx.byPredicate[tr.Predicate.Value], i)
	idx.byObject[shaclLooseKey(tr.Object)] = append(idx.byObject[shaclLooseKey(tr.Object)], i)
}

// find matches a pattern the way findStoredTriples does: subject and object
// by kind and value, a literal's datatype and language only when the
// pattern names them.
func (idx *shaclTripleIndex) find(p TriplePattern) []RDFTriple {
	var candidates []int
	switch {
	case p.Subject != nil:
		candidates = idx.bySubject[shaclLooseKey(*p.Subject)]
	case p.Object != nil:
		candidates = idx.byObject[shaclLooseKey(*p.Object)]
	case p.Predicate != nil:
		candidates = idx.byPredicate[p.Predicate.Value]
	default:
		candidates = make([]int, len(idx.all))
		for i := range idx.all {
			candidates[i] = i
		}
	}
	var out []RDFTriple
	for _, i := range candidates {
		tr := idx.all[i]
		if p.Subject != nil && shaclLooseKey(tr.Subject) != shaclLooseKey(*p.Subject) {
			continue
		}
		if p.Predicate != nil && tr.Predicate.Value != p.Predicate.Value {
			continue
		}
		if p.Object != nil {
			o := *p.Object
			if shaclLooseKey(tr.Object) != shaclLooseKey(o) {
				continue
			}
			if o.Kind == RDFTermLiteral && ((o.Datatype != "" && o.Datatype != tr.Object.Datatype) || (o.Language != "" && !strings.EqualFold(o.Language, tr.Object.Language))) {
				continue
			}
		}
		out = append(out, tr)
	}
	return out
}

type shaclRuleEngine struct {
	g          *GraphStore
	program    *shaclRuleProgram
	namespaces []Namespace
	opts       SHACLRuleOptions

	// base caches what the store returns per pattern. The store does not
	// change during a run, and the fixpoint asks the same questions on every
	// pass, so each distinct pattern is read once.
	base map[string][]RDFTriple
	// layers are the inferred triples visible to the rule being run: the
	// ones committed by earlier order groups, then the ones the current
	// shape inferred in earlier rule-order groups.
	layers []*shaclTripleIndex
	// recorders collect the IDs of every triple read while they are open.
	recorders []map[string]struct{}

	committed  *shaclTripleIndex
	derived    []shaclDerived
	iterations int
	skipped    int
}

func newSHACLRuleEngine(g *GraphStore, program *shaclRuleProgram, namespaces []Namespace, opts SHACLRuleOptions) *shaclRuleEngine {
	return &shaclRuleEngine{
		g:          g,
		program:    program,
		namespaces: namespaces,
		opts:       opts,
		base:       map[string][]RDFTriple{},
		committed:  newSHACLTripleIndex(),
	}
}

func shaclPatternKey(p TriplePattern) string {
	part := func(t *RDFTerm) string {
		if t == nil {
			return "*"
		}
		return t.String()
	}
	return part(p.Subject) + " " + part(p.Predicate) + " " + part(p.Object)
}

// find is the data graph the rules see: the store, minus the triples an
// earlier ApplySHACLRules wrote (this run decides afresh whether they hold),
// plus what this run has inferred and made visible so far.
func (e *shaclRuleEngine) find(ctx context.Context, p TriplePattern) ([]RDFTriple, error) {
	key := shaclPatternKey(p)
	base, ok := e.base[key]
	if !ok {
		stored, err := e.g.FindTriples(ctx, TriplePattern{Subject: p.Subject, Predicate: p.Predicate, Object: p.Object})
		if err != nil {
			return nil, err
		}
		base = stored[:0:0]
		for _, tr := range stored {
			if tr.Inferred && strings.HasPrefix(tr.Rule, SHACLTripleRuleNamePrefix) {
				continue
			}
			base = append(base, tr)
		}
		e.base[key] = base
	}
	out := base
	for _, layer := range e.layers {
		if extra := layer.find(p); len(extra) > 0 {
			out = append(append([]RDFTriple(nil), out...), extra...)
		}
	}
	for _, rec := range e.recorders {
		for _, tr := range out {
			rec[tr.ID] = struct{}{}
		}
	}
	return out, nil
}

// record runs fn and returns the IDs of every triple it read.
func (e *shaclRuleEngine) record(fn func() error) ([]string, error) {
	rec := map[string]struct{}{}
	e.recorders = append(e.recorders, rec)
	err := fn()
	e.recorders = e.recorders[:len(e.recorders)-1]
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rec))
	for id := range rec {
		ids = append(ids, id)
	}
	return ids, nil
}

// validator returns a fresh validator over the current view. Fresh, because
// the validator memoises the class hierarchy, and a memo would hide both the
// subclass triples a later rule inferred and the triples a recorded check
// needs to list as support.
func (e *shaclRuleEngine) validator() *shaclValidator {
	v := newSHACLValidator(e.g, e.program.shapes)
	v.source = e.find
	return v
}

// present reports whether the view already holds s p o in any graph. The
// rules infer into the data graph as a whole, so a statement asserted in a
// named graph, or supplied by the projection, is not inferred again.
func (e *shaclRuleEngine) present(ctx context.Context, tr RDFTriple, key string) (bool, error) {
	for _, layer := range e.layers {
		if layer.has(key) {
			return true, nil
		}
	}
	saved := e.recorders
	e.recorders = nil
	triples, err := e.find(ctx, TriplePattern{Subject: &tr.Subject, Predicate: &tr.Predicate, Object: &tr.Object})
	e.recorders = saved
	if err != nil {
		return false, err
	}
	for _, existing := range triples {
		if termsEqual(existing.Subject, tr.Subject) && existing.Predicate.Value == tr.Predicate.Value && termsEqual(existing.Object, tr.Object) {
			return true, nil
		}
	}
	return false, nil
}

func (e *shaclRuleEngine) run(ctx context.Context) error {
	rules := append([]*shaclTripleRule(nil), e.program.rules...)
	sort.SliceStable(rules, func(i, j int) bool {
		a, b := rules[i], rules[j]
		if a.ShapeOrder != b.ShapeOrder {
			return a.ShapeOrder < b.ShapeOrder
		}
		if a.shapeSeq != b.shapeSeq {
			return a.shapeSeq < b.shapeSeq
		}
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.seq < b.seq
	})

	for pass := 1; ; pass++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if pass > e.opts.maxIterations() {
			return fmt.Errorf("shacl rules did not reach a fixpoint within %d passes (%d triples inferred so far)", e.opts.maxIterations(), len(e.derived))
		}
		e.iterations = pass
		before := len(e.derived)

		// §8.4: shapes in execution order; triples from shapes of equal
		// order are not visible to each other, so they share a snapshot.
		for i := 0; i < len(rules); {
			j := i
			for j < len(rules) && rules[j].ShapeOrder == rules[i].ShapeOrder {
				j++
			}
			var groupAdds []shaclDerived
			for k := i; k < j; {
				m := k
				for m < j && rules[m].shapeSeq == rules[k].shapeSeq {
					m++
				}
				adds, err := e.runShape(ctx, rules[k:m])
				if err != nil {
					return err
				}
				groupAdds = append(groupAdds, adds...)
				k = m
			}
			for _, d := range groupAdds {
				if e.committed.has(d.key) {
					continue // inferred by two equal-order shapes; first one wins
				}
				e.committed.add(d.key, d.triple)
				e.derived = append(e.derived, d)
				if len(e.derived) > e.opts.maxDerived() {
					return fmt.Errorf("shacl rules inferred more than %d triples", e.opts.maxDerived())
				}
			}
			i = j
		}
		if len(e.derived) == before {
			return nil
		}
	}
}

// runShape runs one shape's rules in rule order. A rule-order group sees what
// earlier groups of the same shape inferred, never what its own members or
// other shapes of the same order inferred.
func (e *shaclRuleEngine) runShape(ctx context.Context, rules []*shaclTripleRule) ([]shaclDerived, error) {
	local := newSHACLTripleIndex()
	var adds []shaclDerived
	e.layers = []*shaclTripleIndex{e.committed, local}
	defer func() { e.layers = nil }()

	for i := 0; i < len(rules); {
		j := i
		for j < len(rules) && rules[j].Order == rules[i].Order {
			j++
		}
		pending := newSHACLTripleIndex()
		var pendingList []shaclDerived
		for _, rule := range rules[i:j] {
			out, err := e.runRule(ctx, rule, pending)
			if err != nil {
				return nil, err
			}
			pendingList = append(pendingList, out...)
		}
		for _, d := range pendingList {
			local.add(d.key, d.triple)
		}
		adds = append(adds, pendingList...)
		i = j
	}
	return adds, nil
}

type shaclValue struct {
	term    RDFTerm
	support []string
}

func (e *shaclRuleEngine) targets(ctx context.Context, rule *shaclTripleRule) ([]RDFTerm, error) {
	v := e.validator()
	targets, err := v.targets(ctx, rule.Shape)
	if err != nil {
		return nil, err
	}
	if rule.implicitTgt {
		instances, err := v.instancesOf(ctx, rule.Shape.ID)
		if err != nil {
			return nil, err
		}
		targets = uniqueSHACLTargets(append(targets, instances...))
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].String() < targets[j].String() })
	return targets, nil
}

// targetSupport records why focus is a target of the rule's shape: its
// rdf:type (and the subclass chain) for a class target, its triple with the
// predicate for sh:targetSubjectsOf / sh:targetObjectsOf. sh:targetNode needs
// no data and contributes nothing.
func (e *shaclRuleEngine) targetSupport(ctx context.Context, rule *shaclTripleRule, focus RDFTerm) ([]string, error) {
	shape := rule.Shape
	if containsTerm(shape.TargetNode, focus) {
		return nil, nil
	}
	return e.record(func() error {
		v := e.validator()
		classes := append([]RDFTerm(nil), shape.TargetClass...)
		if rule.implicitTgt {
			classes = append(classes, shape.ID)
		}
		for _, cls := range classes {
			ok, err := v.isInstanceOf(ctx, focus, cls)
			if err != nil || ok {
				return err
			}
		}
		for _, p := range shape.TargetSubjectsOf {
			if focus.Kind == RDFTermLiteral {
				break
			}
			predicate, subject := p, focus
			triples, err := e.find(ctx, TriplePattern{Subject: &subject, Predicate: &predicate})
			if err != nil || len(triples) > 0 {
				return err
			}
		}
		for _, p := range shape.TargetObjectsOf {
			predicate, object := p, focus
			triples, err := e.find(ctx, TriplePattern{Predicate: &predicate, Object: &object})
			if err != nil || len(triples) > 0 {
				return err
			}
		}
		return nil
	})
}

func (e *shaclRuleEngine) runRule(ctx context.Context, rule *shaclTripleRule, pending *shaclTripleIndex) ([]shaclDerived, error) {
	focusNodes, err := e.targets(ctx, rule)
	if err != nil {
		return nil, err
	}
	var out []shaclDerived
	for _, focus := range focusNodes {
		support, err := e.targetSupport(ctx, rule, focus)
		if err != nil {
			return nil, err
		}
		conforms := true
		for _, cond := range rule.Conditions {
			var ok bool
			ids, err := e.record(func() error {
				var err error
				ok, err = e.validator().conforms(ctx, cond, focus)
				return err
			})
			if err != nil {
				return nil, err
			}
			if !ok {
				conforms = false
				break
			}
			support = append(support, ids...)
		}
		if !conforms {
			continue
		}

		subjects, err := e.eval(ctx, rule.Subject, focus)
		if err != nil {
			return nil, err
		}
		if len(subjects) == 0 {
			continue
		}
		predicates, err := e.eval(ctx, rule.Predicate, focus)
		if err != nil {
			return nil, err
		}
		if len(predicates) == 0 {
			continue
		}
		objects, err := e.eval(ctx, rule.Object, focus)
		if err != nil {
			return nil, err
		}
		for _, s := range subjects {
			for _, p := range predicates {
				for _, o := range objects {
					d, ok, err := e.makeTriple(rule, s, p, o, support)
					if err != nil {
						return nil, err
					}
					if !ok {
						continue
					}
					if pending.has(d.key) {
						continue
					}
					present, err := e.present(ctx, d.triple, d.key)
					if err != nil {
						return nil, err
					}
					if present {
						continue
					}
					pending.add(d.key, d.triple)
					out = append(out, d)
				}
			}
		}
	}
	return out, nil
}

func (e *shaclRuleEngine) makeTriple(rule *shaclTripleRule, s, p, o shaclValue, support []string) (shaclDerived, bool, error) {
	if (s.term.Kind != RDFTermIRI && s.term.Kind != RDFTermBlankNode) || p.term.Kind != RDFTermIRI {
		e.skipped++
		return shaclDerived{}, false, nil
	}
	normalized, err := e.g.normalizeTripleWithNamespaces(RDFTriple{Subject: s.term, Predicate: p.term, Object: o.term}, e.namespaces)
	if err != nil {
		e.skipped++
		return shaclDerived{}, false, nil
	}
	normalized.ID = tripleDigest(normalized)
	ids := make([]string, 0, len(support)+len(s.support)+len(p.support)+len(o.support))
	ids = append(ids, support...)
	ids = append(ids, s.support...)
	ids = append(ids, p.support...)
	ids = append(ids, o.support...)
	normalized.Inferred = true
	normalized.Rule = rule.Name
	normalized.SupportIDs = uniqueSortedStrings(ids)
	return shaclDerived{key: inferenceContentKey(normalized), triple: normalized}, true, nil
}

// eval is Eval($expr, $this) of SHACL-AF §6, with each value carrying the IDs
// of the triples that produced it.
func (e *shaclRuleEngine) eval(ctx context.Context, expr *shaclNodeExpr, focus RDFTerm) ([]shaclValue, error) {
	switch expr.kind {
	case shaclExprThis:
		return []shaclValue{{term: focus}}, nil
	case shaclExprConstant:
		return []shaclValue{{term: expr.term}}, nil
	case shaclExprPath:
		inputs := []shaclValue{{term: focus}}
		if expr.nodes != nil {
			var err error
			if inputs, err = e.eval(ctx, expr.nodes, focus); err != nil {
				return nil, err
			}
		}
		var out []shaclValue
		for _, in := range inputs {
			predicate, node := expr.path.Predicate, in.term
			var pattern TriplePattern
			if expr.path.Inverse {
				pattern = TriplePattern{Predicate: &predicate, Object: &node}
			} else {
				if node.Kind == RDFTermLiteral {
					continue
				}
				pattern = TriplePattern{Subject: &node, Predicate: &predicate}
			}
			triples, err := e.find(ctx, pattern)
			if err != nil {
				return nil, err
			}
			for _, tr := range triples {
				value := tr.Object
				if expr.path.Inverse {
					if !termsEqual(tr.Object, node) {
						continue
					}
					value = tr.Subject
				}
				out = append(out, shaclValue{term: value, support: append(append([]string(nil), in.support...), tr.ID)})
			}
		}
		return uniqueSHACLValues(out), nil
	case shaclExprFilter:
		inputs, err := e.eval(ctx, expr.nodes, focus)
		if err != nil {
			return nil, err
		}
		var out []shaclValue
		for _, in := range inputs {
			var ok bool
			ids, err := e.record(func() error {
				var err error
				ok, err = e.validator().conforms(ctx, expr.shape, in.term)
				return err
			})
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, shaclValue{term: in.term, support: append(append([]string(nil), in.support...), ids...)})
			}
		}
		return out, nil
	case shaclExprUnion:
		var out []shaclValue
		for _, m := range expr.members {
			values, err := e.eval(ctx, m, focus)
			if err != nil {
				return nil, err
			}
			out = append(out, values...)
		}
		return uniqueSHACLValues(out), nil
	case shaclExprIntersection:
		var acc []shaclValue
		for i, m := range expr.members {
			values, err := e.eval(ctx, m, focus)
			if err != nil {
				return nil, err
			}
			if i == 0 {
				acc = values
				continue
			}
			byKey := map[string]shaclValue{}
			for _, v := range values {
				byKey[v.term.String()] = v
			}
			var next []shaclValue
			for _, a := range acc {
				if b, ok := byKey[a.term.String()]; ok {
					next = append(next, shaclValue{term: a.term, support: append(append([]string(nil), a.support...), b.support...)})
				}
			}
			acc = next
		}
		return acc, nil
	case shaclExprFunction:
		args := make([][]shaclValue, len(expr.members))
		for i, m := range expr.members {
			values, err := e.eval(ctx, m, focus)
			if err != nil {
				return nil, err
			}
			if len(values) == 0 {
				return nil, nil
			}
			args[i] = values
		}
		var out []shaclValue
		combo := make([]shaclValue, len(args))
		var walk func(i int) error
		walk = func(i int) error {
			if i == len(args) {
				terms := make([]RDFTerm, len(combo))
				var support []string
				for k, c := range combo {
					terms[k] = c.term
					support = append(support, c.support...)
				}
				results, err := expr.fn(terms)
				if err != nil {
					return fmt.Errorf("function <%s>: %w", expr.term.Value, err)
				}
				for _, r := range results {
					out = append(out, shaclValue{term: r, support: append([]string(nil), support...)})
				}
				return nil
			}
			for _, v := range args[i] {
				combo[i] = v
				if err := walk(i + 1); err != nil {
					return err
				}
			}
			return nil
		}
		if err := walk(0); err != nil {
			return nil, err
		}
		return uniqueSHACLValues(out), nil
	}
	return nil, fmt.Errorf("unknown node expression kind %d", expr.kind)
}

// uniqueSHACLValues keeps the first occurrence of each term, so a value
// reached two ways is explained by the first way it was reached.
func uniqueSHACLValues(values []shaclValue) []shaclValue {
	seen := map[string]struct{}{}
	out := values[:0:0]
	for _, v := range values {
		key := v.term.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	return out
}

// ---------------------------------------------------------------------------
// Persistence

// persistSHACLRuleTriples makes the stored SHACL-rule triples equal to this
// run's: new ones are written, changed ones rewritten, unchanged ones left
// alone, and the rest retracted. It writes through persistInferredRecords and
// deletes through deleteInferredTriplesByID, the same paths RDFS inference
// uses, so the rows look like any other inferred triple to ExplainTriple,
// InferenceSummary and the incremental RDFS refresh.
func (g *GraphStore) persistSHACLRuleTriples(ctx context.Context, derived []shaclDerived, result *SHACLRuleResult) error {
	existing, err := g.storedSHACLRuleTriples(ctx)
	if err != nil {
		return err
	}
	want := map[string]struct{}{}
	records := map[string]rdfsInferenceRecord{}
	for _, d := range derived {
		tr := d.triple
		want[tr.ID] = struct{}{}
		if old, ok := existing[tr.ID]; ok {
			if old.Rule == tr.Rule && strings.Join(old.SupportIDs, "\x00") == strings.Join(tr.SupportIDs, "\x00") {
				result.Unchanged++
				continue
			}
			result.Updated++
		} else {
			result.Added++
		}
		records[d.key] = rdfsInferenceRecord{Triple: tripleWithoutInference(tr), Rule: tr.Rule, SupportIDs: tr.SupportIDs, key: d.key}
	}
	if _, err := g.persistInferredRecords(ctx, records); err != nil {
		return err
	}
	var stale []string
	for id := range existing {
		if _, ok := want[id]; !ok {
			stale = append(stale, id)
		}
	}
	removed, err := g.deleteInferredTriplesByID(ctx, stale)
	if err != nil {
		return err
	}
	result.Retracted = removed
	return nil
}

func (g *GraphStore) storedSHACLRuleTriples(ctx context.Context) (map[string]RDFTriple, error) {
	// LIKE narrows the scan; HasPrefix decides, because "_" is a LIKE
	// wildcard and the two backends disagree about the default escape.
	rows, err := g.query(ctx, `SELECT id, inference_rule, support_ids FROM kg_triples WHERE inferred = 1 AND inference_rule LIKE ?`, "shacl%")
	if err != nil {
		return nil, fmt.Errorf("list shacl rule triples: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]RDFTriple{}
	for rows.Next() {
		var id string
		var rule, support *string
		if err := rows.Scan(&id, &rule, &support); err != nil {
			return nil, err
		}
		if rule == nil || !strings.HasPrefix(*rule, SHACLTripleRuleNamePrefix) {
			continue
		}
		tr := RDFTriple{ID: id, Rule: *rule}
		if support != nil && *support != "" {
			if err := json.Unmarshal([]byte(*support), &tr.SupportIDs); err != nil {
				return nil, fmt.Errorf("decode support ids of %s: %w", id, err)
			}
		}
		out[id] = tr
	}
	return out, rows.Err()
}
