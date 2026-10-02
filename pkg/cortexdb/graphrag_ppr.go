package cortexdb

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Personalized PageRank retrieval, after HippoRAG 2.
//
// The graph and graph-light modes expand a fixed number of hops out of the
// seed chunks and keep whatever they meet, scored by how close it was. That
// treats every neighbour of a hub alike, and it cannot tell a passage two hops
// from the question through one bridge entity from a passage two hops away
// through the most-mentioned entity in the store. A seeded random walk can:
// mass leaves the question's entities, splits at every node in proportion to
// edge weight, and pools where several short paths from the question meet.
// The passage that answers the second hop of a two-hop question sits exactly
// there — reachable from the question's entity through a passage that names
// both — while lexical retrieval cannot see it at all, because it shares no
// words with the question.
//
// So RetrievalModePPR is a re-ranking of the passages the graph reaches:
//
//  1. First stage: the retrieval the store would have done anyway (hybrid
//     when an embedder is configured, lexical otherwise), without graph
//     expansion, over a wider pool.
//  2. Seeds: the entities the query names — the plan's entity_names plus any
//     entity whose name appears in the query text — each weighted by
//     1/(passages that mention it), HippoRAG's node specificity, so a name on
//     every page cannot drown out the rare one the question hinges on. The
//     first-stage passages seed the walk too when the query names no entity,
//     and always under HippoRAG 2's own fusion (its "dense-sparse
//     integration"), at a small weight.
//  3. One bounded PPR over the seeds' neighbourhood (pkg/graph).
//  4. Passages ranked by PPR mass and fused with the first stage by RRF.
//
// Through the public API (cortexdb-bench, full sets, no embedder) auto's
// recall@5 against lexical is 2WikiMultiHopQA 0.807 vs 0.657, MuSiQue 0.542
// vs 0.457, LoCoMo 0.495 vs 0.493 and LongMemEval 0.862 vs 0.861.
//
// Measured without an embedder on held-out questions (ppr_bench_test.go),
// supporting-passage recall@5 against lexical: 2WikiMultiHopQA 0.830 vs
// 0.649, MuSiQue 0.563 vs 0.462, and single-hop questions (MuSiQue's first
// sub-questions) 0.866 vs 0.860, with p95 under twice lexical's. That is why
// auto uses the walk on a no-embedder knowledge search whose query names an
// entity (autoUsesWalk). It took two changes to get there: the fusion
// constant (pprFusionRRFK) — at RRF's usual 60 single-hop was 0.830 — and the
// frontier cap (pprMaxFrontier), without which MuSiQue's p95 was over 200 ms.
// HippoRAG 2's own pure-PPR ranking, before both, scored 0.699, 0.498 and
// 0.864: it barely moves single-hop, and barely helps multi-hop, because it
// seeds its walk from a dense retriever over the whole corpus, and seeded
// from a 20-passage lexical pool the passage teleport mostly re-ranks what
// lexical already found. Hence RRF as the default fusion.
//
// It needs no embedder: entity matching is lexical and the walk is pure
// graph, so a no-embedder store gets multi-hop retrieval from the same
// mentions edges its ingest already writes.

// RetrievalModePPR ranks passages by Personalized PageRank over the entity
// graph, seeded by the query's entities and the first-stage passages. It
// pays off on multi-hop questions, where the answer passage shares no words
// with the question; auto chooses it without an embedder (autoUsesWalk).
const RetrievalModePPR = "ppr"

// PPR fusion schemes.
const (
	// PPRFusionRRF, the default, treats the walk as a second, independent
	// retriever and fuses it with the first stage by reciprocal rank fusion.
	// The walk is then seeded by the query's entities alone — seeding it with
	// the first-stage passages too would put every one of them in both lists,
	// and RRF could never rank a passage only the walk found above any of
	// them. When the query names no entity, the passages seed it after all,
	// because a walk from nothing reaches nothing.
	PPRFusionRRF = "rrf"
	// PPRFusionPPR ranks by PPR mass alone, HippoRAG 2's scheme: the first
	// stage speaks only through the teleport weight it gives its passages.
	// Kept selectable because it is the published method and an embedder's
	// first stage may suit it better than a lexical one does.
	PPRFusionPPR = "ppr"
)

