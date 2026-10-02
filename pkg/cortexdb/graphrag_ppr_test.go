package cortexdb

import (
	"context"
	"testing"
)

// The two-hop question these tests are built around:
//
//	"Where was the director of Moonlight Sonata raised?"
//
// The film's passage names its director; the director's passage says where he
// grew up. The second passage shares not one word with the question, so no
// lexical ranking, however good, can return it — the only road to it runs
// through the entity "Arvid Lund", which the question never mentions. That is
// a multi-hop question in its smallest form, and the reason the PPR mode
// exists.

const pprTwoHopQuery = "Where was the director of Moonlight Sonata raised?"

type pprPassage struct {
	id       string
	text     string
	entities []string
}

var pprCorpus = []pprPassage{
	{"film", "Moonlight Sonata is a 1999 drama film directed by Arvid Lund.", []string{"Moonlight Sonata", "Arvid Lund"}},
	{"lund", "Arvid Lund grew up in Malmo, a coastal city in southern Sweden.", []string{"Arvid Lund", "Malmo"}},
	{"beethoven", "Beethoven's Piano Sonata No. 14 is popularly known as the Moonlight Sonata.", []string{"Beethoven", "Moonlight Sonata"}},
	{"orchestra", "The director of the regional orchestra raised funds for a new concert hall.", []string{"Regional Orchestra"}},
	{"sonata", "A sonata is a composition for one or two instruments, raised from older dance forms.", nil},
	{"malmo-port", "The port of Malmo handles ferries to Copenhagen.", []string{"Malmo", "Copenhagen"}},
}

func seedPPRKnowledge(t *testing.T, ctx context.Context, db *DB) {
	t.Helper()
	for _, p := range pprCorpus {
		entities := make([]ToolEntityInput, 0, len(p.entities))
		for _, name := range p.entities {
			entities = append(entities, ToolEntityInput{Name: name, ChunkIDs: []string{graphChunkNodeID(p.id, 0)}})
		}
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
			KnowledgeID: p.id,
			Content:     p.text,
			Entities:    entities,
		}); err != nil {
			t.Fatalf("SaveKnowledge %s: %v", p.id, err)
		}
	}
}

func knowledgeRank(resp *KnowledgeSearchResponse, knowledgeID string) int {
	for i, c := range resp.Chunks {
		if c.DocumentID == knowledgeID {
			return i + 1
		}
	}
	return 0
}

func TestPPRFindsTheAnswerPassageTwoHopsFromTheQueryEntityThatLexicalSearchCannotSee(t *testing.T) {
	for name, db := range lexicalBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			seedPPRKnowledge(t, ctx, db)

			lexical, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: pprTwoHopQuery, TopK: 5, RetrievalMode: RetrievalModeLexical})
			if err != nil {
				t.Fatalf("lexical search: %v", err)
			}
			if knowledgeRank(lexical, "film") == 0 {
				t.Fatalf("lexical search should find the film passage, the first hop: %+v", lexical.Chunks)
			}
			if r := knowledgeRank(lexical, "lund"); r != 0 {
				t.Fatalf("the fixture is broken: lexical search found the answer passage at rank %d, so it no longer proves anything about the graph", r)
			}

			for _, fusion := range []string{PPRFusionRRF, PPRFusionPPR} {
				resp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{
					Query: pprTwoHopQuery, TopK: 5, RetrievalMode: RetrievalModePPR,
					PPR: &PPRRetrievalOptions{Fusion: fusion},
				})
				if err != nil {
					t.Fatalf("ppr search (%s): %v", fusion, err)
				}
				if resp.Decision.EffectiveMode != RetrievalModePPR {
					t.Fatalf("decision = %+v, want effective mode ppr", resp.Decision)
				}
				// In the five returned, where lexical search had no place for
				// it at any rank.
				r := knowledgeRank(resp, "lund")
				if r == 0 || r > 5 {
					t.Fatalf("fusion %s: answer passage at rank %d, want within the top 5: %+v", fusion, r, resp.Chunks)
				}
				if knowledgeRank(resp, "film") == 0 {
					t.Fatalf("fusion %s: the first-hop passage fell out of the results: %+v", fusion, resp.Chunks)
				}
				// The walk reaches the port passage too — Malmo is two hops on
				// from the answer — but less of it: it is a hop further out.
				if p := knowledgeRank(resp, "malmo-port"); p != 0 && p < r {
					t.Fatalf("fusion %s: the three-hop passage (rank %d) outranked the two-hop answer (rank %d)", fusion, p, r)
				}
			}
		})
	}
}

