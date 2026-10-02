package graph

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

const rdf12ExNS = "http://example.org/"

func rdf12IRI(local string) RDFTerm { return NewIRI(rdf12ExNS + local) }

func mustTripleTerm(t *testing.T, s, p, o RDFTerm) RDFTerm {
	t.Helper()
	term, err := NewTripleTerm(s, p, o)
	if err != nil {
		t.Fatalf("NewTripleTerm: %v", err)
	}
	return term
}

func storedTriples(t *testing.T, g *GraphStore) []RDFTriple {
	t.Helper()
	triples, err := g.findStoredTriples(context.Background(), TriplePattern{})
	if err != nil {
		t.Fatalf("findStoredTriples: %v", err)
	}
	return triples
}

func importDoc(t *testing.T, g *GraphStore, format RDFFormat, doc string) int {
	t.Helper()
	n, err := g.ImportRDF(context.Background(), strings.NewReader(doc), format)
	if err != nil {
		t.Fatalf("import %s: %v\n%s", format, err, doc)
	}
	return n
}

// A blank node property list is one blank node however many predicate-object
// pairs it holds. The Turtle library this parser replaced gave each pair a
// fresh node of its own, so [ a sh:TripleRule ; sh:subject sh:this ] imported
// as two unrelated nodes and every SHACL rule written that way was lost.
func TestABlankNodePropertyListImportsAsOneBlankNode(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			importDoc(t, b.store, RDFFormatTurtle, `
@prefix sh: <http://www.w3.org/ns/shacl#> .
@prefix ex: <http://example.org/> .
ex:Shape sh:rule [ a sh:TripleRule ; sh:subject sh:this ; sh:predicate ex:p ;
                   sh:object [ ex:inner 1 ; ex:inner2 2 ] ] .
ex:s ex:list ( 1 [ ex:k ex:v ; ex:k2 ex:v2 ] ) .
`)
			ruleType := NewIRI("http://www.w3.org/ns/shacl#TripleRule")
			typed, err := b.store.FindTriples(ctx, TriplePattern{Predicate: ptrTerm(NewIRI(rdfTypeIRI)), Object: &ruleType})
			if err != nil || len(typed) != 1 {
				t.Fatalf("rdf:type sh:TripleRule: %v %v", typed, err)
			}
			rule := typed[0].Subject
			about, err := b.store.FindTriples(ctx, TriplePattern{Subject: &rule})
			if err != nil {
				t.Fatal(err)
			}
			if len(about) != 4 {
				t.Fatalf("the rule node has %d triples, want 4 (type, subject, predicate, object):\n%s", len(about), dumpQuads(about))
			}
			var inner RDFTerm
			for _, tr := range about {
				if tr.Predicate.Value == "http://www.w3.org/ns/shacl#object" {
					inner = tr.Object
				}
			}
			nested, err := b.store.FindTriples(ctx, TriplePattern{Subject: &inner})
			if err != nil || len(nested) != 2 {
				t.Fatalf("the nested [ ] has %d triples, want 2: %v", len(nested), err)
			}
			k := rdf12IRI("k")
			withK, err := b.store.FindTriples(ctx, TriplePattern{Predicate: &k})
			if err != nil || len(withK) != 1 {
				t.Fatalf("ex:k: %v %v", withK, err)
			}
			listMember := withK[0].Subject
			k2 := rdf12IRI("k2")
			withK2, err := b.store.FindTriples(ctx, TriplePattern{Subject: &listMember, Predicate: &k2})
			if err != nil || len(withK2) != 1 {
				t.Fatalf("the [ ] inside a collection split into separate nodes: %v %v", withK2, err)
			}
			first := NewIRI(rdf12NamespaceIRI + "first")
			firsts, err := b.store.FindTriples(ctx, TriplePattern{Predicate: &first, Object: &listMember})
			if err != nil || len(firsts) != 1 {
				t.Fatalf("the [ ] is not the collection's member: %v %v", firsts, err)
			}
		})
	}
}

