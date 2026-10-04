package graph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The W3C SHACL test suite (w3c/data-shapes, data-shapes-test-suite/tests),
// Core section. Like the RDF suites it is not vendored: the full run reads a
// checkout named by CORTEXDB_SHACL_TESTS and is skipped without one.
//
// A test passes when the report has the same sh:conforms and the same
// results, compared on focus node, path, value, severity, source shape and
// constraint component — everything but the messages, which the suite leaves
// to the implementation. Paths are compared by structure, since a report
// may describe the shape's path with nodes of its own.

const shaclTestsEnv = "CORTEXDB_SHACL_TESTS"

const (
	shtNS = "http://www.w3.org/ns/shacl-test#"
)

func shaclSuiteRoot(t *testing.T) string {
	t.Helper()
	root := strings.TrimSpace(os.Getenv(shaclTestsEnv))
	if root == "" {
		t.Skipf("%s is unset — the W3C SHACL suite is NOT run", shaclTestsEnv)
	}
	return root
}

// collectSHACLTests reads a SHACL test manifest, whose mf:include points
// straight at other manifests rather than at a list of them.
func collectSHACLTests(path string, seen map[string]bool) ([]w3cTest, error) {
	abs, _ := filepath.Abs(path)
	if seen[abs] {
		return nil, nil
	}
	seen[abs] = true
	m, err := loadManifest(abs)
	if err != nil {
		return nil, err
	}
	var out []w3cTest
	for _, typed := range m.subjectsOfType(mfNS + "Manifest") {
		for _, inc := range m.values(typed, mfNS+"include") {
			targets := []RDFTerm{inc}
			if _, isList := m.one(inc, rdfNS+"first"); isList {
				targets = m.list(inc)
			}
			for _, target := range targets {
				sub, err := collectSHACLTests(manifestFile(target.Value), seen)
				if err != nil {
					return nil, err
				}
				out = append(out, sub...)
			}
		}
		for _, entries := range m.values(typed, mfNS+"entries") {
			for _, entry := range m.list(entries) {
				test := w3cTest{manifest: m, node: entry}
				if label, ok := m.one(entry, "http://www.w3.org/2000/01/rdf-schema#label"); ok {
					test.name = label.Value
				}
				for _, typ := range m.values(entry, rdfNS+"type") {
					test.types = append(test.types, typ.Value)
				}
				test.action, _ = m.one(entry, mfNS+"action")
				test.result, test.hasRes = m.one(entry, mfNS+"result")
				out = append(out, test)
			}
		}
	}
	return out, nil
}

