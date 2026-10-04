package graph

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// SPARQL 1.2: triple terms, reified triples, annotations — and the blank
// nodes, blank node property lists, collections and sequence paths the
// syntactic sugar is defined in terms of.
//
// Everything here is parsing into what the engine already evaluates. A
// reified triple << s p o ~ r >> is the pattern r rdf:reifies <<( s p o )>>
// (SPARQL 1.2 Query §4.2.3); an annotation s p o {| q z |} is the triple s p o
// plus that reifying pattern plus r q z (§4.2.4); a blank node in a pattern is
// a variable no solution projects (§4.1.4). So the engine learns one new kind
// of term pattern — a triple term with variables in it, <<( ?s :p ?o )>> —
// and everything else is rewriting at parse time, which is how the spec
// itself defines the sugar.

// sparqlTriplePattern is a triple term pattern: <<( s p o )>> with a variable
// or blank node somewhere inside.
type sparqlTriplePattern struct {
	Subject   sparqlTermPattern
	Predicate sparqlTermPattern
	Object    sparqlTermPattern
}

// hiddenVariablePrefix marks a variable the query did not name: a blank node
// written in a pattern, or one the sugar introduced. ':' cannot occur in a
// SPARQL variable name, so no query can collide with one, and SELECT * leaves
// them out.
const hiddenVariablePrefix = "_:"

func isHiddenVariable(name string) bool { return strings.HasPrefix(name, hiddenVariablePrefix) }

// varName is the variable a term pattern binds: a named variable, or the
// hidden variable a blank node stands for in a pattern.
func (tp sparqlTermPattern) varName() string {
	if tp.Variable != "" {
		return tp.Variable
	}
	if tp.Blank != "" {
		return hiddenVariablePrefix + tp.Blank
	}
	return ""
}

func (tp sparqlTermPattern) isConstant() bool { return tp.Term != nil }

// sparqlNoMatchKind is the kind of a resolved term that no stored triple can
// hold, such as a triple term whose bound subject is a literal. A pattern
// resolving to it has no solutions, rather than failing the query.
const sparqlNoMatchKind = "no-match"

// resolveTripleTermPattern resolves a triple term pattern under a binding:
// the constant triple term when every part is bound, a no-match term when the
// bound parts cannot form a triple, and nil when something is still free.
func resolveTripleTermPattern(tp *sparqlTriplePattern, binding map[string]RDFTerm) *RDFTerm {
	parts := [3]sparqlTermPattern{tp.Subject, tp.Predicate, tp.Object}
	resolved := [3]*RDFTerm{}
	free := false
	for i, part := range parts {
		var value *RDFTerm
		if part.Triple != nil {
			value = resolveTripleTermPattern(part.Triple, binding)
		} else {
			value, _ = resolvePatternTerm(part, binding)
		}
		if value != nil && value.Kind == sparqlNoMatchKind {
			return value
		}
		if value == nil {
			free = true
			continue
		}
		resolved[i] = value
	}
	if s := resolved[0]; s != nil && s.Kind != RDFTermIRI && s.Kind != RDFTermBlankNode {
		return &RDFTerm{Kind: sparqlNoMatchKind}
	}
	if pr := resolved[1]; pr != nil && pr.Kind != RDFTermIRI {
		return &RDFTerm{Kind: sparqlNoMatchKind}
	}
	if free {
		return nil
	}
	term, err := NewTripleTerm(*resolved[0], *resolved[1], *resolved[2])
	if err != nil {
		return &RDFTerm{Kind: sparqlNoMatchKind}
	}
	return &term
}

func noMatch(term *RDFTerm) bool { return term != nil && term.Kind == sparqlNoMatchKind }

