package cypher

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// SQL generation for one MATCH clause.
//
// The division of labour, which everything else in this package follows from:
// SQL finds candidate bindings — which nodes and edges can stand where in the
// pattern — and Go decides what they mean. Labels, relationship types,
// direction, endpoints, variable-length reachability and relationship
// uniqueness inside a trail are all SQL. Property comparisons are pushed into
// SQL only as a *superset* pre-filter and are always re-checked exactly in Go.
//
// The reason is that the two databases disagree about JSON: SQLite's
// json_extract returns a typed value (true reads back as 1), PostgreSQL's
// ->> returns text (true reads back as 'true'), and openCypher's own rules —
// `1 = 1.0` is true, `1 = '1'` is false, comparing a string to a number is
// null — match neither. A filter written to agree with openCypher on both
// backends would be three filters. A superset filter only has to be right
// about strings, which both databases store and return verbatim, and the
// exact check runs once, in one place, the same way on both.
//
// Nothing a caller wrote is ever spliced into the SQL text. Labels, types,
// property keys, string values and ids are bound parameters; the text is built
// only from fixed fragments and aliases this file generates (n0, e1, vl2).
// FuzzParseNeverLeaksUserText holds that line.

// sqlBuilder accumulates SQL text and its arguments in textual order, which
// is the order positional placeholders bind in on both databases.
type sqlBuilder struct {
	b    strings.Builder
	args []any
}

func (s *sqlBuilder) w(parts ...string) {
	for _, p := range parts {
		s.b.WriteString(p)
	}
}

func (s *sqlBuilder) arg(v any) {
	s.b.WriteString("?")
	s.args = append(s.args, v)
}

func (s *sqlBuilder) add(o *sqlBuilder) {
	s.b.WriteString(o.b.String())
	s.args = append(s.args, o.args...)
}

func (s *sqlBuilder) argList(vals []string) {
	s.w("(")
	for i, v := range vals {
		if i > 0 {
			s.w(", ")
		}
		s.arg(v)
	}
	s.w(")")
}

// cond is a predicate rendered against an alias, so the same filter can be
// written once and placed both in the main query and in a recursive CTE's
// seed under a different alias.
type cond func(s *sqlBuilder, alias string)

type nodeEl struct {
	alias   string
	varName string // "" for anonymous
	conds   []cond
	bound   bool     // the variable comes from an earlier clause
	ids     []string // when bound: the candidate ids, nil meaning unconstrained
	score   int      // how selective its conditions are, for seeding traversals
	props   []*MapLit
	// light nodes are anonymous, unnamed by any path and carry no property
	// map: nothing will ever read more than their id, so the SQL does not
	// fetch more.
	light bool
}

type relEl struct {
	alias       string
	varName     string
	pat         *RelPattern
	left, right int // node element indexes, in pattern order
	conds       []cond
	bound       bool
	ids         []string
	// variable-length only
	seedLeft bool
	cte      string
}

type pathEl struct {
	varName string
	nodes   []int
	rels    []int
}

// clausePlan is one MATCH compiled.
type clausePlan struct {
	nodes []*nodeEl
	rels  []*relEl
	paths []*pathEl
	// unboundedCheck holds a query that must return no rows for an
	// unbounded `*` to be answered exactly (see compileVarLen).
	unboundedChecks []*sqlBuilder
	sql             *sqlBuilder
	needContent     bool
}

// sources supplies the FROM-clause text for the node and edge tables (a bare
// table name, or an as-of subquery) and the arguments that text binds.
type sources struct {
	nodeSrc  string
	nodeArgs []any
	edgeSrc  string
	edgeArgs []any
	kind     sqldialect.Kind
	dialect  sqldialect.Dialect
	// needContent is false when nothing in the query can observe a node's
	// content column; chunk and memory nodes carry kilobytes of it.
	needContent bool
	// validAt is the instant edge-property validity (valid_from/valid_to
	// written into properties by temporal facts) is judged at, RFC 3339 UTC;
	// empty disables the filter.
	validAt string
}

