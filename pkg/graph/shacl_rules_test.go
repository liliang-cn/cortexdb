package graph

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Every test here runs on both backends through backends(t). The rules read
// the data through FindTriples and write through the inference path, so a
// backend difference in either would show up as a different set of triples.

const shaclRulePrefixes = `
@prefix ex: <http://ex/> .
@prefix sh: <http://www.w3.org/ns/shacl#> .
@prefix rdf: <http://www.w3.org/1999/02/22-rdf-syntax-ns#> .
@prefix rdfs: <http://www.w3.org/2000/01/rdf-schema#> .
@prefix xsd: <http://www.w3.org/2001/XMLSchema#> .
`

// turtleTriples parses Turtle (with the prefixes above) into triples with the
// importer's own parser, so the SHACL-AF examples are written verbatim and
// read the way a caller's import would read them. The parser labels each
// document's blank nodes with a fresh prefix, so data and shapes parsed
// separately never share one; tag only names the document in failures.
func turtleTriples(t *testing.T, tag, src string) []RDFTriple {
	t.Helper()
	triples, err := parseRDFDocument(shaclRulePrefixes+src, rdfSyntaxTurtle, "")
	if err != nil {
		t.Fatalf("parse turtle (%s): %v", tag, err)
	}
	return triples
}

func loadTurtle(t *testing.T, g *GraphStore, src string) {
	t.Helper()
	triples := turtleTriples(t, "d", src)
	result, err := g.UpsertTriplesBatch(context.Background(), data(triples...))
	if err != nil {
		t.Fatalf("load data: %v", err)
	}
	if result.FailedCount > 0 {
		t.Fatalf("load data: %v", result.Errors[0])
	}
}

func derivedKeys(result *SHACLRuleResult) []string {
	keys := make([]string, 0, len(result.Derived))
	for _, tr := range result.Derived {
		keys = append(keys, shaclTermKey(tr.Subject)+" "+shaclTermKey(tr.Predicate)+" "+shaclTermKey(tr.Object))
	}
	sort.Strings(keys)
	return keys
}

func applyRules(t *testing.T, g *GraphStore, shapes string, opts SHACLRuleOptions) *SHACLRuleResult {
	t.Helper()
	result, err := g.ApplySHACLRules(context.Background(), turtleTriples(t, "s", shapes), opts)
	if err != nil {
		t.Fatalf("ApplySHACLRules: %v", err)
	}
	return result
}

func wantDerived(t *testing.T, result *SHACLRuleResult, want ...string) {
	t.Helper()
	sort.Strings(want)
	if want == nil {
		want = []string{}
	}
	if got := derivedKeys(result); !reflect.DeepEqual(got, want) {
		t.Fatalf("derived\n got  %q\n want %q", got, want)
	}
}

// requireExplainable checks that every derived triple is stored as inferred
// under its rule and that each support id names a triple the store can
// return — the property ExplainTripleTrace depends on.
func requireExplainable(t *testing.T, g *GraphStore, result *SHACLRuleResult) {
	t.Helper()
	ctx := context.Background()
	for _, tr := range result.Derived {
		explanation, err := g.ExplainTriple(ctx, tr.ID)
		if err != nil {
			t.Fatalf("explain %s: %v", tr, err)
		}
		if explanation.Explicit || !strings.HasPrefix(explanation.Rule, SHACLTripleRuleNamePrefix) {
			t.Fatalf("%s explains as explicit=%v rule=%q", tr, explanation.Explicit, explanation.Rule)
		}
		if !reflect.DeepEqual(explanation.SupportTripleIDs, tr.SupportIDs) {
			t.Fatalf("%s stored supports %v, derived with %v", tr, explanation.SupportTripleIDs, tr.SupportIDs)
		}
		for _, id := range explanation.SupportTripleIDs {
			if _, err := g.GetTriple(ctx, id); err != nil {
				t.Fatalf("%s support %s does not resolve: %v", tr, id, err)
			}
		}
	}
}

const rectangleExampleShapes = `
ex:Rectangle
	a rdfs:Class, sh:NodeShape ;
	rdfs:label "Rectangle" ;
	sh:property [
		sh:path ex:height ;
		sh:datatype xsd:integer ;
		sh:maxCount 1 ;
		sh:minCount 1 ;
		sh:name "height" ;
	] ;
	sh:property [
		sh:path ex:width ;
		sh:datatype xsd:integer ;
		sh:maxCount 1 ;
		sh:minCount 1 ;
		sh:name "width" ;
	] ;
	sh:rule [
		a sh:TripleRule ;
		sh:subject sh:this ;
		sh:predicate rdf:type ;
		sh:object ex:Square ;
		sh:condition ex:Rectangle ;
		sh:condition [
			sh:property [
				sh:path ex:width ;
				sh:equals ex:height ;
			] ;
		] ;
	] .
`

const rectangleExampleData = `
ex:InvalidRectangle
	a ex:Rectangle .
ex:NonSquareRectangle
	a ex:Rectangle ;
	ex:height 2 ;
	ex:width 3 .
ex:SquareRectangle
	a ex:Rectangle ;
	ex:height 4 ;
	ex:width 4 .
`

