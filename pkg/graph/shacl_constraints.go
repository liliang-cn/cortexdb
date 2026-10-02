package graph

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// shaclValidator evaluates a parsed shapes graph against the data graph, which
// it reads only through FindTriples. That keeps SHACL honest about what the
// store exposes: anything FindTriples projects (asserted triples, inferred
// ones, a projection of the property graph) is data to validate, and nothing
// else is.
type shaclValidator struct {
	g      *GraphStore
	shapes *shaclShapesGraph
	// superclasses memoises rdfs:subClassOf* per class, because a quality gate
	// over thousands of extracted nodes asks the same "is a project?" question
	// once per edge.
	superclasses map[string][]RDFTerm
	// source, when set, replaces FindTriples as the data graph. SHACL rules
	// set it so that a condition sees the triples earlier rules inferred
	// before they are persisted (or when they never will be, in a dry run),
	// and so that every triple a check reads can be recorded as support.
	source func(context.Context, TriplePattern) ([]RDFTriple, error)
}

func newSHACLValidator(g *GraphStore, shapes *shaclShapesGraph) *shaclValidator {
	return &shaclValidator{g: g, shapes: shapes, superclasses: make(map[string][]RDFTerm)}
}

func (v *shaclValidator) findTriples(ctx context.Context, pattern TriplePattern) ([]RDFTriple, error) {
	if v.source != nil {
		return v.source(ctx, pattern)
	}
	return v.g.FindTriples(ctx, pattern)
}

// targets computes the focus nodes of a shape. sh:targetClass selects SHACL
// instances — rdf:type of the class or of any rdfs:subClassOf* descendant —
// and a sh:targetNode is a focus node even when the data never mentions it.
func (v *shaclValidator) targets(ctx context.Context, shape *shaclShape) ([]RDFTerm, error) {
	var targets []RDFTerm
	for _, cls := range shape.TargetClass {
		instances, err := v.instancesOf(ctx, cls)
		if err != nil {
			return nil, err
		}
		targets = append(targets, instances...)
	}
	targets = append(targets, shape.TargetNode...)
	for _, p := range shape.TargetSubjectsOf {
		predicate := p
		triples, err := v.findTriples(ctx, TriplePattern{Predicate: &predicate})
		if err != nil {
			return nil, err
		}
		for _, tr := range triples {
			targets = append(targets, tr.Subject)
		}
	}
	for _, p := range shape.TargetObjectsOf {
		predicate := p
		triples, err := v.findTriples(ctx, TriplePattern{Predicate: &predicate})
		if err != nil {
			return nil, err
		}
		for _, tr := range triples {
			targets = append(targets, tr.Object)
		}
	}
	return uniqueSHACLTargets(targets), nil
}

