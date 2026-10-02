package graph

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph/cypher"
)

// The openCypher TCK, run against the read-only subset.
//
// Skipped unless CORTEXDB_CYPHER_TCK names a directory of TCK .feature files
// (github.com/opencypher/openCypher, tck/features). It reports, per feature
// file, how many scenarios passed, how many gave a WRONG answer, how many the
// engine refused as outside the subset, and how many were skipped because the
// scenario itself is outside what can be set up or asked here: write queries,
// named graphs, procedures, multi-label nodes, or setup that needs MATCH.
//
// Setup steps use CREATE, which the public language refuses by design. The
// runner translates those CREATE statements into GraphStore calls itself, by
// parsing each CREATE as if it were a MATCH and walking the pattern: the
// language under test never learns to write.
//
// Set CORTEXDB_CYPHER_TCK_STRICT=1 to fail the test on wrong answers.
func TestCypherOpenCypherTCK(t *testing.T) {
	dir := os.Getenv("CORTEXDB_CYPHER_TCK")
	if dir == "" {
		t.Skip("CORTEXDB_CYPHER_TCK unset")
	}
	var files []string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, ".feature") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	_, g, cleanup := setupTestGraph(t)
	defer cleanup()

	type tally struct{ pass, wrong, refused, skip, errPass int }
	var total tally
	causes := map[string]int{}
	var wrongList []string
	only := os.Getenv("CORTEXDB_CYPHER_TCK_ONLY")
	verbose := os.Getenv("CORTEXDB_CYPHER_TCK_VERBOSE") != ""
	for _, f := range files {
		rel, _ := filepath.Rel(dir, f)
		if only != "" && !strings.Contains(rel, only) {
			continue
		}
		scenarios, err := parseFeature(f)
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		var ft tally
		for _, sc := range scenarios {
			outcome, cause := runTCKScenario(t, g, sc)
			switch outcome {
			case "pass":
				ft.pass++
			case "errpass":
				ft.pass++
				ft.errPass++
			case "wrong":
				ft.wrong++
				wrongList = append(wrongList, fmt.Sprintf("%s %s: %s", rel, sc.name, cause))
			case "refused":
				ft.refused++
			default:
				ft.skip++
			}
			if outcome != "pass" && outcome != "errpass" {
				causes[outcome+": "+cause]++
			}
			if verbose && outcome != "pass" && outcome != "errpass" && outcome != "skip" {
				t.Logf("%s %s → %s: %s\n%s", rel, sc.name, outcome, cause, sc.query)
			}
		}
		t.Logf("%-55s pass=%3d (of which expected-error=%d) wrong=%3d refused=%3d skip=%3d", rel, ft.pass, ft.errPass, ft.wrong, ft.refused, ft.skip)
		total.pass += ft.pass
		total.errPass += ft.errPass
		total.wrong += ft.wrong
		total.refused += ft.refused
		total.skip += ft.skip
	}
	t.Logf("TOTAL pass=%d (expected-error=%d) wrong=%d refused=%d skip=%d", total.pass, total.errPass, total.wrong, total.refused, total.skip)
	type kv struct {
		k string
		v int
	}
	var cs []kv
	for k, v := range causes {
		cs = append(cs, kv{k, v})
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].v > cs[j].v || (cs[i].v == cs[j].v && cs[i].k < cs[j].k) })
	for i, c := range cs {
		if i >= 40 {
			break
		}
		t.Logf("cause %4d  %s", c.v, c.k)
	}
	for _, w := range wrongList {
		t.Logf("WRONG %s", w)
	}
	if os.Getenv("CORTEXDB_CYPHER_TCK_STRICT") != "" && total.wrong > 0 {
		t.Errorf("%d wrong answers", total.wrong)
	}
}

// --- feature parsing --------------------------------------------------------

type tckStep struct {
	text  string
	doc   string
	table [][]string
}

type tckScenario struct {
	name  string
	steps []tckStep
	query string
}

