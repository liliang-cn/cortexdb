package graph

// SPARQL 1.1 property paths, the whole algebra.
//
// The first implementation took a path to be one IRI with one operator on it
// — p*, p+, p?, ^p, p|q — so a path with any structure in it was a syntax
// error: (p/q)*, ^(p/q), !(p|^q), p/q* inside an alternative, (p*)*. The W3C
// SPARQL 1.1 suite failed twenty property-path tests on exactly that. A path
// is now an expression tree parsed by the grammar's own productions
// (PathAlternative, PathSequence, PathEltOrInverse, PathElt with PathMod,
// PathPrimary, PathNegatedPropertySet) and evaluated by the semantics of §18.4:
//
//   - an IRI is its triples, an inverse swaps the ends;
//   - a sequence is a join on the middle node and an alternative a union, and
//     both are bags: two ways along a path are two solutions;
//   - p*, p+ and p? are sets (ALP in the spec): reachability, not a count of
//     walks, so a cycle cannot multiply answers; p* and p? also connect every
//     node to itself — every subject and object of the active graph, and a
//     constant end even when the graph does not mention it;
//   - !(…) matches one triple whose predicate is outside the set, forward
//     for plain members and backward for ^ members.
//
// A path is evaluated inside one graph at a time: in GRAPH ?g each named
// graph separately, so a path cannot step from one graph into another; outside
// GRAPH, the default graph as one graph, even when FROM merges several.

import (
	"context"
	"fmt"
	"sort"
)

type sparqlPathOp int

const (
	pathLink sparqlPathOp = iota // one IRI
	pathInverse
	pathSequence
	pathAlternative
	pathZeroOrMore
	pathOneOrMore
	pathZeroOrOne
	pathNegated // a negated property set
)

// sparqlPathExpr is one node of a property path.
type sparqlPathExpr struct {
	op   sparqlPathOp
	iri  RDFTerm
	kids []*sparqlPathExpr
	// For pathNegated: the excluded forward and inverse IRIs.
	notFwd, notInv []RDFTerm
}

// simpleIRI reports whether the path is a single IRI, which a triple pattern
// states as a plain predicate.
func (e *sparqlPathExpr) simpleIRI() bool { return e != nil && e.op == pathLink }

// --- parsing -----------------------------------------------------------------

// parsePathAlternative reads Path ::= PathAlternative.
func (p *sparqlParser) parsePathAlternative(prefixes map[string]string) (*sparqlPathExpr, error) {
	first, err := p.parsePathSequence(prefixes)
	if err != nil {
		return nil, err
	}
	if !p.isOperator("|") {
		return first, nil
	}
	alt := &sparqlPathExpr{op: pathAlternative, kids: []*sparqlPathExpr{first}}
	for p.matchOperator("|") {
		next, err := p.parsePathSequence(prefixes)
		if err != nil {
			return nil, err
		}
		alt.kids = append(alt.kids, next)
	}
	return alt, nil
}

func (p *sparqlParser) parsePathSequence(prefixes map[string]string) (*sparqlPathExpr, error) {
	first, err := p.parsePathEltOrInverse(prefixes)
	if err != nil {
		return nil, err
	}
	if !p.isOperator("/") {
		return first, nil
	}
	seq := &sparqlPathExpr{op: pathSequence, kids: []*sparqlPathExpr{first}}
	for p.matchOperator("/") {
		next, err := p.parsePathEltOrInverse(prefixes)
		if err != nil {
			return nil, err
		}
		seq.kids = append(seq.kids, next)
	}
	return seq, nil
}

func (p *sparqlParser) parsePathEltOrInverse(prefixes map[string]string) (*sparqlPathExpr, error) {
	if p.matchOperator("^") {
		elt, err := p.parsePathElt(prefixes)
		if err != nil {
			return nil, err
		}
		return &sparqlPathExpr{op: pathInverse, kids: []*sparqlPathExpr{elt}}, nil
	}
	return p.parsePathElt(prefixes)
}