func (src *sources) nodeFrom(s *sqlBuilder, alias string) {
	s.w(src.nodeSrc, " AS ", alias)
	s.args = append(s.args, src.nodeArgs...)
}

func (src *sources) edgeFrom(s *sqlBuilder, alias string) {
	s.w(src.edgeSrc, " AS ", alias)
	s.args = append(s.args, src.edgeArgs...)
}

func (src *sources) sep() string {
	if src.kind == sqldialect.Postgres {
		return "chr(31)"
	}
	return "char(31)"
}

func (src *sources) notIn(s *sqlBuilder, hay, needleCol string) {
	if src.kind == sqldialect.Postgres {
		s.w("strpos(", hay, ", ", src.sep(), " || ", needleCol, " || ", src.sep(), ") = 0")
		return
	}
	s.w("instr(", hay, ", ", src.sep(), " || ", needleCol, " || ", src.sep(), ") = 0")
}

// jsonProp renders a superset-safe read of one top-level property key, with
// the key bound as a parameter. ok is false when the key cannot be expressed
// safely as a bound JSON path, and the caller then skips the pre-filter.
func (src *sources) jsonProp(s *sqlBuilder, alias, key string) bool {
	if src.kind == sqldialect.Postgres {
		s.w("(CASE WHEN NULLIF(", alias, ".properties::text, '') IS NOT NULL THEN (NULLIF(", alias, ".properties::text, '')::jsonb ->> CAST(")
		s.arg(key)
		s.w(" AS TEXT)) END)")
		return true
	}
	if !safeJSONKey(key) {
		return false
	}
	s.w("json_extract(CASE WHEN json_valid(", alias, ".properties) = 1 THEN ", alias, ".properties END, ")
	s.arg(`$."` + key + `"`)
	s.w(")")
	return true
}

