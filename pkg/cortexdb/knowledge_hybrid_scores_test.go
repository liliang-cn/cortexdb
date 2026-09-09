package cortexdb

import (
	"context"
	"path/filepath"
	"testing"
)

// A fused result must say what each retriever thought, not only where the
// fusion put it.
//
// Reciprocal rank fusion at k=60 scores the top three results 1/61, 1/62 and
// 1/63 — a band five ten-thousandths wide that rounds to one number whatever
// the corpus and whatever either retriever's confidence was. The fused score
// is the right thing to ORDER by and the wrong thing to READ, and it used to
// be the only thing kept: the vector similarity and the lexical score were
// overwritten on the way through. They survive now, beside the rank each list
// gave the chunk, so a caller comparing two runs looks at the ranks rather
// than at a column that only looks like evidence.
func TestFusedChunksKeepEachRetrieversScoreAndRank(t *testing.T) {
	vector := []GraphRAGChunkResult{
		{ID: "a", Score: 0.91},
		{ID: "b", Score: 0.40},
	}
	lexical := []GraphRAGChunkResult{
		{ID: "b", Score: 7.5},
		{ID: "c", Score: 2.0},
	}

	fused := fuseHybridChunks(vector, lexical, 0)
	byID := make(map[string]GraphRAGChunkResult, len(fused))
	for _, c := range fused {
		byID[c.ID] = c
	}

	// b is in both lists and fuses highest. Its component scores are the ones
	// each list gave it, and its ranks are its positions in each list.
	b := byID["b"]
	if b.VectorScore != 0.40 || b.LexicalScore != 7.5 {
		t.Fatalf("b lost a component score: vector=%v lexical=%v", b.VectorScore, b.LexicalScore)
	}
	if b.VectorRank != 2 || b.LexicalRank != 1 {
		t.Fatalf("b's ranks = vector #%d lexical #%d, want #2 and #1", b.VectorRank, b.LexicalRank)
	}
	// a was found only by the vector retriever: its lexical side is absent,
	// which is zero rank rather than a zero score somebody might read as
	// "scored zero".
	a := byID["a"]
	if a.VectorScore != 0.91 || a.VectorRank != 1 {
		t.Fatalf("a's vector side = %v #%d", a.VectorScore, a.VectorRank)
	}
	if a.LexicalRank != 0 {
		t.Fatalf("a was never in the lexical list but carries lexical rank %d", a.LexicalRank)
	}
	// The fused score is still the RRF value: nothing about ordering changed.
	if fused[0].ID != "b" {
		t.Fatalf("fusion order changed: %v", ids(fused))
	}
	wantB := 1.0/(hybridRRFK+2) + 1.0/(hybridRRFK+1)
	if diff := b.Score - wantB; diff > 1e-12 || diff < -1e-12 {
		t.Fatalf("b's fused score = %v, want the RRF sum %v", b.Score, wantB)
	}
}

// The document-level hit carries the same, so a caller of SearchKnowledge —
// which is what every tool and every client reaches — does not have to dig
// into chunks to learn what the retrievers thought.
func TestSearchHitsCarryRankAndComponentScores(t *testing.T) {
	cfg := DefaultConfig(filepath.Join(t.TempDir(), "hybrid.db"))
	cfg.Dimensions = 4
	db, err := Open(cfg, WithEmbedder(newKeywordEmbedder("quorum", "replica", "backup", "pool")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	for id, body := range map[string]string{
		"rb-quorum": "A replica set with quorum refuses writes when it loses its majority.",
		"rb-pool":   "A thin pool that fills up fails every backup volume in it at once.",
	} {
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
			KnowledgeID: id, Content: body, Collection: "rb",
		}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}

	hits, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{
		Query: "quorum replica majority", Collection: "rb", TopK: 2,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits.Results) < 2 {
		t.Fatalf("expected both notes, got %d", len(hits.Results))
	}
	for i, hit := range hits.Results {
		if hit.Rank != i+1 {
			t.Fatalf("hit %d carries rank %d", i+1, hit.Rank)
		}
	}
	top := hits.Results[0]
	if top.KnowledgeID != "rb-quorum" {
		t.Fatalf("the quorum note should rank first, got %s", top.KnowledgeID)
	}
	if top.VectorScore <= 0 && top.LexicalScore <= 0 {
		t.Fatalf("the top hit carries neither retriever's score: %+v", top)
	}
}

func ids(chunks []GraphRAGChunkResult) []string {
	out := make([]string, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, c.ID)
	}
	return out
}
