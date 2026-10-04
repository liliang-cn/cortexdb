package graph

// SPARQL 1.1 Update graph management (§3.2): CLEAR, DROP, CREATE, LOAD, ADD,
// COPY and MOVE.
//
// The store keeps no record of an empty graph — a graph exists while it holds
// a triple — which the spec allows (§3.2): CREATE of a graph with no triples
// succeeds and leaves nothing behind, CLEAR and DROP are the same operation,
// and a source graph with no triples is an empty graph, not an error. LOAD
// would fetch a document from the network, which this store never does (as
// with remote JSON-LD contexts), so it fails — and LOAD SILENT does nothing.
// Inferred triples belong to the reasoner and are neither copied nor
// removed: the next inference run reconciles them with the new data.

import (
	"context"
	"fmt"
	"strings"
)

// SPARQLQueryGraphManagement is the query type of a CLEAR, DROP, CREATE, LOAD,
// ADD, COPY or MOVE operation.
const SPARQLQueryGraphManagement = "graph_management"

type sparqlGraphRefKind int

const (
	graphRefIRI sparqlGraphRefKind = iota
	graphRefDefault
	graphRefNamed
	graphRefAll
)

type sparqlGraphRef struct {
	kind sparqlGraphRefKind
	iri  RDFTerm
}

func (r sparqlGraphRef) String() string {
	switch r.kind {
	case graphRefDefault:
		return "DEFAULT"
	case graphRefNamed:
		return "NAMED"
	case graphRefAll:
		return "ALL"
	}
	return "<" + r.iri.Value + ">"
}

// holds reports whether a stored triple is in the referenced graph(s).
func (r sparqlGraphRef) holds(t RDFTriple) bool {
	switch r.kind {
	case graphRefDefault:
		return t.Graph == nil
	case graphRefNamed:
		return t.Graph != nil
	case graphRefAll:
		return true
	}
	return t.Graph != nil && termsEqual(*t.Graph, r.iri)
}

func (r sparqlGraphRef) same(o sparqlGraphRef) bool {
	return r.kind == o.kind && (r.kind != graphRefIRI || termsEqual(r.iri, o.iri))
}

type sparqlGraphOp struct {
	op     string // CLEAR DROP CREATE LOAD ADD COPY MOVE
	silent bool
	source sparqlGraphRef
	target sparqlGraphRef
	// loadInto is set for LOAD ... INTO GRAPH.
	loadInto bool
}

