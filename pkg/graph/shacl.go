package graph

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SHACL IRIs
const (
	SHACLNamespace = "http://www.w3.org/ns/shacl#"
	RDFNamespace   = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	XSDNamespace   = "http://www.w3.org/2001/XMLSchema#"

	SHACLNodeShape         = SHACLNamespace + "NodeShape"
	SHACLPropertyShape     = SHACLNamespace + "PropertyShape"
	SHACLProperty          = SHACLNamespace + "property"
	SHACLPath              = SHACLNamespace + "path"
	SHACLInversePath       = SHACLNamespace + "inversePath"
	SHACLTargetClass       = SHACLNamespace + "targetClass"
	SHACLTargetNode        = SHACLNamespace + "targetNode"
	SHACLTargetSubjectsOf  = SHACLNamespace + "targetSubjectsOf"
	SHACLTargetObjectsOf   = SHACLNamespace + "targetObjectsOf"
	SHACLDatatype          = SHACLNamespace + "datatype"
	SHACLMinCount          = SHACLNamespace + "minCount"
	SHACLMaxCount          = SHACLNamespace + "maxCount"
	SHACLMinInclusive      = SHACLNamespace + "minInclusive"
	SHACLMaxInclusive      = SHACLNamespace + "maxInclusive"
	SHACLMinExclusive      = SHACLNamespace + "minExclusive"
	SHACLMaxExclusive      = SHACLNamespace + "maxExclusive"
	SHACLMinLength         = SHACLNamespace + "minLength"
	SHACLMaxLength         = SHACLNamespace + "maxLength"
	SHACLPattern           = SHACLNamespace + "pattern"
	SHACLFlags             = SHACLNamespace + "flags"
	SHACLLanguageIn        = SHACLNamespace + "languageIn"
	SHACLUniqueLang        = SHACLNamespace + "uniqueLang"
	SHACLIn                = SHACLNamespace + "in"
	SHACLHasValue          = SHACLNamespace + "hasValue"
	SHACLNodeKind          = SHACLNamespace + "nodeKind"
	SHACLClass             = SHACLNamespace + "class"
	SHACLNode              = SHACLNamespace + "node"
	SHACLNot               = SHACLNamespace + "not"
	SHACLAnd               = SHACLNamespace + "and"
	SHACLOr                = SHACLNamespace + "or"
	SHACLXone              = SHACLNamespace + "xone"
	SHACLClosed            = SHACLNamespace + "closed"
	SHACLIgnoredProperties = SHACLNamespace + "ignoredProperties"
	SHACLSeverity          = SHACLNamespace + "severity"
	SHACLMessage           = SHACLNamespace + "message"
	SHACLEquals            = SHACLNamespace + "equals"
	SHACLDisjoint          = SHACLNamespace + "disjoint"

	SHACLSeverityInfo      = SHACLNamespace + "Info"
	SHACLSeverityWarning   = SHACLNamespace + "Warning"
	SHACLSeverityViolation = SHACLNamespace + "Violation"

	SHACLIRI                = SHACLNamespace + "IRI"
	SHACLBlankNode          = SHACLNamespace + "BlankNode"
	SHACLLiteral            = SHACLNamespace + "Literal"
	SHACLBlankNodeOrIRI     = SHACLNamespace + "BlankNodeOrIRI"
	SHACLBlankNodeOrLiteral = SHACLNamespace + "BlankNodeOrLiteral"
	SHACLIRIOrLiteral       = SHACLNamespace + "IRIOrLiteral"

	RDFType = RDFNamespace + "type"

	rdfFirstIRI      = RDFNamespace + "first"
	rdfRestIRI       = RDFNamespace + "rest"
	rdfNilIRI        = RDFNamespace + "nil"
	rdfLangStringIRI = RDFNamespace + "langString"
	xsdStringIRI     = XSDNamespace + "string"
)

