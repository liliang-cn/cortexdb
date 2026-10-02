package graph

import (
	"context"
	"sort"
	"strings"
	"testing"
)

func sparqlRows(t *testing.T, g *GraphStore, query string) []map[string]RDFTerm {
	t.Helper()
	result, err := g.ExecuteSPARQL(context.Background(), query)
	if err != nil {
		t.Fatalf("query failed: %v\n%s", err, query)
	}
	return result.Bindings
}

func mustUpdate(t *testing.T, g *GraphStore, update string) int {
	t.Helper()
	result, err := g.ExecuteSPARQL(context.Background(), update)
	if err != nil {
		t.Fatalf("update failed: %v\n%s", err, update)
	}
	return result.Count
}

const sparql12Prologue = `PREFIX : <http://example.org/>
PREFIX rdf: <http://www.w3.org/1999/02/22-rdf-syntax-ns#>
PREFIX xsd: <http://www.w3.org/2001/XMLSchema#>
`

// The point of the whole feature: a fact, and what is known about the fact —
// where it came from and how sure anyone is — stored as RDF 1.2, written with
// SPARQL UPDATE and read back with SPARQL 1.2 in each of its three spellings.
func TestProvenanceAndConfidenceAboutAFactRoundTripThroughSPARQL12(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			inserted := mustUpdate(t, b.store, sparql12Prologue+`
INSERT DATA {
  :alice :worksFor :acme .
  _:r rdf:reifies <<( :alice :worksFor :acme )>> ;
      :source <http://example.org/doc1> ;
      :confidence 0.9 .
  :bob :worksFor :initech {| :source <http://example.org/doc2> ; :confidence 0.3 |} .
}`)
			if inserted != 8 {
				t.Fatalf("INSERT DATA wrote %d triples, want 8 (2 facts, 2 reifying triples, 4 annotations)", inserted)
			}

			// 1. The triple term itself, in a pattern with variables in it.
			rows := sparqlRows(t, b.store, sparql12Prologue+`
SELECT ?who ?org ?source ?confidence WHERE {
  ?r rdf:reifies <<( ?who :worksFor ?org )>> ;
     :source ?source ;
     :confidence ?confidence .
  FILTER(?confidence > 0.5)
}`)
			if len(rows) != 1 || rows[0]["who"].Value != rdf12ExNS+"alice" || rows[0]["org"].Value != rdf12ExNS+"acme" ||
				rows[0]["source"].Value != rdf12ExNS+"doc1" || rows[0]["confidence"].Value != "0.9" {
				t.Fatalf("confident facts = %v, want alice at acme from doc1 at 0.9", rows)
			}

			// 2. The annotation syntax: asserted, and annotated.
			rows = sparqlRows(t, b.store, sparql12Prologue+`
SELECT ?who ?confidence WHERE { ?who :worksFor ?org {| :confidence ?confidence |} } ORDER BY ?who`)
			if len(rows) != 2 || rows[0]["confidence"].Value != "0.9" || rows[1]["confidence"].Value != "0.3" {
				t.Fatalf("annotated facts = %v", rows)
			}
			if _, hidden := rows[0]["_:#anon1"]; hidden {
				t.Fatal("the anonymous reifier leaked into SELECT *-free results")
			}

			// 3. The reified-triple sugar with the reifier named, and the
			// functions that take a triple term apart.
			rows = sparqlRows(t, b.store, sparql12Prologue+`
SELECT ?r ?s ?o ?isTriple WHERE {
  << ?s :worksFor ?o ~ ?r >> :source <http://example.org/doc2> .
  ?r rdf:reifies ?t .
  BIND(isTRIPLE(?t) AS ?isTriple)
  FILTER(SUBJECT(?t) = ?s && OBJECT(?t) = ?o && PREDICATE(?t) = :worksFor)
}`)
			if len(rows) != 1 || rows[0]["s"].Value != rdf12ExNS+"bob" || rows[0]["isTriple"].Value != "true" {
				t.Fatalf("doc2's fact = %v, want bob", rows)
			}

			// The store answers the same question without SPARQL.
			reifiers, err := b.store.FindReifiers(ctx, rdf12IRI("alice"), rdf12IRI("worksFor"), rdf12IRI("acme"))
			if err != nil || len(reifiers) != 1 || reifiers[0].Kind != RDFTermBlankNode {
				t.Fatalf("FindReifiers = %v, %v; want the one blank-node reifier", reifiers, err)
			}

			// Retracting the fact leaves what was said about it: a reifier
			// talks about a triple, it does not assert it.
			mustUpdate(t, b.store, sparql12Prologue+`DELETE DATA { :alice :worksFor :acme }`)
			rows = sparqlRows(t, b.store, sparql12Prologue+`
SELECT ?source WHERE { << :alice :worksFor :acme >> :source ?source . FILTER NOT EXISTS { :alice :worksFor :acme } }`)
			if len(rows) != 1 {
				t.Fatalf("after retracting the fact its provenance should remain, unasserted: %v", rows)
			}
		})
	}
}

