package cypher

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Parse reads a query in the supported subset.
//
// Everything outside the subset is refused here, by name, rather than parsed
// and half-run: a construct the engine skipped would give an answer to a
// different question, which is worse than no answer.
func Parse(src string) (*Query, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks, src: src}
	q, err := p.parseQuery()
	if err != nil {
		return nil, err
	}
	return q, nil
}

type parser struct {
	toks []token
	i    int
	src  string
}

func (p *parser) peek() token { return p.toks[p.i] }
func (p *parser) peekN(n int) token {
	if p.i+n < len(p.toks) {
		return p.toks[p.i+n]
	}
	return p.toks[len(p.toks)-1]
}
func (p *parser) next() token {
	t := p.toks[p.i]
	if t.kind != tEOF {
		p.i++
	}
	return t
}

// isKw reports whether the current token is the keyword kw (case-insensitive).
// A backticked name is never a keyword.
func (p *parser) isKw(kw string) bool { return isKwTok(p.peek(), kw) }

func isKwTok(t token, kw string) bool {
	return t.kind == tIdent && strings.EqualFold(t.text, kw)
}

func (p *parser) acceptKw(kw string) bool {
	if p.isKw(kw) {
		p.i++
		return true
	}
	return false
}

func (p *parser) isPunct(s string) bool {
	t := p.peek()
	return t.kind == tPunct && t.text == s
}

func (p *parser) acceptPunct(s string) bool {
	if p.isPunct(s) {
		p.i++
		return true
	}
	return false
}

func (p *parser) errf(t token, format string, args ...any) error {
	return &Error{Kind: ErrSyntax, Pos: t.pos, Msg: fmt.Sprintf(format, args...)}
}

func (p *parser) expectPunct(s string) error {
	if !p.acceptPunct(s) {
		return p.errf(p.peek(), "expected %q, found %s", s, p.peek())
	}
	return nil
}

func (p *parser) expectKw(kw string) error {
	if !p.acceptKw(kw) {
		return p.errf(p.peek(), "expected %s, found %s", kw, p.peek())
	}
	return nil
}

// writeClauses are refused as read-only rather than as unknown: the subset is
// read-only by design, and saying so stops a caller from hunting for the
// right spelling of something that will never run.
var writeClauses = map[string]bool{
	"CREATE": true, "MERGE": true, "SET": true, "DELETE": true, "DETACH": true,
	"REMOVE": true, "FOREACH": true, "LOAD": true, "INSERT": true, "DROP": true,
}

var unsupportedClauses = map[string]string{
	"CALL":      "CALL (procedures and subqueries)",
	"USE":       "USE (graph selection)",
	"FINISH":    "FINISH",
	"FILTER":    "GQL FILTER (use WHERE)",
	"LET":       "GQL LET (use WITH)",
	"FOR":       "GQL FOR (use UNWIND)",
	"SHOW":      "SHOW",
	"EXPLAIN":   "EXPLAIN",
	"PROFILE":   "PROFILE",
	"START":     "START (legacy Cypher)",
	"MANDATORY": "MANDATORY MATCH",
}

func (p *parser) parseQuery() (*Query, error) {
	q := &Query{}
	for {
		sq, err := p.parseSingle()
		if err != nil {
			return nil, err
		}
		q.Parts = append(q.Parts, sq)
		if p.acceptKw("UNION") {
			q.UnionAll = append(q.UnionAll, p.acceptKw("ALL"))
			continue
		}
		break
	}
	p.acceptPunct(";")
	if t := p.peek(); t.kind != tEOF {
		if t.kind == tIdent {
			up := strings.ToUpper(t.text)
			if writeClauses[up] {
				return nil, &Error{Kind: ErrReadOnly, Pos: t.pos, Msg: up + " is a write clause; this query language is read-only by design — change the graph through its write tools"}
			}
		}
		return nil, p.errf(t, "unexpected %s after the end of the query", t)
	}
	return q, nil
}