// With no entity in the query at all, the walk still starts — from the
// passages the first stage found. That is what the passage seeds are for:
// the film passage matches the words, names the director, and the director's
// passage is one entity away from it.
func TestPPRWalksFromTheFirstStagePassagesWhenTheQueryNamesNoEntity(t *testing.T) {
	for name, db := range lexicalBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			seedPPRKnowledge(t, ctx, db)
			query := "where was the director of the 1999 drama film raised"

			ids, err := db.queryEntityNodeIDs(ctx, query, nil)
			if err != nil {
				t.Fatalf("queryEntityNodeIDs: %v", err)
			}
			if len(ids) != 0 {
				t.Fatalf("the fixture is broken: the query names entities %v, so this no longer tests passage seeds", ids)
			}
			resp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: query, TopK: 5, RetrievalMode: RetrievalModePPR})
			if err != nil {
				t.Fatalf("ppr search: %v", err)
			}
			if knowledgeRank(resp, "lund") == 0 {
				t.Fatalf("the walk from the first-stage passages did not reach the answer: %+v", resp.Chunks)
			}
		})
	}
}

// A query names an entity when it writes one: the longest name the graph
// holds ("Moonlight Sonata", not also "Moonlight"), plus whatever the planner
// hinted. A word the query leaves lowercase is a word, even when some passage
// once capitalised it — seeding the walk from those cost single-hop questions
// their answer.
func TestQueryEntitiesAreTheLongestNamesTheQueryWritesAsNames(t *testing.T) {
	for name, db := range lexicalBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			seedPPRKnowledge(t, ctx, db)
			ids, err := db.queryEntityNodeIDs(ctx, "who scored Moonlight Sonata, and what of beethoven or arvid lund?", []string{"Malmo"})
			if err != nil {
				t.Fatalf("queryEntityNodeIDs: %v", err)
			}
			want := []string{"entity:malmo", "entity:moonlight_sonata"}
			if len(ids) != len(want) {
				t.Fatalf("got %v, want %v", ids, want)
			}
			for i := range want {
				if ids[i] != want[i] {
					t.Fatalf("got %v, want %v", ids, want)
				}
			}
		})
	}
}

func TestPPRMemorySearchReachesAMemoryThroughABridgeEntity(t *testing.T) {
	for name, db := range lexicalBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			for _, p := range pprCorpus {
				entities := make([]ToolEntityInput, 0, len(p.entities))
				for _, n := range p.entities {
					entities = append(entities, ToolEntityInput{Name: n})
				}
				if _, err := db.SaveMemory(ctx, MemorySaveRequest{
					MemoryID: "m-" + p.id, Scope: MemoryScopeGlobal, Content: p.text, Entities: entities,
				}); err != nil {
					t.Fatalf("SaveMemory %s: %v", p.id, err)
				}
			}
			rank := func(resp *MemorySearchResponse, id string) int {
				for i, h := range resp.Results {
					if h.Memory.ID == id {
						return i + 1
					}
				}
				return 0
			}

			lexical, err := db.SearchMemory(ctx, MemorySearchRequest{Query: pprTwoHopQuery, Scope: MemoryScopeGlobal, TopK: 5, RetrievalMode: RetrievalModeLexical})
			if err != nil {
				t.Fatalf("lexical memory search: %v", err)
			}
			if r := rank(lexical, "m-lund"); r != 0 {
				t.Fatalf("the fixture is broken: lexical memory search found the answer at rank %d", r)
			}
			resp, err := db.SearchMemory(ctx, MemorySearchRequest{Query: pprTwoHopQuery, Scope: MemoryScopeGlobal, TopK: 5, RetrievalMode: RetrievalModePPR})
			if err != nil {
				t.Fatalf("ppr memory search: %v", err)
			}
			if r := rank(resp, "m-lund"); r == 0 || r > 5 {
				t.Fatalf("answer memory at rank %d, want within the top 5: %+v", r, resp.Results)
			}
		})
	}
}

func TestRetrievalModePPRIsARequestableMode(t *testing.T) {
	res := resolveRetrievalPlan(retrievalPlanInput{Query: "q", RetrievalMode: "PPR", SupportsGraph: true})
	if res.Decision.EffectiveMode != RetrievalModePPR || !res.Decision.UseGraph {
		t.Fatalf("decision = %+v, want effective ppr with the graph in use", res.Decision)
	}
	if got := (*PPRRetrievalOptions)(nil).resolved(); got.Fusion != PPRFusionRRF || got.Damping != 0.5 {
		t.Fatalf("zero options resolved to %+v, want rrf fusion at damping 0.5", got)
	}
}