func TestATripleTermPatternBindsItsPartsAndNestedTerms(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			mustUpdate(t, b.store, sparql12Prologue+`
INSERT DATA {
  :r1 rdf:reifies <<( :alice :knows :bob )>> .
  :r2 rdf:reifies <<( :carol :said <<( :alice :knows :bob )>> )>> .
  :r3 rdf:reifies <<( :dave :age 42 )>> .
}`)
			rows := sparqlRows(t, b.store, sparql12Prologue+`
SELECT ?who ?s WHERE { ?r rdf:reifies <<( ?who :said <<( ?s :knows :bob )>> )>> }`)
			if len(rows) != 1 || rows[0]["who"].Value != rdf12ExNS+"carol" || rows[0]["s"].Value != rdf12ExNS+"alice" {
				t.Fatalf("nested pattern = %v", rows)
			}
			rows = sparqlRows(t, b.store, sparql12Prologue+`SELECT ?age WHERE { ?r rdf:reifies <<( :dave :age ?age )>> }`)
			if len(rows) != 1 || rows[0]["age"].Value != "42" || rows[0]["age"].Datatype != xsdIntegerIRI {
				t.Fatalf("literal part = %v", rows)
			}
			// A pattern a triple term can never match is no solutions, not an
			// error: RDF 1.2 has no triple term in subject position.
			rows = sparqlRows(t, b.store, sparql12Prologue+`SELECT * WHERE { <<( :alice :knows :bob )>> ?p ?o }`)
			if len(rows) != 0 {
				t.Fatalf("a triple term subject matched: %v", rows)
			}
			// Equality compares the parts by value; sameTerm does not.
			rows = sparqlRows(t, b.store, sparql12Prologue+`
SELECT ?eq ?same WHERE {
  BIND(<<( :a :b 1 )>> = <<( :a :b 1.0 )>> AS ?eq)
  BIND(sameTerm(<<( :a :b 1 )>>, <<( :a :b 1.0 )>>) AS ?same)
}`)
			if len(rows) != 1 || rows[0]["eq"].Value != "true" || rows[0]["same"].Value != "false" {
				t.Fatalf("triple term equality = %v, want = true and sameTerm false", rows)
			}
		})
	}
}

