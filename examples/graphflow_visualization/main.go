// GraphFlow LLM Visualization Example
//
// Complete workflow demonstrating:
// 1. LLM-based knowledge extraction from text
// 2. Build knowledge graph
// 3. Analyze graph
// 4. Export to interactive HTML visualization
//
// Prerequisites:
//   - OpenAI API key or compatible endpoint (e.g., Ollama, Groq)
//   - .env file with credentials
//
// Run: go run . from this directory
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"

	"github.com/joho/godotenv"
)

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Printf("Note: .env file not found, using environment variables")
	}

	// Get LLM credentials (supports OpenAI-compatible APIs and Ollama)
	apiKey := os.Getenv("OPENAI_API_KEY")
	baseURL := os.Getenv("OPENAI_BASE_URL")
	model := os.Getenv("OPENAI_MODEL")

	// Also check for Ollama-specific env vars
	if baseURL == "" {
		baseURL = os.Getenv("OLLAMA_BASE_URL")
	}
	if model == "" {
		model = os.Getenv("OLLAMA_MODEL")
	}

	// Defaults
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if model == "" {
		model = "gpt-4o-mini"
	}

	ctx := context.Background()

	// Create output directory
	outputDir := "graphflow_output"
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output dir: %v", err)
	}

	// Open database
	dbPath := filepath.Join(outputDir, "knowledge.db")
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatalf("Failed to open db: %v", err)
	}
	defer db.Close()

	// Create LLM-backed extractor
	var extractor graphflow.Extractor
	if baseURL != "" && model != "" {
		fmt.Println("🔧 Configuring LLM extractor...")
		fmt.Printf("   Base URL: %s\n", baseURL)
		fmt.Printf("   Model: %s\n", model)

		extractor = &graphflow.LLMExtractor{
			Client:   newOpenAIClient(baseURL, apiKey, model),
			MaxChars: 8000,
		}
	} else {
		fmt.Println("⚠️  No LLM configured, using heuristic extraction instead")
		extractor = &graphflow.HeuristicExtractor{}
	}

	// Sample documents for extraction
	documents := []graphflow.SourceDocument{
		{
			ID:    "doc-1",
			Path:  "memory:person-info",
			Type:  "text",
			Title: "Team Information",
			Content: `
Alice Chen is a senior software engineer at TechCorp, leading the AI infrastructure team.
She works closely with Bob Smith, the product manager, and Carol Wang, the UX designer.
Together they are building CortexDB, a vector database for AI applications.

TechCorp is headquartered in San Francisco and specializes in artificial intelligence.
Alice has expertise in Go programming and is currently learning Rust.
She previously worked at Google and Meta.
			`,
			Metadata: map[string]string{"source": "team-memory"},
		},
		{
			ID:    "doc-2",
			Path:  "memory:project-info",
			Type:  "text",
			Title: "Project Details",
			Content: `
Project Cortex is the main initiative at TechCorp for 2024.
The goal is to build next-generation AI infrastructure using vector databases and RAG systems.

CortexDB is the core product, a fast and scalable vector database.
CortexFlow handles the workflow orchestration for AI pipelines.

Alice leads the CortexDB team while Bob manages the overall project.
The project uses Go for backend services and Python for ML components.
			`,
			Metadata: map[string]string{"source": "project-memory"},
		},
	}

	// Run extraction pipeline
	fmt.Println("\n🔍 Extracting knowledge with LLM...")
	var extractions []graphflow.ExtractionResult
	for _, doc := range documents {
		fmt.Printf("   Processing: %s\n", doc.Title)
		result, err := extractor.Extract(ctx, doc)
		if err != nil {
			log.Printf("   ⚠️ Extraction failed for %s: %v", doc.Title, err)
			continue
		}
		fmt.Printf("   ✓ Extracted %d nodes, %d edges\n", len(result.Nodes), len(result.Edges))
		extractions = append(extractions, *result)
	}

	if len(extractions) == 0 {
		log.Fatal("No extractions successful")
	}

	// Build the knowledge graph
	fmt.Println("\n🔨 Building knowledge graph...")
	buildResult, err := graphflow.Build(ctx, db, extractions, graphflow.BuildOptions{})
	if err != nil {
		log.Fatalf("Build failed: %v", err)
	}
	fmt.Printf("   ✓ Built graph: %d nodes, %d edges\n", buildResult.NodeCount, buildResult.EdgeCount)

	// Analyze the graph
	fmt.Println("\n📊 Analyzing graph...")
	analysis, err := graphflow.Analyze(ctx, db, graphflow.AnalyzeRequest{TopN: 10})
	if err != nil {
		log.Fatalf("Analyze failed: %v", err)
	}
	fmt.Printf("   ✓ Node types: %v\n", analysis.NodeTypes)
	fmt.Printf("   ✓ Relation types: %v\n", analysis.RelationTypes)
	if len(analysis.TopNodes) > 0 {
		fmt.Println("   ✓ Top entities:")
		for _, n := range analysis.TopNodes {
			fmt.Printf("      - %s (%s): score %.2f\n", n.Label, n.Type, n.Score)
		}
	}

	// Export to JSON and Markdown
	fmt.Println("\n📄 Exporting to JSON and Markdown...")
	exportReq := graphflow.ExportRequest{
		OutputDir: outputDir,
		Analysis:  analysis,
	}
	jsonResult, err := graphflow.Export(ctx, db, exportReq)
	if err != nil {
		log.Fatalf("Export (JSON) failed: %v", err)
	}
	fmt.Printf("   ✓ JSON: %s\n", jsonResult.GraphJSON)
	fmt.Printf("   ✓ Markdown: %s\n", jsonResult.ReportMarkdown)

	// Export to interactive HTML visualization
	fmt.Println("\n🌐 Generating interactive HTML visualization...")
	htmlResult, err := graphflow.ExportHTML(ctx, db, exportReq)
	if err != nil {
		log.Fatalf("ExportHTML failed: %v", err)
	}
	fmt.Printf("   ✓ HTML: %s\n", htmlResult.GraphHTML)

	fmt.Println(`
╔══════════════════════════════════════════════════════════════════╗
║                    ✅ Complete!                                ║
╠══════════════════════════════════════════════════════════════════╣
║                                                                  ║
║  Open this file in a browser to view the interactive graph:       ║
║                                                                  ║
║    open ` + htmlResult.GraphHTML + `
║                                                                  ║
║  Features:                                                       ║
║    • Click nodes to see details                                  ║
║    • Search nodes by label                                       ║
║    • Filter by node type                                         ║
║    • Switch between layouts (Force/Hierarchical/Circle)           ║
║    • Export to PNG                                               ║
║                                                                  ║
╚══════════════════════════════════════════════════════════════════╝
`)

	// Print markdown report
	report, _ := os.ReadFile(jsonResult.ReportMarkdown)
	fmt.Println("\n=== Graph Report ===")
	fmt.Println(string(report))
}
