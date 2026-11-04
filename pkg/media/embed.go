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

// OpenAIMediaEmbedder embeds images, sound and text into one space through
// an OpenAI-compatible /embeddings endpoint that takes a modality, the way
// infinity (github.com/michaelfeil/infinity) serves CLIP, SigLIP and CLAP
// models: {"model", "input": [...], "modality": "text" | "image" | "audio"},
// a file sent as a data: URI. Video is left to the brain, which embeds its
// frames.
type OpenAIMediaEmbedder struct {
	baseURL string
	apiKey  string
	model   string
	audio   bool
	client  *http.Client
}

// NewOpenAIMediaEmbedder embeds through baseURL (http://localhost:43181)
// with model (openai/clip-vit-base-patch32); apiKey is optional. audio says
// the model embeds sound too (CLAP); otherwise a recording is not embedded.
func NewOpenAIMediaEmbedder(baseURL, model, apiKey string, audio bool) *OpenAIMediaEmbedder {
	return &OpenAIMediaEmbedder{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		audio:   audio,
		client:  &http.Client{Timeout: 5 * time.Minute},
	}
}

// Name is the model's name.
func (e *OpenAIMediaEmbedder) Name() string { return e.model }

// EmbedMedia embeds an image, or a recording when the model takes sound.
func (e *OpenAIMediaEmbedder) EmbedMedia(ctx context.Context, in cortexdb.MediaInput) ([]cortexdb.MediaVector, error) {
	major, _, _ := strings.Cut(strings.ToLower(in.MediaType), "/")
	switch {
	case major == "image":
	case major == "audio" && e.audio:
	default:
		return nil, cortexdb.ErrUnsupportedMedia
	}
	uri := "data:" + strings.ToLower(in.MediaType) + ";base64," + base64.StdEncoding.EncodeToString(in.Data)
	vec, err := e.embed(ctx, major, uri)
	if err != nil {
		return nil, err
	}
	return []cortexdb.MediaVector{{Vector: vec}}, nil
}

// EmbedText embeds a query into the same space.
func (e *OpenAIMediaEmbedder) EmbedText(ctx context.Context, text string) ([]float32, error) {
	return e.embed(ctx, "text", text)
}

func (e *OpenAIMediaEmbedder) embed(ctx context.Context, modality, input string) ([]float32, error) {
	body, err := json.Marshal(map[string]any{"model": e.model, "input": []string{input}, "modality": modality})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("media embed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("media embed: read the answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("media embed %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("media embed: decode the answer: %w", err)
	}
	if len(out.Data) != 1 || len(out.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("media embed: the answer holds %d embeddings, want 1", len(out.Data))
	}
	return out.Data[0].Embedding, nil
}
