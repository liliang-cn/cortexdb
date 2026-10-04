package cypher

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// The SQL a query compiles to when the backend reports indexed node
// properties. pkg/graph's property_index_test.go runs these against both
// databases; this checks the text itself, which is what a planner matches an
// expression index against.

func compileIndexed(t *testing.T, query string, kind sqldialect.Kind, indexed map[string]bool, params map[string]any) (string, []any, *clausePlan) {
	t.Helper()
	q, err := Parse(query)
	if err != nil {
		t.Fatalf("parse %q: %v", query, err)
	}
	src := &sources{
		nodeSrc: "graph_nodes", edgeSrc: "graph_edges",
		kind: kind, dialect: sqldialect.For(kind),
		needContent: needsContent(q), indexedProps: indexed,
	}
	c, ok := q.Parts[0].Clauses[0].(*MatchClause)
	if !ok {
		t.Fatalf("%q does not start with MATCH", query)
	}
	p, err := src.compile(c, params, map[string][]string{}, map[string]varKind{})
	if err != nil {
		t.Fatalf("compile %q: %v", query, err)
	}
	s := p.sql.b.String()
	if strings.Count(s, "?") != len(p.sql.args) {
		t.Fatalf("placeholders and arguments disagree in %q:\n%s", query, s)
	}
	return s, p.sql.args, p
}

var both = []sqldialect.Kind{sqldialect.SQLite, sqldialect.Postgres}

func TestAnIndexedEqualityIsTheIndexedExpression(t *testing.T) {
	indexed := map[string]bool{"run_id": true}
	for _, kind := range both {
		want := sqldialect.For(kind).JSONTextGuarded("n0.properties", "run_id")
		for _, q := range []string{
			`MATCH (s) WHERE s.run_id = 'r1' RETURN s`,
			`MATCH (s {run_id: 'r1'}) RETURN s`,
			`MATCH (s) WHERE s.run_id IN ['r1', 'r2'] RETURN s`,
		} {
			sql, args, p := compileIndexed(t, q, kind, indexed, nil)
			if !strings.Contains(sql, "("+want+" = ?)") && !strings.Contains(sql, "("+want+" IN (?, ?))") {
				t.Errorf("%s %q does not filter on the indexed expression:\n%s", kind, q, sql)
			}
			if !p.nodes[0].anchored {
				t.Errorf("%s %q: an indexed lookup did not anchor the node", kind, q)
			}
			if args[len(args)-1] != "r1" && args[len(args)-1] != "r2" {
				t.Errorf("%s %q: the value is not bound: %v", kind, q, args)
			}
		}
	}
}

func TestWithoutTheIndexTheSQLIsAsBefore(t *testing.T) {
	for _, kind := range both {
		q := `MATCH (s) WHERE s.run_id = 'r1' RETURN s`
		plain, _, p := compileIndexed(t, q, kind, nil, nil)
		other, _, _ := compileIndexed(t, q, kind, map[string]bool{"status": true}, nil)
		if plain != other {
			t.Errorf("%s: an index on another key changed the SQL:\n%s\n---\n%s", kind, plain, other)
		}
		if strings.Contains(plain, "'$.run_id'") || strings.Contains(plain, "'run_id'") {
			t.Errorf("%s: an unindexed key was written into the SQL:\n%s", kind, plain)
		}
		if p.nodes[0].anchored {
			t.Errorf("%s: an unindexed property filter anchored the node", kind)
		}
	}
}

// name and content filters accept a fallback (title, the content column), and
// a key the catalog holds but that is not an identifier is never written.
func TestKeysTheIndexCannotServeKeepTheBoundFilter(t *testing.T) {
	indexed := map[string]bool{"name": true, "content": true, "a-b": true}
	for _, kind := range both {
		for _, q := range []string{
			`MATCH (s) WHERE s.name = 'x' RETURN s`,
			`MATCH (s) WHERE s.content = 'x' RETURN s`,
			"MATCH (s) WHERE s.`a-b` = 'x' RETURN s",
		} {
			sql, _, p := compileIndexed(t, q, kind, indexed, nil)
			if strings.Contains(sql, "'$.name'") || strings.Contains(sql, "'name'") ||
				strings.Contains(sql, "'$.content'") || strings.Contains(sql, "a-b") {
				t.Errorf("%s %q wrote a key into the SQL:\n%s", kind, q, sql)
			}
			if p.nodes[0].anchored {
				t.Errorf("%s %q anchored on a filter the index cannot serve", kind, q)
			}
		}
	}
}

