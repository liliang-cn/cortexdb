package graph

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// SPARQL 1.1 behaviour that the embedded subset promises, run on both
// backends. Every assertion is on exact values: a query engine that returns a
// plausible answer is the failure mode these tests exist to catch.

const sparqlSpecPrefixes = `
PREFIX ex: <https://example.com/>
PREFIX xsd: <http://www.w3.org/2001/XMLSchema#>
PREFIX rdf: <http://www.w3.org/1999/02/22-rdf-syntax-ns#>
`

const (
	specEx  = "https://example.com/"
	specXSD = "http://www.w3.org/2001/XMLSchema#"
)

// loadSPARQLSpecFixture writes a small dataset: people with names and ages in
// the unnamed default graph, and "knows" edges split across two named graphs,
// with one edge deliberately present in both so a merge has something to
// collapse.
func loadSPARQLSpecFixture(t *testing.T, store *GraphStore) {
	t.Helper()
	ctx := context.Background()
	if err := store.InitGraphSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}
	iri := func(local string) RDFTerm { return NewIRI(specEx + local) }
	integer := func(v string) RDFTerm { return NewTypedLiteral(v, specXSD+"integer") }
	g1 := iri("g1")
	g2 := iri("g2")
	triples := []*RDFTriple{
		{Subject: iri("alice"), Predicate: iri("name"), Object: NewLiteral("Alice")},
		{Subject: iri("alice"), Predicate: iri("age"), Object: integer("30")},
		{Subject: iri("bob"), Predicate: iri("name"), Object: NewLiteral("Bob")},
		{Subject: iri("bob"), Predicate: iri("age"), Object: integer("9")},
		{Subject: iri("carol"), Predicate: iri("name"), Object: NewLangLiteral("李小龙", "zh")},
		{Subject: iri("carol"), Predicate: iri("age"), Object: integer("100")},
		{Subject: iri("dave"), Predicate: iri("name"), Object: NewLiteral("Dave")},
		{Subject: iri("alice"), Predicate: iri("knows"), Object: iri("bob"), Graph: &g1},
		{Subject: iri("alice"), Predicate: iri("label"), Object: NewLiteral("in g1"), Graph: &g1},
		{Subject: iri("bob"), Predicate: iri("knows"), Object: iri("carol"), Graph: &g2},
		{Subject: iri("alice"), Predicate: iri("knows"), Object: iri("bob"), Graph: &g2},
	}
	for _, triple := range triples {
		if err := store.UpsertTriple(ctx, triple); err != nil {
			t.Fatalf("upsert %s: %v", triple.String(), err)
		}
	}
}

func runSPARQL(t *testing.T, store *GraphStore, query string) *SPARQLResult {
	t.Helper()
	result, err := store.ExecuteSPARQL(context.Background(), sparqlSpecPrefixes+query)
	if err != nil {
		t.Fatalf("query failed: %v\n%s", err, query)
	}
	return result
}

// column renders one variable across all rows, in row order, as RDF syntax
// ("unbound" for a missing value), so an assertion checks kind, lexical form,
// language and datatype at once.
func column(result *SPARQLResult, variable string) []string {
	out := make([]string, 0, len(result.Bindings))
	for _, row := range result.Bindings {
		value, ok := row[variable]
		if !ok {
			out = append(out, "unbound")
			continue
		}
		out = append(out, value.String())
	}
	return out
}

func assertColumn(t *testing.T, result *SPARQLResult, variable string, want ...string) {
	t.Helper()
	got := column(result, variable)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("?%s = %q, want %q", variable, got, want)
	}
}

func specIRI(local string) string { return "<" + specEx + local + ">" }
func specInt(v string) string     { return `"` + v + `"^^<` + specXSD + `integer>` }
func specDec(v string) string     { return `"` + v + `"^^<` + specXSD + `decimal>` }
func specBool(v bool) string {
	return `"` + strconv.FormatBool(v) + `"^^<` + specXSD + `boolean>`
}

