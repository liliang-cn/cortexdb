package graph

import (
	"context"
	"sort"
	"strings"
	"testing"
)

// Every test here runs on both backends through backends(t): SHACL reads the
// data only through FindTriples, so a difference between SQLite and
// PostgreSQL in what FindTriples returns (datatypes, language tags, blank
// nodes) would show up as a different report.

const shaclTestNS = "http://ex/"

func ex(local string) RDFTerm { return NewIRI(shaclTestNS + local) }
func sh(local string) RDFTerm { return NewIRI(SHACLNamespace + local) }
func xsd(local string) string { return XSDNamespace + local }

func tri(s, p, o RDFTerm) RDFTriple { return RDFTriple{Subject: s, Predicate: p, Object: o} }

func data(triples ...RDFTriple) []*RDFTriple {
	out := make([]*RDFTriple, len(triples))
	for i := range triples {
		out[i] = &triples[i]
	}
	return out
}

// rdfList builds an RDF collection with blank nodes named prefix0, prefix1, …
func rdfList(prefix string, items ...RDFTerm) (RDFTerm, []RDFTriple) {
	if len(items) == 0 {
		return NewIRI(rdfNilIRI), nil
	}
	var triples []RDFTriple
	for i, item := range items {
		node := NewBlankNode(prefix + string(rune('0'+i)))
		rest := NewIRI(rdfNilIRI)
		if i+1 < len(items) {
			rest = NewBlankNode(prefix + string(rune('0'+i+1)))
		}
		triples = append(triples,
			tri(node, NewIRI(rdfFirstIRI), item),
			tri(node, NewIRI(rdfRestIRI), rest))
	}
	return NewBlankNode(prefix + "0"), triples
}

// shaclTermKey renders a term compactly for assertions: IRIs lose the test
// namespace, literals keep quotes, tags and datatypes.
func shaclTermKey(term RDFTerm) string {
	switch term.Kind {
	case "":
		return ""
	case RDFTermIRI:
		v := strings.TrimPrefix(term.Value, shaclTestNS)
		v = strings.TrimPrefix(v, RDFNamespace)
		return v
	case RDFTermLiteral:
		out := `"` + term.Value + `"`
		if term.Language != "" {
			out += "@" + term.Language
		} else if term.Datatype != "" {
			out += "^^" + strings.TrimPrefix(term.Datatype, XSDNamespace)
		}
		return out
	default:
		return term.String()
	}
}

// resultKeys reduces a report to sorted "focus|path|value|Component" lines, so
// a test states exactly which node, value and constraint failed — no more,
// no less — independent of the order a backend returned rows in.
func resultKeys(report *SHACLReport) []string {
	keys := make([]string, 0, len(report.Results))
	for _, r := range report.Results {
		keys = append(keys, strings.Join([]string{
			shaclTermKey(r.FocusNode),
			shaclTermKey(r.Path),
			shaclTermKey(r.Value),
			strings.TrimSuffix(strings.TrimPrefix(r.Component, SHACLNamespace), "ConstraintComponent"),
		}, "|"))
	}
	sort.Strings(keys)
	return keys
}

// validateOnBothBackends loads the data, validates, and hands each backend's
// report to check.
func validateOnBothBackends(t *testing.T, dataTriples []*RDFTriple, shapes []RDFTriple, check func(t *testing.T, report *SHACLReport)) {
	t.Helper()
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			if len(dataTriples) > 0 {
				if _, err := b.store.UpsertTriplesBatch(ctx, dataTriples); err != nil {
					t.Fatalf("upsert data: %v", err)
				}
			}
			report, err := b.store.ValidateSHACL(ctx, shapes)
			if err != nil {
				t.Fatalf("ValidateSHACL: %v", err)
			}
			check(t, report)
		})
	}
}

func expectResults(t *testing.T, report *SHACLReport, want ...string) {
	t.Helper()
	got := resultKeys(report)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("results differ\n got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	if report.Conforms != (len(want) == 0) {
		t.Fatalf("Conforms = %v with %d results", report.Conforms, len(want))
	}
}

