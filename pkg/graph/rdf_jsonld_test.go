package graph

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/piprate/json-gold/ld"
)

// networkTrap replaces the process-wide default HTTP transport for the length
// of a test with one that records and refuses every request. json-gold's
// built-in loader goes through http.DefaultClient, so if any code path ever
// hands it that loader instead of offlineContextLoader, the trap fires even
// for hosts no test server is listening on (schema.org, say).
type networkTrap struct{ calls atomic.Int64 }

func (n *networkTrap) RoundTrip(req *http.Request) (*http.Response, error) {
	n.calls.Add(1)
	return nil, errors.New("network access during a JSON-LD import: " + req.URL.String())
}

func installNetworkTrap(t *testing.T) *networkTrap {
	t.Helper()
	trap := &networkTrap{}
	previous := http.DefaultTransport
	http.DefaultTransport = trap
	t.Cleanup(func() { http.DefaultTransport = previous })
	return trap
}

var blankLabel = regexp.MustCompile(`_:[A-Za-z0-9_]+`)

// statementsWithAnonymousBlanks renders triples as sorted N-Quads lines with
// every blank node label replaced by "_:?", for assertions about shape where
// the particular labels do not matter.
func statementsWithAnonymousBlanks(triples []*RDFTriple) []string {
	out := make([]string, 0, len(triples))
	for _, triple := range triples {
		out = append(out, blankLabel.ReplaceAllString(triple.String(), "_:?"))
	}
	sort.Strings(out)
	return out
}

// canonicalNQuads is the definition of "the same data modulo blank node
// relabeling" these tests use: the W3C RDF Dataset Canonicalization
// (URDNA2015) of the statements, which assigns every blank node a label
// derived only from its position in the graph. Two datasets are isomorphic
// exactly when their canonical forms are equal strings.
func canonicalNQuads(t *testing.T, triples []RDFTriple) string {
	t.Helper()
	var lines strings.Builder
	for _, triple := range triples {
		lines.WriteString(triple.String())
		lines.WriteString("\n")
	}
	opts := ld.NewJsonLdOptions("")
	opts.DocumentLoader = offlineContextLoader{}
	opts.Algorithm = ld.AlgorithmURDNA2015
	opts.InputFormat = "application/n-quads"
	opts.Format = "application/n-quads"
	out, err := ld.NewJsonLdProcessor().Normalize(lines.String(), opts)
	if err != nil {
		t.Fatalf("canonicalize: %v\n%s", err, lines.String())
	}
	return out.(string)
}

