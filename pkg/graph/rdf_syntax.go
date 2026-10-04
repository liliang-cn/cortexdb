package graph

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// One parser for N-Triples, N-Quads, Turtle and TriG, at RDF 1.2.
//
// The four are one language family: N-Triples is the line-oriented subset of
// Turtle, N-Quads adds a graph label to N-Triples, TriG adds graph blocks to
// Turtle. One recursive-descent parser that knows which member it is reading
// keeps their shared productions — IRIs, blank node labels, string escapes,
// LANG_DIR, triple terms — written once, which is how they stay in agreement
// with each other and with the W3C test suites that pin them down.
//
// Turtle and TriG were parsed by a third-party library until RDF 1.2: it has
// no triple terms, reified triples or annotations, and a parser that reads
// RDF 1.2 Turtle as an error, or worse as something else, is a parser that
// cannot import what this store can hold.

type rdfSyntax int

const (
	rdfSyntaxNTriples rdfSyntax = iota
	rdfSyntaxNQuads
	rdfSyntaxTurtle
	rdfSyntaxTriG
	rdfSyntaxRDFXML
)

func (s rdfSyntax) String() string {
	switch s {
	case rdfSyntaxNTriples:
		return "N-Triples"
	case rdfSyntaxNQuads:
		return "N-Quads"
	case rdfSyntaxTurtle:
		return "Turtle"
	case rdfSyntaxRDFXML:
		return "RDF/XML"
	default:
		return "TriG"
	}
}

// maxRDFNesting bounds every recursive production — collections, property
// lists, annotation blocks and reified triples as well as triple terms — so a
// hostile document is an error and not a stack overflow.
const maxRDFNesting = 256

// rdfSyntaxError locates a parse failure in the document.
type rdfSyntaxError struct {
	Syntax rdfSyntax
	Line   int
	Column int
	Msg    string
}

func (e *rdfSyntaxError) Error() string {
	return fmt.Sprintf("%s syntax error at line %d, column %d: %s", e.Syntax, e.Line, e.Column, e.Msg)
}

type rdfSyntaxParser struct {
	src    string
	pos    int
	syntax rdfSyntax
	base   string

	prefixes map[string]string
	// freshPrefix makes the blank nodes this parse invents ([], collections,
	// anonymous reifiers) distinct from every labelled one in the document
	// and from those of any other parse.
	freshPrefix string
	freshSeq    int

	out   []RDFTriple
	graph *RDFTerm
	depth int

	// version is the last VERSION announced; recorded, never enforced,
	// since RDF 1.2 makes the announcement a hint.
	version string

	// lenientTerms is for reading back a triple term this store wrote: blank
	// node labels and IRIs are taken as stored rather than as the grammar of
	// a fresh document would have them, since the store does not restrict
	// either.
	lenientTerms   bool
	lastTripleTerm RDFTriple

	tripleTermDepth int
}

func newRDFSyntaxParser(src string, syntax rdfSyntax, base string) *rdfSyntaxParser {
	var nonce [4]byte
	_, _ = rand.Read(nonce[:])
	return &rdfSyntaxParser{
		src:         src,
		syntax:      syntax,
		base:        base,
		prefixes:    make(map[string]string),
		freshPrefix: "g" + hex.EncodeToString(nonce[:]) + "x",
	}
}

// parseRDFDocument parses a whole document into triples and quads. base
// resolves relative IRIs in Turtle and TriG; N-Triples and N-Quads have none.
func parseRDFDocument(src string, syntax rdfSyntax, base string) (triples []RDFTriple, err error) {
	if !utf8.ValidString(src) {
		return nil, &rdfSyntaxError{Syntax: syntax, Line: 1, Column: 1, Msg: "document is not valid UTF-8"}
	}
	if syntax == rdfSyntaxRDFXML {
		return parseRDFXML(src, base)
	}
	p := newRDFSyntaxParser(src, syntax, base)
	defer func() {
		if r := recover(); r != nil {
			syntaxErr, ok := r.(*rdfSyntaxError)
			if !ok {
				panic(r)
			}
			triples, err = nil, syntaxErr
		}
	}()
	switch syntax {
	case rdfSyntaxNTriples, rdfSyntaxNQuads:
		p.parseLineDocument()
	case rdfSyntaxTurtle:
		p.parseTurtleDocument()
	case rdfSyntaxTriG:
		p.parseTriGDocument()
	}
	return p.out, nil
}

