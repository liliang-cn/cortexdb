package main

import (
	"context"
	"fmt"
	"math"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Retrieval modes the harness measures. Each maps onto one public facade call,
// so a number here is what an application calling that API would get.
const (
	// modeLexical opens the database with no embedder at all and searches
	// knowledge in lexical mode: the default install, and the path the shared
	// brain's golden set measures.
	modeLexical = "lexical"
	// modeVector is SearchGraphRAG with graph expansion off: vector seeds and
	// the library's rerank, nothing lexical fused in.
	modeVector = "vector"
	// modeHybrid is SearchKnowledge in hybrid mode: vector and lexical
	// rankings fused by reciprocal rank.
	modeHybrid = "hybrid"
	// modeGraph is SearchKnowledge in graph mode: the hybrid (or, without an
	// embedder, lexical) seeds plus expansion over the entity graph that
	// SaveKnowledge builds with the library's own extractor.
	modeGraph = "graph"
	// modePPR is SearchKnowledge in ppr mode: Personalized PageRank over the
	// same entity graph, seeded by the query's entities, fused with a hybrid
	// (or, without an embedder, lexical) first stage — HippoRAG 2's method.
	modePPR = "ppr"
	// modeAuto is SearchKnowledge with no mode named — what a caller who does
	// not choose gets, and so the number that matters most. The library picks
	// the strategy from the query and from whether an embedder is set.
	modeAuto = "auto"
)

var allModes = []string{modeLexical, modeVector, modeHybrid, modeGraph, modePPR, modeAuto}

// Searches rank chunks; the benchmarks score documents. A document of many
// chunks — a LongMemEval session runs to fifteen — can fill the top of a chunk
// ranking on its own, so asking for n chunks returns fewer than n documents
// and would score a retriever on a list shorter than the cutoff. retrieve
// therefore starts at overFetch chunks per document wanted and doubles until it
// has n documents, the ranking stops growing, or maxOverFetch is reached.
const (
	overFetch    = 2
	maxOverFetch = 32
)

// noContextBudget lifts the character budget the facade uses to pack an LLM
// context (2,400 characters by default). That budget decides how many results
// come back, and a benchmark measures the ranking, not how much of it fits in
// a prompt: with LongMemEval's ~700-character chunks the default would return
// three documents and make recall@10 unmeasurable.
const noContextBudget = math.MaxInt32

// retrieve returns up to n distinct knowledge ids for the query, best first.
func retrieve(ctx context.Context, db *cortexdb.DB, mode, query string, n int) ([]string, error) {
	var best []string
	for chunks := n * overFetch; ; chunks *= 2 {
		ids, err := search(ctx, db, mode, query, chunks)
		if err != nil {
			return nil, err
		}
		got := firstDistinct(ids, n)
		if len(got) >= n || len(got) <= len(best) || chunks >= n*maxOverFetch {
			if len(got) < len(best) {
				return best, nil
			}
			return got, nil
		}
		best = got
	}
}

// search runs one facade search for the given number of chunks and returns
// the knowledge ids of the results in rank order. One chunk per document is
// allowed, so the chunk budget goes to distinct documents.
func search(ctx context.Context, db *cortexdb.DB, mode, query string, chunks int) ([]string, error) {
	var ids []string
	switch mode {
	case modeVector:
		res, err := db.SearchGraphRAG(ctx, query, cortexdb.GraphRAGQueryOptions{
			TopK:             chunks,
			RetrievalMode:    cortexdb.RetrievalModeLexical,
			DisableGraph:     true,
			PerDocumentLimit: 1,
			MaxContextChunks: chunks,
			MaxContextChars:  noContextBudget,
		})
		if err != nil {
			return nil, err
		}
		for _, c := range res.Chunks {
			ids = append(ids, c.DocumentID)
		}
	case modeLexical, modeHybrid, modeGraph, modePPR, modeAuto:
		retrievalMode := map[string]string{
			modeLexical: cortexdb.RetrievalModeLexical,
			modeHybrid:  cortexdb.RetrievalModeHybrid,
			modeGraph:   cortexdb.RetrievalModeGraph,
			modePPR:     cortexdb.RetrievalModePPR,
			modeAuto:    cortexdb.RetrievalModeAuto,
		}[mode]
		res, err := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{
			Query:            query,
			TopK:             chunks,
			RetrievalMode:    retrievalMode,
			PerDocumentLimit: 1,
			MaxContextChunks: chunks,
			MaxContextChars:  noContextBudget,
		})
		if err != nil {
			return nil, err
		}
		for _, h := range res.Results {
			ids = append(ids, h.KnowledgeID)
		}
	default:
		return nil, fmt.Errorf("unknown mode %q", mode)
	}
	return ids, nil
}

// firstDistinct keeps the first n distinct ids in order.
func firstDistinct(ids []string, n int) []string {
	out := make([]string, 0, n)
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
		if len(out) == n {
			break
		}
	}
	return out
}
