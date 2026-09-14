package cortexdb

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

func openSchemaDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "schema.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func putSchemaNode(t *testing.T, db *DB, id, nodeType string, props map[string]interface{}) string {
	t.Helper()
	res, err := db.graph.UpsertNodesBatch(context.Background(), []*graph.GraphNode{{
		ID: id, Vector: []float32{0.1, 0.2}, Content: id, NodeType: nodeType, Properties: props,
	}})
	if err == nil {
		err = res.Err()
	}
	if err != nil {
		t.Fatalf("put node %s: %v", id, err)
	}
	return id
}

func putSchemaEdge(t *testing.T, db *DB, id, from, to, edgeType string) {
	t.Helper()
	res, err := db.graph.UpsertEdgesBatch(context.Background(), []*graph.GraphEdge{{
		ID: id, FromNodeID: from, ToNodeID: to, EdgeType: edgeType, Weight: 1,
	}})
	if err == nil {
		err = res.Err()
	}
	if err != nil {
		t.Fatalf("put edge %s: %v", id, err)
	}
}

// A small graph with the two things a traversal has to get right: two edge
// types that sound alike and run between different ends, and a property whose
// stored values are codes rather than the words a question would use.
func seedSchemaGraph(t *testing.T, db *DB) {
	t.Helper()
	people := []string{"alice", "bob", "carol"}
	for _, name := range people {
		putSchemaNode(t, db, "person:"+name, "Person", map[string]interface{}{
			"name": name,
			"role": "engineer",
		})
	}
	putSchemaNode(t, db, "company:acme", "Company", map[string]interface{}{"name": "Acme"})
	putSchemaNode(t, db, "vehicle:v1", "Vehicle", map[string]interface{}{"color": "BLK", "plate": "AAA-1"})
	putSchemaNode(t, db, "vehicle:v2", "Vehicle", map[string]interface{}{"color": "BLK", "plate": "AAA-2"})
	putSchemaNode(t, db, "vehicle:v3", "Vehicle", map[string]interface{}{"color": "WHI", "plate": "AAA-3"})

	for _, name := range people {
		putSchemaEdge(t, db, "edge:works:"+name, "person:"+name, "company:acme", "WORKS_AT")
	}
	putSchemaEdge(t, db, "edge:owns:1", "person:alice", "vehicle:v1", "OWNS")
	putSchemaEdge(t, db, "edge:owns:2", "company:acme", "vehicle:v2", "OWNS")
}

