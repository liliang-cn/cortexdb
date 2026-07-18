package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// newQueryTransformer builds a cortexdb.QueryTransformer backed by the chat LLM,
// or returns nil to keep retrieval on the raw query (the default). It is opt-in:
// query rewriting only activates when CORTEXDB_QUERY_REWRITE is truthy (1/true)
// AND a chat LLM is configured (CORTEXDB_LLM_BASE_URL). Reusing newOrganizeLLM
// means the same endpoint that powers graph distillation also powers pre-retrieval
// rewrite + HyDE, with no extra configuration.
//
//	CORTEXDB_QUERY_REWRITE  1/true to enable query rewriting + HyDE
func newQueryTransformer() cortexdb.QueryTransformer {
	if !envTruthy("CORTEXDB_QUERY_REWRITE") {
		return nil
	}
	llm := newOrganizeLLM()
	if llm == nil {
		return nil
	}
	return &llmQueryTransformer{llm: llm}
}

// envTruthy reports whether an env var is set to a truthy value (1/true/yes/on).
func envTruthy(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// llmQueryTransformer implements cortexdb.QueryTransformer by asking a chat LLM
// to expand a raw query into alternate phrasings, keywords, and a hypothetical
// answer passage (HyDE), returned as a single JSON object.
type llmQueryTransformer struct {
	llm graphflow.JSONGenerator
}

const queryTransformSystemPrompt = `You rewrite a user's search query to improve retrieval. Return ONLY a JSON object:
{"alternate_queries":["paraphrase or sub-question", ...],
 "keywords":["salient term", "synonym", "alias", ...],
 "hypothetical_document":"a short, plausible answer passage to the query"}
Rules: alternate_queries are 2-4 diverse rephrasings or decompositions of the query. keywords include synonyms, aliases, abbreviations, and multilingual variants of the key entities. hypothetical_document is 1-3 sentences written as if it were the ideal answer. Output JSON only, no prose.`

func (t *llmQueryTransformer) TransformQuery(ctx context.Context, query string) (*cortexdb.QueryTransform, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	raw, err := t.llm.GenerateJSON(ctx, queryTransformSystemPrompt, "Query: "+query)
	if err != nil {
		return nil, err
	}
	payload, err := parseQueryTransformPayload(raw)
	if err != nil {
		return nil, err
	}
	return &cortexdb.QueryTransform{
		AlternateQueries:     payload.AlternateQueries,
		Keywords:             payload.Keywords,
		HypotheticalDocument: strings.TrimSpace(payload.HypotheticalDocument),
	}, nil
}

type queryTransformPayload struct {
	AlternateQueries     []string `json:"alternate_queries"`
	Keywords             []string `json:"keywords"`
	HypotheticalDocument string   `json:"hypothetical_document"`
}

// queryTransformThinkRE strips <think>…</think> reasoning blocks that reasoning
// models may leak around the JSON (mirrors graphflow's organize parser).
var queryTransformThinkRE = regexp.MustCompile(`(?s)<think>.*?</think>`)

// parseQueryTransformPayload robustly extracts the JSON object from a model
// response: it strips reasoning blocks, then brace-extracts the outermost object
// so surrounding prose does not break parsing.
func parseQueryTransformPayload(raw []byte) (*queryTransformPayload, error) {
	text := queryTransformThinkRE.ReplaceAllString(string(raw), "")
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("query transform: no json object in model output")
	}
	var p queryTransformPayload
	if err := json.Unmarshal([]byte(text[start:end+1]), &p); err != nil {
		return nil, fmt.Errorf("query transform: invalid json: %w", err)
	}
	return &p, nil
}
