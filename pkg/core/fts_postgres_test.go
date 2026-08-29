package core

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/pgtest"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// The corpus these tests search. Deliberately the shape the comment in
// fts_cjk.go warns about: Chinese lesson words, several of them exactly two
// characters long.
var lexicalCorpus = []string{
	"乘法和除法是四则运算的一部分",
	"分数的加减法需要先通分",
	"三角形的面积等于底乘高除以二",
	"周长是围绕图形一周的长度",
	"the quick brown fox jumps over the lazy dog",
	"PostgreSQL full text search with tsvector and GIN indexes",
}

func openLexicalPG(t *testing.T) *sql.DB {
	t.Helper()
	db := pgtest.Open(t, "core_fts")
	if db == nil {
		t.Skip(pgtest.EnvDSN + " unset — PostgreSQL lexical search NOT covered by this run")
	}
	ctx := context.Background()
	// No DROP first: the schema is this test's own and starts empty.
	if _, err := db.ExecContext(ctx, `CREATE TABLE lex_docs (id serial PRIMARY KEY, content text NOT NULL)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, doc := range lexicalCorpus {
		if _, err := db.ExecContext(ctx, `INSERT INTO lex_docs (content) VALUES ($1)`, doc); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	for _, stmt := range PostgresLexicalDDL("lex_docs", "content") {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("ddl %q: %v", stmt, err)
		}
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// rebind, locally: this package has no dialect dependency and the condition
// ships `?` so the caller can bind it however it binds everything else.
func pgRebind(q string) string {
	out := q
	for i := 1; strings.Contains(out, "?"); i++ {
		out = strings.Replace(out, "?", "$"+string(rune('0'+i)), 1)
	}
	return out
}

func TestPostgresLexicalFindsWhatSQLiteWouldFind(t *testing.T) {
	db := openLexicalPG(t)
	ctx := context.Background()

	cases := []struct {
		query       string
		wantSubstr  string
		wantIndexed bool
		why         string
	}{
		{"乘法", "乘法和除法", false, "two characters: below the trigram floor on both backends"},
		{"分数", "分数的加减法", false, "the case the FTS5 comment calls out by name"},
		{"四则运算", "乘法和除法", true, "four characters: the trigram index can serve it"},
		{"三角形的面积", "三角形的面积", true, "a long CJK phrase"},
		{"quick brown", "quick brown fox", true, "English goes to the word index"},
		{"tsvector", "PostgreSQL full text", true, "a single English term"},
	}

	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			cond, args, indexed := PostgresLexicalCondition("content", tc.query)
			if indexed != tc.wantIndexed {
				t.Errorf("indexed = %v, want %v (%s)", indexed, tc.wantIndexed, tc.why)
			}

			var found string
			err := db.QueryRowContext(ctx,
				pgRebind(`SELECT content FROM lex_docs WHERE `+cond+` LIMIT 1`), args...).Scan(&found)
			if err == sql.ErrNoRows {
				t.Fatalf("%q found nothing — a silent zero is the exact failure this strategy exists to prevent", tc.query)
			}
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			if !strings.Contains(found, tc.wantSubstr) {
				t.Errorf("found %q, want something containing %q", found, tc.wantSubstr)
			}
		})
	}
}

// A query made of punctuation must not become a syntax error or match
// everything. plainto_tsquery is chosen for the same reason MatchExpression
// exists: user text is words, never operators.
func TestPunctuationIsDataNotSyntax(t *testing.T) {
	db := openLexicalPG(t)
	ctx := context.Background()

	for _, q := range []string{"fox & dog", "!quick", "'; DROP TABLE lex_docs; --", "分数!"} {
		cond, args, _ := PostgresLexicalCondition("content", q)
		rows, err := db.QueryContext(ctx, pgRebind(`SELECT content FROM lex_docs WHERE `+cond), args...)
		if err != nil {
			t.Errorf("%q raised %v — user text was read as syntax", q, err)
			continue
		}
		rows.Close()
	}

	// And the table is still there.
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM lex_docs`).Scan(&n); err != nil || n != len(lexicalCorpus) {
		t.Fatalf("corpus is %d rows (err %v), want %d", n, err, len(lexicalCorpus))
	}
}

// The routing decisions come from fts_cjk.go, so both backends send a query
// down the same arm even where the SQL differs.
func TestBothBackendsRouteTheSameQueryTheSameWay(t *testing.T) {
	for _, q := range []string{"乘法", "分数", "四则运算", "quick brown", ""} {
		_, _, indexed := PostgresLexicalCondition("content", q)
		belowFloor := BelowTrigramFloor(q)
		if belowFloor && indexed {
			t.Errorf("%q is below the trigram floor but claims to be indexed", q)
		}
		if !belowFloor && !indexed && q != "" {
			t.Errorf("%q is above the floor but claims no index", q)
		}
	}
}
