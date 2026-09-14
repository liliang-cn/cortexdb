package cortexdb

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Reporting the shape a graph actually has, so a query can be written against
// it.
//
// Writing a correct traversal against a graph nobody has seen needs two facts
// that no amount of reasoning can supply: which types and keys exist, and what
// the stored values literally look like. Guessing the first produces a query
// that names a relation the graph does not have; guessing the second produces
// the worse failure, a query that runs, matches nothing, and answers "there are
// no black vehicles" when the column says BLK. Both answers come back
// confidently, and neither can be told from a true one.
//
// pkg/graph already measures all of this — NodeTypeCounts, EdgeTypeCounts,
// EdgeShapes, NodePropertyKeys, PropertyCounts — and until now the only things
// reaching it were the ontology drafter and the contract tally. This file is
// the general read: the observed schema, for anybody about to query.
//
// Observed, and that is the point of having it beside ontology_get. An ontology
// is a schema somebody declared, and declaring one is opt-in; a graph built by
// extraction usually has none, and that is precisely the graph whose shape
// nobody knows. This describes what the rows say about themselves, so it works
// on the graph that needs it most.

// ErrNoPropertyKey is returned when a property-value listing is asked for
// without naming a key.
//
// Refused rather than answered with every key in the graph: the distribution of
// one key is already the large half of this file's output, and "all of them"
// over a store of any size is a way to lose a context window to a forgotten
// field. An unset key is a mistake, and it should read as one.
var ErrNoPropertyKey = errors.New("cortexdb: graph property values need a key")

// Tool-side caps for the schema read.
//
// The Go API defaults to no cap, following RangeSearchVector: a program asking
// what is in a graph can hold what is in that graph. A tool answer lands in a
// model's context window, where a graph with 284 edge types would crowd out the
// question it was fetched to help answer. The response says when a cap bit, so
// a trimmed listing cannot be mistaken for a complete one.
const (
	defaultGraphSchemaNodeTypes     = 40
	defaultGraphSchemaEdgeTypes     = 40
	defaultGraphSchemaEdgeShapes    = 6
	defaultGraphSchemaPropertyKeys  = 12
	defaultGraphPropertyValueLimit  = 50
	untypedGraphSchemaTypeRendering = "(untyped)"
)

// GraphSchemaRequest narrows and caps a schema read. A zero request describes
// the whole graph.
type GraphSchemaRequest struct {
	// NodeTypes and EdgeTypes keep only the named types. Matched exactly as
	// stored, which is the rule every type filter in pkg/graph follows: the
	// type in the graph is whatever the extracting model emitted, and nothing
	// in this layer decides what a near-miss means. Empty means every type.
	NodeTypes []string `json:"node_types,omitempty"`
	EdgeTypes []string `json:"edge_types,omitempty"`

	// MaxNodeTypes and MaxEdgeTypes cap how many types are listed. The types
	// are ordered by descending count first, so a cap keeps the ones most of
	// the graph is made of. Zero means no cap.
	MaxNodeTypes int `json:"max_node_types,omitempty"`
	MaxEdgeTypes int `json:"max_edge_types,omitempty"`

	// MaxEdgeShapes caps the endpoint-type pairs listed *per edge type*, and
	// MaxPropertyKeys the property keys listed *per node type*. Per type rather
	// than overall, so one promiscuous relation cannot squeeze every other
	// relation's shape out of the answer.
	MaxEdgeShapes   int `json:"max_edge_shapes,omitempty"`
	MaxPropertyKeys int `json:"max_property_keys,omitempty"`
}

// GraphPropertyKeySchema is one property key on the nodes of one type.
//
// DistinctValues is what separates a key that identifies a record from one that
// classifies it: on 412 Person nodes a `name` with 407 distinct values and a
// `role` with 12 are different kinds of field, and only this number says so. It
// is also how a reader decides whether asking for the value distribution is
// worth it.
type GraphPropertyKeySchema struct {
	Key            string `json:"key"`
	Records        int    `json:"records"`
	DistinctValues int    `json:"distinct_values"`
}

