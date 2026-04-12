package graphflow

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Analyze computes a deterministic summary over graphflow nodes and edges.
func Analyze(ctx context.Context, db *cortexdb.DB, req AnalyzeRequest) (*AnalysisReport, error) {
	if db == nil {
		return nil, fmt.Errorf("db is required")
	}
	if req.TopN <= 0 {
		req.TopN = 5
	}

	nodes, edges, err := loadGraphflowSubgraph(ctx, db.Graph())
	if err != nil {
		return nil, err
	}

	report := &AnalysisReport{
		GeneratedAt:   time.Now().UTC(),
		NodeCount:     len(nodes),
		EdgeCount:     len(edges),
		NodeTypes:     map[string]int{},
		RelationTypes: map[string]int{},
		Confidence:    map[string]int{},
	}

	degree := make(map[string]int, len(nodes))
	labelByID := make(map[string]string, len(nodes))
	typeByID := make(map[string]string, len(nodes))
	for _, node := range nodes {
		report.NodeTypes[node.NodeType]++
		degree[node.ID] = 0
		labelByID[node.ID] = node.Content
		typeByID[node.ID] = node.NodeType
	}
	for _, edge := range edges {
		report.RelationTypes[edge.EdgeType]++
		if confidence, ok := edge.Properties["confidence"].(string); ok {
			report.Confidence[confidence]++
		}
		degree[edge.FromNodeID]++
		degree[edge.ToNodeID]++
	}

	top := make([]TopNode, 0, len(nodes))
	for nodeID, score := range degree {
		top = append(top, TopNode{
			ID:    nodeID,
			Label: labelByID[nodeID],
			Type:  typeByID[nodeID],
			Score: float64(score),
		})
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].Score == top[j].Score {
			return top[i].ID < top[j].ID
		}
		return top[i].Score > top[j].Score
	})
	if len(top) > req.TopN {
		top = top[:req.TopN]
	}
	report.TopNodes = top

	if len(top) > 0 {
		report.SuggestedQuestions = append(report.SuggestedQuestions,
			"Why is "+top[0].Label+" the most connected node?",
			"What depends on "+top[0].Label+"?",
		)
	}
	for relation, count := range report.RelationTypes {
		if count > 1 {
			report.SuggestedQuestions = append(report.SuggestedQuestions, "What pattern explains repeated relation "+relation+"?")
			break
		}
	}
	return report, nil
}

func loadGraphflowSubgraph(ctx context.Context, store *graph.GraphStore) ([]*graph.GraphNode, []*graph.GraphEdge, error) {
	nodes, err := store.GetAllNodes(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	filteredNodes := make([]*graph.GraphNode, 0)
	nodeSet := make(map[string]struct{})
	for _, node := range nodes {
		if node == nil {
			continue
		}
		if marker, ok := node.Properties[graphflowMarker].(bool); ok && marker {
			filteredNodes = append(filteredNodes, node)
			nodeSet[node.ID] = struct{}{}
		}
	}

	edgesByID := make(map[string]*graph.GraphEdge)
	for _, node := range filteredNodes {
		outgoing, err := store.GetEdges(ctx, node.ID, "out")
		if err != nil {
			return nil, nil, err
		}
		for _, edge := range outgoing {
			if edge == nil {
				continue
			}
			if marker, ok := edge.Properties[graphflowMarker].(bool); !ok || !marker {
				continue
			}
			if _, ok := nodeSet[edge.ToNodeID]; !ok {
				continue
			}
			edgesByID[edge.ID] = edge
		}
	}

	filteredEdges := make([]*graph.GraphEdge, 0, len(edgesByID))
	for _, edge := range edgesByID {
		filteredEdges = append(filteredEdges, edge)
	}
	sort.Slice(filteredEdges, func(i, j int) bool { return filteredEdges[i].ID < filteredEdges[j].ID })
	return filteredNodes, filteredEdges, nil
}