// The acceptance test in miniature: everything a model needs to write a
// traversal has to be in one answer — the types, their counts, what each
// relation actually joins, and which keys the nodes carry.
func TestGraphSchemaReportsTypesShapesAndKeys(t *testing.T) {
	db := openSchemaDB(t)
	seedSchemaGraph(t, db)

	resp, err := db.GraphSchema(context.Background(), GraphSchemaRequest{})
	if err != nil {
		t.Fatalf("graph schema: %v", err)
	}

	if resp.Nodes != 7 || resp.Edges != 5 {
		t.Fatalf("totals = %d nodes / %d edges, want 7 / 5", resp.Nodes, resp.Edges)
	}
	if resp.NodeTypeCount != 3 || resp.EdgeTypeCount != 2 {
		t.Fatalf("type counts = %d node types / %d edge types, want 3 / 2",
			resp.NodeTypeCount, resp.EdgeTypeCount)
	}
	if resp.Truncated {
		t.Fatalf("nothing was capped, but the answer claims it was trimmed")
	}

	// Descending count, then name: Person 3, Vehicle 3, Company 1.
	var nodeOrder []string
	for _, n := range resp.NodeTypes {
		nodeOrder = append(nodeOrder, n.NodeType)
	}
	if got, want := nodeOrder, []string{"Person", "Vehicle", "Company"}; !equalStrings(got, want) {
		t.Fatalf("node type order = %v, want %v (count desc, then name)", got, want)
	}

	person := resp.NodeTypes[0]
	if person.PropertyKeyCount != 2 {
		t.Fatalf("Person carries %d keys, want 2", person.PropertyKeyCount)
	}
	// name is distinct per person, role is shared: the difference between an
	// identifying key and a classifying one, which is the reason distinct
	// values are reported at all.
	for _, k := range person.PropertyKeys {
		switch k.Key {
		case "name":
			if k.Records != 3 || k.DistinctValues != 3 {
				t.Fatalf("Person.name = %d records / %d distinct, want 3 / 3", k.Records, k.DistinctValues)
			}
		case "role":
			if k.Records != 3 || k.DistinctValues != 1 {
				t.Fatalf("Person.role = %d records / %d distinct, want 3 / 1", k.Records, k.DistinctValues)
			}
		default:
			t.Fatalf("Person carries an unexpected key %q", k.Key)
		}
	}

	shapes := map[string][]GraphEdgeShapeSchema{}
	for _, e := range resp.EdgeTypes {
		shapes[e.EdgeType] = e.Shapes
	}
	if got := shapes["WORKS_AT"]; len(got) != 1 || got[0].FromType != "Person" || got[0].ToType != "Company" || got[0].Count != 3 {
		t.Fatalf("WORKS_AT shapes = %+v, want one Person->Company with 3 edges", got)
	}
	// OWNS runs from two different node types, which is exactly the fact a
	// declared ontology usually flattens and a query written from one has to
	// know.
	if got := shapes["OWNS"]; len(got) != 2 {
		t.Fatalf("OWNS shapes = %+v, want two", got)
	}

	for _, want := range []string{
		"Person -[WORKS_AT]-> Company (3)",
		"Person -[OWNS]-> Vehicle (1)",
		"Company -[OWNS]-> Vehicle (1)",
		"Person (3): name 3/3, role 3/1",
	} {
		if !strings.Contains(resp.Text, want) {
			t.Fatalf("rendered schema is missing %q:\n%s", want, resp.Text)
		}
	}
	// The line that stops the BLK failure before it happens.
	if !strings.Contains(resp.Text, "graph_property_values") {
		t.Fatalf("rendered schema never tells the reader how to see stored values:\n%s", resp.Text)
	}
}

// An empty graph is a real answer. A caller inspecting a store before deciding
// what to do with it must not have to tell "nothing here" from a failure.
func TestGraphSchemaOnEmptyGraphIsAnEmptyAnswer(t *testing.T) {
	db := openSchemaDB(t)

	resp, err := db.GraphSchema(context.Background(), GraphSchemaRequest{})
	if err != nil {
		t.Fatalf("graph schema on empty graph: %v", err)
	}
	if len(resp.NodeTypes) != 0 || len(resp.EdgeTypes) != 0 {
		t.Fatalf("empty graph described %d node types and %d edge types",
			len(resp.NodeTypes), len(resp.EdgeTypes))
	}
	if resp.Nodes != 0 || resp.Edges != 0 || resp.Truncated {
		t.Fatalf("empty graph reported %+v", resp)
	}
	if resp.Text == "" {
		t.Fatalf("empty graph rendered nothing; the reader learns neither that it is empty nor that the call worked")
	}
}

// Ordering has to be a property of the data, not of whichever rows the map
// iteration happened to yield: a listing that reorders between two reads of one
// graph cannot be cached and cannot be diffed.
func TestGraphSchemaOrderIsDeterministicByCountThenName(t *testing.T) {
	db := openSchemaDB(t)
	// Three types, two of them tied, deliberately inserted out of order.
	putSchemaNode(t, db, "z:1", "Zebra", nil)
	putSchemaNode(t, db, "a:1", "Aardvark", nil)
	putSchemaNode(t, db, "m:1", "Mongoose", nil)
	putSchemaNode(t, db, "m:2", "Mongoose", nil)

	ctx := context.Background()
	want := []string{"Mongoose", "Aardvark", "Zebra"}
	for i := 0; i < 5; i++ {
		resp, err := db.GraphSchema(ctx, GraphSchemaRequest{})
		if err != nil {
			t.Fatalf("graph schema: %v", err)
		}
		var got []string
		for _, n := range resp.NodeTypes {
			got = append(got, n.NodeType)
		}
		if !equalStrings(got, want) {
			t.Fatalf("read %d gave order %v, want %v", i, got, want)
		}
	}
}

