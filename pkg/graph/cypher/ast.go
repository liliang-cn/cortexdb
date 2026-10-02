package cypher

import "fmt"

// ErrorKind says which of three different things went wrong, because a caller
// — usually a model — has to react to each differently: a syntax error is a
// typo to fix, an unsupported construct is a query to rewrite in the subset,
// and a runtime error is a query that was fine but met data or a budget it
// could not handle.
type ErrorKind string

const (
	ErrSyntax      ErrorKind = "syntax"
	ErrUnsupported ErrorKind = "unsupported"
	ErrReadOnly    ErrorKind = "read_only"
	ErrSemantic    ErrorKind = "semantic"
	ErrRuntime     ErrorKind = "runtime"
	ErrBudget      ErrorKind = "budget"
)

// Error is every error this package returns about a query.
type Error struct {
	Kind ErrorKind
	Pos  int // byte offset in the query, -1 when not tied to one
	Msg  string
}

func (e *Error) Error() string {
	if e.Pos >= 0 && (e.Kind == ErrSyntax || e.Kind == ErrUnsupported || e.Kind == ErrReadOnly) {
		return fmt.Sprintf("cypher %s error at offset %d: %s", e.Kind, e.Pos, e.Msg)
	}
	return fmt.Sprintf("cypher %s error: %s", e.Kind, e.Msg)
}

func unsupported(pos int, format string, args ...any) *Error {
	return &Error{Kind: ErrUnsupported, Pos: pos, Msg: fmt.Sprintf(format, args...) + supportedHint}
}

func semantic(format string, args ...any) *Error {
	return &Error{Kind: ErrSemantic, Pos: -1, Msg: fmt.Sprintf(format, args...)}
}

func runtimeErr(format string, args ...any) *Error {
	return &Error{Kind: ErrRuntime, Pos: -1, Msg: fmt.Sprintf(format, args...)}
}

const supportedHint = " (supported: MATCH / OPTIONAL MATCH / WHERE / WITH / UNWIND / RETURN / ORDER BY / SKIP / LIMIT / UNION; see the package documentation)"

// --- AST ------------------------------------------------------------------

// Query is a UNION of single queries; most have exactly one part.
type Query struct {
	Parts    []*SingleQuery
	UnionAll []bool // UnionAll[i] joins Parts[i] and Parts[i+1]
}

type SingleQuery struct {
	Clauses []Clause
}

type Clause interface{ clause() }

type MatchClause struct {
	Optional bool
	Patterns []*PatternPart
	Where    Expr
	Pos      int
}

type UnwindClause struct {
	Expr Expr
	Var  string
}

// ProjectionClause is WITH or RETURN; they differ only in what follows.
type ProjectionClause struct {
	IsReturn bool
	Distinct bool
	Star     bool
	Items    []*ProjectionItem
	OrderBy  []*SortItem
	Skip     Expr
	Limit    Expr
	Where    Expr // WITH only
}

func (*MatchClause) clause()      {}
func (*UnwindClause) clause()     {}
func (*ProjectionClause) clause() {}

type ProjectionItem struct {
	Expr  Expr
	Alias string // the column name: explicit AS, or the expression text
	// Explicit is true when the column was named with AS.
	Explicit bool
}

type SortItem struct {
	Expr Expr
	Desc bool
}

// PatternPart is one comma-separated pattern, optionally named (p = ...).
type PatternPart struct {
	PathVar string
	Nodes   []*NodePattern
	Rels    []*RelPattern // len(Rels) == len(Nodes)-1
}

type NodePattern struct {
	Var    string
	Labels [][]string // conjunction of disjunctions: :A|B:C is [[A B] [C]]
	Props  *MapLit
	Pos    int
}

type Direction int

const (
	DirBoth Direction = iota // -[]- (or <-[]->, which openCypher also reads as either)
	DirOut                   // -[]->
	DirIn                    // <-[]-
)

type RelPattern struct {
	Var    string
	Types  []string // alternation; empty means any
	Props  *MapLit
	Dir    Direction
	VarLen bool
	Min    int
	Max    int // -1 when unbounded
	Pos    int
}

// --- expressions --------------------------------------------------------

type Expr interface{ expr() }