// GraphNodeTypeSchema is one node type: how many nodes carry it and what those
// nodes have written on them.
type GraphNodeTypeSchema struct {
	// NodeType is the spelling in the data. The empty string is the untyped,
	// which are reported rather than dropped — "400 nodes nobody typed" is a
	// finding, and omitting it silently makes the counts fail to add up.
	NodeType string `json:"node_type"`
	Count    int    `json:"count"`
	// PropertyKeys holds the keys this type's nodes carry, most widely carried
	// first, capped by MaxPropertyKeys.
	PropertyKeys []GraphPropertyKeySchema `json:"property_keys,omitempty"`
	// PropertyKeyCount is how many keys there are before the cap, so a reader
	// can tell a short list from a trimmed one.
	PropertyKeyCount int  `json:"property_key_count"`
	Truncated        bool `json:"truncated,omitempty"`
}

// GraphEdgeShapeSchema is one node-type pair that an edge type actually runs
// between.
//
// The single most useful fact for writing a traversal, and the one a declared
// ontology most often gets wrong: an extracted relation asserted backwards is
// stored without complaint and walked as a fact. The direction here is the
// direction in the data.
type GraphEdgeShapeSchema struct {
	FromType string `json:"from_type"`
	ToType   string `json:"to_type"`
	Count    int    `json:"count"`
}

// GraphEdgeTypeSchema is one edge type: how many edges carry it and what they
// connect.
type GraphEdgeTypeSchema struct {
	EdgeType string                 `json:"edge_type"`
	Count    int                    `json:"count"`
	Shapes   []GraphEdgeShapeSchema `json:"shapes,omitempty"`
	// ShapeCount is how many distinct shapes exist before the cap.
	ShapeCount int  `json:"shape_count"`
	Truncated  bool `json:"truncated,omitempty"`
}

// GraphSchemaResponse is the observed schema: what is in the graph, by type.
type GraphSchemaResponse struct {
	NodeTypes []GraphNodeTypeSchema `json:"node_types"`
	EdgeTypes []GraphEdgeTypeSchema `json:"edge_types"`
	// Nodes and Edges are the totals over the types that matched the filter,
	// counted before any cap. A caller comparing them against the sum of the
	// listed counts learns exactly how much the cap hid.
	Nodes int `json:"nodes"`
	Edges int `json:"edges"`
	// NodeTypeCount and EdgeTypeCount are how many types matched, likewise
	// before any cap.
	NodeTypeCount int `json:"node_type_count"`
	EdgeTypeCount int `json:"edge_type_count"`
	// Truncated is true when any cap bit anywhere in the answer — the type
	// lists, an edge type's shapes, or a node type's keys. A model reading a
	// schema decides what exists from it, so a trimmed answer that looked
	// complete would teach it that a relation it cannot see does not exist.
	Truncated bool `json:"truncated"`
	// Text is the same answer rendered for reading.
	//
	// Not a convenience. This result exists to be pasted into a prompt, and a
	// few lines of "Person -[WORKS_AT]-> Company (412)" tell a model writing a
	// query far more, for far fewer tokens, than the nested objects above,
	// which it would have to reassemble into exactly these sentences first.
	Text string `json:"text"`
}

