package cortexdb

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// With an embedder, auto retrieval is vector search, then a graph walk from
// what it found, then one ranking of both (#272):
//
//  1. The vector search auto has always run (vector + keyword, relevance
//     floors included), so a question about nothing the store knows still
//     comes back empty: the walk only starts from hits that passed.
//  2. The entity nodes the top hits mention are the walk's seeds, each
//     weighted by its hits' rank and by specificity (1 / passages that
//     mention it); hubs are dropped. Entities the query names seed it too.
//  3. One personalized PageRank over the entity graph from those seeds,
//     under the same per-edge-type weights as RetrievalModePPR: extracted
//     relations count fully, structural edges less. It reaches passages and
//     memories the vector search missed — the second hop of a question — and
//     ranks the entities between them.
//  4. The vector order and the walk order fused by reciprocal rank fusion,
//     the walk's best entities kept for the graph facts of a recall.
//
// When the hits mention no entity, the result is the vector ranking, and the
// decision says so.

const (
	// hitSeedPassages is how many of the vector search's hits give the walk
	// their entities: the head of the list, where the vector search is most
	// likely right. 5 and 20 scored within noise of 10 on 2WikiMultiHopQA
	// and MuSiQue.
	hitSeedPassages = 10
	// walkEntityLimit is how many of the walk's entities are handed on, best
	// first, as the seeds of a recall's graph facts (maxGraphFactSeeds).
	walkEntityLimit = maxGraphFactSeeds
	// walkRerankWeight is the walk's share of the fused ranking, against the
	// vector search's 1, for a passage the vector search also returned, and
	// walkFindWeight for one only the walk reached. Measured on a snapshot of
	// a live shared brain (51 labelled queries, MRR@5 0.643 for the vector
	// search alone) and on 2WikiMultiHopQA / MuSiQue (recall@5 0.638 / 0.543
	// alone): an equal share of both lost on the brain (0.569, 13 queries
	// worse), because hits matching on common entities reordered the head of
	// the list; 0.3 and 0.7 gained on the brain (0.664, 1 worse) and kept the
	// multi-hop gain (0.775 / 0.590).
	walkRerankWeight = 0.3
	walkFindWeight   = 0.7
)

// walksFromHits reports whether a search under decision runs the walk: auto
// was asked for, an embedder is configured, and nothing turned the graph off.
// ppr asked for by name keeps its own seeding.
func (db *DB) walksFromHits(decision RetrievalDecision, disableGraph bool) bool {
	if disableGraph || !db.HasEmbedder() {
		return false
	}
	requested := strings.TrimSpace(decision.RequestedMode)
	if requested != "" && requested != RetrievalModeAuto {
		return false
	}
	return decision.EffectiveMode != RetrievalModePPR
}

// hitWalk is one walk from the entities of a ranked list of hits.
type hitWalk struct {
	// passages are the walk's passages of the kind it was asked for, best
	// first, with their mass.
	passages []graph.PageRankResult
	// entities are the walk's entity nodes, best first, at most
	// walkEntityLimit: the seeds themselves and what lies between them.
	entities []graph.PageRankResult
	seeds    int
}

// walkFromHits seeds a walk with the entities the first hits mention (and
// those the query names) and runs it, keeping the passages keep accepts.
func (db *DB) walkFromHits(ctx context.Context, query string, entityNames []string, passageNodeType string, hitNodeIDs []string, keep func(id string) bool) (*hitWalk, error) {
	if len(hitNodeIDs) > hitSeedPassages {
		hitNodeIDs = hitNodeIDs[:hitSeedPassages]
	}
	seeds, err := db.hitEntitySeeds(ctx, query, entityNames, passageNodeType, hitNodeIDs)
	if err != nil || len(seeds) == 0 {
		return &hitWalk{}, err
	}
	opts := (*PPRRetrievalOptions)(nil).resolved()
	res, err := db.graph.PersonalizedPageRank(ctx, seeds, graph.PPROptions{
		Damping:         opts.Damping,
		EdgeTypeWeights: opts.EdgeTypeWeights,
		MaxFrontier:     pprMaxFrontier,
	})
	if err != nil {
		return nil, fmt.Errorf("personalized pagerank: %w", err)
	}
	walk := &hitWalk{seeds: len(seeds)}
	for _, s := range res.Scores {
		if s.Score <= 0 {
			continue
		}
		switch {
		case strings.HasPrefix(s.NodeID, EntityNodeIDPrefix):
			if len(walk.entities) < walkEntityLimit {
				walk.entities = append(walk.entities, s)
			}
		case keep(s.NodeID):
			if len(walk.passages) < pprMaxCandidates {
				walk.passages = append(walk.passages, s)
			}
		}
	}
	return walk, nil
}