func TestJSONLDImportUnderstandsTheContextFeaturesDocumentsUse(t *testing.T) {
	doc := `{
	  "@context": {
	    "@base": "https://example.com/people/",
	    "@vocab": "https://vocab.example/",
	    "@language": "en",
	    "ex": "https://example.com/ns#",
	    "xsd": "http://www.w3.org/2001/XMLSchema#",
	    "knows": {"@id": "ex:knows", "@type": "@id"},
	    "born": {"@id": "ex:born", "@type": "xsd:date"},
	    "tags": {"@id": "ex:tags", "@container": "@list"},
	    "nick": {"@id": "ex:nick", "@language": null},
	    "parentOf": {"@reverse": "ex:childOf"},
	    "dropped": null
	  },
	  "@id": "alice",
	  "@type": "ex:Person",
	  "name": "Alice",
	  "nick": "ali",
	  "born": "1990-01-02",
	  "knows": ["bob", "https://other.example/carol"],
	  "age": 34,
	  "height": 1.68,
	  "active": true,
	  "title": {"@value": "Dr", "@language": "de"},
	  "score": {"@value": "7", "@type": "xsd:integer"},
	  "tags": ["a", "b"],
	  "address": {"street": "1 Main St"},
	  "parentOf": {"@id": "dave"},
	  "dropped": "this term expands to nothing and is not an error",
	  "@reverse": {"ex:employs": {"@id": "https://acme.example/"}}
	}`
	triples, err := parseJSONLD([]byte(doc), "file:///unused/")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	want := []string{
		`<https://example.com/people/alice> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://example.com/ns#Person> .`,
		`<https://example.com/people/alice> <https://vocab.example/name> "Alice"@en .`,
		`<https://example.com/people/alice> <https://example.com/ns#nick> "ali" .`,
		`<https://example.com/people/alice> <https://example.com/ns#born> "1990-01-02"^^<http://www.w3.org/2001/XMLSchema#date> .`,
		`<https://example.com/people/alice> <https://example.com/ns#knows> <https://example.com/people/bob> .`,
		`<https://example.com/people/alice> <https://example.com/ns#knows> <https://other.example/carol> .`,
		`<https://example.com/people/alice> <https://vocab.example/age> "34"^^<http://www.w3.org/2001/XMLSchema#integer> .`,
		`<https://example.com/people/alice> <https://vocab.example/height> "1.68E0"^^<http://www.w3.org/2001/XMLSchema#double> .`,
		`<https://example.com/people/alice> <https://vocab.example/active> "true"^^<http://www.w3.org/2001/XMLSchema#boolean> .`,
		`<https://example.com/people/alice> <https://vocab.example/title> "Dr"@de .`,
		`<https://example.com/people/alice> <https://vocab.example/score> "7"^^<http://www.w3.org/2001/XMLSchema#integer> .`,
		`<https://example.com/people/alice> <https://example.com/ns#tags> _:? .`,
		`_:? <http://www.w3.org/1999/02/22-rdf-syntax-ns#first> "a"@en .`,
		`_:? <http://www.w3.org/1999/02/22-rdf-syntax-ns#rest> _:? .`,
		`_:? <http://www.w3.org/1999/02/22-rdf-syntax-ns#first> "b"@en .`,
		`_:? <http://www.w3.org/1999/02/22-rdf-syntax-ns#rest> <http://www.w3.org/1999/02/22-rdf-syntax-ns#nil> .`,
		`<https://example.com/people/alice> <https://vocab.example/address> _:? .`,
		`_:? <https://vocab.example/street> "1 Main St"@en .`,
		`<https://example.com/people/dave> <https://example.com/ns#childOf> <https://example.com/people/alice> .`,
		`<https://acme.example/> <https://example.com/ns#employs> <https://example.com/people/alice> .`,
	}
	sort.Strings(want)
	got := statementsWithAnonymousBlanks(triples)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("statements differ\n got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestJSONLDRelativeIRIsResolveAgainstTheDocumentBaseOrTheImportBase(t *testing.T) {
	withBase := `{"@context": {"@base": "https://example.com/a/b/", "p": {"@id": "https://example.com/p", "@type": "@id"}},
	  "@id": "../c", "p": "d?x=1#frag"}`
	triples, err := parseJSONLD([]byte(withBase), "file:///import/dir/")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(triples) != 1 {
		t.Fatalf("want 1 triple, got %v", triples)
	}
	if got := triples[0].Subject.Value; got != "https://example.com/a/c" {
		t.Fatalf("subject resolved to %q, want https://example.com/a/c", got)
	}
	if got := triples[0].Object.Value; got != "https://example.com/a/b/d?x=1#frag" {
		t.Fatalf("object resolved to %q, want https://example.com/a/b/d?x=1#frag", got)
	}

	// Without @base the importer's base applies, as it does for Turtle.
	withoutBase := `{"@context": {"p": {"@id": "https://example.com/p", "@type": "@id"}}, "@id": "doc", "p": "other"}`
	triples, err = parseJSONLD([]byte(withoutBase), "file:///import/dir/")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(triples) != 1 || triples[0].Subject.Value != "file:///import/dir/doc" || triples[0].Object.Value != "file:///import/dir/other" {
		t.Fatalf("relative IRIs did not resolve against the import base: %v", triples)
	}
}

func TestJSONLDNamedGraphsBecomeQuads(t *testing.T) {
	doc := `{
	  "@context": {"ex": "https://example.com/"},
	  "@graph": [
	    {"@id": "ex:alice", "ex:name": "Alice"},
	    {"@id": "ex:g1", "ex:source": "crawl", "@graph": [{"@id": "ex:bob", "ex:name": "Bob"}]},
	    {"@graph": [{"@id": "ex:carol", "ex:name": "Carol"}]}
	  ]
	}`
	triples, err := parseJSONLD([]byte(doc), "file:///")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{
		`<https://example.com/alice> <https://example.com/name> "Alice" .`,
		`<https://example.com/g1> <https://example.com/source> "crawl" .`,
		`<https://example.com/bob> <https://example.com/name> "Bob" <https://example.com/g1> .`,
		`<https://example.com/carol> <https://example.com/name> "Carol" _:? .`,
	}
	sort.Strings(want)
	if got := statementsWithAnonymousBlanks(triples); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("statements differ\n got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestJSONLDImportNeverFetchesARemoteContext(t *testing.T) {
	trap := installNetworkTrap(t)
	var served atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		w.Header().Set("Content-Type", "application/ld+json")
		_, _ = w.Write([]byte(`{"@context": {"name": "https://example.com/name"}}`))
	}))
	defer server.Close()
	remote := server.URL + "/context.jsonld"

	cases := map[string]string{
		"a context named by absolute URL": `{"@context": "` + remote + `", "@id": "https://example.com/a", "name": "A"}`,
		"a URL inside a context array":    `{"@context": [{"ex": "https://example.com/"}, "` + remote + `"], "@id": "ex:a", "name": "A"}`,
		"a context @import":               `{"@context": {"@version": 1.1, "@import": "` + remote + `"}, "@id": "https://example.com/a", "name": "A"}`,
		"a scoped context by URL":         `{"@context": {"p": {"@id": "https://example.com/p", "@context": "` + remote + `"}}, "@id": "https://example.com/a", "p": {"name": "A"}}`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseJSONLD([]byte(doc), "file:///")
			if !errors.Is(err, ErrRemoteJSONLDContext) {
				t.Fatalf("want ErrRemoteJSONLDContext, got %v", err)
			}
			if !strings.Contains(err.Error(), remote) {
				t.Fatalf("error does not name the context to inline: %v", err)
			}
		})
	}

	// A relative context resolves against the import base to a file: URL;
	// reading the server's filesystem is refused just the same.
	_, err := parseJSONLD([]byte(`{"@context": "context.jsonld", "@id": "https://example.com/a"}`), "file:///etc/")
	if !errors.Is(err, ErrRemoteJSONLDContext) || !strings.Contains(err.Error(), "file:///etc/context.jsonld") {
		t.Fatalf("relative context was not refused by name: %v", err)
	}

	if n := served.Load(); n != 0 {
		t.Fatalf("the context server received %d requests; it must receive none", n)
	}
	if n := trap.calls.Load(); n != 0 {
		t.Fatalf("%d HTTP requests were attempted during import", n)
	}
}