func TestTriplesTermsSortAfterEveryOtherKindByTheirParts(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			mustUpdate(t, b.store, sparql12Prologue+`
INSERT DATA {
  :x :v <<( :s :p 2 )>>, <<( :s :p 10 )>>, "lit", :iri, <<( :a :p 99 )>> .
}`)
			rows := sparqlRows(t, b.store, sparql12Prologue+`SELECT ?v WHERE { :x :v ?v } ORDER BY ?v`)
			var got []string
			for _, row := range rows {
				got = append(got, row["v"].Value)
			}
			want := []string{
				rdf12ExNS + "iri", "lit",
				`<<( <http://example.org/a> <http://example.org/p> "99"^^<http://www.w3.org/2001/XMLSchema#integer> )>>`,
				`<<( <http://example.org/s> <http://example.org/p> "2"^^<http://www.w3.org/2001/XMLSchema#integer> )>>`,
				`<<( <http://example.org/s> <http://example.org/p> "10"^^<http://www.w3.org/2001/XMLSchema#integer> )>>`,
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("ORDER BY:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

// A blank node in a CONSTRUCT or INSERT template is a fresh node for every
// solution, so two facts never share a reifier the query did not name.
func TestEachSolutionGetsItsOwnReifierInATemplate(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			mustUpdate(t, b.store, sparql12Prologue+`INSERT DATA { :a :p 1 . :b :p 2 . }`)
			result, err := b.store.ExecuteSPARQL(context.Background(), sparql12Prologue+`
CONSTRUCT { ?s :p ?o {| :checked true |} } WHERE { ?s :p ?o }`)
			if err != nil {
				t.Fatal(err)
			}
			reifiers := map[string]bool{}
			for _, tr := range result.Triples {
				if tr.Predicate.Value == RDFReifiesIRI {
					reifiers[tr.Subject.Value] = true
				}
			}
			if len(result.Triples) != 6 || len(reifiers) != 2 {
				t.Fatalf("CONSTRUCT gave %d triples and %d reifiers, want 6 and 2:\n%s", len(result.Triples), len(reifiers), dumpQuads(result.Triples))
			}
		})
	}
}

// SPARQL 1.2 §4.2.4: each annotation block not preceded by its own reifier
// gets a fresh one, so two blocks are two reifiers, not one with both.
func TestEachUnnamedAnnotationBlockIsItsOwnReifier(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			mustUpdate(t, b.store, sparql12Prologue+`INSERT DATA { :s :p :o {| :source :a |} {| :source :b |} }`)
			rows := sparqlRows(t, b.store, sparql12Prologue+`SELECT ?r WHERE { ?r rdf:reifies <<( :s :p :o )>> }`)
			if len(rows) != 2 {
				t.Fatalf("two annotation blocks gave %d reifiers, want 2", len(rows))
			}
			// A named reifier covers the block right after it, not the next.
			mustUpdate(t, b.store, sparql12Prologue+`INSERT DATA { :x :p :o ~ :r {| :source :a |} {| :source :b |} }`)
			rows = sparqlRows(t, b.store, sparql12Prologue+`SELECT ?src WHERE { :r :source ?src }`)
			if len(rows) != 1 || rows[0]["src"].Value != rdf12ExNS+"a" {
				t.Fatalf(":r annotated with %v, want only :a", rows)
			}
			// In a pattern too: no single reifier has both sources, so
			// two blocks must not be forced onto one.
			if result, _ := b.store.ExecuteSPARQL(context.Background(), sparql12Prologue+`ASK { :s :p :o {| :source :a |} {| :source :b |} }`); !result.Boolean {
				t.Fatal("two annotation blocks in a pattern were forced onto one reifier")
			}
		})
	}
}

// The canonical spelling lower-cases language tags, so a tag's case never
// makes two triple terms of one.
func TestATripleTermIgnoresTheCaseOfALanguageTag(t *testing.T) {
	upper, err := NewTripleTerm(NewIRI(rdf12ExNS+"s"), NewIRI(rdf12ExNS+"p"), RDFTerm{Kind: RDFTermLiteral, Value: "x", Language: "EN-GB"})
	if err != nil {
		t.Fatal(err)
	}
	lower, err := NewTripleTerm(NewIRI(rdf12ExNS+"s"), NewIRI(rdf12ExNS+"p"), RDFTerm{Kind: RDFTermLiteral, Value: "x", Language: "en-gb"})
	if err != nil {
		t.Fatal(err)
	}
	if upper.Value != lower.Value {
		t.Fatalf("%s != %s", upper.Value, lower.Value)
	}
}

