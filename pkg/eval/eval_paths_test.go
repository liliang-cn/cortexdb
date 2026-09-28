package eval_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/eval"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Multi-hop fixture: one fact per document, so a chunk is exactly one piece
// of evidence. Every fact is also written to the graph as the relation an
// extractor would have produced, citing its chunk. Distractors ("d*") share
// names and words with the questions and are wrong for all of them; one of
// them (d4) is a homonym merged into the same node, the failure mode path
// retrieval is weakest against, left in on purpose.
type pathFact struct {
	id       string
	text     string
	entities []string
	rels     [][3]string // from, type, to
}

var multiHopFacts = []pathFact{
	{"f1", "Alice Chen is a senior engineer at Borealis Labs.", []string{"Alice Chen", "Borealis Labs"}, [][3]string{{"Alice Chen", "works_at", "Borealis Labs"}}},
	{"f2", "Borealis Labs designed and built the Kestrel satellite.", []string{"Borealis Labs", "Kestrel"}, [][3]string{{"Borealis Labs", "built", "Kestrel"}}},
	{"f3", "Borealis Labs is headquartered in Lisbon.", []string{"Borealis Labs", "Lisbon"}, [][3]string{{"Borealis Labs", "headquartered_in", "Lisbon"}}},
	{"f4", "Marco Rossi founded Borealis Labs in 2011.", []string{"Marco Rossi", "Borealis Labs"}, [][3]string{{"Marco Rossi", "founded", "Borealis Labs"}}},
	{"f5", "Kestrel was launched on an Ariane rocket operated by Arianespace.", []string{"Kestrel", "Arianespace"}, [][3]string{{"Kestrel", "launched_by", "Arianespace"}}},
	{"f6", "Arianespace is owned by ArianeGroup.", []string{"Arianespace", "ArianeGroup"}, [][3]string{{"Arianespace", "owned_by", "ArianeGroup"}}},
	{"f7", "Priya Nair leads the Orion Team.", []string{"Priya Nair", "Orion Team"}, [][3]string{{"Priya Nair", "leads", "Orion Team"}}},
	{"f8", "The Orion Team maintains the Helios scheduler.", []string{"Orion Team", "Helios"}, [][3]string{{"Orion Team", "maintains", "Helios"}}},
	{"f9", "Helios depends on the Quartz message queue.", []string{"Helios", "Quartz"}, [][3]string{{"Helios", "depends_on", "Quartz"}}},
	{"f10", "Quartz was written by Tomas Berg.", []string{"Quartz", "Tomas Berg"}, [][3]string{{"Tomas Berg", "wrote", "Quartz"}}},
	{"f11", "Tomas Berg studied at Uppsala University.", []string{"Tomas Berg", "Uppsala University"}, [][3]string{{"Tomas Berg", "studied_at", "Uppsala University"}}},

	{"d1", "Alice Chang works on the Kestrel satellite ground station software.", []string{"Alice Chang", "Kestrel"}, [][3]string{{"Alice Chang", "works_on", "Kestrel"}}},
	{"d2", "The kestrel falcon is a small bird; satellite tracking studies follow kestrel migration.", []string{"Kestrel Falcon"}, nil},
	{"d3", "Boreal Labs, not to be confused with Borealis Labs, is headquartered in Porto.", []string{"Boreal Labs", "Porto"}, [][3]string{{"Boreal Labs", "headquartered_in", "Porto"}}},
	{"d4", "Marco Rossi, a chef, opened a restaurant next to the Arianespace office.", []string{"Marco Rossi", "Arianespace"}, [][3]string{{"Marco Rossi", "opened_restaurant_near", "Arianespace"}}},
	{"d5", "Priya Nair gave a meetup talk comparing message queues such as Quartz.", []string{"Priya Nair", "Quartz"}, nil},
	{"d6", "The Helios scheduler page lists Tomas Bergstrom as a former contributor.", []string{"Helios", "Tomas Bergstrom"}, [][3]string{{"Tomas Bergstrom", "contributed_to", "Helios"}}},
	{"d7", "Borealis Labs sponsors the Lisbon Satellite Conference, held in Lisbon each spring.", []string{"Borealis Labs", "Lisbon Satellite Conference", "Lisbon"}, [][3]string{{"Borealis Labs", "sponsors", "Lisbon Satellite Conference"}, {"Lisbon Satellite Conference", "held_in", "Lisbon"}}},
	{"d8", "Uppsala University awarded Priya Nair an honorary degree.", []string{"Uppsala University", "Priya Nair"}, [][3]string{{"Priya Nair", "honorary_degree_from", "Uppsala University"}}},
	{"d9", "Ariane rockets are assembled by ArianeGroup in Les Mureaux.", []string{"ArianeGroup", "Les Mureaux"}, [][3]string{{"ArianeGroup", "located_in", "Les Mureaux"}}},
	{"d10", "Kestrel Capital, an investment firm, is headquartered in Lisbon.", []string{"Kestrel Capital", "Lisbon"}, [][3]string{{"Kestrel Capital", "headquartered_in", "Lisbon"}}},
}

