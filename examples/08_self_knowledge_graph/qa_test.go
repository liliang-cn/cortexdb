package main

// Q&A test over the generated knowledge graph (out/self_kg.db).
// It answers natural-language questions about CortexDB by traversing the
// property graph that graphflow built from docs/PROJECT_OVERVIEW.md.
//
// Generate the db first:  go run ./examples/08_self_knowledge_graph
// Then:                   go test ./examples/08_self_knowledge_graph -v
// The test skips when the db has not been generated (e.g. in CI).

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// kgFact is one edge of the knowledge graph, with human-readable endpoints.
type kgFact struct {
	From     string
	Relation string
	To       string
}

// kgIndex is an in-memory view of the graphflow subgraph, the substrate the
// Q&A helpers traverse.
type kgIndex struct {
	labels map[string]string // node id -> label
	facts  []kgFact
}

func loadKG(t *testing.T) *kgIndex {
	t.Helper()
	const dbPath = "out/self_kg.db"
	if _, err := os.Stat(dbPath); err != nil {
		t.Skipf("knowledge graph not generated yet — run: go run ./examples/08_self_knowledge_graph")
	}
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open kg db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	idx := &kgIndex{labels: map[string]string{}}

	rows, err := db.SQL().QueryContext(ctx,
		`SELECT id, content FROM graph_nodes WHERE id LIKE 'graphflow:node:%'`)
	if err != nil {
		t.Fatalf("query nodes: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, label string
		if err := rows.Scan(&id, &label); err != nil {
			t.Fatalf("scan node: %v", err)
		}
		idx.labels[id] = label
	}

	edgeRows, err := db.SQL().QueryContext(ctx,
		`SELECT from_node_id, edge_type, to_node_id FROM graph_edges
		 WHERE from_node_id LIKE 'graphflow:node:%'`)
	if err != nil {
		t.Fatalf("query edges: %v", err)
	}
	defer edgeRows.Close()
	for edgeRows.Next() {
		var from, rel, to string
		if err := edgeRows.Scan(&from, &rel, &to); err != nil {
			t.Fatalf("scan edge: %v", err)
		}
		idx.facts = append(idx.facts, kgFact{
			From:     idx.labels[from],
			Relation: rel,
			To:       idx.labels[to],
		})
	}
	if len(idx.labels) == 0 || len(idx.facts) == 0 {
		t.Fatalf("knowledge graph is empty: nodes=%d edges=%d", len(idx.labels), len(idx.facts))
	}
	return idx
}

func containsFold(s string, subs ...string) bool {
	lower := strings.ToLower(s)
	for _, sub := range subs {
		if strings.Contains(lower, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

// answer collects the opposite endpoints of every fact whose anchor endpoint
// matches `entity` and whose relation matches any of `relations`.
// LLM extractions vary between runs, so matching is fuzzy by substring.
func (kg *kgIndex) answer(entity string, relations ...string) []string {
	var out []string
	seen := map[string]struct{}{}
	add := func(s string) {
		if s == "" {
			return
		}
		if _, dup := seen[s]; !dup {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	for _, f := range kg.facts {
		if !containsFold(f.Relation, relations...) {
			continue
		}
		if containsFold(f.From, entity) {
			add(f.To)
		}
		if containsFold(f.To, entity) {
			add(f.From)
		}
	}
	return out
}

// hub returns the label of the most connected node.
func (kg *kgIndex) hub() string {
	degree := map[string]int{}
	for _, f := range kg.facts {
		degree[f.From]++
		degree[f.To]++
	}
	best, bestN := "", 0
	for label, n := range degree {
		if n > bestN {
			best, bestN = label, n
		}
	}
	return fmt.Sprintf("%s (%d 条关系)", best, bestN)
}

func TestKnowledgeGraphQA(t *testing.T) {
	kg := loadKG(t)
	t.Logf("knowledge graph loaded: %d nodes, %d facts", len(kg.labels), len(kg.facts))

	qa := []struct {
		question  string
		entity    string
		relations []string
		expect    string // answer must contain this substring
	}{
		{
			question:  "谁创建了 CortexDB？",
			entity:    "CortexDB",
			relations: []string{"creat", "创建", "author"},
			expect:    "Liang Li",
		},
		{
			question:  "CortexDB 的存储内核是什么？",
			entity:    "CortexDB",
			relations: []string{"use", "storage", "存储", "kernel"},
			expect:    "SQLite",
		},
		{
			question:  "CortexDB 是用什么语言实现的？",
			entity:    "CortexDB",
			relations: []string{"implement", "written", "实现"},
			expect:    "Go",
		},
		{
			question:  "公共门面层和哪些模块有依赖关系？",
			entity:    "门面",
			relations: []string{"depend", "依赖", "foundation", "支撑"},
			expect:    "", // 只要求答案非空，具体边由 LLM 抽取决定
		},
	}

	for _, item := range qa {
		t.Run(item.question, func(t *testing.T) {
			answers := kg.answer(item.entity, item.relations...)
			if len(answers) == 0 {
				t.Fatalf("Q: %s — 图谱中没有找到答案 (entity=%q relations=%v)",
					item.question, item.entity, item.relations)
			}
			joined := strings.Join(answers, "；")
			t.Logf("Q: %s\n   A: %s", item.question, joined)
			if item.expect != "" && !containsFold(joined, item.expect) {
				t.Fatalf("Q: %s — 期望答案包含 %q，实际: %s", item.question, item.expect, joined)
			}
		})
	}

	t.Run("整个图谱里连接最多的枢纽节点是？", func(t *testing.T) {
		hub := kg.hub()
		if hub == "" {
			t.Fatal("找不到枢纽节点")
		}
		t.Logf("Q: 整个图谱里连接最多的枢纽节点是？\n   A: %s", hub)
	})
}