// fail aborts the parse. Parsing is a deep recursion that can only end one
// way once it is wrong, so the error unwinds it by panic and parseRDFDocument
// turns it back into an error.
func (p *rdfSyntaxParser) fail(format string, args ...any) {
	line, col := 1, 1
	for i := 0; i < p.pos && i < len(p.src); i++ {
		if p.src[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	panic(&rdfSyntaxError{Syntax: p.syntax, Line: line, Column: col, Msg: fmt.Sprintf(format, args...)})
}

func (p *rdfSyntaxParser) eof() bool { return p.pos >= len(p.src) }

func (p *rdfSyntaxParser) peekByte() byte {
	if p.pos >= len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

func (p *rdfSyntaxParser) hasPrefix(s string) bool {
	return strings.HasPrefix(p.src[p.pos:], s)
}

func (p *rdfSyntaxParser) expect(s string) {
	if !p.hasPrefix(s) {
		p.fail("expected %q", s)
	}
	p.pos += len(s)
}

func (p *rdfSyntaxParser) enter() {
	p.depth++
	if p.depth > maxRDFNesting {
		p.fail("nesting deeper than %d", maxRDFNesting)
	}
}

func (p *rdfSyntaxParser) leave() { p.depth-- }

func (p *rdfSyntaxParser) emit(subject, predicate, object RDFTerm) {
	triple := RDFTriple{Subject: subject, Predicate: predicate, Object: object}
	if p.graph != nil {
		graph := *p.graph
		triple.Graph = &graph
	}
	p.out = append(p.out, triple)
}

func (p *rdfSyntaxParser) freshBlankNode() RDFTerm {
	p.freshSeq++
	return RDFTerm{Kind: RDFTermBlankNode, Value: p.freshPrefix + strconv.Itoa(p.freshSeq)}
}

func (p *rdfSyntaxParser) makeTripleTerm(subject, predicate, object RDFTerm) RDFTerm {
	value, err := spellTripleTerm(subject, predicate, object, true)
	if err != nil {
		p.fail("%v", err)
	}
	p.lastTripleTerm = RDFTriple{Subject: subject, Predicate: predicate, Object: object}
	return RDFTerm{Kind: RDFTermTriple, Value: value}
}

// ---------------------------------------------------------------------------
// N-Triples and N-Quads
// ---------------------------------------------------------------------------

// skipHorizontalSpace skips the white space N-Triples allows between terms:
// spaces and tabs, never a line end, which ends a statement.
func (p *rdfSyntaxParser) skipHorizontalSpace() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
}

func (p *rdfSyntaxParser) skipLineComment() {
	if p.peekByte() == '#' {
		for p.pos < len(p.src) && p.src[p.pos] != '\n' && p.src[p.pos] != '\r' {
			p.pos++
		}
	}
}

func (p *rdfSyntaxParser) atLineEnd() bool {
	return p.eof() || p.src[p.pos] == '\n' || p.src[p.pos] == '\r'
}

func (p *rdfSyntaxParser) parseLineDocument() {
	for !p.eof() {
		p.skipHorizontalSpace()
		p.skipLineComment()
		if p.eof() {
			return
		}
		if p.atLineEnd() {
			p.pos++
			continue
		}
		if p.hasPrefix("VERSION") {
			p.pos += len("VERSION")
			p.skipHorizontalSpace()
			if p.peekByte() != '"' {
				p.fail("VERSION expects a double-quoted string")
			}
			p.version = p.scanQuotedString('"', false)
		} else {
			p.parseLineStatement()
		}
		p.skipHorizontalSpace()
		p.skipLineComment()
		if !p.atLineEnd() {
			p.fail("unexpected content after statement")
		}
	}
}

func (p *rdfSyntaxParser) parseLineStatement() {
	subject := p.parseNTSubject()
	p.skipHorizontalSpace()
	if p.peekByte() != '<' || p.hasPrefix("<<") {
		p.fail("predicate must be an IRI")
	}
	predicate := RDFTerm{Kind: RDFTermIRI, Value: p.scanIRIRef()}
	p.skipHorizontalSpace()
	object := p.parseNTObject(0)
	p.skipHorizontalSpace()
	var graph *RDFTerm
	if p.syntax == rdfSyntaxNQuads && p.peekByte() != '.' {
		var label RDFTerm
		switch {
		case p.hasPrefix("_:"):
			label = p.scanBlankNodeLabel()
		case p.peekByte() == '<' && !p.hasPrefix("<<"):
			label = RDFTerm{Kind: RDFTermIRI, Value: p.scanIRIRef()}
		default:
			p.fail("graph label must be an IRI or blank node")
		}
		graph = &label
		p.skipHorizontalSpace()
	}
	if p.peekByte() != '.' {
		p.fail("expected '.' at the end of the statement")
	}
	p.pos++
	triple := RDFTriple{Subject: subject, Predicate: predicate, Object: object, Graph: graph}
	p.out = append(p.out, triple)
}

func (p *rdfSyntaxParser) parseNTSubject() RDFTerm {
	switch {
	case p.hasPrefix("_:"):
		return p.scanBlankNodeLabel()
	case p.peekByte() == '<' && !p.hasPrefix("<<"):
		return RDFTerm{Kind: RDFTermIRI, Value: p.scanIRIRef()}
	default:
		p.fail("subject must be an IRI or blank node")
		return RDFTerm{}
	}
}

// parseNTObject reads an N-Triples object: IRI, blank node, literal, or a
// triple term, whose own object recurses here.
func (p *rdfSyntaxParser) parseNTObject(depth int) RDFTerm {
	switch {
	case p.hasPrefix("<<("):
		if depth >= maxTripleTermDepth {
			p.fail("triple term nested deeper than %d", maxTripleTermDepth)
		}
		p.pos += 3
		p.skipHorizontalSpace()
		subject := p.parseNTSubject()
		p.skipHorizontalSpace()
		if p.peekByte() != '<' || p.hasPrefix("<<") {
			p.fail("triple term predicate must be an IRI")
		}
		predicate := RDFTerm{Kind: RDFTermIRI, Value: p.scanIRIRef()}
		p.skipHorizontalSpace()
		object := p.parseNTObject(depth + 1)
		p.skipHorizontalSpace()
		p.expect(")>>")
		return p.makeTripleTerm(subject, predicate, object)
	case p.hasPrefix("<<"):
		p.fail("N-Triples has triple terms <<( ... )>> but not reified triples << ... >>")
	case p.peekByte() == '<':
		return RDFTerm{Kind: RDFTermIRI, Value: p.scanIRIRef()}
	case p.hasPrefix("_:"):
		return p.scanBlankNodeLabel()
	case p.peekByte() == '"':
		value := p.scanQuotedString('"', false)
		return p.finishLiteral(value, false)
	}
	p.fail("object must be an IRI, blank node, literal or triple term")
	return RDFTerm{}
}

// ---------------------------------------------------------------------------
// Turtle and TriG
// ---------------------------------------------------------------------------

// skipWS skips Turtle white space and comments, line ends included.
func (p *rdfSyntaxParser) skipWS() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case ' ', '\t', '\r', '\n':
			p.pos++
		case '#':
			for p.pos < len(p.src) && p.src[p.pos] != '\n' && p.src[p.pos] != '\r' {
				p.pos++
			}
		default:
			return
		}
	}
}

// matchKeyword matches a case-insensitive keyword (PREFIX, BASE, VERSION,
// GRAPH) that is not the start of a longer name.
func (p *rdfSyntaxParser) matchKeyword(word string) bool {
	if len(p.src)-p.pos < len(word) || !strings.EqualFold(p.src[p.pos:p.pos+len(word)], word) {
		return false
	}
	if next := p.pos + len(word); next < len(p.src) {
		r, _ := utf8.DecodeRuneInString(p.src[next:])
		if isPNChars(r) || r == ':' || r == '.' {
			return false
		}
	}
	p.pos += len(word)
	return true
}

func (p *rdfSyntaxParser) parseTurtleDocument() {
	for {
		p.skipWS()
		if p.eof() {
			return
		}
		if p.parseDirective() {
			continue
		}
		p.parseTriples()
		p.skipWS()
		p.expect(".")
	}
}

func (p *rdfSyntaxParser) parseTriGDocument() {
	for {
		p.skipWS()
		if p.eof() {
			return
		}
		if p.parseDirective() {
			continue
		}
		p.parseTriGBlock()
	}
}

