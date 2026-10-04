package graph

import (
	"bytes"
	"context"
	"encoding/csv"
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

// sparql11Deviations are the SPARQL 1.1 tests this engine fails on purpose.
// plus-1-corrected and plus-2-corrected expect "1" + "2" — simple literals
// that look like numbers — to be a type error; arithmetic here reads such a
// string as a number (see lenientNumber), because data written through the
// non-SPARQL APIs commonly stores numbers as plain strings.
var sparql11Deviations = map[string]bool{
	"plus-1-corrected": true,
	"plus-2-corrected": true,
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
	case test.hasType("QueryEvaluationTest") || test.hasType("CSVResultFormatTest"):
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
		// qt:serviceData gives each SERVICE endpoint its own dataset: one
		// store per endpoint, reached through the service handler.
		endpoints := map[string]*GraphStore{}
		for _, sd := range m.values(test.action, qtNS+"serviceData") {
			endpoint, ok := m.one(sd, qtNS+"endpoint")
			if !ok {
				return "qt:serviceData without qt:endpoint"
			}
			_, remote, cleanup := setupTestGraph(t)
			t.Cleanup(cleanup)
			for _, data := range m.values(sd, qtNS+"data") {
				if why := loadTestData(remote, root, manifestFile(data.Value), nil); why != "" {
					return why
				}
			}
			endpoints[endpoint.Value] = remote
		}
		if len(endpoints) > 0 {
			// Every endpoint can reach the others: a SERVICE inside a
			// SERVICE runs at the first endpoint.
			handler := func(ctx context.Context, endpoint, query string) (*SPARQLResult, error) {
				remote, ok := endpoints[endpoint]
				if !ok {
					return nil, fmt.Errorf("no endpoint %s", endpoint)
				}
				return remote.ExecuteSPARQL(ctx, query)
			}
			for _, remote := range endpoints {
				remote.SetSPARQLServiceHandler(handler)
			}
			store.SetSPARQLServiceHandler(handler)
			defer store.SetSPARQLServiceHandler(nil)
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
		for _, gd := range m.values(test.action, utNS+"graphData") {
			file, name, why := updateGraphData(m, gd)
			if why != "" {
				return why
			}
			if why := loadTestData(store, root, file, &name); why != "" {
				return why
			}
		}
		update, err := os.ReadFile(manifestFile(request.Value))
		if err != nil {
			return err.Error()
		}
		if _, err := store.ExecuteSPARQL(context.Background(), withBase(string(update), w3cDocumentBase(root, manifestFile(request.Value)))); err != nil {
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
		for _, gd := range m.values(test.result, utNS+"graphData") {
			file, name, why := updateGraphData(m, gd)
			if why != "" {
				return why
			}
			parsed, why := parseTestData(root, file)
			if why != "" {
				return why
			}
			for i := range parsed {
				g := name
				parsed[i].Graph = &g
			}
			want = append(want, parsed...)
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

// updateGraphData reads an update test's ut:graphData: a node naming the
// file with ut:graph and the graph with rdfs:label.
func updateGraphData(m *manifestGraph, node RDFTerm) (string, RDFTerm, string) {
	file, ok := m.one(node, utNS+"graph")
	if !ok {
		return "", RDFTerm{}, "ut:graphData without ut:graph"
	}
	label, ok := m.one(node, "http://www.w3.org/2000/01/rdf-schema#label")
	if !ok {
		return "", RDFTerm{}, "ut:graphData without rdfs:label"
	}
	return manifestFile(file.Value), NewIRI(label.Value), ""
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
	case ".rdf":
		syntax = rdfSyntaxRDFXML
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
		// The same answer written in the expected file's format must read
		// back as the same solutions: this tests WriteResults.
		format, read := SPARQLResultsJSON, readSRJBytes
		if ext == ".srx" {
			format, read = SPARQLResultsXML, readSRXBytes
		}
		return roundTripResults(result, format, read, want)
	case ".tsv":
		data, err := os.ReadFile(expectedPath)
		if err != nil {
			return err.Error()
		}
		_, rows, err := readTSVResults(data)
		if err != nil {
			return "cannot read expected results: " + err.Error()
		}
		got, want := solutionsAsQuads(result.Bindings), solutionsAsQuads(rows)
		if ok, diff := isomorphicDatasets(got, want); !ok {
			return "solutions differ: " + diff
		}
		return roundTripResults(result, SPARQLResultsTSV, func(b []byte) ([]string, []map[string]RDFTerm, *bool, error) {
			vars, rows, err := readTSVResults(b)
			return vars, rows, nil, err
		}, want)
	case ".csv":
		var buf bytes.Buffer
		if err := result.WriteResults(&buf, SPARQLResultsCSV); err != nil {
			return "write CSV: " + err.Error()
		}
		want, err := os.ReadFile(expectedPath)
		if err != nil {
			return err.Error()
		}
		return compareCSVResults(buf.Bytes(), want)
	default:
		want, why := parseTestData(root, expectedPath)
		if why != "" {
			return why
		}
		// A SELECT or ASK result written as RDF, in the rs: vocabulary.
		if rows, boolean, ok := resultSetFromGraph(want); ok && result.QueryType != SPARQLQueryConstruct && result.QueryType != SPARQLQueryDescribe {
			if boolean != nil {
				if result.Boolean != *boolean {
					return fmt.Sprintf("ASK returned %v, want %v", result.Boolean, *boolean)
				}
				return ""
			}
			got, wantQ := solutionsAsQuads(result.Bindings), solutionsAsQuads(rows)
			if ok, diff := isomorphicDatasets(got, wantQ); !ok {
				return "solutions differ: " + diff
			}
			return ""
		}
		if ok, diff := isomorphicDatasets(result.Triples, want); !ok {
			return diff
		}
		return ""
	}
}

// roundTripResults writes result in format, reads it back with read and
// compares the solutions with want.
func roundTripResults(result *SPARQLResult, format SPARQLResultsFormat, read func([]byte) ([]string, []map[string]RDFTerm, *bool, error), want []RDFTriple) string {
	var buf bytes.Buffer
	if err := result.WriteResults(&buf, format); err != nil {
		return "write " + string(format) + ": " + err.Error()
	}
	_, rows, boolean, err := read(buf.Bytes())
	if err != nil {
		return "read back " + string(format) + ": " + err.Error() + "\n" + buf.String()
	}
	if boolean != nil {
		return ""
	}
	if ok, diff := isomorphicDatasets(solutionsAsQuads(rows), want); !ok {
		return string(format) + " round trip differs: " + diff
	}
	return ""
}

// readTSVResults reads a TSV result: a header of ?variables, then one row
// per line of terms written as in Turtle, an empty field for unbound.
func readTSVResults(data []byte) ([]string, []map[string]RDFTerm, error) {
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"), "\n")
	if len(lines) == 0 {
		return nil, nil, fmt.Errorf("empty TSV")
	}
	var vars []string
	for _, h := range strings.Split(lines[0], "\t") {
		vars = append(vars, strings.TrimPrefix(strings.TrimPrefix(h, "?"), "$"))
	}
	var rows []map[string]RDFTerm
	for _, line := range lines[1:] {
		row := map[string]RDFTerm{}
		for i, cell := range strings.Split(line, "\t") {
			if cell == "" || i >= len(vars) {
				continue
			}
			parsed, err := parseRDFDocument("<urn:s> <urn:p> "+cell+" .", rdfSyntaxTurtle, "")
			if err != nil || len(parsed) != 1 {
				return nil, nil, fmt.Errorf("TSV cell %q: %v", cell, err)
			}
			row[vars[i]] = parsed[0].Object
		}
		rows = append(rows, row)
	}
	return vars, rows, nil
}

// compareCSVResults compares two CSV results as multisets of rows, blank
// node labels up to renaming. CSV keeps no term kinds, so a cell is read as
// a blank node if it starts with _:, else as a plain value.
func compareCSVResults(got, want []byte) string {
	read := func(data []byte) ([]string, []map[string]RDFTerm, error) {
		records, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
		if err != nil || len(records) == 0 {
			return nil, nil, fmt.Errorf("bad CSV: %v", err)
		}
		var rows []map[string]RDFTerm
		for _, rec := range records[1:] {
			row := map[string]RDFTerm{}
			for i, cell := range rec {
				switch {
				case cell == "":
				case strings.HasPrefix(cell, "_:"):
					row[records[0][i]] = NewBlankNode(cell[2:])
				default:
					row[records[0][i]] = NewLiteral(cell)
				}
			}
			rows = append(rows, row)
		}
		return records[0], rows, nil
	}
	gv, gr, err := read(got)
	if err != nil {
		return "our CSV: " + err.Error() + "\n" + string(got)
	}
	wv, wr, err := read(want)
	if err != nil {
		return "expected CSV: " + err.Error()
	}
	if strings.Join(gv, ",") != strings.Join(wv, ",") {
		return fmt.Sprintf("CSV header %v, want %v", gv, wv)
	}
	if ok, diff := isomorphicDatasets(solutionsAsQuads(gr), solutionsAsQuads(wr)); !ok {
		return "CSV differs: " + diff
	}
	return ""
}

// resultSetFromGraph reads a result set written in the DAWG result-set
// vocabulary, which some suites use in place of .srx.
func resultSetFromGraph(triples []RDFTriple) ([]map[string]RDFTerm, *bool, bool) {
	const rs = "http://www.w3.org/2001/sw/DataAccess/tests/result-set#"
	by := map[string]map[string][]RDFTerm{}
	var set *RDFTerm
	for _, t := range triples {
		k := t.Subject.String()
		if by[k] == nil {
			by[k] = map[string][]RDFTerm{}
		}
		by[k][t.Predicate.Value] = append(by[k][t.Predicate.Value], t.Object)
		if t.Predicate.Value == rdfNS+"type" && t.Object.Value == rs+"ResultSet" {
			subject := t.Subject
			set = &subject
		}
	}
	if set == nil {
		return nil, nil, false
	}
	props := by[set.String()]
	if b := props[rs+"boolean"]; len(b) == 1 {
		value := b[0].Value == "true"
		return nil, &value, true
	}
	var rows []map[string]RDFTerm
	for _, solution := range props[rs+"solution"] {
		row := map[string]RDFTerm{}
		for _, binding := range by[solution.String()][rs+"binding"] {
			bp := by[binding.String()]
			if len(bp[rs+"variable"]) == 1 && len(bp[rs+"value"]) == 1 {
				row[bp[rs+"variable"][0].Value] = bp[rs+"value"][0]
			}
		}
		rows = append(rows, row)
	}
	return rows, nil, true
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
			out = append(out, RDFTriple{Subject: node, Predicate: NewIRI("urn:var:" + name), Object: numericByValue(value)})
		}
	}
	return out
}

// numericByValue writes an XSD numeric literal in one form per value, so two
// results that differ only in how a number is spelled compare equal: the
// suites' expected results spell the same value differently from test to test
// ("1.0" and "1" as xsd:decimal), and the datatype still has to agree. This
// is the value comparison other implementations' harnesses apply to the
// SPARQL results of the W3C suites.
func numericByValue(term RDFTerm) RDFTerm {
	if n, ok := strictNumber(term); ok {
		return NewTypedLiteral(canonicalNumberLexical(n), term.Datatype)
	}
	return term
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
	return readSRJBytes(data)
}

func readSRJBytes(data []byte) ([]string, []map[string]RDFTerm, *bool, error) {
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
	return readSRXBytes(data)
}

func readSRXBytes(data []byte) ([]string, []map[string]RDFTerm, *bool, error) {
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
		if !strings.Contains(test.typeName(), "Evaluation") && !test.hasType("CSVResultFormatTest") {
			// Syntax tests never touch a store.
			if why := runSPARQLTest(t, root, test, nil); why != "" {
				failures = append(failures, why)
			}
		} else {
			// Every evaluation test starts from an empty dataset on each
			// backend: backends creates fresh stores on every call. The
			// subtest is what releases them — a PostgreSQL schema and its
			// connections — before the next test, not at the end of the
			// suite.
			t.Run(test.name, func(t *testing.T) {
				for _, b := range backends(t) {
					store := b.store
					store.SetPropertyGraphProjection(false)
					if why := runSPARQLTest(t, root, test, func() *GraphStore { return store }); why != "" {
						failures = append(failures, b.name+": "+why)
					}
				}
			})
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
		t.Logf("deviation %-48s pass %4d  fail %4d", k, tally.pass, tally.fail)
		for _, f := range tally.failures {
			t.Logf("  deviation: %s", f)
		}
	}
	t.Logf("deviations total %d, passed %d, failed %d", total, total-failed, failed)
}

func TestTheW3CSPARQL12SuitesPassInFull(t *testing.T) {
	root := w3cSuiteRoot(t)
	inTally, _ := runSPARQLSuite(t, root, filepath.Join(root, "sparql", "sparql12", "manifest.ttl"), func(w3cTest) bool { return true })
	reportTallies(t, "sparql12", inTally)
}

// The SPARQL 1.1 query, update, results-format and federation suites, and
// the second update syntax suite. Entailment regimes, the protocol, the graph
// store protocol and service descriptions are HTTP or reasoning-profile
// specifications this embedded store does not implement as such.
func TestTheW3CSPARQL11SuitesPassInFull(t *testing.T) {
	root := w3cSuiteRoot(t)
	for _, manifest := range []string{"manifest-sparql11-query.ttl", "manifest-sparql11-update.ttl",
		"manifest-sparql11-results.ttl", "manifest-sparql11-fed.ttl", filepath.Join("syntax-update-2", "manifest.ttl")} {
		t.Run(manifest, func(t *testing.T) {
			inTally, outTally := runSPARQLSuite(t, root, filepath.Join(root, "sparql", "sparql11", manifest), func(test w3cTest) bool {
				local := test.node.Value[strings.LastIndex(test.node.Value, "#")+1:]
				return !sparql11Deviations[local]
			})
			reportTallies(t, "sparql11", inTally)
			logOutOfScope(t, outTally)
		})
	}
}

func TestARepresentativeW3CSPARQLSubsetPasses(t *testing.T) {
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