// SHACL constraint components, reported in SHACLValidationResult.Component.
// They are the spec's own IRIs (sh:sourceConstraintComponent), so a report can
// be compared with what any other SHACL engine says about the same data.
const (
	SHACLClassConstraintComponent        = SHACLNamespace + "ClassConstraintComponent"
	SHACLDatatypeConstraintComponent     = SHACLNamespace + "DatatypeConstraintComponent"
	SHACLNodeKindConstraintComponent     = SHACLNamespace + "NodeKindConstraintComponent"
	SHACLMinCountConstraintComponent     = SHACLNamespace + "MinCountConstraintComponent"
	SHACLMaxCountConstraintComponent     = SHACLNamespace + "MaxCountConstraintComponent"
	SHACLMinInclusiveConstraintComponent = SHACLNamespace + "MinInclusiveConstraintComponent"
	SHACLMaxInclusiveConstraintComponent = SHACLNamespace + "MaxInclusiveConstraintComponent"
	SHACLMinExclusiveConstraintComponent = SHACLNamespace + "MinExclusiveConstraintComponent"
	SHACLMaxExclusiveConstraintComponent = SHACLNamespace + "MaxExclusiveConstraintComponent"
	SHACLMinLengthConstraintComponent    = SHACLNamespace + "MinLengthConstraintComponent"
	SHACLMaxLengthConstraintComponent    = SHACLNamespace + "MaxLengthConstraintComponent"
	SHACLPatternConstraintComponent      = SHACLNamespace + "PatternConstraintComponent"
	SHACLLanguageInConstraintComponent   = SHACLNamespace + "LanguageInConstraintComponent"
	SHACLUniqueLangConstraintComponent   = SHACLNamespace + "UniqueLangConstraintComponent"
	SHACLInConstraintComponent           = SHACLNamespace + "InConstraintComponent"
	SHACLHasValueConstraintComponent     = SHACLNamespace + "HasValueConstraintComponent"
	SHACLNodeConstraintComponent         = SHACLNamespace + "NodeConstraintComponent"
	SHACLNotConstraintComponent          = SHACLNamespace + "NotConstraintComponent"
	SHACLAndConstraintComponent          = SHACLNamespace + "AndConstraintComponent"
	SHACLOrConstraintComponent           = SHACLNamespace + "OrConstraintComponent"
	SHACLXoneConstraintComponent         = SHACLNamespace + "XoneConstraintComponent"
	SHACLClosedConstraintComponent       = SHACLNamespace + "ClosedConstraintComponent"
	SHACLEqualsConstraintComponent       = SHACLNamespace + "EqualsConstraintComponent"
	SHACLDisjointConstraintComponent     = SHACLNamespace + "DisjointConstraintComponent"
)

// SHACLValidationResult represents a single constraint violation.
//
// Path is the shape's sh:path for property shapes, empty for node shapes, and
// the offending predicate for sh:closed — the same choices the spec makes for
// sh:resultPath. Component names the constraint component that produced the
// result, so a caller can tell "wrong class" from "too many values" without
// parsing Message.
type SHACLValidationResult struct {
	FocusNode RDFTerm `json:"focus_node"`
	Path      RDFTerm `json:"path"`
	Value     RDFTerm `json:"value,omitempty"`
	Message   string  `json:"message"`
	Severity  string  `json:"severity"`
	Source    RDFTerm `json:"source_shape"`
	Component string  `json:"source_constraint_component,omitempty"`
	// SourceConstraint is the sh:sparql constraint or SPARQL validator that
	// produced a SHACL-SPARQL result (sh:sourceConstraint).
	SourceConstraint RDFTerm `json:"source_constraint,omitempty"`
}

// SHACLReport contains the outcome of SHACL validation.
type SHACLReport struct {
	Conforms bool                    `json:"conforms"`
	Results  []SHACLValidationResult `json:"results,omitempty"`
}

