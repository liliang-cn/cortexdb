package graph

// SHACL-SPARQL (SHACL §5, §6): constraints written as SPARQL queries, and
// constraint components whose validators are SPARQL queries.
//
// A sh:sparql constraint is a SELECT; each solution is a validation result.
// A constraint component declares parameters and a validator; every shape
// that gives all its mandatory parameters a value is checked by it, with the
// values pre-bound as $paramName. Validators are ASK queries (true means the
// value node conforms) or SELECT queries like sh:sparql.
//
// Pre-binding (§5.6) replaces a variable by its value throughout the query —
// $this by the focus node, $value by the value node of an ASK, $currentShape
// by the shape, and each parameter by its value. That is defined only for
// queries without MINUS, SERVICE or VALUES, that never assign a pre-bound
// variable with AS, and whose subqueries project every pre-bound variable,
// so any other query is a failure of the shapes graph, not a result.
// $shapesGraph, which the spec makes optional, is not supported: the shapes
// graph is not a graph in the store.
//
// The queries run against the store, as SPARQL queries do, so a SHACL rule's
// view of triples inferred but not yet written is not visible to them.

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// SHACLSPARQLConstraintComponent is the component of sh:sparql results.
const SHACLSPARQLConstraintComponent = SHACLNamespace + "SPARQLConstraintComponent"

type shaclSPARQLConstraint struct {
	// node is the sh:sparql value, or the component's validator.
	node RDFTerm
	// component is sh:SPARQLConstraintComponent or the component's IRI.
	component string
	query     string
	ask       bool
	messages  []RDFTerm
	// params are the component's parameter values, by variable name.
	params      map[string]RDFTerm
	deactivated bool
}

// shaclComponent is a SPARQL-based constraint component.
type shaclComponent struct {
	iri    RDFTerm
	params []shaclParameter
	// validator is sh:validator (ASK, for any shape); nodeValidator and
	// propertyValidator take precedence for their kind of shape.
	validator, nodeValidator, propertyValidator *RDFTerm
}

type shaclParameter struct {
	path     RDFTerm
	name     string
	optional bool
}

// preBoundNext consumes the next token if it is a pre-bound variable and
// returns its value.
func (p *sparqlParser) preBoundNext() (RDFTerm, bool) {
	if len(p.preBound) == 0 || p.peek().Type != sparqlTokenVar {
		return RDFTerm{}, false
	}
	value, ok := p.preBound[strings.TrimPrefix(p.peek().Value, "?")]
	if ok {
		p.next()
	}
	return value, ok
}

// valuesOf lists the objects of subject's predicate in the shapes graph.
func (sg *shaclShapesGraph) valuesOf(subject RDFTerm, predicate string) []RDFTerm {
	var out []RDFTerm
	for _, tr := range sg.bySubject[subject.String()] {
		if tr.Predicate.Value == predicate {
			out = append(out, tr.Object)
		}
	}
	return out
}