// A cap that quietly dropped types would teach a model that a relation it
// cannot see does not exist, which is worse than a long answer.
func TestGraphSchemaSaysWhenItWasTrimmed(t *testing.T) {
	db := openSchemaDB(t)
	seedSchemaGraph(t, db)
	ctx := context.Background()

	resp, err := db.GraphSchema(ctx, GraphSchemaRequest{MaxNodeTypes: 1, MaxEdgeTypes: 1})
	if err != nil {
		t.Fatalf("graph schema: %v", err)
	}
	if len(resp.NodeTypes) != 1 || len(resp.EdgeTypes) != 1 {
		t.Fatalf("caps did not bite: %d node types, %d edge types", len(resp.NodeTypes), len(resp.EdgeTypes))
	}
	if !resp.Truncated {
		t.Fatalf("answer was trimmed but does not say so")
	}
	// The counts still describe the whole graph, so the reader can see how much
	// is missing rather than only that something is.
	if resp.NodeTypeCount != 3 || resp.EdgeTypeCount != 2 {
		t.Fatalf("pre-cap counts = %d / %d, want 3 / 2", resp.NodeTypeCount, resp.EdgeTypeCount)
	}
	for _, want := range []string{"more node types not shown", "more edge types not shown"} {
		if !strings.Contains(resp.Text, want) {
			t.Fatalf("rendered schema hides the trim (%q missing):\n%s", want, resp.Text)
		}
	}

	// The per-type caps are separate, so one promiscuous relation cannot
	// squeeze every other relation's shape out of the answer.
	perType, err := db.GraphSchema(ctx, GraphSchemaRequest{MaxEdgeShapes: 1, MaxPropertyKeys: 1})
	if err != nil {
		t.Fatalf("graph schema: %v", err)
	}
	if !perType.Truncated {
		t.Fatalf("per-type caps bit but the answer does not say so")
	}
	for _, e := range perType.EdgeTypes {
		if e.EdgeType == "OWNS" {
			if len(e.Shapes) != 1 || e.ShapeCount != 2 || !e.Truncated {
				t.Fatalf("OWNS = %+v, want 1 of 2 shapes and a truncation flag", e)
			}
		}
	}
	if !strings.Contains(perType.Text, "more shapes not shown") {
		t.Fatalf("rendered schema hides the per-edge-type trim:\n%s", perType.Text)
	}
	if !strings.Contains(perType.Text, "more keys not shown") {
		t.Fatalf("rendered schema hides the per-node-type trim:\n%s", perType.Text)
	}
}

// Narrowing is how a caller reads the shape of one relation without paying for
// the other 283.
func TestGraphSchemaNarrowsByType(t *testing.T) {
	db := openSchemaDB(t)
	seedSchemaGraph(t, db)

	resp, err := db.GraphSchema(context.Background(), GraphSchemaRequest{
		NodeTypes: []string{"Vehicle"},
		EdgeTypes: []string{"OWNS"},
	})
	if err != nil {
		t.Fatalf("graph schema: %v", err)
	}
	if len(resp.NodeTypes) != 1 || resp.NodeTypes[0].NodeType != "Vehicle" {
		t.Fatalf("node types = %+v, want only Vehicle", resp.NodeTypes)
	}
	if len(resp.EdgeTypes) != 1 || resp.EdgeTypes[0].EdgeType != "OWNS" {
		t.Fatalf("edge types = %+v, want only OWNS", resp.EdgeTypes)
	}
	if resp.Nodes != 3 || resp.Edges != 2 {
		t.Fatalf("narrowed totals = %d / %d, want 3 / 2", resp.Nodes, resp.Edges)
	}
	// The shapes of a named edge type still show both of its ends, including
	// the Company end that the node filter excluded — an edge runs between two
	// things whether or not the caller asked about both.
	if len(resp.EdgeTypes[0].Shapes) != 2 {
		t.Fatalf("OWNS shapes under a node filter = %+v, want both", resp.EdgeTypes[0].Shapes)
	}
	if !strings.Contains(resp.Text, "Narrowed to") {
		t.Fatalf("rendered schema does not say it is a partial view:\n%s", resp.Text)
	}
}