// shaclShape is one SHACL shape, node or property. The spec treats the two as
// one concept told apart only by sh:path, and so does this: a node shape's only
// value node is its focus node, a property shape's value nodes are reached
// through its path, and every value-based constraint is then checked the same
// way. That is what makes constraints written directly on a node shape (a
// sh:class or sh:in on the focus node itself) work without a second code path.
type shaclShape struct {
	ID       RDFTerm
	Path     *shaclPath
	Severity string
	Message  string

	TargetClass      []RDFTerm
	TargetNode       []RDFTerm
	TargetSubjectsOf []RDFTerm
	TargetObjectsOf  []RDFTerm

	Datatype      string
	NodeKind      string
	Classes       []RDFTerm
	MinCount      *int
	MaxCount      *int
	MinInclusive  *RDFTerm
	MaxInclusive  *RDFTerm
	MinExclusive  *RDFTerm
	MaxExclusive  *RDFTerm
	MinLength     *int
	MaxLength     *int
	Pattern       *regexp.Regexp
	patternSource string
	flags         string
	In            []RDFTerm
	HasIn         bool
	HasValue      []RDFTerm
	LanguageIn    []string
	HasLanguageIn bool
	UniqueLang    bool
	Equals        []RDFTerm
	Disjoint      []RDFTerm

	Properties        []RDFTerm
	Node              []RDFTerm
	Not               []RDFTerm
	And               [][]RDFTerm
	Or                [][]RDFTerm
	Xone              [][]RDFTerm
	Closed            bool
	IgnoredProperties []RDFTerm

	// Deactivated shapes produce no results (§2.1.5).
	Deactivated bool
	// LessThan and LessThanOrEquals name predicates whose values every value
	// node must be below (§4.5.3, §4.5.4).
	LessThan         []RDFTerm
	LessThanOrEquals []RDFTerm
	// The qualified value shape (§4.7.3): QualifiedMin/Max bound how many
	// value nodes conform to it; Disjoint excludes those that also conform
	// to a sibling's qualified shape.
	QualifiedShape    *RDFTerm
	QualifiedMin      *int
	QualifiedMax      *int
	QualifiedDisjoint bool
	// siblings are the qualified value shapes of the other property shapes
	// of this shape's parents, set after parsing.
	siblings []RDFTerm
	// SPARQL are the shape's SPARQL-based constraints: its sh:sparql and the
	// SPARQL constraint components it activates (shacl_sparql.go).
	SPARQL []*shaclSPARQLConstraint
}

// shaclPath is a SHACL property path (§2.3.1): a predicate, or an inverse,
// sequence, alternative, zero-or-more, one-or-more or zero-or-one path built
// from others. Term is the sh:path value as written, which a report gives
// as sh:resultPath. Predicate and Inverse describe the two simple forms, a
// predicate IRI and [ sh:inversePath <iri> ], which sh:closed and SHACL rules
// read directly; see shacl_paths.go for the rest.
type shaclPath struct {
	Term      RDFTerm
	Predicate RDFTerm
	Inverse   bool

	op   shaclPathOp
	kids []*shaclPath
}

// shaclShapesGraph is the parsed shapes graph. Shapes are parsed on demand by
// reference and memoised, because sh:node, sh:property and the logical
// constraints can name a shape that is declared nowhere as a sh:NodeShape.
type shaclShapesGraph struct {
	// components are the SPARQL-based constraint components, read on first
	// use.
	components []*shaclComponent
	bySubject  map[string][]RDFTriple
	shapes     map[string]*shaclShape
	roots      []*shaclShape
}

// ValidateSHACL runs SHACL validation against the graph store using the provided shapes.
func (g *GraphStore) ValidateSHACL(ctx context.Context, shapeTriples []RDFTriple) (*SHACLReport, error) {
	namespaces, err := g.ListNamespaces(ctx)
	if err != nil {
		return nil, err
	}
	// Data IRIs are stored expanded, so a shape written with a registered
	// prefix has to be expanded the same way before its terms are compared
	// with data terms directly (sh:in, sh:hasValue, sh:closed).
	expanded := make([]RDFTriple, len(shapeTriples))
	for i, tr := range shapeTriples {
		tr.Subject = expandSHACLTerm(tr.Subject, namespaces)
		tr.Predicate = expandSHACLTerm(tr.Predicate, namespaces)
		tr.Object = expandSHACLTerm(tr.Object, namespaces)
		expanded[i] = tr
	}

	shapes, err := parseSHACLShapes(expanded)
	if err != nil {
		return nil, fmt.Errorf("parse shapes: %v", err)
	}

	v := newSHACLValidator(g, shapes)
	report := &SHACLReport{}
	for _, shape := range shapes.roots {
		targets, err := v.targets(ctx, shape)
		if err != nil {
			return nil, err
		}
		for _, focusNode := range targets {
			results, err := v.validate(ctx, shape, focusNode)
			if err != nil {
				return nil, err
			}
			report.Results = append(report.Results, results...)
		}
	}

	// SHACL §3.6.2: sh:conforms is true if and only if validation produced no
	// results at all. Severity does not enter into it — a sh:Warning or
	// sh:Info result still makes the report non-conforming. Callers that want
	// a gate which tolerates warnings filter Results by Severity themselves.
	report.Conforms = len(report.Results) == 0
	return report, nil
}