func TestFromMakesTheDefaultGraphTheMergeOfTheListedGraphs(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadSPARQLSpecFixture(t, b.store)

			// Without a dataset clause the default graph is the unnamed graph,
			// so edges that live only in named graphs are not visible.
			none := runSPARQL(t, b.store, `SELECT ?s ?o WHERE { ?s ex:knows ?o }`)
			if none.Count != 0 {
				t.Fatalf("no dataset clause: got %d rows, want 0", none.Count)
			}

			one := runSPARQL(t, b.store, `SELECT ?s ?o FROM ex:g1 WHERE { ?s ex:knows ?o }`)
			assertColumn(t, one, "s", specIRI("alice"))
			assertColumn(t, one, "o", specIRI("bob"))

			// alice-knows-bob is in both graphs; the merge holds it once.
			merged := runSPARQL(t, b.store, `SELECT ?s ?o FROM ex:g1 FROM <https://example.com/g2> WHERE { ?s ex:knows ?o } ORDER BY ?s`)
			assertColumn(t, merged, "s", specIRI("alice"), specIRI("bob"))
			assertColumn(t, merged, "o", specIRI("bob"), specIRI("carol"))

			// The unnamed graph is not part of a declared default graph.
			ages := runSPARQL(t, b.store, `SELECT ?s FROM ex:g1 WHERE { ?s ex:age ?a }`)
			if ages.Count != 0 {
				t.Fatalf("FROM ex:g1 leaked unnamed-graph triples: %+v", ages.Bindings)
			}

			// A property path walks the merged graph, crossing from g1 into g2.
			path := runSPARQL(t, b.store, `SELECT ?o FROM ex:g1 FROM ex:g2 WHERE { ex:alice ex:knows+ ?o } ORDER BY ?o`)
			assertColumn(t, path, "o", specIRI("bob"), specIRI("carol"))

			ask := runSPARQL(t, b.store, `ASK FROM ex:g2 { ex:bob ex:knows ex:carol }`)
			if !ask.Boolean {
				t.Fatalf("ASK FROM ex:g2 should see bob knows carol")
			}
			askMissing := runSPARQL(t, b.store, `ASK FROM ex:g1 WHERE { ex:bob ex:knows ex:carol }`)
			if askMissing.Boolean {
				t.Fatalf("ASK FROM ex:g1 should not see a g2-only edge")
			}

			construct := runSPARQL(t, b.store, `CONSTRUCT { ?s ex:linked ?o } FROM ex:g2 WHERE { ?s ex:knows ?o } ORDER BY ?s`)
			if construct.Count != 2 {
				t.Fatalf("CONSTRUCT FROM ex:g2: got %d triples, want 2: %+v", construct.Count, construct.Triples)
			}

			describe := runSPARQL(t, b.store, `DESCRIBE ex:carol FROM ex:g2`)
			if describe.Count != 1 || describe.Triples[0].Subject.Value != specEx+"bob" {
				t.Fatalf("DESCRIBE FROM ex:g2 should describe carol from g2 alone, got %+v", describe.Triples)
			}
		})
	}
}

func TestFromNamedRestrictsWhichGraphsAGraphPatternCanBind(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadSPARQLSpecFixture(t, b.store)

			all := runSPARQL(t, b.store, `SELECT DISTINCT ?g WHERE { GRAPH ?g { ?s ex:knows ?o } } ORDER BY ?g`)
			assertColumn(t, all, "g", specIRI("g1"), specIRI("g2"))

			named := runSPARQL(t, b.store, `SELECT DISTINCT ?g FROM NAMED ex:g2 WHERE { GRAPH ?g { ?s ex:knows ?o } }`)
			assertColumn(t, named, "g", specIRI("g2"))

			constant := runSPARQL(t, b.store, `SELECT ?s FROM NAMED ex:g2 WHERE { GRAPH ex:g1 { ?s ex:knows ?o } }`)
			if constant.Count != 0 {
				t.Fatalf("GRAPH ex:g1 outside FROM NAMED matched: %+v", constant.Bindings)
			}

			// FROM NAMED alone leaves the default graph empty (SPARQL 1.1 §13.2).
			emptyDefault := runSPARQL(t, b.store, `SELECT ?s FROM NAMED ex:g2 WHERE { ?s ex:name ?n }`)
			if emptyDefault.Count != 0 {
				t.Fatalf("FROM NAMED alone should leave the default graph empty, got %+v", emptyDefault.Bindings)
			}

			// FROM alone leaves no named graphs.
			noNamed := runSPARQL(t, b.store, `SELECT ?g FROM ex:g1 WHERE { GRAPH ?g { ?s ?p ?o } }`)
			if noNamed.Count != 0 {
				t.Fatalf("FROM alone should leave no named graphs, got %+v", noNamed.Bindings)
			}

			both := runSPARQL(t, b.store, `SELECT ?n ?g FROM ex:g1 FROM NAMED ex:g2 WHERE { ?s ex:label ?n . GRAPH ?g { ?s ex:knows ?o } }`)
			assertColumn(t, both, "n", `"in g1"`)
			assertColumn(t, both, "g", specIRI("g2"))
		})
	}
}