func TestABlankNodePropertyListInsideATriGGraphIsOneBlankNode(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			importDoc(t, b.store, RDFFormatTriG, `
@prefix ex: <http://example.org/> .
ex:g { ex:s ex:p [ ex:a 1 ; ex:b 2 ; ex:c ( [ ex:d 3 ; ex:e 4 ] ) ] . }
`)
			a := rdf12IRI("a")
			withA, err := b.store.FindTriples(ctx, TriplePattern{Predicate: &a})
			if err != nil || len(withA) != 1 {
				t.Fatalf("ex:a: %v %v", withA, err)
			}
			node := withA[0].Subject
			if withA[0].Graph == nil || withA[0].Graph.Value != rdf12ExNS+"g" {
				t.Fatalf("the triple left its graph: %+v", withA[0])
			}
			about, err := b.store.FindTriples(ctx, TriplePattern{Subject: &node})
			if err != nil || len(about) != 3 {
				t.Fatalf("the [ ] in a graph has %d triples, want 3: %v", len(about), err)
			}
			d := rdf12IRI("d")
			withD, _ := b.store.FindTriples(ctx, TriplePattern{Predicate: &d})
			e := rdf12IRI("e")
			withE, _ := b.store.FindTriples(ctx, TriplePattern{Predicate: &e})
			if len(withD) != 1 || len(withE) != 1 || !termsEqual(withD[0].Subject, withE[0].Subject) {
				t.Fatalf("the nested [ ] in a collection split: %v %v", withD, withE)
			}
		})
	}
}

// RDF 1.2 Concepts §3.1: a triple term may be the object of a triple and
// nothing else. The store refuses the other positions instead of storing an
// RDF-star-style statement no RDF 1.2 system could exchange.
func TestATripleTermIsAcceptedOnlyAsAnObject(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			tt := mustTripleTerm(t, rdf12IRI("alice"), rdf12IRI("worksFor"), rdf12IRI("acme"))
			for name, triple := range map[string]RDFTriple{
				"subject":   {Subject: tt, Predicate: rdf12IRI("p"), Object: rdf12IRI("o")},
				"predicate": {Subject: rdf12IRI("s"), Predicate: tt, Object: rdf12IRI("o")},
				"graph":     {Subject: rdf12IRI("s"), Predicate: rdf12IRI("p"), Object: rdf12IRI("o"), Graph: &tt},
			} {
				triple := triple
				err := b.store.UpsertTriple(ctx, &triple)
				if err == nil || !strings.Contains(err.Error(), "only as objects") {
					t.Fatalf("a triple term as %s: got %v, want a refusal", name, err)
				}
			}
			ok := RDFTriple{Subject: rdf12IRI("r"), Predicate: NewIRI(RDFReifiesIRI), Object: tt}
			if err := b.store.UpsertTriple(ctx, &ok); err != nil {
				t.Fatalf("a triple term as object: %v", err)
			}
			if _, err := NewTripleTerm(NewLiteral("x"), rdf12IRI("p"), rdf12IRI("o")); err == nil {
				t.Fatal("a literal subject inside a triple term was accepted")
			}
			if _, err := NewTripleTerm(tt, rdf12IRI("p"), rdf12IRI("o")); err == nil {
				t.Fatal("a triple term as the subject of a triple term was accepted")
			}
		})
	}
}

