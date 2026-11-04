package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// OpenAITranscriber transcribes through an OpenAI-compatible
// /audio/transcriptions endpoint, asking for verbose_json to get timed
// segments. whisper.cpp serves it with
// `whisper-server -m <model> --inference-path /v1/audio/transcriptions --convert`
// (--convert has it read any format, video included, through ffmpeg).
type OpenAITranscriber struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewOpenAITranscriber sends files to baseURL (http://localhost:43180/v1,
// https://api.openai.com/v1) for model (whisper-1); apiKey is optional.
func NewOpenAITranscriber(baseURL, model, apiKey string) *OpenAITranscriber {
	return &OpenAITranscriber{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 30 * time.Minute},
	}
}

// Name is the model's name.
func (t *OpenAITranscriber) Name() string { return t.model }

// Transcribe uploads the file and returns its segments.
func (t *OpenAITranscriber) Transcribe(ctx context.Context, in cortexdb.MediaInput) ([]cortexdb.MediaSegment, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "media"+extensionOf(in.MediaType))
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(in.Data); err != nil {
		return nil, err
	}
	_ = form.WriteField("model", t.model)
	_ = form.WriteField("response_format", "verbose_json")
	_ = form.WriteField("timestamp_granularities[]", "segment")
	if err := form.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/audio/transcriptions", &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	if t.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.apiKey)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("transcription %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	return parseVerboseJSON(raw)
}

// parseVerboseJSON reads OpenAI's verbose_json: segments with start and end
// in seconds. A reply with text and no segments is one untimed segment.
func parseVerboseJSON(raw []byte) ([]cortexdb.MediaSegment, error) {
	var out struct {
		Text     string `json:"text"`
		Segments []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Text  string  `json:"text"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode transcription: %w", err)
	}
	segments := make([]cortexdb.MediaSegment, 0, len(out.Segments))
	for _, s := range out.Segments {
		if text := strings.TrimSpace(s.Text); text != "" {
			segments = append(segments, cortexdb.MediaSegment{
				StartMS: int64(s.Start * 1000), EndMS: int64(s.End * 1000), Text: text,
			})
		}
	}
	if len(segments) == 0 && strings.TrimSpace(out.Text) != "" {
		segments = append(segments, cortexdb.MediaSegment{Text: strings.TrimSpace(out.Text)})
	}
	return segments, nil
}

// extensionOf names the upload by its type: some servers pick a decoder by
// the file name.
func extensionOf(mediaType string) string {
	if exts, _ := mime.ExtensionsByType(mediaType); len(exts) > 0 {
		return exts[0]
	}
	if _, sub, ok := strings.Cut(mediaType, "/"); ok && sub != "" {
		return "." + strings.TrimPrefix(sub, "x-")
	}
	return ""
}
