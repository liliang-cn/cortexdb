package cortexdb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
)

// hybridRRFK is the reciprocal-rank-fusion constant. 60 is the widely-used
// default from the original RRF paper; larger values flatten the rank weighting.
const hybridRRFK = 60.0

// searchKnowledgeHybrid runs vector and lexical retrieval and fuses their chunk
// rankings with reciprocal rank fusion, so exact-keyword hits (BM25) and
// semantic hits (vector) reinforce each other instead of one path silently
// losing the matches the other would have found. If one path errors, the other
// is used alone; only if both fail does it return an error.
func (db *DB) searchKnowledgeHybrid(ctx context.Context, query string, opts GraphRAGQueryOptions, lexReq ToolSearchGraphRAGLexicalRequest) (*GraphRAGQueryResult, error) {
	// Side by side, not one after the other: the vector path waits on the
	// embedder, which may be a model on another machine, and the lexical path
	// needs nothing from it.
	var lres *GraphRAGQueryResult
	var lerr error
	lexicalDone := make(chan struct{})
	go func() {
		defer close(lexicalDone)
		lres, lerr = db.GraphRAGTools().SearchGraphRAGLexical(ctx, lexReq)
	}()
	vres, verr := db.SearchGraphRAG(withKeywordFallback(ctx), query, opts)
	<-lexicalDone
	if verr != nil && lerr != nil {
		return nil, verr
	}
	ex := recallExplainerOf(ctx)

	// A single successful path needs no fusion.
	if verr != nil {
		// Said in the answer, not only the log: the caller gets a keyword
		// answer and must be able to tell it from a semantic one.
		reason := DegradedVectorSearchFailed
		if errors.Is(verr, ErrEmbeddingFailed) {
			reason = DegradedEmbedderUnreachable
		}
		slog.Warn("cortexdb: knowledge semantic search fallback to lexical", observability.LogArgs(ctx, "err", verr)...)
		ex.candidates(explainKnowledge, explainKeyword, chunkCandidates(lres.Chunks, func(c GraphRAGChunkResult) float64 { return c.Score }))
		lres.Degraded = addDegraded(lres.Degraded, reason)
		lres.Decision.EffectiveMode = RetrievalModeLexical
		lres.Decision.Reason = fmt.Sprintf("vector search failed (%v); answered by keyword search only", verr)
		return lres, nil
	}
	ex.candidates(explainKnowledge, explainVector, chunkCandidates(vres.Chunks, func(c GraphRAGChunkResult) float64 { return c.Score }))
	if lerr != nil {
		// Marked as the vector search's own, as fusion would have, so the
		// knowledge floor reads their cosine.
		for i := range vres.Chunks {
			vres.Chunks[i].VectorScore, vres.Chunks[i].VectorRank = vres.Chunks[i].Score, i+1
		}
		vres.Degraded = addDegraded(vres.Degraded, DegradedKeywordSearchFailed)
		return vres, nil
	}
	ex.candidates(explainKnowledge, explainKeyword, chunkCandidates(lres.Chunks, func(c GraphRAGChunkResult) float64 { return c.Score }))
	base := vres

	var fused []GraphRAGChunkResult
	if isBareKeyword(query) && len(lres.Chunks) > 0 {
		fused = keywordFirstChunks(vres.Chunks, lres.Chunks, opts.TopK)
	} else {
		fused = fuseHybridChunks(vres.Chunks, lres.Chunks, opts.TopK)
	}
	fused = packGraphRAGContext(fused, opts)
	ex.fused(explainKnowledge, chunkCandidates(fused, func(c GraphRAGChunkResult) float64 { return c.Score }))
	out := &GraphRAGQueryResult{
		Query:    base.Query,
		Plan:     base.Plan,
		Decision: base.Decision,
		Chunks:   fused,
		Entities: orderedUniqueNonEmptyStrings(append(append([]string{}, vres.Entities...), lres.Entities...)),
		Context:  buildGraphRAGContext(fused),
	}
	out.Decision.EffectiveMode = RetrievalModeHybrid
	out.Decision.Reason = "auto used hybrid retrieval (vector + lexical RRF fusion) because an embedder is available"
	// Widened here too, because an option that works on one retrieval path and
	// is quietly ignored on another is worse than not having it: the caller
	// sets ChunkWindow, reads a fragment back, and has no way to tell whether
	// the window was too small or was never applied.
	if err := db.widenGraphRAGContext(ctx, out, opts); err != nil {
		return nil, err
	}
	return out, nil
}