func expectValidationError(t *testing.T, shapes []RDFTriple, wantSubstrings ...string) {
	t.Helper()
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if err := b.store.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			report, err := b.store.ValidateSHACL(ctx, shapes)
			if err == nil {
				t.Fatalf("expected an error, got report %+v", report)
			}
			for _, want := range wantSubstrings {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

func nodeShape(id string, extra ...RDFTriple) []RDFTriple {
	return append([]RDFTriple{tri(ex(id), NewIRI(RDFType), sh("NodeShape"))}, extra...)
}

func TestSHACLClassAcceptsInstancesOfSubclassesAndRejectsEverythingElse(t *testing.T) {
	rdfType := NewIRI(RDFType)
	sub := NewIRI(rdfsSubClassOfIRI)
	d := data(
		tri(ex("Startup"), sub, ex("Company")),
		tri(ex("Unicorn"), sub, ex("Startup")),
		// A cyclic hierarchy that never reaches Company must terminate.
		tri(ex("Loop1"), sub, ex("Loop2")),
		tri(ex("Loop2"), sub, ex("Loop1")),

		tri(ex("alice"), rdfType, ex("Person")),
		tri(ex("alice"), ex("worksFor"), ex("acme")),
		tri(ex("acme"), rdfType, ex("Unicorn")), // two subClassOf hops away

		tri(ex("bob"), rdfType, ex("Person")),
		tri(ex("bob"), ex("worksFor"), ex("carol")),
		tri(ex("carol"), rdfType, ex("Person")),

		tri(ex("dave"), rdfType, ex("Person")),
		tri(ex("dave"), ex("worksFor"), NewLiteral("acme")),

		tri(ex("erin"), rdfType, ex("Person")),
		tri(ex("erin"), ex("worksFor"), ex("cult")),
		tri(ex("cult"), rdfType, ex("Loop1")),
	)
	shapes := append(nodeShape("PersonShape",
		tri(ex("PersonShape"), sh("targetClass"), ex("Person")),
		tri(ex("PersonShape"), sh("property"), ex("EmployerShape")),
	),
		tri(ex("EmployerShape"), sh("path"), ex("worksFor")),
		tri(ex("EmployerShape"), sh("class"), ex("Company")),
	)
	validateOnBothBackends(t, d, shapes, func(t *testing.T, report *SHACLReport) {
		expectResults(t, report,
			`bob|worksFor|carol|Class`,
			`dave|worksFor|"acme"|Class`,
			`erin|worksFor|cult|Class`,
		)
	})
}

func TestSHACLTargetClassSelectsInstancesOfSubclassesToo(t *testing.T) {
	d := data(
		tri(ex("Manager"), NewIRI(rdfsSubClassOfIRI), ex("Employee")),
		tri(ex("ann"), NewIRI(RDFType), ex("Employee")),
		tri(ex("max"), NewIRI(RDFType), ex("Manager")),
		tri(ex("ann"), ex("badge"), NewLiteral("A1")),
	)
	shapes := append(nodeShape("EmployeeShape",
		tri(ex("EmployeeShape"), sh("targetClass"), ex("Employee")),
		tri(ex("EmployeeShape"), sh("property"), ex("BadgeShape")),
	),
		tri(ex("BadgeShape"), sh("path"), ex("badge")),
		tri(ex("BadgeShape"), sh("minCount"), NewLiteral("1")),
	)
	validateOnBothBackends(t, d, shapes, func(t *testing.T, report *SHACLReport) {
		expectResults(t, report, `max|badge||MinCount`)
	})
}

func TestSHACLValueRangesAreInclusiveOrExclusiveAsDeclared(t *testing.T) {
	integer := func(v string) RDFTerm { return NewTypedLiteral(v, xsd("integer")) }
	date := func(v string) RDFTerm { return NewTypedLiteral(v, xsd("date")) }
	d := data(
		tri(ex("ok"), NewIRI(RDFType), ex("Reading")),
		tri(ex("ok"), ex("low"), integer("1")),
		tri(ex("ok"), ex("high"), integer("9")),
		tri(ex("ok"), ex("cap"), integer("10")),
		tri(ex("ok"), ex("since"), date("2026-01-01")),

		tri(ex("bad"), NewIRI(RDFType), ex("Reading")),
		tri(ex("bad"), ex("low"), integer("0")),           // not > 0
		tri(ex("bad"), ex("high"), integer("10")),         // not < 10
		tri(ex("bad"), ex("cap"), integer("11")),          // not <= 10
		tri(ex("bad"), ex("since"), date("2025-12-31")),   // before the bound
		tri(ex("bad"), ex("low"), NewLiteral("unknown")),  // incomparable: a violation, not a pass
		tri(ex("bad"), ex("since"), NewLiteral("recent")), // incomparable
	)
	shapes := append(nodeShape("ReadingShape",
		tri(ex("ReadingShape"), sh("targetClass"), ex("Reading")),
		tri(ex("ReadingShape"), sh("property"), ex("LowShape")),
		tri(ex("ReadingShape"), sh("property"), ex("HighShape")),
		tri(ex("ReadingShape"), sh("property"), ex("CapShape")),
		tri(ex("ReadingShape"), sh("property"), ex("SinceShape")),
	),
		tri(ex("LowShape"), sh("path"), ex("low")),
		tri(ex("LowShape"), sh("minExclusive"), integer("0")),
		tri(ex("HighShape"), sh("path"), ex("high")),
		tri(ex("HighShape"), sh("maxExclusive"), integer("10")),
		tri(ex("CapShape"), sh("path"), ex("cap")),
		tri(ex("CapShape"), sh("maxInclusive"), integer("10")),
		tri(ex("SinceShape"), sh("path"), ex("since")),
		tri(ex("SinceShape"), sh("minInclusive"), date("2026-01-01")),
	)
	validateOnBothBackends(t, d, shapes, func(t *testing.T, report *SHACLReport) {
		expectResults(t, report,
			`bad|low|"0"^^integer|MinExclusive`,
			`bad|low|"unknown"|MinExclusive`,
			`bad|high|"10"^^integer|MaxExclusive`,
			`bad|cap|"11"^^integer|MaxInclusive`,
			`bad|since|"2025-12-31"^^date|MinInclusive`,
			`bad|since|"recent"|MinInclusive`,
		)
	})
}

func TestSHACLHasValueRequiresTheValueAmongTheValueNodes(t *testing.T) {
	d := data(
		tri(ex("t1"), NewIRI(RDFType), ex("Ticket")),
		tri(ex("t1"), ex("tag"), ex("triaged")),
		tri(ex("t1"), ex("tag"), ex("bug")),
		tri(ex("t2"), NewIRI(RDFType), ex("Ticket")),
		tri(ex("t2"), ex("tag"), ex("bug")),
	)
	shapes := append(nodeShape("TicketShape",
		tri(ex("TicketShape"), sh("targetClass"), ex("Ticket")),
		tri(ex("TicketShape"), sh("property"), ex("TagShape")),
	),
		tri(ex("TagShape"), sh("path"), ex("tag")),
		tri(ex("TagShape"), sh("hasValue"), ex("triaged")),
	)
	validateOnBothBackends(t, d, shapes, func(t *testing.T, report *SHACLReport) {
		expectResults(t, report, `t2|tag||HasValue`)
	})
}

func TestSHACLLengthCountsCharactersOfLiteralsAndIRIsAndRejectsBlankNodes(t *testing.T) {
	d := data(
		tri(ex("a"), NewIRI(RDFType), ex("Thing")),
		tri(ex("a"), ex("name"), NewLiteral("李雷")), // 2 characters, 6 bytes: within 2..5

		tri(ex("b"), NewIRI(RDFType), ex("Thing")),
		tri(ex("b"), ex("name"), NewLiteral("李")), // 1 character

		tri(ex("c"), NewIRI(RDFType), ex("Thing")),
		tri(ex("c"), ex("name"), NewLiteral("abcdef")),
		tri(ex("c"), ex("name"), ex("q")), // an IRI counts by its string: "http://ex/q" is 11

		tri(ex("d"), NewIRI(RDFType), ex("Thing")),
		tri(ex("d"), ex("name"), NewBlankNode("anon")),
	)
	shapes := append(nodeShape("ThingShape",
		tri(ex("ThingShape"), sh("targetClass"), ex("Thing")),
		tri(ex("ThingShape"), sh("property"), ex("NameShape")),
	),
		tri(ex("NameShape"), sh("path"), ex("name")),
		tri(ex("NameShape"), sh("minLength"), NewTypedLiteral("2", xsd("integer"))),
		tri(ex("NameShape"), sh("maxLength"), NewTypedLiteral("5", xsd("integer"))),
	)
	validateOnBothBackends(t, d, shapes, func(t *testing.T, report *SHACLReport) {
		expectResults(t, report,
			`b|name|"李"|MinLength`,
			`c|name|"abcdef"|MaxLength`,
			`c|name|q|MaxLength`,
			`d|name|_:anon|MinLength`,
			`d|name|_:anon|MaxLength`,
		)
	})
}

func TestSHACLLanguageInMatchesTagsAndTheirSubtags(t *testing.T) {
	d := data(
		tri(ex("w"), NewIRI(RDFType), ex("Word")),
		tri(ex("w"), ex("label"), NewLangLiteral("colour", "en-GB")),
		tri(ex("w"), ex("label"), NewLangLiteral("颜色", "zh")),
		tri(ex("w"), ex("label"), NewLangLiteral("Farbe", "de")),
		tri(ex("w"), ex("label"), NewLiteral("color")),
	)
	langs, listTriples := rdfList("langs", NewLiteral("en"), NewLiteral("zh"))
	shapes := append(nodeShape("WordShape",
		tri(ex("WordShape"), sh("targetClass"), ex("Word")),
		tri(ex("WordShape"), sh("property"), ex("LabelShape")),
	),
		tri(ex("LabelShape"), sh("path"), ex("label")),
		tri(ex("LabelShape"), sh("languageIn"), langs),
	)
	shapes = append(shapes, listTriples...)
	validateOnBothBackends(t, d, shapes, func(t *testing.T, report *SHACLReport) {
		expectResults(t, report,
			`w|label|"Farbe"@de|LanguageIn`,
			`w|label|"color"|LanguageIn`,
		)
	})
}

func TestSHACLUniqueLangReportsEachRepeatedTagOnce(t *testing.T) {
	d := data(
		tri(ex("dup"), NewIRI(RDFType), ex("Word")),
		tri(ex("dup"), ex("label"), NewLangLiteral("one", "en")),
		tri(ex("dup"), ex("label"), NewLangLiteral("uno", "en")),
		tri(ex("dup"), ex("label"), NewLangLiteral("un", "fr")),
		tri(ex("dup"), ex("label"), NewLangLiteral("une", "fr")),
		tri(ex("dup"), ex("label"), NewLangLiteral("eins", "de")),
		tri(ex("dup"), ex("label"), NewLiteral("plain-1")), // untagged values never clash
		tri(ex("dup"), ex("label"), NewLiteral("plain-2")),

		tri(ex("fine"), NewIRI(RDFType), ex("Word")),
		tri(ex("fine"), ex("label"), NewLangLiteral("one", "en")),
		tri(ex("fine"), ex("label"), NewLangLiteral("un", "fr")),
	)
	shapes := append(nodeShape("WordShape",
		tri(ex("WordShape"), sh("targetClass"), ex("Word")),
		tri(ex("WordShape"), sh("property"), ex("LabelShape")),
	),
		tri(ex("LabelShape"), sh("path"), ex("label")),
		tri(ex("LabelShape"), sh("uniqueLang"), NewTypedLiteral("true", xsd("boolean"))),
	)
	validateOnBothBackends(t, d, shapes, func(t *testing.T, report *SHACLReport) {
		expectResults(t, report,
			`dup|label||UniqueLang`,
			`dup|label||UniqueLang`,
		)
		var tags []string
		for _, r := range report.Results {
			tags = append(tags, r.Message)
		}
		joined := strings.Join(tags, " / ")
		if !strings.Contains(joined, `"en"`) || !strings.Contains(joined, `"fr"`) {
			t.Fatalf("messages should name the repeated tags en and fr: %s", joined)
		}
	})
}

func TestSHACLNodeRequiresEveryValueToConformToTheReferencedShape(t *testing.T) {
	d := data(
		tri(ex("app"), NewIRI(RDFType), ex("Project")),
		tri(ex("app"), ex("dependsOn"), ex("libNamed")),
		tri(ex("app"), ex("dependsOn"), ex("libAnon")),
		tri(ex("libNamed"), ex("name"), NewLiteral("libNamed")),
	)
	shapes := append(nodeShape("ProjectShape",
		tri(ex("ProjectShape"), sh("targetClass"), ex("Project")),
		tri(ex("ProjectShape"), sh("property"), ex("DepShape")),
	),
		tri(ex("DepShape"), sh("path"), ex("dependsOn")),
		tri(ex("DepShape"), sh("node"), ex("NamedShape")),
		tri(ex("NamedShape"), sh("property"), ex("NamedNameShape")),
		tri(ex("NamedNameShape"), sh("path"), ex("name")),
		tri(ex("NamedNameShape"), sh("minCount"), NewLiteral("1")),
	)
	validateOnBothBackends(t, d, shapes, func(t *testing.T, report *SHACLReport) {
		// One sh:node result on the outer shape; the nested minCount failure
		// is the reason, not a second result.
		expectResults(t, report, `app|dependsOn|libAnon|Node`)
		if report.Results[0].Source != ex("DepShape") {
			t.Fatalf("source shape = %v, want DepShape", report.Results[0].Source)
		}
	})
}

func TestSHACLNotRejectsValuesThatConformToTheNegatedShape(t *testing.T) {
	d := data(
		tri(ex("svc"), NewIRI(RDFType), ex("Service")),
		tri(ex("svc"), ex("runsOn"), ex("prod1")),
		tri(ex("svc"), ex("runsOn"), ex("dev1")),
		tri(ex("prod1"), ex("deprecated"), NewTypedLiteral("true", xsd("boolean"))),
	)
	shapes := append(nodeShape("ServiceShape",
		tri(ex("ServiceShape"), sh("targetClass"), ex("Service")),
		tri(ex("ServiceShape"), sh("property"), ex("RunsOnShape")),
	),
		tri(ex("RunsOnShape"), sh("path"), ex("runsOn")),
		tri(ex("RunsOnShape"), sh("not"), ex("DeprecatedShape")),
		tri(ex("DeprecatedShape"), sh("property"), ex("DeprecatedFlag")),
		tri(ex("DeprecatedFlag"), sh("path"), ex("deprecated")),
		tri(ex("DeprecatedFlag"), sh("minCount"), NewLiteral("1")),
	)
	validateOnBothBackends(t, d, shapes, func(t *testing.T, report *SHACLReport) {
		expectResults(t, report, `svc|runsOn|prod1|Not`)
	})
}

// shapesForLogical builds three one-constraint shapes used by the sh:and,
// sh:or and sh:xone tests: IsIRI (sh:nodeKind sh:IRI), IsProject (sh:class
// Project) and IsNamed (a name is present).
func shapesForLogical() []RDFTriple {
	return []RDFTriple{
		tri(ex("IsIRI"), sh("nodeKind"), sh("IRI")),
		tri(ex("IsProject"), sh("class"), ex("Project")),
		tri(ex("IsNamed"), sh("property"), ex("IsNamedName")),
		tri(ex("IsNamedName"), sh("path"), ex("name")),
		tri(ex("IsNamedName"), sh("minCount"), NewLiteral("1")),
	}
}

func logicalData() []*RDFTriple {
	return data(
		tri(ex("root"), ex("ref"), ex("both")),    // project, named
		tri(ex("root"), ex("ref"), ex("proj")),    // project, unnamed
		tri(ex("root"), ex("ref"), ex("named")),   // named, not a project
		tri(ex("root"), ex("ref"), ex("neither")), // an IRI, nothing else
		tri(ex("root"), ex("ref"), NewLiteral("lit")),
		tri(ex("both"), NewIRI(RDFType), ex("Project")),
		tri(ex("both"), ex("name"), NewLiteral("b")),
		tri(ex("proj"), NewIRI(RDFType), ex("Project")),
		tri(ex("named"), ex("name"), NewLiteral("n")),
	)
}

func logicalShape(param string, members ...RDFTerm) []RDFTriple {
	head, listTriples := rdfList(param, members...)
	shapes := []RDFTriple{
		tri(ex("RootShape"), sh("targetNode"), ex("root")),
		tri(ex("RootShape"), sh("property"), ex("RefShape")),
		tri(ex("RefShape"), sh("path"), ex("ref")),
		tri(ex("RefShape"), sh(param), head),
	}
	shapes = append(shapes, listTriples...)
	return append(shapes, shapesForLogical()...)
}

func TestSHACLAndRequiresConformanceToEveryListedShape(t *testing.T) {
	validateOnBothBackends(t, logicalData(), logicalShape("and", ex("IsProject"), ex("IsNamed")), func(t *testing.T, report *SHACLReport) {
		expectResults(t, report,
			`root|ref|proj|And`,
			`root|ref|named|And`,
			`root|ref|neither|And`,
			`root|ref|"lit"|And`,
		)
	})
}

func TestSHACLOrRequiresConformanceToAtLeastOneListedShape(t *testing.T) {
	validateOnBothBackends(t, logicalData(), logicalShape("or", ex("IsProject"), ex("IsNamed")), func(t *testing.T, report *SHACLReport) {
		expectResults(t, report,
			`root|ref|neither|Or`,
			`root|ref|"lit"|Or`,
		)
	})
}

func TestSHACLXoneRequiresConformanceToExactlyOneListedShape(t *testing.T) {
	validateOnBothBackends(t, logicalData(), logicalShape("xone", ex("IsProject"), ex("IsNamed")), func(t *testing.T, report *SHACLReport) {
		expectResults(t, report,
			`root|ref|both|Xone`,
			`root|ref|neither|Xone`,
			`root|ref|"lit"|Xone`,
		)
	})
}

func TestSHACLMalformedListsAreErrorsNotSilentPasses(t *testing.T) {
	first, rest := NewIRI(rdfFirstIRI), NewIRI(rdfRestIRI)
	base := func(head RDFTerm) []RDFTriple {
		return append([]RDFTriple{
			tri(ex("S"), sh("targetNode"), ex("x")),
			tri(ex("S"), sh("or"), head),
		}, shapesForLogical()...)
	}
	cases := []struct {
		name   string
		shapes []RDFTriple
		want   string
	}{
		{"a list node with no rdf:rest", append(base(NewBlankNode("l0")),
			tri(NewBlankNode("l0"), first, ex("IsIRI"))), "exactly one rdf:first and one rdf:rest"},
		{"a list node with two rdf:first", append(base(NewBlankNode("l0")),
			tri(NewBlankNode("l0"), first, ex("IsIRI")),
			tri(NewBlankNode("l0"), first, ex("IsNamed")),
			tri(NewBlankNode("l0"), rest, NewIRI(rdfNilIRI))), "exactly one rdf:first"},
		{"a list whose rest points back at itself", append(base(NewBlankNode("l0")),
			tri(NewBlankNode("l0"), first, ex("IsIRI")),
			tri(NewBlankNode("l0"), rest, NewBlankNode("l0"))), "cycle"},
		{"a list that ends in a literal", append(base(NewBlankNode("l0")),
			tri(NewBlankNode("l0"), first, ex("IsIRI")),
			tri(NewBlankNode("l0"), rest, NewLiteral("nil"))), "literal"},
		{"a literal where the list should be", base(NewLiteral("IsIRI")), "literal"},
		{"a blank node that is not a list at all", base(NewBlankNode("nothing")), "sh:or"},
		{"a list member that is a literal", append(base(NewBlankNode("l0")),
			tri(NewBlankNode("l0"), first, NewLiteral("IsIRI")),
			tri(NewBlankNode("l0"), rest, NewIRI(rdfNilIRI))), "members must be shapes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectValidationError(t, tc.shapes, tc.want)
		})
	}
}

func TestSHACLRecursiveShapesAreRefusedInsteadOfLooping(t *testing.T) {
	// PersonShape → KnowsShape → (sh:node) PersonShape, over data where alice
	// and bob know each other: without the refusal this recurses forever.
	d := data(
		tri(ex("alice"), NewIRI(RDFType), ex("Person")),
		tri(ex("alice"), ex("knows"), ex("bob")),
		tri(ex("bob"), ex("knows"), ex("alice")),
	)
	shapes := append(nodeShape("PersonShape",
		tri(ex("PersonShape"), sh("targetClass"), ex("Person")),
		tri(ex("PersonShape"), sh("property"), ex("KnowsShape")),
	),
		tri(ex("KnowsShape"), sh("path"), ex("knows")),
		tri(ex("KnowsShape"), sh("node"), ex("PersonShape")),
	)
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if _, err := b.store.UpsertTriplesBatch(ctx, d); err != nil {
				t.Fatalf("upsert: %v", err)
			}
			_, err := b.store.ValidateSHACL(ctx, shapes)
			if err == nil || !strings.Contains(err.Error(), "recursive shapes are not supported") {
				t.Fatalf("expected the recursion to be refused, got %v", err)
			}
		})
	}

	t.Run("a shape that negates itself", func(t *testing.T) {
		expectValidationError(t, []RDFTriple{
			tri(ex("Liar"), sh("targetNode"), ex("x")),
			tri(ex("Liar"), sh("not"), ex("Liar")),
		}, "recursive shapes are not supported")
	})
}