func TestTheNoteSquareExampleInfersExactlyTheSquare(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadTurtle(t, b.store, rectangleExampleData)
			result := applyRules(t, b.store, rectangleExampleShapes, SHACLRuleOptions{})
			wantDerived(t, result, "SquareRectangle type Square")
			requireExplainable(t, b.store, result)

			// The support is the target triple and what the conditions read:
			// the rdf:type, the width and the height of the square.
			ctx := context.Background()
			want := map[string]bool{}
			for _, p := range []RDFTriple{
				tri(ex("SquareRectangle"), NewIRI(RDFType), ex("Rectangle")),
				tri(ex("SquareRectangle"), ex("width"), NewTypedLiteral("4", xsd("integer"))),
				tri(ex("SquareRectangle"), ex("height"), NewTypedLiteral("4", xsd("integer"))),
			} {
				normalized, err := b.store.normalizeTriple(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				want[tripleDigest(normalized)] = true
			}
			for _, id := range result.Derived[0].SupportIDs {
				delete(want, id)
			}
			if len(want) > 0 {
				t.Fatalf("supports %v miss %v", result.Derived[0].SupportIDs, want)
			}
		})
	}
}

func TestTheNoteAreaTripleRuleComputesAreaThroughARegisteredFunction(t *testing.T) {
	const shapes = `
ex:RectangleShape
	a sh:NodeShape ;
	sh:targetClass ex:Rectangle ;
	sh:property [
		sh:path ex:width ;
		sh:datatype xsd:integer ;
		sh:minCount 1 ;
		sh:maxCount 1 ;
	] ;
	sh:property [
		sh:path ex:height ;
		sh:datatype xsd:integer ;
		sh:minCount 1 ;
		sh:maxCount 1 ;
	] .
ex:RectangleRulesShape
	a sh:NodeShape ;
	sh:targetClass ex:Rectangle ;
	sh:rule [
		a sh:TripleRule ;
		sh:subject sh:this ;
		sh:predicate ex:area ;
		sh:object [
			ex:multiply ( [ sh:path ex:width ] [ sh:path ex:height ] ) ;
		] ;
		sh:condition ex:RectangleShape ;
	] .
`
	const dataTTL = `
ex:ExampleRectangle
	a ex:Rectangle ;
	ex:width 7 ;
	ex:height 8 .
ex:InvalidRectangle
	a ex:Rectangle ;
	ex:width 7 .
`
	multiply := func(args []RDFTerm) ([]RDFTerm, error) {
		product := int64(1)
		for _, a := range args {
			n, err := strconv.ParseInt(a.Value, 10, 64)
			if err != nil {
				return nil, nil
			}
			product *= n
		}
		return []RDFTerm{NewTypedLiteral(strconv.FormatInt(product, 10), xsd("integer"))}, nil
	}
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadTurtle(t, b.store, dataTTL)
			result := applyRules(t, b.store, shapes, SHACLRuleOptions{Functions: map[string]SHACLFunction{shaclTestNS + "multiply": multiply}})
			wantDerived(t, result, `ExampleRectangle area "56"^^integer`)
			requireExplainable(t, b.store, result)
			if len(result.Derived[0].SupportIDs) < 3 {
				t.Fatalf("area supports %v: want type, width and height", result.Derived[0].SupportIDs)
			}

			_, err := b.store.ApplySHACLRules(context.Background(), turtleTriples(t, "s", shapes), SHACLRuleOptions{DryRun: true})
			if err == nil || !strings.Contains(err.Error(), "not registered") {
				t.Fatalf("an unregistered function must be refused, got %v", err)
			}
		})
	}
}

// The note's sh:order example is written as SPARQL rules; these are the same
// two rules as triple rules. Cousins need uncles, so the order matters within
// one pass and the result needs both.
const uncleCousinShapes = `
ex:RuleOrderExampleShape
	a sh:NodeShape ;
	sh:targetClass ex:Person ;
	sh:rule [
		a sh:TripleRule ;
		sh:order 2 ;
		sh:subject sh:this ;
		sh:predicate ex:cousin ;
		sh:object [ sh:path [ sh:inversePath ex:parent ] ; sh:nodes [ sh:path ex:uncle ] ] ;
	] ;
	sh:rule [
		a sh:TripleRule ;
		sh:order 1 ;
		sh:subject sh:this ;
		sh:predicate ex:uncle ;
		sh:object [
			sh:filterShape [ sh:property [ sh:path ex:gender ; sh:hasValue ex:male ] ] ;
			sh:nodes [ sh:path ex:sibling ; sh:nodes [ sh:path ex:parent ] ] ;
		] ;
	] .
`

const uncleCousinData = `
ex:alice a ex:Person ; ex:parent ex:pat .
ex:pat a ex:Person ; ex:sibling ex:uma, ex:sue .
ex:uma a ex:Person ; ex:gender ex:male .
ex:sue a ex:Person ; ex:gender ex:female .
ex:carl a ex:Person ; ex:parent ex:uma .
ex:cara a ex:Person ; ex:parent ex:sue .
`

func TestTheNoteUncleAndCousinRulesRunInOrderAndChain(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadTurtle(t, b.store, uncleCousinData)
			result := applyRules(t, b.store, uncleCousinShapes, SHACLRuleOptions{})
			wantDerived(t, result, "alice uncle uma", "alice cousin carl")
			if result.Iterations != 2 {
				t.Fatalf("ordered rules reach the fixpoint in one productive pass plus a check, took %d", result.Iterations)
			}
			requireExplainable(t, b.store, result)
			for _, tr := range result.Derived {
				if tr.Predicate.Value == shaclTestNS+"cousin" {
					uncle := tripleDigest(RDFTriple{Subject: ex("alice"), Predicate: ex("uncle"), Object: ex("uma")})
					if !containsString(tr.SupportIDs, uncle) {
						t.Fatalf("the cousin is supported by the inferred uncle triple %s, got %v", uncle, tr.SupportIDs)
					}
				}
			}
		})
	}
}