// hitEntitySeeds weights the entities the first hits mention. A hit's weight
// falls with its rank the way RRF's does, so the first hit's entities count
// most; an entity many passages mention counts less (HippoRAG's node
// specificity), and a hub not at all. The entities the query names are seeds
// at the first hit's weight.
func (db *DB) hitEntitySeeds(ctx context.Context, query string, entityNames []string, passageNodeType string, hitNodeIDs []string) (map[string]float64, error) {
	mentioned, err := db.passageEntityIDs(ctx, hitNodeIDs)
	if err != nil {
		return nil, err
	}
	weight := make(map[string]float64)
	for rank, id := range hitNodeIDs {
		w := (pprFusionRRFK + 1) / (pprFusionRRFK + float64(rank+1))
		for _, entity := range mentioned[id] {
			weight[entity] += w
		}
	}
	named, _, err := db.seedEntities(ctx, query, entityNames, passageNodeType)
	if err != nil {
		return nil, err
	}
	for _, entity := range named {
		weight[entity] += 1
	}
	if len(weight) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(weight))
	for id := range weight {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	counts, err := db.mentionCounts(ctx, ids)
	if err != nil {
		return nil, err
	}
	ids, err = db.dropHubEntities(ctx, ids, counts, passageNodeType)
	if err != nil {
		return nil, err
	}
	// An entity only the hit itself mentions, with no relation of its own,
	// leads nowhere: the walk would hand all of its share straight back to
	// that hit and repeat the vector ranking, at the expense of the entities
	// that do lead to another passage. One the question names stays.
	namedSet := make(map[string]bool, len(named))
	for _, id := range named {
		namedSet[id] = true
	}
	var lone []string
	for _, id := range ids {
		if counts[id] <= 1 && !namedSet[id] {
			lone = append(lone, id)
		}
	}
	related, err := db.entitiesWithRelations(ctx, lone)
	if err != nil {
		return nil, err
	}
	kept := ids[:0]
	for _, id := range ids {
		if counts[id] <= 1 && !namedSet[id] && !related[id] {
			continue
		}
		kept = append(kept, id)
	}
	ids = kept
	seeds := make(map[string]float64, len(ids))
	for _, id := range ids {
		seeds[id] = weight[id] / float64(max(counts[id], 1))
	}
	return seeds, nil
}

