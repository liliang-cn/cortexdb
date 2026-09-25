package graph

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The property graph read as RDF, over both backends.
//
// The fixture is shaped like the shared brain rather than like a textbook:
// node types that differ only in case, ids with colons and slashes in them, a
// node whose properties column is NULL and two whose properties are JSON but
// not an object, an edge nobody typed, and some genuine RDF beside it all, so
// the mirror rows RDF writes into graph_nodes are present to be wrongly
// projected.

const (
	pgGraph = " <urn:cortexdb:graph:property> ."
	rdfType = "<http://www.w3.org/1999/02/22-rdf-syntax-ns#type>"
	rdfsLbl = "<http://www.w3.org/2000/01/rdf-schema#label>"
	xsdInt  = "^^<http://www.w3.org/2001/XMLSchema#integer>"
	xsdDec  = "^^<http://www.w3.org/2001/XMLSchema#decimal>"
	xsdBool = "^^<http://www.w3.org/2001/XMLSchema#boolean>"

	nCortex = "<urn:cortexdb:node:proj%3Acortexdb>"
	nAlpha  = "<urn:cortexdb:node:Proj%2FAlpha>"
	nDell   = "<urn:cortexdb:node:host%3Adell>"
	nABC    = "<urn:cortexdb:node:entity%3Aabc123>"
	nChina  = "<urn:cortexdb:node:中文%20节点%231>"

	storedName  = `<http://example.org/alice> <http://xmlns.com/foaf/0.1/name> "Alice" .`
	storedKnows = `<http://example.org/alice> <http://xmlns.com/foaf/0.1/knows> <http://example.org/bob> .`
)

// projectedFixture is every triple the fixture's property graph implies,
// written out by hand so the test does not check the projection against
// itself.
var projectedFixture = []string{
	nCortex + " " + rdfType + " <urn:cortexdb:type:project>" + pgGraph,
	nCortex + " " + rdfsLbl + ` "CortexDB"` + pgGraph,
	nCortex + ` <urn:cortexdb:prop:active> "true"` + xsdBool + pgGraph,
	nCortex + ` <urn:cortexdb:prop:aliases> "cortex"` + pgGraph,
	nCortex + ` <urn:cortexdb:prop:aliases> "cxdb"` + pgGraph,
	nCortex + ` <urn:cortexdb:prop:description> "memory library"` + pgGraph,
	nCortex + ` <urn:cortexdb:prop:mixed> "a"` + pgGraph,
	nCortex + ` <urn:cortexdb:prop:mixed> "1"` + xsdInt + pgGraph,
	nCortex + ` <urn:cortexdb:prop:name> "CortexDB"` + pgGraph,
	nCortex + ` <urn:cortexdb:prop:score> "4.5"` + xsdDec + pgGraph,
	nCortex + ` <urn:cortexdb:prop:stars> "42"` + xsdInt + pgGraph,

	nAlpha + " " + rdfType + " <urn:cortexdb:type:Project>" + pgGraph,
	nAlpha + " " + rdfsLbl + ` "Alpha"` + pgGraph,
	nAlpha + ` <urn:cortexdb:prop:title> "Alpha"` + pgGraph,

	nDell + " " + rdfType + " <urn:cortexdb:type:host>" + pgGraph,
	nDell + " " + rdfsLbl + ` "dell"` + pgGraph,
	nDell + ` <urn:cortexdb:prop:ip> "192.168.123.98"` + pgGraph,
	nDell + ` <urn:cortexdb:prop:name> "dell"` + pgGraph,

	nABC + " " + rdfType + " <urn:cortexdb:type:project>" + pgGraph,

	nChina + " " + rdfType + " <urn:cortexdb:type:project>" + pgGraph,
	nChina + " " + rdfsLbl + ` "中文"` + pgGraph,
	nChina + ` <urn:cortexdb:prop:key%20with.dot> "v"` + pgGraph,
	nChina + ` <urn:cortexdb:prop:name> "中文"` + pgGraph,

	nCortex + " <urn:cortexdb:rel:depends_on> " + nABC + pgGraph,
	nCortex + " <urn:cortexdb:rel:runs_on> " + nDell + pgGraph,
	nAlpha + " <urn:cortexdb:rel:part_of> " + nCortex + pgGraph,
	nChina + " <urn:cortexdb:rel:depends_on> " + nCortex + pgGraph,
}

