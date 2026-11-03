package main

import (
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// newOpenAIEmbedder is CortexDB's OpenAI-compatible embedder with this server's one-minute
// request timeout.
func newOpenAIEmbedder(baseURL, apiKey, model string, dim int) *cortexdb.OpenAIEmbedder {
	return cortexdb.NewOpenAIEmbedder(cortexdb.OpenAIEmbedderConfig{BaseURL: baseURL, APIKey: apiKey, Model: model, Dim: dim, Timeout: time.Minute})
}