// passageEntityIDs returns the entity nodes each passage node (a chunk or a
// memory) mentions.
func (db *DB) passageEntityIDs(ctx context.Context, passageIDs []string) (map[string][]string, error) {
	out := make(map[string][]string, len(passageIDs))
	for _, batch := range stringChunks(passageIDs, 1) {
		placeholders, args := sqlPlaceholders(batch)
		rows, err := db.query(ctx, `SELECT from_node_id, to_node_id FROM graph_edges
			WHERE from_node_id IN (`+placeholders+`) AND COALESCE(edge_type, '') = 'mentions'
			ORDER BY from_node_id, to_node_id`, args...)
		if err != nil {
			return nil, fmt.Errorf("load hit entities: %w", err)
		}
		for rows.Next() {
			var from, to string
			if err := rows.Scan(&from, &to); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan hit entities: %w", err)
			}
			if strings.HasPrefix(to, EntityNodeIDPrefix) {
				out[from] = append(out[from], to)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}

// walkEntityNames names the walk's entities, best first.
func (db *DB) walkEntityNames(ctx context.Context, entities []graph.PageRankResult) []string {
	if len(entities) == 0 {
		return nil
	}
	ids := make([]string, 0, len(entities))
	for _, e := range entities {
		ids = append(ids, e.NodeID)
	}
	nodes, err := db.graph.GetNodesBatch(ctx, ids)
	if err != nil {
		return nil
	}
	byID := make(map[string]string, len(nodes))
	for _, n := range nodes {
		if n != nil {
			byID[n.ID] = strings.TrimSpace(n.Content)
		}
	}
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		name := byID[id]
		if name == "" {
			name = entityLabelFromID(id)
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func walkReason(prefix string, walk *hitWalk, reached int) string {
	if walk.seeds == 0 {
		return prefix + "; the hits mention no entity to walk the graph from, so this is the vector ranking"
	}
	return fmt.Sprintf("%s; walked the entity graph from %d entities the hits mention and fused the vector and graph rankings (%d passages reached)",
		prefix, walk.seeds, reached)
}

// fuseKnowledgeWalk is the walk and the fusion for a knowledge search, after
// the vector search and its floors.
func (db *DB) fuseKnowledgeWalk(ctx context.Context, result *GraphRAGQueryResult, plan RetrievalPlan, opts GraphRAGQueryOptions) error {
	if result == nil || len(result.Chunks) == 0 {
		return nil
	}
	ids := make([]string, 0, len(result.Chunks))
	for _, c := range result.Chunks {
		ids = append(ids, c.ID)
	}
	walk, err := db.walkFromHits(ctx, plan.Query, plan.EntityNames, "chunk", ids, isChunkWalkNode)
	if err != nil {
		return err
	}
	return db.fuseKnowledgeWithWalk(ctx, result, plan, opts, walk)
}

func isChunkWalkNode(id string) bool {
	return !strings.HasPrefix(id, "doc:") && !strings.HasPrefix(id, memoryGraphNodePrefix)
}

// fuseKnowledgeWithWalk fuses a knowledge search's vector ranking with the
// chunks a walk reached.
func (db *DB) fuseKnowledgeWithWalk(ctx context.Context, result *GraphRAGQueryResult, plan RetrievalPlan, opts GraphRAGQueryOptions, walk *hitWalk) error {
	if result == nil {
		return nil
	}
	result.Decision.UseGraph = walk.seeds > 0
	if walk.seeds == 0 {
		result.Decision.Reason = walkReason(result.Decision.Reason, walk, 0)
		return nil
	}

	passages := make(map[string]*pprRankedPassage, len(result.Chunks)+len(walk.passages))
	byID := make(map[string]GraphRAGChunkResult, len(result.Chunks)+len(walk.passages))
	for i, c := range result.Chunks {
		passages[c.ID] = &pprRankedPassage{id: c.ID, firstRank: i + 1, firstScore: c.Score}
		byID[c.ID] = c
	}
	walkedIDs := make([]string, 0, len(walk.passages))
	for _, w := range walk.passages {
		if isChunkWalkNode(w.NodeID) {
			walkedIDs = append(walkedIDs, w.NodeID)
		}
	}
	stored, err := db.chunksInScope(ctx, walkedIDs, opts.Collection, plan.Filters)
	if err != nil {
		return err
	}
	rank := 0
	for _, w := range walk.passages {
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

	// The walk reorders the list and fills it to top_k; it does not make it
	// longer than that, or than the vector stage's own list where that was
	// longer.
	fused := fuseWalkRankings(passages)
	if limit := max(len(result.Chunks), opts.TopK); len(fused) > limit {
		fused = fused[:limit]
	}
	chunks := make([]GraphRAGChunkResult, 0, len(fused))
	for _, p := range fused {
		c := byID[p.id]
		if p.firstRank > 0 {
			c.BaseScore = c.Score
		}
		c.Score = p.fused
		c.RerankScore = c.Score
		chunks = append(chunks, c)
	}
	chunks = packGraphRAGContext(chunks, opts)
	chunkIDs := make([]string, 0, len(chunks))
	for _, c := range chunks {
		chunkIDs = append(chunkIDs, c.ID)
	}
	names, err := db.chunkEntityNamesBatch(ctx, chunkIDs, opts.MaxEntitiesPerChunk)
	if err != nil {
		return fmt.Errorf("load chunk entities: %w", err)
	}
	entitySet := make(map[string]struct{})
	for i := range chunks {
		if len(chunks[i].Entities) == 0 {
			chunks[i].Entities = names[chunks[i].ID]
		}
		for _, n := range chunks[i].Entities {
			entitySet[n] = struct{}{}
		}
	}
	result.Chunks = chunks
	result.Entities = sortedKeys(entitySet)
	result.GraphEntities = db.walkEntityNames(ctx, walk.entities)
	result.Windows = nil
	result.Context = buildGraphRAGContext(chunks)
	result.Decision.Reason = walkReason(result.Decision.Reason, walk, rank)
	recallExplainerOf(ctx).candidates(explainKnowledge, explainPPR, chunkCandidates(chunks, func(c GraphRAGChunkResult) float64 { return c.Score }))
	return db.widenGraphRAGContext(ctx, result, opts)
}

// fuseMemoryWalk is the walk and the fusion for a memory search, after the
// vector search and its floors. Memories the walk reaches are kept to the
// bucket searched and to what a recall may return, like every other path.
//
// It returns the fused hits, the decision's reason and the walk's entities.
func (db *DB) fuseMemoryWalk(ctx context.Context, hits []MemorySearchHit, plan RetrievalPlan, reason, bucketID string, topK int) ([]MemorySearchHit, string, []string, error) {
	if len(hits) == 0 {
		return hits, reason, nil, nil
	}
	ids := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, memoryGraphNodeID(h.Memory.ID))
	}
	walk, err := db.walkFromHits(ctx, plan.Query, plan.EntityNames, "memory", ids, func(id string) bool {
		return strings.HasPrefix(id, memoryGraphNodePrefix)
	})
	if err != nil {
		return nil, "", nil, err
	}
	out, reached := db.fuseMemoryWithWalk(ctx, hits, bucketID, topK, walk)
	if walk.seeds == 0 {
		return out, walkReason(reason, walk, 0), nil, nil
	}
	return out, walkReason(reason, walk, reached), db.walkEntityNames(ctx, walk.entities), nil
}

// fuseMemoryWithWalk fuses a memory search's vector ranking with the
// memories of bucketID a walk reached, and says how many it reached.
func (db *DB) fuseMemoryWithWalk(ctx context.Context, hits []MemorySearchHit, bucketID string, topK int, walk *hitWalk) ([]MemorySearchHit, int) {
	if walk.seeds == 0 {
		return hits, 0
	}
	type entry struct {
		hit MemorySearchHit
		p   *pprRankedPassage
	}
	entries := make(map[string]*entry, len(hits))
	for i, h := range hits {
		nodeID := memoryGraphNodeID(h.Memory.ID)
		entries[nodeID] = &entry{hit: h, p: &pprRankedPassage{id: nodeID, firstRank: i + 1, firstScore: h.Score}}
	}
	now := time.Now().UTC()
	rank := 0
	for _, w := range walk.passages {
		if !strings.HasPrefix(w.NodeID, memoryGraphNodePrefix) {
			continue
		}
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
			if row.record.SessionID != bucketID || !db.memoryKept(ctx, row.record) {
				continue
			}
			e = &entry{
				hit: MemorySearchHit{Memory: row.record},
				p:   &pprRankedPassage{id: w.NodeID},
			}
			entries[w.NodeID] = e
		}
		rank++
		e.p.pprRank, e.p.pprMass = rank, applyMemoryRecallBoosts(w.Score, e.hit.Memory, now)
	}
	passages := make(map[string]*pprRankedPassage, len(entries))
	for id, e := range entries {
		passages[id] = e.p
	}
	fused := fuseWalkRankings(passages)
	out := make([]MemorySearchHit, 0, topK)
	for _, p := range fused {
		if len(out) >= topK {
			break
		}
		e := entries[p.id]
		h := e.hit
		h.Score = p.fused
		if p.pprRank > 0 {
			h.Via = appendVia(h.Via, memoryViaGraph)
		}
		out = append(out, h)
	}
	return out, rank
}

func appendVia(via []string, how string) []string {
	for _, v := range via {
		if v == how {
			return via
		}
	}
	return append(append([]string(nil), via...), how)
}

// recallWalks reports whether a recall walks the graph from its hits: the
// searches are auto under an embedder and the graph is not turned off.
func (db *DB) recallWalks(req KnowledgeMemoryRecallRequest) bool {
	mode := strings.ToLower(strings.TrimSpace(req.RetrievalMode))
	if req.Plan != nil && mode == "" {
		mode = strings.ToLower(strings.TrimSpace(req.Plan.RetrievalMode))
	}
	return db.walksFromHits(RetrievalDecision{RequestedMode: mode}, req.DisableGraph)
}

// recallWalk is a recall's one walk over both stores (#272). The graph does
// not divide into a memory half and a knowledge half: a document's hit names
// the entity a memory is about as readily as another memory does, and the
// question whose words match only the product page is answered by the
// memory of who owns the product now. So the seeds are the entities both
// stores' hits mention, the walk reaches chunks and memories alike, and each
// store's vector ranking is fused with what the walk reached of its own kind.
// It returns the walk's entities, best first.
func (b *KnowledgeMemory) recallWalk(ctx context.Context, req KnowledgeMemoryRecallRequest, query string, memResp *MemorySearchResponse, knResp *KnowledgeSearchResponse) ([]string, error) {
	db := b.db
	var memIDs, chunkIDs []string
	if memResp != nil {
		for _, h := range memResp.Results {
			memIDs = append(memIDs, memoryGraphNodeID(h.Memory.ID))
		}
	}
	if knResp != nil {
		for _, c := range knResp.Chunks {
			chunkIDs = append(chunkIDs, c.ID)
		}
	}
	// Taken alternately, so each store's best hits seed the walk at the same
	// weight; twice the single-store count, so neither store's head is cut.
	hitIDs := interleaveStrings(nil, memIDs, chunkIDs)
	if len(hitIDs) > 2*hitSeedPassages {
		hitIDs = hitIDs[:2*hitSeedPassages]
	}
	if len(hitIDs) == 0 {
		return nil, nil
	}
	seeds, err := db.hitEntitySeeds(ctx, query, req.EntityNames, "", hitIDs)
	if err != nil {
		return nil, err
	}
	walk := &hitWalk{}
	if len(seeds) > 0 {
		opts := (*PPRRetrievalOptions)(nil).resolved()
		res, err := db.graph.PersonalizedPageRank(ctx, seeds, graph.PPROptions{
			Damping:         opts.Damping,
			EdgeTypeWeights: opts.EdgeTypeWeights,
			MaxFrontier:     pprMaxFrontier,
		})
		if err != nil {
			return nil, fmt.Errorf("personalized pagerank: %w", err)
		}
		walk.seeds = len(seeds)
		for _, s := range res.Scores {
			if s.Score <= 0 {
				continue
			}
			switch {
			case strings.HasPrefix(s.NodeID, EntityNodeIDPrefix):
				if len(walk.entities) < walkEntityLimit {
					walk.entities = append(walk.entities, s)
				}
			case strings.HasPrefix(s.NodeID, "doc:"):
			default:
				if len(walk.passages) < 2*pprMaxCandidates {
					walk.passages = append(walk.passages, s)
				}
			}
		}
	}

	if memResp != nil {
		bucketID := ""
		if len(memResp.Results) > 0 {
			bucketID = memResp.Results[0].Memory.SessionID
		} else if _, id, err := resolveMemoryBucket(req.Scope, req.UserID, req.SessionID, req.Namespace); err == nil {
			bucketID = id
		}
		if ok, err := db.memoryReadPermitted(ctx, bucketID); err != nil {
			return nil, err
		} else if ok && bucketID != "" {
			topK := req.TopKMemories
			if topK <= 0 {
				topK = 5
			}
			hits, reached := db.fuseMemoryWithWalk(ctx, memResp.Results, bucketID, topK, walk)
			memResp.Results = hits
			memResp.Decision.UseGraph = walk.seeds > 0
			memResp.Decision.Reason = walkReason(memResp.Decision.Reason, walk, reached)
		}
	}
	if knResp != nil {
		collection := applyRetrievalPlanCollection(req.Collection, knResp.Plan.Filters)
		if knowledgeReadPermitted(ctx, collection) {
			opts := GraphRAGQueryOptions{
				TopK:                req.TopKKnowledge,
				Collection:          collection,
				MaxContextChunks:    req.MaxContextChunks,
				MaxContextChars:     req.MaxContextChars,
				PerDocumentLimit:    req.PerDocumentLimit,
				ChunkWindow:         req.ChunkWindow,
				MaxEntitiesPerChunk: req.MaxEntitiesPerChunk,
			}
			applyGraphRAGQueryDefaults(&opts)
			result := &GraphRAGQueryResult{
				Query: knResp.Query, Plan: knResp.Plan, Decision: knResp.Decision,
				Chunks: knResp.Chunks, Entities: knResp.Entities, Context: knResp.Context, Degraded: knResp.Degraded,
			}
			if err := db.fuseKnowledgeWithWalk(ctx, result, knResp.Plan, opts, walk); err != nil {
				return nil, err
			}
			hits, err := db.aggregateKnowledgeHits(ctx, result.Chunks)
			if err != nil {
				return nil, err
			}
			knResp.Decision, knResp.Chunks, knResp.Results = result.Decision, result.Chunks, hits
			knResp.Entities, knResp.Context = result.Entities, result.Context
		}
	}
	names := db.walkEntityNames(ctx, walk.entities)
	if knResp != nil {
		knResp.GraphEntities = names
	}
	if memResp != nil {
		memResp.GraphEntities = names
	}
	return names, nil
}

// entitiesWithRelations reports which of the entities take part in a
// relation to another entity. mentions ties an entity to a passage, and
// co_occurs to a name in the same sentence, which a hit mentioning both
// seeds anyway; neither leads the walk anywhere new.
func (db *DB) entitiesWithRelations(ctx context.Context, entityIDs []string) (map[string]bool, error) {
	out := make(map[string]bool, len(entityIDs))
	for _, batch := range stringChunks(entityIDs, 2) {
		placeholders, args := sqlPlaceholders(batch)
		rows, err := db.query(ctx, `SELECT from_node_id, to_node_id FROM graph_edges
			WHERE (from_node_id IN (`+placeholders+`) OR to_node_id IN (`+placeholders+`))
			AND COALESCE(edge_type, '') NOT IN ('mentions', 'co_occurs')`, append(args, args...)...)
		if err != nil {
			return nil, fmt.Errorf("load entity relations: %w", err)
		}
		for rows.Next() {
			var from, to string
			if err := rows.Scan(&from, &to); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan entity relations: %w", err)
			}
			if strings.HasPrefix(from, EntityNodeIDPrefix) && strings.HasPrefix(to, EntityNodeIDPrefix) {
				out[from], out[to] = true, true
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}

// fuseWalkRankings is reciprocal rank fusion of the vector ranking and the
// walk's, the walk weighted by walkRerankWeight or walkFindWeight. A tie, common under RRF, goes to the vector
// search: the walk adds what the words miss, it does not overrule them
// (on MuSiQue a tie that went to the walk cost recall@5 0.59 → 0.54).
func fuseWalkRankings(passages map[string]*pprRankedPassage) []*pprRankedPassage {
	list := make([]*pprRankedPassage, 0, len(passages))
	for _, p := range passages {
		list = append(list, p)
		p.fused = 0
		if p.firstRank > 0 {
			p.fused += 1 / (pprFusionRRFK + float64(p.firstRank))
		}
		if p.pprRank > 0 {
			w := walkRerankWeight
			if p.firstRank == 0 {
				w = walkFindWeight
			}
			p.fused += w / (pprFusionRRFK + float64(p.pprRank))
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].fused != list[j].fused {
			return list[i].fused > list[j].fused
		}
		if list[i].firstScore != list[j].firstScore {
			return list[i].firstScore > list[j].firstScore
		}
		if list[i].pprMass != list[j].pprMass {
			return list[i].pprMass > list[j].pprMass
		}
		return list[i].id < list[j].id
	})
	return list
}