// validate checks one focus node against one shape and returns every result.
// Targets of the shape are ignored here: they only decide which focus nodes
// the top level starts from, and nested shapes (sh:node, sh:and, …) are
// applied to whatever node they are handed.
func (v *shaclValidator) validate(ctx context.Context, shape *shaclShape, focus RDFTerm) ([]SHACLValidationResult, error) {
	values, err := v.valueNodes(ctx, shape, focus)
	if err != nil {
		return nil, err
	}

	var results []SHACLValidationResult
	path := RDFTerm{}
	if shape.Path != nil {
		path = shape.Path.Term
	}
	report := func(component string, value *RDFTerm, fallback string) {
		r := SHACLValidationResult{
			FocusNode: focus,
			Path:      path,
			Message:   shape.message(fallback),
			Severity:  shape.Severity,
			Source:    shape.ID,
			Component: component,
		}
		if value != nil {
			r.Value = *value
		}
		results = append(results, r)
	}

	if shape.MinCount != nil && len(values) < *shape.MinCount {
		report(SHACLMinCountConstraintComponent, nil, fmt.Sprintf("Less than %d values (has %d)", *shape.MinCount, len(values)))
	}
	if shape.MaxCount != nil && len(values) > *shape.MaxCount {
		report(SHACLMaxCountConstraintComponent, nil, fmt.Sprintf("More than %d values (has %d)", *shape.MaxCount, len(values)))
	}

	for i := range values {
		val := values[i]

		for _, cls := range shape.Classes {
			ok, err := v.isInstanceOf(ctx, val, cls)
			if err != nil {
				return nil, err
			}
			if !ok {
				report(SHACLClassConstraintComponent, &val, fmt.Sprintf("Value %s is not an instance of %s", val, cls))
			}
		}
		if shape.Datatype != "" && !hasSHACLDatatype(val, shape.Datatype) {
			report(SHACLDatatypeConstraintComponent, &val, fmt.Sprintf("Value %s does not have datatype %s", val, shape.Datatype))
		}
		if shape.NodeKind != "" && !matchesSHACLNodeKind(val, shape.NodeKind) {
			report(SHACLNodeKindConstraintComponent, &val, fmt.Sprintf("Value %s does not match required node kind %s", val, shape.NodeKind))
		}

		for _, rc := range []struct {
			bound     *RDFTerm
			component string
			holds     func(int) bool
			relation  string
		}{
			{shape.MinInclusive, SHACLMinInclusiveConstraintComponent, func(c int) bool { return c >= 0 }, ">="},
			{shape.MaxInclusive, SHACLMaxInclusiveConstraintComponent, func(c int) bool { return c <= 0 }, "<="},
			{shape.MinExclusive, SHACLMinExclusiveConstraintComponent, func(c int) bool { return c > 0 }, ">"},
			{shape.MaxExclusive, SHACLMaxExclusiveConstraintComponent, func(c int) bool { return c < 0 }, "<"},
		} {
			if rc.bound == nil {
				continue
			}
			// The spec makes an incomparable value (a string against a
			// numeric bound, a blank node) a violation, not a pass.
			c, ok := compareSHACLLiterals(val, *rc.bound)
			if !ok {
				report(rc.component, &val, fmt.Sprintf("Value %s is not comparable with %s", val, rc.bound.Value))
			} else if !rc.holds(c) {
				report(rc.component, &val, fmt.Sprintf("Value %s is not %s %s", val.Value, rc.relation, rc.bound.Value))
			}
		}

		if shape.MinLength != nil || shape.MaxLength != nil {
			if val.Kind == RDFTermBlankNode {
				// Blank nodes have no string form, so both components fail.
				if shape.MinLength != nil {
					report(SHACLMinLengthConstraintComponent, &val, fmt.Sprintf("Blank node %s has no string length", val))
				}
				if shape.MaxLength != nil {
					report(SHACLMaxLengthConstraintComponent, &val, fmt.Sprintf("Blank node %s has no string length", val))
				}
			} else {
				n := utf8.RuneCountInString(val.Value)
				if shape.MinLength != nil && n < *shape.MinLength {
					report(SHACLMinLengthConstraintComponent, &val, fmt.Sprintf("Value %s is shorter than %d characters", val, *shape.MinLength))
				}
				if shape.MaxLength != nil && n > *shape.MaxLength {
					report(SHACLMaxLengthConstraintComponent, &val, fmt.Sprintf("Value %s is longer than %d characters", val, *shape.MaxLength))
				}
			}
		}

		if shape.Pattern != nil && (val.Kind == RDFTermBlankNode || !shape.Pattern.MatchString(val.Value)) {
			report(SHACLPatternConstraintComponent, &val, fmt.Sprintf("Value %s does not match pattern %s", val, shape.patternSource))
		}
		if shape.HasLanguageIn && !matchesSHACLLanguageIn(val, shape.LanguageIn) {
			report(SHACLLanguageInConstraintComponent, &val, fmt.Sprintf("Value %s does not have a language tag in %v", val, shape.LanguageIn))
		}
		if shape.HasIn && !containsTerm(shape.In, val) {
			report(SHACLInConstraintComponent, &val, fmt.Sprintf("Value %s is not in the allowed set", val))
		}

		for _, ref := range shape.Node {
			ok, err := v.conforms(ctx, ref, val)
			if err != nil {
				return nil, err
			}
			if !ok {
				report(SHACLNodeConstraintComponent, &val, fmt.Sprintf("Value %s does not conform to shape %s", val, ref))
			}
		}
		for _, ref := range shape.Not {
			ok, err := v.conforms(ctx, ref, val)
			if err != nil {
				return nil, err
			}
			if ok {
				report(SHACLNotConstraintComponent, &val, fmt.Sprintf("Value %s conforms to shape %s, which sh:not forbids", val, ref))
			}
		}
		for _, list := range shape.And {
			n, err := v.countConforming(ctx, list, val)
			if err != nil {
				return nil, err
			}
			if n != len(list) {
				report(SHACLAndConstraintComponent, &val, fmt.Sprintf("Value %s conforms to %d of the %d shapes in sh:and", val, n, len(list)))
			}
		}
		for _, list := range shape.Or {
			n, err := v.countConforming(ctx, list, val)
			if err != nil {
				return nil, err
			}
			if n == 0 {
				report(SHACLOrConstraintComponent, &val, fmt.Sprintf("Value %s conforms to none of the %d shapes in sh:or", val, len(list)))
			}
		}
		for _, list := range shape.Xone {
			n, err := v.countConforming(ctx, list, val)
			if err != nil {
				return nil, err
			}
			if n != 1 {
				report(SHACLXoneConstraintComponent, &val, fmt.Sprintf("Value %s conforms to %d of the shapes in sh:xone, not exactly one", val, n))
			}
		}
	}

	if len(shape.Equals) > 0 || len(shape.Disjoint) > 0 {
		pairResults, err := v.propertyPairResults(ctx, shape, focus, values)
		if err != nil {
			return nil, err
		}
		for _, r := range pairResults {
			report(r.component, &r.value, r.message)
		}
	}

	for _, want := range shape.HasValue {
		if !containsTerm(values, want) {
			report(SHACLHasValueConstraintComponent, nil, fmt.Sprintf("Missing required value %s", want))
		}
	}

	if shape.UniqueLang {
		// One result per language tag used more than once, in first-use order.
		counts := map[string]int{}
		var order []string
		for _, val := range values {
			if val.Kind != RDFTermLiteral || val.Language == "" {
				continue
			}
			tag := strings.ToLower(val.Language)
			if counts[tag] == 0 {
				order = append(order, tag)
			}
			counts[tag]++
		}
		for _, tag := range order {
			if counts[tag] > 1 {
				report(SHACLUniqueLangConstraintComponent, nil, fmt.Sprintf("Language tag %q is used by %d values", tag, counts[tag]))
			}
		}
	}

	if shape.Closed {
		closed, err := v.closedResults(ctx, shape, focus, values)
		if err != nil {
			return nil, err
		}
		results = append(results, closed...)
	}

	// sh:property results are the nested shape's own results: their focus is
	// the value node, their path and severity are the property shape's.
	for _, val := range values {
		for _, ref := range shape.Properties {
			nested, err := v.validate(ctx, v.shapes.shapes[ref.String()], val)
			if err != nil {
				return nil, err
			}
			results = append(results, nested...)
		}
	}
	return results, nil
}