func seedProjectionFixture(t *testing.T, ctx context.Context, g *GraphStore) {
	t.Helper()
	vector := make([]float32, g.rdfVectorDim())
	vector[0] = 1
	nodes := []GraphNode{
		{ID: "proj:cortexdb", NodeType: "project", Content: "kilobytes of prose that must not be projected",
			Properties: map[string]any{
				"name": "CortexDB", "description": "memory library",
				"aliases": []any{"cortex", "cxdb"},
				"stars":   42, "score": 4.5, "active": true,
				"meta":  map[string]any{"nested": 1},
				"gone":  nil,
				"blank": "   ",
				"mixed": []any{"a", 1, map[string]any{"x": 1}, nil, "a"},
			}},
		{ID: "Proj/Alpha", NodeType: "Project", Properties: map[string]any{"title": "Alpha"}},
		{ID: "host:dell", NodeType: "host", Properties: map[string]any{"name": "dell", "ip": "192.168.123.98"}},
		{ID: "entity:abc123", NodeType: "project"},
		{ID: "odd-string"},
		{ID: "odd-array"},
		{ID: "中文 节点#1", NodeType: "project", Properties: map[string]any{"name": "中文", "key with.dot": "v"}},
	}
	for i := range nodes {
		nodes[i].Vector = vector
		if err := g.UpsertNode(ctx, &nodes[i]); err != nil {
			t.Fatalf("seed node %s: %v", nodes[i].ID, err)
		}
	}
	for id, properties := range map[string]any{
		"entity:abc123": nil,
		"odd-string":    `"just a string"`,
		"odd-array":     `["x", 1]`,
	} {
		if _, err := g.exec(ctx, `UPDATE graph_nodes SET properties = ? WHERE id = ?`, properties, id); err != nil {
			t.Fatalf("set raw properties on %s: %v", id, err)
		}
	}
	edges := []GraphEdge{
		{ID: "e1", FromNodeID: "proj:cortexdb", ToNodeID: "entity:abc123", EdgeType: "depends_on", Weight: 1},
		{ID: "e2", FromNodeID: "proj:cortexdb", ToNodeID: "host:dell", EdgeType: "runs_on", Weight: 0.5,
			Properties: map[string]any{"since": "2026"}},
		{ID: "e3", FromNodeID: "Proj/Alpha", ToNodeID: "proj:cortexdb", EdgeType: "part_of", Weight: 1},
		{ID: "e4", FromNodeID: "Proj/Alpha", ToNodeID: "host:dell", Weight: 1},
		{ID: "e5", FromNodeID: "中文 节点#1", ToNodeID: "proj:cortexdb", EdgeType: "depends_on", Weight: 1},
	}
	for i := range edges {
		if err := g.UpsertEdge(ctx, &edges[i]); err != nil {
			t.Fatalf("seed edge %s: %v", edges[i].ID, err)
		}
	}
	for _, triple := range []RDFTriple{
		{Subject: NewIRI("http://example.org/alice"), Predicate: NewIRI("foaf:name"), Object: NewLiteral("Alice")},
		{Subject: NewIRI("http://example.org/alice"), Predicate: NewIRI("foaf:knows"), Object: NewIRI("http://example.org/bob")},
	} {
		if err := g.UpsertTriple(ctx, &triple); err != nil {
			t.Fatalf("seed triple: %v", err)
		}
	}
}

func tripleStrings(triples []RDFTriple) []string {
	out := make([]string, 0, len(triples))
	for _, triple := range triples {
		out = append(out, triple.String())
	}
	sort.Strings(out)
	return out
}

func sortedCopy(values ...string) []string {
	out := append([]string{}, values...)
	sort.Strings(out)
	return out
}

func assertTriples(t *testing.T, label string, got []RDFTriple, want ...string) {
	t.Helper()
	gotStrings := tripleStrings(got)
	wantStrings := sortedCopy(want...)
	if len(wantStrings) == 0 {
		wantStrings = []string{}
	}
	if !reflect.DeepEqual(gotStrings, wantStrings) {
		t.Fatalf("%s:\n got  %d: %s\n want %d: %s", label,
			len(gotStrings), strings.Join(gotStrings, "\n           "),
			len(wantStrings), strings.Join(wantStrings, "\n           "))
	}
}

// sparqlColumn returns one variable's values from a result, sorted.
func sparqlColumn(t *testing.T, g *GraphStore, query, variable string) []string {
	t.Helper()
	result, err := g.ExecuteSPARQL(context.Background(), query)
	if err != nil {
		t.Fatalf("sparql %q: %v", query, err)
	}
	out := make([]string, 0, len(result.Bindings))
	for _, binding := range result.Bindings {
		out = append(out, binding[variable].Value)
	}
	sort.Strings(out)
	return out
}

func iri(term string) string { return strings.Trim(term, "<>") }

func TestThePropertyGraphProjectsEveryNodeEdgeAndPropertyExactlyOnce(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedProjectionFixture(t, ctx, b.store)
			all, err := b.store.FindTriples(ctx, TriplePattern{})
			if err != nil {
				t.Fatalf("find all: %v", err)
			}
			assertTriples(t, "all triples", all, append([]string{storedName, storedKnows}, projectedFixture...)...)
			for _, triple := range all {
				if triple.Inferred {
					t.Fatalf("projected triple marked inferred: %s", triple)
				}
			}
		})
	}
}

