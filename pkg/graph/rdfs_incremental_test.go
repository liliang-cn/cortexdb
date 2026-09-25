package graph

import (
	"context"
	"sort"
	"strings"
	"testing"
)

// An incremental refresh is a shortcut, so the only acceptable answer is the
// one the long way gives. After each change the test records what the
// incremental refresh left in the store — triples, rules, and support ids —
// then runs a full refresh over the same explicit triples and demands the
// identical result.
//
// The graph mixes components that the changes touch with ones they do not,
// because the incremental path is only interesting when it has to leave
// something alone.
func TestAnIncrementalRefreshWritesWhatAFullRefreshWrites(t *testing.T) {
	base := []RDFTriple{
		infTri(infEx("Manager"), rdfsSubClassOfIRI, infEx("Employee")),
		infTri(infEx("alice"), rdfTypeIRI, infEx("Manager")),
		infTri(infEx("worksFor"), rdfsDomainIRI, infEx("Employee")),
		infTri(infEx("alice"), exNS+"worksFor", infEx("acme")),
		infTri(infEx("manages"), owlInverseOfIRI, infEx("reportsTo")),
		infTri(infEx("alice"), exNS+"manages", infEx("bob")),
		infTri(infEx("project"), owlEquivalentClassIRI, infEx("Project")),
		infTri(infEx("cortexdb"), rdfTypeIRI, infEx("project")),
		infTri(infEx("h1"), owlSameAsIRI, infEx("h2")),
		infTri(infEx("h1"), exNS+"ip", NewLiteral("10.0.0.1")),
		infTri(infEx("partOf"), rdfTypeIRI, NewIRI(owlTransitive)),
		infTri(infEx("room"), exNS+"partOf", infEx("floor")),
		infTri(infEx("floor"), exNS+"partOf", infEx("building")),
		// An island no change reaches.
		infQuad(infEx("Cat"), rdfsSubClassOfIRI, infEx("Animal"), "zoo"),
		infQuad(infEx("tom"), rdfTypeIRI, infEx("Cat"), "zoo"),
	}
	steps := []struct {
		name   string
		add    []RDFTriple
		remove []RDFTriple
	}{
		{name: "extend a subclass chain", add: []RDFTriple{infTri(infEx("Employee"), rdfsSubClassOfIRI, infEx("Person"))}},
		{name: "join two sameAs names to a third", add: []RDFTriple{infTri(infEx("h2"), owlSameAsIRI, infEx("h3")), infTri(infEx("api"), exNS+"runsOn", infEx("h3"))}},
		{name: "lengthen a transitive chain", add: []RDFTriple{infTri(infEx("building"), exNS+"partOf", infEx("campus"))}},
		{name: "retract a sameAs edge", remove: []RDFTriple{infTri(infEx("h1"), owlSameAsIRI, infEx("h2"))}},
		{name: "retract an inverse declaration", remove: []RDFTriple{infTri(infEx("manages"), owlInverseOfIRI, infEx("reportsTo"))}},
		{name: "retract a subclass link mid-chain", remove: []RDFTriple{infTri(infEx("Manager"), rdfsSubClassOfIRI, infEx("Employee"))}},
	}

	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			store := b.store
			seed := make([]*RDFTriple, len(base))
			for i := range base {
				seed[i] = ptrTriple(base[i])
			}
			if _, err := store.UpsertTriplesBatch(ctx, seed); err != nil {
				t.Fatalf("upsert base: %v", err)
			}
			if _, err := store.RefreshRDFSInferences(ctx); err != nil {
				t.Fatalf("initial refresh: %v", err)
			}

			for _, step := range steps {
				var changed []RDFTriple
				for _, triple := range step.add {
					triple := triple
					if err := store.UpsertTriple(ctx, &triple); err != nil {
						t.Fatalf("%s: upsert: %v", step.name, err)
					}
					changed = append(changed, triple)
				}
				for _, triple := range step.remove {
					if err := store.DeleteTriple(ctx, triple); err != nil {
						t.Fatalf("%s: delete: %v", step.name, err)
					}
					changed = append(changed, triple)
				}
				result, err := store.RefreshRDFSInferencesIncremental(ctx, changed)
				if err != nil {
					t.Fatalf("%s: incremental refresh: %v", step.name, err)
				}
				if !result.Incremental {
					t.Fatalf("%s: refresh did not take the incremental path: %+v", step.name, result)
				}
				incremental := storedInferences(t, store)

				if _, err := store.RefreshRDFSInferences(ctx); err != nil {
					t.Fatalf("%s: full refresh: %v", step.name, err)
				}
				full := storedInferences(t, store)
				if strings.Join(incremental, "\n") != strings.Join(full, "\n") {
					missing, extra := diffStringSets(lineSet(full), lineSet(incremental))
					t.Fatalf("%s: incremental differs from full\nmissing: %s\nextra: %s", step.name, strings.Join(missing, "\n  "), strings.Join(extra, "\n  "))
				}
			}
		})
	}
}

// storedInferences reads the inferred triples back out of the store as sorted
// "triple  rule  supports" lines.
func storedInferences(t *testing.T, store *GraphStore) []string {
	t.Helper()
	inferredOnly := true
	triples, err := store.FindTriples(context.Background(), TriplePattern{Inferred: &inferredOnly})
	if err != nil {
		t.Fatalf("find inferred: %v", err)
	}
	lines := make([]string, 0, len(triples))
	for _, triple := range triples {
		supports := append([]string(nil), triple.SupportIDs...)
		sort.Strings(supports)
		lines = append(lines, compactTriple(triple)+"  "+triple.Rule+"  "+strings.Join(supports, ","))
	}
	sort.Strings(lines)
	return lines
}

func lineSet(lines []string) map[string]bool {
	out := make(map[string]bool, len(lines))
	for _, line := range lines {
		out[line] = true
	}
	return out
}