type (
	Literal  struct{ Value any }
	Param    struct{ Name string }
	Variable struct{ Name string }
	ListLit  struct{ Items []Expr }
	MapLit   struct {
		Keys   []string
		Values []Expr
	}
	PropAccess struct {
		Target Expr
		Key    string
	}
	Index struct {
		Target Expr
		Idx    Expr
	}
	Slice struct {
		Target   Expr
		From, To Expr // either may be nil
	}
	Unary struct {
		Op string // NOT, -, +
		X  Expr
	}
	Binary struct {
		Op   string // OR XOR AND = <> < > <= >= =~ IN STARTS ENDS CONTAINS + - * / % ^
		L, R Expr
	}
	IsNull struct {
		X   Expr
		Not bool
	}
	LabelCheck struct {
		X      Expr
		Labels [][]string
		Not    bool
	}
	FuncCall struct {
		Name     string // lower-cased
		Distinct bool
		Star     bool // count(*)
		Args     []Expr
	}
	CaseExpr struct {
		Test  Expr // nil for the searched form
		Whens []Expr
		Thens []Expr
		Else  Expr
	}
	// ListComp is [x IN list WHERE pred | expr].
	ListComp struct {
		Var  string
		List Expr
		Pred Expr
		Proj Expr
	}
	// Quantifier is any/all/none/single(x IN list WHERE pred).
	Quantifier struct {
		Kind string
		Var  string
		List Expr
		Pred Expr
	}
)

func (*Literal) expr()    {}
func (*Param) expr()      {}
func (*Variable) expr()   {}
func (*ListLit) expr()    {}
func (*MapLit) expr()     {}
func (*PropAccess) expr() {}
func (*Index) expr()      {}
func (*Slice) expr()      {}
func (*Unary) expr()      {}
func (*Binary) expr()     {}
func (*IsNull) expr()     {}
func (*LabelCheck) expr() {}
func (*FuncCall) expr()   {}
func (*CaseExpr) expr()   {}
func (*ListComp) expr()   {}
func (*Quantifier) expr() {}

// aggregateNames are the aggregates this subset evaluates.
var aggregateNames = map[string]bool{
	"count": true, "collect": true, "min": true, "max": true, "sum": true, "avg": true,
}

// containsAggregate reports whether e has an aggregate call outside any
// list comprehension's own scope.
func containsAggregate(e Expr) bool {
	found := false
	walkExpr(e, func(x Expr) bool {
		if f, ok := x.(*FuncCall); ok && aggregateNames[f.Name] {
			found = true
			return false
		}
		return !found
	})
	return found
}

// walkExpr visits e and its children, depth first; visit returning false
// skips the children of that node.
func walkExpr(e Expr, visit func(Expr) bool) {
	if e == nil || !visit(e) {
		return
	}
	switch x := e.(type) {
	case *ListLit:
		for _, it := range x.Items {
			walkExpr(it, visit)
		}
	case *MapLit:
		for _, v := range x.Values {
			walkExpr(v, visit)
		}
	case *PropAccess:
		walkExpr(x.Target, visit)
	case *Index:
		walkExpr(x.Target, visit)
		walkExpr(x.Idx, visit)
	case *Slice:
		walkExpr(x.Target, visit)
		walkExpr(x.From, visit)
		walkExpr(x.To, visit)
	case *Unary:
		walkExpr(x.X, visit)
	case *Binary:
		walkExpr(x.L, visit)
		walkExpr(x.R, visit)
	case *IsNull:
		walkExpr(x.X, visit)
	case *LabelCheck:
		walkExpr(x.X, visit)
	case *FuncCall:
		for _, a := range x.Args {
			walkExpr(a, visit)
		}
	case *CaseExpr:
		walkExpr(x.Test, visit)
		for i := range x.Whens {
			walkExpr(x.Whens[i], visit)
			walkExpr(x.Thens[i], visit)
		}
		walkExpr(x.Else, visit)
	case *ListComp:
		walkExpr(x.List, visit)
		walkExpr(x.Pred, visit)
		walkExpr(x.Proj, visit)
	case *Quantifier:
		walkExpr(x.List, visit)
		walkExpr(x.Pred, visit)
	}
}

// freeVars lists the variables e reads from its environment, excluding the
// ones a comprehension binds for itself.
func freeVars(e Expr, out map[string]bool) {
	var rec func(e Expr, bound map[string]bool)
	rec = func(e Expr, bound map[string]bool) {
		walkExpr(e, func(x Expr) bool {
			switch v := x.(type) {
			case *Variable:
				if !bound[v.Name] {
					out[v.Name] = true
				}
			case *ListComp:
				rec(v.List, bound)
				inner := copyBound(bound, v.Var)
				rec(v.Pred, inner)
				rec(v.Proj, inner)
				return false
			case *Quantifier:
				rec(v.List, bound)
				rec(v.Pred, copyBound(bound, v.Var))
				return false
			}
			return true
		})
	}
	rec(e, map[string]bool{})
}

func copyBound(b map[string]bool, add string) map[string]bool {
	n := make(map[string]bool, len(b)+1)
	for k := range b {
		n[k] = true
	}
	n[add] = true
	return n
}