func TestTheProjectionDoesNotCountAnRDFTripleTwice(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedProjectionFixture(t, ctx, b.store)

			// The mirror of the stored triples is really there in the
			// property graph, which is what makes this a test.
			mirror, err := b.store.NodeTypeCounts(ctx)
			if err != nil {
				t.Fatalf("node type counts: %v", err)
			}
			if mirror["rdf_resource"] != 2 || mirror["rdf_literal"] != 1 {
				t.Fatalf("expected the RDF mirror nodes to exist, got %v", mirror)
			}

			alice := NewIRI("http://example.org/alice")
			aboutAlice, err := b.store.FindTriples(ctx, TriplePattern{Subject: &alice})
			if err != nil {
				t.Fatalf("find alice: %v", err)
			}
			assertTriples(t, "alice", aboutAlice, storedName, storedKnows)

			mirrorType := NewIRI("cxt:rdf_resource")
			typed, err := b.store.FindTriples(ctx, TriplePattern{Predicate: &RDFTerm{Kind: RDFTermIRI, Value: "rdf:type"}, Object: &mirrorType})
			if err != nil {
				t.Fatalf("find mirror type: %v", err)
			}
			assertTriples(t, "mirror nodes as projected rdf_resource", typed)

			projectedGraph := NewIRI(PropertyGraphIRI)
			inGraph, err := b.store.FindTriples(ctx, TriplePattern{Graph: &projectedGraph})
			if err != nil {
				t.Fatalf("find in property graph: %v", err)
			}
			assertTriples(t, "the property graph", inGraph, projectedFixture...)
		})
	}
}

func TestProjectedNamesRoundTripThroughTheirIRIs(t *testing.T) {
	nasty := []string{
		"plain", "entity:abc123", "a/b/c", "has space", "hash#frag", "100%", "%41", "a>b<c",
		"中文", "中文 节点#1", "tab\there", "new\nline", "quote\"s", "emoji 🚀", "~._-",
		"\u200dzero-width", "\u3000ideographic space", "bad\xffutf8", "\ufffd",
	}
	for _, name := range nasty {
		escaped := escapeProjectionName(name)
		for _, r := range escaped {
			if r == ' ' || r == ':' || r == '/' || r == '#' || r == '>' || r == '<' || r == '\n' || r == '\t' || r == '"' || r == '\u200d' || r == '\u3000' {
				t.Fatalf("escape(%q) = %q still contains %q", name, escaped, r)
			}
		}
		back, ok := unescapeProjectionName(escaped)
		if !ok || back != name {
			t.Fatalf("round trip of %q via %q gave %q, %v", name, escaped, back, ok)
		}
		if id, ok := PropertyGraphNodeID(PropertyGraphNodeIRI(name)); !ok || id != name {
			t.Fatalf("node IRI round trip of %q gave %q, %v", name, id, ok)
		}
	}
	if got := escapeProjectionName("中文 节点#1"); got != "中文%20节点%231" {
		t.Fatalf("Chinese should stay readable, got %q", got)
	}
	for _, spelling := range []string{"entity:abc123", "entity%3aabc123", "100%", "%4", "%GG", ""} {
		if name, ok := unescapeProjectionName(spelling); ok {
			t.Fatalf("non-canonical spelling %q accepted as %q", spelling, name)
		}
	}
}

func TestSPARQLReadsThePropertyGraphByPrefixedName(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedProjectionFixture(t, ctx, b.store)

			dependsA := sparqlColumn(t, b.store, `SELECT ?a ?b WHERE { ?a cxr:depends_on ?b }`, "a")
			if want := sortedCopy(iri(nCortex), iri(nChina)); !reflect.DeepEqual(dependsA, want) {
				t.Fatalf("depends_on subjects: got %v want %v", dependsA, want)
			}
			dependsB := sparqlColumn(t, b.store, `SELECT ?a ?b WHERE { ?a cxr:depends_on ?b }`, "b")
			if want := sortedCopy(iri(nABC), iri(nCortex)); !reflect.DeepEqual(dependsB, want) {
				t.Fatalf("depends_on objects: got %v want %v", dependsB, want)
			}

			lower := sparqlColumn(t, b.store, `SELECT ?x WHERE { ?x a cxt:project }`, "x")
			if want := sortedCopy(iri(nCortex), iri(nABC), iri(nChina)); !reflect.DeepEqual(lower, want) {
				t.Fatalf("cxt:project: got %v want %v", lower, want)
			}
			upper := sparqlColumn(t, b.store, `SELECT ?x WHERE { ?x a cxt:Project }`, "x")
			if want := []string{iri(nAlpha)}; !reflect.DeepEqual(upper, want) {
				t.Fatalf("cxt:Project: got %v want %v", upper, want)
			}

			// A join across edge and property, which is what the projection
			// is for: what does each project run on, by name.
			hosts := sparqlColumn(t, b.store, `SELECT ?host WHERE { ?p a cxt:project . ?p cxr:runs_on ?h . ?h rdfs:label ?host }`, "host")
			if want := []string{"dell"}; !reflect.DeepEqual(hosts, want) {
				t.Fatalf("join: got %v want %v", hosts, want)
			}

			// The query from the bug report, which returned nothing at all.
			result, err := b.store.ExecuteSPARQL(ctx, `SELECT ?p (COUNT(*) AS ?n) WHERE { ?s ?p ?o } GROUP BY ?p`)
			if err != nil {
				t.Fatalf("group by predicate: %v", err)
			}
			counts := make(map[string]string, len(result.Bindings))
			for _, binding := range result.Bindings {
				counts[binding["p"].Value] = binding["n"].Value
			}
			want := map[string]string{
				iri(rdfType):                       "5",
				iri(rdfsLbl):                       "4",
				"urn:cortexdb:rel:depends_on":      "2",
				"urn:cortexdb:rel:runs_on":         "1",
				"urn:cortexdb:rel:part_of":         "1",
				"urn:cortexdb:prop:name":           "3",
				"urn:cortexdb:prop:aliases":        "2",
				"urn:cortexdb:prop:mixed":          "2",
				"urn:cortexdb:prop:active":         "1",
				"urn:cortexdb:prop:description":    "1",
				"urn:cortexdb:prop:score":          "1",
				"urn:cortexdb:prop:stars":          "1",
				"urn:cortexdb:prop:title":          "1",
				"urn:cortexdb:prop:ip":             "1",
				"urn:cortexdb:prop:key%20with.dot": "1",
				"http://xmlns.com/foaf/0.1/name":   "1",
				"http://xmlns.com/foaf/0.1/knows":  "1",
			}
			if !reflect.DeepEqual(counts, want) {
				t.Fatalf("predicate counts:\n got  %v\n want %v", counts, want)
			}
		})
	}
}