func TestJSONLDSchemaOrgDocumentsImportWithoutTouchingTheNetwork(t *testing.T) {
	trap := installNetworkTrap(t)
	doc := `{
	  "@context": "https://schema.org/",
	  "@type": "Person",
	  "@id": "https://example.com/people/zhang-wei",
	  "name": "张伟",
	  "jobTitle": "Engineer",
	  "address": {
	    "@type": "PostalAddress",
	    "streetAddress": "88 Century Avenue",
	    "addressLocality": "Shanghai",
	    "postalCode": "200120"
	  },
	  "worksFor": {
	    "type": "Organization",
	    "id": "https://example.com/org/acme",
	    "name": "Acme",
	    "numberOfEmployees": 250
	  },
	  "knowsLanguage": ["zh", "en"]
	}`
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			count, err := b.store.ImportRDF(ctx, strings.NewReader(doc), RDFFormatJSONLD)
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			if count != 14 {
				t.Fatalf("want 14 statements, got %d", count)
			}
			found, err := b.store.FindTriples(ctx, TriplePattern{})
			if err != nil {
				t.Fatalf("find: %v", err)
			}
			got := make([]*RDFTriple, 0, len(found))
			for i := range found {
				got = append(got, &found[i])
			}
			want := []string{
				`<https://example.com/people/zhang-wei> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://schema.org/Person> .`,
				`<https://example.com/people/zhang-wei> <https://schema.org/name> "张伟" .`,
				`<https://example.com/people/zhang-wei> <https://schema.org/jobTitle> "Engineer" .`,
				`<https://example.com/people/zhang-wei> <https://schema.org/address> _:? .`,
				`_:? <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://schema.org/PostalAddress> .`,
				`_:? <https://schema.org/streetAddress> "88 Century Avenue" .`,
				`_:? <https://schema.org/addressLocality> "Shanghai" .`,
				`_:? <https://schema.org/postalCode> "200120" .`,
				`<https://example.com/people/zhang-wei> <https://schema.org/worksFor> <https://example.com/org/acme> .`,
				`<https://example.com/org/acme> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://schema.org/Organization> .`,
				`<https://example.com/org/acme> <https://schema.org/name> "Acme" .`,
				`<https://example.com/org/acme> <https://schema.org/numberOfEmployees> "250"^^<http://www.w3.org/2001/XMLSchema#integer> .`,
				`<https://example.com/people/zhang-wei> <https://schema.org/knowsLanguage> "zh" .`,
				`<https://example.com/people/zhang-wei> <https://schema.org/knowsLanguage> "en" .`,
			}
			sort.Strings(want)
			if g := statementsWithAnonymousBlanks(got); strings.Join(g, "\n") != strings.Join(want, "\n") {
				t.Fatalf("statements differ\n got:\n%s\nwant:\n%s", strings.Join(g, "\n"), strings.Join(want, "\n"))
			}
		})
	}
	if n := trap.calls.Load(); n != 0 {
		t.Fatalf("%d HTTP requests were attempted during a schema.org import", n)
	}
}