// readSHACLTestGraph parses a test file once: blank node labels differ from
// parse to parse, and a report names the shapes graph's blank nodes, so the
// shapes, the data and the expected report must all come from one parse.
func readSHACLTestGraph(iri string, cache map[string][]RDFTriple) ([]RDFTriple, error) {
	path := manifestFile(iri)
	abs, _ := filepath.Abs(path)
	if triples, ok := cache[abs]; ok {
		return triples, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	triples, err := parseRDFDocument(string(data), rdfSyntaxTurtle, fileURLOf(abs))
	if err != nil {
		return nil, err
	}
	cache[abs] = triples
	return triples, nil
}

func indexTriples(triples []RDFTriple) *manifestGraph {
	m := &manifestGraph{index: make(map[string]map[string][]RDFTerm)}
	for _, tr := range triples {
		key := termKeyForIndex(tr.Subject)
		if m.index[key] == nil {
			m.index[key] = make(map[string][]RDFTerm)
		}
		m.index[key][tr.Predicate.Value] = append(m.index[key][tr.Predicate.Value], tr.Object)
	}
	return m
}

// shaclPathKey spells a SHACL path by structure, reading its nodes from
// triples.
func shaclPathKey(term RDFTerm, triples []RDFTriple) string {
	if term.Kind == "" {
		return ""
	}
	if term.Kind == RDFTermIRI {
		return "<" + term.Value + ">"
	}
	props := map[string][]RDFTerm{}
	for _, tr := range triples {
		if termsEqual(tr.Subject, term) {
			props[tr.Predicate.Value] = append(props[tr.Predicate.Value], tr.Object)
		}
	}
	if _, ok := props[rdfFirstIRI]; ok {
		var parts []string
		for cur := term; cur.Value != rdfNilIRI; {
			var first, rest RDFTerm
			for _, tr := range triples {
				if termsEqual(tr.Subject, cur) && tr.Predicate.Value == rdfFirstIRI {
					first = tr.Object
				}
				if termsEqual(tr.Subject, cur) && tr.Predicate.Value == rdfRestIRI {
					rest = tr.Object
				}
			}
			if rest.Kind == "" {
				break
			}
			parts = append(parts, shaclPathKey(first, triples))
			cur = rest
		}
		return "(" + strings.Join(parts, " / ") + ")"
	}
	for _, op := range []string{"inversePath", "alternativePath", "zeroOrMorePath", "oneOrMorePath", "zeroOrOnePath"} {
		if v, ok := props[SHACLNamespace+op]; ok && len(v) == 1 {
			return op + "[" + shaclPathKey(v[0], triples) + "]"
		}
	}
	return "_:" + term.Value
}

func shaclResultKey(focus, path, value, severity, source, component, constraint string) string {
	return strings.Join([]string{focus, path, value, severity, source, component, constraint}, " | ")
}

func termKey(t RDFTerm) string {
	if t.Kind == "" {
		return ""
	}
	return t.String()
}

func runSHACLTest(t *testing.T, test w3cTest) string {
	m := test.manifest
	dataIRI, ok := m.one(test.action, shtNS+"dataGraph")
	if !ok {
		return "no sht:dataGraph"
	}
	shapesIRI, ok := m.one(test.action, shtNS+"shapesGraph")
	if !ok {
		return "no sht:shapesGraph"
	}
	cache := map[string][]RDFTriple{}
	manifestTriples, err := readSHACLTestGraph(fileURLOf(m.path), cache)
	if err != nil {
		return err.Error()
	}
	data, err := readSHACLTestGraph(dataIRI.Value, cache)
	if err != nil {
		return "data graph: " + err.Error()
	}
	shapes, err := readSHACLTestGraph(shapesIRI.Value, cache)
	if err != nil {
		return "shapes graph: " + err.Error()
	}
	_, store, cleanup := setupTestGraph(t)
	defer cleanup()
	store.SetPropertyGraphProjection(false)
	batch := make([]*RDFTriple, len(data))
	for i := range data {
		batch[i] = &data[i]
	}
	if len(batch) > 0 {
		res, err := store.UpsertTriplesBatch(context.Background(), batch)
		if err != nil {
			return "load data: " + err.Error()
		}
		if res.FailedCount > 0 {
			return "load data: " + res.Errors[0].Error()
		}
	}
	report, err := store.ValidateSHACL(context.Background(), shapes)

	// The expected report lives in the manifest file.
	if test.result.Kind == RDFTermIRI && test.result.Value == shtNS+"Failure" {
		if err == nil {
			return "validated shapes the suite says must fail"
		}
		return ""
	}
	if err != nil {
		return "validation failed: " + err.Error()
	}
	// The expected report, read from the same parse as the shapes.
	mg := indexTriples(manifestTriples)
	expected, _ := mg.one(test.node, mfNS+"result")
	wantConforms := false
	if c, ok := mg.one(expected, SHACLNamespace+"conforms"); ok {
		wantConforms = c.Value == "true"
	}
	var want, got []string
	for _, r := range mg.values(expected, SHACLNamespace+"result") {
		one := func(p string) RDFTerm { v, _ := mg.one(r, SHACLNamespace+p); return v }
		want = append(want, shaclResultKey(termKey(one("focusNode")), shaclPathKey(one("resultPath"), manifestTriples),
			termKey(one("value")), one("resultSeverity").Value, termKey(one("sourceShape")), one("sourceConstraintComponent").Value,
			termKey(one("sourceConstraint"))))
	}
	for _, r := range report.Results {
		got = append(got, shaclResultKey(termKey(r.FocusNode), shaclPathKey(r.Path, shapes),
			termKey(r.Value), r.Severity, termKey(r.Source), r.Component, termKey(r.SourceConstraint)))
	}
	sort.Strings(want)
	sort.Strings(got)
	if report.Conforms != wantConforms || strings.Join(want, "\n") != strings.Join(got, "\n") {
		return fmt.Sprintf("conforms %v (want %v)\n got:\n  %s\n want:\n  %s", report.Conforms, wantConforms,
			strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	return ""
}

func TestTheW3CSHACLCoreSuitePasses(t *testing.T) {
	runSHACLSuite(t, "core", "SHACL Core")
}

func TestTheW3CSHACLSPARQLSuitePasses(t *testing.T) {
	runSHACLSuite(t, "sparql", "SHACL-SPARQL")
}

func runSHACLSuite(t *testing.T, dir, label string) {
	root := shaclSuiteRoot(t)
	tests, err := collectSHACLTests(filepath.Join(root, dir, "manifest.ttl"), map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	pass, fail := 0, 0
	for _, test := range tests {
		if why := runSHACLTest(t, test); why != "" {
			fail++
			t.Errorf("%s (%s): %s", test.name, test.node.Value, why)
			continue
		}
		pass++
	}
	t.Logf("%s: total %d, passed %d, failed %d", label, pass+fail, pass, fail)
}