func parseFeature(path string) ([]tckScenario, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = fh.Close() }()
	var lines []string
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}

	type raw struct {
		name     string
		outline  bool
		steps    []tckStep
		examples [][]string
	}
	var raws []*raw
	var background []tckStep
	var cur *raw
	inBackground := false
	inExamples := false
	for i := 0; i < len(lines); i++ {
		ln := strings.TrimSpace(lines[i])
		switch {
		case ln == "" || strings.HasPrefix(ln, "#") || strings.HasPrefix(ln, "@"):
			continue
		case strings.HasPrefix(ln, "Feature:"):
			continue
		case strings.HasPrefix(ln, "Background:"):
			inBackground, cur, inExamples = true, nil, false
		case strings.HasPrefix(ln, "Scenario Outline:") || strings.HasPrefix(ln, "Scenario:"):
			inBackground, inExamples = false, false
			cur = &raw{name: strings.TrimSpace(ln[strings.Index(ln, ":")+1:]), outline: strings.HasPrefix(ln, "Scenario Outline:")}
			raws = append(raws, cur)
		case strings.HasPrefix(ln, "Examples:"):
			inExamples = true
		case strings.HasPrefix(ln, "|"):
			row := splitTableRow(ln)
			if inExamples && cur != nil {
				cur.examples = append(cur.examples, row)
				continue
			}
			var st *tckStep
			if inBackground && len(background) > 0 {
				st = &background[len(background)-1]
			} else if cur != nil && len(cur.steps) > 0 {
				st = &cur.steps[len(cur.steps)-1]
			}
			if st != nil {
				st.table = append(st.table, row)
			}
		case strings.HasPrefix(ln, `"""`):
			var b strings.Builder
			indent := strings.Index(lines[i], `"""`)
			for i++; i < len(lines) && strings.TrimSpace(lines[i]) != `"""`; i++ {
				l := lines[i]
				if len(l) >= indent && strings.TrimSpace(l[:indent]) == "" {
					l = l[indent:]
				}
				b.WriteString(l)
				b.WriteString("\n")
			}
			doc := strings.TrimRight(b.String(), "\n")
			if inBackground && len(background) > 0 {
				background[len(background)-1].doc = doc
			} else if cur != nil && len(cur.steps) > 0 {
				cur.steps[len(cur.steps)-1].doc = doc
			}
		default:
			for _, kw := range []string{"Given ", "When ", "Then ", "And ", "But "} {
				if strings.HasPrefix(ln, kw) {
					st := tckStep{text: strings.TrimSpace(ln[len(kw):])}
					if inBackground {
						background = append(background, st)
					} else if cur != nil {
						cur.steps = append(cur.steps, st)
					}
					break
				}
			}
		}
	}

	var out []tckScenario
	for _, r := range raws {
		steps := append(append([]tckStep{}, background...), r.steps...)
		if !r.outline {
			out = append(out, finishScenario(r.name, steps))
			continue
		}
		if len(r.examples) < 2 {
			continue
		}
		header := r.examples[0]
		for ei, ex := range r.examples[1:] {
			sub := func(s string) string {
				for k, h := range header {
					if k < len(ex) {
						s = strings.ReplaceAll(s, "<"+h+">", ex[k])
					}
				}
				return s
			}
			var ss []tckStep
			for _, st := range steps {
				ns := tckStep{text: sub(st.text), doc: sub(st.doc)}
				for _, row := range st.table {
					nr := make([]string, len(row))
					for k := range row {
						nr[k] = sub(row[k])
					}
					ns.table = append(ns.table, nr)
				}
				ss = append(ss, ns)
			}
			out = append(out, finishScenario(fmt.Sprintf("%s (example %d)", r.name, ei+1), ss))
		}
	}
	return out, nil
}

func finishScenario(name string, steps []tckStep) tckScenario {
	s := tckScenario{name: name, steps: steps}
	for _, st := range steps {
		if strings.HasPrefix(st.text, "executing query") {
			s.query = st.doc
		}
	}
	return s
}