// GraphSchema reports the types, endpoint shapes and property keys the graph
// actually has.
//
// One call, because it is one thing to read: a traversal needs the node types,
// the edge types, and which of the former each of the latter joins, and an
// answer that supplied any two of the three would send the caller straight back
// for the third.
//
// An empty graph is an empty answer, not an error. "Nothing here yet" is a true
// and useful description of a graph, and a caller inspecting a store before
// deciding what to do with it should not have to distinguish that from a
// failure.
func (db *DB) GraphSchema(ctx context.Context, req GraphSchemaRequest) (*GraphSchemaResponse, error) {
	// The graph tables are created by Open, but the read paths in this package
	// re-assert the schema so a read against a store some other writer opened
	// cannot fail with "no such table: graph_nodes" instead of returning
	// nothing. See TestFreshDBGraphReadsReturnEmpty.
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return nil, err
	}

	nodeCounts, err := db.graph.NodeTypeCounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("graph schema: %w", err)
	}
	edgeCounts, err := db.graph.EdgeTypeCounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("graph schema: %w", err)
	}

	nodeTypes, nodeTotal, nodeTypesTruncated := rankedGraphTypes(nodeCounts, req.NodeTypes, req.MaxNodeTypes)
	edgeTypes, edgeTotal, edgeTypesTruncated := rankedGraphTypes(edgeCounts, req.EdgeTypes, req.MaxEdgeTypes)

	resp := &GraphSchemaResponse{
		NodeTypes:     make([]GraphNodeTypeSchema, 0, len(nodeTypes)),
		EdgeTypes:     make([]GraphEdgeTypeSchema, 0, len(edgeTypes)),
		Nodes:         nodeTotal,
		Edges:         edgeTotal,
		NodeTypeCount: len(nodeCounts),
		EdgeTypeCount: len(edgeCounts),
		Truncated:     nodeTypesTruncated || edgeTypesTruncated,
	}
	if len(req.NodeTypes) > 0 {
		resp.NodeTypeCount = countGraphTypesMatching(nodeCounts, req.NodeTypes)
	}
	if len(req.EdgeTypes) > 0 {
		resp.EdgeTypeCount = countGraphTypesMatching(edgeCounts, req.EdgeTypes)
	}

	keysByType, err := db.nodePropertyKeysByType(ctx, nodeTypes)
	if err != nil {
		return nil, err
	}
	for _, t := range nodeTypes {
		keys := keysByType[t.name]
		sortGraphPropertyKeys(keys)
		entry := GraphNodeTypeSchema{
			NodeType:         t.name,
			Count:            t.count,
			PropertyKeyCount: len(keys),
		}
		if req.MaxPropertyKeys > 0 && len(keys) > req.MaxPropertyKeys {
			keys = keys[:req.MaxPropertyKeys]
			entry.Truncated = true
			resp.Truncated = true
		}
		entry.PropertyKeys = keys
		resp.NodeTypes = append(resp.NodeTypes, entry)
	}

	shapesByType, err := db.edgeShapesByType(ctx, edgeTypes)
	if err != nil {
		return nil, err
	}
	for _, t := range edgeTypes {
		shapes := shapesByType[t.name]
		sortGraphEdgeShapes(shapes)
		entry := GraphEdgeTypeSchema{
			EdgeType:   t.name,
			Count:      t.count,
			ShapeCount: len(shapes),
		}
		if req.MaxEdgeShapes > 0 && len(shapes) > req.MaxEdgeShapes {
			shapes = shapes[:req.MaxEdgeShapes]
			entry.Truncated = true
			resp.Truncated = true
		}
		entry.Shapes = shapes
		resp.EdgeTypes = append(resp.EdgeTypes, entry)
	}

	resp.Text = renderGraphSchema(resp, req, nodeTypesTruncated, edgeTypesTruncated)
	return resp, nil
}

// nodePropertyKeysByType reads the property keys of exactly the node types that
// survived the filter and the cap.
//
// Narrowed rather than read whole and discarded: enumerating JSON keys is the
// expensive read in this file, and there is no reason to pay for it over the
// 244 types the answer will not mention.
func (db *DB) nodePropertyKeysByType(ctx context.Context, types []graphTypeCount) (map[string][]GraphPropertyKeySchema, error) {
	out := map[string][]GraphPropertyKeySchema{}
	if len(types) == 0 {
		// An empty variadic means "every type" to pkg/graph, so an empty
		// selection has to skip the call rather than make it.
		return out, nil
	}
	names := make([]string, 0, len(types))
	for _, t := range types {
		names = append(names, t.name)
	}
	usages, err := db.graph.NodePropertyKeys(ctx, names...)
	if err != nil {
		return nil, fmt.Errorf("graph schema: %w", err)
	}
	for _, u := range usages {
		out[u.NodeType] = append(out[u.NodeType], GraphPropertyKeySchema{
			Key:            u.Key,
			Records:        u.Records,
			DistinctValues: u.Distinct,
		})
	}
	return out, nil
}

