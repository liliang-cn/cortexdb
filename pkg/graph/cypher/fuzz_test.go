package cypher

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// The SQL this package writes must never contain text a caller wrote. Two
// independent checks hold that line:
//
//  1. Rename invariance: replacing every caller-written string in a query —
//     labels, types, property keys, string values, variable and parameter
//     names — with a different string of the same kind must leave the SQL
//     text byte-for-byte unchanged. Only the arguments may differ.
//  2. Vocabulary: every quoted literal in the SQL is one of the handful of
//     constants this package itself writes.
//
// Either check alone could be fooled; together a leak has to be invisible to
// renaming and look like a constant, which a caller's text cannot.

var allowedSQLLiterals = map[string]bool{
	`''`: true, `'%valid%'`: true, `'\'`: true,
	`'$.valid_to'`: true, `'$.valid_from'`: true, `'valid_to'`: true, `'valid_from'`: true,
}

var sqlLiteralRe = regexp.MustCompile(`'(?:[^']|'')*'`)

func compileForTest(q *Query, kind sqldialect.Kind) ([]string, error) {
	src := &sources{
		nodeSrc: "graph_nodes", edgeSrc: "graph_edges",
		kind: kind, dialect: sqldialect.For(kind),
		validAt: "2026-01-01T00:00:00Z", needContent: needsContent(q),
	}
	var out []string
	for _, part := range q.Parts {
		scope := map[string]varKind{}
		for _, cl := range part.Clauses {
			switch c := cl.(type) {
			case *MatchClause:
				bound := map[string][]string{}
				for _, pp := range c.Patterns {
					for _, n := range pp.Nodes {
						if _, ok := scope[n.Var]; ok && n.Var != "" {
							bound[n.Var] = []string{"bound-id-1", "bound-id-2"}
						}
					}
				}
				p, err := src.compile(c, nil, bound, scope)
				if err != nil {
					return nil, err
				}
				out = append(out, p.sql.b.String())
				for _, chk := range p.unboundedChecks {
					out = append(out, chk.b.String())
				}
				if strings.Count(p.sql.b.String(), "?") != len(p.sql.args) {
					return nil, errors.New("placeholder count does not match argument count")
				}
				for _, pp := range c.Patterns {
					for _, n := range pp.Nodes {
						if n.Var != "" {
							scope[n.Var] = kNode
						}
					}
					for _, r := range pp.Rels {
						if r.Var != "" {
							scope[r.Var] = kRel
						}
					}
				}
			case *UnwindClause:
				scope[c.Var] = kValue
			case *ProjectionClause:
				if !c.IsReturn {
					next := map[string]varKind{}
					if c.Star {
						for k, v := range scope {
							next[k] = v
						}
					}
					for _, it := range c.Items {
						next[it.Alias] = kValue
					}
					scope = next
				}
			}
		}
	}
	return out, nil
}

// renamer maps every caller string to a stand-in of the same class: a key
// SQLite's JSON path can quote stays quotable, one it cannot stays not.
type renamer struct{}

func (renamer) s(v string) string {
	// The two property keys with a column fallback change the shape of the
	// pre-filter by design (name also tries title; content also tries the
	// content column). They are part of the fixed mapping, not caller text.
	if v == "name" || v == "content" {
		return v
	}
	h := sha256.Sum256([]byte("salt:" + v))
	tag := hex.EncodeToString(h[:6])
	if safeJSONKey(v) {
		return "q" + tag
	}
	if v == "" {
		return ""
	}
	return `"` + tag
}

func (rn renamer) expr(e Expr) {
	walkExpr(e, func(x Expr) bool {
		switch v := x.(type) {
		case *Literal:
			if s, ok := v.Value.(string); ok {
				v.Value = rn.s(s)
			}
		case *Variable:
			v.Name = rn.s(v.Name)
		case *Param:
			v.Name = rn.s(v.Name)
		case *PropAccess:
			v.Key = rn.s(v.Key)
		case *MapLit:
			for i := range v.Keys {
				v.Keys[i] = rn.s(v.Keys[i])
			}
		case *LabelCheck:
			for _, g := range v.Labels {
				for i := range g {
					g[i] = rn.s(g[i])
				}
			}
		case *ListComp:
			v.Var = rn.s(v.Var)
		case *Quantifier:
			v.Var = rn.s(v.Var)
		}
		return true
	})
}

func (rn renamer) query(q *Query) {
	for _, part := range q.Parts {
		for _, cl := range part.Clauses {
			switch c := cl.(type) {
			case *MatchClause:
				for _, pp := range c.Patterns {
					pp.PathVar = rn.s(pp.PathVar)
					for _, n := range pp.Nodes {
						n.Var = rn.s(n.Var)
						for _, g := range n.Labels {
							for i := range g {
								g[i] = rn.s(g[i])
							}
						}
						if n.Props != nil {
							rn.expr(n.Props)
						}
					}
					for _, r := range pp.Rels {
						r.Var = rn.s(r.Var)
						for i := range r.Types {
							r.Types[i] = rn.s(r.Types[i])
						}
						if r.Props != nil {
							rn.expr(r.Props)
						}
					}
				}
				rn.expr(c.Where)
			case *UnwindClause:
				rn.expr(c.Expr)
				c.Var = rn.s(c.Var)
			case *ProjectionClause:
				for _, it := range c.Items {
					rn.expr(it.Expr)
					it.Alias = rn.s(it.Alias)
				}
				for _, s := range c.OrderBy {
					rn.expr(s.Expr)
				}
				rn.expr(c.Where)
			}
		}
	}
}

