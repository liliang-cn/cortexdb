package cortexdb

import (
	"context"
	"sort"
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
	vres, verr := db.SearchGraphRAG(ctx, query, opts)
	lres, lerr := db.GraphRAGTools().SearchGraphRAGLexical(ctx, lexReq)
	if verr != nil && lerr != nil {
		return nil, verr
	}

	lists := make([][]GraphRAGChunkResult, 0, 2)
	var base *GraphRAGQueryResult
	if verr == nil && vres != nil {
		lists = append(lists, vres.Chunks)
		base = vres
	}
	if lerr == nil && lres != nil {
		lists = append(lists, lres.Chunks)
		if base == nil {
			base = lres
		}
	}
	// A single successful path needs no fusion.
	if len(lists) == 1 {
		return base, nil
	}

	fused := packGraphRAGContext(fuseHybridChunks(lists, opts.TopK), opts)
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
	return out, nil
}

// fuseHybridChunks merges ranked chunk lists by reciprocal rank fusion: each
// chunk accrues 1/(k+rank) from every list it appears in, so chunks ranked high
// by either retriever — and especially by both — rise to the top. The fused
// score replaces Chunk.Score. Returns at most topK chunks (0 = all).
func fuseHybridChunks(lists [][]GraphRAGChunkResult, topK int) []GraphRAGChunkResult {
	type agg struct {
		chunk GraphRAGChunkResult
		score float64
	}
	byID := make(map[string]*agg)
	order := make([]string, 0)
	for _, list := range lists {
		for rank, c := range list {
			a, ok := byID[c.ID]
			if !ok {
				a = &agg{chunk: c}
				byID[c.ID] = a
				order = append(order, c.ID)
			}
			a.score += 1.0 / (hybridRRFK + float64(rank+1))
		}
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