const (
	// pprDefaultPassageSeedWeight is HippoRAG 2's passage node weight: a
	// first-stage passage teleports at 5% of what a query entity does.
	// 0.01 and 0.2 measured within a point of it.
	pprDefaultPassageSeedWeight = 0.05
	// pprMinPool is the smallest first-stage pool. The pool is both the
	// passage seeds and the first list RRF fuses, so it is wider than top_k.
	pprMinPool = 20
	// pprMaxCandidates bounds how many of the walk's best nodes are looked up
	// as passages, which is the only per-candidate cost after the walk.
	pprMaxCandidates = 200
	// pprMaxQueryTokens and pprMaxNGram bound the entity names tried from
	// the query text — at most a few hundred primary-key lookups.
	pprMaxQueryTokens = 64
	pprMaxNGram       = 8
	// pprFusionRRFK is the RRF constant of the walk/first-stage fusion. The
	// customary 60 makes ranks nearly flat — rank 1 and rank 2 differ by
	// 0.4% — so a passage both lists put around twentieth outscored the one
	// either put first, and the walk, which spreads its mass over every
	// passage that names the question's entity, reordered a correct lexical
	// head: single-hop recall@5 fell from 0.857 to 0.833. At 2 the fusion is
	// close to an interleave: rank 1 of either list stays ahead unless both
	// lists put another passage in their top three. Measured on the dev
	// splits, k from 1 to 5 is one plateau (2Wiki 0.83-0.84, single-hop
	// 0.857-0.863), 10 begins to slide, 60 is 0.803 and 0.833.
	pprFusionRRFK = 2.0
	// pprMaxFrontier is how many nodes each hop of the walk's subgraph
	// expands (graph.PPROptions.MaxFrontier). Uncapped, a question seeded
	// from twenty-odd passages grew to the 20,000-node cap, reading every
	// edge of a few thousand passages to get there, and that read was the
	// whole of a 200-400 ms tail. The frontier is the nodes a forward push
	// estimates the walk reaches with most mass, so what is cut carries
	// least of it: on the dev splits recall@5 is unchanged from 200 down to
	// 20 and drops a point at 10, while p95 falls to the lexical range.
	pprMaxFrontier = 20
	// pprHubMinMentions is the fewest mentions at which an entity can be a
	// hub (pprHubShare): below it every entity is specific enough to seed
	// from, however small the store.
	pprHubMinMentions = 20
)

// pprHubShare is the share of a store's passages above which an entity the
// query names is not a seed. Specificity weighting alone does not handle it:
// it lowers a hub's weight against other seeds, but when the hub is the only
// entity a question names, the walk starts from it all the same, spreads
// over most of the store, and its ranking is noise that RRF still gives a
// vote. That is a conversation, where every turn names its speaker: on
// LoCoMo "Caroline" is mentioned by 77% of her conversation's turns, and
// walking from her cost 10 points of recall@1.
var pprHubShare = 0.10

// PPRRetrievalOptions tunes RetrievalModePPR. The zero value is the measured
// default; every field is optional.
type PPRRetrievalOptions struct {
	// Fusion is PPRFusionRRF (default) or PPRFusionPPR.
	Fusion string `json:"fusion,omitempty"`
	// Damping is the probability of following an edge; default 0.5.
	Damping float64 `json:"damping,omitempty"`
	// PassageSeedWeight scales the first-stage passages' teleport weight
	// relative to a query entity's; default 0.05. Negative disables passage
	// seeding.
	PassageSeedWeight float64 `json:"passage_seed_weight,omitempty"`
	// EdgeTypeWeights overrides the per-edge-type factors of
	// DefaultPPREdgeTypeWeights; a factor <= 0 removes that type from the walk.
	EdgeTypeWeights map[string]float64 `json:"edge_type_weights,omitempty"`
}

// DefaultPPREdgeTypeWeights is how much each structural edge type counts in
// the walk relative to a mention or an extracted relation (factor 1).
//
// The structural edges are not facts about the world: has_chunk ties every
// passage of a document to one hub, next ties neighbouring passages, and
// co_occurs is a statistical guess. Left at full weight they leak mass into
// whatever happens to be beside a seed. They are not removed, because a
// sibling passage of the same document is genuinely a little relevant.
func DefaultPPREdgeTypeWeights() map[string]float64 {
	return map[string]float64{
		"has_chunk": 0.25,
		"next":      0.25,
		"co_occurs": 0.5,
	}
}