type multiHopQuestion struct {
	id    string
	text  string
	seeds []string
	gold  []string // fact ids
}

var multiHopQuestions = []multiHopQuestion{
	// Two named endpoints: the answer is the chain between them.
	{"q1", "How is Alice Chen connected to the Kestrel satellite?", []string{"Alice Chen", "Kestrel"}, []string{"f1", "f2"}},
	{"q2", "What connects Marco Rossi to Arianespace?", []string{"Marco Rossi", "Arianespace"}, []string{"f4", "f2", "f5"}},
	{"q3", "How is Priya Nair related to the Quartz message queue?", []string{"Priya Nair", "Quartz"}, []string{"f7", "f8", "f9"}},
	{"q4", "What links Tomas Berg to the Helios scheduler?", []string{"Tomas Berg", "Helios"}, []string{"f10", "f9"}},
	{"q5", "How is Alice Chen connected to Lisbon?", []string{"Alice Chen", "Lisbon"}, []string{"f1", "f3"}},
	{"q6", "How is the Orion Team connected to Tomas Berg?", []string{"Orion Team", "Tomas Berg"}, []string{"f8", "f9", "f10"}},
	// One named entity: the answer is where a chain from it ends.
	{"q7", "In which city is the company that built Kestrel headquartered?", []string{"Kestrel"}, []string{"f2", "f3"}},
	{"q8", "Who owns the company that launched Kestrel?", []string{"Kestrel"}, []string{"f5", "f6"}},
	{"q9", "Which university did the author of the queue Helios depends on attend?", []string{"Helios"}, []string{"f9", "f10", "f11"}},
	{"q10", "Who founded the company Alice Chen works for?", []string{"Alice Chen"}, []string{"f1", "f4"}},
}

type multiHopScore struct {
	fullHit, recall, distractors, retrieved, gold float64
	n                                             int
}

func (s *multiHopScore) add(retrieved []string, q multiHopQuestion) {
	got := map[string]bool{}
	for _, id := range retrieved {
		got[id] = true
	}
	hit := 0
	for _, g := range q.gold {
		if got[g] {
			hit++
		}
	}
	if hit == len(q.gold) {
		s.fullHit++
	}
	s.recall += float64(hit) / float64(len(q.gold))
	for _, id := range retrieved {
		if strings.HasPrefix(id, "d") {
			s.distractors++
		}
		if containsID(q.gold, id) {
			s.gold++
		}
	}
	s.retrieved += float64(len(retrieved))
	s.n++
}

func (s multiHopScore) row(name string) string {
	dr, prec := 0.0, 0.0
	if s.retrieved > 0 {
		dr = s.distractors / s.retrieved
		prec = s.gold / s.retrieved
	}
	return fmt.Sprintf("%-28s full-evidence %.2f  evidence-recall %.2f  distractor-rate %.2f  precision %.2f  avg-chunks %.1f",
		name, s.fullHit/float64(s.n), s.recall/float64(s.n), dr, prec, s.retrieved/float64(s.n))
}

func containsID(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func openEvalDB(t *testing.T, prefix string) *cortexdb.DB {
	t.Helper()
	dbPath := fmt.Sprintf("%s_%d.db", prefix, testname.Nano())
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, s := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + s)
		}
	})
	return db
}