// Untyped rows are counted rather than dropped, and named rather than rendered
// as a blank gap.
func TestGraphSchemaReportsUntypedRows(t *testing.T) {
	db := openSchemaDB(t)
	putSchemaNode(t, db, "loose:1", "", nil)
	putSchemaNode(t, db, "loose:2", "", nil)
	putSchemaEdge(t, db, "edge:loose", "loose:1", "loose:2", "")

	resp, err := db.GraphSchema(context.Background(), GraphSchemaRequest{})
	if err != nil {
		t.Fatalf("graph schema: %v", err)
	}
	if len(resp.NodeTypes) != 1 || resp.NodeTypes[0].NodeType != "" || resp.NodeTypes[0].Count != 2 {
		t.Fatalf("untyped nodes = %+v, want one bucket of 2", resp.NodeTypes)
	}
	// The untyped edge cannot be narrowed by an IN list, so its shapes are the
	// case that would silently come back empty if the filter were passed
	// through as an empty string.
	if len(resp.EdgeTypes) != 1 || len(resp.EdgeTypes[0].Shapes) != 1 {
		t.Fatalf("untyped edge shapes = %+v, want one", resp.EdgeTypes)
	}
	if !strings.Contains(resp.Text, "(untyped)") {
		t.Fatalf("untyped rows render as a blank gap:\n%s", resp.Text)
	}
}

// The failure this whole file exists to prevent: the question says "black", the
// graph says BLK, and only the stored values can bridge the two.
func TestGraphPropertyValuesReportsValuesAsStored(t *testing.T) {
	db := openSchemaDB(t)
	seedSchemaGraph(t, db)

	resp, err := db.GraphPropertyValues(context.Background(), GraphPropertyValuesRequest{Key: "color"})
	if err != nil {
		t.Fatalf("graph property values: %v", err)
	}
	if resp.DistinctValues != 2 {
		t.Fatalf("distinct values = %d, want 2", resp.DistinctValues)
	}
	if got := resp.Values[0]; got.Value != "BLK" || got.Nodes != 2 || got.Total != 2 {
		t.Fatalf("most common value = %+v, want BLK on 2 nodes", got)
	}
	if got := resp.Values[1]; got.Value != "WHI" || got.Nodes != 1 {
		t.Fatalf("second value = %+v, want WHI on 1 node", got)
	}
	// Records that do not carry the key are the context the distribution is
	// meaningless without, and they must not be listed as a value.
	if resp.AbsentNodes != 4 || resp.AbsentEdges != 5 {
		t.Fatalf("absent = %d nodes / %d edges, want 4 / 5", resp.AbsentNodes, resp.AbsentEdges)
	}
	for _, v := range resp.Values {
		if v.Value == "" {
			t.Fatalf("the empty bucket was listed as if it were a value: %+v", resp.Values)
		}
	}
	for _, want := range []string{"BLK", "WHI", "do not carry", "Match these strings exactly"} {
		if !strings.Contains(resp.Text, want) {
			t.Fatalf("rendered values are missing %q:\n%s", want, resp.Text)
		}
	}
}

func TestGraphPropertyValuesCapsAndSaysSo(t *testing.T) {
	db := openSchemaDB(t)
	seedSchemaGraph(t, db)

	resp, err := db.GraphPropertyValues(context.Background(), GraphPropertyValuesRequest{Key: "color", Limit: 1})
	if err != nil {
		t.Fatalf("graph property values: %v", err)
	}
	if len(resp.Values) != 1 || !resp.Truncated {
		t.Fatalf("limit 1 gave %d values, truncated=%v", len(resp.Values), resp.Truncated)
	}
	if resp.DistinctValues != 2 {
		t.Fatalf("distinct count = %d, want the pre-cap 2", resp.DistinctValues)
	}
	if !strings.Contains(resp.Text, "more values not shown") {
		t.Fatalf("rendered values hide the trim:\n%s", resp.Text)
	}
}

