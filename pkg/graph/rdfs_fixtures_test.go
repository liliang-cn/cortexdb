package graph

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
)

// The graphs the inference engine is judged against.
//
// Each fixture is small enough to read and large enough to exercise a join the
// engine could get wrong: a chain long enough that closure needs several
// rounds, a diamond that reaches one class by two paths, properties whose
// schema is itself inferred, and named graphs whose merge and preference rules
// decide which graph an inference lands in. The expected output of every
// fixture was captured from the engine as it was before semi-naive evaluation,
// so the rewrite is held to the old engine's answer rather than to a new
// opinion of what the answer should be.

const exNS = "https://example.com/"

func infEx(local string) RDFTerm { return NewIRI(exNS + local) }

// explicitFixture assigns the ids the store would assign, so that support ids
// in the output name the same triples a real refresh would name.
func explicitFixture(triples ...RDFTriple) []RDFTriple {
	out := make([]RDFTriple, len(triples))
	for i, triple := range triples {
		triple.ID = tripleDigest(triple)
		out[i] = triple
	}
	return out
}

func infTri(s RDFTerm, p string, o RDFTerm) RDFTriple {
	return RDFTriple{Subject: s, Predicate: NewIRI(p), Object: o}
}

func infQuad(s RDFTerm, p string, o RDFTerm, graph string) RDFTriple {
	g := infEx(graph)
	return RDFTriple{Subject: s, Predicate: NewIRI(p), Object: o, Graph: &g}
}

type inferenceFixture struct {
	name     string
	explicit []RDFTriple
}

func rdfsGoldenFixtures() []inferenceFixture {
	return []inferenceFixture{
		{
			// A0 ⊑ A1 ⊑ … ⊑ A5 ⊑ D, and D reaches Top by way of both L and R.
			name: "subclass_chain_with_diamond",
			explicit: explicitFixture(
				infTri(infEx("A0"), rdfsSubClassOfIRI, infEx("A1")),
				infTri(infEx("A1"), rdfsSubClassOfIRI, infEx("A2")),
				infTri(infEx("A2"), rdfsSubClassOfIRI, infEx("A3")),
				infTri(infEx("A3"), rdfsSubClassOfIRI, infEx("A4")),
				infTri(infEx("A4"), rdfsSubClassOfIRI, infEx("A5")),
				infTri(infEx("A5"), rdfsSubClassOfIRI, infEx("D")),
				infTri(infEx("D"), rdfsSubClassOfIRI, infEx("L")),
				infTri(infEx("D"), rdfsSubClassOfIRI, infEx("R")),
				infTri(infEx("L"), rdfsSubClassOfIRI, infEx("Top")),
				infTri(infEx("R"), rdfsSubClassOfIRI, infEx("Top")),
				infTri(infEx("x"), rdfTypeIRI, infEx("A0")),
				infTri(infEx("y"), rdfTypeIRI, infEx("L")),
			),
		},
		{
			// p0 ⊑ p1 ⊑ p2 ⊑ p3, with domain and range declared part way up,
			// so both are reached only through inferred property use.
			name: "subproperty_chain_with_domain_and_range",
			explicit: explicitFixture(
				infTri(infEx("p0"), rdfsSubPropertyOfIRI, infEx("p1")),
				infTri(infEx("p1"), rdfsSubPropertyOfIRI, infEx("p2")),
				infTri(infEx("p2"), rdfsSubPropertyOfIRI, infEx("p3")),
				infTri(infEx("p1"), rdfsDomainIRI, infEx("Person")),
				infTri(infEx("p3"), rdfsRangeIRI, infEx("Org")),
				infTri(infEx("Person"), rdfsSubClassOfIRI, infEx("Agent")),
				infTri(infEx("a"), infEx("p0").Value, infEx("b")),
				infTri(infEx("c"), infEx("p2").Value, NewLiteral("not an org")),
			),
		},
		{
			// The ontology the original RDFS test used, plus explicit class and
			// property declarations that trigger the reflexive rules.
			name: "domain_range_and_declarations",
			explicit: explicitFixture(
				infTri(infEx("Manager"), rdfsSubClassOfIRI, infEx("Employee")),
				infTri(infEx("Employee"), rdfsSubClassOfIRI, infEx("Person")),
				infTri(infEx("worksFor"), rdfsSubPropertyOfIRI, infEx("affiliatedWith")),
				infTri(infEx("worksFor"), rdfsDomainIRI, infEx("Employee")),
				infTri(infEx("worksFor"), rdfsRangeIRI, infEx("Company")),
				infTri(infEx("Company"), rdfTypeIRI, NewIRI(rdfsClassIRI)),
				infTri(infEx("knows"), rdfTypeIRI, NewIRI(rdfPropertyIRI)),
				infTri(infEx("alice"), rdfTypeIRI, infEx("Manager")),
				infTri(infEx("alice"), infEx("worksFor").Value, infEx("acme")),
				infTri(infEx("alice"), infEx("knows").Value, NewBlankNode("b1")),
			),
		},
		{
			// Schema and data spread over two named graphs and the default
			// graph: transitive closure across graphs lands in the default
			// graph, and data-driven rules keep the data's graph.
			name: "mixed_named_graphs",
			explicit: explicitFixture(
				infQuad(infEx("Cat"), rdfsSubClassOfIRI, infEx("Mammal"), "g1"),
				infQuad(infEx("Mammal"), rdfsSubClassOfIRI, infEx("Animal"), "g2"),
				infTri(infEx("Animal"), rdfsSubClassOfIRI, infEx("Thing")),
				infQuad(infEx("Animal"), rdfsSubClassOfIRI, infEx("Thing"), "g2"),
				infQuad(infEx("tom"), rdfTypeIRI, infEx("Cat"), "g1"),
				infTri(infEx("felix"), rdfTypeIRI, infEx("Cat")),
				infQuad(infEx("owns"), rdfsRangeIRI, infEx("Mammal"), "g2"),
				infQuad(infEx("owns"), rdfsSubPropertyOfIRI, infEx("hasPet"), "g1"),
				infTri(infEx("bob"), infEx("owns").Value, infEx("tom")),
				infQuad(infEx("carol"), infEx("owns").Value, infEx("felix"), "g1"),
			),
		},
	}
}