func TestSPARQLReachesAnIDWithReservedCharactersInBothSpellings(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedProjectionFixture(t, ctx, b.store)

			for _, query := range []string{
				`SELECT ?x WHERE { ?x cxr:depends_on cxn:entity%3Aabc123 }`,
				`SELECT ?x WHERE { ?x cxr:depends_on <urn:cortexdb:node:entity%3Aabc123> }`,
			} {
				if got, want := sparqlColumn(t, b.store, query, "x"), []string{iri(nCortex)}; !reflect.DeepEqual(got, want) {
					t.Fatalf("%s: got %v want %v", query, got, want)
				}
			}
			slash := sparqlColumn(t, b.store, `SELECT ?t WHERE { cxn:Proj%2FAlpha a ?t }`, "t")
			if want := []string{"urn:cortexdb:type:Project"}; !reflect.DeepEqual(slash, want) {
				t.Fatalf("slash id: got %v want %v", slash, want)
			}
			chinese := sparqlColumn(t, b.store, `SELECT ?n WHERE { <urn:cortexdb:node:中文%20节点%231> cxp:name ?n }`, "n")
			if want := []string{"中文"}; !reflect.DeepEqual(chinese, want) {
				t.Fatalf("Chinese id: got %v want %v", chinese, want)
			}
			dotted := sparqlColumn(t, b.store, `SELECT ?x WHERE { ?x <urn:cortexdb:prop:key%20with.dot> "v" }`, "x")
			if want := []string{iri(nChina)}; !reflect.DeepEqual(dotted, want) {
				t.Fatalf("key with a dot: got %v want %v", dotted, want)
			}
			// One node, one IRI: the unescaped spelling names nothing.
			if got := sparqlColumn(t, b.store, `SELECT ?x WHERE { ?x cxr:depends_on <urn:cortexdb:node:entity:abc123> }`, "x"); len(got) != 0 {
				t.Fatalf("non-canonical IRI matched %v", got)
			}
		})
	}
}

func TestGRAPHScopesAQueryToTheProjection(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedProjectionFixture(t, ctx, b.store)

			names := sparqlColumn(t, b.store, `SELECT ?n WHERE { GRAPH <urn:cortexdb:graph:property> { ?s cxp:name ?n } }`, "n")
			if want := sortedCopy("CortexDB", "dell", "中文"); !reflect.DeepEqual(names, want) {
				t.Fatalf("scoped names: got %v want %v", names, want)
			}
			if got := sparqlColumn(t, b.store, `SELECT ?n WHERE { GRAPH <urn:cortexdb:graph:property> { ?s foaf:name ?n } }`, "n"); len(got) != 0 {
				t.Fatalf("stored default-graph triple leaked into the projection's graph: %v", got)
			}
			if got := sparqlColumn(t, b.store, `SELECT ?n WHERE { GRAPH <http://example.org/other> { ?s cxp:name ?n } }`, "n"); len(got) != 0 {
				t.Fatalf("projection leaked into another graph: %v", got)
			}
			if got := sparqlColumn(t, b.store, `SELECT ?n WHERE { ?s foaf:name ?n }`, "n"); !reflect.DeepEqual(got, []string{"Alice"}) {
				t.Fatalf("stored triple in default graph: got %v", got)
			}
		})
	}
}