func TestSHACLClosedShapeAllowsOnlyDeclaredAndIgnoredProperties(t *testing.T) {
	d := data(
		tri(ex("alice"), NewIRI(RDFType), ex("Person")),
		tri(ex("alice"), ex("name"), NewLiteral("Alice")),
		tri(ex("bob"), NewIRI(RDFType), ex("Person")),
		tri(ex("bob"), ex("name"), NewLiteral("Bob")),
		tri(ex("bob"), ex("age"), NewLiteral("40")),
	)
	ignored, listTriples := rdfList("ign", NewIRI(RDFType))
	shapes := append(nodeShape("PersonShape",
		tri(ex("PersonShape"), sh("targetClass"), ex("Person")),
		tri(ex("PersonShape"), sh("closed"), NewTypedLiteral("true", xsd("boolean"))),
		tri(ex("PersonShape"), sh("ignoredProperties"), ignored),
		tri(ex("PersonShape"), sh("property"), ex("NameShape")),
	),
		tri(ex("NameShape"), sh("path"), ex("name")),
	)
	shapes = append(shapes, listTriples...)
	validateOnBothBackends(t, d, shapes, func(t *testing.T, report *SHACLReport) {
		expectResults(t, report, `bob|age|"40"|Closed`)
	})
}

// The spec does not exempt rdf:type from sh:closed. A closed shape targeting a
// class therefore flags the very triple that made the node a target, unless
// rdf:type is listed in sh:ignoredProperties — surprising, and exactly what
// the spec says.
func TestSHACLClosedShapeFlagsRdfTypeUnlessItIsIgnored(t *testing.T) {
	d := data(
		tri(ex("alice"), NewIRI(RDFType), ex("Person")),
		tri(ex("alice"), ex("name"), NewLiteral("Alice")),
	)
	shapes := append(nodeShape("PersonShape",
		tri(ex("PersonShape"), sh("targetClass"), ex("Person")),
		tri(ex("PersonShape"), sh("closed"), NewTypedLiteral("true", xsd("boolean"))),
		tri(ex("PersonShape"), sh("property"), ex("NameShape")),
	),
		tri(ex("NameShape"), sh("path"), ex("name")),
	)
	validateOnBothBackends(t, d, shapes, func(t *testing.T, report *SHACLReport) {
		expectResults(t, report, `alice|type|Person|Closed`)
	})
}