// renderInferred lists the inferred records as sorted "triple<TAB>rule" lines,
// the form the golden files are written in.
func renderInferred(records map[string]rdfsInferenceRecord) []string {
	lines := make([]string, 0, len(records))
	for _, record := range records {
		if record.Explicit {
			continue
		}
		lines = append(lines, tripleWithoutInference(record.Triple).String()+"\t"+record.Rule)
	}
	sort.Strings(lines)
	return lines
}

// syntheticInferenceGraph builds the benchmark graph: a binary class tree of
// the given depth, typed instances at its leaves, and properties with domain,
// range, and short subPropertyOf chains, joined by random edges. The seed is
// fixed so every run measures the same graph.
func syntheticInferenceGraph(depth, instances, properties, edges int) []RDFTriple {
	rng := rand.New(rand.NewSource(42))
	var triples []RDFTriple
	classes := []string{"C"}
	level := []string{"C"}
	for d := 0; d < depth; d++ {
		var next []string
		for _, parent := range level {
			for k := 0; k < 2; k++ {
				child := fmt.Sprintf("%s.%d", parent, k)
				triples = append(triples, infTri(infEx(child), rdfsSubClassOfIRI, infEx(parent)))
				next = append(next, child)
			}
		}
		classes = append(classes, next...)
		level = next
	}
	leaves := level
	for i := 0; i < instances; i++ {
		triples = append(triples, infTri(infEx(fmt.Sprintf("i%d", i)), rdfTypeIRI, infEx(leaves[rng.Intn(len(leaves))])))
	}
	props := make([]string, properties)
	for i := range props {
		props[i] = exNS + fmt.Sprintf("prop%d", i)
		triples = append(triples,
			infTri(NewIRI(props[i]), rdfsDomainIRI, infEx(classes[rng.Intn(len(classes))])),
			infTri(NewIRI(props[i]), rdfsRangeIRI, infEx(classes[rng.Intn(len(classes))])),
		)
		if i%4 != 0 {
			triples = append(triples, infTri(NewIRI(props[i]), rdfsSubPropertyOfIRI, NewIRI(props[i-1])))
		}
	}
	for i := 0; i < edges; i++ {
		s := infEx(fmt.Sprintf("i%d", rng.Intn(instances)))
		o := infEx(fmt.Sprintf("i%d", rng.Intn(instances)))
		triples = append(triples, infTri(s, props[rng.Intn(len(props))], o))
	}
	return explicitFixture(dedupeTriples(triples)...)
}

func dedupeTriples(triples []RDFTriple) []RDFTriple {
	seen := make(map[string]bool, len(triples))
	out := triples[:0]
	for _, triple := range triples {
		key := triple.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, triple)
	}
	return out
}

func goldenPath(name string) string { return "testdata/rdfs_golden/" + name + ".txt" }

// parseGolden reads "triple<TAB>rule[|rule…]" lines. More than one rule means
// the old engine assigned different rules on different runs — its snapshot
// was taken from map iteration, so a triple derivable two ways was credited to
// whichever derivation happened to run first. Any of those is acceptable.
func parseGolden(content string) map[string][]string {
	out := make(map[string][]string)
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		if line == "" {
			continue
		}
		triple, rules, _ := strings.Cut(line, "\t")
		out[triple] = strings.Split(rules, "|")
	}
	return out
}