func splitTableRow(ln string) []string {
	ln = strings.TrimSpace(ln)
	ln = strings.TrimPrefix(ln, "|")
	ln = strings.TrimSuffix(ln, "|")
	// Gherkin table cells escape \|, \\ and \n; the cell is split on raw
	// bars first and unescaped after trimming, so an escaped newline at the
	// edge of a cell survives.
	var cells []string
	var b strings.Builder
	for i := 0; i < len(ln); i++ {
		if ln[i] == '\\' && i+1 < len(ln) {
			b.WriteByte(ln[i])
			b.WriteByte(ln[i+1])
			i++
			continue
		}
		if ln[i] == '|' {
			cells = append(cells, unescapeCell(strings.TrimSpace(b.String())))
			b.Reset()
			continue
		}
		b.WriteByte(ln[i])
	}
	cells = append(cells, unescapeCell(strings.TrimSpace(b.String())))
	return cells
}

func unescapeCell(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '|':
				b.WriteByte('|')
				i++
				continue
			case '\\':
				b.WriteByte('\\')
				i++
				continue
			case 'n':
				b.WriteByte('\n')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// --- running ----------------------------------------------------------------

var tckWriteRe = regexp.MustCompile(`(?i)\b(CREATE|MERGE|SET|DELETE|REMOVE|FOREACH|CALL|LOAD)\b`)

func runTCKScenario(t *testing.T, g *GraphStore, sc tckScenario) (outcome, cause string) {
	ctx := context.Background()
	for _, tbl := range []string{"graph_edges", "graph_nodes", "graph_edge_history", "graph_node_history"} {
		if _, err := g.db.ExecContext(ctx, "DELETE FROM "+tbl); err != nil {
			t.Fatalf("reset: %v", err)
		}
	}
	params := map[string]any{}
	var expect *tckStep
	expectKind := ""
	for i := range sc.steps {
		st := &sc.steps[i]
		switch {
		case st.text == "an empty graph" || st.text == "any graph":
		case strings.HasPrefix(st.text, "the ") && strings.HasSuffix(st.text, " graph"):
			return "skip", "named graph fixture"
		case strings.HasPrefix(st.text, "having executed"):
			if why := tckCreate(ctx, g, st.doc); why != "" {
				return "skip", "setup: " + why
			}
		case strings.HasPrefix(st.text, "parameters are"):
			for _, row := range st.table {
				if len(row) < 2 {
					continue
				}
				v, err := parseTCKValue(row[1])
				if err != nil {
					return "skip", "parameter literal: " + err.Error()
				}
				params[row[0]] = tckToParam(v)
			}
		case strings.HasPrefix(st.text, "there exists a procedure"):
			return "skip", "procedures"
		case strings.HasPrefix(st.text, "executing control query"):
			return "skip", "control query"
		case strings.HasPrefix(st.text, "executing query"):
		case strings.HasPrefix(st.text, "the result should be empty"):
			expectKind = "empty"
		case strings.HasPrefix(st.text, "the result should be"):
			expect = st
			expectKind = "rows"
		case strings.Contains(st.text, "should be raised"):
			expectKind = "error"
		case strings.HasPrefix(st.text, "no side effects"), strings.HasPrefix(st.text, "the side effects should be"):
		}
	}
	if sc.query == "" {
		return "skip", "no query"
	}
	if tckWriteRe.MatchString(stripStrings(sc.query)) {
		return "skip", "write or procedure query"
	}
	res, err := g.QueryCypher(ctx, CypherRequest{Query: sc.query, Params: params, MaxRows: cypher.HardMaxRows, IncludeEndedFacts: true})
	if expectKind == "error" {
		var ce *cypher.Error
		if err != nil && errors.As(err, &ce) && ce.Kind == cypher.ErrUnsupported {
			return "refused", "expected an error; refused as unsupported: " + firstWords(ce.Msg)
		}
		if err != nil {
			return "errpass", ""
		}
		return "wrong", "expected an error, got a result"
	}
	if err != nil {
		var ce *cypher.Error
		if errors.As(err, &ce) && (ce.Kind == cypher.ErrUnsupported || ce.Kind == cypher.ErrReadOnly || ce.Kind == cypher.ErrBudget) {
			return "refused", firstWords(ce.Msg)
		}
		if errors.As(err, &ce) {
			return "wrong", "error " + string(ce.Kind) + ": " + firstWords(ce.Msg)
		}
		return "wrong", "error: " + firstWords(err.Error())
	}
	var want [][]string
	ordered := false
	ignoreListOrder := false
	if expectKind == "rows" {
		ordered = strings.Contains(expect.text, "in order")
		ignoreListOrder = strings.Contains(expect.text, "ignoring element order for lists")
		if len(expect.table) == 0 {
			return "skip", "no result table"
		}
		header := expect.table[0]
		if len(header) != len(res.Columns) {
			return "wrong", fmt.Sprintf("columns %v, want %v", res.Columns, header)
		}
		for i := range header {
			if header[i] != res.Columns[i] {
				return "wrong", fmt.Sprintf("columns %v, want %v", res.Columns, header)
			}
		}
		for _, row := range expect.table[1:] {
			var cells []string
			for _, c := range row {
				v, err := parseTCKValue(c)
				if err != nil {
					return "skip", "result literal: " + err.Error()
				}
				cells = append(cells, formatTCK(v, ignoreListOrder))
			}
			want = append(want, cells)
		}
	}
	var got [][]string
	for _, row := range res.Rows {
		var cells []string
		for _, v := range row {
			cells = append(cells, formatTCK(fromCypher(v), ignoreListOrder))
		}
		got = append(got, cells)
	}
	if expectKind == "empty" && len(got) == 0 {
		return "pass", ""
	}
	gj, wj := joinRows(got), joinRows(want)
	if !ordered {
		sort.Strings(gj)
		sort.Strings(wj)
	}
	if strings.Join(gj, "\n") == strings.Join(wj, "\n") {
		return "pass", ""
	}
	return "wrong", fmt.Sprintf("got %v want %v", truncateList(gj), truncateList(wj))
}

func truncateList(s []string) []string {
	if len(s) > 4 {
		return append(s[:4:4], fmt.Sprintf("… (%d rows)", len(s)))
	}
	return s
}

func joinRows(rows [][]string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = strings.Join(r, " | ")
	}
	return out
}

