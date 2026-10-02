package cypher

import (
	"math"
	"sort"
)

// project runs WITH or RETURN: projection, grouping and aggregation,
// DISTINCT, ORDER BY, SKIP, LIMIT and (for WITH) WHERE, in that order.
func (x *executor) project(c *ProjectionClause, in []row, scope map[string]varKind) ([]string, []row, map[string]varKind, error) {
	items := c.Items
	if c.Star {
		var star []*ProjectionItem
		for _, name := range starColumns(scope) {
			star = append(star, &ProjectionItem{Expr: &Variable{Name: name}, Alias: name})
		}
		items = append(star, items...)
	}
	cols := make([]string, len(items))
	next := map[string]varKind{}
	for i, it := range items {
		cols[i] = it.Alias
		next[it.Alias] = kindOf(it.Expr, scope)
	}

	hasAgg := false
	for _, it := range items {
		if containsAggregate(it.Expr) {
			hasAgg = true
		}
	}

	var out []row
	var orig []row // the incoming row behind each output row, for ORDER BY
	if !hasAgg {
		for _, r := range in {
			o := make(row, len(items))
			for _, it := range items {
				v, err := x.ev.eval(it.Expr, r)
				if err != nil {
					return nil, nil, nil, err
				}
				o[it.Alias] = v
			}
			out = append(out, o)
			orig = append(orig, r)
		}
	} else {
		var err error
		out, err = x.aggregate(items, in)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	// A WHERE that reads variables the projection drops is applied per
	// incoming row, before DISTINCT; check() guarantees it is not combined
	// with SKIP or LIMIT, where the order of the two would be ambiguous.
	earlyWhere := c.Where != nil && !hasAgg && readsHidden(c.Where, next)
	if earlyWhere {
		var kOut, kOrig []row
		for i, o := range out {
			env := make(row, len(orig[i])+len(o))
			for k, v := range orig[i] {
				env[k] = v
			}
			for k, v := range o {
				env[k] = v
			}
			v, err := x.ev.eval(c.Where, env)
			if err != nil {
				return nil, nil, nil, err
			}
			t, err := truth(v)
			if err != nil {
				return nil, nil, nil, err
			}
			if t == triTrue {
				kOut = append(kOut, o)
				kOrig = append(kOrig, orig[i])
			}
		}
		out, orig = kOut, kOrig
	}

	if c.Distinct {
		seen := map[string]bool{}
		var dOut, dOrig []row
		for i, o := range out {
			vals := make([]any, len(cols))
			for j, col := range cols {
				vals[j] = o[col]
			}
			k := groupKey(vals...)
			if seen[k] {
				continue
			}
			seen[k] = true
			dOut = append(dOut, o)
			if orig != nil {
				dOrig = append(dOrig, orig[i])
			}
		}
		out, orig = dOut, dOrig
	}

	if len(c.OrderBy) > 0 {
		exprs := make([]Expr, len(c.OrderBy))
		for i, s := range c.OrderBy {
			exprs[i] = rewriteToAlias(s.Expr, items)
		}
		keys := make([][]any, len(out))
		for i, o := range out {
			env := o
			if !hasAgg && !c.Distinct {
				env = make(row, len(orig[i])+len(o))
				for k, v := range orig[i] {
					env[k] = v
				}
				for k, v := range o {
					env[k] = v
				}
			}
			ks := make([]any, len(exprs))
			for j, e := range exprs {
				v, err := x.ev.eval(e, env)
				if err != nil {
					return nil, nil, nil, err
				}
				ks[j] = v
			}
			keys[i] = ks
		}
		idx := make([]int, len(out))
		for i := range idx {
			idx[i] = i
		}
		sort.SliceStable(idx, func(a, b int) bool {
			ka, kb := keys[idx[a]], keys[idx[b]]
			for j, s := range c.OrderBy {
				cmp := orderCompare(ka[j], kb[j])
				if s.Desc {
					cmp = -cmp
				}
				if cmp != 0 {
					return cmp < 0
				}
			}
			return false
		})
		sorted := make([]row, len(out))
		for i, j := range idx {
			sorted[i] = out[j]
		}
		out = sorted
	}

	if c.Skip != nil {
		n, err := x.count(c.Skip, "SKIP")
		if err != nil {
			return nil, nil, nil, err
		}
		if n >= int64(len(out)) {
			out = nil
		} else {
			out = out[n:]
		}
	}
	if c.Limit != nil {
		n, err := x.count(c.Limit, "LIMIT")
		if err != nil {
			return nil, nil, nil, err
		}
		if n < int64(len(out)) {
			out = out[:n]
		}
	}

	if c.Where != nil && !earlyWhere {
		var kept []row
		for _, o := range out {
			v, err := x.ev.eval(c.Where, o)
			if err != nil {
				return nil, nil, nil, err
			}
			t, err := truth(v)
			if err != nil {
				return nil, nil, nil, err
			}
			if t == triTrue {
				kept = append(kept, o)
			}
		}
		out = kept
	}
	return cols, out, next, nil
}

func (x *executor) count(e Expr, what string) (int64, error) {
	v, err := x.ev.eval(e, row{})
	if err != nil {
		return 0, err
	}
	switch n := v.(type) {
	case int64:
		if n < 0 {
			return 0, semantic("%s must not be negative", what)
		}
		return n, nil
	case float64:
		return 0, semantic("%s must be an integer, got %v", what, n)
	}
	return 0, semantic("%s must be an integer, got a %s", what, typeName(v))
}

// aggregate groups rows by the non-aggregate items and evaluates the
// aggregate items once per group. Groups come out in the order their first
// row arrived, so the result is as deterministic as its input.
func (x *executor) aggregate(items []*ProjectionItem, in []row) ([]row, error) {
	var keyItems []*ProjectionItem
	var calls []*FuncCall
	for _, it := range items {
		if !containsAggregate(it.Expr) {
			keyItems = append(keyItems, it)
			continue
		}
		walkExpr(it.Expr, func(e Expr) bool {
			if f, ok := e.(*FuncCall); ok && aggregateNames[f.Name] {
				calls = append(calls, f)
				return false
			}
			return true
		})
	}
	type group struct {
		first row
		rows  []row
		keys  map[string]any
	}
	var order []string
	groups := map[string]*group{}
	for _, r := range in {
		vals := make([]any, len(keyItems))
		km := make(map[string]any, len(keyItems))
		for i, it := range keyItems {
			v, err := x.ev.eval(it.Expr, r)
			if err != nil {
				return nil, err
			}
			vals[i] = v
			km[it.Alias] = v
		}
		k := groupKey(vals...)
		g := groups[k]
		if g == nil {
			g = &group{first: r, keys: km}
			groups[k] = g
			order = append(order, k)
		}
		g.rows = append(g.rows, r)
	}
	// With no grouping keys, an empty input still yields one row: count(*)
	// of nothing is 0, not no answer.
	if len(keyItems) == 0 && len(in) == 0 {
		groups[""] = &group{first: row{}, keys: map[string]any{}}
		order = append(order, "")
	}

	out := make([]row, 0, len(order))
	for _, k := range order {
		g := groups[k]
		aggs := make(map[*FuncCall]any, len(calls))
		for _, f := range calls {
			v, err := x.aggValue(f, g.rows)
			if err != nil {
				return nil, err
			}
			aggs[f] = v
		}
		o := make(row, len(items))
		sub := &evaluator{params: x.ev.params, regexes: x.ev.regexes, nodeAt: x.ev.nodeAt, aggs: aggs, work: x.ev.work}
		for _, it := range items {
			if v, ok := g.keys[it.Alias]; ok && !containsAggregate(it.Expr) {
				o[it.Alias] = v
				continue
			}
			v, err := sub.eval(it.Expr, g.first)
			if err != nil {
				return nil, err
			}
			o[it.Alias] = v
		}
		out = append(out, o)
	}
	return out, nil
}

func (x *executor) aggValue(f *FuncCall, rows []row) (any, error) {
	if f.Star {
		return int64(len(rows)), nil
	}
	var vals []any
	seen := map[string]bool{}
	for _, r := range rows {
		v, err := x.ev.eval(f.Args[0], r)
		if err != nil {
			return nil, err
		}
		if v == nil {
			continue
		}
		if f.Distinct {
			k := groupKey(v)
			if seen[k] {
				continue
			}
			seen[k] = true
		}
		vals = append(vals, v)
	}
	switch f.Name {
	case "count":
		return int64(len(vals)), nil
	case "collect":
		if vals == nil {
			return []any{}, nil
		}
		return vals, nil
	case "min", "max":
		if len(vals) == 0 {
			return nil, nil
		}
		best := vals[0]
		for _, v := range vals[1:] {
			c := orderCompare(v, best)
			if (f.Name == "min" && c < 0) || (f.Name == "max" && c > 0) {
				best = v
			}
		}
		return best, nil
	case "sum":
		var isum int64
		var fsum float64
		isFloat := false
		for _, v := range vals {
			switch n := v.(type) {
			case int64:
				if isFloat {
					fsum += float64(n)
					continue
				}
				s := isum + n
				if (s > isum) != (n > 0) {
					return nil, runtimeErr("integer overflow in sum()")
				}
				isum = s
			case float64:
				if !isFloat {
					isFloat = true
					fsum = float64(isum)
				}
				fsum += n
			default:
				return nil, runtimeErr("sum() needs numbers, got a %s", typeName(v))
			}
		}
		if isFloat {
			return fsum, nil
		}
		return isum, nil
	case "avg":
		if len(vals) == 0 {
			return nil, nil
		}
		var s float64
		for _, v := range vals {
			if !isNumber(v) {
				return nil, runtimeErr("avg() needs numbers, got a %s", typeName(v))
			}
			s += toFloat(v)
		}
		r := s / float64(len(vals))
		if math.IsInf(r, 0) {
			return nil, runtimeErr("avg() overflowed")
		}
		return r, nil
	}
	return nil, runtimeErr("unknown aggregate %s", f.Name)
}