func TestJSONLDSchemaOrgContextKnowsThePrefixesItPublishes(t *testing.T) {
	doc := `{"@context": "http://schema.org", "id": "https://example.com/doc",
	  "dct:title": "标题", "text": {"@value": "<b>x</b>", "@type": "HTML"}}`
	triples, err := parseJSONLD([]byte(doc), "file:///")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{
		`<https://example.com/doc> <http://purl.org/dc/terms/title> "标题" .`,
		`<https://example.com/doc> <https://schema.org/text> "<b>x</b>"^^<http://www.w3.org/1999/02/22-rdf-syntax-ns#HTML> .`,
	}
	if got := statementsWithAnonymousBlanks(triples); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("statements differ\n got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestJSONLDImportKeepsEachDocumentsBlankNodesApartAndIsIdempotent(t *testing.T) {
	alice := `{"@context": "https://schema.org/", "@id": "https://example.com/alice", "address": {"addressLocality": "Paris"}}`
	bob := `{"@context": "https://schema.org/", "@id": "https://example.com/bob", "address": {"addressLocality": "Oslo"}}`
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			for _, doc := range []string{alice, bob, alice} {
				if _, err := b.store.ImportRDF(ctx, strings.NewReader(doc), RDFFormatJSONLD); err != nil {
					t.Fatalf("import: %v", err)
				}
			}
			addresses, err := b.store.FindTriples(ctx, TriplePattern{Predicate: ptrTerm(NewIRI("https://schema.org/address"))})
			if err != nil {
				t.Fatalf("find: %v", err)
			}
			if len(addresses) != 2 || addresses[0].Object.Value == addresses[1].Object.Value {
				t.Fatalf("want two distinct anonymous addresses, got %v", addresses)
			}
			all, err := b.store.FindTriples(ctx, TriplePattern{})
			if err != nil {
				t.Fatalf("find: %v", err)
			}
			if len(all) != 4 {
				t.Fatalf("re-importing a document must not duplicate it: want 4 statements, got %d", len(all))
			}
		})
	}
}

// roundTripNQuads covers what a JSON-LD export has to carry: language tags,
// datatypes, a lexical form a native JSON number would lose, Chinese text,
// quotes and newlines, blank nodes as subject, object and graph name, named
// graphs, a graph name that is also a subject, and an RDF list.
const roundTripNQuads = `<https://example.com/alice> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://schema.org/Person> .
<https://example.com/alice> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://example.com/Author> .
<https://example.com/alice> <https://schema.org/name> "Alice" .
<https://example.com/alice> <https://schema.org/name> "爱丽丝"@zh-hans .
<https://example.com/alice> <https://schema.org/name> "Alicia"@es .
<https://example.com/alice> <https://schema.org/description> "says \"hi\"\nthen leaves" .
<https://example.com/alice> <https://schema.org/age> "034"^^<http://www.w3.org/2001/XMLSchema#integer> .
<https://example.com/alice> <https://schema.org/birthDate> "1990-01-02"^^<http://www.w3.org/2001/XMLSchema#date> .
<https://example.com/alice> <https://schema.org/address> _:addr .
_:addr <https://schema.org/addressLocality> "上海" .
<https://example.com/alice> <https://example.com/favourites> _:l1 .
_:l1 <http://www.w3.org/1999/02/22-rdf-syntax-ns#first> "tea" .
_:l1 <http://www.w3.org/1999/02/22-rdf-syntax-ns#rest> _:l2 .
_:l2 <http://www.w3.org/1999/02/22-rdf-syntax-ns#first> <https://example.com/books> .
_:l2 <http://www.w3.org/1999/02/22-rdf-syntax-ns#rest> <http://www.w3.org/1999/02/22-rdf-syntax-ns#nil> .
<https://example.com/g/crawl> <https://example.com/retrievedAt> "2026-09-25T10:00:00Z"^^<http://www.w3.org/2001/XMLSchema#dateTime> .
<https://example.com/bob> <https://schema.org/knows> <https://example.com/alice> <https://example.com/g/crawl> .
<https://example.com/bob> <https://schema.org/name> "Bob" <https://example.com/g/crawl> .
_:anon <https://schema.org/name> "unnamed graph member" _:g .
<urn:isbn:9780262510875> <https://schema.org/name> "SICP" .
<https://urn.example/catalogue> <https://schema.org/hasPart> <urn:isbn:9780262510875> .
`