// TestPathRetrievalVsChunkRetrieval measures search_paths against the two
// existing chunk retrievers on the multi-hop fixture, at the same budget of k
// chunks, with the question's named entities given to every method that can
// take them. Deterministic: no embedder, lexical mode throughout.
func TestPathRetrievalVsChunkRetrieval(t *testing.T) {
	ctx := context.Background()
	db := openEvalDB(t, "test_eval_paths")
	tools := db.GraphRAGTools()

	chunkToFact := map[string]string{}
	for _, f := range multiHopFacts {
		ing, err := tools.IngestDocument(ctx, cortexdb.ToolIngestDocumentRequest{DocumentID: f.id, Content: f.text})
		if err != nil {
			t.Fatalf("ingest %s: %v", f.id, err)
		}
		if len(ing.ChunkNodeIDs) != 1 {
			t.Fatalf("%s: want one chunk, got %d", f.id, len(ing.ChunkNodeIDs))
		}
		chunkID := ing.ChunkNodeIDs[0]
		chunkToFact[chunkID] = f.id
		ents := make([]cortexdb.ToolEntityInput, 0, len(f.entities))
		for _, e := range f.entities {
			ents = append(ents, cortexdb.ToolEntityInput{Name: e, Type: "entity", ChunkIDs: []string{chunkID}})
		}
		if _, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{DocumentID: f.id, Entities: ents}); err != nil {
			t.Fatalf("entities %s: %v", f.id, err)
		}
		if len(f.rels) > 0 {
			rels := make([]cortexdb.ToolRelationInput, 0, len(f.rels))
			for _, r := range f.rels {
				rels = append(rels, cortexdb.ToolRelationInput{From: r[0], Type: r[1], To: r[2], ChunkIDs: []string{chunkID}})
			}
			out, err := tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{DocumentID: f.id, Relations: rels})
			if err != nil || len(out.Rejected) > 0 {
				t.Fatalf("relations %s: %v %v", f.id, err, out)
			}
		}
	}
	toFacts := func(chunkIDs []string, k int) []string {
		out := []string{}
		for _, id := range chunkIDs {
			if f, ok := chunkToFact[id]; ok && !containsID(out, f) {
				out = append(out, f)
			}
			if len(out) == k {
				break
			}
		}
		return out
	}

	const k = 5
	methods := []string{"chunk: search_text", "chunk: graphrag_lexical", "path: search_paths"}
	scores := map[string]map[string]*multiHopScore{} // group -> method
	for _, g := range []string{"all", "two-seed", "one-seed"} {
		scores[g] = map[string]*multiHopScore{}
		for _, m := range methods {
			scores[g][m] = &multiHopScore{}
		}
	}
	for _, q := range multiHopQuestions {
		group := "two-seed"
		if len(q.seeds) == 1 {
			group = "one-seed"
		}
		retrieved := map[string][]string{}

		st, err := tools.SearchText(ctx, cortexdb.ToolSearchTextRequest{Query: q.text, TopK: k})
		if err != nil {
			t.Fatalf("%s search_text: %v", q.id, err)
		}
		ids := []string{}
		for _, c := range st.Chunks {
			ids = append(ids, c.ID)
		}
		retrieved[methods[0]] = toFacts(ids, k)

		gr, err := tools.SearchGraphRAGLexical(ctx, cortexdb.ToolSearchGraphRAGLexicalRequest{
			Query: q.text, EntityNames: q.seeds, TopK: k, MaxContextChunks: k, RetrievalMode: cortexdb.RetrievalModeGraph,
		})
		if err != nil {
			t.Fatalf("%s graphrag: %v", q.id, err)
		}
		ids = ids[:0]
		for _, c := range gr.Chunks {
			ids = append(ids, c.ID)
		}
		retrieved[methods[1]] = toFacts(ids, k)

		ps, err := tools.SearchPaths(ctx, cortexdb.ToolSearchPathsRequest{EntityNames: q.seeds})
		if err != nil {
			t.Fatalf("%s paths: %v", q.id, err)
		}
		if len(ps.Unresolved) > 0 {
			t.Fatalf("%s: unresolved seeds %v", q.id, ps.Unresolved)
		}
		retrieved[methods[2]] = toFacts(ps.ChunkIDs, k)

		for _, m := range methods {
			scores["all"][m].add(retrieved[m], q)
			scores[group][m].add(retrieved[m], q)
		}
		t.Logf("%s %-10v text=%v graphrag=%v paths=%v", q.id, q.gold, retrieved[methods[0]], retrieved[methods[1]], retrieved[methods[2]])
	}
	for _, g := range []string{"all", "two-seed", "one-seed"} {
		t.Logf("--- %s (%d questions, k=%d)", g, scores[g][methods[0]].n, k)
		for _, m := range methods {
			t.Logf("%s", scores[g][m].row(m))
		}
	}

	// Floors: path retrieval must keep beating chunk retrieval where it is
	// meant to — questions naming both ends of the chain.
	two := scores["two-seed"]
	path, lex := two[methods[2]], two[methods[1]]
	if path.fullHit/float64(path.n) < 0.80 {
		t.Errorf("two-seed path full-evidence %.2f below floor 0.80", path.fullHit/float64(path.n))
	}
	if path.fullHit < lex.fullHit {
		t.Errorf("path retrieval full-evidence %.0f below graphrag chunk retrieval %.0f", path.fullHit, lex.fullHit)
	}
	if path.distractors/path.retrieved > lex.distractors/lex.retrieved {
		t.Errorf("path distractor rate above chunk retrieval's")
	}
}