// parsePathElt reads PathPrimary PathMod?. A '+' before a number is a signed
// object literal, not a modifier.
func (p *sparqlParser) parsePathElt(prefixes map[string]string) (*sparqlPathExpr, error) {
	primary, err := p.parsePathPrimary(prefixes)
	if err != nil {
		return nil, err
	}
	switch {
	case p.matchOperator("?"):
		return &sparqlPathExpr{op: pathZeroOrOne, kids: []*sparqlPathExpr{primary}}, nil
	case p.matchOperator("*"):
		return &sparqlPathExpr{op: pathZeroOrMore, kids: []*sparqlPathExpr{primary}}, nil
	case p.isOperator("+") && p.peekN(1).Type != sparqlTokenNumber:
		p.next()
		return &sparqlPathExpr{op: pathOneOrMore, kids: []*sparqlPathExpr{primary}}, nil
	}
	return primary, nil
}

func (p *sparqlParser) parsePathPrimary(prefixes map[string]string) (*sparqlPathExpr, error) {
	switch {
	case p.matchPunct("("):
		inner, err := p.parsePathAlternative(prefixes)
		if err != nil {
			return nil, err
		}
		if !p.matchPunct(")") {
			return nil, fmt.Errorf("expected ) to close a property path, got %q", p.peek().Value)
		}
		return inner, nil
	case p.matchOperator("!"):
		return p.parseNegatedPropertySet(prefixes)
	}
	iri, err := p.parsePathIRI(prefixes)
	if err != nil {
		return nil, err
	}
	return &sparqlPathExpr{op: pathLink, iri: iri}, nil
}

// parseNegatedPropertySet reads what follows '!': one member, or a
// parenthesised, possibly empty, list of them.
func (p *sparqlParser) parseNegatedPropertySet(prefixes map[string]string) (*sparqlPathExpr, error) {
	nps := &sparqlPathExpr{op: pathNegated}
	member := func() error {
		inverse := p.matchOperator("^")
		iri, err := p.parsePathIRI(prefixes)
		if err != nil {
			return err
		}
		if inverse {
			nps.notInv = append(nps.notInv, iri)
		} else {
			nps.notFwd = append(nps.notFwd, iri)
		}
		return nil
	}
	if !p.matchPunct("(") {
		return nps, member()
	}
	if p.matchPunct(")") {
		return nps, nil
	}
	for {
		if err := member(); err != nil {
			return nil, err
		}
		if p.matchPunct(")") {
			return nps, nil
		}
		if !p.matchOperator("|") {
			return nil, fmt.Errorf("expected | or ) in a negated property set, got %q", p.peek().Value)
		}
	}
}

func (p *sparqlParser) parsePathIRI(prefixes map[string]string) (RDFTerm, error) {
	if p.peek().Type == sparqlTokenVar {
		return RDFTerm{}, fmt.Errorf("a variable cannot be part of a property path")
	}
	term, err := p.parseTermPattern(prefixes, false)
	if err != nil {
		return RDFTerm{}, err
	}
	if term.Term == nil || term.Term.Kind != RDFTermIRI {
		return RDFTerm{}, fmt.Errorf("property paths require IRI predicates")
	}
	return *term.Term, nil
}

func (p *sparqlParser) isOperator(value string) bool {
	t := p.peek()
	return t.Type == sparqlTokenOperator && t.Value == value
}

// --- evaluation --------------------------------------------------------------

// pathGraph is one graph's triples, indexed for walking.
type pathGraph struct {
	graph *RDFTerm
	out   map[string]map[string][]RDFTerm // predicate -> subject -> objects
	in    map[string]map[string][]RDFTerm // predicate -> object -> subjects
	// all is every triple in the graph, for negated property sets.
	all   []RDFTriple
	nodes map[string]RDFTerm
}

func newPathGraph(graph *RDFTerm) *pathGraph {
	return &pathGraph{
		graph: cloneGraphTerm(graph),
		out:   map[string]map[string][]RDFTerm{},
		in:    map[string]map[string][]RDFTerm{},
		nodes: map[string]RDFTerm{},
	}
}

