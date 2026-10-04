package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Results formats, SERVICE and graph management through the public API. The
// W3C suites check the same against the specs (the results-format and
// federation manifests, and every evaluation test's results read back); these
// pin the API's own contract.

func sparqlFixture(t *testing.T) *GraphStore {
	t.Helper()
	_, store, cleanup := setupTestGraph(t)
	t.Cleanup(cleanup)
	store.SetPropertyGraphProjection(false)
	_, err := store.ExecuteSPARQL(context.Background(), `PREFIX ex: <http://example.org/>
		INSERT DATA {
			ex:a ex:name "Ann, \"the\" first" ; ex:age 42 ; ex:tag "hi"@en--rtl ; ex:knows _:b .
			GRAPH ex:g { ex:a ex:in ex:g }
		}`)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return store
}

func TestSPARQLResultsFormats(t *testing.T) {
	store := sparqlFixture(t)
	ctx := context.Background()
	res, err := store.ExecuteSPARQL(ctx, `PREFIX ex: <http://example.org/>
		SELECT ?s ?name ?age ?tag ?friend ?none WHERE {
			?s ex:name ?name ; ex:age ?age ; ex:tag ?tag ; ex:knows ?friend
			OPTIONAL { ?s ex:missing ?none }
		}`)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer

	if err := res.WriteResults(&out, SPARQLResultsJSON); err != nil {
		t.Fatal(err)
	}
	back, err := ReadSPARQLResultsJSON(&out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(back.Vars, ",") != "s,name,age,tag,friend,none" || len(back.Bindings) != 1 {
		t.Fatalf("JSON round trip: %+v", back)
	}
	row := back.Bindings[0]
	if row["tag"].LanguageTag() != "en" || row["tag"].BaseDirection() != "rtl" || row["age"].Datatype != XSDNamespace+"integer" ||
		row["friend"].Kind != RDFTermBlankNode || row["name"].Value != `Ann, "the" first` {
		t.Fatalf("JSON terms: %+v", row)
	}
	if _, bound := row["none"]; bound {
		t.Fatal("an unbound variable was written")
	}

	out.Reset()
	if err := res.WriteResults(&out, SPARQLResultsXML); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<variable name="none"/>`, `<literal xml:lang="en" its:dir="rtl">hi</literal>`,
		`<literal datatype="http://www.w3.org/2001/XMLSchema#integer">42</literal>`, `<bnode>`, `Ann, &#34;the&#34; first`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("XML lacks %s:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := res.WriteResults(&out, SPARQLResultsCSV); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\r\n"), "\r\n")
	if len(lines) != 2 || lines[0] != "s,name,age,tag,friend,none" ||
		!strings.HasPrefix(lines[1], `http://example.org/a,"Ann, ""the"" first",42,hi,_:`) || !strings.HasSuffix(lines[1], ",") {
		t.Fatalf("CSV:\n%q", out.String())
	}

	out.Reset()
	if err := res.WriteResults(&out, SPARQLResultsTSV); err != nil {
		t.Fatal(err)
	}
	lines = strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if lines[0] != "?s\t?name\t?age\t?tag\t?friend\t?none" || !strings.Contains(lines[1], "\t42\t\"hi\"@en--rtl\t_:") {
		t.Fatalf("TSV:\n%q", out.String())
	}

	if SPARQLResultsJSON.MediaType() != "application/sparql-results+json" || SPARQLResultsTSV.MediaType() == "" {
		t.Error("media types")
	}
}

func TestSPARQLResultsForAskAndGraphs(t *testing.T) {
	store := sparqlFixture(t)
	ctx := context.Background()
	ask, err := store.ExecuteSPARQL(ctx, `ASK { ?s ?p ?o }`)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := ask.WriteResults(&out, SPARQLResultsJSON); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || doc["boolean"] != true {
		t.Fatalf("ASK JSON: %s", out.String())
	}
	back, err := ReadSPARQLResultsJSON(bytes.NewReader(out.Bytes()))
	if err != nil || !back.Boolean || back.QueryType != SPARQLQueryAsk {
		t.Fatalf("ASK read back: %+v %v", back, err)
	}
	out.Reset()
	if err := ask.WriteResults(&out, SPARQLResultsXML); err != nil || !strings.Contains(out.String(), "<boolean>true</boolean>") {
		t.Fatalf("ASK XML: %v %s", err, out.String())
	}
	if err := ask.WriteResults(&out, SPARQLResultsCSV); err == nil {
		t.Error("CSV of an ASK result was written")
	}
	construct, err := store.ExecuteSPARQL(ctx, `CONSTRUCT WHERE { ?s ?p ?o }`)
	if err != nil {
		t.Fatal(err)
	}
	if err := construct.WriteResults(&out, SPARQLResultsJSON); err == nil {
		t.Error("a CONSTRUCT result was written as a result set")
	}
	if err := ask.WriteResults(&out, "yaml"); err == nil {
		t.Error("an unknown format was accepted")
	}
	if _, err := ReadSPARQLResultsJSON(strings.NewReader(`{"results":{"bindings":[{"x":{"type":"wat","value":"1"}}]}}`)); err == nil {
		t.Error("an unknown term type was read")
	}
}

func TestServiceIsOffUntilAHandlerIsSet(t *testing.T) {
	store := sparqlFixture(t)
	ctx := context.Background()
	q := `SELECT ?x WHERE { SERVICE <http://remote.example/sparql> { ?x ?p ?o } }`
	if _, err := store.ExecuteSPARQL(ctx, q); err == nil || !strings.Contains(err.Error(), "no SPARQL service handler") {
		t.Fatalf("SERVICE without a handler: %v", err)
	}
	res, err := store.ExecuteSPARQL(ctx, `SELECT * WHERE { BIND(1 AS ?one) SERVICE SILENT <http://remote.example/sparql> { ?x ?p ?o } }`)
	if err != nil || len(res.Bindings) != 1 {
		t.Fatalf("SERVICE SILENT without a handler: %+v %v", res, err)
	}

	remote := sparqlFixture(t)
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sent = string(body)
		if r.Header.Get("Content-Type") != "application/sparql-query" {
			http.Error(w, "bad content type", http.StatusUnsupportedMediaType)
			return
		}
		result, err := remote.ExecuteSPARQL(r.Context(), sent)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", SPARQLResultsJSON.MediaType())
		_ = result.WriteResults(w, SPARQLResultsJSON)
	}))
	defer srv.Close()
	store.SetSPARQLServiceHandler(HTTPSPARQLService(srv.Client()))
	res, err = store.ExecuteSPARQL(ctx, `PREFIX ex: <http://example.org/>
		SELECT ?age WHERE { SERVICE <`+srv.URL+`> { ex:a ex:age ?age } }`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Bindings) != 1 || res.Bindings[0]["age"].Value != "42" {
		t.Fatalf("SERVICE over HTTP: %+v", res.Bindings)
	}
	if !strings.Contains(sent, "PREFIX ex: <http://example.org/>") || !strings.Contains(sent, "SELECT * WHERE { ex:a ex:age ?age }") {
		t.Errorf("sent %q", sent)
	}
	if _, err := store.ExecuteSPARQL(ctx, `SELECT * WHERE { SERVICE <`+srv.URL+`> { this is not sparql } }`); err == nil {
		t.Error("a SERVICE pattern that does not parse was accepted")
	}
	if _, err := HTTPSPARQLService(nil)(ctx, "ftp://example.org/", "ASK {}"); err == nil {
		t.Error("a non-HTTP endpoint was accepted")
	}
	store.SetSPARQLServiceHandler(nil)
	if _, err := store.ExecuteSPARQL(ctx, q); err == nil {
		t.Error("SERVICE still answered after the handler was removed")
	}
}