// fuseHybridChunks merges the two ranked lists by reciprocal rank fusion: a
// chunk accrues 1/(k+rank) from every list it appears in, so chunks ranked high
// by either retriever — and especially by both — rise to the top. The fused
// value replaces Chunk.Score; what each retriever thought is kept beside it as
// VectorScore/VectorRank and LexicalScore/LexicalRank, because the fused value
// is the right thing to order by and the wrong thing to read. Returns at most
// topK chunks (0 = all).
func fuseHybridChunks(vector, lexical []GraphRAGChunkResult, topK int) []GraphRAGChunkResult {
	type agg struct {
		chunk GraphRAGChunkResult
		score float64
	}
	byID := make(map[string]*agg)
	order := make([]string, 0)
	take := func(c GraphRAGChunkResult) *agg {
		a, ok := byID[c.ID]
		if !ok {
			a = &agg{chunk: c}
			byID[c.ID] = a
			order = append(order, c.ID)
		}
		return a
	}
	for rank, c := range vector {
		a := take(c)
		a.chunk.VectorScore, a.chunk.VectorRank = c.Score, rank+1
		a.score += 1.0 / (hybridRRFK + float64(rank+1))
	}
	for rank, c := range lexical {
		a := take(c)
		a.chunk.LexicalScore, a.chunk.LexicalRank = c.Score, rank+1
		a.score += 1.0 / (hybridRRFK + float64(rank+1))
	}
	aggs := make([]*agg, 0, len(order))
	for _, id := range order {
		aggs = append(aggs, byID[id])
	}
	sort.SliceStable(aggs, func(i, j int) bool { return aggs[i].score > aggs[j].score })

	out := make([]GraphRAGChunkResult, 0, len(aggs))
	for i, a := range aggs {
		if topK > 0 && i >= topK {
			break
		}
		c := a.chunk
		c.Score = a.score
		out = append(out, c)
	}
	return out
}

// keywordFirstChunks answers a bare keyword (see isBareKeyword) with the
// chunks that contain it, in keyword order, and fills the rest from the
// vector list. A one-word query embeds near whatever is thinnest: asking "HA"
// put a chunk holding only ")" first.
func keywordFirstChunks(vector, lexical []GraphRAGChunkResult, topK int) []GraphRAGChunkResult {
	fused := fuseHybridChunks(vector, lexical, 0)
	sort.SliceStable(fused, func(i, j int) bool {
		a, b := fused[i].LexicalRank > 0, fused[j].LexicalRank > 0
		if a != b {
			return a
		}
		if a {
			return fused[i].LexicalRank < fused[j].LexicalRank
		}
		return false
	})
	if topK > 0 && len(fused) > topK {
		fused = fused[:topK]
	}
	// The score is the reciprocal rank of the place given here, so whatever
	// sorts by score afterwards keeps this order.
	for i := range fused {
		fused[i].Score = 1.0 / (hybridRRFK + float64(i+1))
	}
	return fused
}

type keywordFallbackKey struct{}

// withKeywordFallback marks a search whose caller runs a keyword search
// beside it, so its query embedding may give up early (see SearchGraphRAG).
func withKeywordFallback(ctx context.Context) context.Context {
	return context.WithValue(ctx, keywordFallbackKey{}, true)
}

func keywordFallback(ctx context.Context) bool {
	v, _ := ctx.Value(keywordFallbackKey{}).(bool)
	return v
}