func (p *parser) parseSingle() (*SingleQuery, error) {
	sq := &SingleQuery{}
	for {
		t := p.peek()
		if t.kind == tEOF || p.isKw("UNION") || p.isPunct(";") {
			break
		}
		if t.kind != tIdent {
			return nil, p.errf(t, "expected a clause (MATCH, WITH, RETURN, ...), found %s", t)
		}
		up := strings.ToUpper(t.text)
		switch {
		case up == "MATCH" || up == "OPTIONAL":
			c, err := p.parseMatch()
			if err != nil {
				return nil, err
			}
			sq.Clauses = append(sq.Clauses, c)
		case up == "UNWIND":
			p.next()
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if err := p.expectKw("AS"); err != nil {
				return nil, err
			}
			v, err := p.parseName()
			if err != nil {
				return nil, err
			}
			sq.Clauses = append(sq.Clauses, &UnwindClause{Expr: e, Var: v})
		case up == "WITH" || up == "RETURN":
			c, err := p.parseProjection(up == "RETURN")
			if err != nil {
				return nil, err
			}
			sq.Clauses = append(sq.Clauses, c)
		case writeClauses[up]:
			return nil, &Error{Kind: ErrReadOnly, Pos: t.pos, Msg: up + " is a write clause; this query language is read-only by design — change the graph through its write tools"}
		case unsupportedClauses[up] != "":
			return nil, unsupported(t.pos, "%s is not supported", unsupportedClauses[up])
		default:
			return nil, p.errf(t, "unknown clause %s", t.text)
		}
	}
	if len(sq.Clauses) == 0 {
		return nil, p.errf(p.peek(), "empty query")
	}
	return sq, nil
}

func (p *parser) parseMatch() (*MatchClause, error) {
	start := p.peek()
	m := &MatchClause{Pos: start.pos}
	if p.acceptKw("OPTIONAL") {
		m.Optional = true
	}
	if err := p.expectKw("MATCH"); err != nil {
		return nil, err
	}
	// GQL match modes and path search prefixes are not part of the subset.
	for _, kw := range []string{"REPEATABLE", "DIFFERENT", "ANY", "ALL", "SHORTEST", "WALK", "TRAIL", "ACYCLIC", "SIMPLE"} {
		if p.isKw(kw) && !(p.peekN(1).kind == tPunct && p.peekN(1).text == "=") {
			// `MATCH any = (...)` would be a path variable named any; anything
			// else is the GQL keyword.
			if p.peekN(1).kind == tPunct && p.peekN(1).text == "(" && (kw == "ANY" || kw == "ALL") {
				break
			}
			return nil, unsupported(p.peek().pos, "GQL match mode / path search prefix %s is not supported", strings.ToUpper(kw))
		}
	}
	for {
		pp, err := p.parsePatternPart()
		if err != nil {
			return nil, err
		}
		m.Patterns = append(m.Patterns, pp)
		if !p.acceptPunct(",") {
			break
		}
	}
	if p.acceptKw("WHERE") {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		m.Where = e
	}
	return m, nil
}

func (p *parser) parsePatternPart() (*PatternPart, error) {
	pp := &PatternPart{}
	t := p.peek()
	if (t.kind == tIdent || t.kind == tQuotedIdent) && p.peekN(1).kind == tPunct && p.peekN(1).text == "=" {
		pp.PathVar = t.text
		p.i += 2
	}
	t = p.peek()
	if t.kind == tIdent && p.peekN(1).kind == tPunct && p.peekN(1).text == "(" {
		low := strings.ToLower(t.text)
		if low == "shortestpath" || low == "allshortestpaths" {
			return nil, unsupported(t.pos, "%s() is not supported; use a bounded variable-length pattern such as -[*1..4]- and ORDER BY length, or the search_paths tool", t.text)
		}
	}
	// A parenthesised pattern (GQL path pattern grouping) would need
	// quantified path patterns to be useful; refuse it plainly.
	if p.isPunct("(") && p.peekN(1).kind == tPunct && p.peekN(1).text == "(" {
		return nil, unsupported(p.peek().pos, "parenthesised path patterns are not supported")
	}
	n, err := p.parseNodePattern()
	if err != nil {
		return nil, err
	}
	pp.Nodes = append(pp.Nodes, n)
	for p.isPunct("-") || p.isPunct("<") {
		r, err := p.parseRelPattern()
		if err != nil {
			return nil, err
		}
		n, err := p.parseNodePattern()
		if err != nil {
			return nil, err
		}
		pp.Rels = append(pp.Rels, r)
		pp.Nodes = append(pp.Nodes, n)
	}
	if p.isPunct("{") {
		return nil, unsupported(p.peek().pos, "quantified path patterns ({m,n}) are not supported; use -[*m..n]-")
	}
	return pp, nil
}