// parseDirective reads @prefix, @base, @version and their SPARQL-style
// spellings. The @ forms end in '.', the others do not.
func (p *rdfSyntaxParser) parseDirective() bool {
	switch {
	case p.hasPrefix("@prefix"):
		p.pos += len("@prefix")
		p.parsePrefixBody()
		p.skipWS()
		p.expect(".")
	case p.hasPrefix("@base"):
		p.pos += len("@base")
		p.parseBaseBody()
		p.skipWS()
		p.expect(".")
	case p.hasPrefix("@version"):
		p.pos += len("@version")
		p.parseVersionBody()
		p.skipWS()
		p.expect(".")
	case p.matchKeyword("PREFIX"):
		p.parsePrefixBody()
	case p.matchKeyword("BASE"):
		p.parseBaseBody()
	case p.matchKeyword("VERSION"):
		p.parseVersionBody()
	default:
		return false
	}
	return true
}

func (p *rdfSyntaxParser) parsePrefixBody() {
	p.skipWS()
	start := p.pos
	prefix := ""
	if p.peekByte() != ':' {
		prefix = p.scanPNPrefix()
	}
	if p.peekByte() != ':' {
		p.pos = start
		p.fail("expected a prefix name ending in ':'")
	}
	p.pos++
	p.skipWS()
	if p.peekByte() != '<' {
		p.fail("expected an IRI for prefix %q", prefix)
	}
	p.prefixes[prefix] = p.resolveIRI(p.scanIRIRef())
}

func (p *rdfSyntaxParser) parseBaseBody() {
	p.skipWS()
	if p.peekByte() != '<' {
		p.fail("expected a base IRI")
	}
	p.base = p.resolveIRI(p.scanIRIRef())
}

func (p *rdfSyntaxParser) parseVersionBody() {
	p.skipWS()
	quote := p.peekByte()
	if quote != '"' && quote != '\'' {
		p.fail("VERSION expects a quoted string")
	}
	if p.hasPrefix(`"""`) || p.hasPrefix(`'''`) {
		p.fail("VERSION does not take a long string")
	}
	p.version = p.scanQuotedString(quote, false)
}

func (p *rdfSyntaxParser) parseTriGBlock() {
	switch {
	case p.matchKeyword("GRAPH"):
		p.skipWS()
		label := p.parseGraphLabel()
		p.skipWS()
		p.parseWrappedGraph(label)
	case p.peekByte() == '{':
		p.parseWrappedGraph(nil)
	case p.hasPrefix("<<") && !p.hasPrefix("<<("):
		reifier := p.parseReifiedTriple()
		p.skipWS()
		if p.peekByte() != '.' {
			p.parsePredicateObjectList(reifier)
			p.skipWS()
		}
		p.expect(".")
	case p.peekByte() == '[' && !p.atAnon():
		subject := p.parseBlankNodePropertyList()
		p.skipWS()
		if p.peekByte() != '.' {
			p.parsePredicateObjectList(subject)
			p.skipWS()
		}
		p.expect(".")
	case p.peekByte() == '(':
		subject := p.parseCollection()
		p.skipWS()
		p.parsePredicateObjectList(subject)
		p.skipWS()
		p.expect(".")
	default:
		// labelOrSubject, then either a graph block or the rest of a triple.
		subject := p.parseLabelOrSubject()
		p.skipWS()
		if p.peekByte() == '{' {
			p.parseWrappedGraph(&subject)
			return
		}
		p.parsePredicateObjectList(subject)
		p.skipWS()
		p.expect(".")
	}
}

func (p *rdfSyntaxParser) parseGraphLabel() *RDFTerm {
	label := p.parseLabelOrSubject()
	return &label
}

func (p *rdfSyntaxParser) parseLabelOrSubject() RDFTerm {
	switch {
	case p.hasPrefix("_:"):
		return p.scanBlankNodeLabel()
	case p.peekByte() == '[':
		if !p.atAnon() {
			p.fail("a graph label or subject here must be an IRI or blank node")
		}
		return p.scanAnon()
	default:
		return p.parseIRI()
	}
}

func (p *rdfSyntaxParser) parseWrappedGraph(label *RDFTerm) {
	p.expect("{")
	saved := p.graph
	p.graph = label
	for {
		p.skipWS()
		if p.peekByte() == '}' {
			break
		}
		p.parseTriples()
		p.skipWS()
		if p.peekByte() == '.' {
			p.pos++
			continue
		}
		if p.peekByte() != '}' {
			p.fail("expected '.' or '}' in graph block")
		}
	}
	p.pos++
	p.graph = saved
}

// parseTriples is the triples production: a subject and its predicate-object
// list, a blank node property list with an optional one, or a reified triple
// with an optional one.
func (p *rdfSyntaxParser) parseTriples() {
	switch {
	case p.hasPrefix("<<("):
		p.fail("a triple term cannot be a subject")
	case p.hasPrefix("<<"):
		reifier := p.parseReifiedTriple()
		p.skipWS()
		if p.startsPredicate() {
			p.parsePredicateObjectList(reifier)
		}
	case p.peekByte() == '[' && !p.atAnon():
		subject := p.parseBlankNodePropertyList()
		p.skipWS()
		if p.startsPredicate() {
			p.parsePredicateObjectList(subject)
		}
	default:
		subject := p.parseSubject()
		p.skipWS()
		p.parsePredicateObjectList(subject)
	}
}

// startsPredicate reports whether a verb begins here, so an optional
// predicate-object list can be told apart from the '.' that ends a statement.
func (p *rdfSyntaxParser) startsPredicate() bool {
	if p.eof() {
		return false
	}
	switch c := p.peekByte(); c {
	case '.', '}', ']', '|':
		return false
	}
	return true
}

func (p *rdfSyntaxParser) parseSubject() RDFTerm {
	switch {
	case p.hasPrefix("_:"):
		return p.scanBlankNodeLabel()
	case p.peekByte() == '[':
		if !p.atAnon() {
			p.fail("unexpected '['")
		}
		return p.scanAnon()
	case p.peekByte() == '(':
		return p.parseCollection()
	case p.peekByte() == '"' || p.peekByte() == '\'' || isNumberStart(p.src, p.pos):
		p.fail("a literal cannot be a subject")
	}
	return p.parseIRI()
}

func (p *rdfSyntaxParser) parsePredicateObjectList(subject RDFTerm) {
	p.enter()
	defer p.leave()
	p.skipWS()
	predicate := p.parseVerb()
	p.skipWS()
	p.parseObjectList(subject, predicate)
	for {
		p.skipWS()
		if p.peekByte() != ';' {
			return
		}
		for p.peekByte() == ';' {
			p.pos++
			p.skipWS()
		}
		if !p.startsPredicate() || p.peekByte() == ';' {
			return
		}
		predicate = p.parseVerb()
		p.skipWS()
		p.parseObjectList(subject, predicate)
	}
}