func TestJSONLDExportThenImportReproducesTheSameDataset(t *testing.T) {
	sources := backends(t)
	targets := backends(t)
	for i, source := range sources {
		target := targets[i]
		t.Run(source.name, func(t *testing.T) {
			ctx := context.Background()
			if err := source.store.UpsertNamespace(ctx, Namespace{Prefix: "ex", URI: "https://example.com/"}); err != nil {
				t.Fatalf("namespace: %v", err)
			}
			// A prefix named like a scheme used by an uncompacted IRI must
			// not be declared, or urn:isbn:… would be read back through it.
			if err := source.store.UpsertNamespace(ctx, Namespace{Prefix: "urn", URI: "https://urn.example/"}); err != nil {
				t.Fatalf("namespace: %v", err)
			}
			if _, err := source.store.ImportRDF(ctx, strings.NewReader(roundTripNQuads), RDFFormatNQuads); err != nil {
				t.Fatalf("seed: %v", err)
			}
			// A list that arrived as JSON-LD @list must survive as well.
			listDoc := `{"@context": {"ex": "https://example.com/"}, "@id": "ex:bob", "ex:steps": {"@list": [1, 2.5, "三"]}}`
			if _, err := source.store.ImportRDF(ctx, strings.NewReader(listDoc), RDFFormatJSONLD); err != nil {
				t.Fatalf("seed list: %v", err)
			}

			var first, second bytes.Buffer
			if err := source.store.ExportRDF(ctx, &first, RDFFormatJSONLD); err != nil {
				t.Fatalf("export: %v", err)
			}
			if err := source.store.ExportRDF(ctx, &second, RDFFormatJSONLD); err != nil {
				t.Fatalf("second export: %v", err)
			}
			if first.String() != second.String() {
				t.Fatalf("export is not deterministic")
			}
			if strings.Contains(first.String(), `"urn"`) {
				t.Fatalf("the clashing urn prefix was declared:\n%s", first.String())
			}

			if _, err := target.store.ImportRDF(ctx, bytes.NewReader(first.Bytes()), RDFFormatJSONLD); err != nil {
				t.Fatalf("re-import: %v\n%s", err, first.String())
			}

			want, err := source.store.FindTriples(ctx, TriplePattern{})
			if err != nil {
				t.Fatalf("find source: %v", err)
			}
			got, err := target.store.FindTriples(ctx, TriplePattern{})
			if err != nil {
				t.Fatalf("find target: %v", err)
			}
			if len(want) != 28 {
				t.Fatalf("seed produced %d statements, want 28", len(want))
			}
			if a, b := canonicalNQuads(t, want), canonicalNQuads(t, got); a != b {
				t.Fatalf("round trip changed the dataset\nsource:\n%s\ntarget:\n%s\ndocument:\n%s", a, b, first.String())
			}
		})
	}
}