func (pg *pathGraph) add(t RDFTriple) {
	p := t.Predicate.Value
	s, o := inferenceTermKey(t.Subject), inferenceTermKey(t.Object)
	if pg.out[p] == nil {
		pg.out[p] = map[string][]RDFTerm{}
		pg.in[p] = map[string][]RDFTerm{}
	}
	pg.out[p][s] = append(pg.out[p][s], t.Object)
	pg.in[p][o] = append(pg.in[p][o], t.Subject)
	pg.all = append(pg.all, t)
	pg.nodes[s] = t.Subject
	pg.nodes[o] = t.Object
}

// next is the bag of nodes one application of e leads to from x, walking
// forward, or backward when back is set.
func (pg *pathGraph) next(e *sparqlPathExpr, x RDFTerm, back bool) []RDFTerm {
	switch e.op {
	case pathLink:
		if back {
			return pg.in[e.iri.Value][inferenceTermKey(x)]
		}
		return pg.out[e.iri.Value][inferenceTermKey(x)]
	case pathInverse:
		return pg.next(e.kids[0], x, !back)
	case pathSequence:
		frontier := []RDFTerm{x}
		kids := e.kids
		for i := range kids {
			k := kids[i]
			if back {
				k = kids[len(kids)-1-i]
			}
			var reached []RDFTerm
			for _, y := range frontier {
				reached = append(reached, pg.next(k, y, back)...)
			}
			frontier = reached
		}
		return frontier
	case pathAlternative:
		var out []RDFTerm
		for _, k := range e.kids {
			out = append(out, pg.next(k, x, back)...)
		}
		return out
	case pathZeroOrMore, pathOneOrMore, pathZeroOrOne:
		return pg.reach(e, x, back)
	case pathNegated:
		return pg.negated(e, x, back)
	}
	return nil
}

// reach is the set ALP computes for p*, p+ and p?.
func (pg *pathGraph) reach(e *sparqlPathExpr, x RDFTerm, back bool) []RDFTerm {
	inner := e.kids[0]
	seen := map[string]bool{}
	var out []RDFTerm
	add := func(t RDFTerm) bool {
		k := inferenceTermKey(t)
		if seen[k] {
			return false
		}
		seen[k] = true
		out = append(out, t)
		return true
	}
	if e.op != pathOneOrMore {
		add(x)
	}
	if e.op == pathZeroOrOne {
		for _, y := range pg.next(inner, x, back) {
			add(y)
		}
		return out
	}
	// Breadth first from x. Visited nodes are tracked apart from the answer
	// so that for p+ the start is an answer only when a cycle returns to it.
	visited := map[string]bool{inferenceTermKey(x): true}
	frontier := []RDFTerm{x}
	for len(frontier) > 0 {
		var reached []RDFTerm
		for _, y := range frontier {
			for _, z := range pg.next(inner, y, back) {
				add(z)
				if k := inferenceTermKey(z); !visited[k] {
					visited[k] = true
					reached = append(reached, z)
				}
			}
		}
		frontier = reached
	}
	return out
}

func (pg *pathGraph) negated(e *sparqlPathExpr, x RDFTerm, back bool) []RDFTerm {
	excluded := func(p RDFTerm, set []RDFTerm) bool { return containsTerm(set, p) }
	key := inferenceTermKey(x)
	var out []RDFTerm
	// A plain member set walks forward; an inverse member set walks the
	// triple backward. !(a|^b) is the union of the two. Walking the whole
	// expression backward swaps which triples each half reads.
	hasFwd := len(e.notFwd) > 0 || len(e.notInv) == 0
	hasInv := len(e.notInv) > 0
	for _, t := range pg.all {
		if hasFwd && !excluded(t.Predicate, e.notFwd) {
			if !back && inferenceTermKey(t.Subject) == key {
				out = append(out, t.Object)
			}
			if back && inferenceTermKey(t.Object) == key {
				out = append(out, t.Subject)
			}
		}
		if hasInv && !excluded(t.Predicate, e.notInv) {
			if !back && inferenceTermKey(t.Object) == key {
				out = append(out, t.Subject)
			}
			if back && inferenceTermKey(t.Subject) == key {
				out = append(out, t.Object)
			}
		}
	}
	return out
}