// edgeShapesByType reads the endpoint shapes of the edge types that survived.
//
// Untyped edges force the one wrinkle here. EdgeShapes filters on the raw
// edge_type column, where an untyped edge is NULL and no IN list can match it,
// so a selection containing the empty string is read unfiltered and narrowed in
// Go. Passing "" to the filter instead would report the untyped as having no
// shapes at all, which is a claim about the graph rather than a limit of the
// query.
func (db *DB) edgeShapesByType(ctx context.Context, types []graphTypeCount) (map[string][]GraphEdgeShapeSchema, error) {
	out := map[string][]GraphEdgeShapeSchema{}
	if len(types) == 0 {
		return out, nil
	}
	wanted := make(map[string]bool, len(types))
	names := make([]string, 0, len(types))
	untyped := false
	for _, t := range types {
		wanted[t.name] = true
		if t.name == "" {
			untyped = true
			continue
		}
		names = append(names, t.name)
	}
	if untyped {
		names = nil
	}

	var shapes []graph.EdgeShape
	var err error
	if len(names) == 0 {
		shapes, err = db.graph.EdgeShapes(ctx)
	} else {
		shapes, err = db.graph.EdgeShapes(ctx, names...)
	}
	if err != nil {
		return nil, fmt.Errorf("graph schema: %w", err)
	}
	for _, s := range shapes {
		if !wanted[s.EdgeType] {
			continue
		}
		out[s.EdgeType] = append(out[s.EdgeType], GraphEdgeShapeSchema{
			FromType: s.FromType,
			ToType:   s.ToType,
			Count:    s.Count,
		})
	}
	return out, nil
}

// graphTypeCount is one type name with how many records carry it.
type graphTypeCount struct {
	name  string
	count int
}

// rankedGraphTypes filters, orders and caps a type-count map, and reports the
// total across everything that matched before the cap.
//
// Descending count then ascending name, which is the order every listing in
// this file uses. Deterministic because a schema listing whose order changes
// between two reads of one graph cannot be cached and cannot be diffed; by
// count because when a cap has to drop something, the types most of the graph
// is made of are the ones worth keeping.
func rankedGraphTypes(counts map[string]int, only []string, max int) ([]graphTypeCount, int, bool) {
	keep := make(map[string]bool, len(only))
	for _, name := range only {
		keep[name] = true
	}

	ranked := make([]graphTypeCount, 0, len(counts))
	total := 0
	for name, count := range counts {
		if len(keep) > 0 && !keep[name] {
			continue
		}
		ranked = append(ranked, graphTypeCount{name: name, count: count})
		total += count
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].count != ranked[j].count {
			return ranked[i].count > ranked[j].count
		}
		return ranked[i].name < ranked[j].name
	})

	truncated := false
	if max > 0 && len(ranked) > max {
		ranked = ranked[:max]
		truncated = true
	}
	return ranked, total, truncated
}

// countGraphTypesMatching is how many of the named types the graph actually
// has. A caller that asked about a type nothing carries should see that it
// asked about nothing, rather than reading the whole graph's type count.
func countGraphTypesMatching(counts map[string]int, only []string) int {
	n := 0
	for _, name := range only {
		if _, ok := counts[name]; ok {
			n++
		}
	}
	return n
}

func sortGraphPropertyKeys(keys []GraphPropertyKeySchema) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Records != keys[j].Records {
			return keys[i].Records > keys[j].Records
		}
		return keys[i].Key < keys[j].Key
	})
}

func sortGraphEdgeShapes(shapes []GraphEdgeShapeSchema) {
	sort.Slice(shapes, func(i, j int) bool {
		if shapes[i].Count != shapes[j].Count {
			return shapes[i].Count > shapes[j].Count
		}
		if shapes[i].FromType != shapes[j].FromType {
			return shapes[i].FromType < shapes[j].FromType
		}
		return shapes[i].ToType < shapes[j].ToType
	})
}

// graphTypeLabel renders a type name for reading. The untyped are named rather
// than shown as an empty gap, which in a rendered line is indistinguishable
// from a formatting bug.
func graphTypeLabel(name string) string {
	if name == "" {
		return untypedGraphSchemaTypeRendering
	}
	return name
}