func TestPropertyLiteralsKeepTheirJSONTypes(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedProjectionFixture(t, ctx, b.store)

			for query, want := range map[string][]string{
				`SELECT ?x WHERE { ?x cxp:stars 42 }`:                    {iri(nCortex)},
				`SELECT ?x WHERE { ?x cxp:score 4.5 }`:                   {iri(nCortex)},
				`SELECT ?x WHERE { ?x cxp:active true }`:                 {iri(nCortex)},
				`SELECT ?x WHERE { ?x cxp:aliases "cxdb" }`:              {iri(nCortex)},
				`SELECT ?x WHERE { ?x cxp:mixed 1 }`:                     {iri(nCortex)},
				`SELECT ?x WHERE { ?x cxp:name "dell" }`:                 {iri(nDell)},
				`SELECT ?x WHERE { ?x rdfs:label "Alpha" }`:              {iri(nAlpha)},
				`SELECT ?x WHERE { ?x cxp:stars "42"^^xsd:decimal }`:     {},
				`SELECT ?x WHERE { ?x cxp:name "nobody" }`:               {},
				`SELECT ?x WHERE { ?x cxp:content ?c }`:                  {},
				`SELECT ?x WHERE { ?x cxp:meta ?c }`:                     {},
				`SELECT ?x WHERE { ?x cxp:blank ?c }`:                    {},
				`SELECT ?x WHERE { ?x ?p "memory library" }`:             {iri(nCortex)},
				`SELECT ?x WHERE { ?x cxp:since ?v }`:                    {},
				`SELECT ?x WHERE { ?x cxr:related ?y }`:                  {},
				`SELECT ?x WHERE { ?x <http://example.org/nothing> ?y }`: {},
			} {
				got := sparqlColumn(t, b.store, query, "x")
				if want == nil {
					want = []string{}
				}
				if !reflect.DeepEqual(got, sortedCopy(want...)) {
					t.Fatalf("%s: got %v want %v", query, got, want)
				}
			}
		})
	}
}

func TestAnUntypedEdgeIsNotProjected(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedProjectionFixture(t, ctx, b.store)
			alpha := NewIRI(iri(nAlpha))
			got, err := b.store.FindTriples(ctx, TriplePattern{Subject: &alpha})
			if err != nil {
				t.Fatalf("find alpha: %v", err)
			}
			// e4 runs from Alpha to dell with no type and is absent.
			assertTriples(t, "alpha", got,
				nAlpha+" "+rdfType+" <urn:cortexdb:type:Project>"+pgGraph,
				nAlpha+" "+rdfsLbl+` "Alpha"`+pgGraph,
				nAlpha+` <urn:cortexdb:prop:title> "Alpha"`+pgGraph,
				nAlpha+" <urn:cortexdb:rel:part_of> "+nCortex+pgGraph,
			)
		})
	}
}

func TestNodesWhosePropertiesAreNotAnObjectStillProject(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedProjectionFixture(t, ctx, b.store)
			for id, want := range map[string][]string{
				"entity:abc123": {nABC + " " + rdfType + " <urn:cortexdb:type:project>" + pgGraph},
				"odd-string":    nil,
				"odd-array":     nil,
			} {
				subject := NewIRI(PropertyGraphNodeIRI(id))
				got, err := b.store.FindTriples(ctx, TriplePattern{Subject: &subject})
				if err != nil {
					t.Fatalf("find %s: %v", id, err)
				}
				// The only incoming edge to entity:abc123 is an object, not a
				// subject, so it is not here.
				assertTriples(t, id, got, want...)
			}
			// The key and label scans read every row's JSON, and one odd row
			// must not take them down on either database.
			for _, predicate := range []string{"cxp:name", "cxp:key%20with.dot", "rdfs:label"} {
				term := NewIRI(predicate)
				if _, err := b.store.FindTriples(ctx, TriplePattern{Predicate: &term}); err != nil {
					t.Fatalf("scan %s over odd rows: %v", predicate, err)
				}
				literal := NewLiteral("中文")
				if _, err := b.store.FindTriples(ctx, TriplePattern{Predicate: &term, Object: &literal}); err != nil {
					t.Fatalf("scan %s = literal over odd rows: %v", predicate, err)
				}
			}
		})
	}
}

func TestPatternsTheProjectionCannotAnswerAreRefusedBeforeAnySQL(t *testing.T) {
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()
	yes, no := true, false
	literal := NewLiteral("x")
	other := NewIRI("http://example.org/x")
	node := NewIRI("urn:cortexdb:node:a")
	blank := NewBlankNode("b")
	for name, pattern := range map[string]TriplePattern{
		"inferred only":                {Inferred: &yes},
		"another graph":                {Graph: &other},
		"a subject that is not a node": {Subject: &other},
		"a blank subject":              {Subject: &blank},
		"a foreign predicate":          {Predicate: &other},
		"a foreign object":             {Object: &other},
		"a blank object":               {Object: &blank},
		"a literal with rdf:type":      {Predicate: &RDFTerm{Kind: RDFTermIRI, Value: projectionRDFTypeIRI}, Object: &literal},
		"a node object with a prop":    {Predicate: &RDFTerm{Kind: RDFTermIRI, Value: PropertyPropNamespace + "k"}, Object: &node},
		"a non-canonical subject":      {Subject: &RDFTerm{Kind: RDFTermIRI, Value: "urn:cortexdb:node:a:b"}},
	} {
		if _, ok, err := g.planProjection(ctx, pattern); ok || err != nil {
			t.Fatalf("%s: planned a projection (ok=%v, err=%v)", name, ok, err)
		}
	}
	if _, ok, _ := g.planProjection(ctx, TriplePattern{Inferred: &no, Subject: &node}); !ok {
		t.Fatalf("an explicit-only pattern on a node should be planned")
	}
}