func TestSHACLTargetSubjectsOfAndTargetObjectsOfSelectBothEndsOfAnEdge(t *testing.T) {
	d := data(
		tri(ex("web"), NewIRI(RDFType), ex("Project")),
		tri(ex("web"), ex("dependsOn"), ex("db")),
		tri(ex("db"), NewIRI(RDFType), ex("Project")),
		tri(ex("script"), ex("dependsOn"), ex("mystery")), // neither end is a project
	)
	shapes := []RDFTriple{
		tri(ex("DependerShape"), sh("targetSubjectsOf"), ex("dependsOn")),
		tri(ex("DependerShape"), sh("class"), ex("Project")),
		tri(ex("DependeeShape"), sh("targetObjectsOf"), ex("dependsOn")),
		tri(ex("DependeeShape"), sh("class"), ex("Project")),
	}
	validateOnBothBackends(t, d, shapes, func(t *testing.T, report *SHACLReport) {
		expectResults(t, report,
			`script||script|Class`,
			`mystery||mystery|Class`,
		)
	})
}

func TestSHACLNodeLevelConstraintsApplyToTheFocusNodeItself(t *testing.T) {
	d := data(
		tri(ex("x"), NewIRI(RDFType), ex("Thing")),
		tri(ex("z"), NewIRI(RDFType), ex("Other")),
	)
	allowed, listTriples := rdfList("allowed", ex("x"), ex("y"))
	shapes := append(nodeShape("FocusShape",
		tri(ex("FocusShape"), sh("targetNode"), ex("x")),
		tri(ex("FocusShape"), sh("targetNode"), ex("z")),
		tri(ex("FocusShape"), sh("targetNode"), NewLiteral("l")),
		tri(ex("FocusShape"), sh("nodeKind"), sh("BlankNodeOrIRI")),
		tri(ex("FocusShape"), sh("in"), allowed),
		tri(ex("FocusShape"), sh("class"), ex("Thing")),
	), listTriples...)
	validateOnBothBackends(t, d, shapes, func(t *testing.T, report *SHACLReport) {
		expectResults(t, report,
			`z||z|In`,
			`z||z|Class`,
			`"l"||"l"|NodeKind`,
			`"l"||"l"|In`,
			`"l"||"l"|Class`,
		)
	})
}