// renderGraphSchema writes the schema as the paragraphs a model needs before
// writing a query.
//
// The two closing lines are the load-bearing part. Everything above them says
// what exists; they say what the reader must not assume — that a type name is
// approximate, and that a property's values are words. Both assumptions produce
// a query that runs and returns nothing, which is the failure this whole file
// exists to prevent.
func renderGraphSchema(resp *GraphSchemaResponse, req GraphSchemaRequest, nodeTypesTruncated, edgeTypesTruncated bool) string {
	var b strings.Builder
	b.WriteString("Observed graph schema — measured from the stored rows, not a declared ontology.\n")

	if len(req.NodeTypes) > 0 || len(req.EdgeTypes) > 0 {
		b.WriteString("Narrowed to")
		if len(req.NodeTypes) > 0 {
			b.WriteString(" node types " + strings.Join(req.NodeTypes, ", "))
		}
		if len(req.NodeTypes) > 0 && len(req.EdgeTypes) > 0 {
			b.WriteString(" and")
		}
		if len(req.EdgeTypes) > 0 {
			b.WriteString(" edge types " + strings.Join(req.EdgeTypes, ", "))
		}
		b.WriteString("; the graph has more.\n")
	}

	if resp.Nodes == 0 && resp.Edges == 0 {
		b.WriteString("Nothing matched: there are no nodes and no edges to describe.\n")
		return b.String()
	}

	b.WriteString(plural(resp.Nodes, "node", "nodes") + " across " +
		plural(resp.NodeTypeCount, "node type", "node types") + "; " +
		plural(resp.Edges, "edge", "edges") + " across " +
		plural(resp.EdgeTypeCount, "edge type", "edge types") + ".\n")

	b.WriteString("\nNODE TYPES (node count) and the property keys their nodes carry, as key records/distinct-values\n")
	if len(resp.NodeTypes) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, t := range resp.NodeTypes {
		b.WriteString("  " + graphTypeLabel(t.NodeType) + " (" + strconv.Itoa(t.Count) + ")")
		if len(t.PropertyKeys) == 0 {
			b.WriteString(": no property keys")
		} else {
			parts := make([]string, 0, len(t.PropertyKeys))
			for _, k := range t.PropertyKeys {
				parts = append(parts, k.Key+" "+strconv.Itoa(k.Records)+"/"+strconv.Itoa(k.DistinctValues))
			}
			b.WriteString(": " + strings.Join(parts, ", "))
			if t.Truncated {
				b.WriteString(", and " + strconv.Itoa(t.PropertyKeyCount-len(t.PropertyKeys)) + " more keys not shown")
			}
		}
		b.WriteString("\n")
	}
	if nodeTypesTruncated {
		b.WriteString("  ... " + strconv.Itoa(resp.NodeTypeCount-len(resp.NodeTypes)) +
			" more node types not shown; raise max_node_types or name the ones you need.\n")
	}

	b.WriteString("\nEDGE TYPES (edge count) and the node-type pairs they actually connect\n")
	if len(resp.EdgeTypes) == 0 {
		// Said out loud rather than left as a blank section. "No edges" is a
		// finding about the graph — retrieval still finds the nodes and
		// expansion never leaves them — and an empty heading reads as a bug.
		b.WriteString("  (none — nothing in this graph is connected)\n")
	}
	for _, t := range resp.EdgeTypes {
		label := graphTypeLabel(t.EdgeType)
		b.WriteString("  " + label + " (" + strconv.Itoa(t.Count) + ")\n")
		for _, s := range t.Shapes {
			b.WriteString("    " + graphTypeLabel(s.FromType) + " -[" + label + "]-> " +
				graphTypeLabel(s.ToType) + " (" + strconv.Itoa(s.Count) + ")\n")
		}
		if t.Truncated {
			b.WriteString("    ... " + strconv.Itoa(t.ShapeCount-len(t.Shapes)) + " more shapes not shown\n")
		}
	}
	if edgeTypesTruncated {
		b.WriteString("  ... " + strconv.Itoa(resp.EdgeTypeCount-len(resp.EdgeTypes)) +
			" more edge types not shown; raise max_edge_types or name the ones you need.\n")
	}

	b.WriteString("\nType names and property keys are shown exactly as stored; match them literally, not by meaning.\n")
	b.WriteString("Property values are not listed here. Before filtering on a value, call graph_property_values with the key: stored values are often codes rather than words, and a filter written from the key's name alone returns nothing and looks like an answer.\n")
	return b.String()
}

