package graphflow

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// The as-of query, run on PostgreSQL.
//
// cortexdb.DB still holds a *core.SQLiteStore, so there is no PostgreSQL brain
// to point QueryFactsAsOf at yet. That is exactly why this test exists: the
// query is the part that has to be right on both, and a form that is only ever
// built and never executed is a form nobody has tested. Building the same
// string the real code builds and running it against a real database is the
// difference between "should work" and "does".
func TestTheAsOfQueryRunsOnPostgres(t *testing.T) {
	dsn := os.Getenv("CORTEXDB_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("CORTEXDB_TEST_POSTGRES unset — the PostgreSQL form of the as-of query is NOT covered by this run")
	}
	ctx := context.Background()

	// Its own schema, because `go test ./...` runs packages in parallel and
	// pkg/graph's tests are using a graph_edges of their own in the same
	// database. Dropping a table out from under another package is a failure
	// that only appears in the full run and passes on every retry of the one
	// package — the worst kind to leave lying around.
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := admin.ExecContext(ctx, `DROP SCHEMA IF EXISTS graphflow_test CASCADE; CREATE SCHEMA graphflow_test`); err != nil {
		admin.Close()
		t.Fatalf("schema: %v", err)
	}
	admin.Close()

	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("pgx", dsn+sep+"search_path=graphflow_test")
	if err != nil {
		t.Fatalf("open scoped: %v", err)
	}
	defer db.Close()
	// The columns the query touches, with properties as TEXT exactly as the
	// graph schema declares it — the ->> cast has to survive that.
	if _, err := db.ExecContext(ctx, `CREATE TABLE graph_edges (
		id TEXT PRIMARY KEY,
		from_node_id TEXT NOT NULL,
		to_node_id TEXT NOT NULL,
		edge_type TEXT,
		properties TEXT
	)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	start := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	seed := []struct {
		id, from, to, typ, props string
	}{
		{"closed", "entity:leo", "entity:beijing", "lives_in", fmt.Sprintf(
			`{"valid_from":"%s","valid_to":"%s","recorded_at":"%s"}`,
			start.Format(time.RFC3339), end.Format(time.RFC3339), start.Format(time.RFC3339))},
		{"open", "entity:leo", "entity:chengdu", "lives_in", fmt.Sprintf(
			`{"valid_from":"%s","recorded_at":"%s"}`,
			end.Format(time.RFC3339), end.Format(time.RFC3339))},
		// Not a temporal fact at all: no validity. Must never be returned.
		{"plain", "entity:leo", "entity:linbit", "works_at", `{"note":"no validity"}`},
	}
	for _, e := range seed {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO graph_edges (id, from_node_id, to_node_id, edge_type, properties) VALUES ($1,$2,$3,$4,$5)`,
			e.id, e.from, e.to, e.typ, e.props); err != nil {
			t.Fatalf("seed %s: %v", e.id, err)
		}
	}

	pg := sqldialect.For(sqldialect.Postgres)
	for _, tc := range []struct {
		name string
		at   time.Time
		want string // the to_node_id expected, "" for none
	}{
		{"inside the closed interval", start.Add(24 * time.Hour), "entity:beijing"},
		{"the instant the first starts", start, "entity:beijing"},
		{"the handover instant belongs to the new fact", end, "entity:chengdu"},
		{"long after the handover", end.AddDate(1, 0, 0), "entity:chengdu"},
		{"before anything was true", start.Add(-time.Hour), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query, args := asOfQuery(pg, tc.at, TemporalFilter{Type: "lives_in"})
			rows, err := db.QueryContext(ctx, query, args...)
			if err != nil {
				t.Fatalf("the PostgreSQL form of the query does not run: %v\n%s", err, query)
			}
			defer rows.Close()

			var found []string
			for rows.Next() {
				var from, to, etype string
				var vf, vt, rec sql.NullString
				if err := rows.Scan(&from, &to, &etype, &vf, &vt, &rec); err != nil {
					t.Fatalf("scan: %v", err)
				}
				found = append(found, to)
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("rows: %v", err)
			}

			if tc.want == "" {
				if len(found) != 0 {
					t.Errorf("at %s: found %v, want nothing", tc.at.Format(time.RFC3339), found)
				}
				return
			}
			if len(found) != 1 {
				t.Fatalf("at %s: found %v, want exactly [%s]", tc.at.Format(time.RFC3339), found, tc.want)
			}
			if found[0] != tc.want {
				t.Errorf("at %s: %s, want %s", tc.at.Format(time.RFC3339), found[0], tc.want)
			}
		})
	}
}

// Both dialects have to produce the placeholder style their driver expects, or
// the query fails at the driver rather than the database.
func TestAsOfQueryIsReboundPerDialect(t *testing.T) {
	at := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	filter := TemporalFilter{From: "Leo", Type: "lives_in"}

	lite, liteArgs := asOfQuery(sqldialect.For(sqldialect.SQLite), at, filter)
	pg, pgArgs := asOfQuery(sqldialect.For(sqldialect.Postgres), at, filter)

	if len(liteArgs) != len(pgArgs) || len(pgArgs) != 4 {
		t.Fatalf("arg counts differ: sqlite %d, postgres %d", len(liteArgs), len(pgArgs))
	}
	if !contains(lite, "json_extract(") || contains(lite, "->>") {
		t.Errorf("the SQLite query is not SQLite:\n%s", lite)
	}
	if !contains(pg, "->>") || contains(pg, "json_extract(") {
		t.Errorf("the PostgreSQL query is not PostgreSQL:\n%s", pg)
	}
	if contains(pg, "?") {
		t.Errorf("the PostgreSQL query still carries a `?` placeholder:\n%s", pg)
	}
	if !contains(pg, "$4") {
		t.Errorf("the PostgreSQL query did not number all four placeholders:\n%s", pg)
	}
}

func contains(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
