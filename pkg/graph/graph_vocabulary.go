package graph

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// What the graph actually contains, by type.
//
// These answer the questions an ontology or drift check asks: which types are
// really in the graph, and do the edges run between the ends the vocabulary
// declared. Until now the library offered counts of everything and nothing in
// between — GetGraphStatistics reports how many nodes there are, never what
// kind — so callers that needed the breakdown wrote SQL against graph_nodes
// and graph_edges themselves.
//
// That is the thing worth removing. A caller spelling out its own three-table
// JOIN is pinned to this schema and to one database's dialect, and neither is
// promised to it. When the graph layer learned to run on PostgreSQL as well as
// SQLite, nothing in such a caller would have failed loudly: the query still
// parses, still returns rows, and simply stops describing the graph the moment
// the two drift apart. Everything below goes through the same dialect-aware
// path as the rest of the package, so a caller using it moves when the library
// moves.

// EdgeShape is one (edge type, from-node type, to-node type) combination
// present in the graph, and how many edges have it.
type EdgeShape struct {
	EdgeType string `json:"edge_type"`
	FromType string `json:"from_type"`
	ToType   string `json:"to_type"`
	Count    int    `json:"count"`
}

// EdgeEndpoint identifies one end of an edge, with enough about the node to
// name it in a report without a second round trip.
type EdgeEndpoint struct {
	ID       string `json:"id"`
	NodeType string `json:"node_type"`
	Content  string `json:"content"`
}

// EdgeEndpointPair is one (edge type, from node, to node) combination and how
// many edges join exactly those two nodes with that type.
type EdgeEndpointPair struct {
	EdgeType string       `json:"edge_type"`
	From     EdgeEndpoint `json:"from"`
	To       EdgeEndpoint `json:"to"`
	Count    int          `json:"count"`
}

// NodeTypeCounts returns how many nodes carry each node_type.
//
// Untyped nodes are counted under the empty string rather than dropped. A
// caller that sums these and compares against GetGraphStatistics should get
// the same number, and "there are 400 nodes nobody typed" is a finding in its
// own right — silently omitting them turns it into a discrepancy the caller
// has to explain.
func (g *GraphStore) NodeTypeCounts(ctx context.Context) (map[string]int, error) {
	return g.typeCounts(ctx, `
		SELECT COALESCE(node_type, ''), COUNT(*)
		  FROM graph_nodes
		 GROUP BY COALESCE(node_type, '')
		 ORDER BY COALESCE(node_type, '')`)
}

// EdgeTypeCounts returns how many edges carry each edge_type, with untyped
// edges under the empty string for the same reason as NodeTypeCounts.
func (g *GraphStore) EdgeTypeCounts(ctx context.Context) (map[string]int, error) {
	return g.typeCounts(ctx, `
		SELECT COALESCE(edge_type, ''), COUNT(*)
		  FROM graph_edges
		 GROUP BY COALESCE(edge_type, '')
		 ORDER BY COALESCE(edge_type, '')`)
}

// typeCounts runs a two-column "value, count" query into a map.
func (g *GraphStore) typeCounts(ctx context.Context, query string) (map[string]int, error) {
	rows, err := g.query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("count types: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]int)
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			return nil, fmt.Errorf("count types: %w", err)
		}
		out[name] = n
	}
	return out, rows.Err()
}

