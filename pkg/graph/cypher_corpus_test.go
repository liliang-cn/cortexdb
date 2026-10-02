package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph/cypher"
)

// The Cypher corpus: a hand-built graph shaped like the real brain, and over
// a hundred queries whose answers were worked out by hand from the fixture
// below — not by running the engine and pasting what it said. Every query
// runs on SQLite and, when CORTEXDB_TEST_POSTGRES is set, on PostgreSQL, and
// both must give exactly the listed answer. Queries outside the subset must
// fail with the listed error kind; a wrong answer is never acceptable.

type fixtureNode struct {
	id, typ, content string
	props            map[string]any
}

type fixtureEdge struct {
	id, from, to, typ string
	weight            float64
	props             map[string]any
}

func cypherFixture() ([]fixtureNode, []fixtureEdge) {
	nodes := []fixtureNode{
		{"p:cortexdb", "project", "", map[string]any{"name": "CortexDB", "lang": "Go", "stars": 120, "layer": "storage"}},
		{"p:athanor", "project", "", map[string]any{"name": "Athanor", "lang": "Go", "stars": 40}},
		{"p:steward", "project", "", map[string]any{"name": "steward", "lang": "Go", "stars": 15}},
		{"p:aigui", "project", "", map[string]any{"name": "aigui", "lang": "TypeScript", "stars": 300}},
		{"p:harness", "project", "", map[string]any{"name": "harness-rs", "lang": "Rust", "stars": 8.5}},
		{"lib:sqlite", "software", "", map[string]any{"name": "SQLite", "license": "public domain"}},
		{"lib:pgvector", "software", "", map[string]any{"name": "pgvector"}},
		{"h:node-e", "host", "", map[string]any{"name": "node-e", "ip": "192.168.123.252", "cores": 8}},
		{"h:node-a", "host", "", map[string]any{"name": "node-a", "cores": 4}},
		{"h:apps", "host", "", map[string]any{"title": "apps VM"}},
		{"s:grpc", "service", "", map[string]any{"name": "cortexdb-grpc", "port": 47821, "tags": []any{"grpc", "brain"}}},
		{"s:sds", "service", "", map[string]any{"name": "sds-ai", "port": 8090, "enabled": true}},
		{"t:browser", "tool", "", map[string]any{"name": "agent-browser", "enabled": false}},
		{"m:1", "memory", "CortexDB runs on node-e", nil},
		{"x:loop", "entity", "", map[string]any{"name": "loop"}},
		{"x:orphan", "", "", map[string]any{"name": "orphan"}},
		{"k:1", "ring", "", map[string]any{"name": "k1"}},
		{"k:2", "ring", "", map[string]any{"name": "k2"}},
		{"k:3", "ring", "", map[string]any{"name": "k3"}},
	}
	for i := 1; i <= 8; i++ {
		nodes = append(nodes, fixtureNode{fmt.Sprintf("c:%d", i), "step", "", map[string]any{"name": fmt.Sprintf("c%d", i), "i": i}})
	}
	edges := []fixtureEdge{
		{"d1", "p:athanor", "p:cortexdb", "depends_on", 0, nil},
		{"d2", "p:steward", "p:cortexdb", "depends_on", 0, nil},
		{"d3", "p:steward", "p:athanor", "depends_on", 0, nil},
		{"d4", "p:aigui", "p:harness", "depends_on", 0, nil},
		{"u1", "p:cortexdb", "lib:sqlite", "uses", 0.9, map[string]any{"since": "2024"}},
		{"u2", "p:cortexdb", "lib:pgvector", "uses", 0, nil},
		{"u3", "p:athanor", "lib:sqlite", "uses", 0, nil},
		{"r1", "s:grpc", "h:node-e", "runs_on", 0, nil},
		{"r2", "s:sds", "h:node-e", "runs_on", 0, nil},
		{"r3", "t:browser", "h:node-e", "deployed_on", 0, nil},
		{"r4", "p:athanor", "h:apps", "deployed_on", 0, nil},
		{"po1", "s:grpc", "p:cortexdb", "part_of", 0, nil},
		{"mn1", "m:1", "p:cortexdb", "mentions", 0, nil},
		{"mn2", "m:1", "h:node-e", "mentions", 0, nil},
		{"lp", "x:loop", "x:loop", "relates", 0, nil},
		{"mt1", "p:steward", "p:cortexdb", "mentions", 0, nil},
		{"mt2", "p:steward", "p:cortexdb", "mentions", 0, nil},
		// Temporal facts: one ended, one not yet begun, one current.
		{"old", "p:athanor", "h:node-a", "runs_on", 0, map[string]any{"valid_from": "2020-01-01T00:00:00Z", "valid_to": "2021-01-01T00:00:00Z"}},
		{"fut", "s:sds", "h:node-a", "runs_on", 0, map[string]any{"valid_from": "2999-01-01T00:00:00Z"}},
		{"cur", "p:aigui", "h:node-a", "runs_on", 0, map[string]any{"valid_from": "2020-01-01T00:00:00Z"}},
		{"g1", "k:1", "k:2", "cyc", 0, nil},
		{"g2", "k:2", "k:3", "cyc", 0, nil},
		{"g3", "k:3", "k:1", "cyc", 0, nil},
	}
	for i := 1; i <= 7; i++ {
		edges = append(edges, fixtureEdge{fmt.Sprintf("n%d", i), fmt.Sprintf("c:%d", i), fmt.Sprintf("c:%d", i+1), "next", 0, nil})
	}
	return nodes, edges
}