// TestRelationPoliciesRanking measures what per-type weights and depths do to
// the ranking of graph facts around an entity: five people, each with four
// real facts within two hops (employer, employer's city, team, team's
// department) and a cloud of co-occurrence edges at one and two hops, which
// is what statistical extraction adds to a real graph. Ranked by expand_graph
// score, uniform policy (one global weight, which is what traversal was
// before) against a policy that down-weights co_occurs_with and stops it
// after the first hop.
//
// The weighting is chosen knowing the fixture, so the gain is the mechanism
// working, not a forecast for a real graph.
func TestRelationPoliciesRanking(t *testing.T) {
	ctx := context.Background()
	db := openEvalDB(t, "test_eval_relpolicy")
	tools := db.GraphRAGTools()

	type rel struct{ from, typ, to string }
	var rels []rel
	people := []string{"Ada", "Brook", "Cyrus", "Dana", "Emil"}
	relevant := map[string][]string{}
	for i, p := range people {
		org, city := fmt.Sprintf("%s Org", p), fmt.Sprintf("%s City", p)
		team, dept := fmt.Sprintf("%s Team", p), fmt.Sprintf("%s Dept", p)
		rels = append(rels,
			rel{p, "works_at", org}, rel{org, "located_in", city},
			rel{p, "member_of", team}, rel{team, "part_of", dept})
		relevant[p] = []string{org, city, team, dept}
		// Co-occurrence cloud: four hop-1 neighbours, each with two more.
		for j := 0; j < 4; j++ {
			n1 := fmt.Sprintf("Topic %d%d", i, j)
			rels = append(rels, rel{p, "co_occurs_with", n1})
			for m := 0; m < 2; m++ {
				rels = append(rels, rel{n1, "co_occurs_with", fmt.Sprintf("Topic %d%d%d", i, j, m)})
			}
		}
		// And the org co-occurs with something too.
		rels = append(rels, rel{org, "co_occurs_with", fmt.Sprintf("Buzzword %d", i)})
	}
	names := map[string]bool{}
	for _, r := range rels {
		names[r.from], names[r.to] = true, true
	}
	ents := []cortexdb.ToolEntityInput{}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		ents = append(ents, cortexdb.ToolEntityInput{Name: n, Type: "entity"})
	}
	if _, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: ents}); err != nil {
		t.Fatal(err)
	}
	in := make([]cortexdb.ToolRelationInput, 0, len(rels))
	for _, r := range rels {
		in = append(in, cortexdb.ToolRelationInput{From: r.from, Type: r.typ, To: r.to})
	}
	if out, err := tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{Relations: in}); err != nil || len(out.Rejected) > 0 {
		t.Fatalf("relations: %v %v", err, out)
	}

	idOf := map[string]string{}
	found, err := tools.FindNodes(ctx, cortexdb.ToolFindNodesRequest{Names: sorted, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range found.Matches {
		idOf[m.Name] = m.Nodes[0].ID
	}

	policies := map[string]graph.RelationPolicies{
		"uniform":  {"*": {Weight: 1}},
		"weighted": {"co_occurs_with": {Weight: 0.2, MaxDepth: 1}},
	}
	type agg struct{ p5, ndcg10 float64 }
	res := map[string]*agg{"uniform": {}, "weighted": {}}
	for _, p := range people {
		rel := []string{}
		for _, r := range relevant[p] {
			rel = append(rel, idOf[r])
		}
		for name, pol := range policies {
			out, err := tools.ExpandGraph(ctx, cortexdb.ToolExpandGraphRequest{
				NodeIDs: []string{idOf[p]}, MaxHops: 2, RelationPolicies: pol,
			})
			if err != nil {
				t.Fatal(err)
			}
			ranked := make([]string, 0, len(out.Scores))
			for id := range out.Scores {
				if id != idOf[p] {
					ranked = append(ranked, id)
				}
			}
			sort.Slice(ranked, func(i, j int) bool {
				if out.Scores[ranked[i]] != out.Scores[ranked[j]] {
					return out.Scores[ranked[i]] > out.Scores[ranked[j]]
				}
				return ranked[i] < ranked[j]
			})
			res[name].p5 += eval.PrecisionAtK(ranked, rel, 5) / float64(len(people))
			res[name].ndcg10 += eval.NDCGAtK(ranked, rel, 10) / float64(len(people))
		}
	}
	for _, name := range []string{"uniform", "weighted"} {
		t.Logf("%-9s precision@5 %.3f  nDCG@10 %.3f", name, res[name].p5, res[name].ndcg10)
	}
	if res["weighted"].ndcg10 <= res["uniform"].ndcg10 {
		t.Errorf("weighted nDCG@10 %.3f not above uniform %.3f", res["weighted"].ndcg10, res["uniform"].ndcg10)
	}
}