func TestConstructTemplatesCanWriteQuadsIntoNamedGraphs(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadSPARQLSpecFixture(t, b.store)

			result := runSPARQL(t, b.store, `
CONSTRUCT {
	GRAPH ex:out { ?s ex:copied ?n }
	?s ex:plain ?n .
	GRAPH ?g { ?s ex:seenIn ?g }
}
WHERE {
	?s ex:name ?n .
	GRAPH ?g { ?s ex:label ?l }
}`)
			got := make([]string, 0, len(result.Triples))
			for _, triple := range result.Triples {
				got = append(got, triple.String())
			}
			want := []string{
				`<https://example.com/alice> <https://example.com/copied> "Alice" <https://example.com/out> .`,
				`<https://example.com/alice> <https://example.com/plain> "Alice" .`,
				`<https://example.com/alice> <https://example.com/seenIn> <https://example.com/g1> <https://example.com/g1> .`,
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("CONSTRUCT quads:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			if result.Count != 3 {
				t.Fatalf("count = %d, want 3", result.Count)
			}

			short := runSPARQL(t, b.store, `CONSTRUCT WHERE { ex:bob ex:age ?a }`)
			if short.Count != 1 || short.Triples[0].Object.Value != "9" {
				t.Fatalf("CONSTRUCT WHERE short form: %+v", short.Triples)
			}
		})
	}
}

