package cortexdb

import (
	"context"
	"sort"
	"sync"
)

// Why a recall returned what it did.
//
// The reason a hit is there, or a relevant one is missing, used to be
// reconstructed by hand from the code and a guess at the scores. A recall now
// keeps an account as it runs: the plan it searched with, what each retriever
// put forward and how it scored it, what each relevance floor and filter
// dropped, and the hits that came out. The account goes to the trace with the
// call (observability.RecallNote), into the recall log when that is on, and
// into the response when the caller asks for it (explain: true).
//
// A degraded answer says so in the response whether or not anybody asked for
// the account: a recall answered by keyword because the embedder was down
// used to look exactly like one where nothing was semantically close.

// Degraded reasons. Stable strings: a client and an alert rule match them.
const (
	// DegradedEmbedderUnreachable: the query could not be embedded (error or
	// timeout), so the semantic half did not run.
	DegradedEmbedderUnreachable = "embedder_unreachable"
	// DegradedVectorSearchFailed: the query was embedded but the vector
	// search failed.
	DegradedVectorSearchFailed = "vector_search_failed"
	// DegradedKeywordSearchFailed: the keyword search failed and the answer
	// is the semantic half alone.
	DegradedKeywordSearchFailed = "keyword_search_failed"
)

// Retriever names in an explain record.
const (
	explainVector  = "vector"
	explainKeyword = "keyword"
	explainGraph   = "graph"
	explainPPR     = "ppr"
)

// maxExplainCandidates bounds the candidates kept per retriever, and
// maxExplainDrops the drops kept per side: the account goes to a trace line
// per recall and must stay a few kilobytes.
const (
	maxExplainCandidates = 20
	maxExplainDrops      = 40
)

// RecallExplain is the account of one recall.
type RecallExplain struct {
	Plan      RecallExplainPlan  `json:"plan"`
	Memory    *RecallExplainSide `json:"memory,omitempty"`
	Knowledge *RecallExplainSide `json:"knowledge,omitempty"`
	// GraphFacts is how many entity relations the recall added.
	GraphFacts int      `json:"graph_facts,omitempty"`
	Degraded   []string `json:"degraded,omitempty"`
	// Final is the hits as returned, in order.
	Final []RecallExplainHit `json:"final"`
}

// RecallExplainPlan is what the recall searched with.
type RecallExplainPlan struct {
	Query            string   `json:"query"`
	Keywords         []string `json:"keywords,omitempty"`
	AlternateQueries []string `json:"alternate_queries,omitempty"`
	EntityNames      []string `json:"entity_names,omitempty"`
	RetrievalMode    string   `json:"retrieval_mode,omitempty"`
	// Planner is how the caller made the plan, as it said in the request:
	// "model", "fallback: timeout", ... Empty when it did not say.
	Planner string `json:"planner,omitempty"`
}

// RecallExplainSide is one half of a recall: memories or knowledge.
type RecallExplainSide struct {
	RequestedMode string `json:"requested_mode,omitempty"`
	EffectiveMode string `json:"effective_mode,omitempty"`
	Reason        string `json:"reason,omitempty"`
	// Candidates are each retriever's top candidates with its own raw score
	// (cosine for vector, keyword coverage or BM25 for keyword), in its order.
	Candidates map[string][]RecallCandidate `json:"candidates,omitempty"`
	// Fused is the list after the retrievers were merged and before the
	// floors, when that differs from the final hits.
	Fused []RecallCandidate `json:"fused,omitempty"`
	// Dropped is what a floor or filter removed, and DroppedTotal how many,
	// counting those past maxExplainDrops.
	Dropped      []RecallDrop   `json:"dropped,omitempty"`
	DroppedTotal map[string]int `json:"dropped_total,omitempty"`
}

// RecallCandidate is one row a retriever put forward.
type RecallCandidate struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
	Rank  int     `json:"rank"`
}

// RecallDrop is one row a floor or filter removed.
type RecallDrop struct {
	By        string  `json:"by"`
	ID        string  `json:"id"`
	Score     float64 `json:"score,omitempty"`
	Threshold float64 `json:"threshold,omitempty"`
}

// RecallExplainHit is one returned hit.
type RecallExplainHit struct {
	Kind       string   `json:"kind"` // memory, knowledge or chunk
	ID         string   `json:"id"`
	Score      float64  `json:"score"`
	Similarity float64  `json:"similarity,omitempty"`
	Coverage   float64  `json:"coverage,omitempty"`
	Via        []string `json:"via,omitempty"`
	Grade      string   `json:"grade,omitempty"`
}

// Floor and filter names in RecallDrop.By.
const (
	dropMemorySemanticFloor = "memory_semantic_floor"
	dropMemoryKeywordFloor  = "memory_keyword_floor"
	// dropOneWord: the hit covered enough of the question but shared one
	// word of it only, which is not an answer (rarityScorer.answers).
	dropOneWord               = "keyword_one_word"
	dropKnowledgeFloor        = "knowledge_vector_floor"
	dropKnowledgeKeywordFloor = "knowledge_keyword_floor"
	dropExpired               = "expired"
	dropSuperseded            = "superseded"
	dropUnreviewed            = "unreviewed"
	dropBelowMinGrade         = "below_min_grade"
)

// recallExplainer collects the account while the recall runs. Every method
// is free on a nil receiver, so a search outside an explained recall pays a
// context lookup and nothing else.
type recallExplainer struct {
	mu   sync.Mutex
	side map[string]*RecallExplainSide
}

type recallExplainerKey struct{}

