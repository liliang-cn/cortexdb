package cortexdb

import (
	"context"
	"fmt"
	"testing"
)

func TestAutoWithoutAnEmbedderWalksTheGraphAndFindsTheSecondHop(t *testing.T) {
	for name, db := range lexicalBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			seedPPRKnowledge(t, ctx, db)

			resp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: pprTwoHopQuery, TopK: 5})
			if err != nil {
				t.Fatalf("auto search: %v", err)
			}
			if resp.Decision.RequestedMode != RetrievalModeAuto || resp.Decision.EffectiveMode != RetrievalModePPR {
				t.Fatalf("decision = %+v, want auto resolved to ppr", resp.Decision)
			}
			if r := knowledgeRank(resp, "lund"); r == 0 || r > 5 {
				t.Fatalf("answer passage at rank %d, want within the top 5: %+v", r, resp.Chunks)
			}
			if r := knowledgeRank(resp, "film"); r == 0 {
				t.Fatalf("the first-hop passage fell out of the results: %+v", resp.Chunks)
			}
		})
	}
}

func TestAutoStaysLexicalWhenTheQueryNamesNoEntity(t *testing.T) {
	for name, db := range lexicalBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			seedPPRKnowledge(t, ctx, db)

			resp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: "where was the orchestra director raised", TopK: 5})
			if err != nil {
				t.Fatalf("auto search: %v", err)
			}
			if resp.Decision.EffectiveMode != RetrievalModeLexical {
				t.Fatalf("decision = %+v, want lexical", resp.Decision)
			}
		})
	}
}

func TestAutoKeepsItsChoiceWhenAnEmbedderIsConfiguredOrAModeIsNamed(t *testing.T) {
	cases := []struct {
		name        string
		decision    RetrievalDecision
		hasEmbedder bool
		want        string
	}{
		{"auto picked graph, no embedder", RetrievalDecision{RequestedMode: RetrievalModeAuto, EffectiveMode: RetrievalModeGraph, UseGraph: true}, false, RetrievalModePPR},
		{"auto picked graph, embedder", RetrievalDecision{RequestedMode: RetrievalModeAuto, EffectiveMode: RetrievalModeGraph, UseGraph: true}, true, RetrievalModeGraph},
		{"graph named explicitly", RetrievalDecision{RequestedMode: RetrievalModeGraph, EffectiveMode: RetrievalModeGraph, UseGraph: true}, false, RetrievalModeGraph},
		{"auto stayed lexical", RetrievalDecision{RequestedMode: RetrievalModeAuto, EffectiveMode: RetrievalModeLexical}, false, RetrievalModeLexical},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.decision
			autoUsesWalk(&d, tc.hasEmbedder)
			if d.EffectiveMode != tc.want {
				t.Fatalf("effective mode = %q, want %q", d.EffectiveMode, tc.want)
			}
		})
	}
}

// The passage that answers the question was saved without the company as an
// entity — an extractor missed it, or the text names it by an alias — while
// six other passages are linked to the company but share no word with the
// question. Graph mode used to give every linked passage the count of query
// entities it names (1.0) next to lexical scores near 1/61, and the reranker
// normalised them together: the answer, the only lexical hit, fell behind all
// six and out of the top five.
func TestGraphModeKeepsTheLexicalMatchAheadOfPassagesOnlyLinkedToTheQueryEntity(t *testing.T) {
	for name, db := range lexicalBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			save := func(id, text string, entities ...string) {
				t.Helper()
				in := make([]ToolEntityInput, 0, len(entities))
				for _, e := range entities {
					in = append(in, ToolEntityInput{Name: e, ChunkIDs: []string{graphChunkNodeID(id, 0)}})
				}
				if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: id, Content: text, Entities: in}); err != nil {
					t.Fatalf("SaveKnowledge %s: %v", id, err)
				}
			}
			save("answer", "Zorblax Industries was founded by Mira Quell in 1921.", "Mira Quell")
			for i := 0; i < 6; i++ {
				save(fmt.Sprintf("linked-%d", i), fmt.Sprintf("The quarterly report number %d covered revenue and payroll.", i), "Zorblax Industries", fmt.Sprintf("Report %d", i))
			}

			resp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{
				Query: "Who founded Zorblax Industries?", TopK: 5, RetrievalMode: RetrievalModeGraph,
			})
			if err != nil {
				t.Fatalf("graph search: %v", err)
			}
			if r := knowledgeRank(resp, "answer"); r == 0 || r > 2 {
				t.Fatalf("answer passage at rank %d, want 1 or 2: %+v", r, resp.Chunks)
			}
			// The linked passages are still graph evidence, behind it.
			if knowledgeRank(resp, "linked-0") == 0 && knowledgeRank(resp, "linked-1") == 0 {
				t.Fatalf("no entity-linked passage returned at all: %+v", resp.Chunks)
			}
		})
	}
}