// A key nothing carries is an empty answer with the reason attached, not an
// error: "nobody stamped this" is what the caller asked and got.
func TestGraphPropertyValuesForAnAbsentKeyIsEmpty(t *testing.T) {
	db := openSchemaDB(t)
	seedSchemaGraph(t, db)

	resp, err := db.GraphPropertyValues(context.Background(), GraphPropertyValuesRequest{Key: "nonexistent"})
	if err != nil {
		t.Fatalf("graph property values: %v", err)
	}
	if len(resp.Values) != 0 || resp.DistinctValues != 0 {
		t.Fatalf("absent key returned values: %+v", resp.Values)
	}
	if !strings.Contains(resp.Text, "No record in the graph carries") {
		t.Fatalf("rendered answer does not say the key is unused:\n%s", resp.Text)
	}
}

// An unnamed key is a caller mistake, and answering it with every key in the
// graph would hide the mistake behind a very large result.
func TestGraphPropertyValuesRefusesAnEmptyKey(t *testing.T) {
	db := openSchemaDB(t)
	if _, err := db.GraphPropertyValues(context.Background(), GraphPropertyValuesRequest{}); !errors.Is(err, ErrNoPropertyKey) {
		t.Fatalf("empty key gave %v, want ErrNoPropertyKey", err)
	}
}

// The tool surface differs from the facade in exactly one way: it caps by
// default, because its answer lands in a context window.
func TestToolboxGraphSchemaCapsByDefault(t *testing.T) {
	db := openSchemaDB(t)
	seedSchemaGraph(t, db)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	capped, err := tools.GraphSchema(ctx, GraphSchemaRequest{})
	if err != nil {
		t.Fatalf("toolbox graph schema: %v", err)
	}
	// This graph is far below every default, so the caps must not bite here —
	// a default that trimmed a three-type graph would be useless.
	if capped.Truncated {
		t.Fatalf("defaults trimmed a tiny graph: %+v", capped)
	}
	if len(capped.NodeTypes) != 3 || len(capped.EdgeTypes) != 2 {
		t.Fatalf("toolbox answer = %d node types / %d edge types, want 3 / 2",
			len(capped.NodeTypes), len(capped.EdgeTypes))
	}

	// A cap the caller names wins over the default.
	narrow, err := tools.GraphSchema(ctx, GraphSchemaRequest{MaxNodeTypes: 1})
	if err != nil {
		t.Fatalf("toolbox graph schema: %v", err)
	}
	if len(narrow.NodeTypes) != 1 || !narrow.Truncated {
		t.Fatalf("explicit cap ignored: %+v", narrow)
	}
}

func TestToolboxGraphPropertyValues(t *testing.T) {
	db := openSchemaDB(t)
	seedSchemaGraph(t, db)

	resp, err := db.GraphRAGTools().GraphPropertyValues(context.Background(), GraphPropertyValuesRequest{Key: "color"})
	if err != nil {
		t.Fatalf("toolbox graph property values: %v", err)
	}
	if len(resp.Values) != 2 || resp.Truncated {
		t.Fatalf("default limit trimmed a two-value key: %+v", resp)
	}
	if resp.Values[0].Value != "BLK" {
		t.Fatalf("values = %+v, want BLK first", resp.Values)
	}
}

// Every graph read in this package must survive a store nobody has written to:
// a read-only tool hitting a fresh database has to get an empty result, not
// "no such table: graph_nodes".
func TestGraphSchemaReadsWorkOnAFreshStore(t *testing.T) {
	db := openSchemaDB(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	if _, err := tools.GraphSchema(ctx, GraphSchemaRequest{}); err != nil {
		t.Fatalf("graph_schema on a fresh store: %v", err)
	}
	if _, err := tools.GraphPropertyValues(ctx, GraphPropertyValuesRequest{Key: "color"}); err != nil {
		t.Fatalf("graph_property_values on a fresh store: %v", err)
	}
}