type shaclPairResult struct {
	component string
	value     RDFTerm
	message   string
}

// propertyPairResults checks sh:equals and sh:disjoint (SHACL §4.5), which
// compare the value nodes with the values of another predicate on the focus
// node. sh:equals is two-sided — a value missing on either side is a result —
// because the spec defines it as set equality, and a one-sided check would
// let "width equals height" pass for a rectangle with a width and no height.
func (v *shaclValidator) propertyPairResults(ctx context.Context, shape *shaclShape, focus RDFTerm, values []RDFTerm) ([]shaclPairResult, error) {
	var out []shaclPairResult
	other := func(predicate RDFTerm) ([]RDFTerm, error) {
		if focus.Kind == RDFTermLiteral {
			return nil, nil
		}
		subject := focus
		triples, err := v.findTriples(ctx, TriplePattern{Subject: &subject, Predicate: &predicate})
		if err != nil {
			return nil, err
		}
		terms := make([]RDFTerm, 0, len(triples))
		for _, tr := range triples {
			terms = append(terms, tr.Object)
		}
		return uniqueSHACLTargets(terms), nil
	}
	for _, predicate := range shape.Equals {
		others, err := other(predicate)
		if err != nil {
			return nil, err
		}
		for _, val := range values {
			if !containsTerm(others, val) {
				out = append(out, shaclPairResult{SHACLEqualsConstraintComponent, val, fmt.Sprintf("Value %s is not a value of %s", val, predicate)})
			}
		}
		for _, val := range others {
			if !containsTerm(values, val) {
				out = append(out, shaclPairResult{SHACLEqualsConstraintComponent, val, fmt.Sprintf("Value %s of %s is not a value node", val, predicate)})
			}
		}
	}
	for _, predicate := range shape.Disjoint {
		others, err := other(predicate)
		if err != nil {
			return nil, err
		}
		for _, val := range values {
			if containsTerm(others, val) {
				out = append(out, shaclPairResult{SHACLDisjointConstraintComponent, val, fmt.Sprintf("Value %s is also a value of %s", val, predicate)})
			}
		}
	}
	return out, nil
}