// The canonical spelling is the term's identity, so two spellings of one
// triple term must store, match and compare as one term.
func TestTwoSpellingsOfOneTripleTermAreTheSameTerm(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.UpsertNamespace(ctx, Namespace{Prefix: "ex", URI: rdf12ExNS}); err != nil {
				t.Fatal(err)
			}
			handWritten := RDFTerm{Kind: RDFTermTriple, Value: `<<(<ex:alice>   <ex:name> "Alice"^^<http://www.w3.org/2001/XMLSchema#string>)>>`}
			first := RDFTriple{Subject: rdf12IRI("r1"), Predicate: NewIRI(RDFReifiesIRI), Object: handWritten}
			if err := b.store.UpsertTriple(ctx, &first); err != nil {
				t.Fatal(err)
			}
			canonical := mustTripleTerm(t, rdf12IRI("alice"), rdf12IRI("name"), NewLiteral("Alice"))
			second := RDFTriple{Subject: rdf12IRI("r1"), Predicate: NewIRI(RDFReifiesIRI), Object: canonical}
			if err := b.store.UpsertTriple(ctx, &second); err != nil {
				t.Fatal(err)
			}
			if first.ID != second.ID {
				t.Fatalf("one triple stored under two IDs: %s and %s", first.ID, second.ID)
			}
			all := storedTriples(t, b.store)
			if len(all) != 1 || all[0].Object.Value != canonical.Value {
				t.Fatalf("stored %v, want one triple whose object is %s", all, canonical.Value)
			}
		})
	}
}

// The question RDF 1.2 makes common — what are the reifiers of this triple? —
// is answered from the index, and only for the triple asked about.
func TestTheReifiersOfATripleTermAreFoundAndNoOthers(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			importDoc(t, b.store, RDFFormatTurtle, `
PREFIX : <http://example.org/>
:alice :worksFor :acme ~ :r1 ~ :r2 {| :source <http://example.org/doc1> |} .
<< :alice :worksFor :initech ~ :r3 >> :source :doc2 .
:bob :worksFor :acme {| :confidence 0.4 |} .
`)
			reifiers, err := b.store.FindReifiers(ctx, rdf12IRI("alice"), rdf12IRI("worksFor"), rdf12IRI("acme"))
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]bool{}
			for _, r := range reifiers {
				got[r.Value] = true
			}
			if len(got) != 2 || !got[rdf12ExNS+"r1"] || !got[rdf12ExNS+"r2"] {
				t.Fatalf("reifiers of <<( :alice :worksFor :acme )>> = %v, want :r1 and :r2", reifiers)
			}
			reified, err := b.store.FindReifiedTriples(ctx, rdf12IRI("r3"))
			if err != nil || len(reified) != 1 {
				t.Fatalf("FindReifiedTriples(:r3) = %v, %v", reified, err)
			}
			_, _, o, err := reified[0].TripleTermParts()
			if err != nil || o.Value != rdf12ExNS+"initech" {
				t.Fatalf("parts of %s: %v %v", reified[0].Value, o, err)
			}
			// Reified, not asserted: << >> does not state the triple itself.
			initech := rdf12IRI("initech")
			asserted, _ := b.store.FindTriples(ctx, TriplePattern{Subject: ptrTerm(rdf12IRI("alice")), Object: &initech})
			if len(asserted) != 0 {
				t.Fatalf("a reified triple was asserted: %v", asserted)
			}
		})
	}
}

