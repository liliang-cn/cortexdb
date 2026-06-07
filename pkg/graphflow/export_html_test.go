package graphflow_test

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// TestExportHTML demonstrates the complete GraphFlow workflow including HTML visualization.
// Run this test to generate an interactive HTML visualization of a knowledge graph.
func TestExportHTML(t *testing.T) {
	ctx := context.Background()

	// Create a temporary directory for output
	tmpDir, err := os.MkdirTemp("", "graphflow-html-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Open a test database
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	// Build a sample graph using GraphFlow pipeline
	// This simulates extracting knowledge from documents
	extractions := []graphflow.ExtractionResult{
		{
			SourceID:   "doc-1",
			SourceType: "person-info",
			Title:      "Person Information",
			Nodes: []graphflow.ExtractionNode{
				{ID: "alice", Label: "Alice Chen", Type: "Person", Summary: "A software engineer at TechCorp", Metadata: map[string]string{"source_file": "team.csv"}},
				{ID: "bob", Label: "Bob Smith", Type: "Person", Summary: "A product manager at TechCorp", Metadata: map[string]string{"source_file": "team.csv"}},
				{ID: "techcorp", Label: "TechCorp", Type: "Organization", Summary: "A technology company", Metadata: map[string]string{"source_file": "team.csv"}},
				{ID: "sf", Label: "San Francisco", Type: "Location", Summary: "A city in California", Metadata: map[string]string{"source_file": "team.csv"}},
				{ID: "golang", Label: "Go Programming", Type: "Concept", Summary: "A programming language", Metadata: map[string]string{"source_file": "team.csv"}},
			},
			Edges: []graphflow.ExtractionEdge{
				{Source: "alice", Target: "techcorp", Relation: "works_at", Confidence: graphflow.ConfidenceExtracted, Directed: true},
				{Source: "bob", Target: "techcorp", Relation: "works_at", Confidence: graphflow.ConfidenceExtracted, Directed: true},
				{Source: "alice", Target: "bob", Relation: "reports_to", Confidence: graphflow.ConfidenceExtracted, Directed: true},
				{Source: "techcorp", Target: "sf", Relation: "located_in", Confidence: graphflow.ConfidenceExtracted, Directed: true},
				{Source: "alice", Target: "golang", Relation: "knows", Confidence: graphflow.ConfidenceInferred, Directed: false},
			},
		},
		{
			SourceID:   "doc-2",
			SourceType: "project-info",
			Title:      "Project Information",
			Nodes: []graphflow.ExtractionNode{
				{ID: "cortexdb", Label: "CortexDB", Type: "Product", Summary: "A vector database", Metadata: map[string]string{"source_file": "project.md"}},
				{ID: "project-x", Label: "Project X", Type: "Project", Summary: "An AI research project", Metadata: map[string]string{"source_file": "project.md"}},
				{ID: "ml", Label: "Machine Learning", Type: "Concept", Summary: "AI field", Metadata: map[string]string{"source_file": "project.md"}},
			},
			Edges: []graphflow.ExtractionEdge{
				{Source: "alice", Target: "project-x", Relation: "leads", Confidence: graphflow.ConfidenceExtracted, Directed: true},
				{Source: "project-x", Target: "cortexdb", Relation: "uses", Confidence: graphflow.ConfidenceExtracted, Directed: true},
				{Source: "project-x", Target: "ml", Relation: "applies", Confidence: graphflow.ConfidenceExtracted, Directed: true},
				{Source: "bob", Target: "project-x", Relation: "manages", Confidence: graphflow.ConfidenceExtracted, Directed: true},
			},
		},
	}

	// Build the graph
	buildResult, err := graphflow.Build(ctx, db, extractions, graphflow.BuildOptions{})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	t.Logf("Built graph: %d nodes, %d edges", buildResult.NodeCount, buildResult.EdgeCount)

	// Analyze the graph
	analysis, err := graphflow.Analyze(ctx, db, graphflow.AnalyzeRequest{TopN: 5})
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}
	t.Logf("Analysis: %d nodes, %d edges", analysis.NodeCount, analysis.EdgeCount)
	if len(analysis.TopNodes) > 0 {
		t.Logf("Top nodes: %+v", analysis.TopNodes)
	}

	// Export to JSON (original functionality)
	exportReq := graphflow.ExportRequest{
		OutputDir: tmpDir,
		Analysis:  analysis,
	}
	jsonResult, err := graphflow.Export(ctx, db, exportReq)
	if err != nil {
		t.Fatalf("Export (JSON) failed: %v", err)
	}
	t.Logf("Exported JSON: %s", jsonResult.GraphJSON)
	t.Logf("Exported Markdown: %s", jsonResult.ReportMarkdown)

	// NEW: Export to HTML visualization
	htmlResult, err := graphflow.ExportHTML(ctx, db, exportReq)
	if err != nil {
		t.Fatalf("ExportHTML failed: %v", err)
	}
	t.Logf("Exported HTML: %s", htmlResult.GraphHTML)

	// Verify the HTML file exists and has content
	htmlContent, err := os.ReadFile(htmlResult.GraphHTML)
	if err != nil {
		t.Fatalf("Failed to read HTML file: %v", err)
	}
	if len(htmlContent) == 0 {
		t.Fatal("HTML file is empty")
	}

	// Verify HTML contains expected elements
	html := string(htmlContent)
	if !contains(html, "cytoscape") {
		t.Error("HTML missing Cytoscape library")
	}
	if !contains(html, "GRAPH_DATA") {
		t.Error("HTML missing graph data")
	}
	if !contains(html, "CortexDB GraphFlow Visualization") {
		t.Error("HTML missing title")
	}

	// Verify JSON graph data is available for programmatic access. The HTML embeds
	// the same payload as base64, so raw JSON keys are validated in graph.json.
	graphJSON, err := os.ReadFile(htmlResult.GraphJSON)
	if err != nil {
		t.Fatalf("Failed to read graph JSON: %v", err)
	}
	graphJSONText := string(graphJSON)
	if !contains(graphJSONText, `"id"`) || !contains(graphJSONText, `"label"`) {
		t.Error("graph.json missing node structure")
	}

	// List output files
	files, _ := os.ReadDir(tmpDir)
	t.Logf("\n=== Output files in %s ===", tmpDir)
	for _, f := range files {
		info, _ := f.Info()
		t.Logf("  %s (%.1f KB)", f.Name(), float64(info.Size())/1024)
	}

	t.Logf("\n=== SUCCESS ===")
	t.Logf("Open this file in a browser to view the interactive graph:")
	t.Logf("  open %s", htmlResult.GraphHTML)
}

