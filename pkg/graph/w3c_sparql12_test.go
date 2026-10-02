package graph

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The W3C SPARQL 1.2 suite. In scope are the parts SPARQL 1.2 adds for RDF
// 1.2 and that this engine implements: triple-term syntax (positive and
// negative), triple-term evaluation, the triple-term expression tests and the
// base-direction functions. The rest of sparql12/ — codepoint escapes,
// grouping, VERSION, the SPARQL 1.1 syntax regressions — exercises SPARQL 1.1
// features of a full engine; it is run and counted, and reported separately,
// but a failure there does not fail this test.

var sparql12InScope = map[string]bool{
	"eval-triple-terms":            true,
	"syntax-triple-terms-positive": true,
	"syntax-triple-terms-negative": true,
	"expression":                   true,
	"lang-basedir":                 true,
}

func sparqlTestDir(test w3cTest) string {
	return filepath.Base(filepath.Dir(test.manifest.path))
}

// runSPARQLTest runs one test against one backend's store factory and
// returns "" on success.
func runSPARQLTest(t *testing.T, root string, test w3cTest, newStore func() *GraphStore) string {
	m := test.manifest
	switch {
	case test.hasType("PositiveSyntaxTest") || test.hasType("PositiveUpdateSyntaxTest") ||
		test.hasType("PositiveSyntaxTest11") || test.hasType("PositiveUpdateSyntaxTest11"):
		query, err := os.ReadFile(manifestFile(test.action.Value))
		if err != nil {
			return err.Error()
		}
		if _, err := parseSPARQLForTest(string(query)); err != nil {
			return "rejected valid syntax: " + err.Error()
		}
		return ""
	case test.hasType("NegativeSyntaxTest") || test.hasType("NegativeUpdateSyntaxTest") ||
		test.hasType("NegativeSyntaxTest11") || test.hasType("NegativeUpdateSyntaxTest11"):
		query, err := os.ReadFile(manifestFile(test.action.Value))
		if err != nil {
			return err.Error()
		}
		if _, err := parseSPARQLForTest(string(query)); err == nil {
			return "accepted invalid syntax"
		}
		return ""
	case test.hasType("QueryEvaluationTest"):
		store := newStore()
		queryFile, ok := m.one(test.action, qtNS+"query")
		if !ok {
			return "no qt:query"
		}
		for _, data := range m.values(test.action, qtNS+"data") {
			if why := loadTestData(store, root, manifestFile(data.Value), nil); why != "" {
				return why
			}
		}
		for _, data := range m.values(test.action, qtNS+"graphData") {
			name := NewIRI(w3cDocumentBase(root, manifestFile(data.Value)))
			if why := loadTestData(store, root, manifestFile(data.Value), &name); why != "" {
				return why
			}
		}
		query, err := os.ReadFile(manifestFile(queryFile.Value))
		if err != nil {
			return err.Error()
		}
		result, err := store.ExecuteSPARQL(context.Background(), withBase(string(query), w3cDocumentBase(root, manifestFile(queryFile.Value))))
		if err != nil {
			return "query failed: " + err.Error()
		}
		return compareSPARQLResult(root, result, manifestFile(test.result.Value))
	case test.hasType("UpdateEvaluationTest"):
		store := newStore()
		request, ok := m.one(test.action, utNS+"request")
		if !ok {
			return "no ut:request"
		}
		if data, ok := m.one(test.action, utNS+"data"); ok {
			if why := loadTestData(store, root, manifestFile(data.Value), nil); why != "" {
				return why
			}
		}
		update, err := os.ReadFile(manifestFile(request.Value))
		if err != nil {
			return err.Error()
		}
		if _, err := store.ExecuteSPARQL(context.Background(), string(update)); err != nil {
			return "update failed: " + err.Error()
		}
		var want []RDFTriple
		if data, ok := m.one(test.result, utNS+"data"); ok {
			parsed, why := parseTestData(root, manifestFile(data.Value))
			if why != "" {
				return why
			}
			want = parsed
		}
		got, err := store.findStoredTriples(context.Background(), TriplePattern{})
		if err != nil {
			return err.Error()
		}
		if ok, diff := isomorphicDatasets(got, want); !ok {
			return diff
		}
		return ""
	}
	return "unsupported test type " + test.typeName()
}