func (o *PPRRetrievalOptions) resolved() PPRRetrievalOptions {
	var out PPRRetrievalOptions
	if o != nil {
		out = *o
	}
	switch strings.ToLower(strings.TrimSpace(out.Fusion)) {
	case PPRFusionPPR:
		out.Fusion = PPRFusionPPR
	default:
		out.Fusion = PPRFusionRRF
	}
	if out.Damping <= 0 || out.Damping >= 1 {
		out.Damping = graph.DefaultPPRDamping
	}
	if out.PassageSeedWeight == 0 {
		out.PassageSeedWeight = pprDefaultPassageSeedWeight
	}
	weights := DefaultPPREdgeTypeWeights()
	for k, v := range out.EdgeTypeWeights {
		weights[k] = v
	}
	out.EdgeTypeWeights = weights
	return out
}

// pprRankedPassage is one passage with its place in each ranking.
type pprRankedPassage struct {
	id         string
	firstRank  int // 1-based; 0 = not in the first stage
	firstScore float64
	pprRank    int // 1-based among passages; 0 = the walk did not reach it
	pprMass    float64
	fused      float64 // the score fusePPRRankings ordered by
}

// pprSeeds builds the teleport vector: query entities weighted by
// specificity, and — under PPR fusion, or when no entity matched — the
// first-stage passages weighted by their normalised score.
func (db *DB) pprSeeds(ctx context.Context, query string, entityNames []string, passageNodeType string, passageIDs []string, passageScores []float64, opts PPRRetrievalOptions) (map[string]float64, []string, error) {
	entityIDs, counts, err := db.seedEntities(ctx, query, entityNames, passageNodeType)
	if err != nil {
		return nil, nil, err
	}
	seeds := make(map[string]float64, len(entityIDs)+len(passageIDs))
	if len(entityIDs) > 0 {
		for _, id := range entityIDs {
			n := counts[id]
			if n < 1 {
				n = 1
			}
			seeds[id] += 1 / float64(n)
		}
	}
	seedPassages := opts.Fusion == PPRFusionPPR || len(entityIDs) == 0
	if seedPassages && opts.PassageSeedWeight > 0 && len(passageIDs) > 0 {
		lo, hi := passageScores[0], passageScores[0]
		for _, s := range passageScores {
			lo = min(lo, s)
			hi = max(hi, s)
		}
		for i, id := range passageIDs {
			// Min-max like HippoRAG 2, floored so the last passage of the pool
			// is still a seed — it was retrieved, after all.
			norm := 1.0
			if hi > lo {
				norm = 0.1 + 0.9*(passageScores[i]-lo)/(hi-lo)
			}
			seeds[id] += opts.PassageSeedWeight * norm
		}
	}
	return seeds, entityIDs, nil
}

// queryEntityNodeIDs returns the entity nodes a query names, in id order: the
// planner's entity_names (alias-aware), and every word n-gram of the query
// text that is the name of an entity node. Overlapping matches keep the
// longest — "Lothair II" rather than also "Lothair" — because the shorter one
// is usually a different, vaguer entity the question did not mean.
func (db *DB) queryEntityNodeIDs(ctx context.Context, query string, entityNames []string) ([]string, error) {
	found := make(map[string]struct{})
	hinted := make([]string, 0, len(entityNames))
	for _, name := range entityNames {
		if id := db.resolveEntityNameToNode(ctx, strings.TrimSpace(name)); id != "" {
			hinted = append(hinted, id)
		}
	}

	type span struct {
		id         string
		start, end int
	}
	tokens := queryEntityTokens(query)
	var spans []span
	candidateIDs := make([]string, 0, len(tokens)*4)
	seen := make(map[string]struct{})
	for i := range tokens {
		for n := 1; n <= pprMaxNGram && i+n <= len(tokens); n++ {
			name := joinEntityTokens(tokens[i : i+n])
			if n == 1 && !plausibleSingleTokenEntity(tokens[i]) {
				continue
			}
			if !anyDistinctiveToken(tokens[i : i+n]) {
				continue
			}
			id := graphEntityNodeID(name)
			spans = append(spans, span{id: id, start: i, end: i + n})
			if _, dup := seen[id]; !dup {
				seen[id] = struct{}{}
				candidateIDs = append(candidateIDs, id)
			}
		}
	}
	candidateIDs = append(candidateIDs, hinted...)
	if len(candidateIDs) == 0 {
		return nil, nil
	}
	present, err := db.graph.ExistingNodeIDs(ctx, candidateIDs)
	if err != nil {
		return nil, fmt.Errorf("match query entities: %w", err)
	}
	for _, id := range hinted {
		if present[id] {
			found[id] = struct{}{}
		}
	}

	matched := spans[:0]
	for _, s := range spans {
		if present[s.id] {
			matched = append(matched, s)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		li, lj := matched[i].end-matched[i].start, matched[j].end-matched[j].start
		if li != lj {
			return li > lj
		}
		return matched[i].start < matched[j].start
	})
	var kept []span
	for _, s := range matched {
		inside := false
		for _, k := range kept {
			if s.start >= k.start && s.end <= k.end {
				inside = true
				break
			}
		}
		if !inside {
			kept = append(kept, s)
			found[s.id] = struct{}{}
		}
	}
	return sortedKeys(found), nil
}