// closedResults reports every triple of a value node whose predicate is
// neither the sh:path of one of this shape's sh:property shapes nor listed in
// sh:ignoredProperties. rdf:type gets no special treatment: the spec does not
// exempt it, so a closed shape that means to allow it lists it explicitly.
func (v *shaclValidator) closedResults(ctx context.Context, shape *shaclShape, focus RDFTerm, values []RDFTerm) ([]SHACLValidationResult, error) {
	allowed := map[string]struct{}{}
	for _, ref := range shape.Properties {
		if p := v.shapes.shapes[ref.String()].Path; p != nil && !p.Inverse {
			allowed[p.Predicate.Value] = struct{}{}
		}
	}
	for _, p := range shape.IgnoredProperties {
		allowed[p.Value] = struct{}{}
	}

	var results []SHACLValidationResult
	for _, val := range values {
		if val.Kind == RDFTermLiteral {
			continue // literals are never subjects
		}
		subject := val
		triples, err := v.findTriples(ctx, TriplePattern{Subject: &subject})
		if err != nil {
			return nil, err
		}
		seen := map[string]struct{}{}
		for _, tr := range triples {
			if _, ok := allowed[tr.Predicate.Value]; ok {
				continue
			}
			key := tr.Predicate.String() + " " + tr.Object.String()
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			results = append(results, SHACLValidationResult{
				FocusNode: focus,
				Path:      tr.Predicate,
				Value:     tr.Object,
				Message:   shape.message(fmt.Sprintf("Predicate %s is not allowed on %s by closed shape %s", tr.Predicate, val, shape.ID)),
				Severity:  shape.Severity,
				Source:    shape.ID,
				Component: SHACLClosedConstraintComponent,
			})
		}
	}
	return results, nil
}

// conforms reports whether node has no validation results against the shape.
// Per spec the nested shape's severity is irrelevant here: any result, even an
// sh:Info, means "does not conform", and the outer constraint reports at its
// own severity.
func (v *shaclValidator) conforms(ctx context.Context, ref RDFTerm, node RDFTerm) (bool, error) {
	results, err := v.validate(ctx, v.shapes.shapes[ref.String()], node)
	if err != nil {
		return false, err
	}
	return len(results) == 0, nil
}