// withBase gives a query without its own BASE the base the suites assume, so
// a relative IRI in it resolves as the expected results were written.
func withBase(query, base string) string {
	if strings.Contains(strings.ToUpper(query), "BASE") {
		return query
	}
	return "BASE <" + base + ">\n" + query
}

func parseSPARQLForTest(query string) (*sparqlQuery, error) {
	return newSPARQLParser(query, map[string]string{}).parse()
}

func parseTestData(root, path string) ([]RDFTriple, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err.Error()
	}
	syntax := rdfSyntaxTurtle
	switch filepath.Ext(path) {
	case ".trig":
		syntax = rdfSyntaxTriG
	case ".nq":
		syntax = rdfSyntaxNQuads
	case ".nt":
		syntax = rdfSyntaxNTriples
	}
	triples, err := parseRDFDocument(string(data), syntax, w3cDocumentBase(root, path))
	if err != nil {
		return nil, "cannot read " + filepath.Base(path) + ": " + err.Error()
	}
	return triples, ""
}

func loadTestData(store *GraphStore, root, path string, graph *RDFTerm) string {
	triples, why := parseTestData(root, path)
	if why != "" {
		return why
	}
	batch := make([]*RDFTriple, len(triples))
	for i := range triples {
		if graph != nil {
			g := *graph
			triples[i].Graph = &g
		}
		batch[i] = &triples[i]
	}
	result, err := store.UpsertTriplesBatch(context.Background(), batch)
	if err != nil {
		return "load data: " + err.Error()
	}
	if result.FailedCount > 0 {
		return "load data: " + result.Errors[0].Error()
	}
	return ""
}

// compareSPARQLResult compares an answer with the expected .srj, .srx, or
// graph file, solutions as a multiset and blank nodes up to renaming.
func compareSPARQLResult(root string, result *SPARQLResult, expectedPath string) string {
	ext := filepath.Ext(expectedPath)
	switch ext {
	case ".srj", ".srx":
		var (
			vars     []string
			rows     []map[string]RDFTerm
			boolean  *bool
			parseErr error
		)
		if ext == ".srj" {
			vars, rows, boolean, parseErr = readSRJ(expectedPath)
		} else {
			vars, rows, boolean, parseErr = readSRX(expectedPath)
		}
		if parseErr != nil {
			return "cannot read expected results: " + parseErr.Error()
		}
		if boolean != nil {
			if result.Boolean != *boolean {
				return fmt.Sprintf("ASK returned %v, want %v", result.Boolean, *boolean)
			}
			return ""
		}
		_ = vars
		got, want := solutionsAsQuads(result.Bindings), solutionsAsQuads(rows)
		if ok, diff := isomorphicDatasets(got, want); !ok {
			return "solutions differ: " + diff
		}
		return ""
	default:
		want, why := parseTestData(root, expectedPath)
		if why != "" {
			return why
		}
		if ok, diff := isomorphicDatasets(result.Triples, want); !ok {
			return diff
		}
		return ""
	}
}

// solutionsAsQuads encodes a solution multiset as a graph — one blank node per
// solution, one triple per binding, one marker per row so an empty solution
// still counts — so that comparing two multisets up to blank node renaming is
// graph isomorphism, which the harness already has.
func solutionsAsQuads(rows []map[string]RDFTerm) []RDFTriple {
	var out []RDFTriple
	for i, row := range rows {
		node := NewBlankNode(fmt.Sprintf("row%d", i))
		out = append(out, RDFTriple{Subject: node, Predicate: NewIRI("urn:solution"), Object: NewLiteral("row")})
		for name, value := range row {
			if isHiddenVariable(name) {
				continue
			}
			out = append(out, RDFTriple{Subject: node, Predicate: NewIRI("urn:var:" + name), Object: value})
		}
	}
	return out
}

