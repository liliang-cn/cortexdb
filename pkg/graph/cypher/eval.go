package cypher

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// functionArity lists the scalar functions, as [min, max] argument counts
// (max -1 for variadic). Anything not here or in aggregateNames is refused at
// parse time by name.
var functionArity = map[string][2]int{
	"id": {1, 1}, "elementid": {1, 1}, "labels": {1, 1}, "type": {1, 1},
	"keys": {1, 1}, "properties": {1, 1}, "size": {1, 1}, "length": {1, 1},
	"nodes": {1, 1}, "relationships": {1, 1}, "rels": {1, 1},
	"startnode": {1, 1}, "endnode": {1, 1},
	"coalesce": {1, -1}, "head": {1, 1}, "last": {1, 1}, "tail": {1, 1},
	"reverse": {1, 1}, "range": {2, 3},
	"tolower": {1, 1}, "toupper": {1, 1}, "lower": {1, 1}, "upper": {1, 1},
	"trim": {1, 1}, "ltrim": {1, 1}, "rtrim": {1, 1}, "replace": {3, 3},
	"substring": {2, 3}, "left": {2, 2}, "right": {2, 2}, "split": {2, 2},
	"tostring": {1, 1}, "tointeger": {1, 1}, "tofloat": {1, 1}, "toboolean": {1, 1},
	"abs": {1, 1}, "ceil": {1, 1}, "floor": {1, 1}, "round": {1, 1}, "sign": {1, 1}, "sqrt": {1, 1},
	"exists": {1, 1}, "isempty": {1, 1}, "char_length": {1, 1}, "character_length": {1, 1},
}

// row is one binding of variables to values.
type row map[string]any

// evaluator holds what expression evaluation needs beyond the row.
type evaluator struct {
	params  map[string]any
	regexes map[string]*regexp.Regexp
	nodeAt  func(id string) (*Node, error) // startNode/endNode
	// aggs, when set, supplies already computed aggregate values by call.
	aggs map[*FuncCall]any
	// work counts list elements built and iterated by range, comprehensions
	// and quantifiers. SQL is bounded by the row budget and the timeout;
	// this is the same bound for the part of a query that runs in Go,
	// where [x IN range(1, 100000) | range(1, 100000)] would otherwise
	// allocate until the process dies.
	work *int
}

// maxEvalWork bounds evaluation work per query.
const maxEvalWork = 5_000_000

func (ev *evaluator) spend(n int) error {
	if ev.work == nil {
		ev.work = new(int)
	}
	*ev.work += n
	if *ev.work > maxEvalWork {
		return &Error{Kind: ErrBudget, Pos: -1, Msg: "the query builds or scans more than 5000000 list elements"}
	}
	return nil
}

