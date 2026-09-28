package graphflow

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// A labeled alias set, for measuring what entity resolution gets wrong.
//
// Every pair is its own island so no pair's outcome depends on another's.
// Neighbours are document nodes, not entities, so they take part in the
// neighbour evidence without themselves becoming candidates.
//
// Neighbourhood shapes:
//
//	shared    both names are mentioned by the same two documents
//	disjoint  each name is mentioned by its own documents, none in common
//	none      neither name has any edge
//
// The true aliases include cases resolution *should* find hard — the same
// thing written in two documents that never mention each other — because a
// fixture made only of easy aliases measures nothing about false merges.
type aliasPair struct {
	a, b     string
	typeA    string
	typeB    string
	shape    string
	alias    bool
	llmGroup bool // the fake model proposes this pair as one entity
}

var aliasFixture = []aliasPair{
	// True aliases.
	{a: "CortexDB", b: "Cortex DB", typeA: "Project", typeB: "Project", shape: "shared", alias: true},
	{a: "PostgreSQL", b: "Postgres", typeA: "Tool", typeB: "Tool", shape: "shared", alias: true},
	{a: "Kubernetes", b: "K8s", typeA: "Tool", typeB: "Tool", shape: "shared", alias: true, llmGroup: true},
	{a: "New York City", b: "New-York City", typeA: "Place", typeB: "Place", shape: "disjoint", alias: true},
	{a: "gRPC", b: "g-RPC", typeA: "Tool", typeB: "Tool", shape: "none", alias: true},
	{a: "OpenAI", b: "Open AI", typeA: "Org", typeB: "Org", shape: "shared", alias: true},
	{a: "JavaScript", b: "Java Script", typeA: "Language", typeB: "Language", shape: "disjoint", alias: true},
	{a: "Elasticsearch", b: "ElasticSearch", typeA: "Tool", typeB: "Tool", shape: "shared", alias: true},
	{a: "TensorFlow", b: "Tensorflow", typeA: "Tool", typeB: "Tool", shape: "none", alias: true},
	{a: "Visual Studio Code", b: "VS Code", typeA: "Tool", typeB: "Tool", shape: "disjoint", alias: true, llmGroup: true},
	{a: "Microsoft Corporation", b: "Microsoft Corp", typeA: "Org", typeB: "Org", shape: "shared", alias: true},
	{a: "DRBD 9", b: "DRBD9", typeA: "Tool", typeB: "Tool", shape: "none", alias: true},

	// Near-miss non-aliases.
	{a: "Java", b: "JavaFX", typeA: "Language", typeB: "Language", shape: "disjoint"},
	{a: "Python 2", b: "Python 3", typeA: "Language", typeB: "Language", shape: "disjoint"},
	{a: "main.go", b: "main.go ", typeA: "File", typeB: "File", shape: "disjoint"},
	{a: "Austria", b: "Australia", typeA: "Place", typeB: "Place", shape: "disjoint", llmGroup: true},
	{a: "React", b: "React Native", typeA: "Framework", typeB: "Framework", shape: "disjoint"},
	{a: "Mercury", b: "mercury", typeA: "Thing", typeB: "Thing", shape: "disjoint"},
	{a: "Swift", b: "SWIFT", typeA: "Thing", typeB: "Thing", shape: "disjoint"},
	{a: "config.yaml", b: "config.yaml ", typeA: "File", typeB: "File", shape: "disjoint"},
	{a: "Windows 10", b: "Windows 11", typeA: "Product", typeB: "Product", shape: "shared"},
	{a: "GPT-4", b: "GPT-4o", typeA: "Model", typeB: "Model", shape: "shared"},
	{a: "Georgia", b: "Georgia ", typeA: "Place", typeB: "Place", shape: "disjoint"},
	{a: "Jordan", b: "Jordan ", typeA: "Person", typeB: "Place", shape: "disjoint"},
}

type fixtureOutcome struct {
	merged, linked int
}

