package graphflow

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

const graphflowMarker = "graphflow"

// Build persists extraction results into the existing CortexDB graph store.
func Build(ctx context.Context, db *cortexdb.DB, extractions []ExtractionResult, opts BuildOptions) (*BuildResult, error) {
	if db == nil {
		return nil, fmt.Errorf("db is required")
	}
	if len(extractions) == 0 {
		return &BuildResult{}, nil
	}
	graphStore := db.Graph()
	if err := graphStore.InitGraphSchema(ctx); err != nil {
		return nil, err
	}

	nodeDefs := make(map[string]ExtractionNode)
	edgeCount := 0
	for _, extraction := range extractions {
		for _, node := range extraction.Nodes {
			if strings.TrimSpace(node.ID) == "" {
				return nil, fmt.Errorf("extraction node id is required")
			}
			nodeDefs[node.ID] = mergeNode(nodeDefs[node.ID], node)
		}
		for _, edge := range extraction.Edges {
			if edge.Source == "" || edge.Target == "" || edge.Relation == "" {
				return nil, fmt.Errorf("extraction edge source, target, and relation are required")
			}
			if _, ok := nodeDefs[edge.Source]; !ok {
				nodeDefs[edge.Source] = ExtractionNode{ID: edge.Source, Label: edge.Source, Type: "unknown"}
			}
			if _, ok := nodeDefs[edge.Target]; !ok {
				nodeDefs[edge.Target] = ExtractionNode{ID: edge.Target, Label: edge.Target, Type: "unknown"}
			}
		}
	}

	for _, node := range nodeDefs {
		record := &graph.GraphNode{
			ID:       "graphflow:node:" + node.ID,
			Vector:   deterministicVector(64, node.ID, node.Label, node.Type, node.Summary),
			Content:  firstNonEmpty(node.Label, node.ID),
			NodeType: firstNonEmpty(node.Type, "graphflow_node"),
			Properties: map[string]any{
				graphflowMarker:   true,
				"node_id":         node.ID,
				"label":           firstNonEmpty(node.Label, node.ID),
				"summary":         node.Summary,
				"source_file":     node.SourceFile,
				"source_location": node.SourceLocation,
			},
		}
		for key, value := range node.Metadata {
			record.Properties["meta_"+key] = value
		}
		if err := graphStore.UpsertNode(ctx, record); err != nil {
			return nil, err
		}
	}

	for _, extraction := range extractions {
		for _, edge := range extraction.Edges {
			edgeID := graphflowEdgeID(extraction.SourceID, edge)
			record := &graph.GraphEdge{
				ID:         edgeID,
				FromNodeID: "graphflow:node:" + edge.Source,
				ToNodeID:   "graphflow:node:" + edge.Target,
				EdgeType:   edge.Relation,
				Weight:     confidenceWeight(edge.Confidence),
				Properties: map[string]any{
					graphflowMarker:   true,
					"source_id":       extraction.SourceID,
					"source_type":     extraction.SourceType,
					"source_title":    extraction.Title,
					"source_file":     edge.SourceFile,
					"source_location": edge.SourceLocation,
					"confidence":      string(edge.Confidence),
					"directed":        edge.Directed,
				},
			}
			for key, value := range extraction.Metadata {
				record.Properties["source_"+key] = value
			}
			for key, value := range edge.Metadata {
				record.Properties["meta_"+key] = value
			}
			if err := graphStore.UpsertEdge(ctx, record); err != nil {
				return nil, err
			}
			edgeCount++
		}
	}

	return &BuildResult{
		NodeCount: len(nodeDefs),
		EdgeCount: edgeCount,
	}, nil
}

func mergeNode(existing, incoming ExtractionNode) ExtractionNode {
	if existing.ID == "" {
		return incoming
	}
	if existing.Label == "" {
		existing.Label = incoming.Label
	}
	if existing.Type == "" {
		existing.Type = incoming.Type
	}
	if existing.Summary == "" {
		existing.Summary = incoming.Summary
	}
	if existing.SourceFile == "" {
		existing.SourceFile = incoming.SourceFile
	}
	if existing.SourceLocation == "" {
		existing.SourceLocation = incoming.SourceLocation
	}
	if existing.Metadata == nil {
		existing.Metadata = map[string]string{}
	}
	for key, value := range incoming.Metadata {
		if _, ok := existing.Metadata[key]; !ok {
			existing.Metadata[key] = value
		}
	}
	return existing
}

func deterministicVector(dim int, values ...string) []float32 {
	vector := make([]float32, dim)
	for _, value := range values {
		for _, token := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
		}) {
			hasher := fnv.New32a()
			_, _ = hasher.Write([]byte(token))
			vector[int(hasher.Sum32()%uint32(dim))]++
		}
	}
	return vector
}

func confidenceWeight(confidence Confidence) float64 {
	switch confidence {
	case ConfidenceExtracted:
		return 1.0
	case ConfidenceInferred:
		return 0.7
	case ConfidenceAmbiguous:
		return 0.4
	default:
		return 0.5
	}
}

func graphflowEdgeID(sourceID string, edge ExtractionEdge) string {
	if sourceID == "" {
		sourceID = "source"
	}
	return "graphflow:edge:" + sourceID + ":" + edge.Source + ":" + edge.Relation + ":" + edge.Target
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
