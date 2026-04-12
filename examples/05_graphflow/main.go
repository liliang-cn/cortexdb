package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

func main() {
	_ = godotenv.Load(".env", "../../.env")

	dbPath := "example_graphflow.db"
	outputDir := "example_graphflow_out"
	defer func() { _ = os.Remove(dbPath) }()
	defer func() { _ = os.RemoveAll(outputDir) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	doc := graphflow.SourceDocument{
		ID:    "apollo-plan",
		Path:  "memory:apollo-plan",
		Type:  "note",
		Title: "Apollo Plan",
		Content: `
Alice owns the Apollo launch plan.
Apollo ships on Friday.
Bob writes the release notes.
Carol validates the rollout checklist.
`,
	}

	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if model == "" {
		model = "gpt-5.4"
	}

	extractor := graphflow.Extractor(graphflow.HeuristicExtractor{})
	if apiKey != "" {
		client := openai.NewClient(
			option.WithAPIKey(apiKey),
			option.WithBaseURL(baseURL),
		)
		fmt.Printf("using OpenAI SDK extractor: model=%s base_url=%s\n", model, baseURL)
		extractor = graphflow.LLMExtractor{
			Client:   &openAIJSONGenerator{client: client, model: model},
			MaxChars: 8000,
		}
	} else {
		fmt.Println("using deterministic heuristic extractor; set OPENAI_API_KEY, OPENAI_BASE_URL, and OPENAI_MODEL for LLM extraction")
	}

	extraction, err := extractor.Extract(ctx, doc)
	if err != nil {
		log.Fatal(err)
	}
	extractions := []graphflow.ExtractionResult{*extraction}

	build, err := graphflow.Build(ctx, db, extractions, graphflow.BuildOptions{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("built graphflow graph: nodes=%d edges=%d\n", build.NodeCount, build.EdgeCount)

	analysis, err := graphflow.Analyze(ctx, db, graphflow.AnalyzeRequest{TopN: 5})
	if err != nil {
		log.Fatal(err)
	}
	report, err := graphflow.RenderReport(ctx, analysis)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(report)

	export, err := graphflow.Export(ctx, db, graphflow.ExportRequest{OutputDir: outputDir, Analysis: analysis, Report: report})
	if err != nil {
		log.Fatal(err)
	}
	html, err := graphflow.ExportHTML(ctx, db, graphflow.ExportRequest{OutputDir: outputDir, Analysis: analysis})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("exported: %s, %s, %s\n", export.GraphJSON, export.ReportMarkdown, html.GraphHTML)
}