// Each case is one expression evaluated against a single empty solution. The
// expected value is the RDF rendering of the result, or "unbound" where the
// spec says evaluation is an error (a BIND/SELECT expression error leaves the
// variable unbound).
func TestBuiltInFunctionsFollowSPARQL11(t *testing.T) {
	sha := `"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"`
	cases := []struct {
		expr string
		want string
	}{
		// term tests and accessors
		{`isIRI(ex:a)`, specBool(true)},
		{`isURI("a")`, specBool(false)},
		{`isBlank(BNODE())`, specBool(true)},
		{`isBlank(ex:a)`, specBool(false)},
		{`isLiteral("a"@en)`, specBool(true)},
		{`isLiteral(ex:a)`, specBool(false)},
		{`isNumeric(12)`, specBool(true)},
		{`isNumeric("12")`, specBool(false)},
		{`isNumeric("1x"^^xsd:integer)`, specBool(false)},
		{`STR(ex:a)`, `"https://example.com/a"`},
		{`STR("7"^^xsd:integer)`, `"7"`},
		{`STR(BNODE())`, "unbound"},
		{`LANG("chat"@fr)`, `"fr"`},
		{`LANG("chat")`, `""`},
		{`LANG(ex:a)`, "unbound"},
		{`DATATYPE("x")`, "<" + specXSD + "string>"},
		{`DATATYPE("x"@en)`, "<http://www.w3.org/1999/02/22-rdf-syntax-ns#langString>"},
		{`DATATYPE(1.5)`, "<" + specXSD + "decimal>"},
		{`DATATYPE(ex:a)`, "unbound"},
		{`IRI("https://example.com/x")`, specIRI("x")},
		{`URI(ex:y)`, specIRI("y")},
		{`IRI(5)`, "unbound"},
		{`LANGMATCHES(LANG("x"@en-us), "en")`, specBool(true)},
		{`LANGMATCHES("en", "EN-US")`, specBool(false)},
		{`LANGMATCHES("fr", "*")`, specBool(true)},
		{`LANGMATCHES("", "*")`, specBool(false)},
		{`sameTerm(1, 1.0)`, specBool(false)},
		{`sameTerm(ex:a, ex:a)`, specBool(true)},
		{`1 = 1.0`, specBool(true)},
		{`"a" = "a"@en`, "unbound"},
		{`BOUND(?nothing)`, specBool(false)},
		{`STRDT("5", xsd:integer)`, specInt("5")},
		{`STRDT("5"@en, xsd:integer)`, "unbound"},
		{`STRLANG("chat", "fr")`, `"chat"@fr`},
		{`STRLANG("chat"@en, "fr")`, "unbound"},
		{`2 IN (1, 2)`, specBool(true)},
		{`3 IN (1, 2)`, specBool(false)},
		{`3 NOT IN (1, 2)`, specBool(true)},
		{`1 IN ()`, specBool(false)},
		{`1 IN (ABS("x"), 1)`, specBool(true)},
		{`2 IN (ABS("x"), 1)`, "unbound"},

		// strings: codepoints, not bytes
		{`STRLEN("李小龙")`, specInt("3")},
		{`STRLEN("abc"@en)`, specInt("3")},
		{`SUBSTR("李小龙功夫", 2, 2)`, `"小龙"`},
		{`SUBSTR("李小龙"@zh, 2)`, `"小龙"@zh`},
		{`SUBSTR("abc", 0, 2)`, `"a"`},
		{`SUBSTR("abc", 1.5, 1)`, `"b"`},
		{`SUBSTR("abc", 5)`, `""`},
		{`SUBSTR(ex:a, 1)`, "unbound"},
		{`UCASE("abc"@en)`, `"ABC"@en`},
		{`LCASE("ÀBC")`, `"àbc"`},
		{`STRSTARTS("foobar", "foo")`, specBool(true)},
		{`STRSTARTS("foobar"@en, "bar")`, specBool(false)},
		{`STRSTARTS("abc"@en, "a"@fr)`, "unbound"},
		{`STRSTARTS("abc", "a"@en)`, "unbound"},
		{`STRENDS("foobar", "bar")`, specBool(true)},
		{`CONTAINS("李小龙", "小")`, specBool(true)},
		{`CONTAINS("abc"@en, "b"@en)`, specBool(true)},
		{`STRBEFORE("abc", "b")`, `"a"`},
		{`STRBEFORE("abc"@en, "bc")`, `"a"@en`},
		{`STRBEFORE("abc"@en, "z")`, `""`},
		{`STRBEFORE("abc"@en, "")`, `""@en`},
		{`STRAFTER("abc", "b")`, `"c"`},
		{`STRAFTER("abc"@en, "")`, `"abc"@en`},
		{`STRAFTER("李小龙", "小")`, `"龙"`},
		{`CONCAT("a", "b")`, `"ab"`},
		{`CONCAT("a"@en, "b"@en)`, `"ab"@en`},
		{`CONCAT("a"@en, "b")`, `"ab"`},
		{`CONCAT("a"^^xsd:string, "b"^^xsd:string)`, `"ab"^^<` + specXSD + `string>`},
		{`CONCAT()`, `""`},
		{`CONCAT("a", ex:b)`, "unbound"},
		{`REPLACE("abcd", "b", "Z")`, `"aZcd"`},
		{`REPLACE("abab", "B", "Z", "i")`, `"aZaZ"`},
		{`REPLACE("abc", "(b)", "[$1]")`, `"a[b]c"`},
		{`REPLACE("a.c", ".", "!", "q")`, `"a!c"`},
		{`REPLACE("abc"@en, "b", "")`, `"ac"@en`},
		{`REPLACE("abc", "x*", "-")`, "unbound"},
		{`REPLACE("abc", "b", "Z", "k")`, "unbound"},
		{`ENCODE_FOR_URI("Los Angeles 李")`, `"Los%20Angeles%20%E6%9D%8E"`},
		{`ENCODE_FOR_URI("a-b_c.d~")`, `"a-b_c.d~"`},
		{`REGEX("Alice", "^a", "i")`, specBool(true)},
		{`REGEX("Alice", "^a")`, specBool(false)},
		{`REGEX("a\nb", "^b", "m")`, specBool(true)},
		{`REGEX("abc", "(")`, "unbound"},

		// numerics keep their datatype
		{`ABS(-3)`, specInt("3")},
		{`ABS(-1.5)`, specDec("1.5")},
		{`ABS("x")`, "unbound"},
		{`ROUND(2.5)`, specDec("3")},
		{`ROUND(-2.5)`, specDec("-2")},
		{`ROUND(7)`, specInt("7")},
		{`CEIL(1.2)`, specDec("2")},
		{`FLOOR(-1.2)`, specDec("-2")},
		{`1 + 2`, specInt("3")},
		{`7 / 2`, specDec("3.5")},
		{`1 / 0`, "unbound"},
		{`1 + "a"`, "unbound"},

		// hashes
		{`MD5("abc")`, `"900150983cd24fb0d6963f7d28e17f72"`},
		{`SHA1("abc")`, `"a9993e364706816aba3e25717850c26c9cd0d89d"`},
		{`SHA256("abc")`, sha},
		{`SHA256("abc"^^xsd:string)`, sha},
		{`MD5("abc"@en)`, "unbound"},

		// dates
		{`YEAR("2024-03-15T10:20:30Z"^^xsd:dateTime)`, specInt("2024")},
		{`MONTH("2024-03-15T10:20:30Z"^^xsd:dateTime)`, specInt("3")},
		{`DAY("2024-03-15T10:20:30+08:00"^^xsd:dateTime)`, specInt("15")},
		{`HOURS("2024-03-15T10:20:30"^^xsd:dateTime)`, specInt("10")},
		{`MINUTES("2024-03-15T10:20:30Z"^^xsd:dateTime)`, specInt("20")},
		{`SECONDS("2024-03-15T10:20:30.5Z"^^xsd:dateTime)`, specDec("30.5")},
		{`TZ("2024-03-15T10:20:30-05:00"^^xsd:dateTime)`, `"-05:00"`},
		{`TZ("2024-03-15T10:20:30"^^xsd:dateTime)`, `""`},
		{`TIMEZONE("2024-03-15T10:20:30Z"^^xsd:dateTime)`, `"PT0S"^^<` + specXSD + `dayTimeDuration>`},
		{`TIMEZONE("2024-03-15T10:20:30-05:30"^^xsd:dateTime)`, `"-PT5H30M"^^<` + specXSD + `dayTimeDuration>`},
		{`YEAR("2024-03-15")`, "unbound"},
		{`YEAR("nonsense"^^xsd:dateTime)`, "unbound"},

		// conditionals and logic with errors
		{`IF(ABS("x") > 0, "yes", "no")`, "unbound"},
		{`COALESCE(ABS("x"), ?nothing, "fallback")`, `"fallback"`},
		{`ABS("x") > 0 || true`, specBool(true)},
		{`true || ABS("x") > 0`, specBool(true)},
		{`ABS("x") > 0 && false`, specBool(false)},
		{`ABS("x") > 0 && true`, "unbound"},
		{`!(ABS("x") > 0)`, "unbound"},
		{`"b" > "a"`, specBool(true)},
		// Compatibility: an untyped numeric string meets a typed number as a
		// number, but two untyped strings compare as strings.
		{`"10" > 9`, specBool(true)},
		{`"10" < "9"`, specBool(true)},
		{`ex:a < ex:b`, "unbound"},
	}
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			if err := b.store.InitGraphSchema(context.Background()); err != nil {
				t.Fatalf("schema: %v", err)
			}
			for _, tc := range cases {
				result := runSPARQL(t, b.store, `SELECT (`+tc.expr+` AS ?v) WHERE { }`)
				if result.Count != 1 {
					t.Fatalf("%s: got %d rows, want 1", tc.expr, result.Count)
				}
				if got := column(result, "v")[0]; got != tc.want {
					t.Errorf("%s = %s, want %s", tc.expr, got, tc.want)
				}
			}
		})
	}
}