func TestSHACLSeverityIsReportedAndAnyResultMakesTheReportNonConforming(t *testing.T) {
	d := data(
		tri(ex("h"), NewIRI(RDFType), ex("Host")),
	)
	shapes := append(nodeShape("HostShape",
		tri(ex("HostShape"), sh("targetClass"), ex("Host")),
		tri(ex("HostShape"), sh("property"), ex("OwnerShape")),
		tri(ex("HostShape"), sh("property"), ex("NoteShape")),
		tri(ex("HostShape"), sh("property"), ex("NameShape")),
	),
		tri(ex("OwnerShape"), sh("path"), ex("owner")),
		tri(ex("OwnerShape"), sh("minCount"), NewLiteral("1")),
		tri(ex("OwnerShape"), sh("severity"), sh("Warning")),
		tri(ex("OwnerShape"), sh("message"), NewLiteral("a host should have an owner")),
		tri(ex("NoteShape"), sh("path"), ex("note")),
		tri(ex("NoteShape"), sh("minCount"), NewLiteral("1")),
		tri(ex("NoteShape"), sh("severity"), sh("Info")),
		tri(ex("NameShape"), sh("path"), ex("name")),
		tri(ex("NameShape"), sh("minCount"), NewLiteral("1")),
	)
	validateOnBothBackends(t, d, shapes, func(t *testing.T, report *SHACLReport) {
		expectResults(t, report,
			`h|owner||MinCount`,
			`h|note||MinCount`,
			`h|name||MinCount`,
		)
		severity := map[string]string{}
		for _, r := range report.Results {
			severity[shaclTermKey(r.Path)] = r.Severity
			if shaclTermKey(r.Path) == "owner" && r.Message != "a host should have an owner" {
				t.Errorf("sh:message not used: %q", r.Message)
			}
		}
		if severity["owner"] != SHACLSeverityWarning || severity["note"] != SHACLSeverityInfo || severity["name"] != SHACLSeverityViolation {
			t.Fatalf("severities = %v", severity)
		}
	})

	t.Run("warnings alone still do not conform", func(t *testing.T) {
		softOnly := append(nodeShape("SoftHostShape",
			tri(ex("SoftHostShape"), sh("targetClass"), ex("Host")),
			tri(ex("SoftHostShape"), sh("property"), ex("OwnerShape")),
			tri(ex("SoftHostShape"), sh("property"), ex("NoteShape")),
		), shapes[5:12]...)
		validateOnBothBackends(t, d, softOnly, func(t *testing.T, report *SHACLReport) {
			for _, r := range report.Results {
				if r.Severity == SHACLSeverityViolation {
					t.Fatalf("unexpected violation %+v", r)
				}
			}
			if len(report.Results) == 0 || report.Conforms {
				t.Fatalf("a report with only warnings/info must have conforms=false: %+v", report)
			}
		})
	})
}

