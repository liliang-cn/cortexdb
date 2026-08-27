package sqldialect

import (
	"errors"
	"strings"
	"testing"
)

func TestPostgresNumbersPlaceholdersInOrder(t *testing.T) {
	pg := For(Postgres)
	got := pg.Rebind(`INSERT INTO kg_triples (id, subject_value, predicate_value) VALUES (?, ?, ?)`)
	want := `INSERT INTO kg_triples (id, subject_value, predicate_value) VALUES ($1, $2, $3)`
	if got != want {
		t.Errorf("Rebind =\n  %s\nwant\n  %s", got, want)
	}
}

// The count has to keep going past nine, which a single-character conversion
// would silently get wrong — and a query with ten parameters is not unusual
// here: graph.go inserts eleven columns at a time.
func TestPlaceholdersPastNine(t *testing.T) {
	q := ""
	for i := 0; i < 12; i++ {
		q += "?,"
	}
	got := For(Postgres).Rebind(q)
	want := "$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,"
	if got != want {
		t.Errorf("Rebind = %s, want %s", got, want)
	}
}

// A question mark inside a string literal is data, not a parameter. Replacing
// it corrupts the value AND shifts every later number, so the failure lands on
// whichever rows happen to contain the character.
func TestAQuestionMarkInsideALiteralIsLeftAlone(t *testing.T) {
	pg := For(Postgres)
	got := pg.Rebind(`SELECT * FROM graph_nodes WHERE content = 'why?' AND id = ?`)
	want := `SELECT * FROM graph_nodes WHERE content = 'why?' AND id = $1`
	if got != want {
		t.Errorf("Rebind =\n  %s\nwant\n  %s", got, want)
	}

	// An escaped quote must not be read as the end of the literal.
	got = pg.Rebind(`SELECT ? WHERE a = 'it''s ok?' AND b = ?`)
	want = `SELECT $1 WHERE a = 'it''s ok?' AND b = $2`
	if got != want {
		t.Errorf("Rebind =\n  %s\nwant\n  %s", got, want)
	}

	// Quoted identifiers too.
	got = pg.Rebind(`SELECT "odd?column" FROM t WHERE id = ?`)
	want = `SELECT "odd?column" FROM t WHERE id = $1`
	if got != want {
		t.Errorf("Rebind =\n  %s\nwant\n  %s", got, want)
	}
}

func TestSQLiteLeavesTheQueryAlone(t *testing.T) {
	q := `SELECT * FROM graph_nodes WHERE id = ? AND node_type = ?`
	if got := For(SQLite).Rebind(q); got != q {
		t.Errorf("SQLite rewrote the query: %s", got)
	}
}

// The bug this layer exists to kill: the migration in graph.go matched the
// SQLite wording inline, so on PostgreSQL an idempotent ALTER TABLE would fail
// on the *second* start.
func TestEachDialectRecognisesItsOwnDuplicateColumn(t *testing.T) {
	sqlite := For(SQLite)
	pg := For(Postgres)

	sqliteErr := errors.New("SQL logic error: duplicate column name: inferred (1)")
	pgErr := errors.New(`ERROR: column "inferred" of relation "kg_triples" already exists (SQLSTATE 42701)`)

	if !sqlite.IsDuplicateColumn(sqliteErr) {
		t.Error("SQLite did not recognise its own duplicate-column error")
	}
	if !pg.IsDuplicateColumn(pgErr) {
		t.Error("PostgreSQL did not recognise its own duplicate-column error")
	}
	// The cross case is the actual bug: SQLite's matcher against Postgres's
	// wording says "not a duplicate", and the migration aborts.
	if sqlite.IsDuplicateColumn(pgErr) {
		t.Error("the SQLite matcher claimed PostgreSQL's error — the layer would be pointless")
	}
	for _, d := range []Dialect{sqlite, pg} {
		if d.IsDuplicateColumn(nil) {
			t.Errorf("%s called a nil error a duplicate column", d.Kind())
		}
		if d.IsDuplicateColumn(errors.New("connection refused")) {
			t.Errorf("%s swallowed an unrelated error", d.Kind())
		}
	}
}

func TestKindForDSN(t *testing.T) {
	cases := map[string]Kind{
		"postgres://user:pw@localhost:5432/cortex": Postgres,
		"postgresql://localhost/cortex":            Postgres,
		"host=localhost port=5432 dbname=cortex":   Postgres,
		"/Users/liliang/.cortexdb/brain.db":        SQLite,
		"brain.db":                                 SQLite,
		"":                                         SQLite,
	}
	for dsn, want := range cases {
		if got := KindForDSN(dsn); got != want {
			t.Errorf("KindForDSN(%q) = %s, want %s", dsn, got, want)
		}
	}
}

// Only the type that genuinely differs. `inferred` stays INTEGER on both:
// PostgreSQL has INTEGER, and a real BOOLEAN would break the scan into int and
// the `inferred = 1` filters for no gain.
func TestBlobTypeFollowsTheDatabase(t *testing.T) {
	if For(SQLite).BlobType() != "BLOB" || For(Postgres).BlobType() != "BYTEA" {
		t.Error("blob type does not follow the database")
	}
}

// The fourth difference: reading a field out of a JSON column. json_extract is
// SQLite's and PostgreSQL has no such function, which is what kept the
// temporal-fact layer from crossing.
func TestJSONTextSpeaksEachDatabase(t *testing.T) {
	if got := For(SQLite).JSONText("properties", "valid_from"); got != `json_extract(properties, '$.valid_from')` {
		t.Errorf("SQLite JSONText = %s", got)
	}
	if got := For(Postgres).JSONText("properties", "valid_from"); got != `(properties::jsonb ->> 'valid_from')` {
		t.Errorf("PostgreSQL JSONText = %s", got)
	}
	// Every caller passes a constant today, which is exactly when an escape is
	// easy to leave out and hard to miss later.
	for _, d := range []Dialect{For(SQLite), For(Postgres)} {
		got := d.JSONText("properties", "it's")
		if strings.Contains(got, "'s'") && !strings.Contains(got, "''s") {
			t.Errorf("%s left a quote unescaped: %s", d.Kind(), got)
		}
	}
}