func TestARuleThatNeedsALaterRulesOutputIsReachedByIterating(t *testing.T) {
	// Reverse the orders: the cousin rule runs first and sees no uncle until
	// the second pass. A single §8.4 pass would miss the cousin.
	shapes := strings.Replace(strings.Replace(uncleCousinShapes, "sh:order 2", "sh:order 0", 1), "sh:order 1", "sh:order 5", 1)
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadTurtle(t, b.store, uncleCousinData)
			result := applyRules(t, b.store, shapes, SHACLRuleOptions{DryRun: true})
			wantDerived(t, result, "alice uncle uma", "alice cousin carl")
			if result.Iterations != 3 {
				t.Fatalf("want 3 passes (uncle, cousin, nothing), got %d", result.Iterations)
			}
		})
	}
}

func TestOrderDecidesWhatANonMonotoneConditionSees(t *testing.T) {
	// Rule "mark" (order 0) gives every thing ex:mark; rule "flag" (order 1)
	// flags things with no mark. Run in order, flag never fires. Run in the
	// other order, or in one snapshot, it would flag everything.
	const shapes = `
ex:S a sh:NodeShape ;
	sh:targetClass ex:Thing ;
	sh:rule [ a sh:TripleRule ; sh:order 1 ;
		sh:subject sh:this ; sh:predicate ex:flag ; sh:object ex:unmarked ;
		sh:condition [ sh:property [ sh:path ex:mark ; sh:maxCount 0 ] ] ] ;
	sh:rule [ a sh:TripleRule ; sh:order 0 ;
		sh:subject sh:this ; sh:predicate ex:mark ; sh:object ex:m ] .
`
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadTurtle(t, b.store, `ex:a a ex:Thing . ex:b a ex:Thing .`)
			wantDerived(t, applyRules(t, b.store, shapes, SHACLRuleOptions{DryRun: true}), "a mark m", "b mark m")

			// The same two rules at one order do not see each other.
			same := strings.Replace(shapes, "sh:order 1", "sh:order 0", 1)
			wantDerived(t, applyRules(t, b.store, same, SHACLRuleOptions{DryRun: true}),
				"a mark m", "b mark m", "a flag unmarked", "b flag unmarked")

			// Shape order: the flagging shape first.
			split := `
ex:Flag a sh:NodeShape ; sh:order 0 ; sh:targetClass ex:Thing ;
	sh:rule [ a sh:TripleRule ; sh:subject sh:this ; sh:predicate ex:flag ; sh:object ex:unmarked ;
		sh:condition [ sh:property [ sh:path ex:mark ; sh:maxCount 0 ] ] ] .
ex:Mark a sh:NodeShape ; sh:order -1 ; sh:targetClass ex:Thing ;
	sh:rule [ a sh:TripleRule ; sh:subject sh:this ; sh:predicate ex:mark ; sh:object ex:m ] .
`
			wantDerived(t, applyRules(t, b.store, split, SHACLRuleOptions{DryRun: true}), "a mark m", "b mark m")
		})
	}
}

func TestNodeExpressionsUnionIntersectionAndInversePathProduceExactlyTheirSets(t *testing.T) {
	const shapes = `
ex:S a sh:NodeShape ;
	sh:targetNode ex:a ;
	sh:rule [ a sh:TripleRule ; sh:subject sh:this ; sh:predicate ex:either ;
		sh:object [ sh:union ( [ sh:path ex:p ] [ sh:path ex:q ] ) ] ] ;
	sh:rule [ a sh:TripleRule ; sh:subject sh:this ; sh:predicate ex:both ;
		sh:object [ sh:intersection ( [ sh:path ex:p ] [ sh:path ex:q ] ) ] ] ;
	sh:rule [ a sh:TripleRule ; sh:subject [ sh:path [ sh:inversePath ex:knows ] ] ;
		sh:predicate ex:knowsOf ; sh:object sh:this ] ;
	sh:rule [ a sh:TripleRule ; sh:subject sh:this ; sh:predicate ex:label ; sh:object "A"@en ] .
`
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadTurtle(t, b.store, `
ex:a ex:p ex:x, ex:y ; ex:q ex:y, ex:z .
ex:k1 ex:knows ex:a . ex:k2 ex:knows ex:a . ex:k3 ex:knows ex:other .`)
			result := applyRules(t, b.store, shapes, SHACLRuleOptions{})
			wantDerived(t, result,
				"a either x", "a either y", "a either z", "a both y",
				"k1 knowsOf a", "k2 knowsOf a", `a label "A"@en`)
			requireExplainable(t, b.store, result)
			for _, tr := range result.Derived {
				if tr.Predicate.Value == shaclTestNS+"both" && len(tr.SupportIDs) != 2 {
					t.Fatalf("an intersection value is supported by its triple in each member, got %v", tr.SupportIDs)
				}
			}
		})
	}
}

