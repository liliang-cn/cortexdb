package cypher

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// Backend is what the engine needs from a graph store: a way to run a query
// (placeholders already rebound for the database) and the FROM-clause text
// for the node and edge tables as the read should see them — the live tables,
// or an as-of union with history.
type Backend interface {
	Dialect() sqldialect.Dialect
	Query(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	NodeSource(ctx context.Context) (string, []any)
	EdgeSource(ctx context.Context) (string, []any)
}

// Defaults and hard limits. The hard limits are not negotiable per request:
// they are what keeps one query from monopolising a shared brain.
const (
	DefaultMaxRows         = 1000
	HardMaxRows            = 10000
	DefaultMaxIntermediate = 200000
	HardMaxIntermediate    = 1000000
	DefaultTimeout         = 10 * time.Second
	HardMaxTimeout         = 60 * time.Second
	// idInListLimit is the most distinct bound ids passed into a later
	// clause's SQL as an IN list; beyond it the clause runs unconstrained and
	// the join happens in Go alone.
	idInListLimit = 900
)

// Options tune one execution.
type Options struct {
	Params map[string]any
	// MaxRows caps the rows returned; more set Truncated. Default 1000, at most 10000.
	MaxRows int
	// MaxIntermediate caps the bindings any clause may produce. Exceeding it
	// is an error, never a silent truncation: an aggregate over a cut-off
	// stream would be a wrong number. Default 200000.
	MaxIntermediate int
	// Timeout bounds the whole execution. Default 10s, at most 60s.
	Timeout time.Duration
	// ValidAt is the instant temporal facts (valid_from / valid_to in an
	// edge's properties) are judged at. Zero means now.
	ValidAt time.Time
	// IncludeEndedFacts turns that filter off.
	IncludeEndedFacts bool
}

// Result is a query's answer.
type Result struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	Truncated bool     `json:"truncated,omitempty"`
	Stats     Stats    `json:"stats"`
}

// Stats says what the query cost.
type Stats struct {
	SQLStatements    int   `json:"sql_statements"`
	CandidateRows    int   `json:"candidate_rows"`
	ElapsedMillis    int64 `json:"elapsed_ms"`
	ParsedAndChecked bool  `json:"-"`
}

// Execute parses, checks and runs a read-only query.
func Execute(ctx context.Context, b Backend, query string, opts Options) (*Result, error) {
	start := time.Now()
	q, err := Parse(query)
	if err != nil {
		return nil, err
	}
	cols, err := check(q)
	if err != nil {
		return nil, err
	}
	params := map[string]any{}
	for k, v := range opts.Params {
		n, err := NormalizeParam(v)
		if err != nil {
			return nil, semantic("parameter $%s: %v", k, err)
		}
		params[k] = n
	}
	if opts.MaxRows <= 0 {
		opts.MaxRows = DefaultMaxRows
	}
	opts.MaxRows = min(opts.MaxRows, HardMaxRows)
	if opts.MaxIntermediate <= 0 {
		opts.MaxIntermediate = DefaultMaxIntermediate
	}
	opts.MaxIntermediate = min(opts.MaxIntermediate, HardMaxIntermediate)
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	opts.Timeout = min(opts.Timeout, HardMaxTimeout)
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	x := &executor{
		ctx:   ctx,
		b:     b,
		opts:  opts,
		nodes: map[string]*Node{},
		rels:  map[string]*Rel{},
	}
	x.ev = &evaluator{params: params, nodeAt: x.nodeByID, work: new(int)}
	nodeSrc, nodeArgs := b.NodeSource(ctx)
	edgeSrc, edgeArgs := b.EdgeSource(ctx)
	x.src = &sources{
		nodeSrc: nodeSrc, nodeArgs: nodeArgs,
		edgeSrc: edgeSrc, edgeArgs: edgeArgs,
		kind: b.Dialect().Kind(), dialect: b.Dialect(),
		needContent: needsContent(q),
	}
	if !opts.IncludeEndedFacts {
		at := opts.ValidAt
		if at.IsZero() {
			at = time.Now()
		}
		x.src.validAt = at.UTC().Format(time.RFC3339)
	}

	res := &Result{Columns: cols}
	// UNION removes duplicates across all parts; UNION ALL keeps them. The
	// two cannot be mixed in one query (check refuses it).
	distinct := len(q.UnionAll) > 0 && !q.UnionAll[0]
	var all [][]any
	seen := map[string]bool{}
	for _, part := range q.Parts {
		rows, err := x.runSingle(part)
		if err != nil {
			return nil, x.wrap(err)
		}
		for _, r := range rows {
			if distinct {
				k := groupKey(r...)
				if seen[k] {
					continue
				}
				seen[k] = true
			}
			all = append(all, r)
		}
	}
	if len(all) > opts.MaxRows {
		all = all[:opts.MaxRows]
		res.Truncated = true
	}
	for _, r := range all {
		for _, v := range r {
			if err := materialize(v); err != nil {
				return nil, err
			}
		}
	}
	if all == nil {
		all = [][]any{}
	}
	res.Rows = all
	res.Stats = x.stats
	res.Stats.ElapsedMillis = time.Since(start).Milliseconds()
	return res, nil
}