// tripleTermFilterFor turns a partly bound triple term pattern into the
// component filter the store can answer from kg_triple_terms.
func tripleTermFilterFor(tp *sparqlTriplePattern, binding map[string]RDFTerm) *tripleTermFilter {
	filter := &tripleTermFilter{}
	part := func(pattern sparqlTermPattern) *RDFTerm {
		if pattern.Triple != nil {
			return resolveTripleTermPattern(pattern.Triple, binding)
		}
		value, _ := resolvePatternTerm(pattern, binding)
		return value
	}
	filter.Subject = part(tp.Subject)
	filter.Predicate = part(tp.Predicate)
	filter.Object = part(tp.Object)
	return filter
}

// bindTripleTermPattern unifies a triple term pattern with a value, binding
// what is free in the pattern to the matching parts of the value.
func bindTripleTermPattern(binding map[string]RDFTerm, tp *sparqlTriplePattern, value RDFTerm) bool {
	if value.Kind != RDFTermTriple {
		return false
	}
	triple, err := decodeTripleTermValue(value.Value)
	if err != nil {
		return false
	}
	return bindPatternTerm(binding, tp.Subject, triple.Subject) &&
		bindPatternTerm(binding, tp.Predicate, triple.Predicate) &&
		bindPatternTerm(binding, tp.Object, triple.Object)
}

// sparqlTemplateBlanks hands out the fresh blank nodes a template's blank
// nodes become: one per label per solution, as SPARQL §16.2 requires, so two
// solutions never share a reifier the query did not name.
type sparqlTemplateBlanks struct {
	prefix string
	seq    int
	row    map[string]RDFTerm
}

func newSPARQLTemplateBlanks() *sparqlTemplateBlanks {
	var nonce [6]byte
	_, _ = rand.Read(nonce[:])
	return &sparqlTemplateBlanks{prefix: "t" + hex.EncodeToString(nonce[:]) + "_"}
}

func (b *sparqlTemplateBlanks) nextSolution() { b.row = make(map[string]RDFTerm) }

func (b *sparqlTemplateBlanks) node(label string) RDFTerm {
	if b.row == nil {
		b.nextSolution()
	}
	if node, ok := b.row[label]; ok {
		return node
	}
	b.seq++
	node := NewBlankNode(b.prefix + strconv.Itoa(b.seq))
	b.row[label] = node
	return node
}

// instantiateTemplateTerm is a template term under one solution: a constant,
// a bound variable, a fresh blank node, or a triple term built from its
// parts. ok is false when the solution leaves it unbound or it cannot be a
// valid term, and the template triple is then left out (SPARQL §16.2).
func instantiateTemplateTerm(pattern sparqlTermPattern, binding map[string]RDFTerm, blanks *sparqlTemplateBlanks) (RDFTerm, bool) {
	switch {
	case pattern.Term != nil:
		return *pattern.Term, true
	case pattern.Blank != "":
		return blanks.node(pattern.Blank), true
	case pattern.Triple != nil:
		s, ok := instantiateTemplateTerm(pattern.Triple.Subject, binding, blanks)
		if !ok {
			return RDFTerm{}, false
		}
		p, ok := instantiateTemplateTerm(pattern.Triple.Predicate, binding, blanks)
		if !ok {
			return RDFTerm{}, false
		}
		o, ok := instantiateTemplateTerm(pattern.Triple.Object, binding, blanks)
		if !ok {
			return RDFTerm{}, false
		}
		term, err := NewTripleTerm(s, p, o)
		if err != nil {
			return RDFTerm{}, false
		}
		return term, true
	case pattern.Variable != "":
		value, ok := binding[pattern.Variable]
		return value, ok
	}
	return RDFTerm{}, false
}

