package graph

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// Real questions over a real brain, timed.
//
// Skipped unless CORTEXDB_CYPHER_BRAIN names a SQLite brain file. Point it at
// a copy: opening a store migrates its schema. Extra queries can be passed in
// CORTEXDB_CYPHER_QUERIES, one per line.
func TestCypherAnswersRealQuestionsOnARealBrain(t *testing.T) {
	path := os.Getenv("CORTEXDB_CYPHER_BRAIN")
	if path == "" {
		t.Skip("CORTEXDB_CYPHER_BRAIN unset")
	}
	store, err := core.New(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	g := NewGraphStore(store)

	queries := []string{
		// 1. who depends on CortexDB
		"MATCH (p)-[:depends_on]->(c {name: 'CortexDB'}) RETURN p.name AS project, labels(p) AS type ORDER BY project",
		// 2. services running on node-e
		"MATCH (s)-[r:runs_on|deployed_on]->(h) WHERE h.name = 'node-e' RETURN s.name AS service, type(r) AS rel, labels(s) AS type ORDER BY service",
		// 3. what runs on each host
		"MATCH (h:host)<-[:runs_on|deployed_on]-(s) RETURN h.name AS host, count(s) AS n, collect(s.name) AS what ORDER BY n DESC, host LIMIT 10",
		// 4. 2-hop paths between two projects
		"MATCH p = (a {name: 'CortexDB'})-[*1..2]-(b {name: 'Athanor'}) RETURN [n IN nodes(p) | n.name] AS via, [r IN relationships(p) | type(r)] AS rels LIMIT 10",
		// 5. top node types by count
		"MATCH (n) RETURN labels(n)[0] AS type, count(*) AS c ORDER BY c DESC, type LIMIT 10",
		// 6. top edge types by count
		"MATCH ()-[r]->() RETURN type(r) AS rel, count(*) AS c ORDER BY c DESC, rel LIMIT 10",
		// 7. what CortexDB depends on / uses
		"MATCH (c {name: 'CortexDB'})-[r:depends_on|uses]->(x) RETURN type(r) AS rel, x.name AS target ORDER BY rel, target",
		// 8. projects that depend on something CortexDB-dependent (2 hop)
		"MATCH (a:project)-[:depends_on]->(b:project)-[:depends_on]->(c:project) RETURN a.name AS a, b.name AS b, c.name AS c ORDER BY a, b, c LIMIT 20",
		// 9. projects with no dependencies recorded
		"MATCH (p:project) OPTIONAL MATCH (p)-[d:depends_on]->() WITH p, count(d) AS deps WHERE deps = 0 RETURN count(p) AS isolated_projects",
		// 10. hosts and their neighbourhood by relation type
		"MATCH (h:host)-[r]-(x) WITH h, type(r) AS rel, count(*) AS n ORDER BY n DESC RETURN h.name AS host, collect(rel + ':' + toString(n))[0..3] AS top LIMIT 10",
	}
	if extra := os.Getenv("CORTEXDB_CYPHER_QUERIES"); extra != "" {
		queries = nil
		for _, q := range strings.Split(extra, "\n") {
			if strings.TrimSpace(q) != "" {
				queries = append(queries, q)
			}
		}
	}

	const runs = 21
	var all []time.Duration
	for i, q := range queries {
		var times []time.Duration
		var res any
		for k := 0; k < runs; k++ {
			start := time.Now()
			r, err := g.QueryCypher(ctx, CypherRequest{Query: q, MaxRows: 25})
			times = append(times, time.Since(start))
			if err != nil {
				t.Errorf("Q%d %s\n  error: %v", i+1, q, err)
				break
			}
			res = r
		}
		sort.Slice(times, func(a, b int) bool { return times[a] < times[b] })
		all = append(all, times...)
		b, _ := json.Marshal(res)
		out := string(b)
		if len(out) > 1500 {
			out = out[:1500] + "…"
		}
		t.Logf("Q%d %s\n  p50=%v p95=%v\n  %s", i+1, q, times[len(times)/2], times[len(times)*95/100], out)
	}
	sort.Slice(all, func(a, b int) bool { return all[a] < all[b] })
	if len(all) > 0 {
		t.Logf("overall p50=%v p95=%v over %d runs", all[len(all)/2], all[len(all)*95/100], len(all))
	}
}
