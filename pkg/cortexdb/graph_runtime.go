package cortexdb

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

type graphRuntimeConfig struct {
	GraphLight          bool
	MaxExpansionSeeds   int
	MaxTraversalNodes   int
	MaxEntitiesPerChunk int
}

func applyGraphRuntimeDefaults(opts *GraphRAGQueryOptions) {
	if opts == nil {
		return
	}

	if !opts.GraphLight && normalizeRetrievalMode(opts.RetrievalMode) == RetrievalModeAuto {
		opts.GraphLight = true
	}
	if opts.MaxExpansionSeeds <= 0 {
		if opts.GraphLight {
			opts.MaxExpansionSeeds = min(max(opts.TopK, 1), 2)
		} else {
			opts.MaxExpansionSeeds = max(opts.TopK, 1)
		}
	}
	if opts.MaxTraversalNodes <= 0 {
		if opts.GraphLight {
			opts.MaxTraversalNodes = max(opts.TopK*6, opts.MaxExpansionSeeds*4)
		} else {
			opts.MaxTraversalNodes = max(opts.TopK*12, opts.MaxExpansionSeeds*8)
		}
	}
	if opts.MaxEntitiesPerChunk <= 0 {
		if opts.GraphLight {
			opts.MaxEntitiesPerChunk = 6
		} else {
			opts.MaxEntitiesPerChunk = 16
		}
	}
}

func graphRuntimeConfigFromQueryOptions(opts GraphRAGQueryOptions) graphRuntimeConfig {
	return graphRuntimeConfig{
		GraphLight:          opts.GraphLight,
		MaxExpansionSeeds:   opts.MaxExpansionSeeds,
		MaxTraversalNodes:   opts.MaxTraversalNodes,
		MaxEntitiesPerChunk: opts.MaxEntitiesPerChunk,
	}
}

func maxEntitiesPerChunk(mode string, graphLight bool, explicit int) int {
	opts := GraphRAGQueryOptions{
		RetrievalMode:       mode,
		GraphLight:          graphLight,
		MaxEntitiesPerChunk: explicit,
	}
	applyGraphRAGQueryDefaults(&opts)
	return opts.MaxEntitiesPerChunk
}

func (db *DB) expandGraphChunkNeighborhoods(ctx context.Context, chunkResults map[string]*GraphRAGChunkResult, seedIDs []string, opts GraphRAGQueryOptions, filters *RetrievalFilters) (map[string]struct{}, error) {
	entitySet := make(map[string]struct{})
	if len(seedIDs) == 0 || opts.MaxHops <= 0 {
		return entitySet, nil
	}

	runtime := graphRuntimeConfigFromQueryOptions(opts)
	frontierScores := make(map[string]float64)
	visited := make(map[string]struct{}, len(seedIDs))
	for _, seedID := range topScoredChunkIDs(chunkResults, seedIDs, runtime.MaxExpansionSeeds) {
		if chunk := chunkResults[seedID]; chunk != nil {
			frontierScores[seedID] = chunk.Score
			visited[seedID] = struct{}{}
		}
	}

	remainingBudget := runtime.MaxTraversalNodes
	for depth := 0; depth < opts.MaxHops && len(frontierScores) > 0 && remainingBudget > 0; depth++ {
		frontierIDs := topScoredIDs(frontierScores, remainingBudget)
		edgeMap, err := db.graphEdgesByNodeIDs(ctx, frontierIDs, "both")
		if err != nil {
			return nil, err
		}

		nextScores := make(map[string]float64)
		for _, frontierID := range frontierIDs {
			baseScore := frontierScores[frontierID]
			for _, edge := range edgeMap[frontierID] {
				neighborID := edge.FromNodeID
				if neighborID == frontierID {
					neighborID = edge.ToNodeID
				}
				if _, seen := visited[neighborID]; seen {
					continue
				}
				if baseScore > nextScores[neighborID] {
					nextScores[neighborID] = baseScore
				}
			}
		}
		if len(nextScores) == 0 {
			break
		}

		neighborIDs := topScoredIDs(nextScores, remainingBudget)
		summaries, err := db.graphNodeSummariesByIDs(ctx, neighborIDs)
		if err != nil {
			return nil, err
		}

		frontierScores = make(map[string]float64)
		for _, neighborID := range neighborIDs {
			summary, ok := summaries[neighborID]
			if !ok {
				continue
			}
			visited[neighborID] = struct{}{}
			remainingBudget--

			score := nextScores[neighborID]
			switch summary.NodeType {
			case "entity":
				entitySet[summary.Content] = struct{}{}
			case "chunk":
				if !allowDocumentID(filters, summary.DocumentID) {
					continue
				}
				relatedScore := score * 0.5
				existing := chunkResults[neighborID]
				if existing == nil {
					chunkResults[neighborID] = &GraphRAGChunkResult{
						ID:         summary.ID,
						DocumentID: summary.DocumentID,
						Content:    summary.Content,
						Score:      relatedScore,
						BaseScore:  relatedScore,
					}
				} else if relatedScore > existing.Score {
					existing.Score = relatedScore
					existing.BaseScore = relatedScore
				}
			}

			frontierScores[neighborID] = score
			if remainingBudget <= 0 {
				break
			}
		}
	}

	return entitySet, nil
}