func TestATripleAlreadyAssertedIsNotInferredOrOverwritten(t *testing.T) {
	const shapes = `ex:S sh:targetNode ex:a ; sh:rule [ a sh:TripleRule ; sh:subject sh:this ; sh:predicate ex:p ; sh:object ex:b ] .`
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadTurtle(t, b.store, `ex:a ex:p ex:b .`)
			result := applyRules(t, b.store, shapes, SHACLRuleOptions{})
			wantDerived(t, result)
			explicit := false
			triples, err := b.store.FindTriples(context.Background(), TriplePattern{Subject: ptrTerm(ex("a")), Inferred: &explicit})
			if err != nil || len(triples) != 1 {
				t.Fatalf("the asserted triple must stay asserted: %v %v", triples, err)
			}
		})
	}
}

func TestDeactivatedRulesAndShapesInferNothing(t *testing.T) {
	const shapes = `
ex:On sh:targetNode ex:a ;
	sh:rule [ a sh:TripleRule ; sh:deactivated true ; sh:subject sh:this ; sh:predicate ex:p ; sh:object ex:off ] ;
	sh:rule [ a sh:TripleRule ; sh:deactivated false ; sh:subject sh:this ; sh:predicate ex:p ; sh:object ex:on ] .
ex:Off sh:targetNode ex:a ; sh:deactivated true ;
	sh:rule [ a sh:TripleRule ; sh:subject sh:this ; sh:predicate ex:p ; sh:object ex:shapeOff ] .
`
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			wantDerived(t, applyRules(t, b.store, shapes, SHACLRuleOptions{DryRun: true}), "a p on")
		})
	}
}

func TestUnsupportedRulesAndExpressionsAreRefusedNotIgnored(t *testing.T) {
	cases := map[string]string{
		"sparql rule":       `ex:S sh:targetNode ex:a ; sh:rule [ a sh:SPARQLRule ; sh:construct "CONSTRUCT {} WHERE {}" ] .`,
		"untyped rule":      `ex:S sh:targetNode ex:a ; sh:rule [ sh:subject sh:this ; sh:predicate ex:p ; sh:object ex:o ] .`,
		"two subjects":      `ex:S sh:targetNode ex:a ; sh:rule [ a sh:TripleRule ; sh:subject sh:this, ex:b ; sh:predicate ex:p ; sh:object ex:o ] .`,
		"missing object":    `ex:S sh:targetNode ex:a ; sh:rule [ a sh:TripleRule ; sh:subject sh:this ; sh:predicate ex:p ] .`,
		"sequence path":     `ex:S sh:targetNode ex:a ; sh:rule [ a sh:TripleRule ; sh:subject sh:this ; sh:predicate ex:p ; sh:object [ sh:path ( ex:p ex:q ) ] ] .`,
		"one-member union":  `ex:S sh:targetNode ex:a ; sh:rule [ a sh:TripleRule ; sh:subject sh:this ; sh:predicate ex:p ; sh:object [ sh:union ( sh:this ) ] ] .`,
		"filter no nodes":   `ex:S sh:targetNode ex:a ; sh:rule [ a sh:TripleRule ; sh:subject sh:this ; sh:predicate ex:p ; sh:object [ sh:filterShape ex:T ] ] .`,
		"unknown blank":     `ex:S sh:targetNode ex:a ; sh:rule [ a sh:TripleRule ; sh:subject sh:this ; sh:predicate ex:p ; sh:object [ ex:x ex:y ] ] .`,
		"two orders":        `ex:S sh:targetNode ex:a ; sh:rule [ a sh:TripleRule ; sh:order 1, 2 ; sh:subject sh:this ; sh:predicate ex:p ; sh:object ex:o ] .`,
		"recursive filter":  `ex:R sh:node ex:R . ex:S sh:targetNode ex:a ; sh:rule [ a sh:TripleRule ; sh:subject sh:this ; sh:predicate ex:p ; sh:object [ sh:filterShape ex:R ; sh:nodes sh:this ] ] .`,
		"literal condition": `ex:S sh:targetNode ex:a ; sh:rule [ a sh:TripleRule ; sh:condition "x" ; sh:subject sh:this ; sh:predicate ex:p ; sh:object ex:o ] .`,
	}
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			for name, shapes := range cases {
				if _, err := b.store.ApplySHACLRules(context.Background(), turtleTriples(t, "s", shapes), SHACLRuleOptions{DryRun: true}); err == nil {
					t.Errorf("%s: expected an error", name)
				}
			}
		})
	}
}