type executor struct {
	ctx   context.Context
	b     Backend
	opts  Options
	src   *sources
	ev    *evaluator
	nodes map[string]*Node
	rels  map[string]*Rel
	stats Stats
}

func (x *executor) wrap(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(x.ctx.Err(), context.DeadlineExceeded) {
		return &Error{Kind: ErrBudget, Pos: -1, Msg: fmt.Sprintf("query exceeded its time budget of %s; add labels, property filters or a smaller variable-length bound", x.opts.Timeout)}
	}
	var ce *Error
	if errors.As(err, &ce) {
		return ce
	}
	return &Error{Kind: ErrRuntime, Pos: -1, Msg: err.Error()}
}

func (x *executor) budget(n int) error {
	if n > x.opts.MaxIntermediate {
		return &Error{Kind: ErrBudget, Pos: -1, Msg: fmt.Sprintf("a clause produced more than %d intermediate rows; narrow the pattern with labels or property filters", x.opts.MaxIntermediate)}
	}
	return nil
}

func (x *executor) runSingle(sq *SingleQuery) ([][]any, error) {
	rows := []row{{}}
	scope := map[string]varKind{}
	for _, cl := range sq.Clauses {
		if err := x.ctx.Err(); err != nil {
			return nil, err
		}
		switch c := cl.(type) {
		case *MatchClause:
			var err error
			rows, err = x.match(c, rows, scope)
			if err != nil {
				return nil, err
			}
			for _, pp := range c.Patterns {
				for _, n := range pp.Nodes {
					if n.Var != "" {
						if _, ok := scope[n.Var]; !ok {
							scope[n.Var] = kNode
						}
					}
				}
				for _, r := range pp.Rels {
					if r.Var != "" {
						if _, ok := scope[r.Var]; !ok {
							scope[r.Var] = kRel
							if r.VarLen {
								scope[r.Var] = kRelList
							}
						}
					}
				}
				if pp.PathVar != "" {
					scope[pp.PathVar] = kPath
				}
			}
		case *UnwindClause:
			var out []row
			for _, r := range rows {
				v, err := x.ev.eval(c.Expr, r)
				if err != nil {
					return nil, err
				}
				switch l := v.(type) {
				case nil:
				case []any:
					for _, it := range l {
						out = append(out, extend(r, c.Var, it))
					}
				default:
					out = append(out, extend(r, c.Var, v))
				}
				if err := x.budget(len(out)); err != nil {
					return nil, err
				}
			}
			rows = out
			scope[c.Var] = kValue
		case *ProjectionClause:
			cols, out, next, err := x.project(c, rows, scope)
			if err != nil {
				return nil, err
			}
			if c.IsReturn {
				res := make([][]any, len(out))
				for i, r := range out {
					vals := make([]any, len(cols))
					for j, col := range cols {
						vals[j] = r[col]
					}
					res[i] = vals
				}
				return res, nil
			}
			rows, scope = out, next
		}
	}
	return nil, semantic("a query must end with RETURN")
}