func expandSHACLTerm(term RDFTerm, namespaces []Namespace) RDFTerm {
	switch term.Kind {
	case RDFTermIRI:
		term.Value = expandIRIWithNamespaces(strings.TrimSpace(term.Value), namespaces)
	case RDFTermLiteral:
		if term.Datatype != "" {
			term.Datatype = expandIRIWithNamespaces(strings.TrimSpace(term.Datatype), namespaces)
		}
	}
	return term
}

func parseSHACLShapes(triples []RDFTriple) (*shaclShapesGraph, error) {
	sg := &shaclShapesGraph{
		bySubject: make(map[string][]RDFTriple),
		shapes:    make(map[string]*shaclShape),
	}
	// First-appearance order, so the report is ordered the way the shapes
	// were written instead of by Go's map iteration.
	var order []string
	for _, tr := range triples {
		key := tr.Subject.String()
		if _, ok := sg.bySubject[key]; !ok {
			order = append(order, key)
		}
		sg.bySubject[key] = append(sg.bySubject[key], tr)
	}

	for _, key := range order {
		subjectTriples := sg.bySubject[key]
		if !isSHACLShapeRoot(subjectTriples) {
			continue
		}
		shape, err := sg.shape(subjectTriples[0].Subject)
		if err != nil {
			return nil, err
		}
		sg.roots = append(sg.roots, shape)
	}

	for _, shape := range sg.shapes {
		for _, ref := range shape.Properties {
			if sg.shapes[ref.String()].Path == nil {
				return nil, fmt.Errorf("property shape %s (sh:property of %s) has no sh:path", ref, shape.ID)
			}
		}
		// Sibling qualified value shapes: those of the other property
		// shapes of the same parent.
		for _, ref := range shape.Properties {
			ps := sg.shapes[ref.String()]
			if !ps.QualifiedDisjoint {
				continue
			}
			for _, other := range shape.Properties {
				if os := sg.shapes[other.String()]; os != ps && os.QualifiedShape != nil && !containsTerm(ps.siblings, *os.QualifiedShape) {
					ps.siblings = append(ps.siblings, *os.QualifiedShape)
				}
			}
		}
	}
	if err := sg.refuseRecursion(); err != nil {
		return nil, err
	}
	return sg, nil
}

// isSHACLShapeRoot reports whether a subject is a shape whose targets are
// evaluated. The spec counts subjects of target predicates as shapes whether
// or not they are typed, so an untyped shape with a sh:targetClass still runs.
func isSHACLShapeRoot(triples []RDFTriple) bool {
	for _, tr := range triples {
		switch tr.Predicate.Value {
		case RDFType:
			if tr.Object.Value == SHACLNodeShape || tr.Object.Value == SHACLPropertyShape {
				return true
			}
		case SHACLTargetClass, SHACLTargetNode, SHACLTargetSubjectsOf, SHACLTargetObjectsOf:
			return true
		}
	}
	return false
}

// shape parses the shape named by id, or returns the memoised one. A shape
// with no triples at all is legal — it has no constraints, so everything
// conforms to it — which is why a missing subject is not an error here.
func (sg *shaclShapesGraph) shape(id RDFTerm) (*shaclShape, error) {
	key := id.String()
	if existing, ok := sg.shapes[key]; ok {
		return existing, nil
	}
	shape := &shaclShape{ID: id, Severity: SHACLSeverityViolation}
	// Memoise before parsing references so a recursive shape terminates here;
	// refuseRecursion reports it once parsing is complete.
	sg.shapes[key] = shape

	for _, tr := range sg.bySubject[key] {
		if err := sg.applyShapeTriple(shape, tr); err != nil {
			return nil, fmt.Errorf("shape %s: %w", id, err)
		}
	}
	if err := sg.applyComponents(shape); err != nil {
		return nil, fmt.Errorf("shape %s: %w", id, err)
	}
	// Implicit class target (§2.1.3.3): a shape that is also a class
	// targets its own instances.
	if sg.isClassShape(id) {
		shape.TargetClass = append(shape.TargetClass, id)
	}
	if shape.patternSource != "" {
		re, err := compileSHACLPattern(shape.patternSource, shape.flags)
		if err != nil {
			return nil, fmt.Errorf("shape %s: %w", id, err)
		}
		shape.Pattern = re
	} else if shape.flags != "" {
		return nil, fmt.Errorf("shape %s: sh:flags without sh:pattern", id)
	}
	if shape.Path == nil {
		for _, c := range []struct {
			name string
			set  bool
		}{
			{"sh:minCount", shape.MinCount != nil},
			{"sh:maxCount", shape.MaxCount != nil},
			{"sh:uniqueLang", shape.UniqueLang},
		} {
			if c.set {
				return nil, fmt.Errorf("shape %s: %s is only defined on property shapes (it has no sh:path)", id, c.name)
			}
		}
	}
	return shape, nil
}

