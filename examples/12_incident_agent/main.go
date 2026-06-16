// Incident-analysis agent — a complex CortexDB example that REQUIRES an LLM
// (a chat/generation model, not an embedding model).
//
// The LLM is used in two distinct ways:
//
//  1. KNOWLEDGE EXTRACTION (GraphFlow): the model reads unstructured incident
//     reports and extracts a knowledge graph — services, errors, mitigations and
//     their causal relations — which CortexDB stores as RDF.
//  2. AGENTIC TOOL USE: an LLM agent answers an analytical question by deciding,
//     step by step, which CortexDB tool to call (lexical doc search, or the
//     GraphFlow centrality analysis), reading the results, and synthesizing a
//     grounded final answer.
//
// Retrieval is lexical and the graph analysis is deterministic — no embedding
// model is involved. Only the chat LLM is required.
//
// Usage:
//
//	OPENAI_API_KEY=sk-... OPENAI_BASE_URL=https://your-endpoint/v1 \
//	  go run ./examples/12_incident_agent -model gpt-5.5
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
	"github.com/openai/openai-go/v3"
)

// incident reports — unstructured free text. The common thread (db-primary as the
// recurring root cause) is only discoverable by reading and relating them.
var incidents = []graphflow.SourceDocument{
	{ID: "INC-101", Type: "incident", Title: "Checkout 500s", Content: `
On Tuesday the payment-gateway started returning HTTP 500s during checkout.
Root cause: db-primary exhausted its connection pool under peak load.
Mitigation: increased the db-primary pool size and added a circuit breaker.`},
	{ID: "INC-102", Type: "incident", Title: "Login latency", Content: `
auth-service p99 latency spiked to 4s. Investigation traced it to slow queries on
db-primary caused by a missing index. Mitigation: added the index; latency recovered.`},
	{ID: "INC-103", Type: "incident", Title: "Order sync stalled", Content: `
order-service stopped syncing. The cause was a db-primary failover that dropped
in-flight transactions. Mitigation: added retry-with-backoff to order-service.`},
	{ID: "INC-104", Type: "incident", Title: "Report export timeout", Content: `
The reporting job timed out. Unrelated to the database: an upstream S3 bucket was
throttling. Mitigation: added exponential backoff to the export client.`},
}

func main() {
	_ = godotenv.Load(".env", "../../.env")
	model := flagModel()
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		log.Fatal("this example REQUIRES an LLM — set OPENAI_API_KEY (and optionally OPENAI_BASE_URL)")
	}
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if err := run(newAIClient(baseURL, apiKey, model)); err != nil {
		log.Fatal(err)
	}
}

func flagModel() string {
	// default from OPENAI_MODEL (.env) so the example matches the active provider;
	// -model overrides.
	model := strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
	if model == "" {
		model = "gpt-5.5"
	}
	for i, a := range os.Args {
		if a == "-model" && i+1 < len(os.Args) {
			model = os.Args[i+1]
		}
	}
	return model
}

func run(ai *aiClient) error {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "incident-agent")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "brain.db")))
	if err != nil {
		return err
	}
	defer db.Close()

	// --- 1. LLM knowledge extraction → knowledge graph + RAG --------------------
	fmt.Println("=== LLM extracting a knowledge graph from incident reports ===")
	extractor := graphflow.LLMExtractor{Client: ai, MaxChars: 8000}
	var extractions []graphflow.ExtractionResult
	for _, doc := range incidents {
		ex, err := extractor.Extract(ctx, doc)
		if err != nil {
			return fmt.Errorf("extract %s: %w", doc.ID, err)
		}
		extractions = append(extractions, *ex)
		// also index the raw text for lexical doc search
		if _, err := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
			KnowledgeID: doc.ID, Title: doc.Title, Content: doc.Content,
		}); err != nil {
			return err
		}
		fmt.Printf("  %s: %d entities, %d relations\n", doc.ID, len(ex.Nodes), len(ex.Edges))
	}
	build, err := graphflow.Build(ctx, db, extractions, graphflow.BuildOptions{})
	if err != nil {
		return err
	}
	fmt.Printf("built knowledge graph: %d nodes, %d edges\n", build.NodeCount, build.EdgeCount)

	// --- 2. LLM agent answers an analytical question via CortexDB tools ---------
	question := "What is the most common root cause across these incidents, and what mitigations were applied for it? Cite incident IDs."
	fmt.Printf("\n=== agent question ===\n  %s\n\n=== agent trace ===\n", question)
	answer, err := agentLoop(ctx, ai, db, question)
	if err != nil {
		return err
	}
	fmt.Printf("\n=== final answer ===\n%s\n", indent(answer, "  "))
	return nil
}