type srjTerm struct {
	Type     string          `json:"type"`
	Value    json.RawMessage `json:"value"`
	Lang     string          `json:"xml:lang"`
	Dir      string          `json:"its:dir"`
	Datatype string          `json:"datatype"`
}

func (t srjTerm) toRDF() (RDFTerm, error) {
	switch t.Type {
	case "triple":
		var parts struct {
			Subject   srjTerm `json:"subject"`
			Predicate srjTerm `json:"predicate"`
			Object    srjTerm `json:"object"`
		}
		if err := json.Unmarshal(t.Value, &parts); err != nil {
			return RDFTerm{}, err
		}
		s, err := parts.Subject.toRDF()
		if err != nil {
			return RDFTerm{}, err
		}
		p, err := parts.Predicate.toRDF()
		if err != nil {
			return RDFTerm{}, err
		}
		o, err := parts.Object.toRDF()
		if err != nil {
			return RDFTerm{}, err
		}
		return NewTripleTerm(s, p, o)
	}
	var value string
	if err := json.Unmarshal(t.Value, &value); err != nil {
		return RDFTerm{}, err
	}
	switch t.Type {
	case "uri":
		return NewIRI(value), nil
	case "bnode":
		return NewBlankNode(value), nil
	case "literal", "typed-literal":
		lit := RDFTerm{Kind: RDFTermLiteral, Value: value, Datatype: t.Datatype}
		if t.Lang != "" {
			lit = NewDirLangLiteral(value, t.Lang, t.Dir)
		}
		return lit, nil
	}
	return RDFTerm{}, fmt.Errorf("unknown result term type %q", t.Type)
}