func TestRerunningIsIdempotentAndRemovingARuleRetractsOnlyWhatItInferred(t *testing.T) {
	const both = `
ex:S sh:targetClass ex:Person ;
	sh:rule ex:uncleRule, ex:greetRule .
ex:uncleRule a sh:TripleRule ; sh:subject sh:this ; sh:predicate ex:uncle ;
	sh:object [ sh:path ex:sibling ; sh:nodes [ sh:path ex:parent ] ] .
ex:greetRule a sh:TripleRule ; sh:subject sh:this ; sh:predicate ex:greets ;
	sh:object [ sh:union ( [ sh:path ex:uncle ] [ sh:path ex:friend ] ) ] .
`
	const greetOnly = `
ex:S sh:targetClass ex:Person ; sh:rule ex:greetRule .
ex:greetRule a sh:TripleRule ; sh:subject sh:this ; sh:predicate ex:greets ;
	sh:object [ sh:union ( [ sh:path ex:uncle ] [ sh:path ex:friend ] ) ] .
`
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			loadTurtle(t, b.store, `
ex:a a ex:Person ; ex:parent ex:p ; ex:friend ex:f .
ex:p ex:sibling ex:u .`)
			first := applyRules(t, b.store, both, SHACLRuleOptions{})
			wantDerived(t, first, "a uncle u", "a greets u", "a greets f")
			if first.Added != 3 || first.Retracted != 0 {
				t.Fatalf("first run: %+v", first)
			}
			if first.PerRule[SHACLTripleRuleNamePrefix+shaclTestNS+"uncleRule"] != 1 || first.PerRule[SHACLTripleRuleNamePrefix+shaclTestNS+"greetRule"] != 2 {
				t.Fatalf("rule names: %v", first.PerRule)
			}
			requireExplainable(t, b.store, first)

			again := applyRules(t, b.store, both, SHACLRuleOptions{})
			wantDerived(t, again, "a uncle u", "a greets u", "a greets f")
			if again.Added != 0 || again.Updated != 0 || again.Retracted != 0 || again.Unchanged != 3 {
				t.Fatalf("a re-run must change nothing: %+v", again)
			}

			summary, err := b.store.InferenceSummary(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if summary.InferredCount != 3 {
				t.Fatalf("stored inferred triples after two runs: %d, want 3", summary.InferredCount)
			}

			// Without the uncle rule, its triple goes, and so does the greeting
			// that only the uncle justified; the friend greeting stays.
			removed := applyRules(t, b.store, greetOnly, SHACLRuleOptions{})
			wantDerived(t, removed, "a greets f")
			if removed.Retracted != 2 || removed.Unchanged != 1 {
				t.Fatalf("removing a rule: %+v", removed)
			}
			inferred := true
			left, err := b.store.FindTriples(ctx, TriplePattern{Inferred: &inferred})
			if err != nil {
				t.Fatal(err)
			}
			if len(left) != 1 || left[0].Object.Value != shaclTestNS+"f" {
				t.Fatalf("left after removal: %v", left)
			}

			// An empty rule set retracts everything the rules inferred.
			none := applyRules(t, b.store, `ex:S sh:targetClass ex:Person .`, SHACLRuleOptions{})
			if none.Retracted != 1 {
				t.Fatalf("empty rule set: %+v", none)
			}
		})
	}
}

func TestADryRunWritesNothing(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadTurtle(t, b.store, rectangleExampleData)
			result := applyRules(t, b.store, rectangleExampleShapes, SHACLRuleOptions{DryRun: true})
			wantDerived(t, result, "SquareRectangle type Square")
			if result.Added != 0 || !result.DryRun {
				t.Fatalf("dry run: %+v", result)
			}
			inferred := true
			stored, err := b.store.FindTriples(context.Background(), TriplePattern{Inferred: &inferred})
			if err != nil || len(stored) != 0 {
				t.Fatalf("a dry run stored %v (%v)", stored, err)
			}
		})
	}
}

func TestAnEndlessRuleSetFailsInsteadOfStoringAPartialResult(t *testing.T) {
	const shapes = `ex:S sh:targetSubjectsOf ex:next ;
	sh:rule [ a sh:TripleRule ; sh:subject [ ex:succ ( sh:this ) ] ; sh:predicate ex:next ; sh:object sh:this ] .`
	succ := func(args []RDFTerm) ([]RDFTerm, error) { return []RDFTerm{NewIRI(args[0].Value + "'")}, nil }
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadTurtle(t, b.store, `ex:a ex:next ex:b .`)
			_, err := b.store.ApplySHACLRules(context.Background(), turtleTriples(t, "s", shapes),
				SHACLRuleOptions{MaxIterations: 5, Functions: map[string]SHACLFunction{shaclTestNS + "succ": succ}})
			if err == nil || !strings.Contains(err.Error(), "fixpoint") {
				t.Fatalf("want a fixpoint error, got %v", err)
			}
			inferred := true
			stored, _ := b.store.FindTriples(context.Background(), TriplePattern{Inferred: &inferred})
			if len(stored) != 0 {
				t.Fatalf("a failed run stored %d triples", len(stored))
			}
		})
	}
}