// --- MATCH ------------------------------------------------------------------

// match runs one MATCH or OPTIONAL MATCH against the incoming rows.
//
// The clause's pattern is compiled to one SQL statement, constrained by the
// ids that variables bound earlier actually hold, and its candidate bindings
// are hash-joined onto the incoming rows in Go. OPTIONAL MATCH keeps an
// incoming row with nulls for the new variables when nothing — after WHERE —
// matched it, which is why WHERE runs inside this function rather than as a
// filter after it.
func (x *executor) match(c *MatchClause, in []row, scope map[string]varKind) ([]row, error) {
	// The variables of this pattern that earlier clauses bound, and the ids
	// they hold.
	var boundVars []string
	boundIDs := map[string][]string{}
	seenVar := map[string]bool{}
	var patternVars []string
	note := func(v string, isRel bool) error {
		if v == "" || seenVar[v] {
			return nil
		}
		seenVar[v] = true
		if _, ok := scope[v]; !ok {
			patternVars = append(patternVars, v)
			return nil
		}
		boundVars = append(boundVars, v)
		set := map[string]bool{}
		var ids []string
		for _, r := range in {
			val := r[v]
			switch t := val.(type) {
			case nil:
			case *Node:
				if isRel {
					return runtimeErr("variable %s holds a node but is used as a relationship", v)
				}
				if !set[t.ID] {
					set[t.ID] = true
					ids = append(ids, t.ID)
				}
			case *Rel:
				if !isRel {
					return runtimeErr("variable %s holds a relationship but is used as a node", v)
				}
				if !set[t.ID] {
					set[t.ID] = true
					ids = append(ids, t.ID)
				}
			default:
				return runtimeErr("variable %s holds a %s, which cannot be matched as a pattern element", v, typeName(val))
			}
		}
		if len(ids) > idInListLimit {
			ids = nil // unconstrained in SQL; the Go join still enforces it
		} else if ids == nil {
			ids = []string{}
		}
		boundIDs[v] = ids
		return nil
	}
	for _, pp := range c.Patterns {
		for i, n := range pp.Nodes {
			if err := note(n.Var, false); err != nil {
				return nil, err
			}
			if i > 0 {
				if err := note(pp.Rels[i-1].Var, true); err != nil {
					return nil, err
				}
			}
		}
		if pp.PathVar != "" {
			patternVars = append(patternVars, pp.PathVar)
		}
	}

	// A bound variable that is null in every incoming row can match nothing.
	var candidates []row
	var defKeys []string
	var defMaps []*MapLit
	skipSQL := false
	for _, v := range boundVars {
		if ids := boundIDs[v]; ids != nil && len(ids) == 0 {
			skipSQL = true
		}
	}
	// *2..1 is an empty interval: openCypher matches nothing.
	for _, pp := range c.Patterns {
		for _, rp := range pp.Rels {
			if rp.VarLen && rp.Max >= 0 && rp.Max < rp.Min {
				skipSQL = true
			}
		}
	}
	if !skipSQL && len(in) > 0 {
		plan, err := x.src.compile(c, x.ev.params, boundIDs, scope)
		if err != nil {
			return nil, err
		}
		candidates, err = x.runPlan(c, plan)
		if err != nil {
			return nil, err
		}
		defKeys, defMaps = deferredMaps(plan)
	}

	// Hash the candidates on the bound variables' ids.
	key := func(r row) (string, bool) {
		var b strings.Builder
		for _, v := range boundVars {
			switch t := r[v].(type) {
			case *Node:
				b.WriteString(t.ID)
			case *Rel:
				b.WriteString(t.ID)
			default:
				return "", false
			}
			b.WriteByte(0)
		}
		return b.String(), true
	}
	index := map[string][]row{}
	for _, cand := range candidates {
		k, _ := key(cand)
		index[k] = append(index[k], cand)
	}

	var out []row
	for _, r := range in {
		matched := false
		if k, ok := key(r); ok {
			for _, cand := range index[k] {
				merged := make(row, len(r)+len(cand))
				for a, v := range r {
					merged[a] = v
				}
				for a, v := range cand {
					merged[a] = v
				}
				good := true
				for i, m := range defMaps {
					g, err := x.mapMatches(m, merged[defKeys[i]], merged)
					if err != nil {
						return nil, err
					}
					if !g {
						good = false
						break
					}
				}
				for a := range cand {
					if strings.HasPrefix(a, internalPrefix) {
						delete(merged, a)
					}
				}
				if !good {
					continue
				}
				if c.Where != nil {
					v, err := x.ev.eval(c.Where, merged)
					if err != nil {
						return nil, err
					}
					t, err := truth(v)
					if err != nil {
						return nil, err
					}
					if t != triTrue {
						continue
					}
				}
				matched = true
				out = append(out, merged)
				if err := x.budget(len(out)); err != nil {
					return nil, err
				}
			}
		}
		if !matched && c.Optional {
			merged := make(row, len(r)+len(patternVars))
			for a, v := range r {
				merged[a] = v
			}
			for _, v := range patternVars {
				merged[v] = nil
			}
			out = append(out, merged)
		}
	}
	return out, nil
}

