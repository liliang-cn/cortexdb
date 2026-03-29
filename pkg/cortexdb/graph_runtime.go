package cortexdb

import (
	"context"
	"sort"
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