func (sg *shaclShapesGraph) applyShapeTriple(shape *shaclShape, tr RDFTriple) error {
	obj := tr.Object
	switch tr.Predicate.Value {
	case SHACLPath:
		if shape.Path != nil {
			return fmt.Errorf("more than one sh:path")
		}
		path, err := sg.parsePath(obj)
		if err != nil {
			return err
		}
		shape.Path = path
	case SHACLTargetClass:
		shape.TargetClass = append(shape.TargetClass, obj)
	case SHACLTargetNode:
		shape.TargetNode = append(shape.TargetNode, obj)
	case SHACLTargetSubjectsOf:
		if obj.Kind != RDFTermIRI {
			return fmt.Errorf("sh:targetSubjectsOf must be an IRI, got %s", obj)
		}
		shape.TargetSubjectsOf = append(shape.TargetSubjectsOf, obj)
	case SHACLTargetObjectsOf:
		if obj.Kind != RDFTermIRI {
			return fmt.Errorf("sh:targetObjectsOf must be an IRI, got %s", obj)
		}
		shape.TargetObjectsOf = append(shape.TargetObjectsOf, obj)
	case SHACLDatatype:
		if obj.Kind != RDFTermIRI {
			return fmt.Errorf("sh:datatype must be an IRI, got %s", obj)
		}
		if shape.Datatype != "" {
			return fmt.Errorf("more than one sh:datatype")
		}
		shape.Datatype = obj.Value
	case SHACLNodeKind:
		switch obj.Value {
		case SHACLIRI, SHACLBlankNode, SHACLLiteral, SHACLBlankNodeOrIRI, SHACLBlankNodeOrLiteral, SHACLIRIOrLiteral:
		default:
			return fmt.Errorf("sh:nodeKind %s is not one of the six SHACL node kinds", obj)
		}
		if shape.NodeKind != "" {
			return fmt.Errorf("more than one sh:nodeKind")
		}
		shape.NodeKind = obj.Value
	case SHACLClass:
		if obj.Kind == RDFTermLiteral {
			return fmt.Errorf("sh:class must be an IRI or blank node, got %s", obj)
		}
		shape.Classes = append(shape.Classes, obj)
	case SHACLMinCount:
		return setSHACLInt(&shape.MinCount, obj, "sh:minCount")
	case SHACLMaxCount:
		return setSHACLInt(&shape.MaxCount, obj, "sh:maxCount")
	case SHACLMinLength:
		return setSHACLInt(&shape.MinLength, obj, "sh:minLength")
	case SHACLMaxLength:
		return setSHACLInt(&shape.MaxLength, obj, "sh:maxLength")
	case SHACLMinInclusive:
		return setSHACLBound(&shape.MinInclusive, obj, "sh:minInclusive")
	case SHACLMaxInclusive:
		return setSHACLBound(&shape.MaxInclusive, obj, "sh:maxInclusive")
	case SHACLMinExclusive:
		return setSHACLBound(&shape.MinExclusive, obj, "sh:minExclusive")
	case SHACLMaxExclusive:
		return setSHACLBound(&shape.MaxExclusive, obj, "sh:maxExclusive")
	case SHACLPattern:
		if obj.Kind != RDFTermLiteral {
			return fmt.Errorf("sh:pattern must be a literal, got %s", obj)
		}
		if shape.patternSource != "" {
			return fmt.Errorf("more than one sh:pattern")
		}
		shape.patternSource = obj.Value
	case SHACLFlags:
		if obj.Kind != RDFTermLiteral {
			return fmt.Errorf("sh:flags must be a literal, got %s", obj)
		}
		shape.flags = obj.Value
	case SHACLIn:
		values, err := sg.parseInValues(obj)
		if err != nil {
			return err
		}
		shape.In = append(shape.In, values...)
		shape.HasIn = true
	case SHACLHasValue:
		shape.HasValue = append(shape.HasValue, obj)
	case SHACLLanguageIn:
		items, err := sg.parseList(obj, "sh:languageIn")
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.Kind != RDFTermLiteral {
				return fmt.Errorf("sh:languageIn members must be literals, got %s", item)
			}
			shape.LanguageIn = append(shape.LanguageIn, strings.ToLower(strings.TrimSpace(item.Value)))
		}
		shape.HasLanguageIn = true
	case SHACLUniqueLang:
		// Only sh:uniqueLang true activates the constraint (§4.4.4): a
		// different boolean literal, even "1"^^xsd:boolean, is not true.
		if _, err := parseSHACLBool(obj, "sh:uniqueLang"); err != nil {
			return err
		}
		shape.UniqueLang = obj.Value == "true" && (obj.Datatype == "" || obj.Datatype == XSDNamespace+"boolean")
	case SHACLProperty:
		return sg.addShapeRef(&shape.Properties, obj, "sh:property")
	case SHACLNode:
		return sg.addShapeRef(&shape.Node, obj, "sh:node")
	case SHACLNot:
		return sg.addShapeRef(&shape.Not, obj, "sh:not")
	case SHACLAnd:
		return sg.addShapeList(&shape.And, obj, "sh:and")
	case SHACLOr:
		return sg.addShapeList(&shape.Or, obj, "sh:or")
	case SHACLXone:
		return sg.addShapeList(&shape.Xone, obj, "sh:xone")
	case SHACLClosed:
		b, err := parseSHACLBool(obj, "sh:closed")
		if err != nil {
			return err
		}
		shape.Closed = b
	case SHACLIgnoredProperties:
		items, err := sg.parseList(obj, "sh:ignoredProperties")
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.Kind != RDFTermIRI {
				return fmt.Errorf("sh:ignoredProperties members must be IRIs, got %s", item)
			}
		}
		shape.IgnoredProperties = append(shape.IgnoredProperties, items...)
	case SHACLEquals:
		if obj.Kind != RDFTermIRI {
			return fmt.Errorf("sh:equals must be an IRI, got %s", obj)
		}
		shape.Equals = append(shape.Equals, obj)
	case SHACLDisjoint:
		if obj.Kind != RDFTermIRI {
			return fmt.Errorf("sh:disjoint must be an IRI, got %s", obj)
		}
		shape.Disjoint = append(shape.Disjoint, obj)
	case SHACLNamespace + "sparql":
		c, err := sg.parseSPARQLConstraint(obj)
		if err != nil {
			return err
		}
		shape.SPARQL = append(shape.SPARQL, c)
	case SHACLNamespace + "deactivated":
		b, err := parseSHACLBool(obj, "sh:deactivated")
		if err != nil {
			return err
		}
		shape.Deactivated = b
	case SHACLNamespace + "lessThan":
		if obj.Kind != RDFTermIRI {
			return fmt.Errorf("sh:lessThan must be an IRI, got %s", obj)
		}
		shape.LessThan = append(shape.LessThan, obj)
	case SHACLNamespace + "lessThanOrEquals":
		if obj.Kind != RDFTermIRI {
			return fmt.Errorf("sh:lessThanOrEquals must be an IRI, got %s", obj)
		}
		shape.LessThanOrEquals = append(shape.LessThanOrEquals, obj)
	case SHACLNamespace + "qualifiedValueShape":
		if shape.QualifiedShape != nil {
			return fmt.Errorf("more than one sh:qualifiedValueShape")
		}
		var refs []RDFTerm
		if err := sg.addShapeRef(&refs, obj, "sh:qualifiedValueShape"); err != nil {
			return err
		}
		shape.QualifiedShape = &refs[0]
	case SHACLNamespace + "qualifiedMinCount":
		return setSHACLInt(&shape.QualifiedMin, obj, "sh:qualifiedMinCount")
	case SHACLNamespace + "qualifiedMaxCount":
		return setSHACLInt(&shape.QualifiedMax, obj, "sh:qualifiedMaxCount")
	case SHACLNamespace + "qualifiedValueShapesDisjoint":
		b, err := parseSHACLBool(obj, "sh:qualifiedValueShapesDisjoint")
		if err != nil {
			return err
		}
		shape.QualifiedDisjoint = b
	case SHACLSeverity:
		if obj.Kind != RDFTermIRI {
			return fmt.Errorf("sh:severity must be an IRI, got %s", obj)
		}
		shape.Severity = obj.Value
	case SHACLMessage:
		if shape.Message == "" {
			shape.Message = obj.Value
		}
	}
	return nil
}