const agentSystem = `You are an incident-analysis agent. Answer the user's question using ONLY information you obtain from tools.

Reply with ONE JSON object per turn, nothing else:
  {"action":"search_docs","query":"<keywords>"}   - lexical full-text search over incident reports; returns matching report text
  {"action":"top_entities"}                          - returns the most connected entities in the extracted knowledge graph (likely systemic causes)
  {"action":"final","answer":"<answer with cited INC- ids>"} - finish

Output EXACTLY ONE JSON object per turn — no prose, no markdown, no repeated objects. Wait for the tool result before your next turn.

Strategy: use top_entities to spot the systemic cause, then search_docs to gather the specific mitigations, then give a final answer citing incident IDs. Take at most 6 steps.`

func agentLoop(ctx context.Context, ai *aiClient, db *cortexdb.DB, question string) (string, error) {
	msgs := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(agentSystem),
		openai.UserMessage(question),
	}
	for step := 1; step <= 6; step++ {
		raw, err := ai.chat(ctx, msgs)
		if err != nil {
			return "", err
		}
		var act struct {
			Action string `json:"action"`
			Query  string `json:"query"`
			Answer string `json:"answer"`
		}
		// The model may emit several JSON objects or trailing prose; decode just the
		// first complete object.
		s := stripJSONFence(raw)
		if i := strings.IndexByte(s, '{'); i >= 0 {
			_ = json.NewDecoder(strings.NewReader(s[i:])).Decode(&act)
		}
		switch act.Action {
		case "search_docs":
			fmt.Printf("  [%d] search_docs(%q)\n", step, act.Query)
			result := searchDocs(ctx, db, act.Query)
			msgs = append(msgs, openai.AssistantMessage(raw), openai.UserMessage("TOOL RESULT:\n"+result))
		case "top_entities":
			fmt.Printf("  [%d] top_entities()\n", step)
			result := topEntities(ctx, db)
			msgs = append(msgs, openai.AssistantMessage(raw), openai.UserMessage("TOOL RESULT:\n"+result))
		case "final":
			fmt.Printf("  [%d] final\n", step)
			return act.Answer, nil
		default:
			// Model didn't follow the protocol; treat its text as the answer.
			return raw, nil
		}
	}
	return "(agent did not converge within step budget)", nil
}

// searchDocs is the lexical full-text search tool.
func searchDocs(ctx context.Context, db *cortexdb.DB, query string) string {
	res, err := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{Query: query, TopK: 4})
	if err != nil || len(res.Results) == 0 {
		return "(no matches)"
	}
	var b strings.Builder
	for _, h := range res.Results {
		full, _ := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: h.KnowledgeID})
		fmt.Fprintf(&b, "[%s] %s\n", h.KnowledgeID, strings.TrimSpace(full.Knowledge.Content))
	}
	return b.String()
}

// topEntities is the graph-centrality tool (deterministic GraphFlow analysis).
func topEntities(ctx context.Context, db *cortexdb.DB) string {
	analysis, err := graphflow.Analyze(ctx, db, graphflow.AnalyzeRequest{TopN: 6})
	if err != nil || len(analysis.TopNodes) == 0 {
		return "(no entities)"
	}
	var b strings.Builder
	for _, n := range analysis.TopNodes {
		fmt.Fprintf(&b, "- %s (%s) connections=%.0f\n", n.Label, n.Type, n.Score)
	}
	return b.String()
}

func indent(s, pre string) string {
	return pre + strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n"+pre)
}
