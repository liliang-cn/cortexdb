package sqvect_test

import (
	"context"
	"fmt"
	"math"
	mrand "math/rand"

	"github.com/liliang-cn/sqvect/v2/pkg/sqvect"
)

// DummyEmbedder is a simple embedder for testing purposes.
// It generates deterministic vectors based on text content.
// In production, replace this with OpenAI, Ollama, or other embedding providers.
type DummyEmbedder struct {
	dim int
}

func NewDummyEmbedder(dim int) *DummyEmbedder {
	return &DummyEmbedder{dim: dim}
}

func (d *DummyEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	// Generate a deterministic but "random-looking" vector based on text
	// This is NOT a real embedding - just for demonstration
	vector := make([]float32, d.dim)

	// Use text bytes to seed the vector generation
	for i := range vector {
		// Simple hash-like generation
		seed := float64(0)
		for j, b := range text {
			seed += float64(b) * float64(j+1) * float64(i+1)
		}
		vector[i] = float32(math.Sin(seed * 0.001))
	}

	// Normalize the vector
	norm := float32(0)
	for _, v := range vector {
		norm += v * v
	}
	norm = float32(math.Sqrt(float64(norm)))
	if norm > 0 {
		for i := range vector {
			vector[i] /= norm
		}
	}

	return vector, nil
}

func (d *DummyEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := d.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		vectors[i] = vec
	}
	return vectors, nil
}

func (d *DummyEmbedder) Dim() int {
	return d.dim
}

// Example_embedder shows how to use the Embedder interface
func Example_embedder() {
	ctx := context.Background()

	// 1. Open database with an embedder
	db, err := sqvect.Open(
		sqvect.DefaultConfig("test.db"),
		sqvect.WithEmbedder(NewDummyEmbedder(128)),
	)
	if err != nil {
		panic(err)
	}
	defer db.Close()

	// 2. Insert text - embedding is generated automatically
	err = db.InsertText(ctx, "doc1", "The quick brown fox jumps over the lazy dog", nil)
	if err != nil {
		panic(err)
	}

	// 3. Search using text - query embedding is generated automatically
	results, err := db.SearchText(ctx, "fox jumps", 5)
	if err != nil {
		panic(err)
	}

	for _, r := range results {
		fmt.Printf("Score: %.3f, Content: %s\n", r.Score, r.Content)
	}
}

// Example_textOnly shows how to use text-only search without an embedder
func Example_textOnly() {
	ctx := context.Background()

	// 1. Open database WITHOUT an embedder
	db, err := sqvect.Open(sqvect.DefaultConfig("test.db"))
	if err != nil {
		panic(err)
	}
	defer db.Close()

	// 2. Insert vectors manually (you need to provide the vectors)
	vector := make([]float32, 128)
	for i := range vector {
		vector[i] = mrand.Float32() // Just for demo - use real embeddings
	}

	quick := db.Quick()
	id, err := quick.Add(ctx, vector, "The quick brown fox jumps over the lazy dog")
	if err != nil {
		panic(err)
	}
	fmt.Println("Inserted:", id)

	// 3. Search using FTS5 text search (no embedding needed!)
	results, err := db.SearchTextOnly(ctx, "fox OR dog", sqvect.TextSearchOptions{
		TopK: 5,
	})
	if err != nil {
		panic(err)
	}

	for _, r := range results {
		fmt.Printf("Score: %.3f, Content: %s\n", r.Score, r.Content)
	}
}

// OpenAIEmbedder shows how to implement an OpenAI embedder
// This is a template - you need to add actual OpenAI API calls
type OpenAIEmbedder struct {
	apiKey string
	model  string
	dim    int
}

func NewOpenAIEmbedder(apiKey, model string, dim int) *OpenAIEmbedder {
	return &OpenAIEmbedder{apiKey: apiKey, model: model, dim: dim}
}

func (o *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	// TODO: Call OpenAI API
	// Example: POST https://api.openai.com/v1/embeddings
	// Body: {"input": text, "model": o.model}
	//
	// import (
	//     "bytes"
	//     "encoding/json"
	//     "net/http"
	// )
	//
	// body, _ := json.Marshal(map[string]string{"input": text, "model": o.model})
	// req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/embeddings", bytes.NewReader(body))
	// req.Header.Set("Authorization", "Bearer "+o.apiKey)
	// req.Header.Set("Content-Type", "application/json")
	// resp, err := http.DefaultClient.Do(req)
	// ... parse response

	// Placeholder implementation
	vector := make([]float32, o.dim)
	for i := range vector {
		vector[i] = mrand.Float32()
	}
	return vector, nil
}

func (o *OpenAIEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	// TODO: Call OpenAI batch embedding API for better performance
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := o.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		vectors[i] = vec
	}
	return vectors, nil
}

func (o *OpenAIEmbedder) Dim() int {
	return o.dim
}
