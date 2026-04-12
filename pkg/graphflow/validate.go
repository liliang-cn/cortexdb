package graphflow

import (
	"fmt"
	"strings"
)

// ValidateExtraction checks that an extraction result is structurally usable.
func ValidateExtraction(result *ExtractionResult) error {
	if result == nil {
		return fmt.Errorf("extraction result is required")
	}
	if strings.TrimSpace(result.SourceID) == "" {
		return fmt.Errorf("source_id is required")
	}
	nodeSet := make(map[string]struct{}, len(result.Nodes))
	for _, node := range result.Nodes {
		if strings.TrimSpace(node.ID) == "" {
			return fmt.Errorf("node id is required")
		}
		if strings.TrimSpace(node.Label) == "" {
			return fmt.Errorf("node label is required for %s", node.ID)
		}
		nodeSet[node.ID] = struct{}{}
	}
	for _, edge := range result.Edges {
		if strings.TrimSpace(edge.Source) == "" || strings.TrimSpace(edge.Target) == "" {
			return fmt.Errorf("edge source and target are required")
		}
		if strings.TrimSpace(edge.Relation) == "" {
			return fmt.Errorf("edge relation is required")
		}
		switch edge.Confidence {
		case ConfidenceExtracted, ConfidenceInferred, ConfidenceAmbiguous:
		default:
			return fmt.Errorf("unsupported edge confidence: %s", edge.Confidence)
		}
		if _, ok := nodeSet[edge.Source]; !ok {
			return fmt.Errorf("edge source %s does not exist in nodes", edge.Source)
		}
		if _, ok := nodeSet[edge.Target]; !ok {
			return fmt.Errorf("edge target %s does not exist in nodes", edge.Target)
		}
	}
	return nil
}