func (v *shaclValidator) countConforming(ctx context.Context, refs []RDFTerm, node RDFTerm) (int, error) {
	n := 0
	for _, ref := range refs {
		ok, err := v.conforms(ctx, ref, node)
		if err != nil {
			return 0, err
		}
		if ok {
			n++
		}
	}
	return n, nil
}

// valueNodes returns the value nodes of focus for shape, as a set. For a node
// shape that is the focus node itself; for a property shape it is the nodes
// reached through the path. Duplicates (the same triple in two named graphs,
// or asserted and inferred) collapse, since SHACL counts nodes, not rows.
func (v *shaclValidator) valueNodes(ctx context.Context, shape *shaclShape, focus RDFTerm) ([]RDFTerm, error) {
	if shape.Path == nil {
		return []RDFTerm{focus}, nil
	}
	predicate := shape.Path.Predicate
	var pattern TriplePattern
	if shape.Path.Inverse {
		pattern = TriplePattern{Predicate: &predicate, Object: &focus}
	} else {
		if focus.Kind == RDFTermLiteral {
			return nil, nil // a literal has no outgoing edges
		}
		pattern = TriplePattern{Subject: &focus, Predicate: &predicate}
	}
	triples, err := v.findTriples(ctx, pattern)
	if err != nil {
		return nil, err
	}
	values := make([]RDFTerm, 0, len(triples))
	for _, tr := range triples {
		if shape.Path.Inverse {
			// FindTriples matches a plain literal object against any datatype;
			// the value node must be exactly the focus term.
			if !termsEqual(tr.Object, focus) {
				continue
			}
			values = append(values, tr.Subject)
		} else {
			values = append(values, tr.Object)
		}
	}
	return uniqueSHACLTargets(values), nil
}

// isInstanceOf is SHACL's "SHACL instance": node has rdf:type of cls or of a
// class that reaches cls through rdfs:subClassOf*. It walks the data graph
// directly instead of relying on RDFS inference having been materialised,
// because the spec requires the traversal and a gate should not pass or fail
// depending on whether someone remembered to refresh inference.
func (v *shaclValidator) isInstanceOf(ctx context.Context, node RDFTerm, cls RDFTerm) (bool, error) {
	if node.Kind == RDFTermLiteral {
		return false, nil
	}
	typePredicate := NewIRI(RDFType)
	types, err := v.findTriples(ctx, TriplePattern{Subject: &node, Predicate: &typePredicate})
	if err != nil {
		return false, err
	}
	for _, tr := range types {
		supers, err := v.superclassesOf(ctx, tr.Object)
		if err != nil {
			return false, err
		}
		if containsTerm(supers, cls) {
			return true, nil
		}
	}
	return false, nil
}

// superclassesOf returns cls and everything above it via rdfs:subClassOf*,
// breadth-first with a visited set so a cyclic hierarchy terminates.
func (v *shaclValidator) superclassesOf(ctx context.Context, cls RDFTerm) ([]RDFTerm, error) {
	key := cls.String()
	if cached, ok := v.superclasses[key]; ok {
		return cached, nil
	}
	out, err := v.walkSubClassOf(ctx, cls, false)
	if err != nil {
		return nil, err
	}
	v.superclasses[key] = out
	return out, nil
}

// instancesOf returns every SHACL instance of cls: subjects typed with cls or
// with any class below it.
func (v *shaclValidator) instancesOf(ctx context.Context, cls RDFTerm) ([]RDFTerm, error) {
	classes, err := v.walkSubClassOf(ctx, cls, true)
	if err != nil {
		return nil, err
	}
	typePredicate := NewIRI(RDFType)
	var out []RDFTerm
	for i := range classes {
		triples, err := v.findTriples(ctx, TriplePattern{Predicate: &typePredicate, Object: &classes[i]})
		if err != nil {
			return nil, err
		}
		for _, tr := range triples {
			out = append(out, tr.Subject)
		}
	}
	return out, nil
}