func TestNondeterministicFunctionsProduceWellFormedValues(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			if err := b.store.InitGraphSchema(context.Background()); err != nil {
				t.Fatalf("schema: %v", err)
			}
			result := runSPARQL(t, b.store, `
SELECT (RAND() AS ?r) (NOW() AS ?a) (NOW() AS ?b) (YEAR(NOW()) AS ?y)
       (BNODE() AS ?n1) (BNODE() AS ?n2) (BNODE("k") AS ?k1) (BNODE("k") AS ?k2)
       (STRLEN(STRUUID()) AS ?len) (STRSTARTS(STR(UUID()), "urn:uuid:") AS ?urn)
WHERE { }`)
			row := result.Bindings[0]
			r, err := strconv.ParseFloat(row["r"].Value, 64)
			if err != nil || r < 0 || r >= 1 || row["r"].Datatype != specXSD+"double" {
				t.Fatalf("RAND() = %+v, want an xsd:double in [0,1)", row["r"])
			}
			if row["a"] != row["b"] || row["a"].Datatype != specXSD+"dateTime" {
				t.Fatalf("NOW() must be one xsd:dateTime per query: %+v vs %+v", row["a"], row["b"])
			}
			if y, _ := strconv.Atoi(row["y"].Value); y < 2024 {
				t.Fatalf("YEAR(NOW()) = %+v", row["y"])
			}
			if row["n1"].Kind != RDFTermBlankNode || row["n1"] == row["n2"] {
				t.Fatalf("BNODE() must mint distinct blank nodes: %+v %+v", row["n1"], row["n2"])
			}
			if row["k1"] != row["k2"] || row["k1"] == row["n1"] {
				t.Fatalf("BNODE(\"k\") must be stable for one string: %+v %+v", row["k1"], row["k2"])
			}
			if row["len"].Value != "36" || row["urn"].Value != "true" {
				t.Fatalf("UUID/STRUUID malformed: %+v %+v", row["len"], row["urn"])
			}
		})
	}
}