func firstWords(s string) string {
	if i := strings.Index(s, " (supported:"); i > 0 {
		s = s[:i]
	}
	if len(s) > 90 {
		s = s[:90]
	}
	return s
}

func stripStrings(q string) string {
	var b strings.Builder
	var quote byte
	for i := 0; i < len(q); i++ {
		c := q[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' || c == '`' {
			quote = c
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// tckCreate executes a setup consisting only of CREATE clauses by parsing
// each as a MATCH pattern and writing the nodes and edges it names.
func tckCreate(ctx context.Context, g *GraphStore, doc string) string {
	body := stripStrings(doc)
	for _, kw := range []string{"MATCH", "UNWIND", "WITH", "MERGE", "SET", "DELETE", "FOREACH", "CALL", "RETURN", "OPTIONAL", "REMOVE"} {
		if regexp.MustCompile(`(?i)\b` + kw + `\b`).MatchString(body) {
			return "uses " + kw
		}
	}
	createRe := regexp.MustCompile(`(?i)\bCREATE\b`)
	// Replace CREATE outside string literals only.
	var b strings.Builder
	var quote byte
	for i := 0; i < len(doc); i++ {
		c := doc[i]
		if quote != 0 {
			b.WriteByte(c)
			if c == '\\' && i+1 < len(doc) {
				b.WriteByte(doc[i+1])
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' || c == '`' {
			quote = c
			b.WriteByte(c)
			continue
		}
		if loc := createRe.FindStringIndex(doc[i:]); loc != nil && loc[0] == 0 && (i == 0 || !isWordByte(doc[i-1])) {
			b.WriteString("MATCH")
			i += loc[1] - 1
			continue
		}
		b.WriteByte(c)
	}
	q, err := cypher.Parse(b.String() + " RETURN 1")
	if err != nil {
		return "unparseable CREATE: " + firstWords(err.Error())
	}
	if len(q.Parts) != 1 {
		return "union in setup"
	}
	ids := map[string]string{}
	n := 0
	newNode := func(np *cypher.NodePattern) (string, string) {
		if np.Var != "" {
			if id, ok := ids[np.Var]; ok {
				if len(np.Labels) > 0 || (np.Props != nil && len(np.Props.Keys) > 0) {
					return "", "re-declared variable with labels/properties"
				}
				return id, ""
			}
		}
		typ := ""
		for _, grp := range np.Labels {
			if len(grp) != 1 {
				return "", "label alternation in CREATE"
			}
		}
		if len(np.Labels) > 1 {
			return "", "multiple labels on one node"
		}
		if len(np.Labels) == 1 {
			typ = np.Labels[0][0]
		}
		props, why := tckProps(np.Props)
		if why != "" {
			return "", why
		}
		n++
		id := fmt.Sprintf("tck:%04d", n)
		if err := g.UpsertNode(ctx, &GraphNode{ID: id, NodeType: typ, Properties: props, Vector: []float32{1, 0, 0}}); err != nil {
			return "", "node write: " + err.Error()
		}
		if np.Var != "" {
			ids[np.Var] = id
		}
		return id, ""
	}
	e := 0
	for _, cl := range q.Parts[0].Clauses {
		mc, ok := cl.(*cypher.MatchClause)
		if !ok {
			continue
		}
		if mc.Where != nil || mc.Optional {
			return "unexpected clause"
		}
		for _, pp := range mc.Patterns {
			if pp.PathVar != "" {
				return "named path in CREATE"
			}
			prev, why := newNode(pp.Nodes[0])
			if why != "" {
				return why
			}
			for i, rp := range pp.Rels {
				next, why := newNode(pp.Nodes[i+1])
				if why != "" {
					return why
				}
				if len(rp.Types) != 1 || rp.VarLen || rp.Dir == cypher.DirBoth {
					return "relationship shape in CREATE"
				}
				props, why := tckProps(rp.Props)
				if why != "" {
					return why
				}
				from, to := prev, next
				if rp.Dir == cypher.DirIn {
					from, to = next, prev
				}
				e++
				if err := g.UpsertEdge(ctx, &GraphEdge{ID: fmt.Sprintf("tck:e%04d", e), FromNodeID: from, ToNodeID: to, EdgeType: rp.Types[0], Properties: props}); err != nil {
					return "edge write: " + err.Error()
				}
				prev = next
			}
		}
	}
	return ""
}