func loadCypherFixture(t testing.TB, g *GraphStore) {
	t.Helper()
	ctx := context.Background()
	if err := g.InitGraphSchema(ctx); err != nil {
		t.Fatal(err)
	}
	nodes, edges := cypherFixture()
	for _, n := range nodes {
		if err := g.UpsertNode(ctx, &GraphNode{ID: n.id, NodeType: n.typ, Content: n.content, Properties: n.props, Vector: []float32{1, 0, 0, 0}}); err != nil {
			t.Fatalf("node %s: %v", n.id, err)
		}
	}
	for _, e := range edges {
		if err := g.UpsertEdge(ctx, &GraphEdge{ID: e.id, FromNodeID: e.from, ToNodeID: e.to, EdgeType: e.typ, Weight: e.weight, Properties: e.props}); err != nil {
			t.Fatalf("edge %s: %v", e.id, err)
		}
	}
}

type corpusCase struct {
	q       string
	params  map[string]any
	ordered bool
	want    [][]any
	// errKind, when set, is the error the query must fail with.
	errKind cypher.ErrorKind
	// includeEnded runs with IncludeEndedFacts.
	includeEnded bool
}

// r builds one expected row.
func r(vals ...any) []any { return vals }

// l builds an expected list value.
func l(vals ...any) []any {
	if vals == nil {
		return []any{}
	}
	return vals
}