// pathIRIs collects the predicates a path reads, and whether it needs every
// triple (a negated property set reads any predicate).
func pathIRIs(e *sparqlPathExpr, into map[string]RDFTerm) (needsAll bool) {
	switch e.op {
	case pathLink:
		into[e.iri.Value] = e.iri
	case pathNegated:
		return true
	}
	for _, k := range e.kids {
		if pathIRIs(k, into) {
			needsAll = true
		}
	}
	return needsAll
}

// zeroLength reports whether the path can match without moving.
func zeroLength(e *sparqlPathExpr) bool {
	switch e.op {
	case pathZeroOrMore, pathZeroOrOne:
		return true
	case pathInverse:
		return zeroLength(e.kids[0])
	case pathSequence:
		for _, k := range e.kids {
			if !zeroLength(k) {
				return false
			}
		}
		return true
	case pathAlternative:
		for _, k := range e.kids {
			if zeroLength(k) {
				return true
			}
		}
	}
	return false
}

// findSPARQLPathMatches evaluates a property path pattern under one binding.
func (g *GraphStore) findSPARQLPathMatches(ctx context.Context, pattern sparqlPattern, binding map[string]RDFTerm, opts sparqlExecOptions) ([]sparqlPathMatch, error) {
	if pattern.Path == nil || pattern.Path.Expr == nil {
		return nil, nil
	}
	expr := pattern.Path.Expr
	subject, err := resolvePatternTerm(pattern.Subject, binding)
	if err != nil {
		return nil, err
	}
	object, err := resolvePatternTerm(pattern.Object, binding)
	if err != nil {
		return nil, err
	}
	graphTerm, err := resolveOptionalPatternTerm(pattern.Graph, binding)
	if err != nil {
		return nil, err
	}

	graphs, err := g.loadPathGraphs(ctx, pattern, expr, graphTerm, binding, opts)
	if err != nil {
		return nil, err
	}

	var out []sparqlPathMatch
	emit := func(pg *pathGraph, s, o RDFTerm) {
		out = append(out, sparqlPathMatch{Subject: s, Object: o, Graph: cloneGraphTerm(pg.graph)})
	}
	zero := zeroLength(expr)
	for _, pg := range graphs {
		// The pattern is evaluated on its own and then joined, so an end
		// that is a variable can only be a node of the graph, even when an
		// earlier VALUES or pattern bound it to something else: a
		// zero-length path must not connect a term the graph never
		// mentions to itself. A constant written in the pattern is the
		// exception the spec makes, and connects to itself anywhere.
		if zero && !pg.endInGraph(pattern.Subject, subject) || zero && !pg.endInGraph(pattern.Object, object) {
			continue
		}
		switch {
		case subject != nil:
			for _, o := range pg.next(expr, *subject, false) {
				if object == nil || termsEqual(o, *object) {
					emit(pg, *subject, o)
				}
			}
		case object != nil:
			for _, s := range pg.next(expr, *object, true) {
				emit(pg, s, *object)
			}
		default:
			// Both ends free: every node of the graph is a start. The
			// subject and object being the same variable is handled by the
			// caller's unification.
			for _, start := range sortedPathNodes(pg) {
				for _, o := range pg.next(expr, start, false) {
					emit(pg, start, o)
				}
			}
		}
	}
	return out, nil
}

// pathEndIsConstant reports whether an end of the pattern was written as a
// constant term.
func pathEndIsConstant(end sparqlTermPattern) bool { return end.Term != nil }

// endInGraph reports whether a bound variable end is a node of the graph; a
// constant end, or an end still free, always passes.
func (pg *pathGraph) endInGraph(end sparqlTermPattern, value *RDFTerm) bool {
	if pathEndIsConstant(end) || value == nil {
		return true
	}
	_, ok := pg.nodes[inferenceTermKey(*value)]
	return ok
}

