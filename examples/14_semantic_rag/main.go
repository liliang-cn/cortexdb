// Semantic RAG — CortexDB with a real embedding model (vector search), plus an
// LLM answer. This is the embedder-backed counterpart to the lexical examples:
// it wires a DashScope (Qwen) text-embedding-v4 embedder so retrieval works on
// MEANING, not keyword overlap, and uses qwen for the final answer.
//
// Reads config from the environment (or a .env via godotenv):
//
//	OPENAI_API_KEY    (required)
//	OPENAI_BASE_URL   default https://dashscope.aliyuncs.com/compatible-mode/v1
//	OPENAI_MODEL      chat model, default qwen3.7-plus
//	EMBED_MODEL       embedding model, default text-embedding-v4
//	EMBED_DIM         embedding dimension, default 1024
//
//	go run ./examples/14_semantic_rag
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// knowledge base: short facts on distinct topics. None of them share keywords
// with the semantic query below — only meaning connects them.
var docs = map[string]string{
	"doc-payments": "Stripe handles our card transactions and issues refunds to customers.",
	"doc-oncall":   "When the site goes down at night, the engineer on rotation gets paged and must respond.",
	"doc-hr":       "New hires complete onboarding paperwork and a benefits enrollment form in their first week.",
	"doc-infra":    "The Kubernetes cluster autoscales pods based on CPU and memory pressure.",
}

func main() {
	_ = godotenv.Load(".env", "../../.env")
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		log.Fatal("this example REQUIRES an embedding model — set OPENAI_API_KEY (DashScope/OpenAI-compatible)")
	}
	baseURL := envOr("OPENAI_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1")
	chatModel := envOr("OPENAI_MODEL", "qwen3.7-plus")
	embedModel := envOr("EMBED_MODEL", "text-embedding-v4")
	dim, _ := strconv.Atoi(envOr("EMBED_DIM", "1024"))
	if err := run(apiKey, baseURL, chatModel, embedModel, dim); err != nil {
		log.Fatal(err)
	}
}

func run(apiKey, baseURL, chatModel, embedModel string, dim int) error {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "semantic-rag")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	// open CortexDB with a REAL embedder → vector (semantic) search instead of lexical.
	embedder := newDashscopeEmbedder(baseURL, apiKey, embedModel, dim)
	cfg := cortexdb.DefaultConfig(filepath.Join(dir, "brain.db"))
	cfg.Dimensions = dim
	db, err := cortexdb.Open(cfg, cortexdb.WithEmbedder(embedder))
	if err != nil {
		return err
	}
	defer db.Close()

	fmt.Printf("embedder=%s (dim=%d), chat=%s\n\n", embedModel, dim, chatModel)
	for id, content := range docs {
		if err := db.InsertText(ctx, id, content, nil); err != nil {
			return err
		}
	}

	// A paraphrase with NO keyword overlap with the target doc ("on-call"):
	// lexical FTS would miss it; semantic vector search finds it by meaning.
	query := "who deals with production outages after hours?"
	fmt.Printf("=== semantic vector search (no keyword overlap): %q ===\n", query)
	res, err := db.SearchText(ctx, query, 3)
	if err != nil {
		return err
	}
	var top string
	for i, h := range res {
		if i == 0 {
			top = h.Content
		}
		fmt.Printf("  %d. [%s score=%.3f] %s\n", i+1, h.ID, h.Score, h.Content)
	}

	// LLM answer grounded on the semantically-retrieved context.
	client := openai.NewClient(option.WithAPIKey(apiKey), option.WithBaseURL(baseURL))
	comp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: chatModel,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("Answer the question in one sentence using only the provided context."),
			openai.UserMessage("Question: " + query + "\nContext: " + top),
		},
	})
	if err != nil {
		return err
	}
	if len(comp.Choices) > 0 {
		fmt.Printf("\n=== %s answer ===\n  %s\n", chatModel, strings.TrimSpace(comp.Choices[0].Message.Content))
	}
	fmt.Println("\n✓ semantic RAG: embedding-powered vector retrieval (meaning, not keywords) + LLM answer")
	return nil
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