func TestAnIndexedPropertyAnchorsTheWalk(t *testing.T) {
	sql, _, _ := compileIndexed(t,
		`MATCH (l:LLMCall {run_id: $run})-[:TRIGGERED*1..2]->(t:ToolCall) RETURN count(*)`,
		sqldialect.SQLite, map[string]bool{"run_id": true}, map[string]any{"run": "r1"})
	for _, want := range []string{"CROSS JOIN", "+e.edge_type"} {
		if !strings.Contains(sql, want) {
			t.Errorf("the walk from an indexed anchor lacks %q:\n%s", want, sql)
		}
	}
}

func TestNumericRangesOnAnIndexedKey(t *testing.T) {
	indexed := map[string]bool{"latency_ms": true}
	ex := sqldialect.For(sqldialect.SQLite).JSONTextGuarded("n0.properties", "latency_ms")
	for _, c := range []struct {
		query  string
		params map[string]any
		want   string // "" for no range filter
		arg    any
	}{
		{`MATCH (s) WHERE s.latency_ms > 3000 RETURN s`, nil, "(" + ex + " > ?)", int64(3000)},
		{`MATCH (s) WHERE 3000 < s.latency_ms RETURN s`, nil, "(" + ex + " > ?)", int64(3000)},
		{`MATCH (s) WHERE s.latency_ms <= 2.5 RETURN s`, nil, "(" + ex + " <= ?)", 2.5},
		{`MATCH (s) WHERE 10 >= s.latency_ms RETURN s`, nil, "(" + ex + " <= ?)", int64(10)},
		{`MATCH (s) WHERE s.latency_ms < $p RETURN s`, map[string]any{"p": 7}, "(" + ex + " < ?)", int64(7)},
		{`MATCH (s) WHERE s.latency_ms < $p RETURN s`, map[string]any{"p": int32(7)}, "(" + ex + " < ?)", int64(7)},
		{`MATCH (s) WHERE s.latency_ms < $p RETURN s`, map[string]any{"p": float32(1.5)}, "(" + ex + " < ?)", float64(1.5)},
		{`MATCH (s) WHERE s.latency_ms > $p RETURN s`, map[string]any{"p": "3000"}, "", nil},
		{`MATCH (s) WHERE s.other > 3000 RETURN s`, nil, "", nil},
		{`MATCH (s) WHERE s.latency_ms > s.other RETURN s`, nil, "", nil},
	} {
		sql, args, _ := compileIndexed(t, c.query, sqldialect.SQLite, indexed, c.params)
		if c.want == "" {
			if strings.Contains(sql, " > ?") || strings.Contains(sql, " < ?") {
				t.Errorf("%q (%v) pushed a range it cannot judge:\n%s", c.query, c.params, sql)
			}
			continue
		}
		if !strings.Contains(sql, c.want) {
			t.Errorf("%q (%v): want %s in\n%s", c.query, c.params, c.want, sql)
		}
		if args[len(args)-1] != c.arg {
			t.Errorf("%q (%v): bound %v (%T), want %v (%T)", c.query, c.params, args[len(args)-1], args[len(args)-1], c.arg, c.arg)
		}
	}
	// On PostgreSQL ->> is text: no range filter, however the key is indexed.
	sql, _, _ := compileIndexed(t, `MATCH (s) WHERE s.latency_ms > 3000 RETURN s`, sqldialect.Postgres, indexed, nil)
	if strings.Contains(sql, " > ?") {
		t.Errorf("PostgreSQL got a text range filter that could drop true matches:\n%s", sql)
	}
}

// hookDB is noDB that also names indexed properties, and counts being asked.
type hookDB struct {
	noDB
	asked *int
}

func (h hookDB) IndexedNodeProperties(context.Context) map[string]bool {
	*h.asked++
	return map[string]bool{"run_id": true}
}

func (hookDB) Query(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("no database")
}

func TestExecuteAsksTheBackendForItsIndexes(t *testing.T) {
	asked := 0
	_, err := Execute(context.Background(), hookDB{asked: &asked}, `MATCH (s) WHERE s.run_id = 'r1' RETURN s`, Options{})
	if err == nil {
		t.Fatal("a query against no database succeeded")
	}
	if asked != 1 {
		t.Errorf("the backend was asked for its indexes %d times, want 1", asked)
	}
}