func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// tckProps evaluates a CREATE property map, which in the TCK is always
// literals. A null property is not stored, as in openCypher.
func tckProps(m *cypher.MapLit) (map[string]any, string) {
	if m == nil || len(m.Keys) == 0 {
		return nil, ""
	}
	out := map[string]any{}
	for i, k := range m.Keys {
		v, ok := literalOf(m.Values[i])
		if !ok {
			return nil, "non-literal property in CREATE"
		}
		if v == nil {
			continue
		}
		if f, isF := v.(float64); isF && (math.IsNaN(f) || math.IsInf(f, 0)) {
			return nil, "NaN/Inf property cannot be stored as JSON"
		}
		out[k] = v
	}
	return out, ""
}

func literalOf(e cypher.Expr) (any, bool) {
	switch x := e.(type) {
	case *cypher.Literal:
		return x.Value, true
	case *cypher.Unary:
		if x.Op != "-" {
			return nil, false
		}
		v, ok := literalOf(x.X)
		if !ok {
			return nil, false
		}
		switch n := v.(type) {
		case int64:
			return -n, true
		case float64:
			return -n, true
		}
		return nil, false
	case *cypher.ListLit:
		out := make([]any, len(x.Items))
		for i, it := range x.Items {
			v, ok := literalOf(it)
			if !ok {
				return nil, false
			}
			out[i] = v
		}
		return out, true
	}
	return nil, false
}