// Under RRF with the customary k=60 a passage two lists both rank fourth
// (2/64) outscores the one lexical search ranks first and the walk ranks
// twentieth (1/61 + 1/80) — on single-hop questions that is the walk, which
// spreads mass over every passage naming the question's entity, reordering
// a correct lexical head. pprFusionRRFK keeps rank 1 of either list ahead of
// anything both lists merely place in the middle.
func TestPPRFusionKeepsALexicalFirstAheadOfPassagesBothListsRankMidway(t *testing.T) {
	passages := map[string]*pprRankedPassage{
		"answer": {id: "answer", firstRank: 1, firstScore: 0.9, pprRank: 20, pprMass: 0.001},
		"middle": {id: "middle", firstRank: 4, firstScore: 0.5, pprRank: 4, pprMass: 0.05},
	}
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("walk-%d", i)
		passages[id] = &pprRankedPassage{id: id, pprRank: i, pprMass: 0.2 / float64(i)}
		id = fmt.Sprintf("lex-%d", i)
		passages[id] = &pprRankedPassage{id: id, firstRank: i + 1, firstScore: 0.8 / float64(i)}
	}
	fused := fusePPRRankings(passages, PPRFusionRRF)
	rank := map[string]int{}
	for i, p := range fused {
		rank[p.id] = i + 1
	}
	if rank["answer"] > rank["middle"] {
		t.Fatalf("lexical first ranked %d, behind the passage both lists rank fourth at %d: %v", rank["answer"], rank["middle"], rank)
	}
	if rank["answer"] > 2 {
		t.Fatalf("lexical first ranked %d, want 1 or 2 (only the walk's first may tie it): %v", rank["answer"], rank)
	}
}

// Thirty passages mention the question's entity, each also naming an entity
// of its own that one further passage mentions. Uncapped, the walk would read
// all thirty second-hop passages' edges; the retrieval walk expands at most
// pprMaxFrontier nodes a hop, which is what keeps its tail near lexical
// search's on a store with a few popular entities.
func TestTheRetrievalWalkExpandsAtMostItsFrontierCapPerHop(t *testing.T) {
	for name, db := range lexicalBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			const fanout = 30
			for i := 0; i < fanout; i++ {
				first, second := fmt.Sprintf("first-%02d", i), fmt.Sprintf("second-%02d", i)
				bridge := fmt.Sprintf("Bridge %02d", i)
				if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: first, Content: "Quorvane Hall hosted " + bridge + ".",
					Entities: []ToolEntityInput{
						{Name: "Quorvane Hall", ChunkIDs: []string{graphChunkNodeID(first, 0)}},
						{Name: bridge, ChunkIDs: []string{graphChunkNodeID(first, 0)}},
					}}); err != nil {
					t.Fatalf("SaveKnowledge %s: %v", first, err)
				}
				if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: second, Content: bridge + " later moved abroad.",
					Entities: []ToolEntityInput{{Name: bridge, ChunkIDs: []string{graphChunkNodeID(second, 0)}}}}); err != nil {
					t.Fatalf("SaveKnowledge %s: %v", second, err)
				}
			}
			opts := (&PPRRetrievalOptions{}).resolved()
			walked, err := db.runPPR(ctx, map[string]float64{EntityNodeID("Quorvane Hall"): 1}, opts, func(string) bool { return true })
			if err != nil {
				t.Fatalf("runPPR: %v", err)
			}
			reached := 0
			for _, w := range walked {
				for i := 0; i < fanout; i++ {
					if w.NodeID == graphChunkNodeID(fmt.Sprintf("second-%02d", i), 0) {
						reached++
					}
				}
			}
			if reached == 0 || reached > pprMaxFrontier {
				t.Fatalf("walk reached %d of %d second-hop passages, want between 1 and %d", reached, fanout, pprMaxFrontier)
			}
		})
	}
}