func TestSHACLDatatypeTreatsPlainLiteralsAsStringsAndChecksTheLexicalForm(t *testing.T) {
	d := data(
		tri(ex("p"), NewIRI(RDFType), ex("Person")),
		tri(ex("p"), ex("name"), NewLiteral("plain")),
		tri(ex("p"), ex("name"), NewTypedLiteral("typed", xsd("string"))),
		tri(ex("p"), ex("name"), NewLangLiteral("tagged", "en")),
		tri(ex("p"), ex("name"), NewTypedLiteral("7", xsd("integer"))),
		tri(ex("p"), ex("age"), NewTypedLiteral("42", xsd("integer"))),
		tri(ex("p"), ex("age"), NewTypedLiteral("forty", xsd("integer"))),
	)
	shapes := append(nodeShape("PersonShape",
		tri(ex("PersonShape"), sh("targetClass"), ex("Person")),
		tri(ex("PersonShape"), sh("property"), ex("NameShape")),
		tri(ex("PersonShape"), sh("property"), ex("AgeShape")),
	),
		tri(ex("NameShape"), sh("path"), ex("name")),
		tri(ex("NameShape"), sh("datatype"), NewIRI(xsd("string"))),
		tri(ex("AgeShape"), sh("path"), ex("age")),
		tri(ex("AgeShape"), sh("datatype"), NewIRI(xsd("integer"))),
	)
	validateOnBothBackends(t, d, shapes, func(t *testing.T, report *SHACLReport) {
		expectResults(t, report,
			`p|name|"tagged"@en|Datatype`,
			`p|name|"7"^^integer|Datatype`,
			`p|age|"forty"^^integer|Datatype`,
		)
	})
}