// walkSubClassOf follows rdfs:subClassOf from start, upwards (to superclasses)
// or downwards (to subclasses), and returns start plus everything reached.
func (v *shaclValidator) walkSubClassOf(ctx context.Context, start RDFTerm, down bool) ([]RDFTerm, error) {
	subClassOf := NewIRI(rdfsSubClassOfIRI)
	visited := map[string]struct{}{start.String(): {}}
	out := []RDFTerm{start}
	queue := []RDFTerm{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.Kind == RDFTermLiteral {
			continue
		}
		pattern := TriplePattern{Subject: &current, Predicate: &subClassOf}
		if down {
			pattern = TriplePattern{Predicate: &subClassOf, Object: &current}
		}
		triples, err := v.findTriples(ctx, pattern)
		if err != nil {
			return nil, err
		}
		for _, tr := range triples {
			next := tr.Object
			if down {
				next = tr.Subject
			}
			if _, ok := visited[next.String()]; ok {
				continue
			}
			visited[next.String()] = struct{}{}
			out = append(out, next)
			queue = append(queue, next)
		}
	}
	return out, nil
}

func (s *shaclShape) message(fallback string) string {
	if strings.TrimSpace(s.Message) != "" {
		return s.Message
	}
	return fallback
}

func uniqueSHACLTargets(targets []RDFTerm) []RDFTerm {
	unique := make([]RDFTerm, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		key := target.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, target)
	}
	return unique
}

func matchesSHACLNodeKind(term RDFTerm, nodeKind string) bool {
	switch nodeKind {
	case SHACLIRI:
		return term.Kind == RDFTermIRI
	case SHACLBlankNode:
		return term.Kind == RDFTermBlankNode
	case SHACLLiteral:
		return term.Kind == RDFTermLiteral
	case SHACLBlankNodeOrIRI:
		return term.Kind == RDFTermBlankNode || term.Kind == RDFTermIRI
	case SHACLBlankNodeOrLiteral:
		return term.Kind == RDFTermBlankNode || term.Kind == RDFTermLiteral
	case SHACLIRIOrLiteral:
		return term.Kind == RDFTermIRI || term.Kind == RDFTermLiteral
	default:
		return false
	}
}

// shaclEffectiveDatatype is the RDF 1.1 datatype of a literal: a plain literal
// is xsd:string and a language-tagged one is rdf:langString. Without this a
// shape saying "name is an xsd:string" rejects every untyped name an extractor
// writes.
func shaclEffectiveDatatype(term RDFTerm) string {
	switch {
	case term.Language != "":
		return rdfLiteralDatatype(term) // rdf:dirLangString when it has a base direction (RDF 1.2)
	case term.Datatype == "":
		return xsdStringIRI
	default:
		return term.Datatype
	}
}

var (
	xsdIntegerLexical = regexp.MustCompile(`^[+-]?[0-9]+$`)
	xsdDecimalLexical = regexp.MustCompile(`^[+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)$`)
)

// hasSHACLDatatype also requires the lexical form to be valid for the common
// XSD datatypes, as the spec does: "abc"^^xsd:integer is ill-formed and fails
// sh:datatype xsd:integer even though its datatype IRI matches.
func hasSHACLDatatype(term RDFTerm, datatype string) bool {
	if term.Kind != RDFTermLiteral || shaclEffectiveDatatype(term) != datatype {
		return false
	}
	lex := term.Value
	switch strings.TrimPrefix(datatype, XSDNamespace) {
	case "integer", "long", "int", "short", "byte":
		return xsdIntegerLexical.MatchString(lex)
	case "nonNegativeInteger", "unsignedLong", "unsignedInt", "unsignedShort", "unsignedByte":
		return xsdIntegerLexical.MatchString(lex) && (!strings.HasPrefix(lex, "-") || strings.Trim(lex, "-0") == "")
	case "positiveInteger":
		return xsdIntegerLexical.MatchString(lex) && !strings.HasPrefix(lex, "-") && strings.Trim(lex, "+0") != ""
	case "decimal":
		return xsdDecimalLexical.MatchString(lex)
	case "double", "float":
		if lex == "INF" || lex == "-INF" || lex == "+INF" || lex == "NaN" {
			return true
		}
		_, err := strconv.ParseFloat(lex, 64)
		return err == nil && !strings.ContainsAny(lex, "xXpP_") && !strings.EqualFold(lex, "inf") && !strings.EqualFold(lex, "infinity")
	case "boolean":
		return lex == "true" || lex == "false" || lex == "1" || lex == "0"
	case "date":
		_, ok := parseSHACLTime(lex, false)
		return ok
	case "dateTime":
		_, ok := parseSHACLTime(lex, true)
		return ok
	}
	return true
}

