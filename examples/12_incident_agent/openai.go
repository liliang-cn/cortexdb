package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

// aiClient wraps an OpenAI-compatible chat model. It serves two roles:
//   - graphflow.JSONGenerator (GenerateJSON) for LLM knowledge extraction, and
//   - a plain chat turn (chat) for the agent's tool-planning loop.
type aiClient struct {
	client openai.Client
	model  string
}

func newAIClient(baseURL, apiKey, model string) *aiClient {
	return &aiClient{
		client: openai.NewClient(option.WithAPIKey(apiKey), option.WithBaseURL(baseURL)),
		model:  model,
	}
}

// GenerateJSON implements graphflow.JSONGenerator: it asks the model to extract a
// nodes/edges knowledge-graph payload, constrained by a strict JSON schema.
func (g *aiClient) GenerateJSON(ctx context.Context, systemPrompt, userPrompt string) ([]byte, error) {
	completion, err := g.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: g.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemPrompt),
			openai.UserMessage(userPrompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:        "graphflow_extraction",
					Description: openai.String("Knowledge-graph nodes and edges extracted from a document."),
					Strict:      openai.Bool(true),
					Schema:      graphflowExtractionSchema(),
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("extraction completion failed: %w", err)
	}
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("extraction returned no choices")
	}
	content := stripJSONFence(strings.TrimSpace(completion.Choices[0].Message.Content))
	if !json.Valid([]byte(content)) {
		return nil, fmt.Errorf("extraction returned invalid JSON")
	}
	return []byte(content), nil
}

// chat runs one agent turn over the running message history and returns the
// assistant's reply text (expected to be a JSON action object).
func (g *aiClient) chat(ctx context.Context, messages []openai.ChatCompletionMessageParamUnion) (string, error) {
	comp, err := g.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:    g.model,
		Messages: messages,
	})
	if err != nil {
		return "", err
	}
	if len(comp.Choices) == 0 {
		return "", fmt.Errorf("agent turn returned no choices")
	}
	return strings.TrimSpace(comp.Choices[0].Message.Content), nil
}

func graphflowExtractionSchema() map[string]any {
	str := map[string]any{"type": "string"}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"nodes", "edges"},
		"properties": map[string]any{
			"nodes": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"id", "label", "type", "summary", "source_file", "source_location"},
					"properties": map[string]any{
						"id": str, "label": str, "type": str, "summary": str,
						"source_file": str, "source_location": str,
					},
				},
			},
			"edges": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"source", "target", "relation", "confidence", "directed", "source_file", "source_location"},
					"properties": map[string]any{
						"source": str, "target": str, "relation": str,
						"confidence":      map[string]any{"type": "string", "enum": []string{"EXTRACTED", "INFERRED", "AMBIGUOUS"}},
						"directed":        map[string]any{"type": "boolean"},
						"source_file":     str,
						"source_location": str,
					},
				},
			},
		},
	}
}

func stripJSONFence(content string) string {
	if i := strings.Index(content, "```json"); i >= 0 {
		rest := content[i+len("```json"):]
		if end := strings.Index(rest, "```"); end >= 0 {
			return strings.TrimSpace(rest[:end])
		}
	}
	if i := strings.Index(content, "```"); i >= 0 {
		rest := content[i+len("```"):]
		if end := strings.Index(rest, "```"); end >= 0 {
			return strings.TrimSpace(rest[:end])
		}
	}
	return strings.Trim(content, "` \n\t")
}
