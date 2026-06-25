package main

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"unicode"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// hookInput is the subset of the UserPromptSubmit hook payload we read from stdin.
type hookInput struct {
	Prompt string `json:"prompt"`
}

type hookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

type hookOutput struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

// runRecall reads a UserPromptSubmit hook payload from stdin, searches durable
// knowledge for the prompt, and prints matched memories as additionalContext for
// Claude Code to inject.
//
// It is deliberately silent on every failure path: a missing DB, an empty
// prompt, an open error, or zero results all exit 0 with no output, so the
// user's prompt is never blocked or delayed by an error message.
func runRecall() {
	var in hookInput
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		return
	}
	prompt := strings.TrimSpace(in.Prompt)
	if prompt == "" {
		return
	}

	dbPath := os.Getenv("CORTEXDB_PATH")
	if dbPath == "" {
		dbPath = cortexdb.DefaultDBPath()
	}
	// Never create a database just to recall from it: if it isn't already
	// there, this project simply has no CortexDB memory yet.
	if info, err := os.Stat(dbPath); err != nil || info.IsDir() {
		return
	}

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		return
	}
	defer func() { _ = db.Close() }()

	topK := envInt("CORTEXDB_RECALL_TOPK", 3)

	resp, err := db.SearchKnowledge(context.Background(), cortexdb.KnowledgeSearchRequest{
		Query:         prompt,
		Keywords:      keywordsFromPrompt(prompt),
		RetrievalMode: cortexdb.RetrievalModeAuto,
		GraphLight:    true,
		TopK:          topK,
	})
	if err != nil || resp == nil || len(resp.Results) == 0 {
		return
	}

	var b strings.Builder
	b.WriteString("Relevant CortexDB memories for this prompt (retrieved automatically — verify before relying on them):\n")
	wrote := false
	for _, hit := range resp.Results {
		snippet := strings.TrimSpace(hit.Snippet)
		if snippet == "" {
			continue
		}
		title := hit.Title
		if title == "" {
			title = hit.KnowledgeID
		}
		b.WriteString("- ")
		b.WriteString(title)
		b.WriteString(": ")
		b.WriteString(snippet)
		b.WriteString("\n")
		wrote = true
	}
	if !wrote {
		return
	}

	out := hookOutput{HookSpecificOutput: hookSpecificOutput{
		HookEventName:     "UserPromptSubmit",
		AdditionalContext: strings.TrimRight(b.String(), "\n"),
	}}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}

// envInt reads a positive integer from an env var, falling back to def.
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// keywordsFromPrompt splits a natural-language prompt into deduped lexical
// terms. CJK runs stay as single tokens, matching the FTS5 unicode61 tokenizer.
func keywordsFromPrompt(prompt string) []string {
	fields := strings.FieldsFunc(prompt, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := make(map[string]bool, len(fields))
	kw := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.ToLower(f)
		if len([]rune(f)) < 2 || seen[f] {
			continue
		}
		seen[f] = true
		kw = append(kw, f)
	}
	return kw
}