// checkNoLeak runs both checks for one query text.
func checkNoLeak(t *testing.T, query string) {
	q, err := Parse(query)
	if err != nil {
		var ce *Error
		if !errors.As(err, &ce) {
			t.Fatalf("Parse returned a non-package error %T: %v", err, err)
		}
		return
	}
	if _, err := check(q); err != nil {
		return
	}
	for _, kind := range []sqldialect.Kind{sqldialect.SQLite, sqldialect.Postgres} {
		a, err := compileForTest(q, kind)
		if err != nil {
			t.Fatalf("compile %q: %v", query, err)
		}
		for _, s := range a {
			for _, lit := range sqlLiteralRe.FindAllString(s, -1) {
				if !allowedSQLLiterals[lit] {
					t.Fatalf("query %q produced SQL with literal %s:\n%s", query, lit, s)
				}
			}
			for _, r := range s {
				if r == '"' || r == '`' || unicode.IsControl(r) {
					t.Fatalf("query %q produced SQL with %q:\n%s", query, r, s)
				}
			}
		}
		q2, err := Parse(query)
		if err != nil {
			t.Fatal(err)
		}
		renamer{}.query(q2)
		b, err := compileForTest(q2, kind)
		if err != nil {
			t.Fatalf("compile renamed %q: %v", query, err)
		}
		if strings.Join(a, "\n;\n") != strings.Join(b, "\n;\n") {
			t.Fatalf("SQL text depends on caller strings for %q:\n%s\n--- renamed ---\n%s", query, strings.Join(a, "\n"), strings.Join(b, "\n"))
		}
	}
}

// noDB is a Backend that has no database: queries without MATCH never ask
// for one, and those are what the fuzzer evaluates end to end.
type noDB struct{}

func (noDB) Dialect() sqldialect.Dialect { return sqldialect.For(sqldialect.SQLite) }
func (noDB) Query(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("no database")
}
func (noDB) NodeSource(context.Context) (string, []any) { return "graph_nodes", nil }
func (noDB) EdgeSource(context.Context) (string, []any) { return "graph_edges", nil }

var fuzzSeeds = []string{
	"MATCH (p:project)-[:depends_on]->(c {name: 'CortexDB'}) RETURN p.name",
	"MATCH (a {name: 'x'})-[r:T|U*1..3]-(b:L {k: $p}) WHERE b.name STARTS WITH '%_\\\\' AND id(a) IN ['1', '2'] RETURN a, r, b",
	"MATCH (n:`we'ird`) WHERE n.`k\"ey` = 'v''al' AND n.`k\\\\` CONTAINS '\"' RETURN n",
	"MATCH p = (a)-[*]->(b) OPTIONAL MATCH (b)<-[:X {w: 'q'}]-(c) WITH a, count(c) AS n WHERE n > 1 RETURN a.name, n ORDER BY n DESC SKIP 1 LIMIT 2",
	"UNWIND [1, 2, 3] AS x WITH x WHERE x % 2 = 1 RETURN collect(x), sum(x), avg(x)",
	"RETURN [x IN range(1, 10) WHERE x > 3 | x * 2][1..3] AS l, CASE WHEN 1 < 2 THEN 'a' ELSE 'b' END",
	"MATCH (n) WHERE n:a OR n IS LABELED b RETURN DISTINCT labels(n) UNION MATCH (m) RETURN DISTINCT labels(m)",
	"MATCH (a), (b) WHERE a.name = b.name RETURN count(*)",
	"CREATE (n)",
	"MATCH (n) RETURN n {.x}",
	"RETURN 1 / 0",
	"RETURN -9223372036854775808 - 1",
	"RETURN '\\u0041' =~ '[A-",
}

func TestTheGeneratedSQLNeverCarriesCallerText(t *testing.T) {
	for _, q := range fuzzSeeds {
		checkNoLeak(t, q)
	}
}

func FuzzParseNeverLeaksUserText(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, query string) {
		if len(query) > 2000 {
			return
		}
		// A hang is a failure too; the fuzzer cannot see one on its own.
		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				panic(fmt.Sprintf("query did not finish in 10s: %q", query))
			}
		}()
		checkNoLeak(t, query)
		// End to end for queries that need no database: evaluation must
		// return a value or a package error, never panic.
		q, err := Parse(query)
		if err != nil {
			return
		}
		for _, part := range q.Parts {
			for _, cl := range part.Clauses {
				if _, ok := cl.(*MatchClause); ok {
					return
				}
				if u, ok := cl.(*UnwindClause); ok {
					// UNWIND range(...) can be made arbitrarily large.
					_ = u
				}
			}
		}
		_, err = Execute(context.Background(), noDB{}, query, Options{MaxIntermediate: 10000})
		if err != nil {
			var ce *Error
			if !errors.As(err, &ce) {
				t.Fatalf("Execute returned a non-package error %T: %v", err, err)
			}
		}
	})
}