// compareSHACLLiterals orders value against bound the way SPARQL's < does for
// the types that matter here: numbers numerically, xsd:date / xsd:dateTime
// chronologically, strings lexically. ok is false when the two are not
// comparable, which the range components turn into a violation.
//
// An untyped literal that parses as a number is treated as a number. Strictly
// "0" is a string, but shapes in this package have always been written with
// untyped bounds (sh:minInclusive "0"), and refusing them would turn every
// such shape into a wall of violations.
func compareSHACLLiterals(value, bound RDFTerm) (int, bool) {
	if value.Kind != RDFTermLiteral || bound.Kind != RDFTermLiteral {
		return 0, false
	}
	if a, ok := shaclNumber(value); ok {
		if b, ok := shaclNumber(bound); ok {
			return compareFloat(a, b)
		}
		return 0, false
	}
	vdt, bdt := shaclEffectiveDatatype(value), shaclEffectiveDatatype(bound)
	if vdt == bdt && (vdt == XSDNamespace+"date" || vdt == XSDNamespace+"dateTime") {
		a, okA := parseSHACLTime(value.Value, vdt == XSDNamespace+"dateTime")
		b, okB := parseSHACLTime(bound.Value, vdt == XSDNamespace+"dateTime")
		if okA && okB {
			return a.Compare(b), true
		}
		return 0, false
	}
	if vdt == xsdStringIRI && bdt == xsdStringIRI {
		if _, numeric := shaclNumber(bound); !numeric {
			return strings.Compare(value.Value, bound.Value), true
		}
	}
	return 0, false
}

func compareFloat(a, b float64) (int, bool) {
	if math.IsNaN(a) || math.IsNaN(b) {
		return 0, false
	}
	switch {
	case a < b:
		return -1, true
	case a > b:
		return 1, true
	default:
		return 0, true
	}
}

func shaclNumber(term RDFTerm) (float64, bool) {
	if term.Language != "" {
		return 0, false
	}
	switch strings.TrimPrefix(term.Datatype, XSDNamespace) {
	case "", "integer", "decimal", "double", "float", "long", "int", "short", "byte",
		"nonNegativeInteger", "positiveInteger", "negativeInteger", "nonPositiveInteger",
		"unsignedLong", "unsignedInt", "unsignedShort", "unsignedByte":
	default:
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(term.Value), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func parseSHACLTime(lex string, withTime bool) (time.Time, bool) {
	layouts := []string{"2006-01-02", "2006-01-02Z07:00"}
	if withTime {
		layouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999"}
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, lex); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// matchesSHACLLanguageIn applies SPARQL langMatches basic filtering: a range
// matches its own tag and any subtag of it ("en" matches "en-GB"), "*" matches
// any non-empty tag, and a value with no language tag never matches.
func matchesSHACLLanguageIn(term RDFTerm, ranges []string) bool {
	if term.Kind != RDFTermLiteral || term.Language == "" {
		return false
	}
	tag := strings.ToLower(term.Language)
	for _, r := range ranges {
		if r == "*" || tag == r || strings.HasPrefix(tag, r+"-") {
			return true
		}
	}
	return false
}
