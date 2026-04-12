package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"
)

type openAIJSONGenerator struct {
	client openai.Client
	model  string
}

func (g *openAIJSONGenerator) GenerateJSON(ctx context.Context, systemPrompt string, userPrompt string) ([]byte, error) {
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
					Description: openai.String("GraphFlow extraction payload containing nodes and edges."),
					Strict:      openai.Bool(true),
					Schema:      graphflowExtractionSchema(),
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("chat completion failed: %w", err)
	}
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("chat completion returned no choices")
	}

	content := strings.TrimSpace(completion.Choices[0].Message.Content)
	content = stripJSONFence(content)
	if !json.Valid([]byte(content)) {
		return nil, fmt.Errorf("model returned invalid JSON")
	}
	return []byte(content), nil
}

func graphflowExtractionSchema() map[string]any {
	stringSchema := map[string]any{"type": "string"}
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
						"id":              stringSchema,
						"label":           stringSchema,
						"type":            stringSchema,
						"summary":         stringSchema,
						"source_file":     stringSchema,
						"source_location": stringSchema,
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
						"source":          stringSchema,
						"target":          stringSchema,
						"relation":        stringSchema,
						"confidence":      map[string]any{"type": "string", "enum": []string{"EXTRACTED", "INFERRED", "AMBIGUOUS"}},
						"directed":        map[string]any{"type": "boolean"},
						"source_file":     stringSchema,
						"source_location": stringSchema,
					},
				},
			},
		},
	}
}

func stripJSONFence(content string) string {
	if strings.Contains(content, "```json") {
		start := strings.Index(content, "```json") + len("```json")
		end := strings.Index(content[start:], "```")
		if end >= 0 {
			return strings.TrimSpace(content[start : start+end])
		}
	}
	if strings.Contains(content, "```") {
		start := strings.Index(content, "```") + len("```")
		end := strings.Index(content[start:], "```")
		if end >= 0 {
			return strings.TrimSpace(content[start : start+end])
		}
	}
	return strings.Trim(content, "` \n\t")
}
