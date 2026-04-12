package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

func main() {
	dbPath := "graphflow_basic.db"
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	extractions := []graphflow.ExtractionResult{
		{
			SourceID:   "apollo-plan",
			SourceType: "note",
			Title:      "Apollo Plan",
			Nodes: []graphflow.ExtractionNode{
				{ID: "apollo", Label: "Apollo", Type: "project"},
				{ID: "alice", Label: "Alice", Type: "person"},
				{ID: "deadline", Label: "Friday Deadline", Type: "decision"},
			},
			Edges: []graphflow.ExtractionEdge{
				{Source: "alice", Target: "apollo", Relation: "works_on", Confidence: graphflow.ConfidenceExtracted, Directed: true},
				{Source: "apollo", Target: "deadline", Relation: "has_deadline", Confidence: graphflow.ConfidenceExtracted, Directed: true},
			},
		},
	}

	buildResp, err := graphflow.Build(ctx, db, extractions, graphflow.BuildOptions{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("built nodes=%d edges=%d\n", buildResp.NodeCount, buildResp.EdgeCount)

	analysis, err := graphflow.Analyze(ctx, db, graphflow.AnalyzeRequest{TopN: 5})
	if err != nil {
		log.Fatal(err)
	}
	report, err := graphflow.RenderReport(ctx, analysis)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(report)

	exportResp, err := graphflow.Export(ctx, db, graphflow.ExportRequest{
		OutputDir: "graphflow-out",
		Analysis:  analysis,
		Report:    report,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("exported %s and %s\n", exportResp.GraphJSON, exportResp.ReportMarkdown)
}