func withRecallExplainer(ctx context.Context) (context.Context, *recallExplainer) {
	if e := recallExplainerOf(ctx); e != nil {
		return ctx, e
	}
	e := &recallExplainer{side: map[string]*RecallExplainSide{}}
	return context.WithValue(ctx, recallExplainerKey{}, e), e
}

func recallExplainerOf(ctx context.Context) *recallExplainer {
	if ctx == nil {
		return nil
	}
	e, _ := ctx.Value(recallExplainerKey{}).(*recallExplainer)
	return e
}

// Sides of a recall.
const (
	explainMemory    = "memory"
	explainKnowledge = "knowledge"
)

func (e *recallExplainer) sideLocked(side string) *RecallExplainSide {
	s := e.side[side]
	if s == nil {
		s = &RecallExplainSide{}
		e.side[side] = s
	}
	return s
}

// candidates records what retriever put forward on side, in order.
func (e *recallExplainer) candidates(side, retriever string, list []RecallCandidate) {
	if e == nil || len(list) == 0 {
		return
	}
	if len(list) > maxExplainCandidates {
		list = list[:maxExplainCandidates]
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.sideLocked(side)
	if s.Candidates == nil {
		s.Candidates = map[string][]RecallCandidate{}
	}
	s.Candidates[retriever] = append(s.Candidates[retriever], list...)
}

// fused records the merged list before the floors.
func (e *recallExplainer) fused(side string, list []RecallCandidate) {
	if e == nil {
		return
	}
	if len(list) > maxExplainCandidates {
		list = list[:maxExplainCandidates]
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sideLocked(side).Fused = list
}

// dropped records that by removed id.
func (e *recallExplainer) dropped(side, by, id string, score, threshold float64) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.sideLocked(side)
	if s.DroppedTotal == nil {
		s.DroppedTotal = map[string]int{}
	}
	s.DroppedTotal[by]++
	if len(s.Dropped) < maxExplainDrops {
		s.Dropped = append(s.Dropped, RecallDrop{By: by, ID: id, Score: score, Threshold: threshold})
	}
}

// decision records how side chose its retrievers.
func (e *recallExplainer) decision(side string, d RetrievalDecision) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.sideLocked(side)
	s.RequestedMode, s.EffectiveMode, s.Reason = d.RequestedMode, d.EffectiveMode, d.Reason
}

// floorDrops is the drop count per floor across both sides.
func (e *recallExplainer) floorDrops() map[string]int {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[string]int{}
	for _, s := range e.side {
		for by, n := range s.DroppedTotal {
			out[by] += n
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (e *recallExplainer) sideCopy(side string) *RecallExplainSide {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.side[side]
	if s == nil {
		return nil
	}
	c := *s
	return &c
}

// memoryCandidates turns hits into candidates scored by score.
func memoryCandidates(hits []MemorySearchHit, score func(MemorySearchHit) float64) []RecallCandidate {
	out := make([]RecallCandidate, 0, min(len(hits), maxExplainCandidates))
	for i, h := range hits {
		if i == maxExplainCandidates {
			break
		}
		out = append(out, RecallCandidate{ID: h.Memory.ID, Score: score(h), Rank: i + 1})
	}
	return out
}

// chunkCandidates turns chunks into candidates scored by score.
func chunkCandidates(chunks []GraphRAGChunkResult, score func(GraphRAGChunkResult) float64) []RecallCandidate {
	out := make([]RecallCandidate, 0, min(len(chunks), maxExplainCandidates))
	for i, c := range chunks {
		if i == maxExplainCandidates {
			break
		}
		out = append(out, RecallCandidate{ID: c.ID, Score: score(c), Rank: i + 1})
	}
	return out
}

// addDegraded adds reason to list once, keeping the list sorted.
func addDegraded(list []string, reasons ...string) []string {
	for _, r := range reasons {
		found := false
		for _, have := range list {
			if have == r {
				found = true
				break
			}
		}
		if !found {
			list = append(list, r)
		}
	}
	sort.Strings(list)
	return list
}

// build assembles the account of resp.
func (e *recallExplainer) build(req KnowledgeMemoryRecallRequest, resp *KnowledgeMemoryRecallResponse) *RecallExplain {
	plan := resp.MemoryPlan
	if plan.Query == "" {
		plan = resp.KnowledgePlan
	}
	out := &RecallExplain{
		Plan: RecallExplainPlan{
			Query:            resp.Query,
			Keywords:         plan.Keywords,
			AlternateQueries: plan.AlternateQueries,
			EntityNames:      plan.EntityNames,
			RetrievalMode:    plan.RetrievalMode,
			Planner:          req.Planner,
		},
		GraphFacts: len(resp.GraphFacts),
		Degraded:   resp.Degraded,
		Final:      []RecallExplainHit{},
	}
	if !req.DisableMemory {
		e.decision(explainMemory, resp.MemoryDecision)
		out.Memory = e.sideCopy(explainMemory)
	}
	if !req.DisableKnowledge {
		e.decision(explainKnowledge, resp.KnowledgeDecision)
		out.Knowledge = e.sideCopy(explainKnowledge)
	}
	for _, h := range resp.Memories {
		out.Final = append(out.Final, RecallExplainHit{Kind: "memory", ID: h.Memory.ID, Score: h.Score, Similarity: h.Similarity, Coverage: h.Coverage, Via: h.Via, Grade: h.Memory.Grade})
	}
	for _, h := range resp.Knowledge {
		out.Final = append(out.Final, RecallExplainHit{Kind: "knowledge", ID: h.KnowledgeID, Score: h.Score, Grade: normalizeGrade(h.Metadata[KeyGrade])})
	}
	return out
}
