package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The W3C test-suite harness: manifests, and graph isomorphism.
//
// The suites are the w3c/rdf-tests repository. They are not vendored: the
// full run reads a checkout named by CORTEXDB_W3C_TESTS and is skipped without
// one, and a small representative subset lives in testdata/w3c so that CI
// always runs some of them (the suites are under the W3C test-suite and
// 3-clause BSD licenses; see testdata/w3c/LICENSE.md).
//
// Manifests are Turtle, read with this package's own parser — which is part of
// the point: a parser that cannot read the manifests cannot pass the suites.

const w3cTestsEnv = "CORTEXDB_W3C_TESTS"

const (
	mfNS   = "http://www.w3.org/2001/sw/DataAccess/tests/test-manifest#"
	rdftNS = "http://www.w3.org/ns/rdftest#"
	qtNS   = "http://www.w3.org/2001/sw/DataAccess/tests/test-query#"
	utNS   = "http://www.w3.org/2009/sparql/tests/test-update#"
	rdfNS  = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
)

// w3cSuiteRoot is the checkout named by CORTEXDB_W3C_TESTS, or "" to skip.
func w3cSuiteRoot(t *testing.T) string {
	t.Helper()
	root := strings.TrimSpace(os.Getenv(w3cTestsEnv))
	if root == "" {
		t.Skipf("%s is unset — the full W3C suites are NOT run (the testdata subset is)", w3cTestsEnv)
	}
	if _, err := os.Stat(filepath.Join(root, "rdf", "rdf12")); err != nil {
		t.Fatalf("%s=%s does not look like a w3c/rdf-tests checkout: %v", w3cTestsEnv, root, err)
	}
	return root
}

// manifestGraph is a parsed manifest, indexed by subject.
type manifestGraph struct {
	path  string
	index map[string]map[string][]RDFTerm
}

func termKeyForIndex(t RDFTerm) string { return t.Kind + "|" + t.Value }

func loadManifest(path string) (*manifestGraph, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	triples, err := parseRDFDocument(string(data), rdfSyntaxTurtle, fileURLOf(abs))
	if err != nil {
		return nil, fmt.Errorf("manifest %s: %w", path, err)
	}
	m := &manifestGraph{path: abs, index: make(map[string]map[string][]RDFTerm)}
	for _, tr := range triples {
		key := termKeyForIndex(tr.Subject)
		if m.index[key] == nil {
			m.index[key] = make(map[string][]RDFTerm)
		}
		m.index[key][tr.Predicate.Value] = append(m.index[key][tr.Predicate.Value], tr.Object)
	}
	return m, nil
}

func fileURLOf(abs string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
}

func (m *manifestGraph) values(subject RDFTerm, predicate string) []RDFTerm {
	return m.index[termKeyForIndex(subject)][predicate]
}

func (m *manifestGraph) one(subject RDFTerm, predicate string) (RDFTerm, bool) {
	values := m.values(subject, predicate)
	if len(values) == 0 {
		return RDFTerm{}, false
	}
	return values[0], true
}

func (m *manifestGraph) list(head RDFTerm) []RDFTerm {
	var out []RDFTerm
	for head.Value != rdfNS+"nil" {
		first, ok := m.one(head, rdfNS+"first")
		if !ok {
			break
		}
		out = append(out, first)
		rest, ok := m.one(head, rdfNS+"rest")
		if !ok {
			break
		}
		head = rest
	}
	return out
}