// matchWord matches a keyword the tokenizer may have read as an identifier.
func (p *sparqlParser) matchWord(value string) bool {
	token := p.peek()
	if (token.Type == sparqlTokenKeyword || token.Type == sparqlTokenIdent) && equalFoldASCII(token.Value, value) {
		p.position++
		return true
	}
	return false
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'a' <= x && x <= 'z' {
			x -= 'a' - 'A'
		}
		if 'a' <= y && y <= 'z' {
			y -= 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// parseGraphManagement reads a graph management operation, or returns nil if
// the next token does not start one.
func (p *sparqlParser) parseGraphManagement(prefixes map[string]string) (*sparqlGraphOp, error) {
	var op string
	for _, name := range []string{"CLEAR", "DROP", "CREATE", "LOAD", "ADD", "COPY", "MOVE"} {
		if p.matchWord(name) {
			op = name
			break
		}
	}
	if op == "" {
		return nil, nil
	}
	g := &sparqlGraphOp{op: op, silent: p.matchWord("SILENT")}
	iri := func() (RDFTerm, error) {
		term, err := p.parseGraphResourceTerm(prefixes)
		if err != nil {
			return RDFTerm{}, err
		}
		if term.Kind != RDFTermIRI {
			return RDFTerm{}, fmt.Errorf("%s takes a graph IRI", op)
		}
		return term, nil
	}
	var err error
	switch op {
	case "CLEAR", "DROP":
		switch {
		case p.matchWord("DEFAULT"):
			g.target.kind = graphRefDefault
		case p.matchWord("NAMED"):
			g.target.kind = graphRefNamed
		case p.matchWord("ALL"):
			g.target.kind = graphRefAll
		case p.matchWord("GRAPH"):
			g.target.iri, err = iri()
		default:
			return nil, fmt.Errorf("%s takes GRAPH <iri>, DEFAULT, NAMED or ALL", op)
		}
	case "CREATE":
		if !p.matchWord("GRAPH") {
			return nil, fmt.Errorf("CREATE takes GRAPH <iri>")
		}
		g.target.iri, err = iri()
	case "LOAD":
		if g.source.iri, err = iri(); err != nil {
			return nil, err
		}
		if p.matchWord("INTO") {
			if !p.matchWord("GRAPH") {
				return nil, fmt.Errorf("LOAD ... INTO takes GRAPH <iri>")
			}
			g.loadInto = true
			g.target.iri, err = iri()
		} else {
			g.target.kind = graphRefDefault
		}
	default: // ADD COPY MOVE
		ref := func() (sparqlGraphRef, error) {
			if p.matchWord("DEFAULT") {
				return sparqlGraphRef{kind: graphRefDefault}, nil
			}
			p.matchWord("GRAPH")
			term, err := iri()
			return sparqlGraphRef{iri: term}, err
		}
		if g.source, err = ref(); err != nil {
			return nil, err
		}
		if !p.matchWord("TO") {
			return nil, fmt.Errorf("%s ... TO expected", op)
		}
		g.target, err = ref()
	}
	if err != nil {
		return nil, err
	}
	return g, nil
}

func (g *GraphStore) executeSPARQLGraphOp(ctx context.Context, op *sparqlGraphOp) (int, error) {
	if op == nil { // an empty update request
		return 0, nil
	}
	n, err := g.runSPARQLGraphOp(ctx, op)
	if err != nil && op.silent {
		return 0, nil
	}
	return n, err
}

func (g *GraphStore) runSPARQLGraphOp(ctx context.Context, op *sparqlGraphOp) (int, error) {
	switch op.op {
	case "LOAD":
		return 0, fmt.Errorf("LOAD <%s>: this store does not fetch remote documents; read it and use ImportRDF", op.source.iri.Value)
	case "CREATE":
		existing, err := g.graphTriples(ctx, op.target)
		if err != nil {
			return 0, err
		}
		if len(existing) > 0 {
			return 0, fmt.Errorf("CREATE GRAPH %s: the graph already exists", op.target)
		}
		return 0, nil
	case "CLEAR", "DROP":
		existing, err := g.graphTriples(ctx, op.target)
		if err != nil {
			return 0, err
		}
		return g.deleteTriples(ctx, existing)
	}
	// ADD, COPY, MOVE
	if op.source.same(op.target) {
		return 0, nil
	}
	source, err := g.graphTriples(ctx, op.source)
	if err != nil {
		return 0, err
	}
	changed := 0
	if op.op != "ADD" {
		target, err := g.graphTriples(ctx, op.target)
		if err != nil {
			return 0, err
		}
		if changed, err = g.deleteTriples(ctx, target); err != nil {
			return changed, err
		}
	}
	for _, t := range source {
		moved := RDFTriple{Subject: t.Subject, Predicate: t.Predicate, Object: t.Object}
		if op.target.kind == graphRefIRI {
			name := op.target.iri
			moved.Graph = &name
		}
		if err := g.UpsertTriple(ctx, &moved); err != nil {
			return changed, err
		}
		changed++
	}
	if op.op == "MOVE" {
		removed, err := g.deleteTriples(ctx, source)
		changed += removed
		if err != nil {
			return changed, err
		}
	}
	return changed, nil
}

// graphTriples lists the asserted triples in the referenced graph(s).
func (g *GraphStore) graphTriples(ctx context.Context, ref sparqlGraphRef) ([]RDFTriple, error) {
	pattern := TriplePattern{}
	if ref.kind == graphRefIRI {
		name := ref.iri
		pattern.Graph = &name
	}
	all, err := g.findStoredTriples(ctx, pattern)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, t := range all {
		if !t.Inferred && ref.holds(t) {
			out = append(out, t)
		}
	}
	return out, nil
}

func templateHasVariable(patterns []sparqlPattern) bool {
	var has func(t sparqlTermPattern) bool
	has = func(t sparqlTermPattern) bool {
		if t.Variable != "" {
			return true
		}
		return t.Triple != nil && (has(t.Triple.Subject) || has(t.Triple.Predicate) || has(t.Triple.Object))
	}
	for _, pt := range patterns {
		if has(pt.Subject) || has(pt.Predicate) || has(pt.Object) || (pt.Graph != nil && has(*pt.Graph)) {
			return true
		}
	}
	return false
}

// claimBlankLabels records the blank node labels an INSERT DATA writes and
// refuses one an earlier INSERT DATA of the same request already used: the
// grammar does not let two DATA blocks share a label (§19.6, note 13), which
// would name a node the reader expects to be shared and the store would not
// share. Templates of INSERT ... WHERE may reuse labels; each instantiation
// is a fresh node anyway.
func (p *sparqlParser) claimBlankLabels(op *sparqlQuery) error {
	if op.QueryType != SPARQLQueryInsertData {
		return nil
	}
	mine := map[string]bool{}
	var walk func(t sparqlTermPattern)
	walk = func(t sparqlTermPattern) {
		// Labels the parser invents ([] and the RDF 1.2 sugar) start
		// with '#' and are unique to their place in the request.
		if t.Blank != "" && !strings.HasPrefix(t.Blank, "#") {
			mine[t.Blank] = true
		}
		if t.Triple != nil {
			walk(t.Triple.Subject)
			walk(t.Triple.Predicate)
			walk(t.Triple.Object)
		}
	}
	for _, pt := range op.Template {
		walk(pt.Subject)
		walk(pt.Object)
	}
	if p.requestBlanks == nil {
		p.requestBlanks = map[string]bool{}
	}
	for label := range mine {
		if p.requestBlanks[label] {
			return fmt.Errorf("blank node _:%s is used in two operations of one update request", label)
		}
	}
	for label := range mine {
		p.requestBlanks[label] = true
	}
	return nil
}
