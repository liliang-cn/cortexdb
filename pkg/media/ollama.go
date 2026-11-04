package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// describePrompt asks for what search needs from an image: what it shows,
// and every word that can be read in it.
const describePrompt = "Describe this image so that it can be found again by a text search. " +
	"Reply with JSON: \"caption\" is one to three sentences on what the image shows (objects, people, place, " +
	"kind of image: photo, screenshot, diagram, chart, whiteboard); \"text\" is every word legible in the image, " +
	"verbatim, in reading order, or an empty string. Use the language the text in the image is written in."

// OllamaDescriber captions images with a vision model served by Ollama.
type OllamaDescriber struct {
	baseURL string
	model   string
	client  *http.Client
}

// NewOllamaDescriber describes images with model (qwen3.5, llava, gemma3, …)
// at baseURL (http://localhost:11434).
func NewOllamaDescriber(baseURL, model string) *OllamaDescriber {
	return &OllamaDescriber{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		client:  &http.Client{Timeout: 5 * time.Minute},
	}
}

// Name is ollama/<model>.
func (d *OllamaDescriber) Name() string { return "ollama/" + d.model }

// DescribeImage asks the model for a caption and the visible text.
func (d *OllamaDescriber) DescribeImage(ctx context.Context, image cortexdb.MediaInput) (*cortexdb.MediaDescription, error) {
	body, _ := json.Marshal(map[string]any{
		"model":  d.model,
		"stream": false,
		// Thinking models spend minutes and the answer's budget thinking
		// about a caption.
		"think": false,
		"format": map[string]any{
			"type":       "object",
			"properties": map[string]any{"caption": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}},
			"required":   []string{"caption", "text"},
		},
		"options": map[string]any{"temperature": 0},
		"messages": []map[string]any{{
			"role":    "user",
			"content": describePrompt,
			"images":  []string{base64.StdEncoding.EncodeToString(image.Data)},
		}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode ollama reply: %w", err)
	}
	return parseDescription(out.Message.Content), nil
}

// parseDescription reads the model's JSON; an answer that is not JSON is
// taken whole as the caption rather than lost.
func parseDescription(content string) *cortexdb.MediaDescription {
	content = strings.TrimSpace(content)
	var d cortexdb.MediaDescription
	if start, end := strings.Index(content, "{"), strings.LastIndex(content, "}"); start >= 0 && end > start {
		if json.Unmarshal([]byte(content[start:end+1]), &d) == nil && strings.TrimSpace(d.Caption+d.Text) != "" {
			return &d
		}
	}
	return &cortexdb.MediaDescription{Caption: content}
}