func readSRJ(path string) ([]string, []map[string]RDFTerm, *bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil, err
	}
	var doc struct {
		Head struct {
			Vars []string `json:"vars"`
		} `json:"head"`
		Boolean *bool `json:"boolean"`
		Results struct {
			Bindings []map[string]srjTerm `json:"bindings"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, nil, nil, err
	}
	if doc.Boolean != nil {
		return nil, nil, doc.Boolean, nil
	}
	rows := make([]map[string]RDFTerm, 0, len(doc.Results.Bindings))
	for _, binding := range doc.Results.Bindings {
		row := map[string]RDFTerm{}
		for name, term := range binding {
			value, err := term.toRDF()
			if err != nil {
				return nil, nil, nil, err
			}
			row[name] = value
		}
		rows = append(rows, row)
	}
	return doc.Head.Vars, rows, nil, nil
}

// xmlNode is a generic XML element, enough for the SPARQL XML results format.
type xmlNode struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Content  string     `xml:",chardata"`
	Children []xmlNode  `xml:",any"`
}

func (n xmlNode) attr(local string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

func (n xmlNode) child(local string) (xmlNode, bool) {
	for _, c := range n.Children {
		if c.XMLName.Local == local {
			return c, true
		}
	}
	return xmlNode{}, false
}

func srxTerm(n xmlNode) (RDFTerm, error) {
	switch n.XMLName.Local {
	case "uri":
		return NewIRI(n.Content), nil
	case "bnode":
		return NewBlankNode(strings.TrimSpace(n.Content)), nil
	case "literal":
		if lang := n.attr("lang"); lang != "" {
			return NewDirLangLiteral(n.Content, lang, n.attr("dir")), nil
		}
		return RDFTerm{Kind: RDFTermLiteral, Value: n.Content, Datatype: n.attr("datatype")}, nil
	case "triple":
		var parts [3]RDFTerm
		for i, name := range []string{"subject", "predicate", "object"} {
			holder, ok := n.child(name)
			if !ok || len(holder.Children) != 1 {
				return RDFTerm{}, fmt.Errorf("triple without %s", name)
			}
			term, err := srxTerm(holder.Children[0])
			if err != nil {
				return RDFTerm{}, err
			}
			parts[i] = term
		}
		return NewTripleTerm(parts[0], parts[1], parts[2])
	}
	return RDFTerm{}, fmt.Errorf("unknown result element %q", n.XMLName.Local)
}

func readSRX(path string) ([]string, []map[string]RDFTerm, *bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil, err
	}
	var doc xmlNode
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, nil, nil, err
	}
	if b, ok := doc.child("boolean"); ok {
		value := strings.TrimSpace(b.Content) == "true"
		return nil, nil, &value, nil
	}
	var vars []string
	if head, ok := doc.child("head"); ok {
		for _, v := range head.Children {
			vars = append(vars, v.attr("name"))
		}
	}
	results, _ := doc.child("results")
	var rows []map[string]RDFTerm
	for _, result := range results.Children {
		row := map[string]RDFTerm{}
		for _, binding := range result.Children {
			if len(binding.Children) != 1 {
				return nil, nil, nil, fmt.Errorf("binding %q without a value", binding.attr("name"))
			}
			term, err := srxTerm(binding.Children[0])
			if err != nil {
				return nil, nil, nil, err
			}
			row[binding.attr("name")] = term
		}
		rows = append(rows, row)
	}
	return vars, rows, nil, nil
}

func runSPARQLSuite(t *testing.T, root, manifest string, inScope func(w3cTest) bool) (inTally, outTally map[string]*suiteTally) {
	t.Helper()
	tests, err := collectW3CTests(manifest, map[string]bool{})
	if err != nil {
		t.Fatalf("load %s: %v", manifest, err)
	}
	inTally, outTally = map[string]*suiteTally{}, map[string]*suiteTally{}
	for _, test := range tests {
		tally := outTally
		if inScope(test) {
			tally = inTally
		}
		key := sparqlTestDir(test) + " " + test.typeName()
		if tally[key] == nil {
			tally[key] = &suiteTally{}
		}
		var failures []string
		if !strings.Contains(test.typeName(), "Evaluation") {
			// Syntax tests never touch a store.
			if why := runSPARQLTest(t, root, test, nil); why != "" {
				failures = append(failures, why)
			}
		} else {
			// Every evaluation test starts from an empty dataset on each
			// backend: backends creates fresh stores on every call.
			for _, b := range backends(t) {
				store := b.store
				store.SetPropertyGraphProjection(false)
				if why := runSPARQLTest(t, root, test, func() *GraphStore { return store }); why != "" {
					failures = append(failures, b.name+": "+why)
				}
			}
		}
		if len(failures) > 0 {
			tally[key].fail++
			tally[key].failures = append(tally[key].failures, fmt.Sprintf("%s (%s): %s", test.name, test.node.Value, strings.Join(failures, "\n")))
			continue
		}
		tally[key].pass++
	}
	return inTally, outTally
}

func logOutOfScope(t *testing.T, tallies map[string]*suiteTally) {
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
		t.Logf("out of scope %-48s pass %4d  fail %4d", k, tally.pass, tally.fail)
		for _, f := range tally.failures {
			t.Logf("  out of scope failure: %s", f)
		}
	}
	t.Logf("out of scope total %d, passed %d, failed %d", total, total-failed, failed)
}

func TestTheW3CSPARQL12TripleTermSuitesPassInFull(t *testing.T) {
	root := w3cSuiteRoot(t)
	inTally, outTally := runSPARQLSuite(t, root, filepath.Join(root, "sparql", "sparql12", "manifest.ttl"), func(test w3cTest) bool {
		return sparql12InScope[sparqlTestDir(test)]
	})
	reportTallies(t, "sparql12", inTally)
	logOutOfScope(t, outTally)
}

func TestARepresentativeW3CSPARQL12SubsetPasses(t *testing.T) {
	root := filepath.Join("testdata", "w3c")
	manifest := filepath.Join(root, "sparql", "manifest.ttl")
	if _, err := os.Stat(manifest); err != nil {
		t.Fatalf("subset manifest missing: %v", err)
	}
	inTally, _ := runSPARQLSuite(t, root, manifest, func(w3cTest) bool { return true })
	reportTallies(t, "subset", inTally)
	total := 0
	for _, tally := range inTally {
		total += tally.pass + tally.fail
	}
	if total < 20 {
		t.Fatalf("subset ran %d tests; the manifest should list at least 20", total)
	}
}