func TestSHACLPatternHonoursFlagsAndRejectsBlankNodes(t *testing.T) {
	d := data(
		tri(ex("h1"), NewIRI(RDFType), ex("Host")),
		tri(ex("h1"), ex("hostname"), NewLiteral("WEB-01")),
		tri(ex("h2"), NewIRI(RDFType), ex("Host")),
		tri(ex("h2"), ex("hostname"), NewLiteral("web_02")),
		tri(ex("h3"), NewIRI(RDFType), ex("Host")),
		tri(ex("h3"), ex("hostname"), NewBlankNode("h3name")),
	)
	shapes := append(nodeShape("HostShape",
		tri(ex("HostShape"), sh("targetClass"), ex("Host")),
		tri(ex("HostShape"), sh("property"), ex("HostnameShape")),
	),
		tri(ex("HostnameShape"), sh("path"), ex("hostname")),
		tri(ex("HostnameShape"), sh("pattern"), NewLiteral(`^[a-z]+-[0-9]+$`)),
		tri(ex("HostnameShape"), sh("flags"), NewLiteral("i")),
	)
	validateOnBothBackends(t, d, shapes, func(t *testing.T, report *SHACLReport) {
		expectResults(t, report,
			`h2|hostname|"web_02"|Pattern`,
			`h3|hostname|_:h3name|Pattern`,
		)
	})
}

func TestSHACLInversePathReachesTheSubjectsOfIncomingEdges(t *testing.T) {
	d := data(
		tri(ex("used"), NewIRI(RDFType), ex("Library")),
		tri(ex("orphan"), NewIRI(RDFType), ex("Library")),
		tri(ex("app"), ex("dependsOn"), ex("used")),
	)
	shapes := append(nodeShape("LibraryShape",
		tri(ex("LibraryShape"), sh("targetClass"), ex("Library")),
		tri(ex("LibraryShape"), sh("property"), ex("UsedByShape")),
	),
		tri(ex("UsedByShape"), sh("path"), NewBlankNode("inv")),
		tri(NewBlankNode("inv"), sh("inversePath"), ex("dependsOn")),
		tri(ex("UsedByShape"), sh("minCount"), NewLiteral("1")),
	)
	validateOnBothBackends(t, d, shapes, func(t *testing.T, report *SHACLReport) {
		expectResults(t, report, `orphan|_:inv||MinCount`)
	})
}