func TestGraphManagementUpdates(t *testing.T) {
	store := sparqlFixture(t)
	ctx := context.Background()
	count := func(q string) int {
		t.Helper()
		res, err := store.ExecuteSPARQL(ctx, q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return len(res.Bindings)
	}
	run := func(u string) {
		t.Helper()
		if _, err := store.ExecuteSPARQL(ctx, u); err != nil {
			t.Fatalf("%s: %v", u, err)
		}
	}
	run(`COPY DEFAULT TO <http://example.org/copy>`)
	if n := count(`SELECT * WHERE { GRAPH <http://example.org/copy> { ?s ?p ?o } }`); n != 4 {
		t.Fatalf("COPY: %d triples in the copy, want 4", n)
	}
	run(`MOVE <http://example.org/copy> TO <http://example.org/moved>`)
	if count(`SELECT * WHERE { GRAPH <http://example.org/copy> { ?s ?p ?o } }`) != 0 ||
		count(`SELECT * WHERE { GRAPH <http://example.org/moved> { ?s ?p ?o } }`) != 4 {
		t.Fatal("MOVE did not move the graph")
	}
	run(`ADD <http://example.org/g> TO <http://example.org/moved>`)
	if n := count(`SELECT * WHERE { GRAPH <http://example.org/moved> { ?s ?p ?o } }`); n != 5 {
		t.Fatalf("ADD: %d, want 5", n)
	}
	if _, err := store.ExecuteSPARQL(ctx, `CREATE GRAPH <http://example.org/moved>`); err == nil {
		t.Error("CREATE of a graph with triples succeeded")
	}
	run(`CREATE SILENT GRAPH <http://example.org/moved> ; CREATE GRAPH <http://example.org/empty>`)
	run(`CLEAR NAMED`)
	if count(`SELECT * WHERE { GRAPH ?g { ?s ?p ?o } }`) != 0 || count(`SELECT * WHERE { ?s ?p ?o }`) != 4 {
		t.Fatal("CLEAR NAMED did not leave just the default graph")
	}
	run(`DROP DEFAULT`)
	if count(`SELECT * WHERE { ?s ?p ?o }`) != 0 {
		t.Fatal("DROP DEFAULT left triples")
	}
	if _, err := store.ExecuteSPARQL(ctx, `LOAD <http://example.org/data.ttl>`); err == nil || !strings.Contains(err.Error(), "does not fetch") {
		t.Errorf("LOAD: %v", err)
	}
	run(`LOAD SILENT <http://example.org/data.ttl> INTO GRAPH <http://example.org/g>`)
	run(`# an empty request`)
	for _, bad := range []string{`CLEAR`, `COPY <http://example.org/a>`, `WITH <http://example.org/g> CLEAR ALL`, `INSERT DATA { ?s <p> <o> }`,
		`INSERT DATA { GRAPH <g> { GRAPH <h> { <s> <p> <o> } } }`, `INSERT DATA { _:b <p> <o> } ; INSERT DATA { _:b <p> <o> }`} {
		if _, err := store.ExecuteSPARQL(ctx, bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}
