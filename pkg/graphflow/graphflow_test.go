package graphflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func TestBuildAnalyzeReportExport(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "graphflow.db")

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	extractions := []ExtractionResult{
		{
			SourceID:   "doc-1",
			SourceType: "markdown",
			Title:      "Apollo Plan",
			Nodes: []ExtractionNode{
				{ID: "apollo", Label: "Apollo", Type: "project"},
				{ID: "deadline", Label: "Friday Deadline", Type: "decision"},
			},
			Edges: []ExtractionEdge{
				{Source: "apollo", Target: "deadline", Relation: "has_deadline", Confidence: ConfidenceExtracted, Directed: true},
			},
		},
		{
			SourceID:   "doc-2",
			SourceType: "markdown",
			Title:      "Apollo Team",
			Nodes: []ExtractionNode{
				{ID: "apollo", Label: "Apollo", Type: "project"},
				{ID: "alice", Label: "Alice", Type: "person"},
			},
			Edges: []ExtractionEdge{
				{Source: "alice", Target: "apollo", Relation: "works_on", Confidence: ConfidenceExtracted, Directed: true},
			},
		},
	}

	buildResp, err := Build(context.Background(), db, extractions, BuildOptions{})
	if err != nil {
		t.Fatalf("build graphflow graph: %v", err)
	}
	if buildResp.NodeCount != 3 || buildResp.EdgeCount != 2 {
		t.Fatalf("unexpected build result: %+v", buildResp)
	}

	analysis, err := Analyze(context.Background(), db, AnalyzeRequest{TopN: 3})
	if err != nil {
		t.Fatalf("analyze graphflow graph: %v", err)
	}
	if analysis.NodeCount != 3 || analysis.EdgeCount != 2 {
		t.Fatalf("unexpected analysis: %+v", analysis)
	}
	if len(analysis.TopNodes) == 0 {
		t.Fatalf("expected top nodes, got %+v", analysis)
	}

	report, err := RenderReport(context.Background(), analysis)
	if err != nil {
		t.Fatalf("render report: %v", err)
	}
	if !strings.Contains(report, "GRAPHFLOW_REPORT") {
		t.Fatalf("unexpected report output: %s", report)
	}

	outDir := filepath.Join(t.TempDir(), "graphflow-out")
	exportResp, err := Export(context.Background(), db, ExportRequest{
		OutputDir: outDir,
		Analysis:  analysis,
		Report:    report,
	})
	if err != nil {
		t.Fatalf("export graphflow bundle: %v", err)
	}
	if _, err := os.Stat(exportResp.GraphJSON); err != nil {
		t.Fatalf("expected graph.json: %v", err)
	}
	if _, err := os.Stat(exportResp.ReportMarkdown); err != nil {
		t.Fatalf("expected GRAPH_REPORT.md: %v", err)
	}
}

func TestBuildRequiresIDs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), fmt.Sprintf("%s.db", t.Name()))
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	_, err = Build(context.Background(), db, []ExtractionResult{
		{
			SourceID: "doc",
			Nodes: []ExtractionNode{
				{ID: "", Label: "missing"},
			},
		},
	}, BuildOptions{})
	if err == nil {
		t.Fatal("expected build validation error")
	}
}