func cypherCorpus() []corpusCase {
	var I = func(v int64) int64 { return v }
	return []corpusCase{
		// --- basic MATCH, labels, inline maps, direction
		{q: "MATCH (p:project)-[:depends_on]->(c {name:'CortexDB'}) RETURN p.name ORDER BY p.name", ordered: true, want: [][]any{r("Athanor"), r("steward")}},
		{q: "MATCH (p:project) RETURN p.name ORDER BY p.name", ordered: true, want: [][]any{r("Athanor"), r("CortexDB"), r("aigui"), r("harness-rs"), r("steward")}},
		{q: "MATCH (p:project) WHERE p.stars > 30 RETURN p.name ORDER BY p.stars DESC", ordered: true, want: [][]any{r("aigui"), r("CortexDB"), r("Athanor")}},
		{q: "MATCH (p:project) WHERE p.lang = 'Go' AND p.stars >= 40 RETURN p.name ORDER BY p.name", ordered: true, want: [][]any{r("Athanor"), r("CortexDB")}},
		{q: "MATCH (p:project) WHERE p.lang = 'Go' OR p.lang = 'Rust' RETURN count(*)", want: [][]any{r(I(4))}},
		{q: "MATCH (p:project) WHERE NOT p.lang = 'Go' RETURN p.name ORDER BY p.name", ordered: true, want: [][]any{r("aigui"), r("harness-rs")}},
		{q: "MATCH (n) WHERE n.license IS NOT NULL RETURN n.name", want: [][]any{r("SQLite")}},
		{q: "MATCH (n:host) WHERE n.cores IS NULL RETURN n.name", want: [][]any{r("apps VM")}},
		{q: "MATCH (p:project) WHERE p.lang IN ['Rust', 'TypeScript'] RETURN p.name ORDER BY p.name", ordered: true, want: [][]any{r("aigui"), r("harness-rs")}},
		{q: "MATCH (n) WHERE n.name STARTS WITH 'c' RETURN n.name ORDER BY n.name", ordered: true, want: [][]any{r("c1"), r("c2"), r("c3"), r("c4"), r("c5"), r("c6"), r("c7"), r("c8"), r("cortexdb-grpc")}},
		{q: "MATCH (n) WHERE n.name ENDS WITH '-ai' RETURN n.name", want: [][]any{r("sds-ai")}},
		{q: "MATCH (n) WHERE n.name CONTAINS 'grpc' RETURN n.name", want: [][]any{r("cortexdb-grpc")}},
		{q: "MATCH (n:service) WHERE n.name =~ 'sds-.*' RETURN n.name", want: [][]any{r("sds-ai")}},
		{q: "MATCH (n) WHERE n.name =~ '(?i)SQL.*' RETURN n.name", want: [][]any{r("SQLite")}},
		{q: "MATCH (s)-[:runs_on]->(h:host {name:'node-e'}) RETURN s.name ORDER BY s.name", ordered: true, want: [][]any{r("cortexdb-grpc"), r("sds-ai")}},
		{q: "MATCH (s)-[r:runs_on|deployed_on]->(h {name:'node-e'}) RETURN s.name, type(r) ORDER BY s.name", ordered: true, want: [][]any{r("agent-browser", "deployed_on"), r("cortexdb-grpc", "runs_on"), r("sds-ai", "runs_on")}},
		{q: "MATCH (s)-[r:runs_on|:deployed_on]->(h {name:'node-e'}) RETURN count(r)", want: [][]any{r(I(3))}},
		{q: "MATCH (c {name:'CortexDB'})<-[:depends_on]-(p) RETURN p.name ORDER BY p.name", ordered: true, want: [][]any{r("Athanor"), r("steward")}},
		{q: "MATCH (c {name:'Athanor'})-[:depends_on]-(x) RETURN x.name ORDER BY x.name", ordered: true, want: [][]any{r("CortexDB"), r("steward")}},
		{q: "MATCH (a {name:'steward'})-[r:mentions]->(b) RETURN b.name, count(r)", want: [][]any{r("CortexDB", I(2))}},
		{q: "MATCH (a)-[r]->(b) WHERE a.name = 'CortexDB' RETURN type(r), b.name ORDER BY b.name", ordered: true, want: [][]any{r("uses", "SQLite"), r("uses", "pgvector")}},
		{q: "MATCH (a:project)-[:depends_on]->(b:project)-[:depends_on]->(c:project) RETURN a.name, b.name, c.name", want: [][]any{r("steward", "Athanor", "CortexDB")}},
		{q: "MATCH (a {name:'steward'})-[:depends_on]->(x), (x)-[:uses]->(l) RETURN x.name, l.name ORDER BY x.name, l.name", ordered: true, want: [][]any{r("Athanor", "SQLite"), r("CortexDB", "SQLite"), r("CortexDB", "pgvector")}},
		{q: "MATCH (a:host), (b:tool) RETURN count(*)", want: [][]any{r(I(3))}},
		{q: "MATCH (a:host) MATCH (b:tool) RETURN count(*)", want: [][]any{r(I(3))}},
		{q: "MATCH (x)-[:uses]->(l {name:'SQLite'})<-[:uses]-(y) RETURN x.name, y.name ORDER BY x.name", ordered: true, want: [][]any{r("Athanor", "CortexDB"), r("CortexDB", "Athanor")}},

		// --- OPTIONAL MATCH
		{q: "MATCH (p:project) OPTIONAL MATCH (p)-[:deployed_on]->(h) RETURN p.name, h.name ORDER BY p.name", ordered: true, want: [][]any{r("Athanor", "apps VM"), r("CortexDB", nil), r("aigui", nil), r("harness-rs", nil), r("steward", nil)}},
		{q: "MATCH (p:project) OPTIONAL MATCH (p)-[:uses]->(l) WHERE l.name = 'pgvector' RETURN p.name, l.name ORDER BY p.name", ordered: true, want: [][]any{r("Athanor", nil), r("CortexDB", "pgvector"), r("aigui", nil), r("harness-rs", nil), r("steward", nil)}},
		{q: "MATCH (p:project) OPTIONAL MATCH (p)<-[:depends_on]-(d) RETURN p.name, count(d) ORDER BY p.name", ordered: true, want: [][]any{r("Athanor", I(1)), r("CortexDB", I(2)), r("aigui", I(0)), r("harness-rs", I(1)), r("steward", I(0))}},
		{q: "OPTIONAL MATCH (n:Nope) RETURN n", want: [][]any{r(nil)}},
		{q: "MATCH (p:project {name:'aigui'}) OPTIONAL MATCH (p)-[:depends_on]->(x)-[:depends_on]->(y) RETURN x.name, y.name", want: [][]any{r(nil, nil)}},
		{q: "MATCH (p:project {name:'steward'}) OPTIONAL MATCH (p)-[:depends_on]->(x) OPTIONAL MATCH (x)-[:deployed_on]->(h) RETURN x.name, h.name ORDER BY x.name", ordered: true, want: [][]any{r("Athanor", "apps VM"), r("CortexDB", nil)}},
		{q: "MATCH (h:host) OPTIONAL MATCH (h)<-[r:runs_on]-(s) WITH h, collect(s.name) AS svcs RETURN h.name, size(svcs) ORDER BY h.name", ordered: true, want: [][]any{r("apps VM", I(0)), r("node-a", I(1)), r("node-e", I(2))}},

		// --- variable length
		{q: "MATCH (a {name:'steward'})-[:depends_on*1..2]->(b) RETURN b.name ORDER BY b.name", ordered: true, want: [][]any{r("Athanor"), r("CortexDB"), r("CortexDB")}},
		{q: "MATCH (a {name:'steward'})-[:depends_on*1..2]->(b) RETURN DISTINCT b.name ORDER BY b.name", ordered: true, want: [][]any{r("Athanor"), r("CortexDB")}},
		{q: "MATCH (a {name:'c1'})-[:next*3]->(b) RETURN b.name", want: [][]any{r("c4")}},
		{q: "MATCH (a {name:'c3'})-[:next*]->(b) RETURN count(*)", want: [][]any{r(I(5))}},
		{q: "MATCH (a {name:'c1'})-[:next*]->(b) RETURN count(*)", errKind: cypher.ErrBudget},
		{q: "MATCH (a {name:'c1'})-[:next*1..10]->(b) RETURN count(*)", errKind: cypher.ErrUnsupported},
		{q: "MATCH (a {name:'c1'})-[:next*0..2]->(b) RETURN b.name ORDER BY b.name", ordered: true, want: [][]any{r("c1"), r("c2"), r("c3")}},
		{q: "MATCH (a {name:'c1'})-[:next*..6]->(b) RETURN max(b.i)", want: [][]any{r(I(7))}},
		{q: "MATCH (b {name:'c8'})<-[:next*2..3]-(a) RETURN a.name ORDER BY a.name", ordered: true, want: [][]any{r("c5"), r("c6")}},
		{q: "MATCH (a)-[:next*6]->(b {name:'c8'}) RETURN a.name", want: [][]any{r("c2")}},
		{q: "MATCH (a {name:'k1'})-[:cyc*1..6]->(b) RETURN b.name, count(*) ORDER BY b.name", ordered: true, want: [][]any{r("k1", I(1)), r("k2", I(1)), r("k3", I(1))}},
		{q: "MATCH (a {name:'k1'})-[:cyc*2]-(b) RETURN b.name ORDER BY b.name", ordered: true, want: [][]any{r("k2"), r("k3")}},
		{q: "MATCH (a {name:'k1'})-[:cyc*4..5]->(b) RETURN count(*)", want: [][]any{r(I(0))}},
		{q: "MATCH (a {name:'c1'})-[r:next*2]->(b) RETURN [x IN r | id(x)]", want: [][]any{r(l("n1", "n2"))}},
		{q: "MATCH (a {name:'c1'})-[r:next*2]->(b), (b)-[s:next]->(c) RETURN c.name", want: [][]any{r("c4")}},
		{q: "MATCH (a {name:'k1'})-[r:cyc*1..3]->(b)-[s:cyc]->(c) RETURN b.name, c.name ORDER BY b.name", ordered: true, want: [][]any{r("k2", "k3"), r("k3", "k1")}},
		{q: "MATCH (a {name:'k1'})-[s:cyc]->(b)-[r:cyc*1..3]->(c) RETURN c.name ORDER BY c.name", ordered: true, want: [][]any{r("k1"), r("k3")}},
		{q: "MATCH (a {name:'k1'})-[:cyc*1..2]->(b)-[:cyc*1..2]->(c) RETURN count(*)", want: [][]any{r(I(3))}},

		// --- uniqueness, self-loops, undirected
		{q: "MATCH (a {name:'steward'})-[r1:mentions]->(c)<-[r2:mentions]-(a) RETURN count(*)", want: [][]any{r(I(2))}},
		{q: "MATCH (a)-[r:relates]->(a) RETURN a.name", want: [][]any{r("loop")}},
		{q: "MATCH (a {name:'loop'})-[r]-(b) RETURN count(*)", want: [][]any{r(I(1))}},
		{q: "MATCH ()-[r:depends_on]-() RETURN count(*)", want: [][]any{r(I(8))}},
		{q: "MATCH ()-[r:depends_on]->() RETURN count(*)", want: [][]any{r(I(4))}},

		// --- temporal validity
		{q: "MATCH (x)-[:runs_on]->(h {name:'node-a'}) RETURN x.name ORDER BY x.name", ordered: true, want: [][]any{r("aigui")}},
		{q: "MATCH (x)-[:runs_on]->(h {name:'node-a'}) RETURN x.name ORDER BY x.name", includeEnded: true, ordered: true, want: [][]any{r("Athanor"), r("aigui"), r("sds-ai")}},

		// --- aggregation
		{q: "MATCH (p:project) RETURN min(p.stars), max(p.stars), sum(p.stars), avg(p.stars)", want: [][]any{r(8.5, I(300), 483.5, 96.7)}},
		{q: "MATCH (p:project) RETURN p.lang AS lang, count(*) AS n ORDER BY n DESC, lang", ordered: true, want: [][]any{r("Go", I(3)), r("Rust", I(1)), r("TypeScript", I(1))}},
		{q: "MATCH (p:project {lang:'Go'}) RETURN collect(p.name) AS names", want: [][]any{r(l("Athanor", "CortexDB", "steward"))}},
		{q: "MATCH (s)-[:runs_on|deployed_on]->(h) RETURN collect(DISTINCT h.name) AS hosts", want: [][]any{r(l("node-a", "apps VM", "node-e"))}},
		{q: "MATCH (s)-[:runs_on]->(h) RETURN count(DISTINCT h)", want: [][]any{r(I(2))}},
		{q: "MATCH (h:host)<-[r]-(x) WITH h, count(r) AS deg WHERE deg >= 2 RETURN h.name, deg ORDER BY deg DESC", ordered: true, want: [][]any{r("node-e", I(4))}},
		{q: "MATCH (p:project) WITH p.lang AS lang, count(*) AS n WITH max(n) AS m RETURN m", want: [][]any{r(I(3))}},
		{q: "MATCH (h:host) RETURN count(h.cores), count(*)", want: [][]any{r(I(2), I(3))}},
		{q: "MATCH (n:Nope) RETURN count(*), collect(n.name), sum(n.x), avg(n.x), min(n.x)", want: [][]any{r(I(0), l(), I(0), nil, nil)}},
		{q: "MATCH (n:Nope) RETURN n.name, count(*)", want: [][]any{}},
		{q: "MATCH (p:project) RETURN size(collect(p)) AS n", want: [][]any{r(I(5))}},
		{q: "MATCH (p:project) RETURN p.lang, count(*) * 10 AS weighted ORDER BY p.lang", ordered: true, want: [][]any{r("Go", I(30)), r("Rust", I(10)), r("TypeScript", I(10))}},

		// --- WITH, ORDER BY, SKIP, LIMIT
		{q: "MATCH (c {name:'CortexDB'}) WITH c MATCH (c)<-[:depends_on]-(p) RETURN p.name ORDER BY p.name", ordered: true, want: [][]any{r("Athanor"), r("steward")}},
		{q: "MATCH (p:project) WITH p ORDER BY p.stars DESC LIMIT 2 RETURN p.name", ordered: true, want: [][]any{r("aigui"), r("CortexDB")}},
		{q: "MATCH (p:project) RETURN p.name ORDER BY p.name SKIP 1 LIMIT 2", ordered: true, want: [][]any{r("CortexDB"), r("aigui")}},
		{q: "MATCH (h:host) RETURN h.cores ORDER BY h.cores", ordered: true, want: [][]any{r(I(4)), r(I(8)), r(nil)}},
		{q: "MATCH (h:host) RETURN h.cores ORDER BY h.cores DESC", ordered: true, want: [][]any{r(nil), r(I(8)), r(I(4))}},
		{q: "MATCH (n:step) RETURN n.name ORDER BY n.i DESC LIMIT 2", ordered: true, want: [][]any{r("c8"), r("c7")}},
		{q: "MATCH (n:step) WITH n WHERE n.i % 2 = 0 RETURN n.name ORDER BY n.name", ordered: true, want: [][]any{r("c2"), r("c4"), r("c6"), r("c8")}},
		{q: "MATCH (n:step) WITH n.i AS i ORDER BY i DESC LIMIT 3 RETURN sum(i)", want: [][]any{r(I(21))}},

		// --- parameters
		{q: "MATCH (p:project) WHERE p.name = $name RETURN p.stars", params: map[string]any{"name": "CortexDB"}, want: [][]any{r(I(120))}},
		{q: "MATCH (p:project) WHERE p.name IN $names RETURN p.name ORDER BY p.name", params: map[string]any{"names": []any{"aigui", "steward", "nope"}}, ordered: true, want: [][]any{r("aigui"), r("steward")}},
		{q: "MATCH (n:step) RETURN n.name ORDER BY n.i LIMIT $k", params: map[string]any{"k": float64(3)}, ordered: true, want: [][]any{r("c1"), r("c2"), r("c3")}},
		{q: "MATCH (p:project {name: $n}) RETURN p.lang", params: map[string]any{"n": "aigui"}, want: [][]any{r("TypeScript")}},
		{q: "MATCH (s:service) WHERE s.port = $p RETURN s.name", params: map[string]any{"p": float64(8090)}, want: [][]any{r("sds-ai")}},

		// --- built-ins and the content/name/id mapping
		{q: "MATCH (n) WHERE id(n) = 'h:node-a' RETURN n.name", want: [][]any{r("node-a")}},
		{q: "MATCH (a {name:'cortexdb-grpc'})-[r]->(b) RETURN labels(a), type(r), labels(b) ORDER BY type(r)", ordered: true, want: [][]any{r(l("service"), "part_of", l("project")), r(l("service"), "runs_on", l("host"))}},
		{q: "MATCH (n {name:'orphan'}) RETURN labels(n)", want: [][]any{r(l())}},
		{q: "MATCH (m:memory) RETURN m.content", want: [][]any{r("CortexDB runs on node-e")}},
		{q: "MATCH (m:memory) RETURN m.name", want: [][]any{r(nil)}},
		{q: "MATCH (h:host) WHERE h.name = 'apps VM' RETURN id(h)", want: [][]any{r("h:apps")}},
		{q: "MATCH (n:software) RETURN id(n) ORDER BY id(n)", ordered: true, want: [][]any{r("lib:pgvector"), r("lib:sqlite")}},
		{q: "MATCH (n:software) RETURN n.id", want: [][]any{r(nil), r(nil)}},
		{q: "MATCH (a:project)-[:depends_on*2..1]->(b) RETURN count(*)", want: [][]any{r(I(0))}},
		{q: "MATCH (n) WITH [n] AS xs MATCH (xs)-->(m) RETURN m", errKind: cypher.ErrSemantic},
		{q: "MATCH p = (a)-->(b) WHERE p.name = 'x' RETURN p", errKind: cypher.ErrSemantic},
		{q: "MATCH (n:step) WITH n.name AS name WHERE n.i > 6 RETURN name ORDER BY name", ordered: true, want: [][]any{r("c7"), r("c8")}},
		{q: "MATCH (n:step) WITH n.name AS name LIMIT 3 WHERE n.i > 6 RETURN name", errKind: cypher.ErrUnsupported},
		{q: "MATCH (n:project) RETURN n.lang, size([x IN collect(n.name) WHERE x STARTS WITH 'C']) AS c ORDER BY n.lang", ordered: true, want: [][]any{r("Go", I(1)), r("Rust", I(0)), r("TypeScript", I(0))}},
		{q: "MATCH (:project {name:'CortexDB'})-[r:uses]->(x) RETURN x.name, r.weight, r.since ORDER BY x.name", ordered: true, want: [][]any{r("SQLite", 0.9, "2024"), r("pgvector", 1.0, nil)}},
		{q: "MATCH (a)-[r:uses {since:'2024'}]->(b) RETURN a.name, b.name", want: [][]any{r("CortexDB", "SQLite")}},
		{q: "MATCH (s:service) WHERE 'brain' IN s.tags RETURN s.name", want: [][]any{r("cortexdb-grpc")}},
		{q: "MATCH (n) WHERE n.enabled IS NOT NULL RETURN n.name, n.enabled ORDER BY n.name", ordered: true, want: [][]any{r("agent-browser", false), r("sds-ai", true)}},
		{q: "MATCH (n) WHERE n.enabled = true RETURN n.name", want: [][]any{r("sds-ai")}},
		{q: "MATCH (n:host) WHERE n.cores = 8.0 RETURN n.name", want: [][]any{r("node-e")}},
		{q: "MATCH (n:host) WHERE n.cores > '4' RETURN n.name", want: [][]any{}},
		{q: "MATCH (s:service) WHERE s.port = '47821' RETURN s.name", want: [][]any{}},
		{q: "MATCH (s:service) WHERE s.port = 47821 RETURN s.name", want: [][]any{r("cortexdb-grpc")}},
		{q: "MATCH (n:host {name:'node-a'}) RETURN keys(n)", want: [][]any{r(l("cores", "name"))}},
		{q: "MATCH (h:host) RETURN coalesce(h.cores, 0) AS c ORDER BY c", ordered: true, want: [][]any{r(I(0)), r(I(4)), r(I(8))}},
		{q: "MATCH (n:software) RETURN toUpper(n.name) ORDER BY toUpper(n.name)", ordered: true, want: [][]any{r("PGVECTOR"), r("SQLITE")}},
		{q: "MATCH (h:host) WHERE NOT (h.cores > 4) RETURN h.name", want: [][]any{r("node-a")}},
		{q: "MATCH (a)-[r]->(b {name:'node-e'}) WHERE type(r) = 'mentions' RETURN a.content", want: [][]any{r("CortexDB runs on node-e")}},
		{q: "MATCH ()-[r:part_of]->() RETURN startNode(r).name, endNode(r).name", want: [][]any{r("cortexdb-grpc", "CortexDB")}},
		{q: "MATCH (p:project) RETURN p.name, CASE WHEN p.stars >= 100 THEN 'big' ELSE 'small' END AS size ORDER BY p.name", ordered: true, want: [][]any{r("Athanor", "small"), r("CortexDB", "big"), r("aigui", "big"), r("harness-rs", "small"), r("steward", "small")}},
		{q: "MATCH (n:tool) RETURN *", want: [][]any{r("node:t:browser")}},
		{q: "MATCH (n:host) WHERE n IS LABELED host RETURN count(*)", want: [][]any{r(I(3))}},
		{q: "MATCH (n) WHERE n:host OR n:service RETURN count(*)", want: [][]any{r(I(5))}},
		{q: "MATCH (n:host|service) RETURN count(*)", want: [][]any{r(I(5))}},
		{q: "MATCH (n:host:service) RETURN count(*)", want: [][]any{r(I(0))}},
		{q: "MATCH (n IS host) RETURN count(*)", want: [][]any{r(I(3))}},
		{q: "MATCH (n:Nope) RETURN count(*)", want: [][]any{r(I(0))}},
		{q: "MATCH (n) WHERE n IS NOT LABELED step AND n IS NOT LABELED ring RETURN count(*)", want: [][]any{r(I(16))}},

		// --- paths, UNWIND, UNION, no-MATCH queries
		{q: "MATCH p = (a {name:'steward'})-[:depends_on*2]->(b) RETURN length(p), [n IN nodes(p) | n.name]", want: [][]any{r(I(2), l("steward", "Athanor", "CortexDB"))}},
		{q: "MATCH p = (a:service)-[:runs_on]->(h) RETURN a.name, length(p) ORDER BY a.name", ordered: true, want: [][]any{r("cortexdb-grpc", I(1)), r("sds-ai", I(1))}},
		{q: "MATCH p = (b {name:'c3'})<-[:next*2]-(a) RETURN [n IN nodes(p) | n.name], [x IN relationships(p) | id(x)]", want: [][]any{r(l("c3", "c2", "c1"), l("n2", "n1"))}},
		{q: "UNWIND ['CortexDB', 'aigui', 'nope'] AS nm MATCH (p:project {name: nm}) RETURN p.stars ORDER BY p.stars", ordered: true, want: [][]any{r(I(120)), r(I(300))}},
		{q: "UNWIND [1, 2, 3] AS x RETURN sum(x)", want: [][]any{r(I(6))}},
		{q: "RETURN 1 + 2 AS three, 'a' + 'b' AS ab", want: [][]any{r(I(3), "ab")}},
		{q: "MATCH (p:project {lang:'Go'}) RETURN p.name AS n UNION MATCH (s:service) RETURN s.name AS n", want: [][]any{r("Athanor"), r("CortexDB"), r("steward"), r("cortexdb-grpc"), r("sds-ai")}},
		{q: "MATCH (p:project {name:'aigui'}) RETURN p.name AS n UNION ALL MATCH (p:project {name:'aigui'}) RETURN p.name AS n", want: [][]any{r("aigui"), r("aigui")}},
		{q: "MATCH (p:project {name:'aigui'}) RETURN p.name AS n UNION MATCH (p:project {name:'aigui'}) RETURN p.name AS n", want: [][]any{r("aigui")}},
		{q: "MATCH (p:project) WHERE all(x IN [p.stars] WHERE x > 10) RETURN count(*)", want: [][]any{r(I(4))}},
		{q: "MATCH (s:service) RETURN s.name, s.tags[0] ORDER BY s.name", ordered: true, want: [][]any{r("cortexdb-grpc", "grpc"), r("sds-ai", nil)}},

		// --- refused, by name
		{q: "CREATE (n:project {name:'x'})", errKind: cypher.ErrReadOnly},
		{q: "MATCH (n) SET n.x = 1 RETURN n", errKind: cypher.ErrReadOnly},
		{q: "MATCH (n) DELETE n", errKind: cypher.ErrReadOnly},
		{q: "MERGE (n {name:'x'}) RETURN n", errKind: cypher.ErrReadOnly},
		{q: "MATCH (n) DETACH DELETE n", errKind: cypher.ErrReadOnly},
		{q: "MATCH p = shortestPath((a)-[*]-(b)) RETURN p", errKind: cypher.ErrUnsupported},
		{q: "CALL db.labels()", errKind: cypher.ErrUnsupported},
		{q: "MATCH (n) WHERE (n)-->() RETURN n", errKind: cypher.ErrUnsupported},
		{q: "MATCH (n) RETURN n {.name}", errKind: cypher.ErrUnsupported},
		{q: "MATCH (n) WHERE exists { (n)-->() } RETURN n", errKind: cypher.ErrUnsupported},
		{q: "MATCH (n) RETURN reduce(s = 0, x IN [1] | s + x)", errKind: cypher.ErrUnsupported},
		{q: "MATCH (a)-[*1..9]->(b) RETURN a", errKind: cypher.ErrUnsupported},
		{q: "MATCH (n) RETURN apoc.coll.sum([1])", errKind: cypher.ErrUnsupported},
		{q: "MATCH (n) RETURN undefinedVar", errKind: cypher.ErrSemantic},
		{q: "MATCH (n) WITH n.name RETURN n", errKind: cypher.ErrSemantic},
		{q: "MATCH (n) WHERE count(n) > 1 RETURN n", errKind: cypher.ErrSemantic},
		{q: "MATCH (n:step) RETURN n.name, n.i + count(*)", errKind: cypher.ErrSemantic},
		{q: "MATCH (n) RETURN n LIMIT -1", errKind: cypher.ErrSemantic},
		{q: "MATCH (n RETURN n", errKind: cypher.ErrSyntax},
	}
}