func (p *rdfSyntaxParser) parseVerb() RDFTerm {
	if p.peekByte() == 'a' {
		next := p.pos + 1
		if next >= len(p.src) {
			p.pos = next
			return NewIRI(rdf12NamespaceIRI + "type")
		}
		r, _ := utf8.DecodeRuneInString(p.src[next:])
		if !isPNChars(r) && r != ':' && r != '.' {
			p.pos = next
			return RDFTerm{Kind: RDFTermIRI, Value: rdf12NamespaceIRI + "type"}
		}
	}
	if p.peekByte() != '<' && !isPNCharsBaseAt(p.src, p.pos) && p.peekByte() != ':' {
		p.fail("expected a predicate IRI")
	}
	if p.hasPrefix("<<") {
		p.fail("a predicate must be an IRI")
	}
	return p.parseIRI()
}

func (p *rdfSyntaxParser) parseObjectList(subject, predicate RDFTerm) {
	for {
		p.skipWS()
		object := p.parseObject()
		p.emit(subject, predicate, object)
		p.skipWS()
		p.parseAnnotation(subject, predicate, object)
		p.skipWS()
		if p.peekByte() != ',' {
			return
		}
		p.pos++
	}
}

// parseAnnotation reads the reifiers and annotation blocks after an object
// (RDF 1.2 Turtle §7.3.3–7.3.4). Each "~ r" emits r rdf:reifies the triple
// term of the triple just read; a block uses the reifier named immediately
// before it, or a fresh blank node when there is none.
func (p *rdfSyntaxParser) parseAnnotation(subject, predicate, object RDFTerm) {
	var tripleTerm *RDFTerm
	term := func() RDFTerm {
		if tripleTerm == nil {
			t := p.makeTripleTerm(subject, predicate, object)
			tripleTerm = &t
		}
		return *tripleTerm
	}
	var reifier *RDFTerm
	for {
		p.skipWS()
		switch {
		case p.peekByte() == '~':
			p.pos++
			p.skipWS()
			r := p.parseOptionalReifierID()
			p.emit(r, NewIRI(RDFReifiesIRI), term())
			reifier = &r
		case p.hasPrefix("{|"):
			p.pos += 2
			var r RDFTerm
			if reifier != nil {
				r = *reifier
			} else {
				r = p.freshBlankNode()
				p.emit(r, NewIRI(RDFReifiesIRI), term())
			}
			p.skipWS()
			if p.hasPrefix("|}") {
				p.fail("an annotation block cannot be empty")
			}
			p.parsePredicateObjectList(r)
			p.skipWS()
			p.expect("|}")
			reifier = nil
		default:
			return
		}
	}
}

// parseOptionalReifierID reads the IRI or blank node after '~', or allocates
// a fresh blank node when none is written.
func (p *rdfSyntaxParser) parseOptionalReifierID() RDFTerm {
	switch {
	case p.hasPrefix("_:"):
		return p.scanBlankNodeLabel()
	case p.peekByte() == '[' && p.atAnon():
		return p.scanAnon()
	case p.peekByte() == '<' && !p.hasPrefix("<<"):
		return p.parseIRI()
	case p.peekByte() == ':' || isPNCharsBaseAt(p.src, p.pos):
		return p.parseIRI()
	default:
		return p.freshBlankNode()
	}
}

func (p *rdfSyntaxParser) parseObject() RDFTerm {
	switch c := p.peekByte(); {
	case p.hasPrefix("<<("):
		return p.parseTripleTerm()
	case p.hasPrefix("<<"):
		return p.parseReifiedTriple()
	case c == '<':
		return p.parseIRI()
	case p.hasPrefix("_:"):
		return p.scanBlankNodeLabel()
	case c == '[':
		if p.atAnon() {
			return p.scanAnon()
		}
		return p.parseBlankNodePropertyList()
	case c == '(':
		return p.parseCollection()
	case c == '"' || c == '\'' || isNumberStart(p.src, p.pos):
		return p.parseLiteral()
	default:
		if term, ok := p.tryBoolean(); ok {
			return term
		}
		return p.parseIRI()
	}
}

// parseTripleTerm reads <<( s p o )>>: its subject an IRI or blank node, its
// object anything an object can be except a collection, property list or
// reified triple.
func (p *rdfSyntaxParser) parseTripleTerm() RDFTerm {
	p.enter()
	defer p.leave()
	// The same limit N-Triples reading applies, so every triple term a
	// Turtle document yields can be stored and read back.
	p.tripleTermDepth++
	defer func() { p.tripleTermDepth-- }()
	if p.tripleTermDepth > maxTripleTermDepth {
		p.fail("triple term nested deeper than %d", maxTripleTermDepth)
	}
	p.expect("<<(")
	p.skipWS()
	var subject RDFTerm
	switch {
	case p.hasPrefix("_:"):
		subject = p.scanBlankNodeLabel()
	case p.peekByte() == '[' && p.atAnon():
		subject = p.scanAnon()
	case p.hasPrefix("<<"), p.peekByte() == '[', p.peekByte() == '(', p.peekByte() == '"', p.peekByte() == '\'', isNumberStart(p.src, p.pos):
		p.fail("a triple term's subject must be an IRI or blank node")
	default:
		if _, ok := p.tryBoolean(); ok {
			p.fail("a triple term's subject must be an IRI or blank node")
		}
		subject = p.parseIRI()
	}
	p.skipWS()
	predicate := p.parseVerb()
	p.skipWS()
	var object RDFTerm
	switch c := p.peekByte(); {
	case p.hasPrefix("<<("):
		object = p.parseTripleTerm()
	case p.hasPrefix("<<"):
		p.fail("a triple term's object cannot be a reified triple")
	case c == '<':
		object = p.parseIRI()
	case p.hasPrefix("_:"):
		object = p.scanBlankNodeLabel()
	case c == '[':
		if !p.atAnon() {
			p.fail("a triple term's object cannot be a blank node property list")
		}
		object = p.scanAnon()
	case c == '(':
		p.fail("a triple term's object cannot be a collection")
	case c == '"' || c == '\'' || isNumberStart(p.src, p.pos):
		object = p.parseLiteral()
	default:
		if term, ok := p.tryBoolean(); ok {
			object = term
		} else {
			object = p.parseIRI()
		}
	}
	p.skipWS()
	p.expect(")>>")
	return p.makeTripleTerm(subject, predicate, object)
}