// manifestFile turns a file: IRI the manifest named into a local path.
func manifestFile(iri string) string {
	u, err := url.Parse(iri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	return filepath.FromSlash(u.Path)
}

// w3cTest is one manifest entry.
type w3cTest struct {
	manifest *manifestGraph
	node     RDFTerm
	name     string
	types    []string
	action   RDFTerm
	result   RDFTerm
	hasRes   bool
}

func (t w3cTest) hasType(local string) bool {
	for _, typ := range t.types {
		if strings.HasSuffix(typ, "#"+local) {
			return true
		}
	}
	return false
}

func (t w3cTest) typeName() string {
	if len(t.types) == 0 {
		return ""
	}
	name := t.types[0]
	return name[strings.LastIndexAny(name, "#/")+1:]
}

// collectW3CTests walks a manifest and every manifest it includes.
func collectW3CTests(path string, seen map[string]bool) ([]w3cTest, error) {
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
		for _, includeList := range m.values(typed, mfNS+"include") {
			for _, included := range m.list(includeList) {
				sub, err := collectW3CTests(manifestFile(included.Value), seen)
				if err != nil {
					return nil, err
				}
				out = append(out, sub...)
			}
		}
		for _, entries := range m.values(typed, mfNS+"entries") {
			for _, entry := range m.list(entries) {
				test := w3cTest{manifest: m, node: entry}
				if name, ok := m.one(entry, mfNS+"name"); ok {
					test.name = name.Value
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

func (m *manifestGraph) subjectsOfType(typ string) []RDFTerm {
	var out []RDFTerm
	for key, preds := range m.index {
		for _, v := range preds[rdfNS+"type"] {
			if v.Kind == RDFTermIRI && v.Value == typ {
				kind, value, _ := strings.Cut(key, "|")
				out = append(out, RDFTerm{Kind: kind, Value: value})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out
}

// w3cDocumentBase is the base IRI the suites assume for a test document: its
// URL on w3c.github.io, which is what every mf:assumedTestBase spells out.
func w3cDocumentBase(root, localPath string) string {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	rel, err := filepath.Rel(root, localPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fileURLOf(localPath)
	}
	return "https://w3c.github.io/rdf-tests/" + filepath.ToSlash(rel)
}

// ---------------------------------------------------------------------------
// Isomorphism of RDF datasets, blank nodes inside triple terms included.
// ---------------------------------------------------------------------------

// isoKey spells a term with its blank nodes renamed by rename, recursing into
// triple terms; rename returning "" leaves a label as a wildcard marker.
func isoKey(t RDFTerm, rename func(string) string) string {
	switch t.Kind {
	case RDFTermBlankNode:
		return "_:" + rename(t.Value)
	case RDFTermTriple:
		triple, err := decodeTripleTermValue(t.Value)
		if err != nil {
			return "!bad:" + t.Value
		}
		return "<<( " + isoKey(triple.Subject, rename) + " " + isoKey(triple.Predicate, rename) + " " + isoKey(triple.Object, rename) + " )>>"
	case RDFTermLiteral:
		lit := t
		if lit.Datatype == rdf12XSDStringIRI {
			lit.Datatype = ""
		}
		out, err := canonicalLiteral(lit)
		if err != nil {
			return "!bad-literal:" + t.Value
		}
		return out
	default:
		return "<" + t.Value + ">"
	}
}

func isoQuadKey(q RDFTriple, rename func(string) string) string {
	key := isoKey(q.Subject, rename) + " " + isoKey(q.Predicate, rename) + " " + isoKey(q.Object, rename)
	if q.Graph != nil {
		key += " " + isoKey(*q.Graph, rename)
	}
	return key
}

func collectBlankNodes(t RDFTerm, into map[string]bool) {
	switch t.Kind {
	case RDFTermBlankNode:
		into[t.Value] = true
	case RDFTermTriple:
		if triple, err := decodeTripleTermValue(t.Value); err == nil {
			collectBlankNodes(triple.Subject, into)
			collectBlankNodes(triple.Object, into)
		}
	}
}

func quadBlankNodes(q RDFTriple) map[string]bool {
	out := map[string]bool{}
	collectBlankNodes(q.Subject, out)
	collectBlankNodes(q.Object, out)
	if q.Graph != nil {
		collectBlankNodes(*q.Graph, out)
	}
	return out
}

func dedupeQuads(quads []RDFTriple) []RDFTriple {
	seen := map[string]bool{}
	var out []RDFTriple
	for _, q := range quads {
		key := isoQuadKey(q, func(s string) string { return s })
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, q)
	}
	return out
}

// blankNodeColors refines a color per blank node from the quads it occurs in
// until the coloring is stable: the standard way to make isomorphism search
// cheap, since nodes of different colors can never be mapped to each other.
func blankNodeColors(quads []RDFTriple) map[string]string {
	colors := map[string]string{}
	occurrences := map[string][]RDFTriple{}
	for _, q := range quads {
		for b := range quadBlankNodes(q) {
			occurrences[b] = append(occurrences[b], q)
			colors[b] = "0"
		}
	}
	for round := 0; round < 8; round++ {
		next := map[string]string{}
		for b, qs := range occurrences {
			sigs := make([]string, 0, len(qs))
			for _, q := range qs {
				sigs = append(sigs, isoQuadKey(q, func(label string) string {
					if label == b {
						return "SELF"
					}
					return "c" + colors[label]
				}))
			}
			sort.Strings(sigs)
			sum := sha256.Sum256([]byte(colors[b] + "\x00" + strings.Join(sigs, "\x00")))
			next[b] = hex.EncodeToString(sum[:8])
		}
		colors = next
	}
	return colors
}

// isomorphicDatasets reports whether two sets of quads are equal up to a
// renaming of blank nodes. On failure it returns a short description.
func isomorphicDatasets(got, want []RDFTriple) (bool, string) {
	got, want = dedupeQuads(got), dedupeQuads(want)
	if len(got) != len(want) {
		return false, fmt.Sprintf("got %d quads, want %d\n got:\n%s\n want:\n%s", len(got), len(want), dumpQuads(got), dumpQuads(want))
	}
	gotColors, wantColors := blankNodeColors(got), blankNodeColors(want)
	if len(gotColors) != len(wantColors) {
		return false, fmt.Sprintf("got %d blank nodes, want %d\n got:\n%s\n want:\n%s", len(gotColors), len(wantColors), dumpQuads(got), dumpQuads(want))
	}
	wantSet := map[string]bool{}
	for _, q := range want {
		wantSet[isoQuadKey(q, func(s string) string { return s })] = true
	}
	byColor := map[string][]string{}
	for b, c := range wantColors {
		byColor[c] = append(byColor[c], b)
	}
	gotNodes := make([]string, 0, len(gotColors))
	for b := range gotColors {
		gotNodes = append(gotNodes, b)
	}
	sort.Slice(gotNodes, func(i, j int) bool {
		ci, cj := len(byColor[gotColors[gotNodes[i]]]), len(byColor[gotColors[gotNodes[j]]])
		if ci != cj {
			return ci < cj
		}
		return gotNodes[i] < gotNodes[j]
	})
	mapping := map[string]string{}
	used := map[string]bool{}
	steps := 0
	var search func(i int) bool
	check := func(complete bool) bool {
		for _, q := range got {
			allMapped := true
			for b := range quadBlankNodes(q) {
				if _, ok := mapping[b]; !ok {
					allMapped = false
					break
				}
			}
			if !allMapped {
				if complete {
					return false
				}
				continue
			}
			if !wantSet[isoQuadKey(q, func(s string) string { return mapping[s] })] {
				return false
			}
		}
		return true
	}
	search = func(i int) bool {
		steps++
		if steps > 200000 {
			return false
		}
		if i == len(gotNodes) {
			return check(true)
		}
		b := gotNodes[i]
		for _, candidate := range byColor[gotColors[b]] {
			if used[candidate] {
				continue
			}
			mapping[b], used[candidate] = candidate, true
			if check(false) && search(i+1) {
				return true
			}
			delete(mapping, b)
			used[candidate] = false
		}
		return false
	}
	if search(0) {
		return true, ""
	}
	return false, fmt.Sprintf("datasets differ\n got:\n%s\n want:\n%s", dumpQuads(got), dumpQuads(want))
}

func dumpQuads(quads []RDFTriple) string {
	lines := make([]string, 0, len(quads))
	for _, q := range quads {
		lines = append(lines, "   "+isoQuadKey(q, func(s string) string { return s }))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
