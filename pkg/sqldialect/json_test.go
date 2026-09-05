package sqldialect

import (
	"strings"
	"testing"
)

// Every one of these existed in the codebase as SQLite-only SQL that no test
// ever ran against PostgreSQL, because the tests that covered the features
// above them ran on SQLite. What they cost, in order: all graph-mode
// retrieval (json_valid), every document deletion (json_each), and every
// inference rule re-deriving its own output (json_extract of a JSON true
// reads back as 1 on SQLite and as the text 'true' on PostgreSQL).

func TestJSONHelpersSpeakEachDatabase(t *testing.T) {
	for _, tc := range []struct {
		name    string
		build   func(Dialect) string
		sqlite  []string
		postgre []string
	}{
		{
			name:    "guarded text read",
			build:   func(d Dialect) string { return d.JSONTextGuarded("properties", "document_id") },
			sqlite:  []string{"json_valid(properties)", "json_extract(properties, '$.document_id')"},
			postgre: []string{"NULLIF(properties::text, '')", "->> 'document_id'"},
		},
		{
			name:    "boolean flag",
			build:   func(d Dialect) string { return d.JSONFlag("properties", "inferred") },
			sqlite:  []string{"json_extract(properties, '$.inferred') IN (1, 'true')"},
			postgre: []string{"IN ('true', '1')"},
		},
		{
			name:    "array containment",
			build:   func(d Dialect) string { return d.JSONArrayContains("properties", "source_document_ids") },
			sqlite:  []string{"json_each(", "je.value = ?"},
			postgre: []string{"@> to_jsonb(?::text)"},
		},
		{
			// The only helper here that is a join rather than an expression,
			// and the only one whose failure mode is "the statement raises on
			// one odd row" rather than "the value reads back wrong".
			name:    "key enumeration",
			build:   func(d Dialect) string { return d.JSONEachEntry("n.properties") },
			sqlite:  []string{", json_each(", "json_type(n.properties) = 'object'", ") je"},
			postgre: []string{"CROSS JOIN LATERAL jsonb_each_text(", "jsonb_typeof(", "AS je(key, value)"},
		},
		{
			name:    "field write",
			build:   func(d Dialect) string { return d.JSONSet("properties", "source_document_ids") },
			sqlite:  []string{"json_set(properties, '$.source_document_ids', json(?))"},
			postgre: []string{"jsonb_set(", "'{source_document_ids}'"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lite := tc.build(For(SQLite))
			for _, want := range tc.sqlite {
				if !strings.Contains(lite, want) {
					t.Errorf("sqlite: %q missing from %s", want, lite)
				}
			}
			pg := tc.build(For(Postgres))
			for _, want := range tc.postgre {
				if !strings.Contains(pg, want) {
					t.Errorf("postgres: %q missing from %s", want, pg)
				}
			}
			// The one check that would have caught every bug in this list.
			for _, sqliteOnly := range []string{"json_valid", "json_extract", "json_each", "json_set(", "COLLATE NOCASE"} {
				if strings.Contains(pg, sqliteOnly) {
					t.Errorf("postgres expression contains SQLite-only %q: %s", sqliteOnly, pg)
				}
			}
		})
	}
}

// A helper that carries a placeholder has to carry exactly one, or the caller's
// argument list silently stops lining up with the query.
func TestPlaceholderCarryingHelpersCarryExactlyOne(t *testing.T) {
	for _, kind := range []Kind{SQLite, Postgres} {
		d := For(kind)
		for name, expr := range map[string]string{
			"JSONArrayContains": d.JSONArrayContains("properties", "ids"),
			"JSONSet":           d.JSONSet("properties", "ids"),
		} {
			if n := strings.Count(expr, "?"); n != 1 {
				t.Errorf("%s/%s has %d placeholders: %s", kind, name, n, expr)
			}
		}
		for name, expr := range map[string]string{
			"JSONTextGuarded": d.JSONTextGuarded("properties", "k"),
			"JSONFlag":        d.JSONFlag("properties", "k"),
		} {
			if strings.Contains(expr, "?") {
				t.Errorf("%s/%s takes no argument but has a placeholder: %s", kind, name, expr)
			}
		}
	}
}

func TestJSONKeyIsEscaped(t *testing.T) {
	for _, kind := range []Kind{SQLite, Postgres} {
		expr := For(kind).JSONTextGuarded("properties", "it's")
		if strings.Contains(expr, "'it's'") {
			t.Errorf("%s: unescaped quote closes the literal early: %s", kind, expr)
		}
	}
}

// The helpers are pointed at two different column types: graph_nodes.properties
// is TEXT on both backends, messages.metadata is TEXT on SQLite and jsonb on
// PostgreSQL. NULLIF of a jsonb against ” is an error — and the caller that
// hit it discards its errors by design, so recall accounting on PostgreSQL
// wrote nothing and said nothing for as long as it existed.
func TestPostgresJSONHelpersDoNotAssumeATextColumn(t *testing.T) {
	d := For(Postgres)
	for name, expr := range map[string]string{
		"JSONText":          d.JSONText("metadata", "k"),
		"JSONTextGuarded":   d.JSONTextGuarded("metadata", "k"),
		"JSONFlag":          d.JSONFlag("metadata", "k"),
		"JSONArrayContains": d.JSONArrayContains("metadata", "k"),
		"JSONSet":           d.JSONSet("metadata", "k"),
	} {
		if strings.Contains(expr, "NULLIF(metadata, '')") {
			t.Errorf("%s compares the raw column against a text literal, which "+
				"is an error when the column is jsonb: %s", name, expr)
		}
		if !strings.Contains(expr, "metadata::text") {
			t.Errorf("%s does not normalise the column to text first: %s", name, expr)
		}
	}
}

// JSONSet's result is assigned straight into a column, and the two columns it
// is aimed at have different types on PostgreSQL: graph_nodes.properties is
// TEXT, messages.metadata is jsonb. Only jsonb-into-text has an assignment
// cast, so the expression must stay jsonb to work for both.
func TestPostgresJSONSetStaysJSONB(t *testing.T) {
	expr := For(Postgres).JSONSet("metadata", "recall_count")
	if strings.HasSuffix(expr, "::text") {
		t.Errorf("cast back to text — this cannot be assigned to a jsonb column: %s", expr)
	}
}
