package cypher

import (
	"fmt"
	"sort"
)

// varKind is what a variable is known to hold at plan time.
type varKind int

const (
	kValue varKind = iota // anything; checked at run time where it matters
	kNode
	kRel
	kRelList // a variable-length relationship
	kPath
	kList // a value known at plan time to be a list
)

func (k varKind) String() string {
	switch k {
	case kNode:
		return "node"
	case kRel:
		return "relationship"
	case kRelList:
		return "variable-length relationship"
	case kPath:
		return "path"
	case kList:
		return "list"
	}
	return "value"
}

// MaxVarLength is the hard cap on a variable-length relationship's upper
// bound. Six hops covers every realistic question over a knowledge graph —
// "how is A related to B" past six steps is answered by everything — and the
// number of trails grows roughly as degree^length, so the cap is what keeps
// one careless `*` from turning into a statement that never finishes.
const MaxVarLength = 6

// check validates a parsed query against the subset's static rules: every
// variable is defined before use, kinds agree, projections are aliased where
// openCypher requires it, aggregates appear only where they can, and
// variable-length bounds respect the cap. It returns the column names.
func check(q *Query) ([]string, error) {
	for i := 1; i < len(q.UnionAll); i++ {
		if q.UnionAll[i] != q.UnionAll[0] {
			return nil, semantic("UNION and UNION ALL cannot be mixed in one query")
		}
	}
	var cols []string
	for i, part := range q.Parts {
		c, err := checkSingle(part)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			cols = c
			continue
		}
		if len(c) != len(cols) {
			return nil, semantic("all parts of a UNION must return the same columns: %v vs %v", cols, c)
		}
		for j := range c {
			if c[j] != cols[j] {
				return nil, semantic("all parts of a UNION must return the same columns: %v vs %v", cols, c)
			}
		}
	}
	return cols, nil
}

func checkSingle(sq *SingleQuery) ([]string, error) {
	scope := map[string]varKind{}
	for ci, cl := range sq.Clauses {
		last := ci == len(sq.Clauses)-1
		switch c := cl.(type) {
		case *MatchClause:
			if err := checkMatch(c, scope); err != nil {
				return nil, err
			}
			if last {
				return nil, semantic("a query cannot end with MATCH; add a RETURN")
			}
		case *UnwindClause:
			if err := checkExpr(c.Expr, scope, false); err != nil {
				return nil, err
			}
			if _, dup := scope[c.Var]; dup {
				return nil, semantic("variable %s is already defined", c.Var)
			}
			scope[c.Var] = kValue
			if last {
				return nil, semantic("a query cannot end with UNWIND; add a RETURN")
			}
		case *ProjectionClause:
			if c.IsReturn && !last {
				return nil, semantic("RETURN must be the last clause")
			}
			next, err := checkProjection(c, scope)
			if err != nil {
				return nil, err
			}
			if c.IsReturn {
				cols := make([]string, len(c.Items))
				for i, it := range c.Items {
					cols[i] = it.Alias
				}
				if c.Star {
					cols = append(starColumns(scope), cols...)
				}
				return cols, nil
			}
			scope = next
		}
	}
	return nil, semantic("a query must end with RETURN")
}