func (p *parser) parseNodePattern() (*NodePattern, error) {
	t := p.peek()
	if err := p.expectPunct("("); err != nil {
		return nil, err
	}
	n := &NodePattern{Pos: t.pos}
	if tk := p.peek(); (tk.kind == tIdent && !isKwTok(tk, "IS") && !isKwTok(tk, "WHERE")) || tk.kind == tQuotedIdent {
		n.Var = tk.text
		p.next()
	}
	labels, err := p.parseLabels(true)
	if err != nil {
		return nil, err
	}
	n.Labels = labels
	if p.isPunct("{") || p.peek().kind == tParam {
		m, err := p.parsePropMap()
		if err != nil {
			return nil, err
		}
		n.Props = m
	}
	if p.isKw("WHERE") {
		return nil, unsupported(p.peek().pos, "WHERE inside a node pattern is not supported; put the condition in the MATCH's WHERE")
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	return n, nil
}

// parseLabels reads `:A:B`, `:A|B`, `:A|:B` and the GQL `IS A|B`, `IS A&B`.
func (p *parser) parseLabels(allowIs bool) ([][]string, error) {
	var out [][]string
	if allowIs && p.isKw("IS") {
		p.next()
		return p.parseLabelExpr(out)
	}
	for p.isPunct(":") {
		p.next()
		var err error
		out, err = p.parseLabelExpr(out)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (p *parser) parseLabelExpr(out [][]string) ([][]string, error) {
	if p.isPunct("!") || p.isPunct("%") || p.isPunct("(") {
		return nil, unsupported(p.peek().pos, "label negation, wildcards and grouping are not supported in label expressions")
	}
	name, err := p.parseName()
	if err != nil {
		return nil, err
	}
	group := []string{name}
	for {
		if p.acceptPunct("|") {
			p.acceptPunct(":")
			n, err := p.parseName()
			if err != nil {
				return nil, err
			}
			group = append(group, n)
			continue
		}
		break
	}
	out = append(out, group)
	// GQL conjunction A&B is the same as :A:B. The lexer has no '&' token, so
	// it surfaces as an unexpected character before we get here.
	return out, nil
}

func (p *parser) parseName() (string, error) {
	t := p.peek()
	if t.kind == tIdent || t.kind == tQuotedIdent {
		p.next()
		return t.text, nil
	}
	return "", p.errf(t, "expected a name, found %s", t)
}

func (p *parser) parsePropMap() (*MapLit, error) {
	if t := p.peek(); t.kind == tParam {
		return nil, unsupported(t.pos, "a parameter as a whole property map is not supported; write {key: $param}")
	}
	e, err := p.parseMapLit()
	if err != nil {
		return nil, err
	}
	return e, nil
}

func (p *parser) parseRelPattern() (*RelPattern, error) {
	start := p.peek()
	r := &RelPattern{Pos: start.pos, Min: 1, Max: 1}
	leftArrow := false
	if p.acceptPunct("<") {
		leftArrow = true
	}
	if err := p.expectPunct("-"); err != nil {
		return nil, err
	}
	if p.acceptPunct("[") {
		if tk := p.peek(); tk.kind == tIdent || tk.kind == tQuotedIdent {
			if !isKwTok(tk, "WHERE") && !isKwTok(tk, "IS") {
				r.Var = tk.text
				p.next()
			}
		}
		if p.acceptPunct(":") || p.acceptKw("IS") {
			for {
				n, err := p.parseName()
				if err != nil {
					return nil, err
				}
				r.Types = append(r.Types, n)
				if p.acceptPunct("|") {
					p.acceptPunct(":")
					continue
				}
				break
			}
			if p.isPunct(":") {
				return nil, unsupported(p.peek().pos, "a relationship has exactly one type; :A:B on a relationship never matches — use :A|B")
			}
		}
		if p.acceptPunct("*") {
			r.VarLen = true
			r.Min, r.Max = 1, -1
			if t := p.peek(); t.kind == tInt {
				v, err := p.smallInt(t)
				if err != nil {
					return nil, err
				}
				p.next()
				r.Min = v
				r.Max = v
				if p.acceptPunct("..") {
					r.Max = -1
					if t2 := p.peek(); t2.kind == tInt {
						v2, err := p.smallInt(t2)
						if err != nil {
							return nil, err
						}
						p.next()
						r.Max = v2
					}
				}
			} else if p.acceptPunct("..") {
				r.Min = 1
				if t2 := p.peek(); t2.kind == tInt {
					v2, err := p.smallInt(t2)
					if err != nil {
						return nil, err
					}
					p.next()
					r.Max = v2
				}
			}
		}
		if p.isPunct("{") {
			m, err := p.parsePropMap()
			if err != nil {
				return nil, err
			}
			r.Props = m
		} else if p.peek().kind == tParam {
			return nil, unsupported(p.peek().pos, "a parameter as a whole property map is not supported; write {key: $param}")
		}
		if p.isKw("WHERE") {
			return nil, unsupported(p.peek().pos, "WHERE inside a relationship pattern is not supported; put the condition in the MATCH's WHERE")
		}
		if err := p.expectPunct("]"); err != nil {
			return nil, err
		}
	}
	if err := p.expectPunct("-"); err != nil {
		return nil, err
	}
	rightArrow := p.acceptPunct(">")
	switch {
	case leftArrow && !rightArrow:
		r.Dir = DirIn
	case rightArrow && !leftArrow:
		r.Dir = DirOut
	default:
		r.Dir = DirBoth
	}
	return r, nil
}

func (p *parser) smallInt(t token) (int, error) {
	v, err := strconv.Atoi(t.text)
	if err != nil || v < 0 || v > 1000 {
		return 0, p.errf(t, "invalid variable-length bound %s", t.text)
	}
	return v, nil
}

func (p *parser) parseProjection(isReturn bool) (*ProjectionClause, error) {
	p.next()
	c := &ProjectionClause{IsReturn: isReturn}
	if p.acceptKw("DISTINCT") {
		c.Distinct = true
	}
	if p.acceptPunct("*") {
		c.Star = true
		if !p.acceptPunct(",") {
			goto tail
		}
	}
	for {
		start := p.peek().pos
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		end := p.prevEnd()
		item := &ProjectionItem{Expr: e}
		if p.acceptKw("AS") {
			n, err := p.parseName()
			if err != nil {
				return nil, err
			}
			item.Alias = n
			item.Explicit = true
		} else {
			if v, ok := e.(*Variable); ok {
				item.Alias = v.Name
			} else {
				item.Alias = strings.TrimSpace(p.src[start:end])
			}
		}
		c.Items = append(c.Items, item)
		if !p.acceptPunct(",") {
			break
		}
	}
tail:
	if p.acceptKw("ORDER") {
		if err := p.expectKw("BY"); err != nil {
			return nil, err
		}
		for {
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			si := &SortItem{Expr: e}
			switch {
			case p.acceptKw("DESC"), p.acceptKw("DESCENDING"):
				si.Desc = true
			case p.acceptKw("ASC"), p.acceptKw("ASCENDING"):
			}
			c.OrderBy = append(c.OrderBy, si)
			if !p.acceptPunct(",") {
				break
			}
		}
	}
	if p.acceptKw("SKIP") || p.acceptKw("OFFSET") {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		c.Skip = e
	}
	if p.acceptKw("LIMIT") {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		c.Limit = e
	}
	if !isReturn && p.acceptKw("WHERE") {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		c.Where = e
	}
	return c, nil
}

// prevEnd is the byte offset just past the previous token, for slicing the
// source text of an expression into a column name.
func (p *parser) prevEnd() int {
	if p.i == 0 {
		return 0
	}
	// The next token's start bounds the previous token's text, minus any
	// whitespace, which TrimSpace removes.
	return p.toks[p.i].pos
}

// --- expressions, lowest precedence first ----------------------------------

func (p *parser) parseExpr() (Expr, error) { return p.parseOr() }

func (p *parser) parseOr() (Expr, error) {
	l, err := p.parseXor()
	if err != nil {
		return nil, err
	}
	for p.acceptKw("OR") {
		r, err := p.parseXor()
		if err != nil {
			return nil, err
		}
		l = &Binary{Op: "OR", L: l, R: r}
	}
	return l, nil
}

func (p *parser) parseXor() (Expr, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.acceptKw("XOR") {
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l = &Binary{Op: "XOR", L: l, R: r}
	}
	return l, nil
}

func (p *parser) parseAnd() (Expr, error) {
	l, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.acceptKw("AND") {
		r, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		l = &Binary{Op: "AND", L: l, R: r}
	}
	return l, nil
}

func (p *parser) parseNot() (Expr, error) {
	if p.acceptKw("NOT") {
		x, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &Unary{Op: "NOT", X: x}, nil
	}
	return p.parseComparison()
}

func (p *parser) parseComparison() (Expr, error) {
	l, err := p.parseStringPred()
	if err != nil {
		return nil, err
	}
	// Chains like a < b < c mean a < b AND b < c in openCypher.
	var result Expr
	for {
		t := p.peek()
		if t.kind != tPunct {
			break
		}
		op := t.text
		switch op {
		case "=", "<>", "!=", "<", ">", "<=", ">=":
		default:
			goto done
		}
		p.next()
		if op == "!=" {
			op = "<>"
		}
		r, err := p.parseStringPred()
		if err != nil {
			return nil, err
		}
		cmp := &Binary{Op: op, L: l, R: r}
		if result == nil {
			result = cmp
		} else {
			result = &Binary{Op: "AND", L: result, R: cmp}
		}
		l = r
	}
done:
	if result == nil {
		return l, nil
	}
	return result, nil
}

// parseStringPred handles the predicates that bind tighter than comparison:
// =~, IN, STARTS WITH, ENDS WITH, CONTAINS, IS [NOT] NULL, IS [NOT] LABELED.
func (p *parser) parseStringPred() (Expr, error) {
	l, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.isPunct("=~"):
			p.next()
			r, err := p.parseAdd()
			if err != nil {
				return nil, err
			}
			l = &Binary{Op: "=~", L: l, R: r}
		case p.isKw("IN"):
			p.next()
			r, err := p.parseAdd()
			if err != nil {
				return nil, err
			}
			l = &Binary{Op: "IN", L: l, R: r}
		case p.isKw("STARTS") || p.isKw("ENDS"):
			op := strings.ToUpper(p.next().text)
			if err := p.expectKw("WITH"); err != nil {
				return nil, err
			}
			r, err := p.parseAdd()
			if err != nil {
				return nil, err
			}
			l = &Binary{Op: op, L: l, R: r}
		case p.isKw("CONTAINS"):
			p.next()
			r, err := p.parseAdd()
			if err != nil {
				return nil, err
			}
			l = &Binary{Op: "CONTAINS", L: l, R: r}
		case p.isKw("IS"):
			p.next()
			not := p.acceptKw("NOT")
			switch {
			case p.acceptKw("NULL"):
				l = &IsNull{X: l, Not: not}
			case p.acceptKw("LABELED"):
				labels, err := p.parseLabelExpr(nil)
				if err != nil {
					return nil, err
				}
				l = &LabelCheck{X: l, Labels: labels, Not: not}
			case p.isPunct(":"):
				labels, err := p.parseLabels(false)
				if err != nil {
					return nil, err
				}
				l = &LabelCheck{X: l, Labels: labels, Not: not}
			default:
				t := p.peek()
				if t.kind == tIdent {
					switch strings.ToUpper(t.text) {
					case "TYPED", "NORMALIZED", "DIRECTED", "SOURCE", "DESTINATION", "TRUE", "FALSE", "UNKNOWN":
						return nil, unsupported(t.pos, "IS %s is not supported", strings.ToUpper(t.text))
					}
				}
				return nil, p.errf(t, "expected NULL or LABELED after IS, found %s", t)
			}
		default:
			return l, nil
		}
	}
}

func (p *parser) parseAdd() (Expr, error) {
	l, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for p.isPunct("+") || p.isPunct("-") {
		op := p.next().text
		r, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		l = &Binary{Op: op, L: l, R: r}
	}
	return l, nil
}

func (p *parser) parseMul() (Expr, error) {
	l, err := p.parsePow()
	if err != nil {
		return nil, err
	}
	for p.isPunct("*") || p.isPunct("/") || p.isPunct("%") {
		op := p.next().text
		r, err := p.parsePow()
		if err != nil {
			return nil, err
		}
		l = &Binary{Op: op, L: l, R: r}
	}
	return l, nil
}

func (p *parser) parsePow() (Expr, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.isPunct("^") {
		p.next()
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l = &Binary{Op: "^", L: l, R: r}
	}
	return l, nil
}

func (p *parser) parseUnary() (Expr, error) {
	if p.isPunct("-") || p.isPunct("+") {
		op := p.next().text
		// Fold a negative number literal so -9223372036854775808 is
		// representable.
		if op == "-" {
			if t := p.peek(); t.kind == tInt {
				p.next()
				v, err := parseIntLit("-" + t.text)
				if err != nil {
					return nil, p.errf(t, "%v", err)
				}
				return p.parsePostfix(&Literal{Value: v})
			}
		}
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		if op == "+" {
			return &Unary{Op: "+", X: x}, nil
		}
		return &Unary{Op: "-", X: x}, nil
	}
	a, err := p.parseAtom()
	if err != nil {
		return nil, err
	}
	return p.parsePostfix(a)
}

func (p *parser) parsePostfix(x Expr) (Expr, error) {
	for {
		switch {
		case p.isPunct("."):
			p.next()
			k, err := p.parseName()
			if err != nil {
				return nil, err
			}
			x = &PropAccess{Target: x, Key: k}
		case p.isPunct("["):
			p.next()
			var from, to Expr
			var err error
			if !p.isPunct("..") {
				from, err = p.parseExpr()
				if err != nil {
					return nil, err
				}
			}
			if p.acceptPunct("..") {
				if !p.isPunct("]") {
					to, err = p.parseExpr()
					if err != nil {
						return nil, err
					}
				}
				if err := p.expectPunct("]"); err != nil {
					return nil, err
				}
				x = &Slice{Target: x, From: from, To: to}
				continue
			}
			if err := p.expectPunct("]"); err != nil {
				return nil, err
			}
			x = &Index{Target: x, Idx: from}
		case p.isPunct(":"):
			// n:Label as a predicate.
			labels, err := p.parseLabels(false)
			if err != nil {
				return nil, err
			}
			x = &LabelCheck{X: x, Labels: labels}
		case p.isPunct("{"):
			return nil, unsupported(p.peek().pos, "map projection (n {.prop}) is not supported; return n.prop AS prop")
		default:
			return x, nil
		}
	}
}

func parseIntLit(s string) (int64, error) {
	neg := strings.HasPrefix(s, "-")
	body := strings.TrimPrefix(s, "-")
	base := 10
	switch {
	case strings.HasPrefix(body, "0x") || strings.HasPrefix(body, "0X"):
		base, body = 16, body[2:]
	case strings.HasPrefix(body, "0o") || strings.HasPrefix(body, "0O"):
		base, body = 8, body[2:]
	}
	if neg {
		body = "-" + body
	}
	v, err := strconv.ParseInt(body, base, 64)
	if err != nil {
		return 0, fmt.Errorf("integer literal %s is out of range or malformed", s)
	}
	return v, nil
}

func (p *parser) parseAtom() (Expr, error) {
	t := p.peek()
	switch t.kind {
	case tInt:
		p.next()
		v, err := parseIntLit(t.text)
		if err != nil {
			return nil, p.errf(t, "%v", err)
		}
		return &Literal{Value: v}, nil
	case tFloat:
		p.next()
		v, err := strconv.ParseFloat(t.text, 64)
		if err != nil || math.IsInf(v, 0) {
			return nil, p.errf(t, "float literal %s is out of range", t.text)
		}
		return &Literal{Value: v}, nil
	case tString:
		p.next()
		return &Literal{Value: t.text}, nil
	case tParam:
		p.next()
		return &Param{Name: t.text}, nil
	case tQuotedIdent:
		p.next()
		return &Variable{Name: t.text}, nil
	case tPunct:
		switch t.text {
		case "(":
			// A parenthesised pattern in expression position is a pattern
			// predicate; detect the common shapes and refuse by name.
			if p.looksLikePattern() {
				return nil, unsupported(t.pos, "pattern expressions and pattern predicates are not supported; move the pattern into a MATCH or OPTIONAL MATCH")
			}
			p.next()
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if err := p.expectPunct(")"); err != nil {
				return nil, err
			}
			return e, nil
		case "[":
			return p.parseListOrComp()
		case "{":
			return p.parseMapLit()
		}
		return nil, p.errf(t, "unexpected %s", t)
	case tIdent:
		up := strings.ToUpper(t.text)
		switch up {
		case "TRUE":
			p.next()
			return &Literal{Value: true}, nil
		case "FALSE":
			p.next()
			return &Literal{Value: false}, nil
		case "NULL":
			p.next()
			return &Literal{Value: nil}, nil
		case "CASE":
			return p.parseCase()
		case "EXISTS", "COUNT", "COLLECT":
			if n := p.peekN(1); n.kind == tPunct && n.text == "{" {
				return nil, unsupported(t.pos, "%s { subquery } is not supported", up)
			}
		}
		if n := p.peekN(1); n.kind == tPunct && n.text == "(" {
			return p.parseCall()
		}
		// a.b.c( is a namespaced function call (apoc.*, db.*, gds.*).
		for j := 1; p.peekN(j).kind == tPunct && p.peekN(j).text == "." && p.peekN(j+1).kind == tIdent; j += 2 {
			if n := p.peekN(j + 2); n.kind == tPunct && n.text == "(" {
				return nil, unsupported(t.pos, "namespaced function %s.%s() is not supported", t.text, p.peekN(j+1).text)
			}
		}
		p.next()
		return &Variable{Name: t.text}, nil
	}
	return nil, p.errf(t, "unexpected %s", t)
}

// looksLikePattern is true for `(a)-->(b)`-like text in expression position.
func (p *parser) looksLikePattern() bool {
	depth := 0
	for j := p.i; j < len(p.toks); j++ {
		t := p.toks[j]
		if t.kind == tEOF {
			return false
		}
		if t.kind == tPunct {
			switch t.text {
			case "(":
				depth++
			case ")":
				depth--
				if depth == 0 {
					n := p.peekAt(j + 1)
					n2 := p.peekAt(j + 2)
					if n.kind == tPunct && n.text == "-" && n2.kind == tPunct && (n2.text == "-" || n2.text == "[" || n2.text == ">") {
						return true
					}
					if n.kind == tPunct && n.text == "<" && n2.kind == tPunct && n2.text == "-" {
						return true
					}
					return false
				}
			}
		}
	}
	return false
}

func (p *parser) peekAt(j int) token {
	if j < len(p.toks) {
		return p.toks[j]
	}
	return p.toks[len(p.toks)-1]
}

func (p *parser) parseCall() (Expr, error) {
	nameTok := p.next()
	name := strings.ToLower(nameTok.text)
	p.next() // (
	f := &FuncCall{Name: name}
	if name == "count" && p.isPunct("*") {
		p.next()
		f.Star = true
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		return f, nil
	}
	switch name {
	case "any", "all", "none", "single":
		return p.parseQuantifier(name)
	case "shortestpath", "allshortestpaths":
		return nil, unsupported(nameTok.pos, "%s() is not supported", nameTok.text)
	case "exists":
		if p.looksLikePattern() {
			return nil, unsupported(nameTok.pos, "exists() over a pattern is not supported; use OPTIONAL MATCH and IS NOT NULL")
		}
	case "reduce":
		return nil, unsupported(nameTok.pos, "reduce() is not supported")
	}
	if p.acceptKw("DISTINCT") {
		f.Distinct = true
	}
	if !p.isPunct(")") {
		for {
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			f.Args = append(f.Args, e)
			if !p.acceptPunct(",") {
				break
			}
		}
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	if _, ok := functionArity[name]; !ok && !aggregateNames[name] {
		return nil, unsupported(nameTok.pos, "function %s() is not supported", nameTok.text)
	}
	return f, nil
}

func (p *parser) parseQuantifier(kind string) (Expr, error) {
	v, err := p.parseName()
	if err != nil {
		return nil, err
	}
	if err := p.expectKw("IN"); err != nil {
		return nil, err
	}
	list, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if err := p.expectKw("WHERE"); err != nil {
		return nil, err
	}
	pred, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	return &Quantifier{Kind: kind, Var: v, List: list, Pred: pred}, nil
}

func (p *parser) parseListOrComp() (Expr, error) {
	p.next() // [
	// [x IN list ...] is a comprehension.
	if t := p.peek(); (t.kind == tIdent || t.kind == tQuotedIdent) && isKwTok(p.peekN(1), "IN") {
		save := p.i
		v := t.text
		p.i += 2
		list, err := p.parseExpr()
		if err == nil && (p.isKw("WHERE") || p.isPunct("|") || p.isPunct("]")) {
			lc := &ListComp{Var: v, List: list}
			if p.acceptKw("WHERE") {
				lc.Pred, err = p.parseExpr()
				if err != nil {
					return nil, err
				}
			}
			if p.acceptPunct("|") {
				lc.Proj, err = p.parseExpr()
				if err != nil {
					return nil, err
				}
			}
			if err := p.expectPunct("]"); err != nil {
				return nil, err
			}
			return lc, nil
		}
		p.i = save
	}
	l := &ListLit{}
	if p.acceptPunct("]") {
		return l, nil
	}
	for {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		l.Items = append(l.Items, e)
		if !p.acceptPunct(",") {
			break
		}
	}
	if err := p.expectPunct("]"); err != nil {
		return nil, err
	}
	return l, nil
}

func (p *parser) parseMapLit() (*MapLit, error) {
	if err := p.expectPunct("{"); err != nil {
		return nil, err
	}
	m := &MapLit{}
	if p.acceptPunct("}") {
		return m, nil
	}
	seen := map[string]bool{}
	for {
		kt := p.peek()
		k, err := p.parseName()
		if err != nil {
			return nil, err
		}
		if seen[k] {
			return nil, p.errf(kt, "duplicate map key %s", k)
		}
		seen[k] = true
		if err := p.expectPunct(":"); err != nil {
			return nil, err
		}
		v, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		m.Keys = append(m.Keys, k)
		m.Values = append(m.Values, v)
		if !p.acceptPunct(",") {
			break
		}
	}
	if err := p.expectPunct("}"); err != nil {
		return nil, err
	}
	return m, nil
}

func (p *parser) parseCase() (Expr, error) {
	p.next() // CASE
	c := &CaseExpr{}
	if !p.isKw("WHEN") {
		t, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		c.Test = t
	}
	for p.acceptKw("WHEN") {
		w, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expectKw("THEN"); err != nil {
			return nil, err
		}
		th, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		c.Whens = append(c.Whens, w)
		c.Thens = append(c.Thens, th)
	}
	if len(c.Whens) == 0 {
		return nil, p.errf(p.peek(), "CASE needs at least one WHEN")
	}
	if p.acceptKw("ELSE") {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		c.Else = e
	}
	if err := p.expectKw("END"); err != nil {
		return nil, err
	}
	return c, nil
}