func TestLimitSpansStoredAndProjectedTriplesInAStableOrder(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedProjectionFixture(t, ctx, b.store)

			all, err := b.store.FindTriples(ctx, TriplePattern{})
			if err != nil {
				t.Fatalf("find all: %v", err)
			}
			again, err := b.store.FindTriples(ctx, TriplePattern{})
			if err != nil {
				t.Fatalf("find all again: %v", err)
			}
			if !reflect.DeepEqual(all, again) {
				t.Fatalf("two reads disagree on order")
			}
			if strings.HasPrefix(all[0].ID, projectedTripleIDPrefix) || strings.HasPrefix(all[1].ID, projectedTripleIDPrefix) ||
				!strings.HasPrefix(all[2].ID, projectedTripleIDPrefix) {
				t.Fatalf("stored triples should come first: %v %v %v", all[0].ID, all[1].ID, all[2].ID)
			}
			for _, limit := range []int{1, 2, 3, 12, len(all), len(all) + 5} {
				limited, err := b.store.FindTriples(ctx, TriplePattern{Limit: limit})
				if err != nil {
					t.Fatalf("limit %d: %v", limit, err)
				}
				want := all
				if limit < len(all) {
					want = all[:limit]
				}
				if !reflect.DeepEqual(limited, want) {
					t.Fatalf("limit %d: got %d triples, not the first %d", limit, len(limited), len(want))
				}
			}
			// Across the node/edge boundary within the projection.
			graphTerm := NewIRI(PropertyGraphIRI)
			graphAll, _ := b.store.FindTriples(ctx, TriplePattern{Graph: &graphTerm})
			graphLimited, err := b.store.FindTriples(ctx, TriplePattern{Graph: &graphTerm, Limit: 25})
			if err != nil || !reflect.DeepEqual(graphLimited, graphAll[:25]) {
				t.Fatalf("limit across nodes and edges: %v", err)
			}
		})
	}
}

func TestAProjectedTripleCanBeFetchedAndExplainedByID(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedProjectionFixture(t, ctx, b.store)
			graphTerm := NewIRI(PropertyGraphIRI)
			projected, err := b.store.FindTriples(ctx, TriplePattern{Graph: &graphTerm})
			if err != nil {
				t.Fatalf("find projected: %v", err)
			}
			seen := make(map[string]bool, len(projected))
			for _, triple := range projected {
				if seen[triple.ID] {
					t.Fatalf("two projected triples share ID %s", triple.ID)
				}
				seen[triple.ID] = true
				got, err := b.store.GetTriple(ctx, triple.ID)
				if err != nil {
					t.Fatalf("get %s: %v", triple.ID, err)
				}
				if got.String() != triple.String() {
					t.Fatalf("get %s: got %s want %s", triple.ID, got, triple)
				}
				explained, err := b.store.ExplainTriple(ctx, triple.ID)
				if err != nil || !explained.Explicit {
					t.Fatalf("explain %s: %+v %v", triple.ID, explained, err)
				}
			}
			if _, err := b.store.GetTriple(ctx, "pg:edge:e4"); err == nil {
				t.Fatalf("the untyped edge resolved to a triple")
			}
			b.store.SetPropertyGraphProjection(false)
			if _, err := b.store.GetTriple(ctx, projected[0].ID); err == nil {
				t.Fatalf("a projected ID resolved with the projection off")
			}
		})
	}
}

