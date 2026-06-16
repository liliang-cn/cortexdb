// Dogfooding demo: turn docs/PROJECT_OVERVIEW.md (CortexDB's own project
// overview) into a knowledge graph using CortexDB's graphflow workflow, with an
// LLM extractor.
//
// LLM config (any OpenAI-compatible endpoint), via env or a .env (godotenv):
//   OPENAI_API_KEY + OPENAI_BASE_URL + OPENAI_MODEL   (e.g. DashScope qwen-plus)
// If those are unset it falls back to a local Ollama (OLLAMA_BASE / OLLAMA_MODEL,
// default http://localhost:11434 + qwen3.5).
//
// Run:     go run ./examples/08_self_knowledge_graph
// Output:  examples/08_self_knowledge_graph/out/ (graph JSON, report, HTML viz)
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// ollamaJSONGenerator implements graphflow.JSONGenerator against Ollama's
// native chat API. think:false matters: qwen3.5 is a reasoning model and
// would otherwise burn minutes on a reasoning trace; format:json forces
// well-formed output.
type ollamaJSONGenerator struct {
	baseURL string
	model   string
	client  *http.Client
}

func (o *ollamaJSONGenerator) GenerateJSON(ctx context.Context, systemPrompt, userPrompt string) ([]byte, error) {
	body, err := json.Marshal(map[string]any{
		"model":  o.model,
		"think":  false,
		"stream": false,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned %s", resp.Status)
	}
	var parsed struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	return []byte(parsed.Message.Content), nil
}

// openAICompatJSONGenerator implements graphflow.JSONGenerator against any
// OpenAI-compatible /chat/completions endpoint (e.g. DashScope qwen-plus).
type openAICompatJSONGenerator struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

func (o *openAICompatJSONGenerator) GenerateJSON(ctx context.Context, systemPrompt, userPrompt string) ([]byte, error) {
	body, err := json.Marshal(map[string]any{
		"model": o.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"response_format": map[string]string{"type": "json_object"},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.baseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chat endpoint returned %s", resp.Status)
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("chat endpoint returned no choices")
	}
	return []byte(parsed.Choices[0].Message.Content), nil
}

// repairingGenerator wraps a JSONGenerator and sanitizes what the local
// model returns before graphflow's strict validation sees it: strips prose
// around the JSON object, drops edges whose endpoints were never declared
// as nodes, and defaults unknown confidence values to EXTRACTED.
type repairingGenerator struct {
	inner graphflow.JSONGenerator
}

func (r *repairingGenerator) GenerateJSON(ctx context.Context, systemPrompt, userPrompt string) ([]byte, error) {
	raw, err := r.inner.GenerateJSON(ctx, systemPrompt, userPrompt)
	if err != nil {
		return nil, err
	}
	text := string(raw)
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("model did not return a JSON object: %.200s", text)
	}
	var payload struct {
		Nodes []graphflow.ExtractionNode `json:"nodes"`
		Edges []graphflow.ExtractionEdge `json:"edges"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &payload); err != nil {
		return nil, fmt.Errorf("model returned invalid JSON: %w", err)
	}
	nodeIDs := make(map[string]struct{}, len(payload.Nodes))
	for _, n := range payload.Nodes {
		nodeIDs[n.ID] = struct{}{}
	}
	kept := payload.Edges[:0]
	for _, e := range payload.Edges {
		if _, ok := nodeIDs[e.Source]; !ok {
			continue
		}
		if _, ok := nodeIDs[e.Target]; !ok {
			continue
		}
		switch e.Confidence {
		case graphflow.ConfidenceExtracted, graphflow.ConfidenceInferred, graphflow.ConfidenceAmbiguous:
		default:
			e.Confidence = graphflow.ConfidenceExtracted
		}
		kept = append(kept, e)
	}
	payload.Edges = kept
	return json.Marshal(payload)
}

// splitSections turns the overview markdown into one SourceDocument per
// "## " section so each extraction call gets a focused chunk.
func splitSections(markdown string) []graphflow.SourceDocument {
	var docs []graphflow.SourceDocument
	sections := strings.Split(markdown, "\n## ")
	for i, section := range sections[1:] {
		lines := strings.SplitN(section, "\n", 2)
		title := strings.TrimSpace(lines[0])
		content := ""
		if len(lines) > 1 {
			content = strings.TrimSpace(lines[1])
		}
		if content == "" {
			continue
		}
		docs = append(docs, graphflow.SourceDocument{
			ID:      fmt.Sprintf("overview-%02d", i+1),
			Path:    "docs/PROJECT_OVERVIEW.md#" + title,
			Type:    "markdown",
			Title:   title,
			Content: content,
		})
	}
	return docs
}

func firstExisting(paths ...string) string {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return paths[0]
}

func main() {
	_ = godotenv.Load(".env", "../../.env")
	ollamaBase := os.Getenv("OLLAMA_BASE")
	if ollamaBase == "" {
		ollamaBase = "http://localhost:11434"
	}
	model := os.Getenv("OLLAMA_MODEL")
	if model == "" {
		model = "qwen3.5"
	}

	overviewPath := firstExisting("docs/PROJECT_OVERVIEW.md", "../../docs/PROJECT_OVERVIEW.md")
	outDir := firstExisting("examples/08_self_knowledge_graph", "../../examples/08_self_knowledge_graph") + "/out"
	dbPath := outDir + "/self_kg.db"

	markdown, err := os.ReadFile(overviewPath)
	if err != nil {
		log.Fatalf("read overview: %v", err)
	}
	docs := splitSections(string(markdown))
	fmt.Printf("overview sections: %d\n", len(docs))

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		log.Fatal(err)
	}
	_ = os.Remove(dbPath)
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	// Prefer the standard OPENAI_* config (from .env, an OpenAI-compatible endpoint
	// such as DashScope) — same as the other examples. EXTRACT_* overrides it, and
	// a local Ollama is the last-resort fallback when no endpoint is configured.
	base := firstNonEmpty(os.Getenv("EXTRACT_BASE_URL"), os.Getenv("OPENAI_BASE_URL"))
	apiKey := firstNonEmpty(os.Getenv("EXTRACT_API_KEY"), os.Getenv("OPENAI_API_KEY"))
	var generator graphflow.JSONGenerator
	if base != "" && apiKey != "" {
		extractModel := firstNonEmpty(os.Getenv("EXTRACT_MODEL"), os.Getenv("OPENAI_MODEL"), "qwen-plus")
		fmt.Printf("extractor: %s via %s\n", extractModel, base)
		generator = &openAICompatJSONGenerator{
			baseURL: base,
			apiKey:  apiKey,
			model:   extractModel,
			client:  &http.Client{Timeout: 5 * time.Minute},
		}
	} else {
		fmt.Printf("extractor: %s via %s (local ollama; set OPENAI_API_KEY/OPENAI_BASE_URL in .env to use a hosted model)\n", model, ollamaBase)
		generator = &ollamaJSONGenerator{
			baseURL: ollamaBase,
			model:   model,
			client:  &http.Client{Timeout: 5 * time.Minute},
		}
	}
	extractor := graphflow.LLMExtractor{
		Client:   &repairingGenerator{inner: generator},
		MaxChars: 8000,
	}

	var extractions []graphflow.ExtractionResult
	for _, doc := range docs {
		started := time.Now()
		extraction, err := extractor.Extract(ctx, doc)
		if err != nil {
			log.Fatalf("extract %s: %v", doc.ID, err)
		}
		extractions = append(extractions, *extraction)
		fmt.Printf("  %-12s %-14s nodes=%-3d edges=%-3d (%.1fs)\n",
			doc.ID, doc.Title, len(extraction.Nodes), len(extraction.Edges), time.Since(started).Seconds())
	}

	build, err := graphflow.Build(ctx, db, extractions, graphflow.BuildOptions{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("knowledge graph built: nodes=%d edges=%d\n", build.NodeCount, build.EdgeCount)

	analysis, err := graphflow.Analyze(ctx, db, graphflow.AnalyzeRequest{TopN: 10})
	if err != nil {
		log.Fatal(err)
	}
	report, err := graphflow.RenderReport(ctx, analysis)
	if err != nil {
		log.Fatal(err)
	}

	export, err := graphflow.Export(ctx, db, graphflow.ExportRequest{OutputDir: outDir, Analysis: analysis, Report: report})
	if err != nil {
		log.Fatal(err)
	}
	html, err := graphflow.ExportHTML(ctx, db, graphflow.ExportRequest{OutputDir: outDir, Analysis: analysis})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println()
	fmt.Println(report)
	fmt.Printf("artifacts:\n  db:     %s\n  graph:  %s\n  report: %s\n  html:   %s\n",
		dbPath, export.GraphJSON, export.ReportMarkdown, html.GraphHTML)
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