// --- TCK value literals -------------------------------------------------------

type tckNode struct {
	labels []string
	props  map[string]any
}

type tckRel struct {
	typ   string
	props map[string]any
}

type tckPath struct {
	nodes []tckNode
	rels  []tckRel
	dirs  []bool // true when the relationship points forward
}

func fromCypher(v any) any {
	switch x := v.(type) {
	case *cypher.Node:
		return tckNode{labels: x.Labels, props: x.Properties}
	case *cypher.Rel:
		return tckRel{typ: x.Type, props: x.Properties}
	case *cypher.Path:
		p := tckPath{}
		for _, n := range x.Nodes {
			p.nodes = append(p.nodes, tckNode{labels: n.Labels, props: n.Properties})
		}
		for i, r := range x.Rels {
			p.rels = append(p.rels, tckRel{typ: r.Type, props: r.Properties})
			p.dirs = append(p.dirs, r.StartID == x.Nodes[i].ID)
		}
		return p
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = fromCypher(x[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = fromCypher(e)
		}
		return out
	}
	return v
}

func formatTCK(v any, ignoreListOrder bool) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		if x == 0 {
			x = 0 // -0.0 and 0.0 are the same number
		}
		switch {
		case math.IsNaN(x):
			return "NaN"
		case math.IsInf(x, 1):
			return "Infinity"
		case math.IsInf(x, -1):
			return "-Infinity"
		}
		s := strconv.FormatFloat(x, 'g', 15, 64)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		return s
	case string:
		return "'" + x + "'"
	case []any:
		parts := make([]string, len(x))
		for i := range x {
			parts[i] = formatTCK(x[i], ignoreListOrder)
		}
		if ignoreListOrder {
			sort.Strings(parts)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		return formatProps(x, ignoreListOrder)
	case tckNode:
		s := "("
		for _, l := range x.labels {
			s += ":" + l
		}
		if len(x.props) > 0 {
			if len(x.labels) > 0 {
				s += " "
			}
			s += formatProps(x.props, ignoreListOrder)
		}
		return s + ")"
	case tckRel:
		s := "[:" + x.typ
		if len(x.props) > 0 {
			s += " " + formatProps(x.props, ignoreListOrder)
		}
		return s + "]"
	case tckPath:
		s := "<" + formatTCK(x.nodes[0], ignoreListOrder)
		for i, r := range x.rels {
			if x.dirs[i] {
				s += "-" + formatTCK(r, ignoreListOrder) + "->"
			} else {
				s += "<-" + formatTCK(r, ignoreListOrder) + "-"
			}
			s += formatTCK(x.nodes[i+1], ignoreListOrder)
		}
		return s + ">"
	}
	return fmt.Sprintf("?%T", v)
}

func formatProps(m map[string]any, ilo bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + ": " + formatTCK(m[k], ilo)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func tckToParam(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = tckToParam(x[i])
		}
		return out
	case map[string]any:
		return x
	}
	return v
}

type tckParser struct {
	s string
	i int
}

func parseTCKValue(s string) (any, error) {
	p := &tckParser{s: strings.TrimSpace(s)}
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	p.ws()
	if p.i != len(p.s) {
		return nil, fmt.Errorf("trailing text in %q", s)
	}
	return v, nil
}

func (p *tckParser) ws() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

