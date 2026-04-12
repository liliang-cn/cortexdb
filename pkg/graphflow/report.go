package graphflow

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// RenderReport renders a deterministic markdown report.
func RenderReport(_ context.Context, report *AnalysisReport) (string, error) {
	if report == nil {
		return "", fmt.Errorf("analysis report is required")
	}
	var builder strings.Builder
	builder.WriteString("# GRAPHFLOW_REPORT\n\n")
	builder.WriteString(fmt.Sprintf("- Nodes: %d\n", report.NodeCount))
	builder.WriteString(fmt.Sprintf("- Edges: %d\n", report.EdgeCount))
	builder.WriteString(fmt.Sprintf("- GeneratedAt: %s\n\n", report.GeneratedAt.Format("2006-01-02 15:04:05Z")))

	if len(report.TopNodes) > 0 {
		builder.WriteString("## Top Nodes\n")
		for _, node := range report.TopNodes {
			builder.WriteString(fmt.Sprintf("- %s (%s) score=%.0f\n", node.Label, node.Type, node.Score))
		}
		builder.WriteString("\n")
	}

	if len(report.RelationTypes) > 0 {
		builder.WriteString("## Relation Types\n")
		keys := make([]string, 0, len(report.RelationTypes))
		for key := range report.RelationTypes {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			builder.WriteString(fmt.Sprintf("- %s: %d\n", key, report.RelationTypes[key]))
		}
		builder.WriteString("\n")
	}

	if len(report.SuggestedQuestions) > 0 {
		builder.WriteString("## Suggested Questions\n")
		for _, question := range report.SuggestedQuestions {
			builder.WriteString("- " + question + "\n")
		}
	}
	return builder.String(), nil
}