// starColumns are the variables * expands to, in openCypher's order: by name.
func starColumns(scope map[string]varKind) []string {
	out := make([]string, 0, len(scope))
	for k := range scope {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func checkMatch(c *MatchClause, scope map[string]varKind) error {
	local := map[string]varKind{}
	define := func(name string, k varKind) error {
		if name == "" {
			return nil
		}
		if prev, ok := scope[name]; ok {
			switch {
			case k == kRelList || prev == kRelList:
				return unsupported(c.Pos, "reusing variable-length relationship variable %s across clauses is not supported", name)
			case k == kPath || prev == kPath:
				return semantic("variable %s is already defined", name)
			case prev == kList:
				return semantic("variable %s is a list and cannot be used as a %s", name, k)
			case prev == kValue:
				// A projected value used as a node or relationship; checked at
				// run time against what it actually holds.
			case prev != k:
				return semantic("variable %s is a %s, not a %s", name, prev, k)
			}
			return nil
		}
		if prev, ok := local[name]; ok {
			if prev != k || k == kRelList || k == kPath {
				if k == kRel && prev == kRel {
					return semantic("relationship variable %s is used twice in one MATCH; a relationship can be matched only once per pattern", name)
				}
				return semantic("variable %s is used as both a %s and a %s", name, prev, k)
			}
			if k == kRel {
				return semantic("relationship variable %s is used twice in one MATCH; a relationship can be matched only once per pattern", name)
			}
			return nil
		}
		local[name] = k
		return nil
	}
	for _, pp := range c.Patterns {
		for _, n := range pp.Nodes {
			if err := define(n.Var, kNode); err != nil {
				return err
			}
		}
		for _, r := range pp.Rels {
			k := kRel
			if r.VarLen {
				k = kRelList
				if r.Max > MaxVarLength || r.Min > MaxVarLength {
					return unsupported(r.Pos, "variable-length bound *%d..%d exceeds the hard cap of %d hops", r.Min, r.Max, MaxVarLength)
				}
			}
			if err := define(r.Var, k); err != nil {
				return err
			}
		}
		if pp.PathVar != "" {
			if err := define(pp.PathVar, kPath); err != nil {
				return err
			}
		}
	}
	// Property maps may only read variables bound before this clause.
	for _, pp := range c.Patterns {
		for _, n := range pp.Nodes {
			if n.Props != nil {
				if err := checkExpr(n.Props, scope, false); err != nil {
					return err
				}
			}
		}
		for _, r := range pp.Rels {
			if r.Props != nil {
				if err := checkExpr(r.Props, scope, false); err != nil {
					return err
				}
			}
		}
	}
	for k, v := range local {
		scope[k] = v
	}
	if c.Where != nil {
		if err := checkExpr(c.Where, scope, false); err != nil {
			return err
		}
		if err := checkPredicate(c.Where, scope); err != nil {
			return err
		}
	}
	return nil
}

// checkExpr verifies variables are defined and aggregates are allowed.
func checkExpr(e Expr, scope map[string]varKind, aggOK bool) error {
	free := map[string]bool{}
	freeVars(e, free)
	for v := range free {
		if _, ok := scope[v]; !ok {
			return semantic("variable %s is not defined", v)
		}
	}
	var err error
	walkExpr(e, func(x Expr) bool {
		if err != nil {
			return false
		}
		switch v := x.(type) {
		case *ListComp:
			for _, sub := range []Expr{v.Pred, v.Proj} {
				if sub != nil && containsAggregate(sub) {
					err = semantic("aggregates are not allowed inside a list comprehension")
					return false
				}
			}
		case *Quantifier:
			if containsAggregate(v.Pred) {
				err = semantic("aggregates are not allowed inside %s()", v.Kind)
				return false
			}
		case *PropAccess:
			if t, ok := v.Target.(*Variable); ok && scope[t.Name] == kPath {
				err = semantic("a path has no properties: %s.%s", t.Name, v.Key)
				return false
			}
		}
		if f, ok := x.(*FuncCall); ok {
			if e := checkArgKinds(f, scope); e != nil {
				err = e
				return false
			}
			if aggregateNames[f.Name] {
				if !aggOK {
					err = semantic("aggregate %s() is not allowed here; aggregate in WITH or RETURN", f.Name)
					return false
				}
				for _, a := range f.Args {
					if containsAggregate(a) {
						err = semantic("aggregates cannot be nested: %s(...)", f.Name)
						return false
					}
				}
				if f.Star && f.Name != "count" {
					err = semantic("only count(*) takes *")
					return false
				}
				if !f.Star && len(f.Args) != 1 {
					err = semantic("%s() takes exactly one argument", f.Name)
					return false
				}
			} else {
				ar := functionArity[f.Name]
				if len(f.Args) < ar[0] || (ar[1] >= 0 && len(f.Args) > ar[1]) {
					err = semantic("%s() takes %s arguments, got %d", f.Name, arityText(ar), len(f.Args))
					return false
				}
			}
		}
		return true
	})
	return err
}

func arityText(ar [2]int) string {
	switch {
	case ar[1] < 0:
		return fmt.Sprintf("at least %d", ar[0])
	case ar[0] == ar[1]:
		return fmt.Sprintf("%d", ar[0])
	}
	return fmt.Sprintf("%d to %d", ar[0], ar[1])
}

// checkProjection validates WITH/RETURN and returns the scope after it.
func checkProjection(c *ProjectionClause, scope map[string]varKind) (map[string]varKind, error) {
	next := map[string]varKind{}
	if c.Star {
		if len(scope) == 0 {
			return nil, semantic("RETURN * / WITH * with no variables in scope")
		}
		for k, v := range scope {
			next[k] = v
		}
	}
	hasAgg := false
	for _, it := range c.Items {
		if err := checkExpr(it.Expr, scope, true); err != nil {
			return nil, err
		}
		if containsAggregate(it.Expr) {
			hasAgg = true
		}
		if !c.IsReturn && !it.Explicit {
			if _, ok := it.Expr.(*Variable); !ok {
				return nil, semantic("expression %q in WITH must be aliased with AS", it.Alias)
			}
		}
		if _, dup := next[it.Alias]; dup && (!c.Star || scope[it.Alias] != kindOf(it.Expr, scope)) {
			return nil, semantic("column %s is projected twice", it.Alias)
		}
		next[it.Alias] = kindOf(it.Expr, scope)
	}
	if hasAgg {
		if err := checkGrouping(c, scope); err != nil {
			return nil, err
		}
	}
	// ORDER BY sees the projected names, plus the incoming variables when
	// the projection neither aggregates nor de-duplicates.
	orderScope := next
	if !hasAgg && !c.Distinct {
		orderScope = map[string]varKind{}
		for k, v := range scope {
			orderScope[k] = v
		}
		for k, v := range next {
			orderScope[k] = v
		}
	}
	for _, s := range c.OrderBy {
		if hasAgg && containsAggregate(s.Expr) {
			keys := map[string]bool{}
			keyExprs := map[string]bool{}
			for _, it := range c.Items {
				keys[it.Alias] = true
				if containsAggregate(it.Expr) {
					continue
				}
				switch x := it.Expr.(type) {
				case *Variable:
					keyExprs[exprString(x)] = true
				case *PropAccess:
					if _, ok := x.Target.(*Variable); ok {
						keyExprs[exprString(x)] = true
					}
				}
			}
			if bad := ungroupedVar(s.Expr, keys, keyExprs, map[string]bool{}); bad != "" {
				return nil, semantic("ORDER BY mixes an aggregate with %s, which is not a projected column or a projected property of one", bad)
			}
		}
		e := rewriteToAlias(s.Expr, c.Items)
		if err := checkExpr(e, orderScope, false); err != nil {
			if hasAgg || c.Distinct {
				return nil, semantic("ORDER BY after an aggregating or DISTINCT projection may only use projected columns: %v", err.(*Error).Msg)
			}
			return nil, err
		}
	}
	for _, e := range []Expr{c.Skip, c.Limit} {
		if e == nil {
			continue
		}
		free := map[string]bool{}
		freeVars(e, free)
		if len(free) > 0 {
			return nil, semantic("SKIP and LIMIT must be constants or parameters")
		}
		if containsAggregate(e) {
			return nil, semantic("SKIP and LIMIT cannot aggregate")
		}
	}
	if c.Where != nil {
		whereScope := next
		if !hasAgg {
			// A non-aggregating WITH's WHERE can also read what came in.
			whereScope = orderScope
			if c.Distinct {
				whereScope = map[string]varKind{}
				for k, v := range scope {
					whereScope[k] = v
				}
				for k, v := range next {
					whereScope[k] = v
				}
			}
		}
		if err := checkExpr(c.Where, whereScope, false); err != nil {
			return nil, err
		}
		if err := checkPredicate(c.Where, whereScope); err != nil {
			return nil, err
		}
		if !hasAgg && (c.Skip != nil || c.Limit != nil) && readsHidden(c.Where, next) {
			return nil, &Error{Kind: ErrUnsupported, Pos: -1, Msg: "WITH ... SKIP/LIMIT ... WHERE that reads a variable the WITH does not project is ambiguous and not supported; project the variable or filter before the WITH" + supportedHint}
		}
	}
	return next, nil
}

func kindOf(e Expr, scope map[string]varKind) varKind {
	switch x := e.(type) {
	case *Variable:
		return scope[x.Name]
	case *ListLit, *ListComp:
		return kList
	case *FuncCall:
		switch x.Name {
		case "collect", "nodes", "relationships", "rels", "labels", "keys", "range", "split", "tail", "reverse":
			if x.Name == "reverse" {
				return kValue
			}
			return kList
		}
	}
	return kValue
}

// checkGrouping refuses an aggregating item whose non-aggregate parts read a
// variable that is not itself a grouping key: the value would come from an
// arbitrary row of the group, which openCypher rejects and which would be an
// answer nobody asked for.
func checkGrouping(c *ProjectionClause, scope map[string]varKind) error {
	keys := map[string]bool{}
	keyExprs := map[string]bool{}
	for _, it := range c.Items {
		if containsAggregate(it.Expr) {
			continue
		}
		// Only a key that is a variable or a property of one may be reused
		// inside an aggregating expression; openCypher calls anything more
		// complex ambiguous even when it is projected.
		switch x := it.Expr.(type) {
		case *Variable:
			keys[x.Name] = true
			keyExprs[exprString(x)] = true
		case *PropAccess:
			if _, ok := x.Target.(*Variable); ok {
				keyExprs[exprString(x)] = true
			}
		}
	}
	if c.Star {
		for k := range scope {
			keys[k] = true
		}
	}
	for _, it := range c.Items {
		if !containsAggregate(it.Expr) {
			continue
		}
		if bad := ungroupedVar(it.Expr, keys, keyExprs, map[string]bool{}); bad != "" {
			return semantic("%s mixes an aggregate with variable %s, which is not a grouping key; project %s (or its property) as its own column", it.Alias, bad, bad)
		}
	}
	return nil
}

// ungroupedVar finds a variable read outside any aggregate that is neither a
// grouping key nor bound by an enclosing comprehension.
func ungroupedVar(e Expr, keys, keyExprs, bound map[string]bool) string {
	bad := ""
	walkExpr(e, func(x Expr) bool {
		if bad != "" {
			return false
		}
		switch v := x.(type) {
		case *FuncCall:
			if aggregateNames[v.Name] {
				return false
			}
		case *Variable:
			if !bound[v.Name] && !keys[v.Name] {
				bad = v.Name
			}
			return false
		case *ListComp:
			if b := ungroupedVar(v.List, keys, keyExprs, bound); b != "" {
				bad = b
				return false
			}
			inner := copyBound(bound, v.Var)
			for _, sub := range []Expr{v.Pred, v.Proj} {
				if sub == nil {
					continue
				}
				if b := ungroupedVar(sub, keys, keyExprs, inner); b != "" {
					bad = b
					return false
				}
			}
			return false
		case *Quantifier:
			if b := ungroupedVar(v.List, keys, keyExprs, bound); b != "" {
				bad = b
				return false
			}
			if b := ungroupedVar(v.Pred, keys, keyExprs, copyBound(bound, v.Var)); b != "" {
				bad = b
			}
			return false
		}
		if keyExprs[exprString(x)] {
			return false
		}
		return true
	})
	return bad
}

// rewriteToAlias replaces any sub-expression of an ORDER BY item that is
// textually one of the projection's expressions with that column — what lets
// `RETURN n.x, count(*) ORDER BY count(*)` sort by the computed column.
func rewriteToAlias(e Expr, items []*ProjectionItem) Expr {
	for _, it := range items {
		if exprString(it.Expr) == exprString(e) {
			return &Variable{Name: it.Alias}
		}
	}
	switch x := e.(type) {
	case *Binary:
		return &Binary{Op: x.Op, L: rewriteToAlias(x.L, items), R: rewriteToAlias(x.R, items)}
	case *Unary:
		return &Unary{Op: x.Op, X: rewriteToAlias(x.X, items)}
	case *FuncCall:
		if aggregateNames[x.Name] {
			return x
		}
		args := make([]Expr, len(x.Args))
		for i, a := range x.Args {
			args[i] = rewriteToAlias(a, items)
		}
		return &FuncCall{Name: x.Name, Distinct: x.Distinct, Star: x.Star, Args: args}
	case *PropAccess:
		return &PropAccess{Target: rewriteToAlias(x.Target, items), Key: x.Key}
	}
	return e
}

// exprString is a canonical rendering used only to compare expressions.
func exprString(e Expr) string {
	switch x := e.(type) {
	case nil:
		return "<nil>"
	case *Literal:
		return fmt.Sprintf("lit(%s:%s)", typeName(x.Value), groupKey(x.Value))
	case *Param:
		return "$" + x.Name
	case *Variable:
		return "var(" + x.Name + ")"
	case *ListLit:
		s := "["
		for _, it := range x.Items {
			s += exprString(it) + ","
		}
		return s + "]"
	case *MapLit:
		s := "{"
		for i, k := range x.Keys {
			s += k + ":" + exprString(x.Values[i]) + ","
		}
		return s + "}"
	case *PropAccess:
		return exprString(x.Target) + "." + x.Key
	case *Index:
		return exprString(x.Target) + "[" + exprString(x.Idx) + "]"
	case *Slice:
		return exprString(x.Target) + "[" + exprString(x.From) + ".." + exprString(x.To) + "]"
	case *Unary:
		return x.Op + "(" + exprString(x.X) + ")"
	case *Binary:
		return "(" + exprString(x.L) + " " + x.Op + " " + exprString(x.R) + ")"
	case *IsNull:
		return fmt.Sprintf("isnull(%s,%v)", exprString(x.X), x.Not)
	case *LabelCheck:
		return fmt.Sprintf("labels(%s,%v,%v)", exprString(x.X), x.Labels, x.Not)
	case *FuncCall:
		s := x.Name + "("
		if x.Distinct {
			s += "distinct "
		}
		if x.Star {
			s += "*"
		}
		for _, a := range x.Args {
			s += exprString(a) + ","
		}
		return s + ")"
	case *CaseExpr:
		s := "case(" + exprString(x.Test)
		for i := range x.Whens {
			s += ";" + exprString(x.Whens[i]) + "->" + exprString(x.Thens[i])
		}
		return s + ";" + exprString(x.Else) + ")"
	case *ListComp:
		return fmt.Sprintf("comp(%s,%s,%s,%s)", x.Var, exprString(x.List), exprString(x.Pred), exprString(x.Proj))
	case *Quantifier:
		return fmt.Sprintf("%s(%s,%s,%s)", x.Kind, x.Var, exprString(x.List), exprString(x.Pred))
	}
	return fmt.Sprintf("%T", e)
}

// needsContent reports whether anything in the query can observe a node's
// content column: reading n.content, or using a node as a whole value (in a
// projection, a list, a comparison other than identity). Most graph
// questions read names and types, and not fetching the content of every
// chunk node they pass over is most of what keeps a scan fast.
func needsContent(q *Query) bool {
	heavy := false
	var visit func(e Expr)
	visit = func(e Expr) {
		if e == nil || heavy {
			return
		}
		switch x := e.(type) {
		case *Variable:
			heavy = true
		case *PropAccess:
			if _, ok := x.Target.(*Variable); ok {
				if x.Key == "content" {
					heavy = true
				}
				return
			}
			visit(x.Target)
		case *LabelCheck:
			if _, ok := x.X.(*Variable); ok {
				return
			}
			visit(x.X)
		case *IsNull:
			if _, ok := x.X.(*Variable); ok {
				return
			}
			visit(x.X)
		case *FuncCall:
			switch x.Name {
			case "id", "elementid", "labels", "type", "keys", "count", "exists", "length", "size":
				for _, a := range x.Args {
					if _, ok := a.(*Variable); ok {
						continue
					}
					visit(a)
				}
				return
			}
			for _, a := range x.Args {
				visit(a)
			}
		default:
			walkChildren(e, visit)
		}
	}
	for _, part := range q.Parts {
		for _, cl := range part.Clauses {
			switch c := cl.(type) {
			case *MatchClause:
				visit(c.Where)
				for _, pp := range c.Patterns {
					for _, n := range pp.Nodes {
						if n.Props != nil {
							visit(n.Props)
						}
					}
				}
			case *UnwindClause:
				visit(c.Expr)
			case *ProjectionClause:
				if c.Star {
					return true
				}
				for _, it := range c.Items {
					visit(it.Expr)
				}
				for _, s := range c.OrderBy {
					visit(s.Expr)
				}
				visit(c.Where)
			}
		}
	}
	return heavy
}

// walkChildren calls visit on each direct child of e.
func walkChildren(e Expr, visit func(Expr)) {
	first := true
	walkExpr(e, func(x Expr) bool {
		if first {
			first = false
			return true
		}
		visit(x)
		return false
	})
}

// checkArgKinds refuses, at plan time, a function applied to a variable
// whose kind it can never accept. At run time these are type errors; caught
// here they are errors even when no row reaches them, which is when a
// mistaken query is most likely to be read as a real empty answer.
func checkArgKinds(f *FuncCall, scope map[string]varKind) error {
	if len(f.Args) != 1 {
		return nil
	}
	v, ok := f.Args[0].(*Variable)
	if !ok {
		return nil
	}
	k, known := scope[v.Name]
	if !known || k == kValue {
		return nil
	}
	bad := false
	switch f.Name {
	case "type", "startnode", "endnode":
		bad = k != kRel
	case "labels":
		bad = k != kNode
	case "length":
		bad = k == kNode || k == kRel
	case "size":
		bad = k == kNode || k == kRel || k == kPath
	case "nodes":
		bad = k != kPath
	case "relationships", "rels":
		bad = k != kPath && k != kRelList
	}
	if bad {
		return semantic("%s() cannot take %s, which is a %s", f.Name, v.Name, k)
	}
	return nil
}

// checkPredicate refuses a WHERE that is a bare node, relationship or path:
// none of them is a truth value.
func checkPredicate(e Expr, scope map[string]varKind) error {
	if v, ok := e.(*Variable); ok {
		switch scope[v.Name] {
		case kNode, kRel, kRelList, kPath, kList:
			return semantic("WHERE %s: a %s is not a predicate", v.Name, scope[v.Name])
		}
	}
	return nil
}

// readsHidden reports whether e reads a variable that is not in the
// projection's output.
func readsHidden(e Expr, projected map[string]varKind) bool {
	free := map[string]bool{}
	freeVars(e, free)
	for v := range free {
		if _, ok := projected[v]; !ok {
			return true
		}
	}
	return false
}