// queryEntityToken is one word of a query, or one CJK character: Chinese
// names are not space-delimited, so every run of characters is a candidate.
type queryEntityToken struct {
	text string
	cjk  bool
}

func queryEntityTokens(query string) []queryEntityToken {
	var tokens []queryEntityToken
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			tokens = append(tokens, queryEntityToken{text: word.String()})
			word.Reset()
		}
	}
	for _, r := range query {
		switch {
		case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r):
			flush()
			tokens = append(tokens, queryEntityToken{text: string(r), cjk: true})
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '+' || r == '#':
			word.WriteRune(r)
		case r == '-' || r == '_':
			// Kept inside a word: "sds-meta" is one name, and graphEntityNodeID
			// folds the dash into the same underscore a space becomes.
			word.WriteRune(r)
		default:
			flush()
		}
		if len(tokens) >= pprMaxQueryTokens {
			break
		}
	}
	flush()
	for i := range tokens {
		tokens[i].text = strings.Trim(tokens[i].text, ".-_")
	}
	return tokens
}

func joinEntityTokens(tokens []queryEntityToken) string {
	var b strings.Builder
	for i, t := range tokens {
		if i > 0 && !(t.cjk && tokens[i-1].cjk) {
			b.WriteByte(' ')
		}
		b.WriteString(t.text)
	}
	return b.String()
}

// anyDistinctiveToken reports whether an n-gram carries some mark of being a
// name rather than a phrase: a capital letter, a digit, identifier punctuation
// ("sds-e", "v2.1", "C#"), or CJK, where there is no case to go by.
//
// Without it every lowercase word of a question that some passage once
// capitalised — "mother", "place", "birth", "film" — became a seed, and on
// single-hop questions those walks crowded the one passage lexical search
// had right out of the top five.
func anyDistinctiveToken(tokens []queryEntityToken) bool {
	for _, t := range tokens {
		if t.cjk {
			return true
		}
		for _, r := range t.text {
			if unicode.IsUpper(r) || unicode.IsDigit(r) || strings.ContainsRune("-_.+#", r) {
				return true
			}
		}
	}
	return false
}

// plausibleSingleTokenEntity drops one-token candidates that would match
// grammar rather than a name: stopwords, single letters, a lone CJK character.
func plausibleSingleTokenEntity(t queryEntityToken) bool {
	if t.cjk {
		return false
	}
	if len([]rune(t.text)) < 2 {
		return false
	}
	_, stop := entityStopwords[strings.ToLower(t.text)]
	return !stop
}