// parseReifiedTriple reads << s p o ~ r >>, emits the reifying triple
// r rdf:reifies <<( s p o )>>, and stands for r. Without a reifier r is a
// fresh blank node. The triple itself is not asserted.
func (p *rdfSyntaxParser) parseReifiedTriple() RDFTerm {
	p.enter()
	defer p.leave()
	p.expect("<<")
	p.skipWS()
	var subject RDFTerm
	switch {
	case p.hasPrefix("<<("):
		p.fail("a reified triple's subject cannot be a triple term")
	case p.hasPrefix("<<"):
		subject = p.parseReifiedTriple()
	case p.hasPrefix("_:"):
		subject = p.scanBlankNodeLabel()
	case p.peekByte() == '[':
		if !p.atAnon() {
			p.fail("a reified triple's subject cannot be a blank node property list")
		}
		subject = p.scanAnon()
	case p.peekByte() == '(' || p.peekByte() == '"' || p.peekByte() == '\'' || isNumberStart(p.src, p.pos):
		p.fail("a reified triple's subject must be an IRI, blank node or reified triple")
	default:
		if _, ok := p.tryBoolean(); ok {
			p.fail("a reified triple's subject must be an IRI, blank node or reified triple")
		}
		subject = p.parseIRI()
	}
	p.skipWS()
	predicate := p.parseVerb()
	p.skipWS()
	var object RDFTerm
	switch c := p.peekByte(); {
	case p.hasPrefix("<<("):
		object = p.parseTripleTerm()
	case p.hasPrefix("<<"):
		object = p.parseReifiedTriple()
	case c == '<':
		object = p.parseIRI()
	case p.hasPrefix("_:"):
		object = p.scanBlankNodeLabel()
	case c == '[':
		if !p.atAnon() {
			p.fail("a reified triple's object cannot be a blank node property list")
		}
		object = p.scanAnon()
	case c == '(':
		p.fail("a reified triple's object cannot be a collection")
	case c == '"' || c == '\'' || isNumberStart(p.src, p.pos):
		object = p.parseLiteral()
	default:
		if term, ok := p.tryBoolean(); ok {
			object = term
		} else {
			object = p.parseIRI()
		}
	}
	p.skipWS()
	var reifier RDFTerm
	if p.peekByte() == '~' {
		p.pos++
		p.skipWS()
		reifier = p.parseOptionalReifierID()
		p.skipWS()
	} else {
		reifier = p.freshBlankNode()
	}
	p.expect(">>")
	p.emit(reifier, NewIRI(RDFReifiesIRI), p.makeTripleTerm(subject, predicate, object))
	return reifier
}

func (p *rdfSyntaxParser) parseBlankNodePropertyList() RDFTerm {
	p.enter()
	defer p.leave()
	p.expect("[")
	node := p.freshBlankNode()
	p.skipWS()
	p.parsePredicateObjectList(node)
	p.skipWS()
	p.expect("]")
	return node
}

func (p *rdfSyntaxParser) parseCollection() RDFTerm {
	p.enter()
	defer p.leave()
	p.expect("(")
	var items []RDFTerm
	for {
		p.skipWS()
		if p.peekByte() == ')' {
			p.pos++
			break
		}
		if p.eof() {
			p.fail("unterminated collection")
		}
		items = append(items, p.parseObject())
	}
	if len(items) == 0 {
		return NewIRI(rdf12NamespaceIRI + "nil")
	}
	first := p.freshBlankNode()
	current := first
	for i, item := range items {
		p.emit(current, NewIRI(rdf12NamespaceIRI+"first"), item)
		if i == len(items)-1 {
			p.emit(current, NewIRI(rdf12NamespaceIRI+"rest"), NewIRI(rdf12NamespaceIRI+"nil"))
			break
		}
		next := p.freshBlankNode()
		p.emit(current, NewIRI(rdf12NamespaceIRI+"rest"), next)
		current = next
	}
	return first
}

// atAnon reports whether '[' WS* ']' starts here.
func (p *rdfSyntaxParser) atAnon() bool {
	if p.peekByte() != '[' {
		return false
	}
	i := p.pos + 1
	for i < len(p.src) {
		switch p.src[i] {
		case ' ', '\t', '\r', '\n':
			i++
			continue
		case '#':
			// A comment is white space here too.
			for i < len(p.src) && p.src[i] != '\n' && p.src[i] != '\r' {
				i++
			}
			continue
		case ']':
			return true
		}
		return false
	}
	return false
}

func (p *rdfSyntaxParser) scanAnon() RDFTerm {
	p.expect("[")
	p.skipWS()
	p.expect("]")
	return p.freshBlankNode()
}

func (p *rdfSyntaxParser) tryBoolean() (RDFTerm, bool) {
	for _, word := range []string{"true", "false"} {
		if !p.hasPrefix(word) {
			continue
		}
		next := p.pos + len(word)
		if next < len(p.src) {
			r, _ := utf8.DecodeRuneInString(p.src[next:])
			if isPNChars(r) || r == ':' || r == '.' && next+1 < len(p.src) && isPNCharsOrColonAt(p.src, next+1) {
				continue
			}
		}
		p.pos = next
		return RDFTerm{Kind: RDFTermLiteral, Value: word, Datatype: XSDNamespace + "boolean"}, true
	}
	return RDFTerm{}, false
}

func isPNCharsOrColonAt(s string, i int) bool {
	r, _ := utf8.DecodeRuneInString(s[i:])
	return isPNChars(r) || r == ':'
}

func (p *rdfSyntaxParser) parseLiteral() RDFTerm {
	c := p.peekByte()
	if c == '"' || c == '\'' {
		long := p.hasPrefix(`"""`) || p.hasPrefix(`'''`)
		value := p.scanQuotedString(c, long)
		return p.finishLiteral(value, true)
	}
	return p.scanNumber()
}