// templateHasBlankNode reports whether a template mentions a blank node,
// which DELETE templates and DELETE DATA may not (SPARQL Update §3.1.3):
// a fresh node never matches anything already stored, so the deletion could
// only ever be a no-op that looked like it did something.
func templateHasBlankNode(patterns []sparqlPattern) bool {
	var has func(sparqlTermPattern) bool
	has = func(tp sparqlTermPattern) bool {
		if tp.Blank != "" {
			return true
		}
		if tp.Term != nil && tp.Term.Kind == RDFTermBlankNode {
			return true
		}
		if tp.Triple != nil {
			return has(tp.Triple.Subject) || has(tp.Triple.Predicate) || has(tp.Triple.Object)
		}
		return false
	}
	for _, pattern := range patterns {
		if has(pattern.Subject) || has(pattern.Predicate) || has(pattern.Object) || (pattern.Graph != nil && has(*pattern.Graph)) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Parsing triples blocks
// ---------------------------------------------------------------------------

// sparqlTriplesBuilder collects the triple patterns one statement expands to.
type sparqlTriplesBuilder struct {
	p        *sparqlParser
	prefixes map[string]string
	graph    *sparqlTermPattern
	out      []sparqlPattern
}

func (b *sparqlTriplesBuilder) add(subject, predicate sparqlTermPattern, path *sparqlPropertyPath, object sparqlTermPattern) {
	pattern := sparqlPattern{Subject: subject, Predicate: predicate, Path: path, Object: object}
	if b.graph != nil {
		graph := *b.graph
		pattern.Graph = &graph
	}
	b.out = append(b.out, pattern)
}

func (p *sparqlParser) freshBlank(kind string) sparqlTermPattern {
	p.blankSeq++
	return sparqlTermPattern{Blank: "#" + kind + strconv.Itoa(p.blankSeq)}
}

func (p *sparqlParser) peekPunct(value string) bool {
	token := p.peek()
	return token.Type == sparqlTokenPunct && token.Value == value
}

// parseTriplePatternStatement parses one TriplesSameSubjectPath (or, in a
// template, TriplesSameSubject) into the triple patterns it stands for.
func (p *sparqlParser) parseTriplePatternStatement(activeGraph *sparqlTermPattern, prefixes map[string]string) ([]sparqlPattern, error) {
	b := &sparqlTriplesBuilder{p: p, prefixes: prefixes, graph: activeGraph}
	if err := b.statement(); err != nil {
		return nil, err
	}
	return b.out, nil
}

func (b *sparqlTriplesBuilder) statement() error {
	p := b.p
	switch {
	case p.peekPunct("<<"):
		reifier, err := b.reifiedTriple()
		if err != nil {
			return err
		}
		if b.startsVerb() {
			return b.propertyList(reifier)
		}
		return nil
	case p.peekPunct("["):
		if p.peekN(1).Type == sparqlTokenPunct && p.peekN(1).Value == "]" {
			p.next()
			p.next()
			return b.propertyList(p.freshBlank("anon"))
		}
		node, err := b.blankNodePropertyList()
		if err != nil {
			return err
		}
		if b.startsVerb() {
			return b.propertyList(node)
		}
		return nil
	case p.peekPunct("("):
		node, err := b.collection()
		if err != nil {
			return err
		}
		if b.startsVerb() {
			return b.propertyList(node)
		}
		return nil
	}
	if p.peekPunct("<<(") {
		// Legal syntax that can never match: RDF 1.2 has no triple whose
		// subject is a triple term. It still needs its property list.
		subject, err := b.tripleTermPattern()
		if err != nil {
			return err
		}
		return b.propertyList(subject)
	}
	subject, err := b.varOrTerm(true)
	if err != nil {
		return err
	}
	return b.propertyList(subject)
}

// startsVerb reports whether a property list begins at the cursor.
func (b *sparqlTriplesBuilder) startsVerb() bool {
	token := b.p.peek()
	switch token.Type {
	case sparqlTokenEOF:
		return false
	case sparqlTokenPunct:
		return token.Value == "(" // a parenthesised path
	case sparqlTokenKeyword:
		return false
	case sparqlTokenOperator:
		return token.Value == "^" || token.Value == "!"
	}
	return true
}

// propertyList parses Verb ObjectList (';' (Verb ObjectList)?)*.
func (b *sparqlTriplesBuilder) propertyList(subject sparqlTermPattern) error {
	p := b.p
	if !b.startsVerb() {
		return fmt.Errorf("expected a predicate after the subject, got %q", p.peek().Value)
	}
	for {
		predicate, path, sequence, err := b.verb()
		if err != nil {
			return err
		}
		for {
			object, err := b.graphNode()
			if err != nil {
				return err
			}
			b.addWithPath(subject, predicate, path, sequence, object)
			if err := b.annotation(subject, predicate, path, sequence, object); err != nil {
				return err
			}
			if !p.matchPunct(",") {
				break
			}
		}
		if !p.matchPunct(";") {
			return nil
		}
		for p.matchPunct(";") {
		}
		if !b.startsVerb() || p.peekPunct("]") || p.peekPunct("|}") {
			return nil
		}
	}
}

// addWithPath adds subject-predicate-object, expanding a sequence path into
// the chain of patterns joined by hidden variables that SPARQL 1.1 §18.2.2.4
// translates it to.
func (b *sparqlTriplesBuilder) addWithPath(subject, predicate sparqlTermPattern, path *sparqlPropertyPath, sequence []sparqlPathStep, object sparqlTermPattern) {
	if len(sequence) == 0 {
		b.add(subject, predicate, path, object)
		return
	}
	current := subject
	for i, step := range sequence {
		next := object
		if i < len(sequence)-1 {
			next = b.p.freshBlank("seq")
		}
		b.add(current, step.predicate, step.path, next)
		current = next
	}
}

type sparqlPathStep struct {
	predicate sparqlTermPattern
	path      *sparqlPropertyPath
}

// verb parses a predicate: a variable, 'a', an IRI, or a property path. A
// sequence path comes back as its steps.
func (b *sparqlTriplesBuilder) verb() (sparqlTermPattern, *sparqlPropertyPath, []sparqlPathStep, error) {
	p := b.p
	if p.peek().Type == sparqlTokenVar {
		v := p.next()
		// SPARQL has no paths over a variable predicate (VerbSimple). '+'
		// before a number is a signed object literal, not a path.
		if op := p.peek(); op.Type == sparqlTokenOperator && (op.Value == "/" || op.Value == "|" || op.Value == "*" || op.Value == "?" ||
			(op.Value == "+" && p.peekN(1).Type != sparqlTokenNumber)) {
			return sparqlTermPattern{}, nil, nil, fmt.Errorf("a variable cannot be part of a property path")
		}
		return sparqlTermPattern{Variable: strings.TrimPrefix(v.Value, "?")}, nil, nil, nil
	}
	expr, err := p.parsePathAlternative(b.prefixes)
	if err != nil {
		return sparqlTermPattern{}, nil, nil, err
	}
	if p.inTemplate && !expr.simpleIRI() {
		return sparqlTermPattern{}, nil, nil, fmt.Errorf("property paths are not allowed in a template")
	}
	// A top-level sequence is a join through fresh blank nodes, which is
	// how the spec's own translation states it and how the engine joins
	// best; each step is then a predicate or a path of its own.
	if expr.op == pathSequence {
		steps := make([]sparqlPathStep, len(expr.kids))
		for i, k := range expr.kids {
			steps[i] = pathStepOf(k)
		}
		return sparqlTermPattern{}, nil, steps, nil
	}
	step := pathStepOf(expr)
	return step.predicate, step.path, nil, nil
}

// pathStepOf states a path as a plain predicate when it is one IRI.
func pathStepOf(e *sparqlPathExpr) sparqlPathStep {
	if e.simpleIRI() {
		iri := e.iri
		return sparqlPathStep{predicate: sparqlTermPattern{Term: &iri}}
	}
	return sparqlPathStep{path: &sparqlPropertyPath{Expr: e}}
}

// annotation parses (Reifier | AnnotationBlock)* after an object.
func (b *sparqlTriplesBuilder) annotation(subject, predicate sparqlTermPattern, path *sparqlPropertyPath, sequence []sparqlPathStep, object sparqlTermPattern) error {
	p := b.p
	if !p.peekPunct("~") && !p.peekPunct("{|") {
		return nil
	}
	if path != nil || len(sequence) > 0 {
		return fmt.Errorf("a reifier or annotation may follow only a triple whose predicate is an IRI, 'a' or a variable")
	}
	tripleTerm := b.tripleTermOf(subject, predicate, object)
	var reifier *sparqlTermPattern
	for {
		switch {
		case p.matchPunct("~"):
			r, err := b.optionalReifier()
			if err != nil {
				return err
			}
			b.add(r, sparqlReifiesPattern(), nil, tripleTerm)
			reifier = &r
		case p.matchPunct("{|"):
			var r sparqlTermPattern
			if reifier != nil {
				r = *reifier
			} else {
				r = p.freshBlank("anon")
				b.add(r, sparqlReifiesPattern(), nil, tripleTerm)
			}
			if p.peekPunct("|}") {
				return fmt.Errorf("an annotation block cannot be empty")
			}
			if err := b.propertyList(r); err != nil {
				return err
			}
			p.expectPunct("|}")
			reifier = nil
		default:
			return nil
		}
	}
}

func sparqlReifiesPattern() sparqlTermPattern {
	term := NewIRI(RDFReifiesIRI)
	return sparqlTermPattern{Term: &term}
}

// tripleTermOf is the triple term pattern <<( s p o )>>, folded to a
// constant when nothing in it is free.
func (b *sparqlTriplesBuilder) tripleTermOf(subject, predicate, object sparqlTermPattern) sparqlTermPattern {
	tp := &sparqlTriplePattern{Subject: subject, Predicate: predicate, Object: object}
	if subject.isConstant() && predicate.isConstant() && object.isConstant() {
		if term, err := NewTripleTerm(*subject.Term, *predicate.Term, *object.Term); err == nil {
			return sparqlTermPattern{Term: &term}
		}
	}
	return sparqlTermPattern{Triple: tp}
}

// optionalReifier parses what may follow '~': a variable, IRI or blank node,
// or nothing, which stands for a fresh blank node.
func (b *sparqlTriplesBuilder) optionalReifier() (sparqlTermPattern, error) {
	p := b.p
	switch token := p.peek(); token.Type {
	case sparqlTokenVar, sparqlTokenIRI, sparqlTokenQName, sparqlTokenBlank:
		return b.varOrTerm(false)
	case sparqlTokenPunct:
		if token.Value == "[" && p.peekN(1).Type == sparqlTokenPunct && p.peekN(1).Value == "]" {
			p.next()
			p.next()
			return p.freshBlank("anon"), nil
		}
	}
	return p.freshBlank("anon"), nil
}

// graphNode parses an object: VarOrTerm, a triple term, a reified triple, a
// blank node property list or a collection.
func (b *sparqlTriplesBuilder) graphNode() (sparqlTermPattern, error) {
	p := b.p
	switch {
	case p.peekPunct("<<("):
		return b.tripleTermPattern()
	case p.peekPunct("<<"):
		return b.reifiedTriple()
	case p.peekPunct("["):
		if p.peekN(1).Type == sparqlTokenPunct && p.peekN(1).Value == "]" {
			p.next()
			p.next()
			return p.freshBlank("anon"), nil
		}
		return b.blankNodePropertyList()
	case p.peekPunct("("):
		return b.collection()
	}
	return b.varOrTerm(true)
}

// varOrTerm parses a variable, IRI, literal or blank node label.
func (b *sparqlTriplesBuilder) varOrTerm(allowLiteral bool) (sparqlTermPattern, error) {
	return b.p.parseTermPattern(b.prefixes, allowLiteral)
}

// reifiedTriple parses << s p o ~ r >> and returns r, adding the reifying
// pattern r rdf:reifies <<( s p o )>>.
func (b *sparqlTriplesBuilder) reifiedTriple() (sparqlTermPattern, error) {
	p := b.p
	if err := p.enter(); err != nil {
		return sparqlTermPattern{}, err
	}
	defer p.leave()
	p.expectPunct("<<")
	subject, err := b.reifiedTripleNode("subject")
	if err != nil {
		return sparqlTermPattern{}, err
	}
	predicate, err := b.simpleVerb()
	if err != nil {
		return sparqlTermPattern{}, err
	}
	object, err := b.reifiedTripleNode("object")
	if err != nil {
		return sparqlTermPattern{}, err
	}
	var reifier sparqlTermPattern
	if p.matchPunct("~") {
		if reifier, err = b.optionalReifier(); err != nil {
			return sparqlTermPattern{}, err
		}
	} else {
		reifier = p.freshBlank("anon")
	}
	p.expectPunct(">>")
	b.add(reifier, sparqlReifiesPattern(), nil, b.tripleTermOf(subject, predicate, object))
	return reifier, nil
}

// reifiedTripleNode is ReifiedTripleSubject/Object: VarOrTerm without
// collections, or a nested reified triple or triple term.
func (b *sparqlTriplesBuilder) reifiedTripleNode(position string) (sparqlTermPattern, error) {
	p := b.p
	switch {
	case p.peekPunct("<<("):
		return b.tripleTermPattern()
	case p.peekPunct("<<"):
		return b.reifiedTriple()
	case p.peekPunct("["):
		if p.peekN(1).Type == sparqlTokenPunct && p.peekN(1).Value == "]" {
			p.next()
			p.next()
			return p.freshBlank("anon"), nil
		}
		return sparqlTermPattern{}, fmt.Errorf("a reified triple's %s cannot be a blank node property list", position)
	case p.peekPunct("("):
		return sparqlTermPattern{}, fmt.Errorf("a reified triple's %s cannot be a collection", position)
	}
	return b.varOrTerm(true)
}

// simpleVerb is Verb without paths: a variable, an IRI or 'a'.
func (b *sparqlTriplesBuilder) simpleVerb() (sparqlTermPattern, error) {
	p := b.p
	switch p.peek().Type {
	case sparqlTokenVar, sparqlTokenIRI, sparqlTokenQName, sparqlTokenIdent:
	default:
		return sparqlTermPattern{}, fmt.Errorf("expected a predicate IRI or variable, got %q", p.peek().Value)
	}
	predicate, err := b.varOrTerm(false)
	if err != nil {
		return sparqlTermPattern{}, err
	}
	if predicate.Term != nil && predicate.Term.Kind != RDFTermIRI {
		return sparqlTermPattern{}, fmt.Errorf("a predicate must be an IRI or variable")
	}
	if predicate.Blank != "" {
		return sparqlTermPattern{}, fmt.Errorf("a predicate cannot be a blank node")
	}
	if token := p.peek(); token.Type == sparqlTokenOperator && (token.Value == "/" || token.Value == "|" || token.Value == "*" || token.Value == "+" || token.Value == "?") {
		return sparqlTermPattern{}, fmt.Errorf("a property path cannot appear inside a triple term or reified triple")
	}
	return predicate, nil
}

// tripleTermPattern parses <<( s p o )>> in a pattern or template.
func (b *sparqlTriplesBuilder) tripleTermPattern() (sparqlTermPattern, error) {
	p := b.p
	if err := p.enter(); err != nil {
		return sparqlTermPattern{}, err
	}
	defer p.leave()
	p.expectPunct("<<(")
	part := func(position string) (sparqlTermPattern, error) {
		switch {
		case p.peekPunct("<<("):
			return b.tripleTermPattern()
		case p.peekPunct("<<"):
			return sparqlTermPattern{}, fmt.Errorf("a triple term's %s cannot be a reified triple", position)
		case p.peekPunct("["):
			if p.peekN(1).Type == sparqlTokenPunct && p.peekN(1).Value == "]" {
				p.next()
				p.next()
				return p.freshBlank("anon"), nil
			}
			return sparqlTermPattern{}, fmt.Errorf("a triple term's %s cannot be a blank node property list", position)
		case p.peekPunct("("):
			return sparqlTermPattern{}, fmt.Errorf("a triple term's %s cannot be a collection", position)
		}
		return b.varOrTerm(true)
	}
	subject, err := part("subject")
	if err != nil {
		return sparqlTermPattern{}, err
	}
	predicate, err := b.simpleVerb()
	if err != nil {
		return sparqlTermPattern{}, err
	}
	object, err := part("object")
	if err != nil {
		return sparqlTermPattern{}, err
	}
	p.expectPunct(")>>")
	return b.tripleTermOf(subject, predicate, object), nil
}

func (b *sparqlTriplesBuilder) blankNodePropertyList() (sparqlTermPattern, error) {
	p := b.p
	if err := p.enter(); err != nil {
		return sparqlTermPattern{}, err
	}
	defer p.leave()
	p.expectPunct("[")
	node := p.freshBlank("anon")
	if err := b.propertyList(node); err != nil {
		return sparqlTermPattern{}, err
	}
	p.expectPunct("]")
	return node, nil
}

func (b *sparqlTriplesBuilder) collection() (sparqlTermPattern, error) {
	p := b.p
	if err := p.enter(); err != nil {
		return sparqlTermPattern{}, err
	}
	defer p.leave()
	p.expectPunct("(")
	var items []sparqlTermPattern
	for !p.matchPunct(")") {
		if p.peek().Type == sparqlTokenEOF {
			return sparqlTermPattern{}, fmt.Errorf("unterminated collection")
		}
		item, err := b.graphNode()
		if err != nil {
			return sparqlTermPattern{}, err
		}
		items = append(items, item)
	}
	nilTerm := NewIRI(rdf12NamespaceIRI + "nil")
	if len(items) == 0 {
		return sparqlTermPattern{Term: &nilTerm}, nil
	}
	first, rest := NewIRI(rdf12NamespaceIRI+"first"), NewIRI(rdf12NamespaceIRI+"rest")
	head := p.freshBlank("list")
	current := head
	for i, item := range items {
		b.add(current, sparqlTermPattern{Term: &first}, nil, item)
		if i == len(items)-1 {
			b.add(current, sparqlTermPattern{Term: &rest}, nil, sparqlTermPattern{Term: &nilTerm})
			break
		}
		next := p.freshBlank("list")
		b.add(current, sparqlTermPattern{Term: &rest}, nil, next)
		current = next
	}
	return head, nil
}

// ---------------------------------------------------------------------------
// Triple terms in VALUES and expressions
// ---------------------------------------------------------------------------

// parseTripleTermData parses TripleTermData, the constant triple term VALUES
// allows: an IRI subject, an IRI or 'a' predicate, and an IRI, literal or
// nested TripleTermData object.
func (p *sparqlParser) parseTripleTermData(prefixes map[string]string) (RDFTerm, error) {
	if err := p.enter(); err != nil {
		return RDFTerm{}, err
	}
	defer p.leave()
	p.expectPunct("<<(")
	subject, err := p.parseTermPattern(prefixes, false)
	if err != nil {
		return RDFTerm{}, err
	}
	if subject.Term == nil || subject.Term.Kind != RDFTermIRI {
		return RDFTerm{}, fmt.Errorf("a triple term in VALUES must have an IRI subject")
	}
	predicate, err := p.parseTermPattern(prefixes, false)
	if err != nil {
		return RDFTerm{}, err
	}
	if predicate.Term == nil || predicate.Term.Kind != RDFTermIRI {
		return RDFTerm{}, fmt.Errorf("a triple term in VALUES must have an IRI predicate")
	}
	var object RDFTerm
	if p.peekPunct("<<(") {
		if object, err = p.parseTripleTermData(prefixes); err != nil {
			return RDFTerm{}, err
		}
	} else {
		pattern, err := p.parseTermPattern(prefixes, true)
		if err != nil {
			return RDFTerm{}, err
		}
		if pattern.Term == nil || pattern.Term.Kind == RDFTermBlankNode {
			return RDFTerm{}, fmt.Errorf("a triple term in VALUES must have an IRI, literal or triple term object")
		}
		object = *pattern.Term
	}
	p.expectPunct(")>>")
	return NewTripleTerm(*subject.Term, *predicate.Term, object)
}

// parseExprTripleTerm parses ExprTripleTerm, the <<( ... )>> shorthand for
// TRIPLE() in an expression: subject an IRI or variable, object an IRI,
// literal, variable or nested ExprTripleTerm.
func (p *sparqlParser) parseExprTripleTerm(prefixes map[string]string) (sparqlValueExpr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	p.expectPunct("<<(")
	operand := func(allowLiteral bool, position string) (sparqlValueExpr, error) {
		if allowLiteral && p.peekPunct("<<(") {
			return p.parseExprTripleTerm(prefixes)
		}
		if term, ok := p.preBoundNext(); ok {
			return sparqlLiteralExpr{Term: term}, nil
		}
		term, err := p.parseTermPattern(prefixes, allowLiteral)
		if err != nil {
			return nil, err
		}
		switch {
		case term.Variable != "":
			return sparqlVarExpr{Variable: term.Variable}, nil
		case term.Term != nil && term.Term.Kind != RDFTermBlankNode:
			return sparqlLiteralExpr{Term: *term.Term}, nil
		}
		return nil, fmt.Errorf("a triple term expression's %s must be an IRI or variable", position)
	}
	subject, err := operand(false, "subject")
	if err != nil {
		return nil, err
	}
	predicate, err := operand(false, "predicate")
	if err != nil {
		return nil, err
	}
	object, err := operand(true, "object")
	if err != nil {
		return nil, err
	}
	p.expectPunct(")>>")
	fn := sparqlFunctions["TRIPLE"]
	return sparqlFuncExpr{Name: "TRIPLE", Args: []sparqlValueExpr{subject, predicate, object}, fn: fn, rt: p.rt}, nil
}

// enter and leave bound the parser's recursion, so a query nesting triple
// terms or collections thousands deep is an error and not a stack overflow.
func (p *sparqlParser) enter() error {
	p.depth++
	if p.depth > maxRDFNesting {
		return fmt.Errorf("query nested deeper than %d", maxRDFNesting)
	}
	return nil
}

func (p *sparqlParser) leave() { p.depth-- }

// ---------------------------------------------------------------------------
// Ordering and equality of triple terms
// ---------------------------------------------------------------------------

// sparqlTripleTermsEqual is RDFterm-equal for two triple terms (SPARQL 1.2
// §17.4.1.7, sameValue): equal when each pair of parts is, with the literal
// parts compared by value, so <<( :a :b 123 )>> = <<( :a :b 123.0 )>>. An
// error in any pair is an error.
func sparqlTripleTermsEqual(left, right RDFTerm) (bool, error) {
	a, err := decodeTripleTermValue(left.Value)
	if err != nil {
		return false, err
	}
	b, err := decodeTripleTermValue(right.Value)
	if err != nil {
		return false, err
	}
	equal := true
	for _, pair := range [][2]RDFTerm{{a.Subject, b.Subject}, {a.Predicate, b.Predicate}, {a.Object, b.Object}} {
		same, err := sparqlCompareOp("=", pair[0], pair[1])
		if err != nil {
			return false, err
		}
		if !same {
			equal = false
		}
	}
	return equal, nil
}

// sparqlCompareTripleTermsForOrder orders two triple terms by subject, then
// predicate, then object, each by the ORDER BY order — which is how the W3C
// tests expect ORDER BY to place them among themselves.
func sparqlCompareTripleTermsForOrder(left, right RDFTerm) int {
	a, errA := decodeTripleTermValue(left.Value)
	b, errB := decodeTripleTermValue(right.Value)
	if errA != nil || errB != nil {
		return strings.Compare(left.Value, right.Value)
	}
	for _, pair := range [][2]RDFTerm{{a.Subject, b.Subject}, {a.Predicate, b.Predicate}, {a.Object, b.Object}} {
		if cmp := sparqlOrderCompare(pair[0], true, pair[1], true); cmp != 0 {
			return cmp
		}
	}
	return 0
}