func TestDeletingAProjectedTripleFailsLoudlyAndRemovesNothing(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedProjectionFixture(t, ctx, b.store)
			before, err := b.store.FindTriples(ctx, TriplePattern{})
			if err != nil {
				t.Fatalf("find before: %v", err)
			}

			for _, query := range []string{
				`DELETE WHERE { ?a cxr:depends_on ?b }`,
				`DELETE DATA { cxn:proj%3Acortexdb cxr:runs_on cxn:host%3Adell }`,
				`DELETE DATA { GRAPH <urn:cortexdb:graph:property> { cxn:proj%3Acortexdb cxr:runs_on cxn:host%3Adell } }`,
				// Mixed: the stored triples come first and must survive.
				`DELETE WHERE { ?s ?p ?o }`,
				`DELETE { ?s cxp:name ?n } INSERT { ?s foaf:name ?n } WHERE { ?s cxp:name ?n }`,
			} {
				_, err := b.store.ExecuteSPARQL(ctx, query)
				if !errors.Is(err, ErrPropertyGraphReadOnly) {
					t.Fatalf("%s: expected ErrPropertyGraphReadOnly, got %v", query, err)
				}
			}
			predicate := NewIRI("cxr:depends_on")
			if _, err := b.store.DeleteTriples(ctx, TriplePattern{Predicate: &predicate}); !errors.Is(err, ErrPropertyGraphReadOnly) {
				t.Fatalf("DeleteTriples: expected ErrPropertyGraphReadOnly, got %v", err)
			}
			if err := b.store.DeleteTriple(ctx, before[len(before)-1]); !errors.Is(err, ErrPropertyGraphReadOnly) {
				t.Fatalf("DeleteTriple by ID: expected ErrPropertyGraphReadOnly, got %v", err)
			}
			content := before[len(before)-1]
			content.ID = ""
			if err := b.store.DeleteTriple(ctx, content); !errors.Is(err, ErrPropertyGraphReadOnly) {
				t.Fatalf("DeleteTriple by content: expected ErrPropertyGraphReadOnly, got %v", err)
			}

			after, err := b.store.FindTriples(ctx, TriplePattern{})
			if err != nil {
				t.Fatalf("find after: %v", err)
			}
			if !reflect.DeepEqual(tripleStrings(after), tripleStrings(before)) {
				t.Fatalf("a refused delete changed the graph")
			}
			edges, err := b.store.EdgeTypeCounts(ctx)
			if err != nil || edges["depends_on"] != 2 {
				t.Fatalf("edges after refused delete: %v %v", edges, err)
			}
		})
	}
}

func TestADeleteCountsOnlyTriplesThatWereRemoved(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedProjectionFixture(t, ctx, b.store)
			for _, step := range []struct {
				query string
				want  int
			}{
				{`DELETE DATA { <http://example.org/nobody> foaf:name "Nobody" }`, 0},
				{`DELETE DATA { <http://example.org/alice> foaf:name "Alice" . <http://example.org/x> foaf:name "X" }`, 1},
				{`DELETE WHERE { <http://example.org/alice> ?p ?o }`, 1},
				{`DELETE WHERE { <http://example.org/alice> ?p ?o }`, 0},
			} {
				result, err := b.store.ExecuteSPARQL(ctx, step.query)
				if err != nil {
					t.Fatalf("%s: %v", step.query, err)
				}
				if result.Count != step.want {
					t.Fatalf("%s: counted %d, removed %d", step.query, result.Count, step.want)
				}
			}
		})
	}
}

func TestWritingIntoThePropertyGraphsNameIsRefused(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedProjectionFixture(t, ctx, b.store)
			for _, query := range []string{
				`INSERT DATA { GRAPH <urn:cortexdb:graph:property> { cxn:a cxr:b cxn:c } }`,
				`WITH <urn:cortexdb:graph:property> INSERT { ?s cxr:twin ?s } WHERE { ?s a cxt:host }`,
			} {
				if _, err := b.store.ExecuteSPARQL(ctx, query); err == nil || !strings.Contains(err.Error(), "read-only property-graph projection") {
					t.Fatalf("%s: expected a refusal, got %v", query, err)
				}
			}
			stored, err := b.store.findStoredTriples(ctx, TriplePattern{})
			if err != nil {
				t.Fatalf("find stored: %v", err)
			}
			assertTriples(t, "stored after refused inserts", stored, storedName, storedKnows)
		})
	}
}

func TestTurningTheProjectionOffLeavesOnlyStoredTriples(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedProjectionFixture(t, ctx, b.store)
			if !b.store.PropertyGraphProjectionEnabled() {
				t.Fatalf("the projection should be on by default")
			}
			b.store.SetPropertyGraphProjection(false)
			all, err := b.store.FindTriples(ctx, TriplePattern{})
			if err != nil {
				t.Fatalf("find: %v", err)
			}
			assertTriples(t, "projection off", all, storedName, storedKnows)
			b.store.SetPropertyGraphProjection(true)
			all, err = b.store.FindTriples(ctx, TriplePattern{})
			if err != nil || len(all) != 2+len(projectedFixture) {
				t.Fatalf("projection back on: %d triples, %v", len(all), err)
			}
		})
	}
}

func TestAnExportLeavesTheProjectionOut(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedProjectionFixture(t, ctx, b.store)
			var out bytes.Buffer
			if err := b.store.ExportRDF(ctx, &out, RDFFormatNQuads); err != nil {
				t.Fatalf("export: %v", err)
			}
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			sort.Strings(lines)
			if want := sortedCopy(storedName, storedKnows); !reflect.DeepEqual(lines, want) {
				t.Fatalf("export:\n got  %v\n want %v", lines, want)
			}
		})
	}
}