// seedAliasFixture writes the fixture and returns the node ids of each pair.
func seedAliasFixture(t *testing.T, ctx context.Context, db *cortexdb.DB) [][2]string {
	t.Helper()
	g := db.Graph()
	ids := make([][2]string, len(aliasFixture))
	for i, p := range aliasFixture {
		ida := fmt.Sprintf("entity:fx%02d_a", i)
		idb := fmt.Sprintf("entity:fx%02d_b", i)
		ids[i] = [2]string{ida, idb}
		for _, n := range []struct{ id, name, typ string }{{ida, p.a, p.typeA}, {idb, p.b, p.typeB}} {
			if err := g.UpsertNode(ctx, &graph.GraphNode{ID: n.id, Content: n.name, NodeType: n.typ, Vector: fixtureVec}); err != nil {
				t.Fatalf("seed node: %v", err)
			}
		}
		doc := func(k int) string { return fmt.Sprintf("doc:fx%02d_%d", i, k) }
		link := func(docID, ent string) {
			if err := g.UpsertNode(ctx, &graph.GraphNode{ID: docID, Content: docID, NodeType: "Document", Vector: fixtureVec}); err != nil {
				t.Fatalf("seed doc: %v", err)
			}
			if err := g.UpsertEdge(ctx, &graph.GraphEdge{
				ID: docID + "->" + ent, FromNodeID: docID, ToNodeID: ent, EdgeType: "mentions", Weight: 1,
			}); err != nil {
				t.Fatalf("seed edge: %v", err)
			}
		}
		switch p.shape {
		case "shared":
			for _, k := range []int{1, 2} {
				link(doc(k), ida)
				link(doc(k), idb)
			}
		case "disjoint":
			link(doc(1), ida)
			link(doc(2), idb)
		}
	}
	return ids
}

