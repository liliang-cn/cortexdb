package graph

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The W3C RDF 1.2 syntax suites for N-Triples, N-Quads, Turtle and TriG, which
// include the RDF 1.1 suites by reference — RDF 1.2 conformance is defined as
// passing both. RDF/XML and the semantics tests are out of scope: this store
// neither reads RDF/XML nor implements RDF 1.2 entailment.

// rdfSyntaxForTest maps a test type to the syntax under test.
func rdfSyntaxForTest(test w3cTest) (rdfSyntax, bool) {
	name := strings.ToLower(test.typeName())
	switch {
	case strings.HasPrefix(name, "testntriples"):
		return rdfSyntaxNTriples, true
	case strings.HasPrefix(name, "testnquads"):
		return rdfSyntaxNQuads, true
	case strings.HasPrefix(name, "testturtle"):
		return rdfSyntaxTurtle, true
	case strings.HasPrefix(name, "testtrig"):
		return rdfSyntaxTriG, true
	case strings.HasPrefix(name, "testxml"):
		return rdfSyntaxRDFXML, true
	}
	return 0, false
}

// runRDFSyntaxTest runs one test and returns "" on success or why it failed.
func runRDFSyntaxTest(root string, test w3cTest) string {
	syntax, ok := rdfSyntaxForTest(test)
	if !ok {
		return "unsupported test type " + test.typeName()
	}
	actionPath := manifestFile(test.action.Value)
	data, err := os.ReadFile(actionPath)
	if err != nil {
		return err.Error()
	}
	base := w3cDocumentBase(root, actionPath)
	parsed, parseErr := parseRDFDocument(string(data), syntax, base)
	name := strings.ToLower(test.typeName())
	switch {
	case strings.Contains(name, "negative"):
		if parseErr == nil {
			return fmt.Sprintf("parsed a document that must be rejected (%d triples):\n%s", len(parsed), dumpQuads(parsed))
		}
		return ""
	case strings.HasSuffix(name, "positivesyntax"):
		if parseErr != nil {
			return "rejected a valid document: " + parseErr.Error()
		}
		return ""
	case strings.HasSuffix(name, "positivec14n"):
		if parseErr != nil {
			return "rejected a valid document: " + parseErr.Error()
		}
		want, err := os.ReadFile(manifestFile(test.result.Value))
		if err != nil {
			return err.Error()
		}
		var got bytes.Buffer
		if err := writeCanonicalNQuads(&got, parsed); err != nil {
			return "canonical write: " + err.Error()
		}
		if got.String() != string(want) {
			return fmt.Sprintf("canonical form differs\n got: %q\nwant: %q", got.String(), string(want))
		}
		return ""
	case strings.HasSuffix(name, "eval"):
		if parseErr != nil {
			return "rejected a valid document: " + parseErr.Error()
		}
		resultPath := manifestFile(test.result.Value)
		wantData, err := os.ReadFile(resultPath)
		if err != nil {
			return err.Error()
		}
		want, err := parseRDFDocument(string(wantData), rdfSyntaxNQuads, "")
		if err != nil {
			return "cannot read expected result: " + err.Error()
		}
		if ok, diff := isomorphicDatasets(parsed, want); !ok {
			return diff
		}
		return ""
	}
	return "unknown test kind " + test.typeName()
}

// writeCanonicalNQuads writes canonical N-Triples/N-Quads exactly as RDF 1.2
// N-Triples §3 lays it out, blank node labels kept.
func writeCanonicalNQuads(buf *bytes.Buffer, triples []RDFTriple) error {
	for _, tr := range triples {
		parts := []RDFTerm{tr.Subject, tr.Predicate, tr.Object}
		if tr.Graph != nil {
			parts = append(parts, *tr.Graph)
		}
		for i, part := range parts {
			if i > 0 {
				buf.WriteByte(' ')
			}
			text, err := canonicalTerm(part)
			if err != nil {
				return err
			}
			buf.WriteString(text)
		}
		buf.WriteString(" .\n")
	}
	return nil
}

type suiteTally struct {
	pass, fail int
	failures   []string
}

func runRDFSuite(t *testing.T, root, manifest string) map[string]*suiteTally {
	t.Helper()
	tests, err := collectW3CTests(manifest, map[string]bool{})
	if err != nil {
		t.Fatalf("load %s: %v", manifest, err)
	}
	tallies := map[string]*suiteTally{}
	for _, test := range tests {
		if _, ok := rdfSyntaxForTest(test); !ok {
			continue
		}
		key := test.typeName()
		if tallies[key] == nil {
			tallies[key] = &suiteTally{}
		}
		if why := runRDFSyntaxTest(root, test); why != "" {
			tallies[key].fail++
			tallies[key].failures = append(tallies[key].failures, fmt.Sprintf("%s (%s): %s", test.name, test.node.Value, why))
			continue
		}
		tallies[key].pass++
	}
	return tallies
}

func reportTallies(t *testing.T, label string, tallies map[string]*suiteTally) {
	t.Helper()
	keys := make([]string, 0, len(tallies))
	total, failed := 0, 0
	for k := range tallies {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		tally := tallies[k]
		total += tally.pass + tally.fail
		failed += tally.fail
		t.Logf("%s %-34s pass %4d  fail %4d", label, k, tally.pass, tally.fail)
		for _, f := range tally.failures {
			t.Errorf("%s: %s", k, f)
		}
	}
	t.Logf("%s total %d, passed %d, failed %d", label, total, total-failed, failed)
}

func TestTheW3CRDF12SyntaxSuitesPassInFull(t *testing.T) {
	root := w3cSuiteRoot(t)
	for _, suite := range []string{"rdf-n-triples", "rdf-n-quads", "rdf-turtle", "rdf-trig", "rdf-xml"} {
		t.Run(suite, func(t *testing.T) {
			tallies := runRDFSuite(t, root, filepath.Join(root, "rdf", "rdf12", suite, "manifest.ttl"))
			reportTallies(t, suite, tallies)
		})
	}
}

// RDF/XML has no RDF 1.2 rewrite of its core suite: the RDF 1.1 tests are the
// grammar, and rdf12/rdf-xml adds only the 1.2 features on top.
func TestTheW3CRDF11RDFXMLSuitePassesInFull(t *testing.T) {
	root := w3cSuiteRoot(t)
	tallies := runRDFSuite(t, root, filepath.Join(root, "rdf", "rdf11", "rdf-xml", "manifest.ttl"))
	reportTallies(t, "rdf11/rdf-xml", tallies)
}

// The subset in testdata/w3c is a copy of a few dozen tests from the same
// suites, chosen to cover each RDF 1.2 feature and each negative rule, so CI
// exercises the parsers against the W3C's own documents on every run.
func TestARepresentativeW3CRDF12SubsetPasses(t *testing.T) {
	root := filepath.Join("testdata", "w3c")
	manifest := filepath.Join(root, "rdf", "manifest.ttl")
	if _, err := os.Stat(manifest); err != nil {
		t.Fatalf("subset manifest missing: %v", err)
	}
	tallies := runRDFSuite(t, root, manifest)
	reportTallies(t, "subset", tallies)
	total := 0
	for _, tally := range tallies {
		total += tally.pass + tally.fail
	}
	if total < 30 {
		t.Fatalf("subset ran %d tests; the manifest should list at least 30", total)
	}
}
