package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// recallHit is the one shape both recall paths reduce to: local search returns
// Go structs, the remote tool returns JSON, and the injected text must be
// identical either way.
type recallHit struct {
	KnowledgeID string `json:"knowledge_id"`
	Title       string `json:"title"`
	Snippet     string `json:"snippet"`
}

// recallToolName is the shared-brain tool that answers the same question
// SearchKnowledge answers locally.
const recallToolName = "knowledge_search"

// runRecallRemote answers the UserPromptSubmit hook from the shared brain
// instead of a local database file.
//
// Without this, `--recall` returns from main before the CORTEXDB_REMOTE branch
// is ever reached, so a machine using a shared brain still injected memories
// read from its own local file. That failure is silent and looks like working
// software: memories appear, they are just the wrong ones — frozen at whenever
// the machine switched over.
//
// Silent on every failure, like the local path: the hook must never block or
// delay the user's prompt with an error.
func runRecallRemote(addr, token, prompt string, topK int) string {
	conn, err := dialCortexDB(addr, token)
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()

	args, err := json.Marshal(map[string]any{"query": prompt, "top_k": topK})
	if err != nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), remoteDialTimeout)
	defer cancel()

	resp, err := rpcv1.NewToolsServiceClient(conn).CallTool(ctx, &rpcv1.CallToolRequest{
		Name:     recallToolName,
		ArgsJson: string(args),
	})
	if err != nil {
		return ""
	}

	hits, err := parseRecallPayload(resp.GetResultJson())
	if err != nil {
		return ""
	}
	return formatRecallHits(hits)
}

// parseRecallPayload reads hits out of the tool's JSON answer. Anything that is
// not a search result — an error object, a truncated body, a future response
// shape — yields no hits rather than an injected mess.
func parseRecallPayload(body string) ([]recallHit, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, nil
	}
	var payload struct {
		Results []recallHit `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, fmt.Errorf("recall payload is not search results: %w", err)
	}
	return payload.Results, nil
}

// formatRecallHits renders hits as the additionalContext block. Shared by both
// paths so the text an agent sees never depends on where the brain lives.
func formatRecallHits(hits []recallHit) string {
	var b strings.Builder
	b.WriteString("Relevant CortexDB memories for this prompt (retrieved automatically — verify before relying on them):\n")
	wrote := false
	for _, hit := range hits {
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
		return ""
	}
	// Nudge the save side too: recall keeps the brain useful only if new durable
	// facts also get written back.
	b.WriteString("(If this exchange states a durable preference, decision, or fact, save it with memory_save / knowledge_save.)")
	return b.String()
}