func TestSHACLEqualsAndDisjointValidateAsSetComparisons(t *testing.T) {
	const shapes = `
ex:S a sh:NodeShape ; sh:targetClass ex:R ;
	sh:property [ sh:path ex:width ; sh:equals ex:height ] ;
	sh:property [ sh:path ex:width ; sh:disjoint ex:depth ] .
`
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadTurtle(t, b.store, `
ex:ok a ex:R ; ex:width 1 ; ex:height 1 ; ex:depth 2 .
ex:noHeight a ex:R ; ex:width 1 .
ex:extraHeight a ex:R ; ex:width 1 ; ex:height 1, 2 .
ex:clash a ex:R ; ex:width 3 ; ex:height 3 ; ex:depth 3 .`)
			report, err := b.store.ValidateSHACL(context.Background(), turtleTriples(t, "s", shapes))
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, r := range report.Results {
				got = append(got, shaclTermKey(r.FocusNode)+" "+shaclTermKey(r.Value)+" "+strings.TrimPrefix(r.Component, SHACLNamespace))
			}
			sort.Strings(got)
			want := []string{
				`clash "3"^^integer DisjointConstraintComponent`,
				`extraHeight "2"^^integer EqualsConstraintComponent`,
				`noHeight "1"^^integer EqualsConstraintComponent`,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %q\nwant %q", got, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Reference evaluator

// refRuleSet is a random rule set in a form both the engine (as shape
// triples) and the reference evaluator below (directly) can read. The
// reference is deliberately naive — every rule over every target, all against
// one snapshot, repeated until a pass adds nothing — and shares no code with
// the engine beyond the RDFTerm type. Conditions are monotone (sh:minCount 1,
// sh:hasValue, sh:class), so the least fixpoint is unique and order cannot
// make a correct engine disagree with it.
type refExpr struct {
	kind    string // this, const, path, inv, union, inter, filter
	term    RDFTerm
	pred    RDFTerm
	nodes   *refExpr
	members []*refExpr
	cond    *refCond
}

type refCond struct {
	kind  string // min1, hasValue, class
	pred  RDFTerm
	value RDFTerm
}

type refRule struct {
	targetKind string // class, node, subjectsOf, objectsOf
	target     RDFTerm
	order      int
	subject    *refExpr
	predicate  RDFTerm
	object     *refExpr
	conds      []*refCond
}

type refGraph struct {
	set     map[string]RDFTriple
	triples []RDFTriple
}

func newRefGraph(triples []RDFTriple) *refGraph {
	g := &refGraph{set: map[string]RDFTriple{}}
	for _, tr := range triples {
		g.add(tr)
	}
	return g
}

func refKey(tr RDFTriple) string {
	return tr.Subject.String() + " " + tr.Predicate.String() + " " + tr.Object.String()
}

func (g *refGraph) add(tr RDFTriple) bool {
	k := refKey(tr)
	if _, ok := g.set[k]; ok {
		return false
	}
	g.set[k] = tr
	g.triples = append(g.triples, tr)
	return true
}

func (g *refGraph) objects(s, p RDFTerm) []RDFTerm {
	var out []RDFTerm
	for _, tr := range g.triples {
		if termsEqual(tr.Subject, s) && termsEqual(tr.Predicate, p) {
			out = append(out, tr.Object)
		}
	}
	return out
}

func (g *refGraph) subjects(p, o RDFTerm) []RDFTerm {
	var out []RDFTerm
	for _, tr := range g.triples {
		if termsEqual(tr.Predicate, p) && termsEqual(tr.Object, o) {
			out = append(out, tr.Subject)
		}
	}
	return out
}

func (g *refGraph) instanceOf(node, cls RDFTerm) bool {
	// subclass closure upward from each type
	for _, t := range g.objects(node, NewIRI(RDFType)) {
		seen := map[string]bool{}
		queue := []RDFTerm{t}
		for len(queue) > 0 {
			c := queue[0]
			queue = queue[1:]
			if seen[c.String()] {
				continue
			}
			seen[c.String()] = true
			if termsEqual(c, cls) {
				return true
			}
			queue = append(queue, g.objects(c, NewIRI(rdfsSubClassOfIRI))...)
		}
	}
	return false
}

func (g *refGraph) holds(c *refCond, node RDFTerm) bool {
	switch c.kind {
	case "min1":
		return node.Kind != RDFTermLiteral && len(g.objects(node, c.pred)) > 0
	case "hasValue":
		return node.Kind != RDFTermLiteral && containsTerm(g.objects(node, c.pred), c.value)
	case "class":
		return g.instanceOf(node, c.value)
	}
	panic(c.kind)
}

func (g *refGraph) eval(e *refExpr, focus RDFTerm) []RDFTerm {
	set := func(in []RDFTerm) []RDFTerm { return uniqueSHACLTargets(in) }
	inputs := func() []RDFTerm {
		if e.nodes == nil {
			return []RDFTerm{focus}
		}
		return g.eval(e.nodes, focus)
	}
	switch e.kind {
	case "this":
		return []RDFTerm{focus}
	case "const":
		return []RDFTerm{e.term}
	case "path":
		var out []RDFTerm
		for _, n := range inputs() {
			out = append(out, g.objects(n, e.pred)...)
		}
		return set(out)
	case "inv":
		var out []RDFTerm
		for _, n := range inputs() {
			out = append(out, g.subjects(e.pred, n)...)
		}
		return set(out)
	case "union":
		var out []RDFTerm
		for _, m := range e.members {
			out = append(out, g.eval(m, focus)...)
		}
		return set(out)
	case "inter":
		acc := g.eval(e.members[0], focus)
		for _, m := range e.members[1:] {
			other := g.eval(m, focus)
			var next []RDFTerm
			for _, a := range acc {
				if containsTerm(other, a) {
					next = append(next, a)
				}
			}
			acc = next
		}
		return acc
	case "filter":
		var out []RDFTerm
		for _, n := range inputs() {
			if g.holds(e.cond, n) {
				out = append(out, n)
			}
		}
		return out
	}
	panic(e.kind)
}

func (g *refGraph) targets(r *refRule) []RDFTerm {
	var out []RDFTerm
	switch r.targetKind {
	case "node":
		out = []RDFTerm{r.target}
	case "class":
		for _, tr := range g.triples {
			if tr.Predicate.Value == RDFType && g.instanceOf(tr.Subject, r.target) {
				out = append(out, tr.Subject)
			}
		}
	case "subjectsOf":
		for _, tr := range g.triples {
			if termsEqual(tr.Predicate, r.target) {
				out = append(out, tr.Subject)
			}
		}
	case "objectsOf":
		for _, tr := range g.triples {
			if termsEqual(tr.Predicate, r.target) {
				out = append(out, tr.Object)
			}
		}
	}
	return uniqueSHACLTargets(out)
}

// referenceFixpoint returns the triples the rules add to base.
func referenceFixpoint(base []RDFTriple, rules []*refRule) []string {
	g := newRefGraph(base)
	initial := len(g.triples)
	for {
		var next []RDFTriple
		for _, r := range rules {
			for _, focus := range g.targets(r) {
				ok := true
				for _, c := range r.conds {
					if !g.holds(c, focus) {
						ok = false
					}
				}
				if !ok {
					continue
				}
				for _, s := range g.eval(r.subject, focus) {
					if s.Kind == RDFTermLiteral {
						continue
					}
					for _, o := range g.eval(r.object, focus) {
						next = append(next, RDFTriple{Subject: s, Predicate: r.predicate, Object: o})
					}
				}
			}
		}
		added := false
		for _, tr := range next {
			if g.add(tr) {
				added = true
			}
		}
		if !added {
			break
		}
	}
	var out []string
	for _, tr := range g.triples[initial:] {
		out = append(out, refKey(tr))
	}
	sort.Strings(out)
	return out
}

// randomRuleCase builds data and rules in namespace http://r/<seed>/ so
// every case can share one store.
func randomRuleCase(seed int) ([]RDFTriple, []*refRule, []RDFTriple) {
	rng := rand.New(rand.NewSource(int64(seed)))
	ns := fmt.Sprintf("http://r/%d/", seed)
	node := func() RDFTerm { return NewIRI(ns + "n" + strconv.Itoa(rng.Intn(5))) }
	pred := func() RDFTerm { return NewIRI(ns + "p" + strconv.Itoa(rng.Intn(3))) }
	class := func() RDFTerm { return NewIRI(ns + "C" + strconv.Itoa(rng.Intn(3))) }
	typ := NewIRI(RDFType)

	var base []RDFTriple
	for i := 0; i < 10+rng.Intn(15); i++ {
		base = append(base, RDFTriple{Subject: node(), Predicate: pred(), Object: node()})
	}
	for i := 0; i < 3+rng.Intn(4); i++ {
		base = append(base, RDFTriple{Subject: node(), Predicate: typ, Object: class()})
	}
	if rng.Intn(2) == 0 {
		base = append(base, RDFTriple{Subject: NewIRI(ns + "C0"), Predicate: NewIRI(rdfsSubClassOfIRI), Object: NewIRI(ns + "C1")})
	}
	if rng.Intn(2) == 0 {
		base = append(base, RDFTriple{Subject: node(), Predicate: pred(), Object: NewLiteral("v" + strconv.Itoa(rng.Intn(2)))})
	}

	cond := func() *refCond {
		switch rng.Intn(3) {
		case 0:
			return &refCond{kind: "min1", pred: pred()}
		case 1:
			return &refCond{kind: "hasValue", pred: pred(), value: node()}
		default:
			return &refCond{kind: "class", value: class()}
		}
	}
	var expr func(depth int) *refExpr
	expr = func(depth int) *refExpr {
		k := rng.Intn(8)
		if depth > 1 {
			k = rng.Intn(4)
		}
		switch k {
		case 0:
			return &refExpr{kind: "this"}
		case 1:
			if rng.Intn(3) == 0 {
				return &refExpr{kind: "const", term: class()}
			}
			return &refExpr{kind: "const", term: node()}
		case 2:
			e := &refExpr{kind: "path", pred: pred()}
			if depth < 2 && rng.Intn(3) == 0 {
				e.nodes = expr(depth + 1)
			}
			return e
		case 3:
			return &refExpr{kind: "inv", pred: pred()}
		case 4:
			return &refExpr{kind: "union", members: []*refExpr{expr(depth + 1), expr(depth + 1)}}
		case 5:
			return &refExpr{kind: "inter", members: []*refExpr{expr(depth + 1), expr(depth + 1)}}
		default:
			return &refExpr{kind: "filter", cond: cond(), nodes: expr(depth + 1)}
		}
	}

	var rules []*refRule
	for i := 0; i < 2+rng.Intn(4); i++ {
		r := &refRule{order: rng.Intn(3)}
		switch rng.Intn(4) {
		case 0:
			r.targetKind, r.target = "class", class()
		case 1:
			r.targetKind, r.target = "node", node()
		case 2:
			r.targetKind, r.target = "subjectsOf", pred()
		default:
			r.targetKind, r.target = "objectsOf", pred()
		}
		subj := []func() *refExpr{
			func() *refExpr { return &refExpr{kind: "this"} },
			func() *refExpr { return &refExpr{kind: "path", pred: pred()} },
			func() *refExpr { return &refExpr{kind: "inv", pred: pred()} },
			func() *refExpr { return &refExpr{kind: "const", term: node()} },
		}
		r.subject = subj[rng.Intn(len(subj))]()
		if rng.Intn(4) == 0 {
			r.predicate = typ
			r.object = &refExpr{kind: "const", term: class()}
		} else {
			r.predicate = pred()
			r.object = expr(0)
		}
		for c := 0; c < rng.Intn(2); c++ {
			r.conds = append(r.conds, cond())
		}
		rules = append(rules, r)
	}
	return base, rules, refRulesToShapes(ns, rules)
}

// refRulesToShapes writes the rules as SHACL: one shape per rule, so targets
// stay per rule, with sh:order on the shape and on the rule.
func refRulesToShapes(ns string, rules []*refRule) []RDFTriple {
	var out []RDFTriple
	n := 0
	blank := func() RDFTerm { n++; return NewBlankNode(fmt.Sprintf("rb%d", n)) }
	add := func(s, p, o RDFTerm) { out = append(out, RDFTriple{Subject: s, Predicate: p, Object: o}) }
	list := func(items []RDFTerm) RDFTerm {
		head := NewIRI(rdfNilIRI)
		for i := len(items) - 1; i >= 0; i-- {
			cell := blank()
			add(cell, NewIRI(rdfFirstIRI), items[i])
			add(cell, NewIRI(rdfRestIRI), head)
			head = cell
		}
		return head
	}
	condShape := func(c *refCond) RDFTerm {
		shape := blank()
		switch c.kind {
		case "class":
			add(shape, sh("class"), c.value)
		default:
			prop := blank()
			add(shape, sh("property"), prop)
			add(prop, sh("path"), c.pred)
			if c.kind == "min1" {
				add(prop, sh("minCount"), NewTypedLiteral("1", xsd("integer")))
			} else {
				add(prop, sh("hasValue"), c.value)
			}
		}
		return shape
	}
	var expr func(e *refExpr) RDFTerm
	expr = func(e *refExpr) RDFTerm {
		switch e.kind {
		case "this":
			return sh("this")
		case "const":
			return e.term
		case "path", "inv":
			node := blank()
			if e.kind == "inv" {
				inv := blank()
				add(inv, sh("inversePath"), e.pred)
				add(node, sh("path"), inv)
			} else {
				add(node, sh("path"), e.pred)
			}
			if e.nodes != nil {
				add(node, sh("nodes"), expr(e.nodes))
			}
			return node
		case "union", "inter":
			node := blank()
			var members []RDFTerm
			for _, m := range e.members {
				members = append(members, expr(m))
			}
			p := sh("union")
			if e.kind == "inter" {
				p = sh("intersection")
			}
			add(node, p, list(members))
			return node
		case "filter":
			node := blank()
			add(node, sh("filterShape"), condShape(e.cond))
			add(node, sh("nodes"), expr(e.nodes))
			return node
		}
		panic(e.kind)
	}
	for i, r := range rules {
		shape := NewIRI(fmt.Sprintf("%sshape%d", ns, i))
		add(shape, NewIRI(RDFType), sh("NodeShape"))
		switch r.targetKind {
		case "class":
			add(shape, sh("targetClass"), r.target)
		case "node":
			add(shape, sh("targetNode"), r.target)
		case "subjectsOf":
			add(shape, sh("targetSubjectsOf"), r.target)
		case "objectsOf":
			add(shape, sh("targetObjectsOf"), r.target)
		}
		add(shape, sh("order"), NewTypedLiteral(strconv.Itoa(r.order), xsd("integer")))
		rule := blank()
		add(shape, sh("rule"), rule)
		add(rule, NewIRI(RDFType), sh("TripleRule"))
		add(rule, sh("order"), NewTypedLiteral(strconv.Itoa(2-r.order), xsd("integer")))
		add(rule, sh("subject"), expr(r.subject))
		add(rule, sh("predicate"), r.predicate)
		add(rule, sh("object"), expr(r.object))
		for _, c := range r.conds {
			add(rule, sh("condition"), condShape(c))
		}
	}
	return out
}

func TestRandomRuleSetsAgreeWithANaiveReferenceFixpoint(t *testing.T) {
	const cases = 120
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			nonEmpty, chained := 0, 0
			for seed := 0; seed < cases; seed++ {
				base, rules, shapes := randomRuleCase(seed)
				if _, err := b.store.UpsertTriplesBatch(ctx, data(base...)); err != nil {
					t.Fatal(err)
				}
				want := referenceFixpoint(base, rules)
				// Persist every tenth case (each run replaces the last one's
				// output) and check it explains; dry-run the rest.
				persist := seed%10 == 0
				result, err := b.store.ApplySHACLRules(ctx, shapes, SHACLRuleOptions{DryRun: !persist})
				if err != nil {
					t.Fatalf("seed %d: %v", seed, err)
				}
				var got []string
				for _, tr := range result.Derived {
					got = append(got, refKey(tr))
				}
				sort.Strings(got)
				if want == nil {
					want = []string{}
				}
				if got == nil {
					got = []string{}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("seed %d disagrees with the reference\n got  %q\n want %q", seed, got, want)
				}
				if len(got) > 0 {
					nonEmpty++
				}
				if result.Iterations > 2 {
					chained++
				}
				if persist {
					requireExplainable(t, b.store, result)
				}
			}
			t.Logf("%d cases, %d inferred something, %d needed more than one productive pass", cases, nonEmpty, chained)
			if nonEmpty < cases/2 {
				t.Fatalf("only %d of %d random cases inferred anything; the generator is too weak to compare", nonEmpty, cases)
			}
		})
	}
}