// runPlan executes a compiled clause and turns its rows into candidate
// bindings, applying the exact checks SQL was not trusted with.
func (x *executor) runPlan(c *MatchClause, p *clausePlan) ([]row, error) {
	for _, chk := range p.unboundedChecks {
		x.stats.SQLStatements++
		rows, err := x.b.Query(x.ctx, chk.b.String(), chk.args...)
		if err != nil {
			return nil, err
		}
		hit := rows.Next()
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
		if hit {
			return nil, &Error{Kind: ErrBudget, Pos: -1, Msg: fmt.Sprintf("an unbounded variable-length relationship (*) reaches past the %d-hop cap here; give an explicit upper bound such as *1..%d", MaxVarLength, MaxVarLength)}
		}
	}

	x.stats.SQLStatements++
	rows, err := x.b.Query(x.ctx, p.sql.b.String(), p.sql.args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	type rawRel struct {
		fixed *Rel
		path  []string // edge ids for a variable-length relationship, in seed order
	}
	type rawRow struct {
		nodes []*Node
		rels  []rawRel
	}
	var raws []rawRow
	ncols := len(p.nodes)*4 + 0
	for _, r := range p.rels {
		if r.pat.VarLen {
			ncols++
		} else {
			ncols += 6
		}
	}
	dest := make([]any, ncols)
	strs := make([]sql.NullString, ncols)
	var weights = make([]sql.NullFloat64, len(p.rels))
	for rows.Next() {
		i := 0
		for range p.nodes {
			for k := 0; k < 4; k++ {
				dest[i] = &strs[i]
				i++
			}
		}
		for ri, r := range p.rels {
			if r.pat.VarLen {
				dest[i] = &strs[i]
				i++
				continue
			}
			for k := 0; k < 6; k++ {
				if k == 4 {
					dest[i] = &weights[ri]
				} else {
					dest[i] = &strs[i]
				}
				i++
			}
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		var rr rawRow
		i = 0
		for _, ne := range p.nodes {
			if ne.light {
				rr.nodes = append(rr.nodes, &Node{ID: strs[i].String})
				i += 4
				continue
			}
			n, err := x.internNode(strs[i].String, strs[i+1].String, strs[i+2].String, strs[i+3].String)
			if err != nil {
				return nil, err
			}
			rr.nodes = append(rr.nodes, n)
			i += 4
		}
		for ri, r := range p.rels {
			if r.pat.VarLen {
				rr.rels = append(rr.rels, rawRel{path: splitPath(strs[i].String)})
				i++
				continue
			}
			rel, err := x.internRel(strs[i].String, strs[i+1].String, strs[i+2].String, strs[i+3].String, weights[ri].Float64, strs[i+5].String)
			if err != nil {
				return nil, err
			}
			rr.rels = append(rr.rels, rawRel{fixed: rel})
			i += 6
		}
		raws = append(raws, rr)
		x.stats.CandidateRows++
		if err := x.budget(len(raws)); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()

	// Load the edges variable-length relationships walked, when anything
	// will look at them, and the nodes along named paths.
	needEdges := len(p.paths) > 0
	for _, r := range p.rels {
		if r.pat.VarLen && (r.varName != "" || (r.pat.Props != nil && len(r.pat.Props.Keys) > 0)) {
			needEdges = true
		}
	}
	if needEdges {
		var ids []string
		for _, rr := range raws {
			for _, rl := range rr.rels {
				ids = append(ids, rl.path...)
			}
		}
		if err := x.loadRels(ids); err != nil {
			return nil, err
		}
		// A named path needs the nodes along its trails too; load them in
		// one batch rather than one query per node as the path is built.
		if len(p.paths) > 0 {
			var nids []string
			for _, id := range ids {
				if e := x.rels[id]; e != nil {
					nids = append(nids, e.StartID, e.EndID)
				}
			}
			if err := x.loadNodes(nids); err != nil {
				return nil, err
			}
		}
	}

	var out []row
	for _, rr := range raws {
		cand := row{}
		ok := true
		// Exact checks for inline property maps on nodes.
		for ni, n := range p.nodes {
			for _, m := range n.props {
				if hasFreeVars(m) {
					continue // checked after the join, where the variables exist
				}
				good, err := x.mapMatches(m, rr.nodes[ni], nil)
				if err != nil {
					return nil, err
				}
				if !good {
					ok = false
				}
			}
			cand[elemKey(n.varName, n.alias)] = rr.nodes[ni]
		}
		if !ok {
			continue
		}
		used := map[string]bool{}
		relLists := make([][]*Rel, len(p.rels))
		for ri, r := range p.rels {
			rl := rr.rels[ri]
			if !r.pat.VarLen {
				if used[rl.fixed.ID] {
					ok = false
					break
				}
				used[rl.fixed.ID] = true
				if !hasFreeVars(r.pat.Props) {
					good, err := x.mapMatches(r.pat.Props, rl.fixed, nil)
					if err != nil {
						return nil, err
					}
					if !good {
						ok = false
						break
					}
				}
				relLists[ri] = []*Rel{rl.fixed}
				cand[elemKey(r.varName, r.alias)] = rl.fixed
				continue
			}
			ids := rl.path
			if !r.seedLeft {
				ids = reversed(ids)
			}
			list := make([]*Rel, 0, len(ids))
			for _, id := range ids {
				if used[id] {
					ok = false
					break
				}
				used[id] = true
				if needEdges {
					e := x.rels[id]
					if e == nil {
						return nil, runtimeErr("edge %q vanished during the query", id)
					}
					if !hasFreeVars(r.pat.Props) {
						good, err := x.mapMatches(r.pat.Props, e, nil)
						if err != nil {
							return nil, err
						}
						if !good {
							ok = false
							break
						}
					}
					list = append(list, e)
				}
			}
			if !ok {
				break
			}
			relLists[ri] = list
			vals := make([]any, len(list))
			for i, e := range list {
				vals[i] = e
			}
			cand[elemKey(r.varName, r.alias)] = vals
		}
		if !ok {
			continue
		}
		for _, pe := range p.paths {
			path, err := x.buildPath(pe, p, rr.nodes, relLists)
			if err != nil {
				return nil, err
			}
			cand[pe.varName] = path
		}
		out = append(out, cand)
	}
	// Deterministic order regardless of backend: by the matched elements'
	// ids, in the order the pattern names them.
	sortKeys := make([]string, len(out))
	for i, r := range out {
		sortKeys[i] = candidateKey(r, p)
	}
	idx := make([]int, len(out))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return sortKeys[idx[a]] < sortKeys[idx[b]] })
	sorted := make([]row, len(out))
	for i, j := range idx {
		sorted[i] = out[j]
	}
	_ = c
	return sorted, nil
}

func candidateKey(r row, p *clausePlan) string {
	var b strings.Builder
	for _, n := range p.nodes {
		if n.varName != "" {
			if v, ok := r[n.varName].(*Node); ok {
				b.WriteString(v.ID)
			}
		}
		b.WriteByte(0)
	}
	for _, rl := range p.rels {
		if rl.varName == "" {
			b.WriteByte(0)
			continue
		}
		switch v := r[rl.varName].(type) {
		case *Rel:
			b.WriteString(v.ID)
		case []any:
			for _, e := range v {
				b.WriteString(e.(*Rel).ID)
				b.WriteByte(1)
			}
		}
		b.WriteByte(0)
	}
	for _, pe := range p.paths {
		if v, ok := r[pe.varName].(*Path); ok {
			for _, e := range v.Rels {
				b.WriteString(e.ID)
				b.WriteByte(1)
			}
		}
		b.WriteByte(0)
	}
	return b.String()
}

func reversed(s []string) []string {
	out := make([]string, len(s))
	for i := range s {
		out[len(s)-1-i] = s[i]
	}
	return out
}

func splitPath(p string) []string {
	p = strings.Trim(p, "\x1f")
	if p == "" {
		return nil
	}
	return strings.Split(p, "\x1f")
}

func (x *executor) buildPath(pe *pathEl, p *clausePlan, nodes []*Node, relLists [][]*Rel) (*Path, error) {
	path := &Path{Nodes: []*Node{nodes[pe.nodes[0]]}}
	for i, ri := range pe.rels {
		cur := path.Nodes[len(path.Nodes)-1]
		for _, e := range relLists[ri] {
			path.Rels = append(path.Rels, e)
			nextID := e.EndID
			if e.EndID == cur.ID && e.StartID != cur.ID {
				nextID = e.StartID
			} else if e.StartID != cur.ID && e.EndID != cur.ID {
				return nil, runtimeErr("path does not connect at %s", cur.ID)
			}
			n, err := x.nodeByID(nextID)
			if err != nil {
				return nil, err
			}
			path.Nodes = append(path.Nodes, n)
			cur = n
		}
		// The pattern's own node at this position is authoritative (it
		// matters for a zero-length hop).
		path.Nodes[len(path.Nodes)-1] = nodes[pe.nodes[i+1]]
	}
	return path, nil
}

// elemKey is where a pattern element's binding lives in a candidate row: its
// variable, or for an anonymous element a key no query can spell, which
// match() removes once the deferred property checks have used it.
func elemKey(varName, alias string) string {
	if varName != "" {
		return varName
	}
	return internalPrefix + alias
}

const internalPrefix = "\x00"

func hasFreeVars(m *MapLit) bool {
	if m == nil {
		return false
	}
	free := map[string]bool{}
	freeVars(m, free)
	return len(free) > 0
}

// deferredMaps lists the inline property maps that read variables bound
// by earlier clauses, with where their targets live in a joined row.
func deferredMaps(p *clausePlan) (keys []string, maps []*MapLit) {
	for _, n := range p.nodes {
		for _, m := range n.props {
			if hasFreeVars(m) {
				keys = append(keys, elemKey(n.varName, n.alias))
				maps = append(maps, m)
			}
		}
	}
	for _, r := range p.rels {
		if hasFreeVars(r.pat.Props) {
			keys = append(keys, elemKey(r.varName, r.alias))
			maps = append(maps, r.pat.Props)
		}
	}
	return keys, maps
}

// mapMatches is the exact check for an inline property map, evaluated in
// env (nil when the map reads no variables). A variable-length relationship
// matches when every relationship on it does.
func (x *executor) mapMatches(m *MapLit, target any, env row) (bool, error) {
	if m == nil {
		return true, nil
	}
	if env == nil {
		env = row{}
	}
	if list, ok := target.([]any); ok {
		for _, e := range list {
			good, err := x.mapMatches(m, e, env)
			if err != nil || !good {
				return good, err
			}
		}
		return true, nil
	}
	for i, k := range m.Keys {
		want, err := x.ev.eval(m.Values[i], env)
		if err != nil {
			return false, err
		}
		have, err := propertyOf(target, k)
		if err != nil {
			return false, err
		}
		if equals(have, want) != triTrue {
			return false, nil
		}
	}
	return true, nil
}

func (x *executor) internNode(id, typ, props, content string) (*Node, error) {
	if n := x.nodes[id]; n != nil {
		return n, nil
	}
	n := &Node{ID: id, Content: content, raw: props, Labels: []string{}}
	if typ != "" {
		n.Labels = []string{typ}
	}
	x.nodes[id] = n
	return n, nil
}

func (x *executor) internRel(id, from, to, typ string, weight float64, props string) (*Rel, error) {
	if r := x.rels[id]; r != nil {
		return r, nil
	}
	if x.src.kind == sqldialect.Postgres {
		// graph_edges.weight is REAL, which PostgreSQL stores in single
		// precision: a weight written as 0.9 reads back as 0.8999999761…
		// Reading it at the precision it was stored with gives back 0.9 —
		// what was written, and what SQLite's double-precision REAL returns.
		if f, err := strconv.ParseFloat(strconv.FormatFloat(weight, 'g', -1, 32), 64); err == nil {
			weight = f
		}
	}
	r := &Rel{ID: id, StartID: from, EndID: to, Type: typ, Weight: weight, raw: props}
	x.rels[id] = r
	return r, nil
}

func (x *executor) nodeByID(id string) (*Node, error) {
	if n := x.nodes[id]; n != nil {
		return n, nil
	}
	if err := x.loadNodes([]string{id}); err != nil {
		return nil, err
	}
	if n := x.nodes[id]; n != nil {
		return n, nil
	}
	return nil, nil
}

func (x *executor) loadNodes(ids []string) error {
	var missing []string
	seen := map[string]bool{}
	for _, id := range ids {
		if x.nodes[id] == nil && !seen[id] {
			seen[id] = true
			missing = append(missing, id)
		}
	}
	for len(missing) > 0 {
		chunk := missing[:min(len(missing), 500)]
		missing = missing[len(chunk):]
		var s sqlBuilder
		s.w("SELECT n.id, n.node_type, n.properties, ")
		if x.src.needContent {
			s.w("n.content")
		} else {
			s.w("NULL")
		}
		s.w(" FROM ")
		x.src.nodeFrom(&s, "n")
		s.w(" WHERE n.id IN ")
		s.argList(chunk)
		x.stats.SQLStatements++
		rows, err := x.b.Query(x.ctx, s.b.String(), s.args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, typ, props, content sql.NullString
			if err := rows.Scan(&id, &typ, &props, &content); err != nil {
				_ = rows.Close()
				return err
			}
			if _, err := x.internNode(id.String, typ.String, props.String, content.String); err != nil {
				_ = rows.Close()
				return err
			}
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (x *executor) loadRels(ids []string) error {
	var missing []string
	seen := map[string]bool{}
	for _, id := range ids {
		if x.rels[id] == nil && !seen[id] {
			seen[id] = true
			missing = append(missing, id)
		}
	}
	for len(missing) > 0 {
		chunk := missing[:min(len(missing), 500)]
		missing = missing[len(chunk):]
		var s sqlBuilder
		s.w("SELECT e.id, e.from_node_id, e.to_node_id, COALESCE(e.edge_type, ''), COALESCE(e.weight, 0), e.properties FROM ")
		x.src.edgeFrom(&s, "e")
		s.w(" WHERE e.id IN ")
		s.argList(chunk)
		x.stats.SQLStatements++
		rows, err := x.b.Query(x.ctx, s.b.String(), s.args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, from, to, typ, props sql.NullString
			var w sql.NullFloat64
			if err := rows.Scan(&id, &from, &to, &typ, &w, &props); err != nil {
				_ = rows.Close()
				return err
			}
			if _, err := x.internRel(id.String, from.String, to.String, typ.String, w.Float64, props.String); err != nil {
				_ = rows.Close()
				return err
			}
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