// The component index answers a pattern over the parts of a triple term in
// SQL, and must agree with matching the parts one by one.
func TestTheTripleTermIndexNarrowsByComponent(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			importDoc(t, b.store, RDFFormatTurtle, `
PREFIX : <http://example.org/>
:r1 <http://www.w3.org/1999/02/22-rdf-syntax-ns#reifies> <<( :alice :knows :bob )>> .
:r2 <http://www.w3.org/1999/02/22-rdf-syntax-ns#reifies> <<( :alice :likes "tea"@en )>> .
:r3 <http://www.w3.org/1999/02/22-rdf-syntax-ns#reifies> <<( :carol :knows <<( :alice :knows :bob )>> )>> .
`)
			count := func(filter tripleTermFilter) int {
				t.Helper()
				found, err := b.store.FindTriples(ctx, TriplePattern{objectTriple: &filter})
				if err != nil {
					t.Fatal(err)
				}
				return len(found)
			}
			alice, knows, tea := rdf12IRI("alice"), rdf12IRI("knows"), NewLangLiteral("tea", "EN")
			nested := mustTripleTerm(t, rdf12IRI("alice"), rdf12IRI("knows"), rdf12IRI("bob"))
			if n := count(tripleTermFilter{}); n != 3 {
				t.Fatalf("any triple term: %d, want 3", n)
			}
			if n := count(tripleTermFilter{Subject: &alice}); n != 2 {
				t.Fatalf("subject :alice: %d, want 2", n)
			}
			if n := count(tripleTermFilter{Predicate: &knows}); n != 2 {
				t.Fatalf("predicate :knows: %d, want 2", n)
			}
			if n := count(tripleTermFilter{Object: &tea}); n != 1 {
				t.Fatalf("object \"tea\"@en: %d, want 1", n)
			}
			if n := count(tripleTermFilter{Object: &nested}); n != 1 {
				t.Fatalf("object <<( :alice :knows :bob )>>: %d, want 1", n)
			}
		})
	}
}

func TestDeletingTheLastUseOfATripleTermRemovesItsIndexRow(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			inner := mustTripleTerm(t, rdf12IRI("a"), rdf12IRI("b"), rdf12IRI("c"))
			outer := mustTripleTerm(t, rdf12IRI("x"), rdf12IRI("says"), inner)
			first := RDFTriple{Subject: rdf12IRI("r1"), Predicate: NewIRI(RDFReifiesIRI), Object: outer}
			second := RDFTriple{Subject: rdf12IRI("r2"), Predicate: NewIRI(RDFReifiesIRI), Object: outer}
			for _, tr := range []*RDFTriple{&first, &second} {
				if err := b.store.UpsertTriple(ctx, tr); err != nil {
					t.Fatal(err)
				}
			}
			rows := func() int {
				var n int
				if err := b.store.queryRow(ctx, `SELECT COUNT(*) FROM kg_triple_terms`).Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n
			}
			if n := rows(); n != 2 {
				t.Fatalf("index rows after two uses of a nested term: %d, want 2 (outer and inner)", n)
			}
			if err := b.store.DeleteTriple(ctx, first); err != nil {
				t.Fatal(err)
			}
			if n := rows(); n != 2 {
				t.Fatalf("index rows while a use remains: %d, want 2", n)
			}
			if err := b.store.DeleteTriple(ctx, second); err != nil {
				t.Fatal(err)
			}
			if n := rows(); n != 0 {
				t.Fatalf("index rows after the last use: %d, want 0", n)
			}
		})
	}
}

// Everything RDF 1.2 adds survives a trip out through every line and Turtle
// format and back into a fresh store on the same backend.
func TestRDF12DataRoundTripsThroughEveryTextFormat(t *testing.T) {
	doc := `
VERSION "1.2"
PREFIX : <http://example.org/>
PREFIX xsd: <http://www.w3.org/2001/XMLSchema#>
:alice :worksFor :acme ~ :r1 {| :source <http://example.org/doc1> ; :confidence 0.9 |} .
<< :bob :said << :alice :likes "tea"@en--ltr >> >> :at "2026-01-01"^^xsd:date .
_:x :about <<( _:x :p "a \"quoted\"\ttab\nline\u0001" )>> .
:s :empty "" ; :padded "  spaced  " ; :rtl "مرحبا"@ar--rtl .
`
	src := backends(t)
	for _, format := range []RDFFormat{RDFFormatNTriples, RDFFormatNQuads, RDFFormatTurtle, RDFFormatTriG} {
		dst := backends(t)
		for i := range src {
			t.Run(string(format)+"/"+src[i].name, func(t *testing.T) {
				ctx := context.Background()
				if len(storedTriples(t, src[i].store)) == 0 {
					importDoc(t, src[i].store, RDFFormatTurtle, doc)
				}
				var out bytes.Buffer
				if err := src[i].store.ExportRDF(ctx, &out, format); err != nil {
					t.Fatalf("export: %v", err)
				}
				importDoc(t, dst[i].store, format, out.String())
				if ok, diff := isomorphicDatasets(storedTriples(t, dst[i].store), storedTriples(t, src[i].store)); !ok {
					t.Fatalf("round trip through %s changed the data:\n%s\nexported:\n%s", format, diff, out.String())
				}
			})
		}
	}
}