func (sg *shaclShapesGraph) addShapeRef(dst *[]RDFTerm, obj RDFTerm, param string) error {
	if obj.Kind != RDFTermIRI && obj.Kind != RDFTermBlankNode {
		return fmt.Errorf("%s must name a shape (IRI or blank node), got %s", param, obj)
	}
	if _, err := sg.shape(obj); err != nil {
		return err
	}
	*dst = append(*dst, obj)
	return nil
}

func (sg *shaclShapesGraph) addShapeList(dst *[][]RDFTerm, obj RDFTerm, param string) error {
	members, err := sg.parseList(obj, param)
	if err != nil {
		return err
	}
	for _, member := range members {
		if member.Kind != RDFTermIRI && member.Kind != RDFTermBlankNode {
			return fmt.Errorf("%s members must be shapes (IRI or blank node), got %s", param, member)
		}
		if _, err := sg.shape(member); err != nil {
			return err
		}
	}
	*dst = append(*dst, members)
	return nil
}

// parseList reads an RDF collection (rdf:first / rdf:rest / rdf:nil). Every
// way a list can be malformed is an error naming the parameter, because the
// alternative — treating a broken sh:or as an empty one, say — silently turns
// a constraint into something else.
func (sg *shaclShapesGraph) parseList(head RDFTerm, param string) ([]RDFTerm, error) {
	var values []RDFTerm
	seen := map[string]struct{}{}
	current := head
	for {
		if current.Kind == RDFTermIRI && current.Value == rdfNilIRI {
			return values, nil
		}
		if current.Kind == RDFTermLiteral {
			return nil, fmt.Errorf("%s: expected an RDF list, got literal %s", param, current)
		}
		key := current.String()
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("%s: RDF list cycle at %s", param, key)
		}
		seen[key] = struct{}{}

		var firsts, rests []RDFTerm
		for _, tr := range sg.bySubject[key] {
			switch tr.Predicate.Value {
			case rdfFirstIRI:
				firsts = append(firsts, tr.Object)
			case rdfRestIRI:
				rests = append(rests, tr.Object)
			}
		}
		if len(firsts) != 1 || len(rests) != 1 {
			return nil, fmt.Errorf("%s: RDF list node %s must have exactly one rdf:first and one rdf:rest (has %d and %d)", param, key, len(firsts), len(rests))
		}
		values = append(values, firsts[0])
		current = rests[0]
	}
}