// GraphPropertyValue is one value of one property, and how many records carry
// it.
//
// Nodes and edges are kept apart because they are not interchangeable to a
// reader: an edge is an assertion about two things and a node is one thing, so
// where a value lives changes what a query filtering on it will find.
type GraphPropertyValue struct {
	Value string `json:"value"`
	Nodes int    `json:"nodes"`
	Edges int    `json:"edges"`
	Total int    `json:"total"`
}

// GraphPropertyValuesRequest asks what values one property key takes.
type GraphPropertyValuesRequest struct {
	// Key is the top-level property key to break down. Required.
	Key string `json:"key"`
	// Limit caps how many values are listed, most frequent first. Zero means no
	// cap.
	Limit int `json:"limit,omitempty"`
}

// GraphPropertyValuesResponse is the value distribution of one property key.
type GraphPropertyValuesResponse struct {
	Key    string               `json:"key"`
	Values []GraphPropertyValue `json:"values"`
	// DistinctValues is how many distinct values exist before the cap.
	DistinctValues int `json:"distinct_values"`
	// Nodes and Edges are how many records carry the key with a value.
	Nodes int `json:"nodes"`
	Edges int `json:"edges"`
	// AbsentNodes and AbsentEdges count the records that do not carry the key —
	// or carry it empty, which the storage layer cannot tell apart from not
	// carrying it. Usually the most important number here: a breakdown over the
	// 3% of a graph that was stamped with a key looks exactly like a breakdown
	// over all of it.
	AbsentNodes int  `json:"absent_nodes"`
	AbsentEdges int  `json:"absent_edges"`
	Truncated   bool `json:"truncated"`
	// Text renders the same answer for reading, values included, so it can go
	// straight into a prompt beside the schema.
	Text string `json:"text"`
}

// GraphPropertyValues reports which values a property key actually takes, and
// how often.
//
// Its own entry point rather than a field of the schema read, for three
// reasons: it is asked per key, it can be far larger than the schema itself,
// and it is asked *after* the schema has said the key exists. Folding it in
// would make every schema read pay for the values of every key.
//
// This is the call that answers the failure this package's users hit most: the
// question says "black", the graph says BLK, and a filter written from the
// key's name alone returns nothing while reading as a confident negative
// answer. Nothing infers the mapping — the values are reported as stored and
// the caller matches on them.
func (db *DB) GraphPropertyValues(ctx context.Context, req GraphPropertyValuesRequest) (*GraphPropertyValuesResponse, error) {
	if req.Key == "" {
		return nil, ErrNoPropertyKey
	}
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return nil, err
	}

	counts, err := db.graph.PropertyCounts(ctx, req.Key)
	if err != nil {
		return nil, fmt.Errorf("graph property values: %w", err)
	}

	resp := &GraphPropertyValuesResponse{
		Key:    req.Key,
		Values: []GraphPropertyValue{},
	}
	values := make([]GraphPropertyValue, 0, len(counts))
	for value, c := range counts {
		if value == "" {
			// The empty bucket is "nothing says", not a value anybody could
			// filter on. Listed separately so it cannot be read as one, and so
			// it does not spend a slot the cap could give to a real value.
			resp.AbsentNodes = c.Nodes
			resp.AbsentEdges = c.Edges
			continue
		}
		values = append(values, GraphPropertyValue{
			Value: value,
			Nodes: c.Nodes,
			Edges: c.Edges,
			Total: c.Nodes + c.Edges,
		})
		resp.Nodes += c.Nodes
		resp.Edges += c.Edges
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Total != values[j].Total {
			return values[i].Total > values[j].Total
		}
		return values[i].Value < values[j].Value
	})
	resp.DistinctValues = len(values)
	if req.Limit > 0 && len(values) > req.Limit {
		values = values[:req.Limit]
		resp.Truncated = true
	}
	resp.Values = append(resp.Values, values...)
	resp.Text = renderGraphPropertyValues(resp)
	return resp, nil
}