func TestDESCRIBEOfANodeReturnsItsProjectedTriples(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedProjectionFixture(t, ctx, b.store)
			result, err := b.store.ExecuteSPARQL(ctx, `DESCRIBE cxn:host%3Adell`)
			if err != nil {
				t.Fatalf("describe: %v", err)
			}
			assertTriples(t, "describe dell", result.Triples,
				nDell+" "+rdfType+" <urn:cortexdb:type:host>"+pgGraph,
				nDell+" "+rdfsLbl+` "dell"`+pgGraph,
				nDell+` <urn:cortexdb:prop:ip> "192.168.123.98"`+pgGraph,
				nDell+` <urn:cortexdb:prop:name> "dell"`+pgGraph,
				nCortex+" <urn:cortexdb:rel:runs_on> "+nDell+pgGraph,
			)
		})
	}
}

func TestRDFSInfersClassesForPropertyGraphNodes(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedProjectionFixture(t, ctx, b.store)
			if _, err := b.store.ExecuteSPARQL(ctx, `INSERT DATA { cxt:host rdfs:subClassOf cxt:Host }`); err != nil {
				t.Fatalf("declare subclass: %v", err)
			}
			if _, err := b.store.RefreshRDFSInferences(ctx); err != nil {
				t.Fatalf("refresh: %v", err)
			}
			hosts := sparqlColumn(t, b.store, `SELECT ?x WHERE { ?x a cxt:Host }`, "x")
			if want := []string{iri(nDell)}; !reflect.DeepEqual(hosts, want) {
				t.Fatalf("?x a cxt:Host: got %v want %v", hosts, want)
			}

			// The inference is stored, and so mirrored into graph_nodes; the
			// mirror must not come back as projected triples.
			graphTerm := NewIRI(PropertyGraphIRI)
			inGraph, err := b.store.FindTriples(ctx, TriplePattern{Graph: &graphTerm})
			if err != nil {
				t.Fatalf("find in graph: %v", err)
			}
			var projected []RDFTriple
			var inferred *RDFTriple
			for i, triple := range inGraph {
				switch {
				case strings.HasPrefix(triple.ID, projectedTripleIDPrefix):
					projected = append(projected, triple)
				case triple.Inferred && triple.Object.Value == "urn:cortexdb:type:Host":
					inferred = &inGraph[i]
				}
			}
			assertTriples(t, "projection after refresh", projected, projectedFixture...)
			if inferred == nil {
				t.Fatalf("the inferred type is not in the projection's graph: %v", tripleStrings(inGraph))
			}

			// And its provenance can be followed back into the projection.
			trace, err := b.store.ExplainTripleTrace(ctx, inferred.ID, 1)
			if err != nil {
				t.Fatalf("trace: %v", err)
			}
			var reachedProjection bool
			for _, entry := range trace {
				if entry.TripleID == "pg:type:host%3Adell" {
					reachedProjection = true
				}
			}
			if !reachedProjection {
				t.Fatalf("trace did not reach the projected type triple: %+v", trace)
			}
		})
	}
}

// A pattern's literal matches by the rule kg_triples applies, whichever
// source answers: the value must match, and the datatype and language only
// when the pattern names one. So a plain "42" finds a stored
// "42"^^xsd:integer and a projected one alike, and two sources cannot answer
// one pattern by two rules.
func TestAPatternLiteralMatchesStoredAndProjectedTriplesByTheSameRule(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			seedProjectionFixture(t, ctx, b.store)
			for _, triple := range []RDFTriple{
				{Subject: NewIRI("http://example.org/repo"), Predicate: NewIRI("http://example.org/stars"), Object: NewTypedLiteral("42", "xsd:integer")},
				{Subject: NewIRI("http://example.org/repo"), Predicate: NewIRI("http://example.org/motto"), Object: NewLangLiteral("dell", "en")},
			} {
				if err := b.store.UpsertTriple(ctx, &triple); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			stored := func(p, o string) string {
				return "<http://example.org/repo> <http://example.org/" + p + "> " + o + " ."
			}
			for _, step := range []struct {
				object RDFTerm
				want   []string
			}{
				{NewLiteral("42"), []string{
					stored("stars", `"42"`+xsdInt),
					nCortex + ` <urn:cortexdb:prop:stars> "42"` + xsdInt + pgGraph,
				}},
				{NewTypedLiteral("42", "xsd:integer"), []string{
					stored("stars", `"42"`+xsdInt),
					nCortex + ` <urn:cortexdb:prop:stars> "42"` + xsdInt + pgGraph,
				}},
				{NewTypedLiteral("42", "xsd:string"), nil},
				{NewLiteral("dell"), []string{
					stored("motto", `"dell"@en`),
					nDell + " " + rdfsLbl + ` "dell"` + pgGraph,
					nDell + ` <urn:cortexdb:prop:name> "dell"` + pgGraph,
				}},
				{NewLangLiteral("dell", "en"), []string{stored("motto", `"dell"@en`)}},
			} {
				object := step.object
				got, err := b.store.FindTriples(ctx, TriplePattern{Object: &object})
				if err != nil {
					t.Fatalf("find %s: %v", object, err)
				}
				assertTriples(t, object.String(), got, step.want...)
			}
		})
	}
}