func TestJSONLDExportRefusesATripleTermRatherThanDroppingIt(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			importDoc(t, b.store, RDFFormatNTriples,
				`<http://example.org/r> <http://www.w3.org/1999/02/22-rdf-syntax-ns#reifies> <<( <http://example.org/a> <http://example.org/b> <http://example.org/c> )>> .`+"\n")
			var out bytes.Buffer
			err := b.store.ExportRDF(ctx, &out, RDFFormatJSONLD)
			if err == nil || !strings.Contains(err.Error(), "triple term") {
				t.Fatalf("JSON-LD export of a triple term: %v, want a refusal naming it", err)
			}
		})
	}
}

func TestABaseDirectionIsStoredWithItsLiteralAndExportedToJSONLD(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			importDoc(t, b.store, RDFFormatTurtle, `<http://example.org/s> <http://example.org/p> "Hello"@EN-gb--ltr, "Hello"@en-gb .`)
			all := storedTriples(t, b.store)
			if len(all) != 2 {
				t.Fatalf("\"Hello\"@en-gb--ltr and \"Hello\"@en-gb are two terms; stored %d", len(all))
			}
			var directed RDFTerm
			for _, tr := range all {
				if tr.Object.BaseDirection() != "" {
					directed = tr.Object
				}
			}
			if directed.LanguageTag() != "en-gb" || directed.BaseDirection() != "ltr" || rdfLiteralDatatype(directed) != rdf12DirLangStringIRI {
				t.Fatalf("directed literal read back as %+v", directed)
			}
			var out bytes.Buffer
			if err := b.store.ExportRDF(ctx, &out, RDFFormatJSONLD); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), `"@direction": "ltr"`) && !strings.Contains(out.String(), `"@direction":"ltr"`) {
				t.Fatalf("JSON-LD export lost the base direction:\n%s", out.String())
			}
			bad := RDFTriple{Subject: rdf12IRI("s"), Predicate: rdf12IRI("p"), Object: RDFTerm{Kind: RDFTermLiteral, Value: "x", Language: "en--up"}}
			if err := b.store.UpsertTriple(ctx, &bad); err == nil {
				t.Fatal("a base direction other than ltr or rtl was stored")
			}
		})
	}
}

// Creating the triple-term index is the whole migration, and it must be
// harmless on a store that already has one and recover one that lost it.
func TestTheTripleTermMigrationIsIdempotentAndRecreatesAMissingTable(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			plain := RDFTriple{Subject: rdf12IRI("s"), Predicate: rdf12IRI("p"), Object: NewLiteral("pre-1.2 row")}
			if err := b.store.UpsertTriple(ctx, &plain); err != nil {
				t.Fatal(err)
			}
			if _, err := b.store.exec(ctx, `DROP TABLE kg_triple_terms`); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := b.store.createGraphSchema(ctx); err != nil {
					t.Fatalf("schema creation %d: %v", i+1, err)
				}
			}
			tt := mustTripleTerm(t, rdf12IRI("s"), rdf12IRI("p"), NewLiteral("pre-1.2 row"))
			reifier := RDFTriple{Subject: rdf12IRI("r"), Predicate: NewIRI(RDFReifiesIRI), Object: tt}
			if err := b.store.UpsertTriple(ctx, &reifier); err != nil {
				t.Fatal(err)
			}
			all := storedTriples(t, b.store)
			if len(all) != 2 {
				t.Fatalf("after migration: %d triples, want the old row and the new one", len(all))
			}
		})
	}
}