func TestDeleteTemplatesRefuseTheBlankNodesTheSugarWouldIntroduce(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			g := b.store
			for _, update := range []string{
				`DELETE DATA { :s :p :o {| :q :z |} }`,
				`DELETE { ?s :p ?o {| :q :z |} } WHERE { ?s :p ?o }`,
				`DELETE WHERE { << :s :p :o >> :q ?z }`,
			} {
				if _, err := g.ExecuteSPARQL(context.Background(), sparql12Prologue+update); err == nil || !strings.Contains(err.Error(), "blank node") {
					t.Fatalf("%s: got %v, want a refusal", update, err)
				}
			}
			// Naming the reifier makes it deletable.
			mustUpdate(t, g, sparql12Prologue+`INSERT DATA { :s :p :o ~ :r {| :q :z |} }`)
			if n := mustUpdate(t, g, sparql12Prologue+`DELETE DATA { :s :p :o ~ :r {| :q :z |} }`); n != 3 {
				t.Fatalf("DELETE DATA with a named reifier removed %d triples, want 3", n)
			}
		})
	}
}

func TestBaseDirectionFunctionsFollowSPARQL12(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			g := b.store
			rows := sparqlRows(t, g, `
		SELECT ?lang ?dir ?dt ?has ?hasDir ?made ?bad WHERE {
		  BIND("مرحبا"@ar--rtl AS ?v)
		  BIND(LANG(?v) AS ?lang)
		  BIND(LANGDIR(?v) AS ?dir)
		  BIND(DATATYPE(?v) AS ?dt)
		  BIND(hasLANG(?v) AS ?has)
		  BIND(hasLANGDIR("x"@en) AS ?hasDir)
		  BIND(STRLANGDIR("abc", "en", "ltr") AS ?made)
		  BIND(STRLANGDIR("abc", "en", "LTR") AS ?bad)
		}`)
			if len(rows) != 1 {
				t.Fatalf("rows = %v", rows)
			}
			r := rows[0]
			if r["lang"].Value != "ar" || r["dir"].Value != "rtl" || r["dt"].Value != rdf12DirLangStringIRI ||
				r["has"].Value != "true" || r["hasDir"].Value != "false" || r["made"].Language != "en--ltr" {
				t.Fatalf("base direction functions = %v", r)
			}
			if _, ok := r["bad"]; ok {
				t.Fatalf("STRLANGDIR with LTR should be an error, got %v", r["bad"])
			}
			if _, err := g.ExecuteSPARQL(context.Background(), `SELECT * WHERE { BIND("x"@en--up AS ?v) }`); err == nil {
				t.Fatal(`"x"@en--up parsed`)
			}
		})
	}
}

// Several update operations in one request run in order, each seeing the
// last, and the request reports their total.
func TestUpdateOperationsSeparatedBySemicolonsRunInOrder(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			g := b.store
			n := mustUpdate(t, g, sparql12Prologue+`
		INSERT DATA { :s :p :o {| :source :faraway |} } ;
		DELETE DATA { :s :p :o } ;
		INSERT { ?r :seen true } WHERE { ?r rdf:reifies <<( :s :p :o )>> }`)
			if n != 5 {
				t.Fatalf("three operations changed %d triples, want 3+1+1", n)
			}
			rows := sparqlRows(t, g, sparql12Prologue+`SELECT ?seen WHERE { << :s :p :o >> :seen ?seen }`)
			if len(rows) != 1 {
				t.Fatalf("the third operation did not see the first: %v", rows)
			}
		})
	}
}

