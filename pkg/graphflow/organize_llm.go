package graphflow

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// organizeLLMMaxCharsPerBatch bounds how much brain text is sent per LLM call.
// Memories and knowledge snippets are short, so a few thousand characters group
// many of them into one call while staying well inside a small model's context.
const organizeLLMMaxCharsPerBatch = 6000

// organizeLLMPayload is the clean, distilled shape asked of the model: short
// named entities with a type, and only relationships actually stated in the
// text. Deliberately simpler than the generic node/edge extraction schema —
// organize writes through the GraphRAG entity path (entity:<name> ids), so it
// wants proper-noun names, not documents with synthetic ids.
type organizeLLMPayload struct {
	Entities []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"entities"`
	Relations []struct {
		From string `json:"from"`
		To   string `json:"to"`
		Type string `json:"type"`
	} `json:"relations"`
}

const organizeSystemPrompt = "You distill a clean knowledge graph from personal memory notes. " +
	"Extract only concrete, named entities (specific people, projects, tools, technologies, organizations, places, or concepts) " +
	"and the relationships explicitly stated between them in the text. " +
	"Do NOT invent relationships that are not stated. " +
	"Do NOT emit generic section headings, verbs, adjectives, or sentence fragments as entities. " +
	"Entity names must be the proper noun itself (short), never a phrase or sentence. " +
	"An entity type is exactly one of: person, project, tool, technology, organization, place, concept. " +
	"A relation type is a short snake_case verb phrase, e.g. uses, depends_on, works_on, created_by, part_of, integrates_with, related_to. " +
	"Return JSON only, no prose, in this exact shape: " +
	"{\"entities\":[{\"name\":\"\",\"type\":\"\"}],\"relations\":[{\"from\":\"\",\"to\":\"\",\"type\":\"\"}]}"

// organizeWithLLM replaces the deterministic candidate extractor with an LLM
// that distills clean, typed entities and only explicitly-stated relations,
// then writes them through the same GraphRAG upsert path (so the resulting
// entity:<name> nodes and relation edges are exactly what the graph view and
// GraphRAG retrieval read). Per-batch LLM failures are non-fatal: a bad or
// unreachable call skips that batch and organizing continues.
func organizeWithLLM(ctx context.Context, db *cortexdb.DB, texts []string, llm JSONGenerator, report *OrganizeReport) error {
	batches := batchTextsByChars(texts, organizeLLMMaxCharsPerBatch)

	display := make(map[string]string) // lower(name) -> display name
	etype := make(map[string]string)   // lower(name) -> normalized type
	addEntity := func(name, typ string) string {
		name = collapseSpaces(name)
		if name == "" || len([]rune(name)) > 60 {
			return ""
		}
		l := strings.ToLower(name)
		if _, ok := display[l]; !ok {
			display[l] = name
			etype[l] = normalizeEntityType(typ)
		} else if etype[l] == "entity" {
			// Fill in a better type if a later mention supplies one.
			if t := normalizeEntityType(typ); t != "entity" {
				etype[l] = t
			}
		}
		return l
	}

	relSeen := make(map[string]struct{})
	relations := make([]cortexdb.ToolRelationInput, 0)
	for _, batch := range batches {
		raw, err := llm.GenerateJSON(ctx, organizeSystemPrompt, buildOrganizePrompt(batch))
		if err != nil {
			continue
		}
		payload, err := parseOrganizeLLMPayload(raw)
		if err != nil {
			continue
		}
		for _, e := range payload.Entities {
			addEntity(e.Name, e.Type)
		}
		for _, r := range payload.Relations {
			// Relation endpoints become entities too, so the edge never dangles
			// even if the model omitted them from the entities list.
			fl := addEntity(r.From, "")
			tl := addEntity(r.To, "")
			if fl == "" || tl == "" || fl == tl {
				continue
			}
			typ := normalizeRelationType(r.Type)
			key := fl + "\x00" + tl + "\x00" + typ
			if _, ok := relSeen[key]; ok {
				continue
			}
			relSeen[key] = struct{}{}
			relations = append(relations, cortexdb.ToolRelationInput{From: display[fl], To: display[tl], Type: typ})
		}
	}

	report.CandidatesSeen = len(display)
	report.CandidatesKept = len(display)

	// Never re-upsert an existing entity (preserves its richer stored type).
	existing := loadExistingEntityNames(ctx, db.SQL())
	entities := make([]cortexdb.ToolEntityInput, 0, len(display))
	for l, name := range display {
		if _, ok := existing[l]; ok {
			continue
		}
		entities = append(entities, cortexdb.ToolEntityInput{Name: name, Type: etype[l]})
	}

	tools := db.GraphRAGTools()
	if len(entities) > 0 {
		if _, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: entities}); err != nil {
			return fmt.Errorf("graphflow: organize (llm) upsert entities: %w", err)
		}
		report.EntityCount = len(entities)
	}
	if len(relations) > 0 {
		if _, err := tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{Relations: relations}); err != nil {
			return fmt.Errorf("graphflow: organize (llm) upsert relations: %w", err)
		}
		report.RelationCount = len(relations)
	}
	return nil
}

func buildOrganizePrompt(batch string) string {
	return "Memory notes to distill:\n\n" + batch + "\n\nExtract entities and explicitly-stated relations as specified. JSON only."
}

// batchTextsByChars groups consecutive texts until adding the next would exceed
// maxChars, so many short memories share one LLM call. A single oversized text
// is truncated (on a rune boundary) to the budget.
func batchTextsByChars(texts []string, maxChars int) []string {
	batches := make([]string, 0)
	var b strings.Builder
	for _, t := range texts {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if b.Len() > 0 && b.Len()+len(t)+2 > maxChars {
			batches = append(batches, b.String())
			b.Reset()
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(truncateRunes(t, maxChars))
	}
	if b.Len() > 0 {
		batches = append(batches, b.String())
	}
	return batches
}

// thinkTagRE strips <think>…</think> reasoning blocks that reasoning models
// (e.g. qwen3.5) may leak around the JSON.
var thinkTagRE = regexp.MustCompile(`(?s)<think>.*?</think>`)

func parseOrganizeLLMPayload(raw []byte) (*organizeLLMPayload, error) {
	text := thinkTagRE.ReplaceAllString(string(raw), "")
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("graphflow: organize: no json object in model output")
	}
	var p organizeLLMPayload
	if err := json.Unmarshal([]byte(text[start:end+1]), &p); err != nil {
		return nil, fmt.Errorf("graphflow: organize: invalid json: %w", err)
	}
	return &p, nil
}

var allowedEntityTypes = map[string]bool{
	"person": true, "project": true, "tool": true, "technology": true,
	"organization": true, "place": true, "concept": true,
}

var relationTypeSanitizeRE = regexp.MustCompile(`[^a-z0-9_]+`)

// normalizeEntityType constrains the model's type to the documented vocabulary,
// falling back to the generic "entity" (matching the deterministic path).
func normalizeEntityType(t string) string {
	t = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(t)), " ", "_")
	if t == "" || !allowedEntityTypes[t] {
		return "entity"
	}
	return t
}

// normalizeRelationType lowercases to a snake_case identifier, defaulting to
// "related_to" when empty or unusable.
func normalizeRelationType(t string) string {
	t = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(t)), " ", "_")
	t = strings.Trim(relationTypeSanitizeRE.ReplaceAllString(t, "_"), "_")
	if t == "" {
		return "related_to"
	}
	return t
}

// collapseSpaces trims and collapses internal whitespace runs to single spaces.
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncateRunes returns s limited to at most maxChars bytes, cut on a rune
// boundary so multibyte characters are never split.
func truncateRunes(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	r := []rune(s)
	// Trim rune-by-rune until the byte length fits.
	for len(string(r)) > maxChars && len(r) > 0 {
		r = r[:len(r)-1]
	}
	return string(r)
}