// mentionCounts returns how many passages (chunks or memories) mention each
// entity — the denominator of HippoRAG's node specificity.
func (db *DB) mentionCounts(ctx context.Context, entityIDs []string) (map[string]int, error) {
	counts := make(map[string]int, len(entityIDs))
	for _, batch := range stringChunks(entityIDs, 1) {
		placeholders, args := sqlPlaceholders(batch)
		// Grouped by type rather than filtered on it, so the walk's covering
		// index on (to_node_id, from_node_id, edge_type, ...) answers it alone;
		// with edge_type in the WHERE clause the planner may pick the
		// edge_type index and read every mention edge in the store.
		rows, err := db.query(ctx, `SELECT to_node_id, COALESCE(edge_type, ''), COUNT(*) FROM graph_edges
			WHERE to_node_id IN (`+placeholders+`)
			GROUP BY to_node_id, edge_type`, args...)
		if err != nil {
			return nil, fmt.Errorf("count entity mentions: %w", err)
		}
		for rows.Next() {
			var id, edgeType string
			var n int
			if err := rows.Scan(&id, &edgeType, &n); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan entity mentions: %w", err)
			}
			if edgeType == "mentions" {
				counts[id] = n
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return counts, nil
}

// seedEntities returns the entities the query names that a walk can start
// from — those in the graph and not hubs — with how many passages mention
// each.
func (db *DB) seedEntities(ctx context.Context, query string, entityNames []string, passageNodeType string) ([]string, map[string]int, error) {
	entityIDs, err := db.queryEntityNodeIDs(ctx, query, entityNames)
	if err != nil || len(entityIDs) == 0 {
		return nil, nil, err
	}
	counts, err := db.mentionCounts(ctx, entityIDs)
	if err != nil {
		return nil, nil, err
	}
	entityIDs, err = db.dropHubEntities(ctx, entityIDs, counts, passageNodeType)
	if err != nil {
		return nil, nil, err
	}
	return entityIDs, counts, nil
}

// autoWalksFromAnEntity undoes autoUsesWalk when the query names no entity
// a walk can start from: none it names is in the graph, or only hubs are.
// The walk would then start from the first-stage passages and re-rank what
// lexical search already ranked — on LoCoMo, worse. Asked for by name, ppr
// still walks from the passages, as HippoRAG does.
func (db *DB) autoWalksFromAnEntity(ctx context.Context, resolution *retrievalPlanResolution) error {
	decision := &resolution.Decision
	if decision.RequestedMode != RetrievalModeAuto || decision.EffectiveMode != RetrievalModePPR {
		return nil
	}
	entities, _, err := db.seedEntities(ctx, resolution.Plan.Query, resolution.Plan.EntityNames, "chunk")
	if err != nil || len(entities) > 0 {
		return err
	}
	decision.EffectiveMode = RetrievalModeLexical
	decision.UseGraph = false
	decision.Reason = "auto mode stayed lexical: the query names no entity specific enough to walk the entity graph from"
	resolution.Plan.RetrievalMode = RetrievalModeLexical
	return nil
}

// dropHubEntities removes the entities mentioned by more than pprHubShare of
// the store's passages (nodes of passageNodeType); see pprHubShare.
func (db *DB) dropHubEntities(ctx context.Context, entityIDs []string, counts map[string]int, passageNodeType string) ([]string, error) {
	most := 0
	for _, id := range entityIDs {
		most = max(most, counts[id])
	}
	if most <= pprHubMinMentions {
		return entityIDs, nil
	}
	var passages int
	if err := db.queryRow(ctx, `SELECT COUNT(*) FROM graph_nodes WHERE node_type = ?`, passageNodeType).Scan(&passages); err != nil {
		return nil, fmt.Errorf("count passages: %w", err)
	}
	limit := max(pprHubMinMentions, int(pprHubShare*float64(passages)))
	kept := entityIDs[:0:0]
	for _, id := range entityIDs {
		if counts[id] <= limit {
			kept = append(kept, id)
		}
	}
	return kept, nil
}

// runPPR walks from the seeds and returns the walk's nodes best first,
// skipping the seed entities themselves and the node kinds that are never
// passages (entities, documents), at most pprMaxCandidates of them.
func (db *DB) runPPR(ctx context.Context, seeds map[string]float64, opts PPRRetrievalOptions, keep func(id string) bool) ([]graph.PageRankResult, error) {
	if len(seeds) == 0 {
		return nil, nil
	}
	res, err := db.graph.PersonalizedPageRank(ctx, seeds, graph.PPROptions{
		Damping:         opts.Damping,
		EdgeTypeWeights: opts.EdgeTypeWeights,
		MaxFrontier:     pprMaxFrontier,
	})
	if err != nil {
		return nil, err
	}
	out := make([]graph.PageRankResult, 0, pprMaxCandidates)
	for _, s := range res.Scores {
		if s.Score <= 0 || !keep(s.NodeID) {
			continue
		}
		out = append(out, s)
		if len(out) >= pprMaxCandidates {
			break
		}
	}
	return out, nil
}

// fusePPRRankings merges the first-stage and PPR orders into one ranking.
func fusePPRRankings(passages map[string]*pprRankedPassage, fusion string) []*pprRankedPassage {
	list := make([]*pprRankedPassage, 0, len(passages))
	score := make(map[string]float64, len(passages))
	for id, p := range passages {
		list = append(list, p)
		switch fusion {
		case PPRFusionPPR:
			score[id] = p.pprMass
		default:
			if p.firstRank > 0 {
				score[id] += 1 / (pprFusionRRFK + float64(p.firstRank))
			}
			if p.pprRank > 0 {
				score[id] += 1 / (pprFusionRRFK + float64(p.pprRank))
			}
		}
	}
	sort.Slice(list, func(i, j int) bool {
		si, sj := score[list[i].id], score[list[j].id]
		if si != sj {
			return si > sj
		}
		// Ties — common under RRF, where rank 3 in one list equals rank 3 in
		// the other — go to the walk, then to the first stage, then to the id.
		if list[i].pprMass != list[j].pprMass {
			return list[i].pprMass > list[j].pprMass
		}
		if list[i].firstScore != list[j].firstScore {
			return list[i].firstScore > list[j].firstScore
		}
		return list[i].id < list[j].id
	})
	for _, p := range list {
		p.fused = score[p.id]
	}
	return list
}

// searchKnowledgePPR is SearchKnowledge under RetrievalModePPR.
func (db *DB) searchKnowledgePPR(ctx context.Context, req KnowledgeSearchRequest, resolution retrievalPlanResolution, opts GraphRAGQueryOptions, lexReq ToolSearchGraphRAGLexicalRequest, hydeDoc string) (*GraphRAGQueryResult, error) {
	pprOpts := req.PPR.resolved()
	pool := max(opts.TopK*5, pprMinPool)

	// First stage: what auto would retrieve, minus graph expansion — the walk
	// replaces the expansion, it does not stack on top of it.
	firstOpts := opts
	firstOpts.TopK = pool
	firstOpts.MaxContextChunks = pool
	firstOpts.MaxRelatedChunks = 0
	firstOpts.MaxContextChars = 1 << 30
	firstOpts.PerDocumentLimit = pool
	firstOpts.ChunkWindow = 0
	firstOpts.RetrievalMode = RetrievalModeLexical
	firstPlan := resolution.Plan
	firstPlan.RetrievalMode = RetrievalModeLexical
	firstOpts.Plan = &firstPlan
	firstLex := lexReq
	firstLex.TopK = pool
	firstLex.MaxContextChunks = pool
	firstLex.MaxContextChars = 1 << 30
	firstLex.PerDocumentLimit = pool
	firstLex.ChunkWindow = 0
	firstLex.RetrievalMode = RetrievalModeLexical
	firstLex.Plan = &firstPlan

	var first *GraphRAGQueryResult
	var err error
	if db.HasEmbedder() {
		firstOpts.EmbedText = hydeDoc
		first, err = db.searchKnowledgeHybrid(ctx, resolution.Plan.Query, firstOpts, firstLex)
	} else {
		// The plain lexical ranking, without the diversity rerank the
		// lexical GraphRAG path applies: the pool is seeds and a list to fuse,
		// not a context to read, and MMR over a pool five times top_k was
		// most of this mode's latency.
		var text *ToolSearchTextResponse
		text, err = db.GraphRAGTools().SearchText(ctx, ToolSearchTextRequest{
			Query:         resolution.Plan.Query,
			Collection:    opts.Collection,
			TopK:          pool,
			RetrievalMode: RetrievalModeLexical,
			Plan:          &firstPlan,
		})
		if err == nil {
			first = &GraphRAGQueryResult{}
			for _, c := range text.Chunks {
				if !allowDocumentID(resolution.Plan.Filters, c.DocumentID) {
					continue
				}
				first.Chunks = append(first.Chunks, GraphRAGChunkResult{ID: c.ID, DocumentID: c.DocumentID, Content: c.Content, Score: c.Score})
			}
		}
	}
	if err != nil {
		return nil, err
	}

	passages := make(map[string]*pprRankedPassage, pool)
	byID := make(map[string]GraphRAGChunkResult, pool)
	ids := make([]string, 0, len(first.Chunks))
	scores := make([]float64, 0, len(first.Chunks))
	for i, c := range first.Chunks {
		passages[c.ID] = &pprRankedPassage{id: c.ID, firstRank: i + 1, firstScore: c.Score}
		byID[c.ID] = c
		ids = append(ids, c.ID)
		scores = append(scores, c.Score)
	}

	seeds, seedEntities, err := db.pprSeeds(ctx, resolution.Plan.Query, resolution.Plan.EntityNames, "chunk", ids, scores, pprOpts)
	if err != nil {
		return nil, err
	}
	walked, err := db.runPPR(ctx, seeds, pprOpts, func(id string) bool {
		return !strings.HasPrefix(id, EntityNodeIDPrefix) && !strings.HasPrefix(id, "doc:") &&
			!strings.HasPrefix(id, memoryGraphNodePrefix)
	})
	if err != nil {
		return nil, fmt.Errorf("personalized pagerank: %w", err)
	}

	// Only nodes that are stored chunks in scope are passages; the walk also
	// crosses other collections and node kinds, which it may but must not
	// return.
	walkedIDs := make([]string, 0, len(walked))
	for _, w := range walked {
		walkedIDs = append(walkedIDs, w.NodeID)
	}
	stored, err := db.chunksInScope(ctx, walkedIDs, opts.Collection, resolution.Plan.Filters)
	if err != nil {
		return nil, err
	}
	rank := 0
	for _, w := range walked {
		c, ok := stored[w.NodeID]
		if !ok {
			continue
		}
		rank++
		p := passages[w.NodeID]
		if p == nil {
			p = &pprRankedPassage{id: w.NodeID}
			passages[w.NodeID] = p
			byID[w.NodeID] = c
		}
		p.pprRank, p.pprMass = rank, w.Score
	}

	fused := fusePPRRankings(passages, pprOpts.Fusion)
	chunks := make([]GraphRAGChunkResult, 0, len(fused))
	for _, p := range fused {
		c := byID[p.id]
		c.BaseScore = c.Score
		c.Score = p.fused
		c.RerankScore = c.Score
		chunks = append(chunks, c)
	}
	// Packed before the entity names are loaded: naming what a chunk
	// mentions is a join per chunk, and it is only worth paying for the few
	// that will be returned.
	chunks = packGraphRAGContext(chunks, opts)
	chunkIDs := make([]string, 0, len(chunks))
	for _, c := range chunks {
		chunkIDs = append(chunkIDs, c.ID)
	}
	names, err := db.chunkEntityNamesBatch(ctx, chunkIDs, opts.MaxEntitiesPerChunk)
	if err != nil {
		return nil, fmt.Errorf("load chunk entities: %w", err)
	}
	entitySet := make(map[string]struct{})
	for i := range chunks {
		chunks[i].Entities = names[chunks[i].ID]
		for _, n := range chunks[i].Entities {
			entitySet[n] = struct{}{}
		}
	}

	decision := resolution.Decision
	decision.Reason = fmt.Sprintf("personalized PageRank from %d query entities and %d first-stage passages (fusion %s)",
		len(seedEntities), len(ids), pprOpts.Fusion)
	result := &GraphRAGQueryResult{
		Query:    resolution.Plan.Query,
		Plan:     resolution.Plan,
		Decision: decision,
		Chunks:   chunks,
		Entities: sortedKeys(entitySet),
		Context:  buildGraphRAGContext(chunks),
	}
	if err := db.widenGraphRAGContext(ctx, result, opts); err != nil {
		return nil, err
	}
	return result, nil
}

// chunksInScope loads the stored chunks among ids that belong to the
// collection (when one is named) and pass the plan's document filter.
func (db *DB) chunksInScope(ctx context.Context, ids []string, collection string, filters *RetrievalFilters) (map[string]GraphRAGChunkResult, error) {
	out := make(map[string]GraphRAGChunkResult, len(ids))
	for _, batch := range stringChunks(ids, 1) {
		placeholders, args := sqlPlaceholders(batch)
		rows, err := db.query(ctx, `SELECT e.id, COALESCE(e.doc_id, ''), e.content, COALESCE(c.name, '')
			FROM embeddings e LEFT JOIN collections c ON c.id = e.collection_id
			WHERE e.id IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("load walked chunks: %w", err)
		}
		for rows.Next() {
			var id, docID, content, coll string
			if err := rows.Scan(&id, &docID, &content, &coll); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan walked chunk: %w", err)
			}
			if collection != "" && coll != collection {
				continue
			}
			if !allowDocumentID(filters, docID) {
				continue
			}
			out[id] = GraphRAGChunkResult{ID: id, DocumentID: docID, Content: content}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}

// searchMemoryPPR is SearchMemory under RetrievalModePPR: the same walk, with
// memory nodes as the passages. A memory saved with entities is a node that
// mentions them, so a question about A reaches the memory about B when some
// other memory or chunk says A and B belong together.
func (db *DB) searchMemoryPPR(ctx context.Context, req MemorySearchRequest, resolution retrievalPlanResolution, bucketID string) ([]MemorySearchHit, string, error) {
	pprOpts := req.PPR.resolved()
	pool := max(req.TopK*5, pprMinPool)

	firstPlan := resolution.Plan
	firstPlan.RetrievalMode = RetrievalModeAuto
	firstPlan.EntityNames = nil
	firstReq := req
	firstReq.TopK = pool
	firstReq.EntityNames = nil
	firstReq.RetrievalMode = RetrievalModeAuto
	firstReq.Plan = &firstPlan
	first, err := db.searchMemory(ctx, firstReq, false)
	if err != nil {
		return nil, "", err
	}

	type entry struct {
		hit MemorySearchHit
		p   *pprRankedPassage
	}
	entries := make(map[string]*entry, pool)
	ids := make([]string, 0, len(first.Results))
	scores := make([]float64, 0, len(first.Results))
	for i, h := range first.Results {
		nodeID := memoryGraphNodeID(h.Memory.ID)
		entries[nodeID] = &entry{hit: h, p: &pprRankedPassage{id: nodeID, firstRank: i + 1, firstScore: h.Score}}
		ids = append(ids, nodeID)
		scores = append(scores, h.Score)
	}

	seeds, seedEntities, err := db.pprSeeds(ctx, resolution.Plan.Query, resolution.Plan.EntityNames, "memory", ids, scores, pprOpts)
	if err != nil {
		return nil, "", err
	}
	walked, err := db.runPPR(ctx, seeds, pprOpts, func(id string) bool {
		return strings.HasPrefix(id, memoryGraphNodePrefix)
	})
	if err != nil {
		return nil, "", fmt.Errorf("personalized pagerank: %w", err)
	}
	now := time.Now().UTC()
	rank := 0
	for _, w := range walked {
		e := entries[w.NodeID]
		if e == nil {
			memoryID, ok := memoryIDFromGraphNode(w.NodeID)
			if !ok {
				continue
			}
			row, err := db.loadMemoryRow(ctx, memoryID)
			if err != nil {
				continue // a node can outlive its memory
			}
			if row.record.SessionID != bucketID || memoryExpired(row.record) || memorySuperseded(row.record) {
				continue
			}
			e = &entry{
				hit: MemorySearchHit{Memory: row.record},
				p:   &pprRankedPassage{id: w.NodeID},
			}
			entries[w.NodeID] = e
		}
		rank++
		// The recall boosts stay bounded tie-breakers, as on every other path.
		e.p.pprRank, e.p.pprMass = rank, applyMemoryRecallBoosts(w.Score, e.hit.Memory, now)
	}

	passages := make(map[string]*pprRankedPassage, len(entries))
	for id, e := range entries {
		passages[id] = e.p
	}
	fused := fusePPRRankings(passages, pprOpts.Fusion)
	hits := make([]MemorySearchHit, 0, req.TopK)
	for _, p := range fused {
		if len(hits) >= req.TopK {
			break
		}
		h := entries[p.id].hit
		h.Score = p.fused
		hits = append(hits, h)
	}
	reason := fmt.Sprintf("personalized PageRank from %d query entities and %d first-stage memories (fusion %s)",
		len(seedEntities), len(ids), pprOpts.Fusion)
	return hits, reason, nil
}

// pprModeDescription is appended to the retrieval_mode description of every
// tool that implements the walk, so an agent learns when to reach for it.
const pprModeDescription = ` "ppr" ranks passages by Personalized PageRank over the entity graph, seeded by the entities the query (or entity_names) names and by the first-stage hits: use it for multi-hop questions whose answer passage shares no words with the question ("where was the director of X born?"). Works without an embedder; tune it with "ppr".`

// toolPPROptionsSchema documents PPRRetrievalOptions for the tool surface.
func toolPPROptionsSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": `Optional tuning for retrieval_mode "ppr"; ignored by other modes.`,
		"properties": map[string]any{
			"fusion":              toolEnumSchema(`"rrf" (default) fuses a walk seeded by the query's entities with the first-stage hits by reciprocal rank fusion; "ppr" is HippoRAG 2's ranking by PageRank mass alone, with the first-stage hits as weak seeds.`, PPRFusionRRF, PPRFusionPPR),
			"damping":             toolNumberSchema("Probability of following an edge rather than restarting at a seed, in (0,1). Default 0.5."),
			"passage_seed_weight": toolNumberSchema("Teleport weight of a first-stage hit relative to a query entity. Default 0.05; negative disables passage seeds."),
		},
	}
}