// renderGraphPropertyValues writes the distribution as lines a query can be
// written from.
func renderGraphPropertyValues(resp *GraphPropertyValuesResponse) string {
	var b strings.Builder
	quoted := strconv.Quote(resp.Key)

	if resp.DistinctValues == 0 {
		b.WriteString("No record in the graph carries a property named " + quoted + " with a value.\n")
		if resp.AbsentNodes > 0 || resp.AbsentEdges > 0 {
			b.WriteString(plural(resp.AbsentNodes, "node", "nodes") + " and " +
				plural(resp.AbsentEdges, "edge", "edges") + " were checked. " +
				"Call graph_schema to see which property keys do exist.\n")
		}
		return b.String()
	}

	b.WriteString("Values of property " + quoted + " as they are actually stored: " +
		plural(resp.DistinctValues, "distinct value", "distinct values") + " on " +
		plural(resp.Nodes, "node", "nodes") + " and " +
		plural(resp.Edges, "edge", "edges") + ".\n")
	for _, v := range resp.Values {
		var carriers []string
		if v.Nodes > 0 {
			carriers = append(carriers, plural(v.Nodes, "node", "nodes"))
		}
		if v.Edges > 0 {
			carriers = append(carriers, plural(v.Edges, "edge", "edges"))
		}
		b.WriteString("  " + v.Value + " — " + strings.Join(carriers, ", ") + "\n")
	}
	if resp.Truncated {
		b.WriteString("  ... " + strconv.Itoa(resp.DistinctValues-len(resp.Values)) +
			" more values not shown; raise limit to see the tail.\n")
	}
	if resp.AbsentNodes > 0 || resp.AbsentEdges > 0 {
		b.WriteString(plural(resp.AbsentNodes, "node", "nodes") + " and " +
			plural(resp.AbsentEdges, "edge", "edges") + " do not carry " + quoted +
			" at all (or carry it empty), so a filter on it excludes them.\n")
	}
	b.WriteString("Match these strings exactly — they are what a filter or a SPARQL literal has to equal. " +
		"A value that looks like a code is a code; do not substitute the word you would have expected.\n")
	return b.String()
}

// plural renders a count with the right noun, because "1 node types" in an
// answer meant to be read as prose undermines the rest of it.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// GraphSchema describes the graph's observed shape for a tool caller.
//
// The only difference from the facade is the caps: a tool caller that names
// none gets the defaults above, because this answer goes into a context window
// and an uncapped default there is one badly chosen call away from filling it.
func (t *GraphRAGToolbox) GraphSchema(ctx context.Context, req GraphSchemaRequest) (*GraphSchemaResponse, error) {
	if req.MaxNodeTypes <= 0 {
		req.MaxNodeTypes = defaultGraphSchemaNodeTypes
	}
	if req.MaxEdgeTypes <= 0 {
		req.MaxEdgeTypes = defaultGraphSchemaEdgeTypes
	}
	if req.MaxEdgeShapes <= 0 {
		req.MaxEdgeShapes = defaultGraphSchemaEdgeShapes
	}
	if req.MaxPropertyKeys <= 0 {
		req.MaxPropertyKeys = defaultGraphSchemaPropertyKeys
	}
	return t.db.GraphSchema(ctx, req)
}

// GraphPropertyValues lists one property key's values for a tool caller.
func (t *GraphRAGToolbox) GraphPropertyValues(ctx context.Context, req GraphPropertyValuesRequest) (*GraphPropertyValuesResponse, error) {
	if req.Limit <= 0 {
		req.Limit = defaultGraphPropertyValueLimit
	}
	return t.db.GraphPropertyValues(ctx, req)
}