func (p *tckParser) peek() byte {
	p.ws()
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

func (p *tckParser) expect(c byte) error {
	if p.peek() != c {
		return fmt.Errorf("expected %q at %d in %q", c, p.i, p.s)
	}
	p.i++
	return nil
}

func (p *tckParser) ident() string {
	p.ws()
	if p.i < len(p.s) && p.s[p.i] == '`' {
		end := strings.IndexByte(p.s[p.i+1:], '`')
		if end >= 0 {
			id := p.s[p.i+1 : p.i+1+end]
			p.i += end + 2
			return id
		}
	}
	st := p.i
	for p.i < len(p.s) && (isWordByte(p.s[p.i]) || p.s[p.i] >= 0x80) {
		p.i++
	}
	return p.s[st:p.i]
}

func (p *tckParser) value() (any, error) {
	switch c := p.peek(); {
	case c == '\'':
		p.i++
		var b strings.Builder
		for p.i < len(p.s) {
			ch := p.s[p.i]
			if ch == '\\' && p.i+1 < len(p.s) {
				switch e := p.s[p.i+1]; e {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				case 'r':
					b.WriteByte('\r')
				default:
					b.WriteByte(e)
				}
				p.i += 2
				continue
			}
			if ch == '\'' {
				p.i++
				return b.String(), nil
			}
			b.WriteByte(ch)
			p.i++
		}
		return nil, fmt.Errorf("unterminated string")
	case c == '[':
		save := p.i
		p.i++
		if p.peek() == ':' {
			p.i = save
			return p.rel()
		}
		out := []any{}
		if p.peek() == ']' {
			p.i++
			return out, nil
		}
		for {
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			if p.peek() == ',' {
				p.i++
				continue
			}
			break
		}
		return out, p.expect(']')
	case c == '{':
		return p.props()
	case c == '(':
		return p.node()
	case c == '<':
		p.i++
		path := tckPath{}
		n, err := p.node()
		if err != nil {
			return nil, err
		}
		path.nodes = append(path.nodes, n)
		for p.peek() == '-' || p.peek() == '<' {
			back := false
			if p.peek() == '<' {
				back = true
				p.i++
			}
			if err := p.expect('-'); err != nil {
				return nil, err
			}
			r, err := p.rel()
			if err != nil {
				return nil, err
			}
			if err := p.expect('-'); err != nil {
				return nil, err
			}
			if !back {
				if err := p.expect('>'); err != nil {
					return nil, err
				}
			}
			n, err := p.node()
			if err != nil {
				return nil, err
			}
			path.rels = append(path.rels, r)
			path.dirs = append(path.dirs, !back)
			path.nodes = append(path.nodes, n)
		}
		return path, p.expect('>')
	default:
		st := p.i
		for p.i < len(p.s) && !strings.ContainsRune(",]}) ", rune(p.s[p.i])) {
			p.i++
		}
		tok := p.s[st:p.i]
		switch tok {
		case "null":
			return nil, nil
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "NaN":
			return math.NaN(), nil
		case "Infinity":
			return math.Inf(1), nil
		case "-Infinity":
			return math.Inf(-1), nil
		}
		if i, err := strconv.ParseInt(tok, 10, 64); err == nil {
			return i, nil
		}
		if f, err := strconv.ParseFloat(tok, 64); err == nil {
			return f, nil
		}
		return nil, fmt.Errorf("unknown literal %q", tok)
	}
}

func (p *tckParser) props() (map[string]any, error) {
	if err := p.expect('{'); err != nil {
		return nil, err
	}
	out := map[string]any{}
	if p.peek() == '}' {
		p.i++
		return out, nil
	}
	for {
		k := p.ident()
		if err := p.expect(':'); err != nil {
			return nil, err
		}
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		out[k] = v
		if p.peek() == ',' {
			p.i++
			continue
		}
		break
	}
	return out, p.expect('}')
}

func (p *tckParser) node() (tckNode, error) {
	n := tckNode{labels: []string{}}
	if err := p.expect('('); err != nil {
		return n, err
	}
	for p.peek() == ':' {
		p.i++
		n.labels = append(n.labels, p.ident())
	}
	if p.peek() == '{' {
		m, err := p.props()
		if err != nil {
			return n, err
		}
		n.props = m
	}
	return n, p.expect(')')
}

func (p *tckParser) rel() (tckRel, error) {
	r := tckRel{}
	if err := p.expect('['); err != nil {
		return r, err
	}
	if err := p.expect(':'); err != nil {
		return r, err
	}
	r.typ = p.ident()
	if p.peek() == '{' {
		m, err := p.props()
		if err != nil {
			return r, err
		}
		r.props = m
	}
	return r, p.expect(']')
}