// prefixDeclarations renders sh:prefixes as PREFIX lines: the sh:declare
// entries of each value, each a sh:prefix and a sh:namespace.
func (sg *shaclShapesGraph) prefixDeclarations(node RDFTerm) (string, error) {
	var lines []string
	seen := map[string]string{}
	// The declarations of each sh:prefixes value and of what it
	// owl:imports, transitively.
	var holders []RDFTerm
	visited := map[string]bool{}
	queue := sg.valuesOf(node, SHACLNamespace+"prefixes")
	for len(queue) > 0 {
		h := queue[0]
		queue = queue[1:]
		if visited[h.String()] {
			continue
		}
		visited[h.String()] = true
		holders = append(holders, h)
		queue = append(queue, sg.valuesOf(h, "http://www.w3.org/2002/07/owl#imports")...)
	}
	for _, holder := range holders {
		for _, decl := range sg.valuesOf(holder, SHACLNamespace+"declare") {
			prefixes := sg.valuesOf(decl, SHACLNamespace+"prefix")
			namespaces := sg.valuesOf(decl, SHACLNamespace+"namespace")
			if len(prefixes) != 1 || len(namespaces) != 1 {
				return "", fmt.Errorf("sh:declare %s needs exactly one sh:prefix and one sh:namespace", decl)
			}
			prefix, ns := prefixes[0].Value, namespaces[0].Value
			if prev, dup := seen[prefix]; dup {
				if prev != ns {
					return "", fmt.Errorf("prefix %q is declared as both <%s> and <%s>", prefix, prev, ns)
				}
				continue
			}
			seen[prefix] = ns
			lines = append(lines, fmt.Sprintf("PREFIX %s: <%s>", prefix, ns))
		}
	}
	sort.Strings(lines)
	if len(lines) == 0 {
		return "", nil
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// sparqlQueryOf reads the query of a sh:sparql constraint or validator.
func (sg *shaclShapesGraph) sparqlQueryOf(node RDFTerm) (string, bool, error) {
	selects := sg.valuesOf(node, SHACLNamespace+"select")
	asks := sg.valuesOf(node, SHACLNamespace+"ask")
	if len(selects)+len(asks) != 1 {
		return "", false, fmt.Errorf("%s needs exactly one sh:select or sh:ask", node)
	}
	prefixes, err := sg.prefixDeclarations(node)
	if err != nil {
		return "", false, err
	}
	if len(asks) == 1 {
		return prefixes + asks[0].Value, true, nil
	}
	return prefixes + selects[0].Value, false, nil
}

func (sg *shaclShapesGraph) parseSPARQLConstraint(node RDFTerm) (*shaclSPARQLConstraint, error) {
	query, ask, err := sg.sparqlQueryOf(node)
	if err != nil {
		return nil, fmt.Errorf("sh:sparql: %w", err)
	}
	if ask {
		return nil, fmt.Errorf("sh:sparql %s: a SPARQL constraint is a SELECT query", node)
	}
	c := &shaclSPARQLConstraint{node: node, component: SHACLSPARQLConstraintComponent, query: query,
		messages: sg.valuesOf(node, SHACLMessage)}
	for _, d := range sg.valuesOf(node, SHACLNamespace+"deactivated") {
		c.deactivated = d.Value == "true"
	}
	return c, nil
}

// loadComponents finds the SPARQL-based constraint components declared in
// the shapes graph.
func (sg *shaclShapesGraph) loadComponents() error {
	if sg.components != nil {
		return nil
	}
	sg.components = []*shaclComponent{}
	var keys []string
	for key, triples := range sg.bySubject {
		for _, tr := range triples {
			if tr.Predicate.Value == RDFType && sg.subClassOf(tr.Object, SHACLNamespace+"ConstraintComponent") {
				keys = append(keys, key)
				break
			}
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		iri := sg.bySubject[key][0].Subject
		c := &shaclComponent{iri: iri}
		for _, param := range sg.valuesOf(iri, SHACLNamespace+"parameter") {
			paths := sg.valuesOf(param, SHACLPath)
			if len(paths) != 1 || paths[0].Kind != RDFTermIRI {
				return fmt.Errorf("component %s: a parameter needs one sh:path IRI", iri)
			}
			p := shaclParameter{path: paths[0], name: localName(paths[0].Value)}
			for _, o := range sg.valuesOf(param, SHACLNamespace+"optional") {
				p.optional = o.Value == "true"
			}
			c.params = append(c.params, p)
		}
		pick := func(predicate string) *RDFTerm {
			if v := sg.valuesOf(iri, predicate); len(v) > 0 {
				return &v[0]
			}
			return nil
		}
		c.validator = pick(SHACLNamespace + "validator")
		c.nodeValidator = pick(SHACLNamespace + "nodeValidator")
		c.propertyValidator = pick(SHACLNamespace + "propertyValidator")
		sg.components = append(sg.components, c)
	}
	return nil
}

// subClassOf reports whether class is cls or reaches it through
// rdfs:subClassOf* in the shapes graph.
func (sg *shaclShapesGraph) subClassOf(class RDFTerm, cls string) bool {
	seen := map[string]bool{}
	queue := []RDFTerm{class}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c.Kind == RDFTermIRI && c.Value == cls {
			return true
		}
		if seen[c.String()] {
			continue
		}
		seen[c.String()] = true
		queue = append(queue, sg.valuesOf(c, rdfsSubClassOfIRI)...)
	}
	return false
}

// localName is the variable a parameter binds: its path's local name.
func localName(iri string) string {
	if i := strings.LastIndexAny(iri, "#/:"); i >= 0 {
		return iri[i+1:]
	}
	return iri
}

// applyComponents adds the component constraints a shape activates: one per
// combination of parameter values.
func (sg *shaclShapesGraph) applyComponents(shape *shaclShape) error {
	if err := sg.loadComponents(); err != nil {
		return err
	}
	for _, c := range sg.components {
		validator := c.validator
		if shape.Path == nil && c.nodeValidator != nil {
			validator = c.nodeValidator
		}
		if shape.Path != nil && c.propertyValidator != nil {
			validator = c.propertyValidator
		}
		combos := []map[string]RDFTerm{{}}
		active := true
		for _, p := range c.params {
			values := sg.valuesOf(shape.ID, p.path.Value)
			if len(values) == 0 {
				if !p.optional {
					active = false
					break
				}
				continue
			}
			var next []map[string]RDFTerm
			for _, combo := range combos {
				for _, v := range values {
					m := make(map[string]RDFTerm, len(combo)+1)
					for k, x := range combo {
						m[k] = x
					}
					m[p.name] = v
					next = append(next, m)
				}
			}
			combos = next
		}
		if !active || validator == nil {
			continue
		}
		query, ask, err := sg.sparqlQueryOf(*validator)
		if err != nil {
			return fmt.Errorf("component %s: %w", c.iri, err)
		}
		if !ask && validator == c.validator {
			return fmt.Errorf("component %s: sh:validator must be an ASK query", c.iri)
		}
		messages := sg.valuesOf(*validator, SHACLMessage)
		if len(messages) == 0 {
			messages = sg.valuesOf(c.iri, SHACLMessage)
		}
		for _, combo := range combos {
			shape.SPARQL = append(shape.SPARQL, &shaclSPARQLConstraint{node: *validator, component: c.iri.Value,
				query: query, ask: ask, messages: messages, params: combo})
		}
	}
	return nil
}

// sparqlPathSyntax writes a SHACL path in SPARQL property path syntax, for
// $PATH.
func sparqlPathSyntax(p *shaclPath) string {
	join := func(sep string) string {
		parts := make([]string, len(p.kids))
		for i, k := range p.kids {
			parts[i] = sparqlPathSyntax(k)
		}
		return "(" + strings.Join(parts, sep) + ")"
	}
	switch p.op {
	case shaclPathPredicate:
		return "<" + p.Predicate.Value + ">"
	case shaclPathInverse:
		return "^" + sparqlPathSyntax(p.kids[0])
	case shaclPathSequence:
		return join("/")
	case shaclPathAlternative:
		return join("|")
	case shaclPathZeroOrMore:
		return "(" + sparqlPathSyntax(p.kids[0]) + ")*"
	case shaclPathOneOrMore:
		return "(" + sparqlPathSyntax(p.kids[0]) + ")+"
	}
	return "(" + sparqlPathSyntax(p.kids[0]) + ")?"
}

var shaclPathVariable = regexp.MustCompile(`\$PATH\b`)

// runPreBound parses query with the pre-bound variables substituted, checks
// the restrictions pre-binding needs, and runs it.
func (v *shaclValidator) runPreBound(ctx context.Context, query string, preBound map[string]RDFTerm) (*SPARQLResult, error) {
	namespaces, err := v.g.ListNamespaces(ctx)
	if err != nil {
		return nil, err
	}
	prefixes := make(map[string]string, len(namespaces))
	for _, ns := range namespaces {
		prefixes[ns.Prefix] = ns.URI
	}
	// The restrictions are about the query as written, before any
	// variable is replaced, so it is parsed twice.
	written, err := newSPARQLParser(query, clonePrefixes(prefixes)).parse()
	if err != nil {
		return nil, err
	}
	if isSPARQLUpdate(written.QueryType) {
		return nil, fmt.Errorf("a SHACL query cannot be an update")
	}
	if err := checkPreBindable(written, preBound); err != nil {
		return nil, err
	}
	parser := newSPARQLParser(query, prefixes)
	parser.preBound = preBound
	parsed, err := parser.parse()
	if err != nil {
		return nil, err
	}
	result, err := v.g.executeParsedSPARQL(ctx, parsed)
	if err != nil {
		return nil, err
	}
	// A projected pre-bound variable has its value in every solution.
	for _, row := range result.Bindings {
		for name, value := range preBound {
			if _, ok := row[name]; !ok {
				row[name] = value
			}
		}
	}
	return result, nil
}

// checkPreBindable refuses the queries pre-binding is not defined for.
func checkPreBindable(q *sparqlQuery, preBound map[string]RDFTerm) error {
	for _, item := range q.SelectItems {
		if _, ok := preBound[item.Alias]; ok && !isPlainVariable(item) {
			return fmt.Errorf("the query assigns pre-bound ?%s with AS", item.Alias)
		}
	}
	return checkPreBindableGroup(q.Group, preBound)
}

func checkPreBindableGroup(group sparqlGroup, preBound map[string]RDFTerm) error {
	for _, raw := range group.Steps {
		switch step := raw.(type) {
		case sparqlMinusStep:
			return fmt.Errorf("MINUS is not allowed in a query with pre-bound variables")
		case sparqlServiceStep:
			return fmt.Errorf("SERVICE is not allowed in a query with pre-bound variables")
		case sparqlValuesStep:
			return fmt.Errorf("VALUES is not allowed in a query with pre-bound variables")
		case sparqlBindStep:
			if _, ok := preBound[step.Variable]; ok {
				return fmt.Errorf("the query assigns pre-bound ?%s with BIND", step.Variable)
			}
		case sparqlOptionalStep:
			if err := checkPreBindableGroup(step.Group, preBound); err != nil {
				return err
			}
		case sparqlGroupStep:
			if err := checkPreBindableGroup(step.Group, preBound); err != nil {
				return err
			}
		case sparqlUnionStep:
			for _, b := range step.Branches {
				if err := checkPreBindableGroup(b, preBound); err != nil {
					return err
				}
			}
		case sparqlSubQueryStep:
			sub := step.Query
			projected := sub.Vars
			if sub.SelectAll {
				in := newVarSet()
				groupVars(sub.Group, in)
				projected = in.order
			}
			for name := range preBound {
				if name == "shapesGraph" || name == "currentShape" {
					continue
				}
				if !containsString(projected, name) {
					return fmt.Errorf("a subquery must project pre-bound ?%s", name)
				}
			}
			if err := checkPreBindable(sub, preBound); err != nil {
				return err
			}
		}
	}
	return nil
}

var shaclMessageVariable = regexp.MustCompile(`\{[?$]([A-Za-z_][A-Za-z0-9_]*)\}`)

// sparqlMessage is the constraint's first message with {?var} and {$var}
// replaced by the solution's or the parameters' values.
func (c *shaclSPARQLConstraint) sparqlMessage(shape *shaclShape, row map[string]RDFTerm, fallback string) string {
	text := ""
	if len(c.messages) > 0 {
		text = c.messages[0].Value
	} else {
		text = shape.message(fallback)
	}
	return shaclMessageVariable.ReplaceAllStringFunc(text, func(m string) string {
		name := shaclMessageVariable.FindStringSubmatch(m)[1]
		if v, ok := row[name]; ok {
			return v.Value
		}
		if v, ok := c.params[name]; ok {
			return v.Value
		}
		return m
	})
}

// sparqlResults evaluates a shape's SPARQL-based constraints at one focus
// node.
func (v *shaclValidator) sparqlResults(ctx context.Context, shape *shaclShape, focus RDFTerm, values []RDFTerm) ([]SHACLValidationResult, error) {
	var out []SHACLValidationResult
	path := RDFTerm{}
	if shape.Path != nil {
		path = shape.Path.Term
	}
	for _, c := range shape.SPARQL {
		if c.deactivated {
			continue
		}
		preBound := map[string]RDFTerm{"this": focus, "currentShape": shape.ID}
		for name, value := range c.params {
			preBound[name] = value
		}
		query := c.query
		if shaclPathVariable.MatchString(query) {
			if shape.Path == nil {
				return nil, fmt.Errorf("constraint %s uses $PATH at a node shape", c.node)
			}
			query = shaclPathVariable.ReplaceAllLiteralString(query, sparqlPathSyntax(shape.Path))
		}
		result := func(focus, path, value RDFTerm, row map[string]RDFTerm) SHACLValidationResult {
			r := SHACLValidationResult{FocusNode: focus, Path: path, Value: value, Severity: shape.Severity,
				Source: shape.ID, Component: c.component,
				Message: c.sparqlMessage(shape, row, fmt.Sprintf("Value does not conform to %s", c.node))}
			// sh:sourceConstraint names a sh:sparql constraint; a
			// component's results name the component instead.
			if c.component == SHACLSPARQLConstraintComponent {
				r.SourceConstraint = c.node
			}
			return r
		}
		if c.ask {
			for _, val := range values {
				pb := make(map[string]RDFTerm, len(preBound)+1)
				for k, x := range preBound {
					pb[k] = x
				}
				pb["value"] = val
				res, err := v.runPreBound(ctx, query, pb)
				if err != nil {
					return nil, fmt.Errorf("constraint %s: %w", c.node, err)
				}
				if !res.Boolean {
					out = append(out, result(focus, path, val, map[string]RDFTerm{"this": focus, "value": val}))
				}
			}
			continue
		}
		res, err := v.runPreBound(ctx, query, preBound)
		if err != nil {
			return nil, fmt.Errorf("constraint %s: %w", c.node, err)
		}
		for _, row := range res.Bindings {
			if f, ok := row["failure"]; ok && f.Value == "true" {
				return nil, fmt.Errorf("constraint %s reported ?failure at %s", c.node, focus)
			}
			rFocus := focus
			if t, ok := row["this"]; ok {
				rFocus = t
			}
			rPath := path
			if p, ok := row["path"]; ok && p.Kind == RDFTermIRI {
				rPath = p
			}
			// sh:value is ?value, or at a node shape the focus node.
			rValue := RDFTerm{}
			if val, ok := row["value"]; ok {
				rValue = val
			} else if shape.Path == nil {
				rValue = rFocus
			}
			out = append(out, result(rFocus, rPath, rValue, row))
		}
	}
	return out, nil
}