// parseInValues reads sh:in. The spec value is one RDF list, and anything
// that looks like a list is parsed strictly. A literal, or an IRI that is not
// a list node, is taken as a single allowed value and repeated sh:in triples
// accumulate — an extension this package accepted before list support
// existed, kept so shapes written that way do not start failing.
func (sg *shaclShapesGraph) parseInValues(head RDFTerm) ([]RDFTerm, error) {
	if head.Kind == RDFTermLiteral {
		return []RDFTerm{head}, nil
	}
	if head.Kind == RDFTermIRI && head.Value != rdfNilIRI && !sg.isListNode(head) {
		return []RDFTerm{head}, nil
	}
	return sg.parseList(head, "sh:in")
}

func (sg *shaclShapesGraph) isListNode(term RDFTerm) bool {
	for _, tr := range sg.bySubject[term.String()] {
		if tr.Predicate.Value == rdfFirstIRI || tr.Predicate.Value == rdfRestIRI {
			return true
		}
	}
	return false
}

// refuseRecursion rejects shapes that reach themselves through sh:property,
// sh:node, sh:not, sh:and, sh:or or sh:xone. SHACL 1.0 §3.4.3 leaves
// validation with recursive shapes undefined; engines that "support" it pick
// incompatible semantics, and a naive one loops on cyclic data. Refusing up
// front is the only answer that is both defined and the same on every run.
func (sg *shaclShapesGraph) refuseRecursion() error {
	const (
		unvisited = iota
		onStack
		done
	)
	state := make(map[string]int, len(sg.shapes))
	var stack []string
	var visit func(key string) error
	visit = func(key string) error {
		switch state[key] {
		case onStack:
			start := 0
			for i, k := range stack {
				if k == key {
					start = i
				}
			}
			cycle := append(append([]string(nil), stack[start:]...), key)
			return fmt.Errorf("recursive shapes are not supported (SHACL leaves their validation undefined): %s", strings.Join(cycle, " -> "))
		case done:
			return nil
		}
		state[key] = onStack
		stack = append(stack, key)
		for _, ref := range sg.shapes[key].references() {
			if err := visit(ref.String()); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[key] = done
		return nil
	}
	for key := range sg.shapes {
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}

// isClassShape reports whether a shape is also a class: a SHACL instance of
// rdfs:Class in the shapes graph, through rdfs:subClassOf* there.
func (sg *shaclShapesGraph) isClassShape(id RDFTerm) bool {
	isShape := false
	var types []RDFTerm
	for _, tr := range sg.bySubject[id.String()] {
		if tr.Predicate.Value != RDFType {
			continue
		}
		switch tr.Object.Value {
		case SHACLNodeShape, SHACLPropertyShape:
			isShape = true
		default:
			types = append(types, tr.Object)
		}
	}
	if !isShape {
		return false
	}
	seen := map[string]bool{}
	for len(types) > 0 {
		t := types[0]
		types = types[1:]
		if t.Value == rdfsClassIRI {
			return true
		}
		if seen[t.String()] {
			continue
		}
		seen[t.String()] = true
		for _, tr := range sg.bySubject[t.String()] {
			if tr.Predicate.Value == rdfsSubClassOfIRI {
				types = append(types, tr.Object)
			}
		}
	}
	return false
}

func (s *shaclShape) references() []RDFTerm {
	refs := append([]RDFTerm(nil), s.Properties...)
	if s.QualifiedShape != nil {
		refs = append(refs, *s.QualifiedShape)
	}
	refs = append(refs, s.Node...)
	refs = append(refs, s.Not...)
	for _, lists := range [][][]RDFTerm{s.And, s.Or, s.Xone} {
		for _, list := range lists {
			refs = append(refs, list...)
		}
	}
	return refs
}

func setSHACLInt(dst **int, obj RDFTerm, param string) error {
	if obj.Kind != RDFTermLiteral {
		return fmt.Errorf("%s must be a literal integer, got %s", param, obj)
	}
	if *dst != nil {
		return fmt.Errorf("more than one %s", param)
	}
	n, err := strconv.Atoi(strings.TrimSpace(obj.Value))
	if err != nil || n < 0 {
		return fmt.Errorf("%s must be a non-negative integer, got %q", param, obj.Value)
	}
	*dst = &n
	return nil
}

func setSHACLBound(dst **RDFTerm, obj RDFTerm, param string) error {
	if obj.Kind != RDFTermLiteral {
		return fmt.Errorf("%s must be a literal, got %s", param, obj)
	}
	if *dst != nil {
		return fmt.Errorf("more than one %s", param)
	}
	bound := obj
	*dst = &bound
	return nil
}

func parseSHACLBool(obj RDFTerm, param string) (bool, error) {
	if obj.Kind == RDFTermLiteral {
		switch strings.ToLower(strings.TrimSpace(obj.Value)) {
		case "true", "1":
			return true, nil
		case "false", "0":
			return false, nil
		}
	}
	return false, fmt.Errorf("%s must be a boolean literal, got %s", param, obj)
}

// compileSHACLPattern maps sh:flags onto Go's inline flags. SHACL borrows
// XPath's regex flags; Go's RE2 has i, m and s with the same meaning and
// nothing for x or q, so those are refused instead of being ignored.
func compileSHACLPattern(pattern, flags string) (*regexp.Regexp, error) {
	prefix := ""
	for _, f := range flags {
		switch f {
		case 'i', 'm', 's':
			if !strings.ContainsRune(prefix, f) {
				prefix += string(f)
			}
		default:
			return nil, fmt.Errorf("sh:flags %q: only i, m and s are supported", flags)
		}
	}
	if prefix != "" {
		pattern = "(?" + prefix + ")" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("sh:pattern %q does not compile: %v", pattern, err)
	}
	return re, nil
}
