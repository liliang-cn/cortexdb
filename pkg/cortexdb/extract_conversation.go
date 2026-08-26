package cortexdb

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
)

// ToolExtractConversationRequest asks to extract key information from a chunk of
// conversation text (or a stored session's messages).
type ToolExtractConversationRequest struct {
	// Text is the conversation content to analyze. If empty, SessionID is used
	// to load the session's messages.
	Text string `json:"text,omitempty"`
	// SessionID loads and concatenates that session's messages when Text is empty.
	SessionID string `json:"session_id,omitempty"`
	// Persist writes the extracted entities/relations into the knowledge graph
	// and the summary into durable knowledge.
	Persist bool `json:"persist,omitempty"`
	// Collection for the persisted summary (default "conversations").
	Collection string `json:"collection,omitempty"`
	// MaxEntities caps extracted entities (default 30).
	MaxEntities int `json:"max_entities,omitempty"`
}

// ExtractedRelation is a subject-predicate-object relation extracted from text.
type ExtractedRelation struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

// ToolExtractConversationResponse is the extracted key information.
type ToolExtractConversationResponse struct {
	Summary     string              `json:"summary"`
	Themes      []string            `json:"themes,omitempty"`
	Entities    []string            `json:"entities,omitempty"`
	Relations   []ExtractedRelation `json:"relations,omitempty"`
	Persisted   bool                `json:"persisted"`
	KnowledgeID string              `json:"knowledge_id,omitempty"`
}

// ExtractConversation pulls key information — a summary, themes, entities, and
// co-occurrence relations — out of conversation text, deterministically (no LLM
// or embedder). Optionally it persists the result: entities/relations into the
// knowledge graph and the summary into durable knowledge, so a conversation
// becomes recallable and graph-queryable in one call.
//
// Extraction is heuristic (proper-noun/identifier entities, sentence
// co-occurrence, keyword themes, lead-sentence summary); for typed relations
// and abstractive summaries, run an LLM over the same text and persist via
// knowledge_save / upsert_relations instead.
func (t *GraphRAGToolbox) ExtractConversation(ctx context.Context, req ToolExtractConversationRequest) (*ToolExtractConversationResponse, error) {
	text := strings.TrimSpace(req.Text)
	if text == "" && strings.TrimSpace(req.SessionID) != "" {
		loaded, err := t.loadSessionText(ctx, req.SessionID)
		if err != nil {
			return nil, err
		}
		text = loaded
	}
	if text == "" {
		return nil, fmt.Errorf("extract_conversation: text or session_id is required")
	}

	maxEntities := req.MaxEntities
	if maxEntities <= 0 {
		maxEntities = 30
	}

	// Entities: proper-noun/identifier candidates, minus common stopwords.
	seen := make(map[string]struct{})
	entities := make([]string, 0, maxEntities)
	for _, e := range extractCorpusEntities(text) {
		name := strings.TrimSpace(e.Name)
		low := strings.ToLower(name)
		if len([]rune(name)) < 2 {
			continue
		}
		if _, stop := lexicalQueryStopwords[low]; stop {
			continue
		}
		if _, ok := seen[low]; ok {
			continue
		}
		seen[low] = struct{}{}
		entities = append(entities, name)
		if len(entities) >= maxEntities {
			break
		}
	}

	// Relations: entities co-occurring within a sentence.
	relSeen := make(map[string]struct{})
	relations := make([]ExtractedRelation, 0)
	for _, sent := range splitChunkSentences(text) {
		inSent := make([]string, 0)
		for _, e := range extractTitleEntities(sent) {
			if _, ok := seen[strings.ToLower(strings.TrimSpace(e.Name))]; ok {
				inSent = append(inSent, strings.TrimSpace(e.Name))
			}
		}
		for i := 0; i+1 < len(inSent); i++ {
			a, b := inSent[i], inSent[i+1]
			if strings.EqualFold(a, b) {
				continue
			}
			key := strings.ToLower(a) + "\x00" + strings.ToLower(b)
			if _, ok := relSeen[key]; ok {
				continue
			}
			relSeen[key] = struct{}{}
			relations = append(relations, ExtractedRelation{From: a, To: b, Type: "co_occurs"})
		}
	}

	// Summary: the first couple of sentences. Themes: top keywords.
	summary := clipString(strings.TrimSpace(leadSentences(text, 2)), 400)
	themes := lexicalQueryKeywords(text)
	if len(themes) > 8 {
		themes = themes[:8]
	}

	resp := &ToolExtractConversationResponse{
		Summary:   summary,
		Themes:    themes,
		Entities:  entities,
		Relations: relations,
	}

	if req.Persist {
		if len(entities) > 0 {
			entInputs := make([]ToolEntityInput, 0, len(entities))
			for _, n := range entities {
				entInputs = append(entInputs, ToolEntityInput{Name: n, Type: "entity"})
			}
			if _, err := t.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: entInputs}); err != nil {
				return nil, fmt.Errorf("extract_conversation: persist entities: %w", err)
			}
		}
		if len(relations) > 0 {
			relInputs := make([]ToolRelationInput, 0, len(relations))
			for _, r := range relations {
				relInputs = append(relInputs, ToolRelationInput{From: r.From, To: r.To, Type: r.Type})
			}
			if _, err := t.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: relInputs}); err != nil {
				return nil, fmt.Errorf("extract_conversation: persist relations: %w", err)
			}
		}
		if summary != "" {
			collection := req.Collection
			if collection == "" {
				collection = "conversations"
			}
			kid := "conversation:" + firstNonEmpty(strings.TrimSpace(req.SessionID), shortTextHash(text))
			if _, err := t.db.SaveKnowledge(ctx, KnowledgeSaveRequest{
				KnowledgeID: kid,
				Title:       "Conversation summary",
				Content:     summary,
				Collection:  collection,
				Metadata:    map[string]string{"source": "conversation", "session_id": req.SessionID},
			}); err != nil {
				return nil, fmt.Errorf("extract_conversation: persist summary: %w", err)
			}
			resp.KnowledgeID = kid
		}
		resp.Persisted = true
	}
	return resp, nil
}

// loadSessionText concatenates a session's messages as "role: content" lines.
func (t *GraphRAGToolbox) loadSessionText(ctx context.Context, sessionID string) (string, error) {
	rows, err := t.db.SQL().QueryContext(ctx,
		`SELECT COALESCE(role,''), COALESCE(content,'') FROM messages WHERE session_id = ? ORDER BY created_at`, sessionID)
	if err != nil {
		return "", fmt.Errorf("extract_conversation: load session: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var b strings.Builder
	for rows.Next() {
		var role, content string
		if err := rows.Scan(&role, &content); err != nil {
			return "", err
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		if role != "" {
			b.WriteString(role)
			b.WriteString(": ")
		}
		b.WriteString(content)
		b.WriteString("\n")
	}
	return b.String(), rows.Err()
}

// leadSentences returns the first n sentences of text joined by spaces.
func leadSentences(text string, n int) string {
	sents := splitChunkSentences(text)
	if len(sents) > n {
		sents = sents[:n]
	}
	return strings.Join(sents, " ")
}

// shortTextHash gives a stable short id fragment for text without a session id.
func shortTextHash(text string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(text))
	return fmt.Sprintf("%x", h.Sum64())
}