// finishLiteral reads the optional LANG_DIR or ^^datatype after a string.
// literal is a grammar rule and not a terminal, so white space may separate
// the string from its tag or datatype; the canonical-form tests hold
// parsers to that.
func (p *rdfSyntaxParser) finishLiteral(value string, turtle bool) RDFTerm {
	skip := p.skipHorizontalSpace
	if turtle {
		skip = p.skipWS
	}
	afterString := p.pos
	skip()
	switch {
	case p.peekByte() == '@':
		langDir := p.scanLangDir()
		return RDFTerm{Kind: RDFTermLiteral, Value: value, Language: langDir}
	case p.hasPrefix("^^"):
		p.pos += 2
		skip()
		var datatype string
		if turtle {
			datatype = p.parseIRI().Value
		} else {
			if p.peekByte() != '<' {
				p.fail("datatype must be an IRI")
			}
			datatype = p.scanIRIRef()
		}
		if datatype == rdf12LangStringIRI || datatype == rdf12DirLangStringIRI {
			p.fail("a %s literal must be written with a language tag, not a datatype", datatype)
		}
		return RDFTerm{Kind: RDFTermLiteral, Value: value, Datatype: datatype}
	}
	p.pos = afterString
	return RDFTerm{Kind: RDFTermLiteral, Value: value}
}

// scanLangDir reads '@' [a-zA-Z]+ ('-' [a-zA-Z0-9]+)* ('--' [a-zA-Z]+)? and
// checks it: the tag well-formed BCP 47, the direction ltr or rtl. Tags are
// lower-cased, as canonical N-Triples writes them.
func (p *rdfSyntaxParser) scanLangDir() string {
	p.expect("@")
	start := p.pos
	for p.pos < len(p.src) && isASCIILetter(p.src[p.pos]) {
		p.pos++
	}
	if p.pos == start {
		p.fail("empty language tag")
	}
	for p.pos+1 < len(p.src) && p.src[p.pos] == '-' && isASCIILetterOrDigit(p.src[p.pos+1]) {
		p.pos++
		for p.pos < len(p.src) && isASCIILetterOrDigit(p.src[p.pos]) {
			p.pos++
		}
	}
	tag := p.src[start:p.pos]
	direction := ""
	if p.hasPrefix("--") && p.pos+2 < len(p.src) && isASCIILetter(p.src[p.pos+2]) {
		p.pos += 2
		dirStart := p.pos
		for p.pos < len(p.src) && isASCIILetter(p.src[p.pos]) {
			p.pos++
		}
		direction = p.src[dirStart:p.pos]
		if direction != "ltr" && direction != "rtl" {
			p.fail("base direction must be ltr or rtl, got %q", direction)
		}
	}
	if !wellFormedLanguageTag(tag) {
		p.fail("language tag %q is not well-formed", tag)
	}
	tag = strings.ToLower(tag)
	if direction != "" {
		return tag + "--" + direction
	}
	return tag
}

func isASCIILetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func isASCIILetterOrDigit(c byte) bool { return isASCIILetter(c) || (c >= '0' && c <= '9') }

