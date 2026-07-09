package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// newOrganizeLLM builds a graphflow.JSONGenerator from CORTEXDB_LLM_* env, or
// returns nil when no LLM base URL is set — in which case graph organization
// stays fully deterministic (the default). Wiring an LLM here is what turns the
// noisy deterministic entity extraction into clean, typed graph distillation.
//
//	CORTEXDB_LLM_BASE_URL  chat endpoint base (enables it). A bare Ollama host
//	                       (e.g. http://localhost:11434) uses the native
//	                       /api/chat API with think:false — fast and well-formed
//	                       JSON for reasoning models like qwen3.5. A /v1 base
//	                       (e.g. http://localhost:11434/v1 or a hosted provider)
//	                       uses OpenAI-compatible /chat/completions.
//	CORTEXDB_LLM_MODEL     model name (default qwen3.5)
//	CORTEXDB_LLM_API_KEY   bearer token for the OpenAI-compatible path (optional;
//	                       Ollama needs none)
func newOrganizeLLM() graphflow.JSONGenerator {
	baseURL := firstEnv("CORTEXDB_LLM_BASE_URL", "CORTEXDB_LLM_URL")
	if baseURL == "" {
		return nil
	}
	return &chatLLM{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  firstEnv("CORTEXDB_LLM_API_KEY"),
		model:   envOr("CORTEXDB_LLM_MODEL", "qwen3.5"),
		// A base without "/v1" is treated as an Ollama native host.
		native: !strings.Contains(baseURL, "/v1"),
		client: &http.Client{Timeout: 5 * time.Minute},
	}
}

// chatLLM implements graphflow.JSONGenerator over either the Ollama native
// /api/chat API or an OpenAI-compatible /chat/completions endpoint, using plain
// net/http (deliberately no SDK dependency).
type chatLLM struct {
	baseURL string
	apiKey  string
	model   string
	native  bool
	client  *http.Client
}

func (c *chatLLM) GenerateJSON(ctx context.Context, systemPrompt, userPrompt string) ([]byte, error) {
	if c.native {
		return c.generateOllama(ctx, systemPrompt, userPrompt)
	}
	return c.generateOpenAI(ctx, systemPrompt, userPrompt)
}

// generateOllama calls Ollama's native chat API. think:false stops reasoning
// models from burning minutes on a reasoning trace; format:"json" forces a
// well-formed object.
func (c *chatLLM) generateOllama(ctx context.Context, systemPrompt, userPrompt string) ([]byte, error) {
	body, err := json.Marshal(map[string]any{
		"model":  c.model,
		"think":  false,
		"stream": false,
		"format": "json",
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama chat returned %s", resp.Status)
	}
	var parsed struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	return []byte(parsed.Message.Content), nil
}

// generateOpenAI calls any OpenAI-compatible /chat/completions endpoint.
func (c *chatLLM) generateOpenAI(ctx context.Context, systemPrompt, userPrompt string) ([]byte, error) {
	body, err := json.Marshal(map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"response_format": map[string]string{"type": "json_object"},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chat endpoint returned %s", resp.Status)
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("chat endpoint returned no choices")
	}
	return []byte(parsed.Choices[0].Message.Content), nil
}