func TestBlankNodesListsAndSequencePathsMatchInPatterns(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			g := b.store
			mustUpdate(t, g, sparql12Prologue+`INSERT DATA {
		  :a :knows [ :name "Bea" ; :age 30 ] .
		  :a :likes ( :tea :coffee ) .
		  :a :parent :b . :b :parent :c .
		}`)
			rows := sparqlRows(t, g, sparql12Prologue+`SELECT ?n WHERE { :a :knows [ :name ?n ; :age 30 ] }`)
			if len(rows) != 1 || rows[0]["n"].Value != "Bea" {
				t.Fatalf("[ ] in a pattern = %v", rows)
			}
			rows = sparqlRows(t, g, sparql12Prologue+`SELECT * WHERE { :a :likes ( :tea ?second ) }`)
			if len(rows) != 1 || rows[0]["second"].Value != rdf12ExNS+"coffee" {
				t.Fatalf("collection in a pattern = %v", rows)
			}
			for name := range rows[0] {
				if isHiddenVariable(name) {
					t.Fatalf("SELECT * projected the hidden variable %q", name)
				}
			}
			rows = sparqlRows(t, g, sparql12Prologue+`SELECT ?g WHERE { :a :parent/:parent ?g }`)
			if len(rows) != 1 || rows[0]["g"].Value != rdf12ExNS+"c" {
				t.Fatalf("sequence path = %v", rows)
			}
			rows = sparqlRows(t, g, sparql12Prologue+`SELECT ?x WHERE { :a :parent? ?x }`)
			var got []string
			for _, row := range rows {
				got = append(got, row["x"].Value)
			}
			sort.Strings(got)
			if strings.Join(got, ",") != rdf12ExNS+"a,"+rdf12ExNS+"b" {
				t.Fatalf("zero-or-one path = %v, want :a and :b", got)
			}
		})
	}
}

// SHACL walks from a focus node to its values; a triple term as focus node
// (the objects of rdf:reifies) has none, which is a violation to report and
// not an error to fail validation with. A directional literal's datatype is
// rdf:dirLangString.
func TestSHACLValidatesTripleTermsAndDirectionalLiterals(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			importDoc(t, b.store, RDFFormatTurtle, `
PREFIX : <http://example.org/>
:r <http://www.w3.org/1999/02/22-rdf-syntax-ns#reifies> <<( :a :b :c )>> .
:s :label "hello"@en--ltr .
`)
			shapes, err := parseRDFDocument(`
PREFIX sh: <http://www.w3.org/ns/shacl#>
PREFIX rdf: <http://www.w3.org/1999/02/22-rdf-syntax-ns#>
PREFIX : <http://example.org/>
:Reified a sh:NodeShape ; sh:targetObjectsOf rdf:reifies ;
    sh:property [ sh:path :checkedBy ; sh:minCount 1 ] .
:Labelled a sh:NodeShape ; sh:targetSubjectsOf :label ;
    sh:property [ sh:path :label ; sh:datatype rdf:dirLangString ] .
`, rdfSyntaxTurtle, "")
			if err != nil {
				t.Fatal(err)
			}
			report, err := b.store.ValidateSHACL(ctx, shapes)
			if err != nil {
				t.Fatalf("SHACL over a triple-term focus node failed: %v", err)
			}
			if report.Conforms || len(report.Results) != 1 || report.Results[0].FocusNode.Kind != RDFTermTriple {
				t.Fatalf("want exactly the triple term's missing :checkedBy reported, got %+v", report)
			}
		})
	}
}

// RDFS and OWL rules that turn an object into a subject must leave a triple
// term alone: RDF 1.2 has no triple with a triple-term subject, and one
// derivation that produced it used to fail the whole refresh.
func TestInferenceNeverMakesATripleTermASubject(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			importDoc(t, b.store, RDFFormatTurtle, `
PREFIX : <http://example.org/>
PREFIX rdfs: <http://www.w3.org/2000/01/rdf-schema#>
PREFIX owl: <http://www.w3.org/2002/07/owl#>
:says rdfs:range :Statement ; a owl:SymmetricProperty .
:alice :says <<( :a :b :c )>> .
:alice :claims << :x :y :z >> .
`)
			if _, err := b.store.RefreshRDFSInferences(ctx); err != nil {
				t.Fatalf("inference over a triple term: %v", err)
			}
			for _, tr := range storedTriples(t, b.store) {
				if tr.Subject.Kind == RDFTermTriple || tr.Predicate.Kind == RDFTermTriple {
					t.Fatalf("inference stored %s", tr.String())
				}
			}
		})
	}
}