func isNumberStart(s string, i int) bool {
	if i >= len(s) {
		return false
	}
	c := s[i]
	if c >= '0' && c <= '9' {
		return true
	}
	if c == '+' || c == '-' {
		return i+1 < len(s) && (isDigit(s[i+1]) || (s[i+1] == '.' && i+2 < len(s) && isDigit(s[i+2])))
	}
	if c == '.' {
		return i+1 < len(s) && isDigit(s[i+1])
	}
	return false
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// scanNumber reads INTEGER, DECIMAL or DOUBLE by longest match. A '.' that
// no digit or exponent follows ends the statement, not the number.
func (p *rdfSyntaxParser) scanNumber() RDFTerm {
	start := p.pos
	if p.peekByte() == '+' || p.peekByte() == '-' {
		p.pos++
	}
	intStart := p.pos
	for p.pos < len(p.src) && isDigit(p.src[p.pos]) {
		p.pos++
	}
	intDigits := p.pos - intStart
	exponentAt := func(i int) int {
		if i < len(p.src) && (p.src[i] == 'e' || p.src[i] == 'E') {
			j := i + 1
			if j < len(p.src) && (p.src[j] == '+' || p.src[j] == '-') {
				j++
			}
			k := j
			for k < len(p.src) && isDigit(p.src[k]) {
				k++
			}
			if k > j {
				return k
			}
		}
		return -1
	}
	if p.peekByte() == '.' {
		fracStart := p.pos + 1
		k := fracStart
		for k < len(p.src) && isDigit(p.src[k]) {
			k++
		}
		fracDigits := k - fracStart
		if end := exponentAt(k); end > 0 && (intDigits > 0 || fracDigits > 0) {
			p.pos = end
			return RDFTerm{Kind: RDFTermLiteral, Value: p.src[start:p.pos], Datatype: XSDNamespace + "double"}
		}
		if fracDigits > 0 {
			p.pos = k
			return RDFTerm{Kind: RDFTermLiteral, Value: p.src[start:p.pos], Datatype: XSDNamespace + "decimal"}
		}
	}
	if end := exponentAt(p.pos); end > 0 && intDigits > 0 {
		p.pos = end
		return RDFTerm{Kind: RDFTermLiteral, Value: p.src[start:p.pos], Datatype: XSDNamespace + "double"}
	}
	if intDigits == 0 {
		p.fail("malformed number")
	}
	return RDFTerm{Kind: RDFTermLiteral, Value: p.src[start:p.pos], Datatype: XSDNamespace + "integer"}
}

// parseIRI reads an IRIREF, resolved against the base, or a prefixed name.
func (p *rdfSyntaxParser) parseIRI() RDFTerm {
	if p.peekByte() == '<' {
		if p.hasPrefix("<<") {
			p.fail("expected an IRI")
		}
		return RDFTerm{Kind: RDFTermIRI, Value: p.resolveIRI(p.scanIRIRef())}
	}
	return RDFTerm{Kind: RDFTermIRI, Value: p.scanPrefixedName()}
}

func (p *rdfSyntaxParser) resolveIRI(ref string) string {
	if p.syntax == rdfSyntaxNTriples || p.syntax == rdfSyntaxNQuads {
		return ref
	}
	if hasIRIScheme(ref) {
		return removeDotSegmentsIfAbsolute(ref)
	}
	if p.base == "" {
		p.fail("relative IRI <%s> with no base IRI to resolve it against", ref)
	}
	return resolveIRIReference(p.base, ref)
}

// scanIRIRef reads '<' ... '>' with \u and \U escapes decoded. The
// characters IRIREF excludes are refused raw and refused escaped: an escape
// changes how a character is written, not which characters an IRI may hold.
func (p *rdfSyntaxParser) scanIRIRef() string {
	p.expect("<")
	var b strings.Builder
	for {
		if p.eof() {
			p.fail("unterminated IRI")
		}
		r, size := utf8.DecodeRuneInString(p.src[p.pos:])
		switch {
		case r == '>':
			p.pos++
			iri := b.String()
			if !p.lenientTerms && (p.syntax == rdfSyntaxNTriples || p.syntax == rdfSyntaxNQuads) && !hasIRIScheme(iri) {
				p.fail("IRI <%s> is not absolute", iri)
			}
			return iri
		case r == '\\':
			decoded := p.scanUCHAR()
			if !p.lenientTerms && iriExcluded(decoded) {
				p.fail("IRI cannot contain U+%04X, escaped or not", decoded)
			}
			b.WriteRune(decoded)
		case iriExcluded(r):
			p.fail("IRI cannot contain %q", r)
		default:
			b.WriteRune(r)
			p.pos += size
		}
	}
}

func iriExcluded(r rune) bool {
	return r <= 0x20 || strings.ContainsRune("<>\"{}|^`\\", r)
}

// scanUCHAR reads \uXXXX or \UXXXXXXXX at the cursor.
func (p *rdfSyntaxParser) scanUCHAR() rune {
	if !p.hasPrefix(`\u`) && !p.hasPrefix(`\U`) {
		p.fail("invalid escape")
	}
	width := 4
	if p.src[p.pos+1] == 'U' {
		width = 8
	}
	start := p.pos + 2
	if start+width > len(p.src) {
		p.fail("truncated \\u escape")
	}
	digits := p.src[start : start+width]
	value, err := strconv.ParseUint(digits, 16, 32)
	if err != nil || strings.ContainsAny(digits, "+-_xX") {
		p.fail("invalid hex in escape %q", digits)
	}
	r := rune(value)
	if (r >= 0xD800 && r <= 0xDFFF) || r > utf8.MaxRune {
		p.fail("escape \\u%s is not a Unicode scalar value", digits)
	}
	p.pos = start + width
	return r
}

// scanQuotedString reads a string delimited by quote (" or '), long or short,
// decoding ECHAR and UCHAR escapes. A short string may not hold a raw line
// end; a long one may hold anything but its own unescaped delimiter.
func (p *rdfSyntaxParser) scanQuotedString(quote byte, long bool) string {
	delim := string(quote)
	if long {
		delim = strings.Repeat(delim, 3)
	}
	p.expect(delim)
	var b strings.Builder
	for {
		if p.eof() {
			p.fail("unterminated string")
		}
		if p.hasPrefix(delim) {
			p.pos += len(delim)
			return b.String()
		}
		c := p.src[p.pos]
		switch {
		case c == '\\':
			if p.pos+1 >= len(p.src) {
				p.fail("unterminated escape")
			}
			switch e := p.src[p.pos+1]; e {
			case 't':
				b.WriteByte('\t')
			case 'b':
				b.WriteByte('\b')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 'f':
				b.WriteByte('\f')
			case '"', '\'', '\\':
				b.WriteByte(e)
			case 'u', 'U':
				b.WriteRune(p.scanUCHAR())
				continue
			default:
				p.fail("invalid escape \\%c in string", e)
			}
			p.pos += 2
		case !long && (c == '\n' || c == '\r'):
			p.fail("line end in a short string")
		default:
			r, size := utf8.DecodeRuneInString(p.src[p.pos:])
			b.WriteRune(r)
			p.pos += size
		}
	}
}

// scanBlankNodeLabel reads '_:' (PN_CHARS_U | [0-9]) ((PN_CHARS | '.')* PN_CHARS)?.
// The label is kept: re-importing a document finds the same blank nodes, as
// the importer this replaced did.
func (p *rdfSyntaxParser) scanBlankNodeLabel() RDFTerm {
	p.expect("_:")
	start := p.pos
	if p.lenientTerms {
		for p.pos < len(p.src) && !isRDFWhitespace(rune(p.src[p.pos])) {
			p.pos++
		}
		if p.pos == start {
			p.fail("empty blank node label")
		}
		return RDFTerm{Kind: RDFTermBlankNode, Value: p.src[start:p.pos]}
	}
	r, size := utf8.DecodeRuneInString(p.src[p.pos:])
	if !(isPNCharsU(r) || (r >= '0' && r <= '9')) {
		p.fail("invalid blank node label")
	}
	p.pos += size
	p.scanNameTail(false)
	return RDFTerm{Kind: RDFTermBlankNode, Value: p.src[start:p.pos]}
}

// scanNameTail consumes (PN_CHARS | '.')* PN_CHARS — and ':' and PLX when
// local is set — giving back any trailing dots, which belong to the
// statement and not the name.
func (p *rdfSyntaxParser) scanNameTail(local bool) string {
	var b strings.Builder
	lastGood := p.pos
	goodLen := 0
	for p.pos < len(p.src) {
		r, size := utf8.DecodeRuneInString(p.src[p.pos:])
		switch {
		case r == '.':
			b.WriteByte('.')
			p.pos++
			continue
		case isPNChars(r) || (local && r == ':'):
			b.WriteRune(r)
			p.pos += size
		case local && r == '%':
			if p.pos+2 >= len(p.src) || !isHex(p.src[p.pos+1]) || !isHex(p.src[p.pos+2]) {
				p.fail("invalid percent escape in local name")
			}
			b.WriteString(p.src[p.pos : p.pos+3])
			p.pos += 3
		case local && r == '\\':
			if p.pos+1 >= len(p.src) || !strings.ContainsRune("_~.-!$&'()*+,;=/?#@%", rune(p.src[p.pos+1])) {
				p.fail("invalid escape in local name")
			}
			b.WriteByte(p.src[p.pos+1])
			p.pos += 2
		default:
			p.pos = lastGood
			return b.String()[:goodLen]
		}
		lastGood = p.pos
		goodLen = b.Len()
	}
	p.pos = lastGood
	return b.String()[:goodLen]
}

func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// scanPNPrefix reads PN_PREFIX: PN_CHARS_BASE ((PN_CHARS | '.')* PN_CHARS)?.
func (p *rdfSyntaxParser) scanPNPrefix() string {
	start := p.pos
	r, size := utf8.DecodeRuneInString(p.src[p.pos:])
	if !isPNCharsBase(r) {
		p.fail("invalid prefix name")
	}
	p.pos += size
	p.scanNameTail(false)
	return p.src[start:p.pos]
}

// scanPrefixedName reads PNAME_NS or PNAME_LN and expands it.
func (p *rdfSyntaxParser) scanPrefixedName() string {
	prefix := ""
	if p.peekByte() != ':' {
		if p.eof() || !isPNCharsBaseAt(p.src, p.pos) {
			p.fail("expected an IRI, prefixed name, blank node or literal")
		}
		prefix = p.scanPNPrefix()
	}
	if p.peekByte() != ':' {
		p.fail("expected ':' after prefix %q", prefix)
	}
	p.pos++
	namespace, ok := p.prefixes[prefix]
	if !ok {
		p.fail("undeclared prefix %q", prefix)
	}
	local := ""
	if p.pos < len(p.src) {
		r, size := utf8.DecodeRuneInString(p.src[p.pos:])
		switch {
		case isPNCharsU(r) || r == ':' || (r >= '0' && r <= '9'):
			var b strings.Builder
			b.WriteRune(r)
			p.pos += size
			b.WriteString(p.scanNameTail(true))
			local = b.String()
		case r == '%' || r == '\\':
			local = p.scanNameTail(true)
		}
	}
	return namespace + local
}

func isPNCharsBaseAt(s string, i int) bool {
	if i >= len(s) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return isPNCharsBase(r)
}

func isPNCharsBase(r rune) bool {
	switch {
	case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		return true
	case r >= 0xC0 && r <= 0xD6, r >= 0xD8 && r <= 0xF6, r >= 0xF8 && r <= 0x2FF,
		r >= 0x370 && r <= 0x37D, r >= 0x37F && r <= 0x1FFF, r >= 0x200C && r <= 0x200D,
		r >= 0x2070 && r <= 0x218F, r >= 0x2C00 && r <= 0x2FEF, r >= 0x3001 && r <= 0xD7FF,
		r >= 0xF900 && r <= 0xFDCF, r >= 0xFDF0 && r <= 0xFFFD, r >= 0x10000 && r <= 0xEFFFF:
		// U+FFFD included: the document was checked to be UTF-8 before
		// parsing, so a decoded U+FFFD is the character itself.
		return true
	}
	return false
}

func isPNCharsU(r rune) bool { return isPNCharsBase(r) || r == '_' }

func isPNChars(r rune) bool {
	return isPNCharsU(r) || r == '-' || (r >= '0' && r <= '9') || r == 0xB7 ||
		(r >= 0x300 && r <= 0x36F) || (r >= 0x203F && r <= 0x2040)
}

// ---------------------------------------------------------------------------
// IRI resolution (RFC 3986 §5.2)
// ---------------------------------------------------------------------------

var iriSchemePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.\-]*:`)

func hasIRIScheme(iri string) bool { return iriSchemePattern.MatchString(iri) }

type iriParts struct {
	scheme, authority, path, query, fragment string
	hasAuthority, hasQuery, hasFragment      bool
}

func splitIRI(iri string) iriParts {
	var parts iriParts
	rest := iri
	if loc := iriSchemePattern.FindStringIndex(rest); loc != nil {
		parts.scheme = rest[:loc[1]-1]
		rest = rest[loc[1]:]
	}
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		parts.fragment, parts.hasFragment = rest[i+1:], true
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		parts.query, parts.hasQuery = rest[i+1:], true
		rest = rest[:i]
	}
	if strings.HasPrefix(rest, "//") {
		rest = rest[2:]
		end := strings.IndexByte(rest, '/')
		if end < 0 {
			end = len(rest)
		}
		parts.authority, parts.hasAuthority = rest[:end], true
		rest = rest[end:]
	}
	parts.path = rest
	return parts
}

func (parts iriParts) String() string {
	var b strings.Builder
	if parts.scheme != "" {
		b.WriteString(parts.scheme)
		b.WriteByte(':')
	}
	if parts.hasAuthority {
		b.WriteString("//")
		b.WriteString(parts.authority)
	}
	b.WriteString(parts.path)
	if parts.hasQuery {
		b.WriteByte('?')
		b.WriteString(parts.query)
	}
	if parts.hasFragment {
		b.WriteByte('#')
		b.WriteString(parts.fragment)
	}
	return b.String()
}

// removeDotSegmentsIfAbsolute leaves an absolute IRI as written: RFC 3986
// only removes dot segments from a reference that is being resolved, and the
// Turtle tests hold an absolute IRI with "/./" in it to that.
func removeDotSegmentsIfAbsolute(iri string) string { return iri }

// resolveIRIReference is RFC 3986 §5.2.2 without normalization, which is what
// Turtle §6.3 specifies.
func resolveIRIReference(base, ref string) string {
	r := splitIRI(ref)
	b := splitIRI(base)
	var t iriParts
	switch {
	case r.scheme != "":
		t = r
		t.path = removeDotSegments(r.path)
	case r.hasAuthority:
		t = r
		t.scheme = b.scheme
		t.path = removeDotSegments(r.path)
	default:
		t.scheme = b.scheme
		t.authority, t.hasAuthority = b.authority, b.hasAuthority
		switch {
		case r.path == "":
			t.path = b.path
			if r.hasQuery {
				t.query, t.hasQuery = r.query, true
			} else {
				t.query, t.hasQuery = b.query, b.hasQuery
			}
		case strings.HasPrefix(r.path, "/"):
			t.path = removeDotSegments(r.path)
			t.query, t.hasQuery = r.query, r.hasQuery
		default:
			t.path = removeDotSegments(mergeIRIPaths(b, r.path))
			t.query, t.hasQuery = r.query, r.hasQuery
		}
	}
	t.fragment, t.hasFragment = r.fragment, r.hasFragment
	return t.String()
}

func mergeIRIPaths(base iriParts, refPath string) string {
	if base.hasAuthority && base.path == "" {
		return "/" + refPath
	}
	if i := strings.LastIndexByte(base.path, '/'); i >= 0 {
		return base.path[:i+1] + refPath
	}
	return refPath
}

// removeDotSegments is RFC 3986 §5.2.4.
func removeDotSegments(path string) string {
	in := path
	var out []string
	for in != "" {
		switch {
		case strings.HasPrefix(in, "../"):
			in = in[3:]
		case strings.HasPrefix(in, "./"):
			in = in[2:]
		case strings.HasPrefix(in, "/./"):
			in = in[2:]
		case in == "/.":
			in = "/"
		case strings.HasPrefix(in, "/../"):
			in = in[3:]
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		case in == "/..":
			in = "/"
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		case in == "." || in == "..":
			in = ""
		default:
			start := 0
			if in[0] == '/' {
				start = 1
			}
			end := strings.IndexByte(in[start:], '/')
			if end < 0 {
				end = len(in)
			} else {
				end += start
			}
			out = append(out, in[:end])
			in = in[end:]
		}
	}
	return strings.Join(out, "")
}