// contains is a simple helper
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr, 0))
}

func containsAt(s, substr string, start int) bool {
	for i := start; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestExportHTMLUnicodeLabels guards against mojibake: the embedded payload is
// base64, and the template must decode it as UTF-8 (plain atob() yields a
// Latin-1 byte string, garbling any non-ASCII label).
func TestExportHTMLUnicodeLabels(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(tmpDir, "test.db")))
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	extractions := []graphflow.ExtractionResult{
		{
			SourceID: "doc-cn",
			Title:    "中文文档",
			Nodes: []graphflow.ExtractionNode{
				{ID: "facade", Label: "公共门面层", Type: "layer"},
				{ID: "engine", Label: "引擎层", Type: "layer"},
			},
			Edges: []graphflow.ExtractionEdge{
				{Source: "engine", Target: "facade", Relation: "支撑", Confidence: graphflow.ConfidenceExtracted},
			},
		},
	}
	if _, err := graphflow.Build(ctx, db, extractions, graphflow.BuildOptions{}); err != nil {
		t.Fatalf("build: %v", err)
	}

	result, err := graphflow.ExportHTML(ctx, db, graphflow.ExportRequest{OutputDir: tmpDir})
	if err != nil {
		t.Fatalf("export html: %v", err)
	}
	html, err := os.ReadFile(result.GraphHTML)
	if err != nil {
		t.Fatalf("read html: %v", err)
	}

	// The template must decode the base64 payload as UTF-8, not Latin-1.
	if !strings.Contains(string(html), "TextDecoder") {
		t.Fatal("graph.html must decode the embedded payload with TextDecoder('utf-8')")
	}

	// The embedded payload itself must round-trip the Chinese labels.
	re := regexp.MustCompile(`atob\('([A-Za-z0-9+/=]+)'\)`)
	match := re.FindSubmatch(html)
	if match == nil {
		t.Fatal("embedded base64 payload not found in graph.html")
	}
	payload, err := base64.StdEncoding.DecodeString(string(match[1]))
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !strings.Contains(string(payload), "公共门面层") {
		t.Fatalf("payload lost the Chinese label: %.200s", payload)
	}
}