func TestExpressionErrorsDropTheRowInFilterAndLeaveTheVariableUnboundInBind(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			if err := b.store.InitGraphSchema(context.Background()); err != nil {
				t.Fatalf("schema: %v", err)
			}
			filtered := runSPARQL(t, b.store, `SELECT ?x WHERE { VALUES ?x { 1 "a" ex:c 3 } FILTER(ABS(?x) >= 1) }`)
			assertColumn(t, filtered, "x", specInt("1"), specInt("3"))

			// An error on one side of || does not poison a true other side.
			or := runSPARQL(t, b.store, `SELECT ?x WHERE { VALUES ?x { 1 "a" } FILTER(ABS(?x) > 0 || isLiteral(?x)) }`)
			assertColumn(t, or, "x", specInt("1"), `"a"`)

			bound := runSPARQL(t, b.store, `SELECT ?x ?y WHERE { VALUES ?x { -4 "a" 0 } BIND(ABS(?x) AS ?y) }`)
			assertColumn(t, bound, "x", specInt("-4"), `"a"`, specInt("0"))
			assertColumn(t, bound, "y", specInt("4"), "unbound", specInt("0"))

			divided := runSPARQL(t, b.store, `SELECT ?y WHERE { VALUES ?x { 2 0 } BIND(10 / ?x AS ?y) }`)
			assertColumn(t, divided, "y", specDec("5"), "unbound")

			// A BIND of an unbound variable keeps the row.
			optional := runSPARQL(t, b.store, `SELECT ?x ?y WHERE { VALUES ?x { 1 } BIND(?missing AS ?y) }`)
			assertColumn(t, optional, "y", "unbound")

			regex := runSPARQL(t, b.store, `SELECT ?x WHERE { VALUES ?x { "a(" "b" } FILTER(REGEX(?x, "(")) }`)
			if regex.Count != 0 {
				t.Fatalf("an invalid regex is an evaluation error, want 0 rows: %+v", regex.Bindings)
			}
			long := runSPARQL(t, b.store, `SELECT ?x WHERE { VALUES ?x { "aaa" } FILTER(REGEX(?x, "`+strings.Repeat("a", sparqlMaxRegexPatternBytes+1)+`")) }`)
			if long.Count != 0 {
				t.Fatalf("an over-long regex must be rejected per row, got %+v", long.Bindings)
			}

			ebv := runSPARQL(t, b.store, `SELECT ?x WHERE { VALUES ?x { ex:a "" "s" 0 2 true "false"^^xsd:boolean } FILTER(?x) }`)
			assertColumn(t, ebv, "x", `"s"`, specInt("2"), specBool(true))

			// Built-in calls and nested brackets parse where SPARQL allows them.
			grammar := runSPARQL(t, b.store, `SELECT ?x ?big WHERE { VALUES ?x { 1 5 } FILTER regex(STR(?x), "5|1") FILTER((?x + 1) > 1) BIND(?x > 2 AS ?big) }`)
			assertColumn(t, grammar, "big", specBool(false), specBool(true))
		})
	}
}

func TestAggregatesHonourDistinctAndCountOverNothingIsZero(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadSPARQLSpecFixture(t, b.store)

			agg := runSPARQL(t, b.store, `
SELECT (COUNT(?x) AS ?n) (COUNT(DISTINCT ?x) AS ?dn) (COUNT(*) AS ?all) (COUNT(DISTINCT *) AS ?dall)
       (SUM(?x) AS ?sum) (SUM(DISTINCT ?x) AS ?dsum) (AVG(DISTINCT ?x) AS ?davg)
       (GROUP_CONCAT(DISTINCT ?x; SEPARATOR="|") AS ?cat) (MIN(?x) AS ?min) (MAX(?x) AS ?max)
WHERE { VALUES ?x { 1 1 2 10 } }`)
			assertColumn(t, agg, "n", specInt("4"))
			assertColumn(t, agg, "dn", specInt("3"))
			assertColumn(t, agg, "all", specInt("4"))
			assertColumn(t, agg, "dall", specInt("3"))
			assertColumn(t, agg, "sum", specInt("14"))
			assertColumn(t, agg, "dsum", specInt("13"))
			assertColumn(t, agg, "davg", specDec("4.333333333333333"))
			assertColumn(t, agg, "cat", `"1|2|10"`)
			assertColumn(t, agg, "min", specInt("1"))
			assertColumn(t, agg, "max", specInt("10"))

			empty := runSPARQL(t, b.store, `SELECT (COUNT(*) AS ?c) (SUM(?o) AS ?sum) (AVG(?o) AS ?a) (MAX(?o) AS ?m) WHERE { ?s ex:nothing ?o }`)
			assertColumn(t, empty, "c", specInt("0"))
			assertColumn(t, empty, "sum", specInt("0"))
			assertColumn(t, empty, "a", specInt("0"))
			assertColumn(t, empty, "m", "unbound")

			grouped := runSPARQL(t, b.store, `SELECT ?s (COUNT(*) AS ?c) WHERE { ?s ex:nothing ?o } GROUP BY ?s`)
			if grouped.Count != 0 {
				t.Fatalf("GROUP BY over nothing has no groups, got %+v", grouped.Bindings)
			}

			// A SUM over a non-number is an error for that group, not the query.
			bad := runSPARQL(t, b.store, `SELECT ?k (SUM(?x) AS ?s) WHERE { VALUES (?k ?x) { ("a" 1) ("a" 2) ("b" "x") } } GROUP BY ?k ORDER BY ?k`)
			assertColumn(t, bad, "k", `"a"`, `"b"`)
			assertColumn(t, bad, "s", specInt("3"), "unbound")
		})
	}
}