func (ev *evaluator) eval(e Expr, r row) (any, error) {
	switch x := e.(type) {
	case *Literal:
		return x.Value, nil
	case *Param:
		v, ok := ev.params[x.Name]
		if !ok {
			return nil, semantic("parameter $%s was not supplied", x.Name)
		}
		return v, nil
	case *Variable:
		v, ok := r[x.Name]
		if !ok {
			return nil, semantic("variable %s is not defined", x.Name)
		}
		return v, nil
	case *ListLit:
		out := make([]any, len(x.Items))
		for i, it := range x.Items {
			v, err := ev.eval(it, r)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	case *MapLit:
		out := make(map[string]any, len(x.Keys))
		for i, k := range x.Keys {
			v, err := ev.eval(x.Values[i], r)
			if err != nil {
				return nil, err
			}
			out[k] = v
		}
		return out, nil
	case *PropAccess:
		t, err := ev.eval(x.Target, r)
		if err != nil {
			return nil, err
		}
		return propertyOf(t, x.Key)
	case *Index:
		return ev.evalIndex(x, r)
	case *Slice:
		return ev.evalSlice(x, r)
	case *Unary:
		v, err := ev.eval(x.X, r)
		if err != nil {
			return nil, err
		}
		switch x.Op {
		case "NOT":
			t, err := truth(v)
			if err != nil {
				return nil, err
			}
			switch t {
			case triTrue:
				return false, nil
			case triFalse:
				return true, nil
			}
			return nil, nil
		case "-":
			switch n := v.(type) {
			case nil:
				return nil, nil
			case int64:
				if n == math.MinInt64 {
					return nil, runtimeErr("integer overflow negating %d", n)
				}
				return -n, nil
			case float64:
				return -n, nil
			}
			return nil, runtimeErr("cannot negate a %s", typeName(v))
		case "+":
			if v == nil || isNumber(v) {
				return v, nil
			}
			return nil, runtimeErr("unary + needs a number, got a %s", typeName(v))
		}
	case *Binary:
		return ev.evalBinary(x, r)
	case *IsNull:
		v, err := ev.eval(x.X, r)
		if err != nil {
			return nil, err
		}
		return (v == nil) != x.Not, nil
	case *LabelCheck:
		v, err := ev.eval(x.X, r)
		if err != nil {
			return nil, err
		}
		if v == nil {
			return nil, nil
		}
		var have string
		switch n := v.(type) {
		case *Node:
			have = n.label()
		case *Rel:
			have = n.Type
		default:
			return nil, runtimeErr("a label check needs a node or relationship, got a %s", typeName(v))
		}
		ok := labelsMatch(have, x.Labels)
		return ok != x.Not, nil
	case *FuncCall:
		if aggregateNames[x.Name] {
			if ev.aggs != nil {
				if v, ok := ev.aggs[x]; ok {
					return v, nil
				}
			}
			return nil, semantic("aggregate %s() is only allowed in WITH or RETURN", x.Name)
		}
		return ev.evalFunc(x, r)
	case *CaseExpr:
		return ev.evalCase(x, r)
	case *ListComp:
		lv, err := ev.eval(x.List, r)
		if err != nil {
			return nil, err
		}
		if lv == nil {
			return nil, nil
		}
		list, ok := lv.([]any)
		if !ok {
			return nil, runtimeErr("a list comprehension needs a list, got a %s", typeName(lv))
		}
		out := make([]any, 0, len(list))
		inner := extend(r, x.Var, nil)
		for _, it := range list {
			if err := ev.spend(1); err != nil {
				return nil, err
			}
			inner[x.Var] = it
			if x.Pred != nil {
				pv, err := ev.eval(x.Pred, inner)
				if err != nil {
					return nil, err
				}
				t, err := truth(pv)
				if err != nil {
					return nil, err
				}
				if t != triTrue {
					continue
				}
			}
			if x.Proj != nil {
				v, err := ev.eval(x.Proj, inner)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
			} else {
				out = append(out, it)
			}
		}
		return out, nil
	case *Quantifier:
		return ev.evalQuantifier(x, r)
	}
	return nil, runtimeErr("cannot evaluate %T", e)
}

func extend(r row, k string, v any) row {
	n := make(row, len(r)+1)
	for a, b := range r {
		n[a] = b
	}
	n[k] = v
	return n
}

func labelsMatch(have string, groups [][]string) bool {
	for _, g := range groups {
		ok := false
		for _, l := range g {
			if l == have && have != "" {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// truth reads a value as a predicate. Only booleans and null are predicates;
// anything else is a type error rather than a guess at truthiness.
func truth(v any) (tri, error) {
	switch x := v.(type) {
	case nil:
		return triNull, nil
	case bool:
		return triOf(x), nil
	}
	return triNull, runtimeErr("expected a boolean, got a %s", typeName(v))
}

// propertyOf is n.key. See the package documentation for how content, name
// and id surface: a stored property always wins, and the column fallback
// only answers when the properties do not carry the key.
func propertyOf(t any, key string) (any, error) {
	switch x := t.(type) {
	case nil:
		return nil, nil
	case *Node:
		props, err := x.props()
		if err != nil {
			return nil, err
		}
		if v, ok := props[key]; ok {
			return v, nil
		}
		switch key {
		case "name":
			if v, ok := props["title"]; ok {
				return v, nil
			}
		case "content":
			if x.Content != "" {
				return x.Content, nil
			}
		}
		return nil, nil
	case *Rel:
		props, err := x.props()
		if err != nil {
			return nil, err
		}
		if v, ok := props[key]; ok {
			return v, nil
		}
		if key == "weight" {
			return x.Weight, nil
		}
		return nil, nil
	case map[string]any:
		return x[key], nil
	}
	return nil, runtimeErr("cannot read property %s of a %s", key, typeName(t))
}

func (ev *evaluator) evalIndex(x *Index, r row) (any, error) {
	t, err := ev.eval(x.Target, r)
	if err != nil {
		return nil, err
	}
	i, err := ev.eval(x.Idx, r)
	if err != nil {
		return nil, err
	}
	if t == nil || i == nil {
		return nil, nil
	}
	switch c := t.(type) {
	case []any:
		n, ok := i.(int64)
		if !ok {
			return nil, runtimeErr("a list index must be an integer, got a %s", typeName(i))
		}
		if n < 0 {
			n += int64(len(c))
		}
		if n < 0 || n >= int64(len(c)) {
			return nil, nil
		}
		return c[n], nil
	case map[string]any, *Node, *Rel:
		k, ok := i.(string)
		if !ok {
			return nil, runtimeErr("a map key must be a string, got a %s", typeName(i))
		}
		return propertyOf(t, k)
	}
	return nil, runtimeErr("cannot index a %s", typeName(t))
}

func (ev *evaluator) evalSlice(x *Slice, r row) (any, error) {
	t, err := ev.eval(x.Target, r)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, nil
	}
	list, ok := t.([]any)
	if !ok {
		return nil, runtimeErr("cannot slice a %s", typeName(t))
	}
	n := int64(len(list))
	bound := func(e Expr, def int64) (int64, bool, error) {
		if e == nil {
			return def, true, nil
		}
		v, err := ev.eval(e, r)
		if err != nil {
			return 0, false, err
		}
		if v == nil {
			return 0, false, nil
		}
		k, ok := v.(int64)
		if !ok {
			return 0, false, runtimeErr("a slice bound must be an integer, got a %s", typeName(v))
		}
		if k < 0 {
			k += n
		}
		if k < 0 {
			k = 0
		}
		if k > n {
			k = n
		}
		return k, true, nil
	}
	from, ok1, err := bound(x.From, 0)
	if err != nil {
		return nil, err
	}
	to, ok2, err := bound(x.To, n)
	if err != nil {
		return nil, err
	}
	if !ok1 || !ok2 {
		return nil, nil
	}
	if from >= to {
		return []any{}, nil
	}
	out := make([]any, to-from)
	copy(out, list[from:to])
	return out, nil
}

func (ev *evaluator) evalBinary(x *Binary, r row) (any, error) {
	switch x.Op {
	case "AND", "OR", "XOR":
		lv, err := ev.eval(x.L, r)
		if err != nil {
			return nil, err
		}
		l, err := truth(lv)
		if err != nil {
			return nil, err
		}
		// Both sides are evaluated: a non-boolean operand is a type error
		// even when the other side already decides the answer.
		rv, err := ev.eval(x.R, r)
		if err != nil {
			return nil, err
		}
		rt, err := truth(rv)
		if err != nil {
			return nil, err
		}
		switch x.Op {
		case "AND":
			if l == triFalse || rt == triFalse {
				return false, nil
			}
			if l == triNull || rt == triNull {
				return nil, nil
			}
			return true, nil
		case "OR":
			if l == triTrue || rt == triTrue {
				return true, nil
			}
			if l == triNull || rt == triNull {
				return nil, nil
			}
			return false, nil
		default:
			if l == triNull || rt == triNull {
				return nil, nil
			}
			return l != rt, nil
		}
	}
	lv, err := ev.eval(x.L, r)
	if err != nil {
		return nil, err
	}
	rv, err := ev.eval(x.R, r)
	if err != nil {
		return nil, err
	}
	switch x.Op {
	case "=":
		return equals(lv, rv).value(), nil
	case "<>":
		switch equals(lv, rv) {
		case triTrue:
			return false, nil
		case triFalse:
			return true, nil
		}
		return nil, nil
	case "<", ">", "<=", ">=":
		// NaN is a number that is not ordered against any number: every
		// comparison with it is false, not null.
		if isNaNValue(lv) && isNumber(rv) || isNaNValue(rv) && isNumber(lv) {
			return false, nil
		}
		c, ok := compare(lv, rv)
		if !ok {
			return nil, nil
		}
		switch x.Op {
		case "<":
			return c < 0, nil
		case ">":
			return c > 0, nil
		case "<=":
			return c <= 0, nil
		}
		return c >= 0, nil
	case "IN":
		if rv == nil {
			return nil, nil
		}
		list, ok := rv.([]any)
		if !ok {
			return nil, runtimeErr("IN needs a list on the right, got a %s", typeName(rv))
		}
		res := triFalse
		for _, it := range list {
			if err := ev.spend(1); err != nil {
				return nil, err
			}
			switch equals(lv, it) {
			case triTrue:
				return true, nil
			case triNull:
				res = triNull
			}
		}
		if lv == nil && len(list) > 0 {
			return nil, nil
		}
		return res.value(), nil
	case "STARTS", "ENDS", "CONTAINS":
		ls, lok := lv.(string)
		rs, rok := rv.(string)
		if !lok || !rok {
			return nil, nil
		}
		switch x.Op {
		case "STARTS":
			return strings.HasPrefix(ls, rs), nil
		case "ENDS":
			return strings.HasSuffix(ls, rs), nil
		}
		return strings.Contains(ls, rs), nil
	case "=~":
		ls, lok := lv.(string)
		rs, rok := rv.(string)
		if !lok || !rok {
			return nil, nil
		}
		re, err := ev.regex(rs)
		if err != nil {
			return nil, err
		}
		return re.MatchString(ls), nil
	case "+":
		return add(lv, rv)
	case "-", "*", "/", "%", "^":
		return arith(x.Op, lv, rv)
	}
	return nil, runtimeErr("unknown operator %s", x.Op)
}

func (ev *evaluator) regex(p string) (*regexp.Regexp, error) {
	if re, ok := ev.regexes[p]; ok {
		return re, nil
	}
	// openCypher regular expressions match the whole string.
	re, err := regexp.Compile(`^(?:` + p + `)$`)
	if err != nil {
		return nil, runtimeErr("invalid regular expression %q: %v (RE2 syntax; no backreferences or lookaround)", p, err)
	}
	if ev.regexes == nil {
		ev.regexes = map[string]*regexp.Regexp{}
	}
	ev.regexes[p] = re
	return re, nil
}

func add(a, b any) (any, error) {
	if a == nil || b == nil {
		return nil, nil
	}
	switch x := a.(type) {
	case string:
		switch y := b.(type) {
		case string:
			return x + y, nil
		case int64, float64:
			return x + stringify(y), nil
		}
	case []any:
		if y, ok := b.([]any); ok {
			out := make([]any, 0, len(x)+len(y))
			return append(append(out, x...), y...), nil
		}
		out := make([]any, 0, len(x)+1)
		return append(append(out, x...), b), nil
	case int64, float64:
		if s, ok := b.(string); ok {
			return stringify(a) + s, nil
		}
		if isNumber(b) {
			return arith("+", a, b)
		}
	}
	if y, ok := b.([]any); ok {
		out := make([]any, 0, len(y)+1)
		return append(append(out, a), y...), nil
	}
	return nil, runtimeErr("cannot add a %s and a %s", typeName(a), typeName(b))
}

func arith(op string, a, b any) (any, error) {
	if a == nil || b == nil {
		return nil, nil
	}
	if !isNumber(a) || !isNumber(b) {
		return nil, runtimeErr("operator %s needs numbers, got a %s and a %s", op, typeName(a), typeName(b))
	}
	ai, aInt := a.(int64)
	bi, bInt := b.(int64)
	if aInt && bInt && op != "^" {
		switch op {
		case "+":
			s := ai + bi
			if (s > ai) != (bi > 0) {
				return nil, runtimeErr("integer overflow")
			}
			return s, nil
		case "-":
			s := ai - bi
			if (s < ai) != (bi > 0) {
				return nil, runtimeErr("integer overflow")
			}
			return s, nil
		case "*":
			if ai != 0 && bi != 0 {
				s := ai * bi
				if s/bi != ai || (ai == -1 && bi == math.MinInt64) || (bi == -1 && ai == math.MinInt64) {
					return nil, runtimeErr("integer overflow")
				}
				return s, nil
			}
			return int64(0), nil
		case "/":
			if bi == 0 {
				return nil, runtimeErr("division by zero")
			}
			return ai / bi, nil
		case "%":
			if bi == 0 {
				return nil, runtimeErr("division by zero")
			}
			return ai % bi, nil
		}
	}
	af, bf := toFloat(a), toFloat(b)
	switch op {
	case "+":
		return af + bf, nil
	case "-":
		return af - bf, nil
	case "*":
		return af * bf, nil
	case "/":
		return af / bf, nil
	case "%":
		return math.Mod(af, bf), nil
	case "^":
		return math.Pow(af, bf), nil
	}
	return nil, runtimeErr("unknown operator %s", op)
}

func (ev *evaluator) evalCase(x *CaseExpr, r row) (any, error) {
	var test any
	if x.Test != nil {
		v, err := ev.eval(x.Test, r)
		if err != nil {
			return nil, err
		}
		test = v
	}
	for i, w := range x.Whens {
		wv, err := ev.eval(w, r)
		if err != nil {
			return nil, err
		}
		hit := false
		if x.Test != nil {
			hit = equals(test, wv) == triTrue
		} else {
			t, err := truth(wv)
			if err != nil {
				return nil, err
			}
			hit = t == triTrue
		}
		if hit {
			return ev.eval(x.Thens[i], r)
		}
	}
	if x.Else != nil {
		return ev.eval(x.Else, r)
	}
	return nil, nil
}

func (ev *evaluator) evalQuantifier(x *Quantifier, r row) (any, error) {
	lv, err := ev.eval(x.List, r)
	if err != nil {
		return nil, err
	}
	if lv == nil {
		return nil, nil
	}
	list, ok := lv.([]any)
	if !ok {
		return nil, runtimeErr("%s() needs a list, got a %s", x.Kind, typeName(lv))
	}
	inner := extend(r, x.Var, nil)
	trues, nulls := 0, 0
	for _, it := range list {
		if err := ev.spend(1); err != nil {
			return nil, err
		}
		inner[x.Var] = it
		pv, err := ev.eval(x.Pred, inner)
		if err != nil {
			return nil, err
		}
		t, err := truth(pv)
		if err != nil {
			return nil, err
		}
		switch t {
		case triTrue:
			trues++
		case triNull:
			nulls++
		}
	}
	falses := len(list) - trues - nulls
	switch x.Kind {
	case "any":
		if trues > 0 {
			return true, nil
		}
		if nulls > 0 {
			return nil, nil
		}
		return false, nil
	case "all":
		if falses > 0 {
			return false, nil
		}
		if nulls > 0 {
			return nil, nil
		}
		return true, nil
	case "none":
		if trues > 0 {
			return false, nil
		}
		if nulls > 0 {
			return nil, nil
		}
		return true, nil
	default: // single
		if trues > 1 {
			return false, nil
		}
		if nulls > 0 {
			return nil, nil
		}
		return trues == 1, nil
	}
}

func (ev *evaluator) evalFunc(f *FuncCall, r row) (any, error) {
	ar := functionArity[f.Name]
	if len(f.Args) < ar[0] || (ar[1] >= 0 && len(f.Args) > ar[1]) {
		return nil, semantic("%s() takes %d..%d arguments, got %d", f.Name, ar[0], ar[1], len(f.Args))
	}
	if f.Distinct {
		return nil, semantic("DISTINCT is only allowed in an aggregate call, not %s()", f.Name)
	}
	args := make([]any, len(f.Args))
	for i, a := range f.Args {
		v, err := ev.eval(a, r)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	if f.Name == "coalesce" {
		for _, a := range args {
			if a != nil {
				return a, nil
			}
		}
		return nil, nil
	}
	a0 := args[0]
	// Every other function returns null for a null first argument.
	if a0 == nil && f.Name != "range" {
		if f.Name == "exists" {
			return false, nil
		}
		return nil, nil
	}
	switch f.Name {
	case "exists":
		return true, nil
	case "id", "elementid":
		switch x := a0.(type) {
		case *Node:
			return x.ID, nil
		case *Rel:
			return x.ID, nil
		}
	case "labels":
		if n, ok := a0.(*Node); ok {
			out := make([]any, len(n.Labels))
			for i, l := range n.Labels {
				out[i] = l
			}
			return out, nil
		}
	case "type":
		if x, ok := a0.(*Rel); ok {
			return x.Type, nil
		}
	case "keys":
		switch x := a0.(type) {
		case *Node:
			m, err := x.props()
			if err != nil {
				return nil, err
			}
			return stringList(sortedKeys(m)), nil
		case *Rel:
			m, err := x.props()
			if err != nil {
				return nil, err
			}
			return stringList(sortedKeys(m)), nil
		case map[string]any:
			return stringList(sortedKeys(x)), nil
		}
	case "properties":
		switch x := a0.(type) {
		case *Node:
			return x.props()
		case *Rel:
			return x.props()
		case map[string]any:
			return x, nil
		}
	case "size", "char_length", "character_length":
		switch x := a0.(type) {
		case []any:
			if f.Name == "size" {
				return int64(len(x)), nil
			}
		case string:
			return int64(utf8.RuneCountInString(x)), nil
		}
	case "length":
		switch x := a0.(type) {
		case *Path:
			return int64(len(x.Rels)), nil
		case []any:
			return int64(len(x)), nil
		case string:
			return int64(utf8.RuneCountInString(x)), nil
		}
	case "isempty":
		switch x := a0.(type) {
		case []any:
			return len(x) == 0, nil
		case string:
			return x == "", nil
		case map[string]any:
			return len(x) == 0, nil
		}
	case "nodes":
		if p, ok := a0.(*Path); ok {
			out := make([]any, len(p.Nodes))
			for i, n := range p.Nodes {
				out[i] = n
			}
			return out, nil
		}
	case "relationships", "rels":
		switch p := a0.(type) {
		case *Path:
			out := make([]any, len(p.Rels))
			for i, n := range p.Rels {
				out[i] = n
			}
			return out, nil
		case []any: // a variable-length relationship list
			return p, nil
		}
	case "startnode", "endnode":
		if x, ok := a0.(*Rel); ok {
			id := x.StartID
			if f.Name == "endnode" {
				id = x.EndID
			}
			if ev.nodeAt == nil {
				return nil, runtimeErr("%s() is not available here", f.Name)
			}
			return ev.nodeAt(id)
		}
	case "head", "last":
		if l, ok := a0.([]any); ok {
			if len(l) == 0 {
				return nil, nil
			}
			if f.Name == "head" {
				return l[0], nil
			}
			return l[len(l)-1], nil
		}
	case "tail":
		if l, ok := a0.([]any); ok {
			if len(l) == 0 {
				return []any{}, nil
			}
			out := make([]any, len(l)-1)
			copy(out, l[1:])
			return out, nil
		}
	case "reverse":
		switch x := a0.(type) {
		case []any:
			out := make([]any, len(x))
			for i := range x {
				out[len(x)-1-i] = x[i]
			}
			return out, nil
		case string:
			rs := []rune(x)
			for i, j := 0, len(rs)-1; i < j; i, j = i+1, j-1 {
				rs[i], rs[j] = rs[j], rs[i]
			}
			return string(rs), nil
		}
	case "range":
		for _, a := range args {
			if _, ok := a.(int64); !ok {
				return nil, runtimeErr("range() needs integers")
			}
		}
		from, to := args[0].(int64), args[1].(int64)
		step := int64(1)
		if len(args) == 3 {
			step = args[2].(int64)
		}
		if step == 0 {
			return nil, runtimeErr("range() step must not be zero")
		}
		var out []any
		for v := from; (step > 0 && v <= to) || (step < 0 && v >= to); v += step {
			out = append(out, v)
			if err := ev.spend(1); err != nil {
				return nil, err
			}
			if len(out) > 100000 {
				return nil, &Error{Kind: ErrBudget, Pos: -1, Msg: "range() longer than 100000 elements"}
			}
		}
		if out == nil {
			out = []any{}
		}
		return out, nil
	case "tolower", "lower":
		if s, ok := a0.(string); ok {
			return strings.ToLower(s), nil
		}
	case "toupper", "upper":
		if s, ok := a0.(string); ok {
			return strings.ToUpper(s), nil
		}
	case "trim":
		if s, ok := a0.(string); ok {
			return strings.TrimFunc(s, unicode.IsSpace), nil
		}
	case "ltrim":
		if s, ok := a0.(string); ok {
			return strings.TrimLeftFunc(s, unicode.IsSpace), nil
		}
	case "rtrim":
		if s, ok := a0.(string); ok {
			return strings.TrimRightFunc(s, unicode.IsSpace), nil
		}
	case "replace":
		s, ok1 := a0.(string)
		from, ok2 := args[1].(string)
		to, ok3 := args[2].(string)
		if args[1] == nil || args[2] == nil {
			return nil, nil
		}
		if ok1 && ok2 && ok3 {
			return strings.ReplaceAll(s, from, to), nil
		}
	case "substring":
		s, ok := a0.(string)
		if !ok {
			break
		}
		rs := []rune(s)
		start, ok := args[1].(int64)
		if !ok || start < 0 {
			return nil, runtimeErr("substring() start must be a non-negative integer")
		}
		end := int64(len(rs))
		if len(args) == 3 {
			l, ok := args[2].(int64)
			if !ok || l < 0 {
				return nil, runtimeErr("substring() length must be a non-negative integer")
			}
			end = min(start+l, end)
		}
		if start >= int64(len(rs)) {
			return "", nil
		}
		return string(rs[start:end]), nil
	case "left", "right":
		s, ok := a0.(string)
		if !ok {
			break
		}
		if args[1] == nil {
			return nil, runtimeErr("%s() length must not be null", f.Name)
		}
		n, ok := args[1].(int64)
		if !ok || n < 0 {
			return nil, runtimeErr("%s() length must be a non-negative integer", f.Name)
		}
		rs := []rune(s)
		if n > int64(len(rs)) {
			n = int64(len(rs))
		}
		if f.Name == "left" {
			return string(rs[:n]), nil
		}
		return string(rs[int64(len(rs))-n:]), nil
	case "split":
		s, ok1 := a0.(string)
		if args[1] == nil {
			return nil, nil
		}
		sep, ok2 := args[1].(string)
		if ok1 && ok2 {
			return stringList(strings.Split(s, sep)), nil
		}
	case "tostring":
		switch x := a0.(type) {
		case string:
			return x, nil
		case int64, float64, bool:
			return stringify(x), nil
		}
	case "tointeger":
		switch x := a0.(type) {
		case int64:
			return x, nil
		case float64:
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return nil, nil
			}
			return int64(x), nil
		case string:
			if i, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil {
				return i, nil
			}
			if fl, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil && !math.IsInf(fl, 0) && !math.IsNaN(fl) {
				return int64(fl), nil
			}
			return nil, nil
		case bool:
			if x {
				return int64(1), nil
			}
			return int64(0), nil
		}
	case "tofloat":
		switch x := a0.(type) {
		case int64:
			return float64(x), nil
		case float64:
			return x, nil
		case string:
			if fl, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
				return fl, nil
			}
			return nil, nil
		}
	case "toboolean":
		switch x := a0.(type) {
		case bool:
			return x, nil
		case string:
			switch strings.ToLower(strings.TrimSpace(x)) {
			case "true":
				return true, nil
			case "false":
				return false, nil
			}
			return nil, nil
		case int64:
			return x != 0, nil
		}
	case "abs":
		switch x := a0.(type) {
		case int64:
			if x < 0 {
				return -x, nil
			}
			return x, nil
		case float64:
			return math.Abs(x), nil
		}
	case "ceil", "floor", "round", "sqrt":
		if isNumber(a0) {
			v := toFloat(a0)
			switch f.Name {
			case "ceil":
				return math.Ceil(v), nil
			case "floor":
				return math.Floor(v), nil
			case "round":
				// openCypher rounds half up (towards positive infinity).
				return math.Floor(v + 0.5), nil
			}
			return math.Sqrt(v), nil
		}
	case "sign":
		if isNumber(a0) {
			v := toFloat(a0)
			switch {
			case v > 0:
				return int64(1), nil
			case v < 0:
				return int64(-1), nil
			}
			return int64(0), nil
		}
	}
	return nil, runtimeErr("%s() cannot take a %s", f.Name, typeName(a0))
}

func stringList(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// stringify is toString for scalars, in openCypher's spelling: floats always
// carry a fraction (1.0, not 1).
func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		switch {
		case math.IsNaN(x):
			return "NaN"
		case math.IsInf(x, 1):
			return "Infinity"
		case math.IsInf(x, -1):
			return "-Infinity"
		}
		s := strconv.FormatFloat(x, 'f', -1, 64)
		if math.Abs(x) >= 1e16 || (math.Abs(x) < 1e-6 && x != 0) {
			s = strconv.FormatFloat(x, 'E', -1, 64)
		}
		if !strings.ContainsAny(s, ".EN") {
			s += ".0"
		}
		return s
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return fmt.Sprint(v)
}

func isNaNValue(v any) bool {
	f, ok := v.(float64)
	return ok && math.IsNaN(f)
}