func TestJSONLDExportIsAStableDocument(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.UpsertNamespace(ctx, Namespace{Prefix: "ex", URI: "https://example.com/"}); err != nil {
				t.Fatalf("namespace: %v", err)
			}
			if err := b.store.UpsertNamespace(ctx, Namespace{Prefix: "v", URI: "https://vocab.example/v"}); err != nil {
				t.Fatalf("namespace: %v", err)
			}
			seed := `<https://example.com/b> <https://schema.org/name> "B"@en .
<https://example.com/a> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://schema.org/Thing> .
<https://example.com/a> <https://vocab.example/vcount> "2"^^<http://www.w3.org/2001/XMLSchema#integer> .
<https://example.com/a> <https://schema.org/knows> <https://example.com/b> .
<https://example.com/a> <https://schema.org/knows> _:x .
<https://example.com/a> <https://schema.org/name> "A" <https://example.com/g> .
`
			if _, err := b.store.ImportRDF(ctx, strings.NewReader(seed), RDFFormatNQuads); err != nil {
				t.Fatalf("seed: %v", err)
			}
			var out bytes.Buffer
			if err := b.store.ExportRDF(ctx, &out, RDFFormatJSONLD); err != nil {
				t.Fatalf("export: %v", err)
			}
			want := `{
  "@context": {
    "ex": "https://example.com/",
    "schema": "https://schema.org/",
    "v": {
      "@id": "https://vocab.example/v",
      "@prefix": true
    },
    "xsd": "http://www.w3.org/2001/XMLSchema#"
  },
  "@graph": [
    {
      "@id": "ex:a",
      "@type": "schema:Thing",
      "schema:knows": [
        {
          "@id": "_:x"
        },
        {
          "@id": "ex:b"
        }
      ],
      "v:count": {
        "@type": "xsd:integer",
        "@value": "2"
      }
    },
    {
      "@id": "ex:b",
      "schema:name": {
        "@language": "en",
        "@value": "B"
      }
    },
    {
      "@graph": [
        {
          "@id": "ex:a",
          "schema:name": "A"
        }
      ],
      "@id": "ex:g"
    }
  ]
}
`
			if out.String() != want {
				t.Fatalf("export differs\n got:\n%s\nwant:\n%s", out.String(), want)
			}
		})
	}
}

func TestJSONLDImportRejectsMalformedInputWithAnError(t *testing.T) {
	for name, doc := range map[string]string{
		"invalid JSON":        `{"@id": `,
		"trailing data":       `{} {}`,
		"a bare string":       `"https://example.com/doc.jsonld"`,
		"an invalid @context": `{"@context": 5, "@id": "https://example.com/a"}`,
		"a bad @version":      `{"@context": {"@version": 2}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseJSONLD([]byte(doc), "file:///"); err == nil {
				t.Fatalf("want an error for %s", doc)
			}
		})
	}
}

// json-gold v0.8.0 dereferences nil on an IRI that net/url cannot parse and
// indexes past the end of an empty key; the fuzzer found both within seconds.
// Each must reach the caller as an error on the import, not as a crash of the
// process doing the importing.
func TestJSONLDImportTurnsProcessorPanicsIntoErrors(t *testing.T) {
	for _, doc := range []string{
		`{"@id": "%"}`,
		`{"@context": "%"}`,
		`{"": 0}`,
	} {
		t.Run(doc, func(t *testing.T) {
			triples, err := parseJSONLD([]byte(doc), "file:///")
			if err == nil || !strings.Contains(err.Error(), "invalid input") {
				t.Fatalf("want a recovered processing error, got triples=%v err=%v", triples, err)
			}
		})
	}
}

func FuzzJSONLDImportNeverPanics(f *testing.F) {
	for _, seed := range []string{
		`{}`, `[]`, `{"@context": "https://schema.org/", "name": "x"}`,
		`{"@context": {"@vocab": "https://v/"}, "@id": "_:a", "p": {"@list": [1, {"@id": "x"}]}}`,
		`{"@graph": [{"@id": "https://g", "@graph": {"@id": "https://s", "https://p": {"@value": 1, "@type": "https://t"}}}]}`,
		`{"@context": {"@base": "https://e/a/", "r": {"@reverse": "https://p"}}, "@id": "../b", "r": {"@id": "c"}}`,
		`{"@context": [{"@version": 1.1}, {"t": {"@id": "https://t", "@container": ["@graph", "@index"]}}], "t": {"i": {"https://p": "v"}}}`,
		`{"@context": "https://attacker.example/ctx"}`,
	} {
		f.Add([]byte(seed))
	}
	// Whatever the input, no request may leave the process.
	trap := &networkTrap{}
	previous := http.DefaultTransport
	http.DefaultTransport = trap
	f.Cleanup(func() { http.DefaultTransport = previous })

	f.Fuzz(func(t *testing.T, data []byte) {
		triples, err := parseJSONLD(data, "file:///fuzz/")
		if n := trap.calls.Load(); n != 0 {
			t.Fatalf("%d HTTP requests attempted for input %q", n, data)
		}
		if err != nil {
			return
		}
		for _, triple := range triples {
			if triple == nil {
				t.Fatalf("nil triple without an error")
			}
			if triple.Subject.Kind == RDFTermLiteral || triple.Predicate.Kind != RDFTermIRI ||
				(triple.Graph != nil && triple.Graph.Kind == RDFTermLiteral) {
				t.Fatalf("importer produced a statement that is not RDF: %s", triple)
			}
		}
	})
}