func TestSHACLRefusesIllFormedPaths(t *testing.T) {
	one, oneTriples := rdfList("one", ex("a"))
	expectValidationError(t, append([]RDFTriple{
		tri(ex("S"), sh("targetNode"), ex("x")),
		tri(ex("S"), sh("property"), ex("P")),
		tri(ex("P"), sh("path"), one),
	}, oneTriples...), "at least two members")
	bad := NewBlankNode("bad")
	expectValidationError(t, []RDFTriple{
		tri(ex("S"), sh("targetNode"), ex("x")),
		tri(ex("S"), sh("property"), ex("P")),
		tri(ex("P"), sh("path"), bad),
		tri(bad, ex("notAPathOperator"), ex("a")),
	}, "is not a path operator")
}

// An extraction quality gate, in the vocabulary LLM extraction writes into the
// property graph (and FindTriples projects as RDF): typed nodes, relation
// edges, name literals. It is the reason SHACL grew sh:class, sh:closed and
// the rest — "depends_on must point at a project", "a host has exactly one
// name", "nothing outside the declared properties".
func TestSHACLActsAsAQualityGateForAnExtractedGraph(t *testing.T) {
	const (
		typeNS = "urn:cortexdb:type:"
		relNS  = "urn:cortexdb:rel:"
		propNS = "urn:cortexdb:prop:"
		entity = "urn:cortexdb:entity:"
	)
	node := func(id string) RDFTerm { return NewIRI(entity + id) }
	typ := func(id string) RDFTerm { return NewIRI(typeNS + id) }
	rel := func(id string) RDFTerm { return NewIRI(relNS + id) }
	prop := func(id string) RDFTerm { return NewIRI(propNS + id) }
	rdfType := NewIRI(RDFType)

	d := data(
		// A clean slice of the graph.
		tri(node("cortexdb"), rdfType, typ("project")),
		tri(node("cortexdb"), prop("name"), NewLiteral("CortexDB")),
		tri(node("cortexdb"), rel("depends_on"), node("sqlite")),
		tri(node("cortexdb"), rel("runs_on"), node("vm1")),
		tri(node("sqlite"), rdfType, typ("project")),
		tri(node("sqlite"), prop("name"), NewLiteral("SQLite")),
		tri(node("vm1"), rdfType, typ("host")),
		tri(node("vm1"), prop("name"), NewLiteral("vm1")),

		// What a confused extractor produces.
		tri(node("athanor"), rdfType, typ("project")),
		tri(node("athanor"), prop("name"), NewLiteral("Athanor")),
		tri(node("athanor"), prop("name"), NewLiteral("athanor")), // two names
		tri(node("athanor"), rel("depends_on"), node("vm1")),      // a host, not a project
		tri(node("athanor"), rel("runs_on"), node("cortexdb")),    // a project, not a host
		tri(node("athanor"), prop("stars"), NewLiteral("5")),      // undeclared property
		tri(node("vm2"), rdfType, typ("host")),                    // no name at all
	)

	ignored, ignoredTriples := rdfList("ign", rdfType)
	closed := NewTypedLiteral("true", xsd("boolean"))
	shape := func(id string) RDFTerm { return NewIRI("urn:gate:" + id) }
	shapes := []RDFTriple{
		tri(shape("Project"), rdfType, sh("NodeShape")),
		tri(shape("Project"), sh("targetClass"), typ("project")),
		tri(shape("Project"), sh("closed"), closed),
		tri(shape("Project"), sh("ignoredProperties"), ignored),
		tri(shape("Project"), sh("property"), shape("Name")),
		tri(shape("Project"), sh("property"), shape("DependsOn")),
		tri(shape("Project"), sh("property"), shape("RunsOn")),

		tri(shape("Host"), rdfType, sh("NodeShape")),
		tri(shape("Host"), sh("targetClass"), typ("host")),
		tri(shape("Host"), sh("closed"), closed),
		tri(shape("Host"), sh("ignoredProperties"), ignored),
		tri(shape("Host"), sh("property"), shape("Name")),

		tri(shape("Name"), sh("path"), prop("name")),
		tri(shape("Name"), sh("minCount"), NewLiteral("1")),
		tri(shape("Name"), sh("maxCount"), NewLiteral("1")),
		tri(shape("Name"), sh("datatype"), NewIRI(xsd("string"))),
		tri(shape("Name"), sh("minLength"), NewLiteral("1")),

		tri(shape("DependsOn"), sh("path"), rel("depends_on")),
		tri(shape("DependsOn"), sh("class"), typ("project")),

		tri(shape("RunsOn"), sh("path"), rel("runs_on")),
		tri(shape("RunsOn"), sh("class"), typ("host")),
		tri(shape("RunsOn"), sh("maxCount"), NewLiteral("1")),
	}
	shapes = append(shapes, ignoredTriples...)

	validateOnBothBackends(t, d, shapes, func(t *testing.T, report *SHACLReport) {
		expectResults(t, report,
			`urn:cortexdb:entity:athanor|urn:cortexdb:prop:name||MaxCount`,
			`urn:cortexdb:entity:athanor|urn:cortexdb:rel:depends_on|urn:cortexdb:entity:vm1|Class`,
			`urn:cortexdb:entity:athanor|urn:cortexdb:rel:runs_on|urn:cortexdb:entity:cortexdb|Class`,
			`urn:cortexdb:entity:athanor|urn:cortexdb:prop:stars|"5"|Closed`,
			`urn:cortexdb:entity:vm2|urn:cortexdb:prop:name||MinCount`,
		)
	})
}