func topScoredChunkIDs(chunks map[string]*GraphRAGChunkResult, preferredOrder []string, limit int) []string {
	scored := make([]*GraphRAGChunkResult, 0, len(preferredOrder))
	seen := make(map[string]struct{}, len(preferredOrder))
	for _, chunkID := range preferredOrder {
		chunk := chunks[chunkID]
		if chunk == nil {
			continue
		}
		seen[chunkID] = struct{}{}
		scored = append(scored, chunk)
	}
	for chunkID, chunk := range chunks {
		if _, ok := seen[chunkID]; ok || chunk == nil {
			continue
		}
		scored = append(scored, chunk)
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].ID < scored[j].ID
		}
		return scored[i].Score > scored[j].Score
	})
	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}
	result := make([]string, 0, len(scored))
	for _, chunk := range scored {
		result = append(result, chunk.ID)
	}
	return result
}

func topScoredIDs(scores map[string]float64, limit int) []string {
	type item struct {
		id    string
		score float64
	}
	items := make([]item, 0, len(scores))
	for id, score := range scores {
		items = append(items, item{id: id, score: score})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].score == items[j].score {
			return items[i].id < items[j].id
		}
		return items[i].score > items[j].score
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.id)
	}
	return result
}

// graphHubMentions is the number of passages above which a query entity no
// longer picks passages for graph mode. Each passage that mentions an
// entity gets 1/(its passage count) from it — HippoRAG's node specificity —
// so a hub's share is already a rounding error; skipping it saves reading
// every one of its mention edges to hand each a rounding error.
const graphHubMentions = 500

// entityLinkedChunks returns the stored chunks in scope that mention any of the
// entities directly, best first: by the summed specificity of the entities a
// chunk mentions, so a chunk naming the question's rare entity outranks one
// naming only its common one, then by lexical rank, then by id. At most limit.
//
// One hop and no further. Reaching past the mention edge — through "next",
// co_occurs or a shared hub — is what graph expansion is for, scored as such;
// counted here, every such chunk tied the passage the question names.
func (db *DB) entityLinkedChunks(ctx context.Context, entityIDs []string, collection string, filters *RetrievalFilters, lexicalRank map[string]int, limit int) ([]GraphRAGChunkResult, error) {
	if len(entityIDs) == 0 || limit <= 0 {
		return nil, nil
	}
	counts, err := db.mentionCounts(ctx, entityIDs)
	if err != nil {
		return nil, err
	}
	specific := make([]string, 0, len(entityIDs))
	for _, id := range entityIDs {
		if n := counts[id]; n > 0 && n <= graphHubMentions {
			specific = append(specific, id)
		}
	}
	scores := make(map[string]float64)
	for _, batch := range stringChunks(specific, 1) {
		placeholders, args := sqlPlaceholders(batch)
		// edge_type is read, not filtered on, for the reason mentionCounts gives.
		rows, err := db.query(ctx, `SELECT from_node_id, to_node_id, COALESCE(edge_type, '') FROM graph_edges
			WHERE to_node_id IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("load entity mentions: %w", err)
		}
		for rows.Next() {
			var from, to, edgeType string
			if err := rows.Scan(&from, &to, &edgeType); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan entity mention: %w", err)
			}
			if edgeType != "mentions" || strings.HasPrefix(from, memoryGraphNodePrefix) {
				continue
			}
			scores[from] += 1 / float64(counts[to])
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	if len(scores) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	rankOf := func(id string) int {
		if r, ok := lexicalRank[id]; ok {
			return r
		}
		return math.MaxInt
	}
	sort.Slice(ids, func(i, j int) bool {
		if scores[ids[i]] != scores[ids[j]] {
			return scores[ids[i]] > scores[ids[j]]
		}
		if ri, rj := rankOf(ids[i]), rankOf(ids[j]); ri != rj {
			return ri < rj
		}
		return ids[i] < ids[j]
	})
	// Over-read: some ids are not chunks of this collection.
	if len(ids) > limit*4 {
		ids = ids[:limit*4]
	}
	stored, err := db.chunksInScope(ctx, ids, collection, filters)
	if err != nil {
		return nil, err
	}
	out := make([]GraphRAGChunkResult, 0, min(limit, len(stored)))
	for _, id := range ids {
		c, ok := stored[id]
		if !ok {
			continue
		}
		c.Score = scores[id]
		c.BaseScore = c.Score
		out = append(out, c)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}
