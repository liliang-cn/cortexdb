package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// OpenAIClient implements graphflow.JSONGenerator using the official OpenAI Go SDK.
type OpenAIClient struct {
	client openai.Client
	model  string
}

// newOpenAIClient creates a new OpenAI client.
func newOpenAIClient(baseURL, apiKey, model string) *OpenAIClient {
	opts := []option.RequestOption{}

	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}

	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}

	client := openai.NewClient(opts...)

	return &OpenAIClient{
		client: client,
		model:  model,
	}
}

// GenerateJSON implements graphflow.JSONGenerator.
func (c *OpenAIClient) GenerateJSON(ctx context.Context, systemPrompt, userPrompt string) ([]byte, error) {
	completion, err := c.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: c.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemPrompt),
			openai.UserMessage(userPrompt),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("chat completion failed: %w", err)
	}

	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("no completion choices returned")
	}

	content := completion.Choices[0].Message.Content

	// Try to extract JSON from markdown code blocks if present
	if strings.Contains(content, "```json") {
		start := strings.Index(content, "```json") + 7
		end := strings.Index(content[start:], "```")
		if end > 0 {
			content = strings.TrimSpace(content[start : start+end])
		}
	} else if strings.Contains(content, "```") {
		start := strings.Index(content, "```") + 3
		end := strings.Index(content[start:], "```")
		if end > 0 {
			content = strings.TrimSpace(content[start : start+end])
		}
	}

	// Trim any remaining markdown formatting
	content = strings.Trim(content, "`")

	return []byte(content), nil
}
