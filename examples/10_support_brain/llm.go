package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// askLLM sends a system+user prompt to an OpenAI-compatible chat endpoint and
// returns the assistant's reply. The caller passes ONLY desensitized context, so
// the model never sees raw PII.
func askLLM(ctx context.Context, baseURL, apiKey, model, system, user string) (string, error) {
	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
	)
	comp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(system),
			openai.UserMessage(user),
		},
	})
	if err != nil {
		return "", err
	}
	if len(comp.Choices) == 0 {
		return "", fmt.Errorf("model returned no choices")
	}
	return strings.TrimSpace(comp.Choices[0].Message.Content), nil
}