func TestOrderByDistinctAndReducedFollowSolutionModifierOrder(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadSPARQLSpecFixture(t, b.store)

			// Numbers order by value, not by their lexical form.
			byAge := runSPARQL(t, b.store, `SELECT ?age WHERE { ?s ex:age ?age } ORDER BY ?age`)
			assertColumn(t, byAge, "age", specInt("9"), specInt("30"), specInt("100"))

			byExpr := runSPARQL(t, b.store, `SELECT ?s WHERE { ?s ex:age ?age } ORDER BY DESC(?age * -1)`)
			assertColumn(t, byExpr, "s", specIRI("bob"), specIRI("alice"), specIRI("carol"))

			// ORDER BY sees SELECT expression aliases.
			byAlias := runSPARQL(t, b.store, `SELECT ?s (STRLEN(?n) AS ?len) WHERE { ?s ex:name ?n } ORDER BY DESC(?len) ?s`)
			assertColumn(t, byAlias, "len", specInt("5"), specInt("4"), specInt("3"), specInt("3"))
			assertColumn(t, byAlias, "s", specIRI("alice"), specIRI("dave"), specIRI("bob"), specIRI("carol"))

			byAggregateAlias := runSPARQL(t, b.store, `SELECT ?k (COUNT(?x) AS ?c) WHERE { VALUES (?k ?x) { ("a" 1) ("b" 1) ("b" 2) ("c" 1) ("c" 2) ("c" 3) } } GROUP BY ?k ORDER BY DESC(?c)`)
			assertColumn(t, byAggregateAlias, "k", `"c"`, `"b"`, `"a"`)

			// Unbound sorts first ascending.
			unbound := runSPARQL(t, b.store, `SELECT ?s ?age WHERE { ?s ex:name ?n OPTIONAL { ?s ex:age ?age } } ORDER BY ?age`)
			assertColumn(t, unbound, "s", specIRI("dave"), specIRI("bob"), specIRI("alice"), specIRI("carol"))

			// DISTINCT happens before LIMIT, grouped or not.
			distinctGrouped := runSPARQL(t, b.store, `SELECT DISTINCT ?k WHERE { VALUES (?g ?k) { (1 "a") (2 "a") (3 "b") } } GROUP BY ?g ?k ORDER BY ?k LIMIT 2`)
			assertColumn(t, distinctGrouped, "k", `"a"`, `"b"`)

			reduced := runSPARQL(t, b.store, `SELECT REDUCED ?k WHERE { VALUES ?k { "a" "a" "b" } } ORDER BY ?k`)
			assertColumn(t, reduced, "k", `"a"`, `"b"`)

			// Mixed literals order by class first (numbers before strings),
			// so the order is total and the same for MIN and MAX.
			mixed := runSPARQL(t, b.store, `SELECT ?x WHERE { VALUES ?x { "1a" 2 "10" 1.5 ex:i } } ORDER BY ?x`)
			assertColumn(t, mixed, "x", specIRI("i"), specDec("1.5"), specInt("2"), `"10"`, `"1a"`)
			minMax := runSPARQL(t, b.store, `SELECT (MIN(?x) AS ?min) (MAX(?x) AS ?max) WHERE { VALUES ?x { "1a" 2 "10" 1.5 } }`)
			assertColumn(t, minMax, "min", specDec("1.5"))
			assertColumn(t, minMax, "max", `"1a"`)
		})
	}
}

// Counts whose lexical order differs from their numeric order (101 > 12 > 3,
// but "3" > "12" > "101" as text) ordered by the aggregate's alias, with a
// LIMIT that must cut the ordered list, not the unordered one. This is the
// shape of "top predicates by count" against a real graph.
func TestOrderByAnAggregateAliasSortsNumericallyBeforeLimit(t *testing.T) {
	var rows strings.Builder
	for key, n := range map[string]int{"few": 3, "some": 12, "many": 101} {
		for i := 0; i < n; i++ {
			rows.WriteString(`("` + key + `" ` + strconv.Itoa(i) + `) `)
		}
	}
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			if err := b.store.InitGraphSchema(context.Background()); err != nil {
				t.Fatalf("schema: %v", err)
			}
			byCount := runSPARQL(t, b.store, `SELECT ?k (COUNT(*) AS ?n) WHERE { VALUES (?k ?i) { `+rows.String()+`} } GROUP BY ?k ORDER BY DESC(?n) LIMIT 2`)
			assertColumn(t, byCount, "k", `"many"`, `"some"`)
			assertColumn(t, byCount, "n", specInt("101"), specInt("12"))

			ascending := runSPARQL(t, b.store, `SELECT ?k (COUNT(?i) AS ?n) WHERE { VALUES (?k ?i) { `+rows.String()+`} } GROUP BY ?k ORDER BY ?n`)
			assertColumn(t, ascending, "n", specInt("3"), specInt("12"), specInt("101"))

			byExpression := runSPARQL(t, b.store, `SELECT ?k WHERE { VALUES (?k ?i) { `+rows.String()+`} } GROUP BY ?k ORDER BY DESC(COUNT(?i)) LIMIT 1`)
			assertColumn(t, byExpression, "k", `"many"`)
		})
	}
}

