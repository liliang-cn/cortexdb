package memoryflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// MemoryEditGenerator returns JSON given a system and user prompt. Declared here
// rather than imported from graphflow so the two workflow layers stay
// independent — a caller wiring only memory maintenance should not have to pull
// in the graph pipeline.
type MemoryEditGenerator interface {
	GenerateJSON(ctx context.Context, systemPrompt, userPrompt string) ([]byte, error)
}

const memoryEditSystemPrompt = "You maintain an agent's long-term memory. You are given EXISTING MEMORIES (each with an id) and NEW TEXT. " +
	"Produce the minimal set of edits that makes the memory correctly reflect the new text. " +
	"Use op=add for a genuinely new durable fact, preference or decision. " +
	"Use op=update ONLY to fix wording or an outright error in an existing memory, keeping its meaning. " +
	"Use op=supersede when an existing memory was true before and the new text makes it no longer true — the old text is kept and linked forward, so prefer supersede over update whenever the history is worth reading later. " +
	"Never supersede a memory merely because the new text does not mention it, and never supersede one you are not confident refers to the same fact. " +
	"When in doubt, add and leave the old one alone: a duplicate is cheap, a wrongly retired memory is not. " +
	"Do not record transient chatter — only things worth knowing weeks from now. " +
	"Return JSON only: {\"edits\":[{\"op\":\"add|update|supersede\",\"memory_id\":\"\",\"content\":\"\",\"reason\":\"\"}]}. " +
	"memory_id must be one of the ids given; omit it for add. Return an empty edits array if nothing should change."

const defaultMaxCandidates = 30

// ProposeMemoryEdits asks the model which existing memories the new text
// changes, without applying anything.
//
// Separate from ApplyMemoryEdits on purpose: the proposal is the part that can
// be wrong, so it must be inspectable — printed, reviewed, or fed to a dry run —
// before anything touches the brain.
func ProposeMemoryEdits(ctx context.Context, db *cortexdb.DB, text string, llm MemoryEditGenerator, opts MemoryEditOptions) (*MemoryEditPlan, error) {
	if db == nil {
		return nil, fmt.Errorf("memoryflow: memory edit: nil db")
	}
	if llm == nil {
		return nil, fmt.Errorf("memoryflow: memory edit requires an LLM")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return &MemoryEditPlan{}, nil
	}

	existing, err := candidateMemories(ctx, db, text, opts)
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString("EXISTING MEMORIES:\n")
	if existing == "" {
		b.WriteString("(none stored yet that relate to this text)\n")
	} else {
		b.WriteString(existing)
	}
	b.WriteString("\nNEW TEXT:\n")
	b.WriteString(truncateRunes(text, 6000))
	b.WriteString("\n\nReturn the edits. JSON only.")

	raw, err := llm.GenerateJSON(ctx, memoryEditSystemPrompt, b.String())
	if err != nil {
		return nil, fmt.Errorf("memoryflow: memory edit: llm: %w", err)
	}
	obj, err := extractJSONObject(raw)
	if err != nil {
		return nil, fmt.Errorf("memoryflow: memory edit: %w", err)
	}
	var plan MemoryEditPlan
	if err := json.Unmarshal(obj, &plan); err != nil {
		return nil, fmt.Errorf("memoryflow: memory edit: decode plan: %w", err)
	}
	return &plan, nil
}

// UpdateMemoryFromText proposes and applies in one pass. Prefer a DryRun first
// on anything that matters.
func UpdateMemoryFromText(ctx context.Context, db *cortexdb.DB, text string, llm MemoryEditGenerator, opts MemoryEditOptions) (*MemoryEditReport, error) {
	plan, err := ProposeMemoryEdits(ctx, db, text, llm, opts)
	if err != nil {
		return nil, err
	}
	return ApplyMemoryEdits(ctx, db, *plan, opts)
}

// candidateMemories renders the memories the text might change. Retrieval is
// the ordinary memory search, so this works with or without an embedder;
// already-retired memories are left out so the model cannot retire one twice or
// resurrect a superseded claim.
func candidateMemories(ctx context.Context, db *cortexdb.DB, text string, opts MemoryEditOptions) (string, error) {
	limit := opts.MaxCandidates
	if limit <= 0 {
		limit = defaultMaxCandidates
	}

	resp, err := db.SearchMemory(ctx, cortexdb.MemorySearchRequest{
		Query:     text,
		Scope:     opts.Scope,
		UserID:    opts.UserID,
		Namespace: opts.Namespace,
		TopK:      limit,
	})
	if err != nil {
		return "", fmt.Errorf("memoryflow: memory edit: search candidates: %w", err)
	}
	if resp == nil {
		return "", nil
	}

	var b strings.Builder
	for _, hit := range resp.Results {
		m := hit.Memory
		if m.Metadata[supersededByKey] != nil {
			continue // already retired; not a candidate for anything
		}
		b.WriteString("- id=")
		b.WriteString(m.ID)
		b.WriteString(": ")
		b.WriteString(truncateRunes(strings.TrimSpace(m.Content), 400))
		b.WriteString("\n")
	}
	return b.String(), nil
}

// truncateRunes bounds a string by runes, so a cut never lands inside a
// multi-byte character.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// extractJSONObject pulls the first JSON object out of a model's answer, which
// routinely arrives wrapped in prose or a fenced code block.
func extractJSONObject(raw []byte) ([]byte, error) {
	s := strings.TrimSpace(string(raw))
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			return []byte(s[i : j+1]), nil
		}
	}
	return nil, fmt.Errorf("no JSON object in model output")
}