// normalizeCypherValue turns result values into something a hand-written
// expectation can be compared to: nodes and relationships by id.
func normalizeCypherValue(v any) any {
	switch x := v.(type) {
	case *cypher.Node:
		return "node:" + x.ID
	case *cypher.Rel:
		return "rel:" + x.ID
	case *cypher.Path:
		out := []any{}
		for _, n := range x.Nodes {
			out = append(out, "node:"+n.ID)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = normalizeCypherValue(x[i])
		}
		return out
	case int:
		return int64(x)
	}
	return v
}

func canonicalRows(rows [][]any, ordered bool) []string {
	out := make([]string, len(rows))
	for i, rw := range rows {
		b, _ := json.Marshal(rw)
		out[i] = string(b)
	}
	if !ordered {
		sort.Strings(out)
	}
	return out
}

func TestCypherCorpusAgreesWithHandComputedAnswersOnBothBackends(t *testing.T) {
	cases := cypherCorpus()
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadCypherFixture(t, b.store)
			agree, refused := 0, 0
			for i, c := range cases {
				res, err := b.store.QueryCypher(context.Background(), CypherRequest{Query: c.q, Params: c.params, IncludeEndedFacts: c.includeEnded})
				if c.errKind != "" {
					var ce *cypher.Error
					if err == nil || !errors.As(err, &ce) || ce.Kind != c.errKind {
						t.Errorf("#%d %s\n  want %s error, got %v (res=%v)", i, c.q, c.errKind, err, res)
						continue
					}
					refused++
					continue
				}
				if err != nil {
					t.Errorf("#%d %s\n  error: %v", i, c.q, err)
					continue
				}
				got := make([][]any, len(res.Rows))
				for j, rw := range res.Rows {
					got[j] = make([]any, len(rw))
					for k := range rw {
						got[j][k] = normalizeCypherValue(rw[k])
					}
				}
				gs, ws := canonicalRows(got, c.ordered), canonicalRows(c.want, c.ordered)
				if !reflect.DeepEqual(gs, ws) {
					t.Errorf("#%d %s\n  got  %v\n  want %v", i, c.q, gs, ws)
					continue
				}
				agree++
			}
			t.Logf("%s: %d/%d answered exactly, %d refused as expected", b.name, agree, len(cases)-refused, refused)
		})
	}
}

// The as-of instant decides which temporal facts a query sees.
func TestCypherJudgesTemporalFactsAtTheRequestedInstant(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadCypherFixture(t, b.store)
			res, err := cypher.Execute(context.Background(), cypherBackend{b.store},
				"MATCH (x)-[:runs_on]->(h {name:'node-a'}) RETURN x.name ORDER BY x.name",
				cypher.Options{ValidAt: time.Date(2020, 6, 1, 0, 0, 0, 0, time.UTC)})
			if err != nil {
				t.Fatal(err)
			}
			got := canonicalRows(res.Rows, true)
			want := canonicalRows([][]any{r("Athanor"), r("aigui")}, true)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %v want %v", got, want)
			}
		})
	}
}