// The property-graph projection is part of the engine's default graph, and a
// graph like any other once a query declares its dataset: FROM can select it,
// and a FROM that does not name it leaves it out.
func TestFromCanNameThePropertyGraphProjectionOrLeaveItOut(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedProjectionFixture(t, ctx, b.store)

			byDefault := sparqlColumn(t, b.store, `SELECT ?n WHERE { ?s cxp:name ?n }`, "n")
			if want := sortedCopy("CortexDB", "dell", "中文"); strings.Join(byDefault, ",") != strings.Join(want, ",") {
				t.Fatalf("projection in the default graph: got %v want %v", byDefault, want)
			}
			named := sparqlColumn(t, b.store, `SELECT ?n FROM <urn:cortexdb:graph:property> WHERE { ?s cxp:name ?n }`, "n")
			if strings.Join(named, ",") != strings.Join(byDefault, ",") {
				t.Fatalf("FROM the projection: got %v want %v", named, byDefault)
			}
			if got := sparqlColumn(t, b.store, `SELECT ?n FROM <urn:cortexdb:graph:property> WHERE { ?s foaf:name ?n }`, "n"); len(got) != 0 {
				t.Fatalf("FROM the projection leaked the unnamed graph: %v", got)
			}
			if got := sparqlColumn(t, b.store, `SELECT ?n FROM <http://example.org/other> WHERE { ?s cxp:name ?n }`, "n"); len(got) != 0 {
				t.Fatalf("FROM another graph leaked the projection: %v", got)
			}
			graphs := sparqlColumn(t, b.store, `SELECT DISTINCT ?g FROM NAMED <urn:cortexdb:graph:property> WHERE { GRAPH ?g { ?s cxp:name ?n } }`, "g")
			if strings.Join(graphs, ",") != PropertyGraphIRI {
				t.Fatalf("FROM NAMED the projection: got %v", graphs)
			}
		})
	}
}

// A variable bound to a literal can be joined into a subject position — ?rel
// ranges over every predicate, so ?y is sometimes a name — and a literal
// subject matches no triple. That is an empty solution for that row, not a
// failed query. Found on the production brain, where the first query below,
// asking what two hosts are connected to, failed outright with "rdf subject
// must be iri or blank node".
func TestALiteralBoundIntoASubjectMatchesNothingInsteadOfFailingTheQuery(t *testing.T) {
	const ex = "https://example.com/"
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			for _, tr := range []RDFTriple{
				{Subject: NewIRI(ex + "a"), Predicate: NewIRI(ex + "name"), Object: NewLiteral("A")},
				{Subject: NewIRI(ex + "a"), Predicate: NewIRI(ex + "knows"), Object: NewIRI(ex + "b")},
				{Subject: NewIRI(ex + "b"), Predicate: NewIRI(ex + "name"), Object: NewLiteral("B")},
				{Subject: NewIRI(ex + "b"), Predicate: NewIRI(ex + "knows"), Object: NewIRI(ex + "c")},
			} {
				tr := tr
				if err := b.store.UpsertTriple(ctx, &tr); err != nil {
					t.Fatalf("upsert: %v", err)
				}
			}

			joined := runSPARQL(t, b.store, `SELECT ?o WHERE { <`+ex+`a> ?rel ?y . ?y <`+ex+`name> ?o }`)
			assertColumn(t, joined, "o", `"B"`)

			valued := runSPARQL(t, b.store, `SELECT ?o WHERE { VALUES ?y { "A" <`+ex+`b> } ?y <`+ex+`name> ?o }`)
			assertColumn(t, valued, "o", `"B"`)

			pathed := runSPARQL(t, b.store, `SELECT ?z WHERE { <`+ex+`a> ?rel ?y . ?y <`+ex+`knows>+ ?z }`)
			assertColumn(t, pathed, "z", "<"+ex+"c>")
		})
	}
}