func measureAliasOutcome(t *testing.T, ctx context.Context, db *cortexdb.DB, ids [][2]string) (aliases, nonAliases fixtureOutcome) {
	t.Helper()
	g := db.Graph()
	for i, p := range aliasFixture {
		a, _ := g.GetNode(ctx, ids[i][0])
		b, _ := g.GetNode(ctx, ids[i][1])
		merged := (a == nil) != (b == nil)
		var links int
		if err := db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_edges
			WHERE edge_type = 'possiblySame'
			  AND ((from_node_id = ? AND to_node_id = ?) OR (from_node_id = ? AND to_node_id = ?))`,
			ids[i][0], ids[i][1], ids[i][1], ids[i][0]).Scan(&links); err != nil {
			t.Fatalf("count links: %v", err)
		}
		out := &nonAliases
		if p.alias {
			out = &aliases
		}
		switch {
		case merged:
			out.merged++
		case links > 0:
			out.linked++
		}
		t.Logf("%-3d alias=%-5v %-24q %-24q merged=%v linked=%v", i, p.alias, p.a, p.b, merged, links > 0)
	}
	return aliases, nonAliases
}

func fixtureLLM() resolveFakeLLM {
	return resolveFakeLLM{resp: `{"groups":[
		{"canonical":"Kubernetes","aliases":["K8s"]},
		{"canonical":"Visual Studio Code","aliases":["VS Code"]},
		{"canonical":"Australia","aliases":["Austria"]}]}`}
}

// TestResolveAliasFixtureMetrics reports false-merge and linking rates on the
// labeled fixture. It asserts only the invariants the design promises; the
// rates are logged so a change to the scorer can be compared run to run.
func TestResolveAliasFixtureMetrics(t *testing.T) {
	var nAlias, nNon int
	for _, p := range aliasFixture {
		if p.alias {
			nAlias++
		} else {
			nNon++
		}
	}
	for _, withLLM := range []bool{false, true} {
		t.Run(fmt.Sprintf("llm=%v", withLLM), func(t *testing.T) {
			db, ctx := openResolveTestDB(t)
			ids := seedAliasFixture(t, ctx, db)
			opts := ResolveOptions{}
			if withLLM {
				opts.LLM = fixtureLLM()
			}
			if _, err := ResolveEntities(ctx, db, opts); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			al, non := measureAliasOutcome(t, ctx, db, ids)
			t.Logf("METRIC llm=%v aliases=%d non_aliases=%d | true merged=%d linked=%d found(merged+linked)=%.2f merge_rate=%.2f | false_merge=%d false_merge_rate=%.2f non_alias_linked=%d",
				withLLM, nAlias, nNon,
				al.merged, al.linked, float64(al.merged+al.linked)/float64(nAlias), float64(al.merged)/float64(nAlias),
				non.merged, float64(non.merged)/float64(nNon), non.linked)
		})
	}
}

var fixtureVec = []float32{1, 0, 0, 0}

// A link is a record a person can act on: it carries its evidence, passes the
// knowledge contract, and is what contract_needs_attention lists.
func TestResolveLinkIsHeldForAPerson(t *testing.T) {
	db, ctx := openResolveTestDB(t)
	ids := seedAliasFixture(t, ctx, db)
	if _, err := ResolveEntities(ctx, db, ResolveOptions{}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Mercury/mercury: same normalized name, disjoint neighbourhoods.
	k := orderedPair(ids[17][0], ids[17][1])
	edges, err := db.Graph().GetEdgesBatch(ctx, []string{possiblySameEdgeID(k)})
	if err != nil || len(edges) != 1 {
		t.Fatalf("possiblySame edge: %v, %v", edges, err)
	}
	props := edges[0].Properties
	for _, key := range []string{"name_similarity", "shared_neighbours", "neighbour_jaccard", "type_agreement", "score"} {
		if _, ok := props[key]; !ok {
			t.Errorf("evidence %q missing from %v", key, props)
		}
	}
	if props["neighbours_disjoint"] != true {
		t.Errorf("neighbours_disjoint = %v", props["neighbours_disjoint"])
	}
	if err := cortexdb.ValidateContract(possiblySameContract(props)); err != nil {
		t.Fatalf("link fails the contract: %v", err)
	}
	att, err := db.NeedsAttention(ctx, 0)
	if err != nil {
		t.Fatalf("needs attention: %v", err)
	}
	found := false
	for _, r := range att {
		if r.ID == possiblySameEdgeID(k) {
			found = r.Grade == cortexdb.GradeHeld && strings.Contains(r.Why, "Mercury")
		}
	}
	if !found {
		t.Fatalf("link not listed by NeedsAttention with its reason: %+v", att)
	}
}

// A person's answer is read back: verified merges on the next pass, refused
// is never proposed again.
func TestResolveReadsAPersonsAnswer(t *testing.T) {
	db, ctx := openResolveTestDB(t)
	ids := seedAliasFixture(t, ctx, db)
	if _, err := ResolveEntities(ctx, db, ResolveOptions{}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	grade := func(i int, g string) {
		k := orderedPair(ids[i][0], ids[i][1])
		edges, err := db.Graph().GetEdgesBatch(ctx, []string{possiblySameEdgeID(k)})
		if err != nil || len(edges) != 1 {
			t.Fatalf("link %d: %v %v", i, edges, err)
		}
		edges[0].Properties[cortexdb.KeyGrade] = g
		edges[0].Properties[cortexdb.KeyBy] = "tester"
		if err := db.Graph().UpsertEdge(ctx, edges[0]); err != nil {
			t.Fatalf("grade: %v", err)
		}
	}
	grade(3, cortexdb.GradeVerified) // New York City — the same place
	grade(17, cortexdb.GradeRefused) // Mercury — two things

	report, err := ResolveEntities(ctx, db, ResolveOptions{})
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	a, _ := db.Graph().GetNode(ctx, ids[3][0])
	b, _ := db.Graph().GetNode(ctx, ids[3][1])
	if (a == nil) == (b == nil) {
		t.Fatalf("a verified link was not merged (a=%v b=%v)", a != nil, b != nil)
	}
	for _, l := range report.Links {
		if orderedPair(l.AID, l.BID) == orderedPair(ids[17][0], ids[17][1]) {
			t.Fatalf("a refused pair was proposed again: %+v", l)
		}
	}
	edges, _ := db.Graph().GetEdgesBatch(ctx, []string{possiblySameEdgeID(orderedPair(ids[17][0], ids[17][1]))})
	if len(edges) != 1 || edges[0].Properties[cortexdb.KeyGrade] != cortexdb.GradeRefused {
		t.Fatalf("the refusal was overwritten: %+v", edges)
	}
}

func TestJaroWinklerKnownValues(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want float64
	}{
		{"martha", "marhta", 0.961},
		{"dixon", "dicksonx", 0.813},
		{"abc", "abc", 1},
		{"abc", "", 0},
	} {
		if got := round3(jaroWinkler(c.a, c.b)); got != c.want {
			t.Errorf("jaroWinkler(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