func sortedPathNodes(pg *pathGraph) []RDFTerm {
	keys := make([]string, 0, len(pg.nodes))
	for k := range pg.nodes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]RDFTerm, len(keys))
	for i, k := range keys {
		out[i] = pg.nodes[k]
	}
	return out
}

// loadPathGraphs reads the triples a path needs, one pathGraph per graph it
// is evaluated in.
func (g *GraphStore) loadPathGraphs(ctx context.Context, pattern sparqlPattern, expr *sparqlPathExpr, graphTerm *RDFTerm, binding map[string]RDFTerm, opts sparqlExecOptions) ([]*pathGraph, error) {
	iris := map[string]RDFTerm{}
	needsAll := pathIRIs(expr, iris)
	// A zero-length path whose end is a variable ranges over the nodes of
	// the graph — see endInGraph — so it needs every triple to know them.
	if zeroLength(expr) && (!pathEndIsConstant(pattern.Subject) || !pathEndIsConstant(pattern.Object)) {
		needsAll = true
	}

	scope := sparqlPattern{
		Subject: sparqlTermPattern{Variable: "__path_s"},
		Object:  sparqlTermPattern{Variable: "__path_o"},
		Graph:   pattern.Graph,
	}
	var triples []RDFTriple
	fetch := func(predicate *RDFTerm) error {
		q := scope
		if predicate != nil {
			q.Predicate = sparqlTermPattern{Term: predicate}
		} else {
			q.Predicate = sparqlTermPattern{Variable: "__path_p"}
		}
		ts, err := g.findSPARQLPatternTriples(ctx, q, binding, opts)
		if err != nil {
			return err
		}
		for _, t := range ts {
			if sparqlTripleAllowedForGraph(pattern, t, opts) {
				triples = append(triples, t)
			}
		}
		return nil
	}
	if needsAll {
		if err := fetch(nil); err != nil {
			return nil, err
		}
	} else {
		keys := make([]string, 0, len(iris))
		for k := range iris {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			iri := iris[k]
			if err := fetch(&iri); err != nil {
				return nil, err
			}
		}
	}

	byGraph := map[string]*pathGraph{}
	var order []string
	graphFor := func(key string, term *RDFTerm) *pathGraph {
		pg := byGraph[key]
		if pg == nil {
			pg = newPathGraph(term)
			byGraph[key] = pg
			order = append(order, key)
		}
		return pg
	}
	for _, t := range triples {
		// Outside GRAPH the default graph is one graph, however many
		// graphs FROM merged into it.
		key, term := "", (*RDFTerm)(nil)
		if pattern.Graph != nil {
			key, term = sparqlGraphKey(t.Graph), t.Graph
		}
		graphFor(key, term).add(t)
	}
	// A graph with nothing on the path still holds the zero-length path
	// between a constant end and itself. Outside GRAPH that graph is the
	// default graph; inside GRAPH with a bound name, that graph — if the
	// dataset has it.
	if len(order) == 0 {
		switch {
		case pattern.Graph == nil:
			graphFor("", nil)
		case graphTerm != nil && sparqlGraphInDataset(ctx, g, *graphTerm, opts):
			graphFor(sparqlGraphKey(graphTerm), graphTerm)
		}
	}
	out := make([]*pathGraph, 0, len(order))
	for _, k := range order {
		pg := byGraph[k]
		if graphTerm != nil && pattern.Graph != nil && k != sparqlGraphKey(graphTerm) {
			continue
		}
		out = append(out, pg)
	}
	return out, nil
}

// sparqlGraphInDataset reports whether a named graph is part of the query's
// dataset: listed by FROM NAMED, or, with no dataset declared, present in the
// store.
func sparqlGraphInDataset(ctx context.Context, g *GraphStore, graph RDFTerm, opts sparqlExecOptions) bool {
	if opts.DatasetDeclared || len(opts.NamedGraphs) > 0 {
		return containsTerm(opts.NamedGraphs, graph)
	}
	gc := graph
	ts, err := g.FindTriples(ctx, TriplePattern{Graph: &gc})
	return err == nil && len(ts) > 0
}