// safeJSONKey is true for keys SQLite's JSON path syntax can quote. A key
// with a double quote or backslash has no spelling there, so it simply gets
// no pre-filter and is matched in Go like every other comparison.
func safeJSONKey(k string) bool {
	if k == "" {
		return false
	}
	for _, r := range k {
		if r == '"' || r == '\\' || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// propCond builds the superset pre-filter for `alias.key <op> value`, where
// value is a string. The property-to-column fallbacks (name → title,
// content → the content column, id → the id column) are OR-ed in so the
// filter is never narrower than the exact Go check.
func (src *sources) propCond(isNode bool, key, op string, vals []string) cond {
	return func(s *sqlBuilder, alias string) {
		alts := []func(*sqlBuilder) bool{
			func(s *sqlBuilder) bool { return src.jsonProp(s, alias, key) },
		}
		switch {
		case isNode && key == "name":
			alts = append(alts, func(s *sqlBuilder) bool { return src.jsonProp(s, alias, "title") })
		case isNode && key == "content":
			alts = append(alts, func(s *sqlBuilder) bool { s.w(alias, ".content"); return true })
		}
		s.w("(")
		wrote := 0
		for _, alt := range alts {
			var t sqlBuilder
			if !alt(&t) {
				continue
			}
			if wrote > 0 {
				s.w(" OR ")
			}
			wrote++
			s.add(&t)
			switch op {
			case "=":
				s.w(" = ")
				s.arg(vals[0])
			case "IN":
				s.w(" IN ")
				s.argList(vals)
			case "LIKE":
				s.w(" LIKE ")
				s.arg(vals[0])
				s.w(` ESCAPE '\'`)
			}
		}
		if wrote == 0 {
			s.w("1 = 1")
		}
		s.w(")")
	}
}

func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func colIn(col string, vals []string) cond {
	return func(s *sqlBuilder, alias string) {
		s.w(alias, ".", col, " IN ")
		s.argList(vals)
	}
}

// validity keeps temporal facts that have ended (or not yet begun) out of a
// match, the way graphflow's own as-of query judges them: valid_from and
// valid_to are RFC 3339 UTC strings in the edge's properties, so text order
// is time order.
func (src *sources) validity(s *sqlBuilder, alias string) {
	vt := src.dialect.JSONTextGuarded(alias+".properties", "valid_to")
	vf := src.dialect.JSONTextGuarded(alias+".properties", "valid_from")
	// The LIKE is a fast path, not a filter: an edge whose properties text
	// does not even contain "valid" cannot carry either key, so the JSON is
	// parsed only for the few that might. Most edges have no properties.
	s.w("(", alias, ".properties IS NULL OR ", alias, ".properties NOT LIKE '%valid%' OR ((")
	s.w(vt, " IS NULL OR ", vt, " = '' OR ", vt, " > ")
	s.arg(src.validAt)
	s.w(") AND (", vf, " IS NULL OR ", vf, " = '' OR ", vf, " <= ")
	s.arg(src.validAt)
	s.w(")))")
}

// pushdown collects superset pre-filters from a WHERE conjunct that reads a
// single pattern variable. Anything it does not recognise is simply left to
// the exact Go evaluation.
func (src *sources) pushdown(e Expr, params map[string]any, nodeByVar map[string]*nodeEl, relByVar map[string]*relEl) {
	switch x := e.(type) {
	case *Binary:
		if x.Op == "AND" {
			src.pushdown(x.L, params, nodeByVar, relByVar)
			src.pushdown(x.R, params, nodeByVar, relByVar)
			return
		}
		l, r := x.L, x.R
		if x.Op == "=" {
			if _, ok := stringValue(l, params); ok {
				l, r = r, l
			}
		}
		switch x.Op {
		case "=":
			v, ok := stringValue(r, params)
			if !ok {
				return
			}
			src.pushOne(l, "=", []string{v}, nodeByVar, relByVar)
		case "IN":
			vals, ok := stringListValue(r, params)
			if !ok {
				return
			}
			src.pushOne(l, "IN", vals, nodeByVar, relByVar)
		case "STARTS", "ENDS", "CONTAINS":
			v, ok := stringValue(r, params)
			if !ok {
				return
			}
			pat := likeEscape(v)
			switch x.Op {
			case "STARTS":
				pat += "%"
			case "ENDS":
				pat = "%" + pat
			default:
				pat = "%" + pat + "%"
			}
			if pa, ok := l.(*PropAccess); ok {
				src.pushOne(pa, "LIKE", []string{pat}, nodeByVar, relByVar)
			}
		}
	case *LabelCheck:
		if x.Not {
			return
		}
		v, ok := x.X.(*Variable)
		if !ok {
			return
		}
		if n := nodeByVar[v.Name]; n != nil {
			for _, g := range x.Labels {
				n.conds = append(n.conds, colIn("node_type", g))
				n.score += 1
			}
		}
		if r := relByVar[v.Name]; r != nil && !r.pat.VarLen && len(x.Labels) == 1 {
			r.conds = append(r.conds, colIn("edge_type", x.Labels[0]))
		}
	}
}

func (src *sources) pushOne(l Expr, op string, vals []string, nodeByVar map[string]*nodeEl, relByVar map[string]*relEl) {
	switch t := l.(type) {
	case *PropAccess:
		v, ok := t.Target.(*Variable)
		if !ok {
			return
		}
		if n := nodeByVar[v.Name]; n != nil {
			n.conds = append(n.conds, src.propCond(true, t.Key, op, vals))
			n.score += 4
		}
		if r := relByVar[v.Name]; r != nil && !r.pat.VarLen {
			r.conds = append(r.conds, src.propCond(false, t.Key, op, vals))
		}
	case *FuncCall:
		if op == "LIKE" || len(t.Args) != 1 {
			return
		}
		v, ok := t.Args[0].(*Variable)
		if !ok {
			return
		}
		switch t.Name {
		case "id", "elementid":
			if n := nodeByVar[v.Name]; n != nil {
				n.conds = append(n.conds, colIn("id", vals))
				n.score += 8
			}
			if r := relByVar[v.Name]; r != nil && !r.pat.VarLen {
				r.conds = append(r.conds, colIn("id", vals))
			}
		case "type":
			if r := relByVar[v.Name]; r != nil && !r.pat.VarLen {
				r.conds = append(r.conds, colIn("edge_type", vals))
			}
		}
	}
}

func stringValue(e Expr, params map[string]any) (string, bool) {
	switch x := e.(type) {
	case *Literal:
		s, ok := x.Value.(string)
		return s, ok
	case *Param:
		s, ok := params[x.Name].(string)
		return s, ok
	}
	return "", false
}

func stringListValue(e Expr, params map[string]any) ([]string, bool) {
	var items []any
	switch x := e.(type) {
	case *ListLit:
		for _, it := range x.Items {
			s, ok := stringValue(it, params)
			if !ok {
				return nil, false
			}
			items = append(items, s)
		}
	case *Param:
		l, ok := params[x.Name].([]any)
		if !ok {
			return nil, false
		}
		items = l
	default:
		return nil, false
	}
	if len(items) == 0 || len(items) > 500 {
		return nil, false
	}
	out := make([]string, len(items))
	for i, it := range items {
		s, ok := it.(string)
		if !ok {
			return nil, false
		}
		out[i] = s
	}
	return out, true
}

// mapPushdown turns an inline property map into pre-filters: {name: 'x'} is
// the same question as WHERE n.name = 'x'.
func (src *sources) mapPushdown(m *MapLit, isNode bool, params map[string]any) (conds []cond, score int) {
	if m == nil {
		return nil, 0
	}
	for i, k := range m.Keys {
		if v, ok := stringValue(m.Values[i], params); ok {
			conds = append(conds, src.propCond(isNode, k, "=", []string{v}))
			score += 4
		}
	}
	return conds, score
}

// compile builds the SQL for one MATCH clause.
func (src *sources) compile(c *MatchClause, params map[string]any, bound map[string][]string, boundKinds map[string]varKind) (*clausePlan, error) {
	p := &clausePlan{}
	nodeByVar := map[string]*nodeEl{}
	relByVar := map[string]*relEl{}

	addNode := func(np *NodePattern) *nodeEl {
		if np.Var != "" {
			if n := nodeByVar[np.Var]; n != nil {
				n.props = append(n.props, np.Props)
				cs, sc := src.mapPushdown(np.Props, true, params)
				n.conds = append(n.conds, cs...)
				n.score += sc
				for _, g := range np.Labels {
					n.conds = append(n.conds, colIn("node_type", g))
					n.score++
				}
				return n
			}
		}
		n := &nodeEl{alias: fmt.Sprintf("n%d", len(p.nodes)), varName: np.Var}
		n.props = append(n.props, np.Props)
		for _, g := range np.Labels {
			n.conds = append(n.conds, colIn("node_type", g))
			n.score++
		}
		cs, sc := src.mapPushdown(np.Props, true, params)
		n.conds = append(n.conds, cs...)
		n.score += sc
		if np.Var != "" {
			if ids, ok := bound[np.Var]; ok {
				n.bound = true
				n.ids = ids
				if ids != nil {
					n.conds = append(n.conds, colIn("id", ids))
					n.score += 16
				}
			}
			nodeByVar[np.Var] = n
		}
		p.nodes = append(p.nodes, n)
		return n
	}

	for _, pp := range c.Patterns {
		path := &pathEl{varName: pp.PathVar}
		prev := -1
		for i, np := range pp.Nodes {
			n := addNode(np)
			idx := indexOfNode(p.nodes, n)
			path.nodes = append(path.nodes, idx)
			if i > 0 {
				rp := pp.Rels[i-1]
				r := &relEl{alias: fmt.Sprintf("e%d", len(p.rels)), varName: rp.Var, pat: rp, left: prev, right: idx}
				if rp.VarLen {
					r.alias = fmt.Sprintf("vl%d", len(p.rels))
				}
				if len(rp.Types) > 0 {
					r.conds = append(r.conds, colIn("edge_type", rp.Types))
				}
				if !rp.VarLen {
					cs, _ := src.mapPushdown(rp.Props, false, params)
					r.conds = append(r.conds, cs...)
				}
				if rp.Var != "" {
					if ids, ok := bound[rp.Var]; ok {
						r.bound = true
						r.ids = ids
						if ids != nil {
							r.conds = append(r.conds, colIn("id", ids))
						}
					}
					relByVar[rp.Var] = r
				}
				path.rels = append(path.rels, len(p.rels))
				p.rels = append(p.rels, r)
			}
			prev = idx
		}
		if pp.PathVar != "" {
			p.paths = append(p.paths, path)
		}
	}
	_ = boundKinds
	if c.Where != nil {
		src.pushdown(c.Where, params, nodeByVar, relByVar)
	}

	// The recursive CTEs come first in the text, so they are built first.
	var ctes sqlBuilder
	for _, r := range p.rels {
		if !r.pat.VarLen {
			continue
		}
		if err := src.compileVarLen(p, r, &ctes); err != nil {
			return nil, err
		}
	}

	var sel, from, where sqlBuilder
	sel.w("SELECT ")
	cols := 0
	col := func(parts ...string) {
		if cols > 0 {
			sel.w(", ")
		}
		sel.w(parts...)
		cols++
	}
	inPath := map[int]bool{}
	for _, pe := range p.paths {
		for _, ni := range pe.nodes {
			inPath[ni] = true
		}
	}
	for i, n := range p.nodes {
		n.light = n.varName == "" && !inPath[i] && !hasProps(n.props)
		col(n.alias, ".id")
		if n.light {
			col("NULL")
			col("NULL")
			col("NULL")
			continue
		}
		col(n.alias, ".node_type")
		col(n.alias, ".properties")
		if src.needContent {
			col(n.alias, ".content")
		} else {
			col("NULL")
		}
	}
	for _, r := range p.rels {
		if r.pat.VarLen {
			col(r.alias, ".p")
			continue
		}
		col(r.alias, ".id")
		col(r.alias, ".from_node_id")
		col(r.alias, ".to_node_id")
		col("COALESCE(", r.alias, ".edge_type, '')")
		col("COALESCE(", r.alias, ".weight, 0)")
		col(r.alias, ".properties")
	}

	from.w(" FROM ")
	first := true
	sepFrom := func() {
		if !first {
			from.w(", ")
		}
		first = false
	}
	var conds []func()
	_ = conds
	nw := 0
	and := func() {
		if nw == 0 {
			where.w(" WHERE ")
		} else {
			where.w(" AND ")
		}
		nw++
	}
	for _, n := range p.nodes {
		sepFrom()
		src.nodeFrom(&from, n.alias)
		for _, c := range n.conds {
			and()
			c(&where, n.alias)
		}
	}
	for _, r := range p.rels {
		sepFrom()
		l, rt := p.nodes[r.left].alias, p.nodes[r.right].alias
		if r.pat.VarLen {
			from.w(r.cte, " AS ", r.alias)
			seed, other := l, rt
			if !r.seedLeft {
				seed, other = rt, l
			}
			and()
			where.w(r.alias, ".s = ", seed, ".id AND ", r.alias, ".c = ", other, ".id AND ", r.alias, ".d >= ")
			where.arg(int64(r.pat.Min))
			continue
		}
		src.edgeFrom(&from, r.alias)
		and()
		switch r.pat.Dir {
		case DirOut:
			where.w(r.alias, ".from_node_id = ", l, ".id AND ", r.alias, ".to_node_id = ", rt, ".id")
		case DirIn:
			where.w(r.alias, ".from_node_id = ", rt, ".id AND ", r.alias, ".to_node_id = ", l, ".id")
		default:
			where.w("((", r.alias, ".from_node_id = ", l, ".id AND ", r.alias, ".to_node_id = ", rt, ".id) OR (",
				r.alias, ".from_node_id = ", rt, ".id AND ", r.alias, ".to_node_id = ", l, ".id))")
		}
		for _, c := range r.conds {
			and()
			c(&where, r.alias)
		}
		if src.validAt != "" {
			and()
			src.validity(&where, r.alias)
		}
	}
	// Relationship isomorphism between fixed-length relationships is cheap
	// to state in SQL; anything involving a variable-length list is checked
	// in Go, which has the lists.
	for i := 0; i < len(p.rels); i++ {
		for j := i + 1; j < len(p.rels); j++ {
			if p.rels[i].pat.VarLen || p.rels[j].pat.VarLen {
				continue
			}
			and()
			where.w(p.rels[i].alias, ".id <> ", p.rels[j].alias, ".id")
		}
	}

	var all sqlBuilder
	if ctes.b.Len() > 0 {
		all.w("WITH RECURSIVE ")
		all.add(&ctes)
		all.w(" ")
	}
	all.add(&sel)
	all.add(&from)
	all.add(&where)
	p.sql = &all
	return p, nil
}

func hasProps(ms []*MapLit) bool {
	for _, m := range ms {
		if m != nil && len(m.Keys) > 0 {
			return true
		}
	}
	return false
}

func indexOfNode(ns []*nodeEl, n *nodeEl) int {
	for i := range ns {
		if ns[i] == n {
			return i
		}
	}
	return -1
}

// compileVarLen writes the recursive CTE for one variable-length relationship.
//
// The traversal is seeded from whichever endpoint is more constrained — a
// bound id beats a property filter beats a label — because a CTE seeded from
// every node computes every trail in the graph before the join throws most of
// them away. The CTE tracks the edges walked as a delimited string and refuses
// an edge already on it, which is openCypher's relationship isomorphism: a
// trail may revisit a node but never reuse a relationship.
//
// An unbounded `*` is answered up to MaxVarLength hops, and only after a
// separate check proves no trail from any seed is longer than that; if one is,
// the query is refused rather than silently cut short.
func (src *sources) compileVarLen(p *clausePlan, r *relEl, ctes *sqlBuilder) error {
	left, right := p.nodes[r.left], p.nodes[r.right]
	r.seedLeft = left.score >= right.score
	seed := left
	dir := r.pat.Dir
	if !r.seedLeft {
		seed = right
		switch dir {
		case DirOut:
			dir = DirIn
		case DirIn:
			dir = DirOut
		}
	}
	maxD := r.pat.Max
	unbounded := maxD < 0
	if unbounded {
		maxD = MaxVarLength
	}
	name := "cte" + strings.TrimPrefix(r.alias, "vl")
	r.cte = name

	build := func(limit int) *sqlBuilder {
		var b sqlBuilder
		b.w(name, "(s, c, p, d) AS (SELECT sn.id, sn.id, ", src.sep(), ", 0 FROM ")
		src.nodeFrom(&b, "sn")
		for i, c := range seed.conds {
			if i == 0 {
				b.w(" WHERE ")
			} else {
				b.w(" AND ")
			}
			c(&b, "sn")
		}
		b.w(" UNION ALL SELECT v.s, ")
		switch dir {
		case DirOut:
			b.w("e.to_node_id")
		case DirIn:
			b.w("e.from_node_id")
		default:
			b.w("CASE WHEN e.from_node_id = v.c THEN e.to_node_id ELSE e.from_node_id END")
		}
		b.w(", v.p || e.id || ", src.sep(), ", v.d + 1 FROM ", name, " AS v, ")
		src.edgeFrom(&b, "e")
		b.w(" WHERE v.d < ")
		b.arg(int64(limit))
		b.w(" AND ")
		switch dir {
		case DirOut:
			b.w("e.from_node_id = v.c")
		case DirIn:
			b.w("e.to_node_id = v.c")
		default:
			b.w("(e.from_node_id = v.c OR e.to_node_id = v.c)")
		}
		b.w(" AND ")
		src.notIn(&b, "v.p", "e.id")
		for _, c := range r.conds {
			b.w(" AND ")
			c(&b, "e")
		}
		if src.validAt != "" {
			b.w(" AND ")
			src.validity(&b, "e")
		}
		b.w(")")
		return &b
	}

	if ctes.b.Len() > 0 {
		ctes.w(", ")
	}
	ctes.add(build(maxD))
	if unbounded {
		var chk sqlBuilder
		chk.w("WITH RECURSIVE ")
		chk.add(build(maxD + 1))
		chk.w(" SELECT 1 FROM ", name, " WHERE d > ")
		chk.arg(int64(maxD))
		chk.w(" LIMIT 1")
		p.unboundedChecks = append(p.unboundedChecks, &chk)
	}
	return nil
}