// EdgeShapes reports the type shapes the graph's edges actually have.
//
// This is what makes a declared relation checkable. An extracted `backs`
// asserted from the resource to the pool is stored without complaint, reads as
// a fact, and is walked as one; nothing in the answer it produces says the
// arrow points the wrong way. Comparing these shapes against the declared ends
// is the only thing that can disagree with it.
//
// Passing edge types narrows the scan to those; passing none reports every
// shape in the graph. They are matched exactly, as stored — the same rule the
// traversal filters use, since the type in the graph is whatever the extracting
// model emitted and this package nowhere decides what a near-miss means.
//
// Edges whose endpoints are missing are not reported: both endpoints are
// declared foreign keys, so on a store this package opened they cannot exist.
func (g *GraphStore) EdgeShapes(ctx context.Context, edgeTypes ...string) ([]EdgeShape, error) {
	where, args := typeFilter("e.edge_type", edgeTypes)
	rows, err := g.query(ctx, `
		SELECT COALESCE(e.edge_type, ''),
		       COALESCE(f.node_type, ''),
		       COALESCE(t.node_type, ''),
		       COUNT(*)
		  FROM graph_edges e
		  JOIN graph_nodes f ON f.id = e.from_node_id
		  JOIN graph_nodes t ON t.id = e.to_node_id`+where+`
		 GROUP BY COALESCE(e.edge_type, ''), COALESCE(f.node_type, ''), COALESCE(t.node_type, '')
		 ORDER BY COALESCE(e.edge_type, ''), COALESCE(f.node_type, ''), COALESCE(t.node_type, '')`, args...)
	if err != nil {
		return nil, fmt.Errorf("edge shapes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []EdgeShape
	for rows.Next() {
		var s EdgeShape
		if err := rows.Scan(&s.EdgeType, &s.FromType, &s.ToType, &s.Count); err != nil {
			return nil, fmt.Errorf("edge shapes: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// EdgeEndpointPairs reports which nodes the edges of each type actually run
// between.
//
// EdgeShapes groups by type, which answers "how is this relation wrong" and
// not "what is wrong". On a live base eighteen non-conforming edges came from
// thirteen nodes, two of which carried six between them — six edges, two
// places to look. Getting from one answer to the other needs the nodes, and
// getting the nodes needs their identity, which is why this returns content
// and type alongside the id rather than a list of ids to look up afterwards.
//
// Ordering, filtering, and the treatment of missing endpoints are the same as
// EdgeShapes. This scans one row per distinct (type, from, to) triple, so on a
// large graph pass the edge types the caller actually cares about.
func (g *GraphStore) EdgeEndpointPairs(ctx context.Context, edgeTypes ...string) ([]EdgeEndpointPair, error) {
	where, args := typeFilter("e.edge_type", edgeTypes)
	rows, err := g.query(ctx, `
		SELECT COALESCE(e.edge_type, ''),
		       f.id, COALESCE(f.node_type, ''), COALESCE(f.content, ''),
		       t.id, COALESCE(t.node_type, ''), COALESCE(t.content, ''),
		       COUNT(*)
		  FROM graph_edges e
		  JOIN graph_nodes f ON f.id = e.from_node_id
		  JOIN graph_nodes t ON t.id = e.to_node_id`+where+`
		 GROUP BY COALESCE(e.edge_type, ''), f.id, COALESCE(f.node_type, ''), COALESCE(f.content, ''),
		          t.id, COALESCE(t.node_type, ''), COALESCE(t.content, '')
		 ORDER BY COALESCE(e.edge_type, ''), f.id, t.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("edge endpoint pairs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []EdgeEndpointPair
	for rows.Next() {
		var p EdgeEndpointPair
		if err := rows.Scan(&p.EdgeType,
			&p.From.ID, &p.From.NodeType, &p.From.Content,
			&p.To.ID, &p.To.NodeType, &p.To.Content,
			&p.Count); err != nil {
			return nil, fmt.Errorf("edge endpoint pairs: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// typeFilter builds an optional IN clause and its arguments.
//
// Over any column, not only an edge's type: the node-side reads below narrow
// the same way, and two copies of one IN clause is how the two would come to
// disagree about the empty case.
//
// The placeholders are written as ? and left for the dialect to rebind, like
// every other query in this package: a hand-numbered $1 would work on
// PostgreSQL and break on SQLite, which is the mistake this indirection exists
// to make impossible.
func typeFilter(column string, types []string) (string, []any) {
	if len(types) == 0 {
		return "", nil
	}
	args := make([]any, 0, len(types))
	for _, t := range types {
		args = append(args, t)
	}
	return " WHERE " + column + " IN (" + strings.TrimSuffix(strings.Repeat("?,", len(args)), ",") + ")", args
}

// Connectivity is how much of the graph an edge can reach.
type Connectivity struct {
	Nodes   int `json:"nodes"`
	Orphans int `json:"orphans"`
}

// Connectivity counts the nodes no edge touches, alongside the total.
//
// A large share of orphans means writes landed and their edges did not — the
// state a store reaches when an ingest's edges are rejected, or when entities
// accumulate across re-ingests with nothing joining them. Retrieval still finds
// these nodes and expansion never leaves them, so the graph half of GraphRAG
// quietly stops contributing while every count still grows.
//
// Both numbers come from one statement so they describe one graph. Asked
// separately, a write between them yields a share that was never true — and
// this is a health check, whose whole output is that ratio.
//
// Distinct from GraphStatistics.ConnectedComponents, which asks how the
// reachable part is divided; this asks how much of the graph is reachable at
// all.
func (g *GraphStore) Connectivity(ctx context.Context) (Connectivity, error) {
	// The edge source is named inside the SELECT list and the node source in
	// the FROM, so for an as-of read the arguments go in that textual order —
	// the correlated subquery's first.
	nodeSrc, nodeArgs := g.nodeSource(ctx)
	edgeSrc, edgeArgs := g.edgeSource(ctx)
	var c Connectivity
	err := g.queryRow(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN NOT EXISTS (
		           SELECT 1 FROM `+edgeSrc+` AS e
		            WHERE e.from_node_id = n.id OR e.to_node_id = n.id
		       ) THEN 1 ELSE 0 END), 0)
		  FROM `+nodeSrc+` AS n`, append(edgeArgs, nodeArgs...)...).Scan(&c.Nodes, &c.Orphans)
	if err != nil {
		return Connectivity{}, fmt.Errorf("graph connectivity: %w", err)
	}
	return c, nil
}

// NodeLabel is a node's identity together with what it is called.
type NodeLabel struct {
	ID       string `json:"id"`
	NodeType string `json:"node_type"`
	Content  string `json:"content"`
}

// NodeLabelQuery narrows NodeLabels. A zero query returns every node.
type NodeLabelQuery struct {
	// IDPrefix keeps only nodes whose id starts with it. Node ids are
	// namespaced by what wrote them, so this is how a caller asks for one
	// writer's nodes without knowing how to recognise them from content.
	// Matched literally: % and _ in the prefix are not wildcards.
	IDPrefix string
	// MinContentLength drops nodes whose label is shorter than this, counted
	// in characters. 1 excludes the unlabelled.
	MinContentLength int
	// Limit caps the rows returned. 0 means no cap.
	Limit int
}

// NodeLabels lists nodes with their type and label.
//
// The question behind it is "what is in here, called what" — which spellings a
// vocabulary actually produced, whether one concept arrived under two names,
// which labels are long enough to match text against. Callers were reading
// graph_nodes for it directly, and pairing a schema they are not promised with
// an id convention they are not promised either.
//
// Ordered by id so two runs over one graph agree, and so a caller paging with
// Limit sees a stable sequence rather than whichever rows the planner returned
// first.
func (g *GraphStore) NodeLabels(ctx context.Context, q NodeLabelQuery) ([]NodeLabel, error) {
	var where []string
	var args []any
	if q.IDPrefix != "" {
		// ESCAPE is spelled out because a prefix is caller data: an id
		// convention containing _ — which is a single-character wildcard —
		// would otherwise match ids that merely resemble it.
		where = append(where, `id LIKE ? ESCAPE '\'`)
		args = append(args, escapeLikePrefix(q.IDPrefix)+"%")
	}
	if q.MinContentLength > 0 {
		where = append(where, `LENGTH(COALESCE(content, '')) >= ?`)
		args = append(args, q.MinContentLength)
	}
	src, srcArgs := g.nodeSource(ctx)
	args = append(srcArgs, args...)
	sqlText := `SELECT id, COALESCE(node_type, ''), COALESCE(content, '') FROM ` + src + ` AS n`
	if len(where) > 0 {
		sqlText += " WHERE " + strings.Join(where, " AND ")
	}
	sqlText += " ORDER BY id"
	if q.Limit > 0 {
		sqlText += " LIMIT ?"
		args = append(args, q.Limit)
	}

	rows, err := g.query(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("node labels: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []NodeLabel
	for rows.Next() {
		var l NodeLabel
		if err := rows.Scan(&l.ID, &l.NodeType, &l.Content); err != nil {
			return nil, fmt.Errorf("node labels: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// escapeLikePrefix makes a literal string safe to use as a LIKE prefix under
// ESCAPE '\'. The backslash goes first: escaping it afterwards would escape the
// backslashes this function just introduced.
func escapeLikePrefix(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

// PropertyKeyUsage is one property key as it appears on the nodes of one type:
// how many of them carry it, and how many distinct values they carry.
//
// Distinct is the field that earns the type. Coverage alone says a key is
// present; only distinctness separates a key that identifies a record from one
// that classifies it, which on a store of 4,000 nodes is the difference between
// `name` and `type` and is the only evidence data can offer about identity at
// all.
type PropertyKeyUsage struct {
	NodeType string `json:"node_type"`
	Key      string `json:"key"`
	Records  int    `json:"records"`
	Distinct int    `json:"distinct_values"`
}

// NodePropertyKeys reports which property keys the nodes of each type carry.
//
// Every other property query in this file starts from a key the caller already
// knows. This one starts from nothing, because that is where anything deriving
// a shape from stored data has to start: what the records say about themselves,
// before anybody has written down what they were supposed to say. A caller
// wanting it had to enumerate JSON keys itself — which is json_each on SQLite
// and a lateral jsonb_each_text on PostgreSQL, so it was also a caller pinned
// to one database.
//
// Passing node types narrows the scan to those; passing none reports every
// type in the graph. They are matched exactly, as stored, and untyped nodes
// come back under the empty string — the same two rules NodeTypeCounts and
// EdgeShapes follow, and for the same reasons.
//
// Domain-neutral, like the rest of this section: it knows that properties is a
// JSON object of string fields, and nothing about what any key means or what
// anybody intends to do with the answer.
//
// One row per (type, key) pair, so the cost is a scan of graph_nodes and its
// properties rather than a scan per type. Ordered by type then key so two
// reads of one graph agree.
func (g *GraphStore) NodePropertyKeys(ctx context.Context, nodeTypes ...string) ([]PropertyKeyUsage, error) {
	const nodeType = "COALESCE(n.node_type, '')"
	where, args := typeFilter(nodeType, nodeTypes)
	rows, err := g.query(ctx, `
		SELECT `+nodeType+`, je.key, COUNT(*), COUNT(DISTINCT je.value)
		  FROM graph_nodes n`+g.dialect.JSONEachEntry("n.properties")+where+`
		 GROUP BY `+nodeType+`, je.key
		 ORDER BY `+nodeType+`, je.key`, args...)
	if err != nil {
		return nil, fmt.Errorf("node property keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []PropertyKeyUsage
	for rows.Next() {
		var u PropertyKeyUsage
		if err := rows.Scan(&u.NodeType, &u.Key, &u.Records, &u.Distinct); err != nil {
			return nil, fmt.Errorf("node property keys: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Grouping and listing the graph by what is written on it, rather than by
// type.
//
// NodeTypeCounts above answers "what kinds of thing are in here". These answer
// "what do the records say about themselves" — the question a caller asks when
// the interesting fact is not the node's type but a field its writer stamped
// on: which import a record came from, which model proposed it, how well
// established it is. GraphFilter.Properties could already narrow a search to
// one such value; nothing could count them, and nothing could see edges at
// all, so a caller wanting the breakdown wrote SQL over both tables itself —
// with the schema and dialect exposure the block comment above this section
// spends its length on.
//
// Domain-neutral on purpose: this layer knows that properties is a JSON object
// with string fields, and nothing about what any particular key means.

// PropertyCount is how many nodes and how many edges carry one value.
//
// Kept apart rather than summed because the two are not interchangeable to a
// reader: an edge is an assertion about two things and a node is one thing, so
// "40 records" over a graph of 4 nodes and 36 edges describes a different
// graph than the reverse, and a caller that wants the total can add them.
type PropertyCount struct {
	Nodes int `json:"nodes"`
	Edges int `json:"edges"`
}

// PropertyCounts groups every node and edge by one top-level property.
//
// Records that do not carry the property at all are counted under the empty
// string, for the reason NodeTypeCounts gives about untyped nodes: "nothing
// says" is a finding, and dropping it turns a caller's sum into a discrepancy
// it has to go and explain. It is usually the most important number in the
// result — a breakdown over the 3% of a shelf that was stamped looks exactly
// like a breakdown over all of it.
func (g *GraphStore) PropertyCounts(ctx context.Context, key string) (map[string]PropertyCount, error) {
	if key == "" {
		return nil, fmt.Errorf("property counts: no key")
	}
	out := map[string]PropertyCount{}
	for _, t := range []struct {
		table string
		edge  bool
	}{{"graph_nodes", false}, {"graph_edges", true}} {
		// COALESCE around the guarded read, not inside it: the guard yields
		// NULL both for a record with no properties at all and for one whose
		// JSON lacks this key, and those are the same answer to this question.
		expr := "COALESCE(" + g.dialect.JSONTextGuarded("properties", key) + ", '')"
		rows, err := g.query(ctx, `
			SELECT `+expr+`, COUNT(*)
			  FROM `+t.table+`
			 GROUP BY `+expr+`
			 ORDER BY `+expr)
		if err != nil {
			return nil, fmt.Errorf("property counts over %s: %w", t.table, err)
		}
		for rows.Next() {
			var value string
			var n int
			if err := rows.Scan(&value, &n); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("property counts over %s: %w", t.table, err)
			}
			c := out[value]
			if t.edge {
				c.Edges += n
			} else {
				c.Nodes += n
			}
			out[value] = c
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, fmt.Errorf("property counts over %s: %w", t.table, err)
		}
	}
	return out, nil
}

// PropertyRecord is one node or edge, with the properties the caller asked for.
type PropertyRecord struct {
	ID string `json:"id"`
	// Edge distinguishes the two without a caller having to guess from the
	// shape of the id.
	Edge bool `json:"edge"`
	// Type is node_type or edge_type.
	Type string `json:"type"`
	// Content is a node's label. Edges have none — they are named by their
	// type and their ends.
	Content string `json:"content,omitempty"`
	// From and To are an edge's ends, empty on a node. Ids rather than labels:
	// resolving them costs a second query per row, and a caller that wants the
	// labels has GetNodesBatch.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Properties holds exactly the keys asked for. A key the record does not
	// carry is absent rather than empty, so "nothing says" stays
	// distinguishable from "says the empty string".
	Properties map[string]string `json:"properties,omitempty"`
}

// PropertyRecordQuery narrows RecordsWithProperties. A zero query is refused
// rather than returning the whole graph: an unfiltered read of both tables is
// never what a caller of this API meant, and returning it makes the mistake
// look like it worked until the graph is large.
type PropertyRecordQuery struct {
	// Where narrows to records carrying these properties. Keys are ANDed and
	// the values within one key are ORed, which is the shape the questions
	// actually take: "held or refused, from this import".
	Where map[string][]string
	// Fetch names the properties to return on each record. Empty returns the
	// keys in Where, which is the common case and saves repeating them.
	Fetch []string
	// Limit caps the rows. 0 means no cap.
	Limit int
}

// RecordsWithProperties lists the nodes and edges matching a property query.
//
// Ordered by id within each table and nodes before edges, so two reads of one
// graph agree and a caller paging with Limit sees a stable sequence. Limit is
// applied to each table and then to the merged result: a cap that returned
// only nodes because they sorted first would hide every matching edge, which
// on a shelf whose assertions are mostly edges is the whole answer.
func (g *GraphStore) RecordsWithProperties(ctx context.Context, q PropertyRecordQuery) ([]PropertyRecord, error) {
	if len(q.Where) == 0 {
		return nil, fmt.Errorf("records with properties: no filter — refusing to read the whole graph")
	}
	fetch := q.Fetch
	if len(fetch) == 0 {
		for k := range q.Where {
			fetch = append(fetch, k)
		}
	}
	sort.Strings(fetch)

	where, args := g.propertyWhere(q.Where)
	var out []PropertyRecord
	for _, t := range []struct {
		table string
		edge  bool
	}{{"graph_nodes", false}, {"graph_edges", true}} {
		cols := []string{"id"}
		if t.edge {
			cols = append(cols, "COALESCE(edge_type, '')", "''", "from_node_id", "to_node_id")
		} else {
			cols = append(cols, "COALESCE(node_type, '')", "COALESCE(content, '')", "''", "''")
		}
		for _, k := range fetch {
			cols = append(cols, g.dialect.JSONTextGuarded("properties", k))
		}
		text := "SELECT " + strings.Join(cols, ", ") + " FROM " + t.table +
			" WHERE " + where + " ORDER BY id"
		a := append([]any{}, args...)
		if q.Limit > 0 {
			text += " LIMIT ?"
			a = append(a, q.Limit)
		}
		rows, err := g.query(ctx, text, a...)
		if err != nil {
			return nil, fmt.Errorf("records with properties over %s: %w", t.table, err)
		}
		for rows.Next() {
			rec := PropertyRecord{Edge: t.edge}
			// Nullable because the guarded read yields NULL for a record
			// missing the key, which must stay different from the empty
			// string it would otherwise arrive as.
			vals := make([]sql.NullString, len(fetch))
			dest := []any{&rec.ID, &rec.Type, &rec.Content, &rec.From, &rec.To}
			for i := range vals {
				dest = append(dest, &vals[i])
			}
			if err := rows.Scan(dest...); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("records with properties over %s: %w", t.table, err)
			}
			for i, k := range fetch {
				if vals[i].Valid {
					if rec.Properties == nil {
						rec.Properties = map[string]string{}
					}
					rec.Properties[k] = vals[i].String
				}
			}
			out = append(out, rec)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, fmt.Errorf("records with properties over %s: %w", t.table, err)
		}
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

// propertyWhere renders the ANDed-keys, ORed-values filter.
//
// Keys are sorted so one query renders one SQL string, which is what lets a
// prepared-statement cache do its job and what makes a failing query
// reproducible from a log.
func (g *GraphStore) propertyWhere(w map[string][]string) (string, []any) {
	keys := make([]string, 0, len(w))
	for k := range w {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var clauses []string
	var args []any
	for _, k := range keys {
		expr := g.dialect.JSONTextGuarded("properties", k)
		var ors []string
		for _, v := range w[k] {
			if v == "" {
				// The empty string is how PropertyCounts reports "does not
				// carry this key", so accept it here as the same question:
				// show me the records nothing was stamped on.
				ors = append(ors, "("+expr+" IS NULL OR "+expr+" = ?)")
				args = append(args, "")
				continue
			}
			ors = append(ors, expr+" = ?")
			args = append(args, v)
		}
		if len(ors) == 0 {
			continue
		}
		clauses = append(clauses, "("+strings.Join(ors, " OR ")+")")
	}
	if len(clauses) == 0 {
		return "1=0", nil
	}
	return strings.Join(clauses, " AND "), args
}
