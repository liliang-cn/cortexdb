package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
	"github.com/liliang-cn/sqvect/v2/pkg/index"
)

// RAGExample demonstrates a complete RAG system using sqvect
type RAGExample struct {
	vectorStore *core.SQLiteStore
	lshIndex    *index.LSHIndex
	config      RAGConfig
}

type RAGConfig struct {
	DBPath        string
	VectorDim     int
	ChunkSize     int
	ChunkOverlap  int
	TopK          int
	UseStreaming  bool
	UseLSH        bool
}

// Document represents a document to be indexed
type Document struct {
	ID       string
	Title    string
	Content  string
	Metadata map[string]string
}

// Initialize RAG system
func NewRAGExample(config RAGConfig) (*RAGExample, error) {
	// Set defaults
	if config.VectorDim == 0 {
		config.VectorDim = 1536 // OpenAI default
	}
	if config.ChunkSize == 0 {
		config.ChunkSize = 500
	}
	if config.TopK == 0 {
		config.TopK = 5
	}

	// Initialize vector store
	storeConfig := core.DefaultConfig()
	storeConfig.Path = config.DBPath
	storeConfig.VectorDim = config.VectorDim

	store, err := core.NewWithConfig(storeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		return nil, fmt.Errorf("failed to init store: %w", err)
	}

	rag := &RAGExample{
		vectorStore: store,
		config:      config,
	}

	// Initialize LSH index if requested
	if config.UseLSH {
		lshConfig := index.LSHConfig{
			NumTables:    10,
			NumHashFuncs: 8,
			Dimension:    config.VectorDim,
			Seed:         42,
		}
		rag.lshIndex = index.NewLSHIndex(lshConfig)
	}

	return rag, nil
}

// IndexDocument processes and indexes a document
func (r *RAGExample) IndexDocument(doc Document) error {
	log.Printf("Indexing document: %s", doc.Title)

	// Split into chunks
	chunks := r.createChunks(doc.Content)
	log.Printf("Created %d chunks", len(chunks))

	ctx := context.Background()
	
	for i, chunk := range chunks {
		// In production, generate real embedding here
		// For demo, create a mock embedding
		embedding := r.mockEmbedding(chunk)

		// Create metadata
		metadata := map[string]string{
			"doc_id":     doc.ID,
			"doc_title":  doc.Title,
			"chunk_index": fmt.Sprintf("%d", i),
			"chunk_text": chunk,
		}

		// Merge additional metadata
		for k, v := range doc.Metadata {
			metadata[k] = v
		}

		// Store in vector database
		embeddingID := fmt.Sprintf("%s_chunk_%d", doc.ID, i)
		err := r.vectorStore.Upsert(ctx, &core.Embedding{
			ID:       embeddingID,
			Vector:   embedding,
			Content:  chunk,
			DocID:    doc.ID,
			Metadata: metadata,
		})

		if err != nil {
			return fmt.Errorf("failed to upsert chunk %d: %w", i, err)
		}

		// Also add to LSH index if enabled
		if r.lshIndex != nil {
			_ = r.lshIndex.Insert(embeddingID, embedding)
		}
	}

	log.Printf("Successfully indexed document %s with %d chunks", doc.ID, len(chunks))
	return nil
}

// Query performs RAG search
func (r *RAGExample) Query(question string) (*RAGResponse, error) {
	log.Printf("Processing query: %s", question)

	// Generate query embedding
	queryEmbedding := r.mockEmbedding(question)
	ctx := context.Background()

	var results []core.ScoredEmbedding
	var searchMethod string

	if r.config.UseStreaming {
		// Use streaming search for real-time results
		searchMethod = "streaming"
		results = r.streamingSearch(ctx, queryEmbedding)
	} else if r.lshIndex != nil {
		// Use LSH for fast approximate search
		searchMethod = "LSH"
		results = r.lshSearch(ctx, queryEmbedding)
	} else {
		// Regular search
		searchMethod = "exact"
		var err error
		results, err = r.vectorStore.Search(ctx, queryEmbedding, core.SearchOptions{
			TopK: r.config.TopK,
		})
		if err != nil {
			return nil, fmt.Errorf("search failed: %w", err)
		}
	}

	// Build context from results
	var contexts []string
	var sources []Source
	
	for _, result := range results {
		contexts = append(contexts, result.Content)
		sources = append(sources, Source{
			DocID:    result.Metadata["doc_id"],
			DocTitle: result.Metadata["doc_title"],
			Chunk:    result.Content,
			Score:    result.Score,
		})
	}

	// Generate response (mock LLM call)
	answer := r.generateAnswer(question, contexts)

	return &RAGResponse{
		Question:     question,
		Answer:       answer,
		Sources:      sources,
		SearchMethod: searchMethod,
		NumResults:   len(results),
	}, nil
}

// Advanced search with filters
func (r *RAGExample) QueryWithFilter(question string, filterExpr string) (*RAGResponse, error) {
	// Parse filter expression
	filter, err := core.ParseFilterString(filterExpr)
	if err != nil {
		return nil, fmt.Errorf("invalid filter: %w", err)
	}

	queryEmbedding := r.mockEmbedding(question)
	ctx := context.Background()

	// Search with advanced filtering
	results, err := r.vectorStore.SearchWithAdvancedFilter(ctx, queryEmbedding,
		core.AdvancedSearchOptions{
			SearchOptions: core.SearchOptions{
				TopK: r.config.TopK,
			},
			PreFilter: filter,
		})

	if err != nil {
		return nil, err
	}

	// Process results...
	var contexts []string
	var sources []Source
	
	for _, result := range results {
		contexts = append(contexts, result.Content)
		sources = append(sources, Source{
			DocID:    result.Metadata["doc_id"],
			DocTitle: result.Metadata["doc_title"],
			Chunk:    result.Content,
			Score:    result.Score,
		})
	}

	answer := r.generateAnswer(question, contexts)

	return &RAGResponse{
		Question:   question,
		Answer:     answer,
		Sources:    sources,
		Filter:     filterExpr,
		NumResults: len(results),
	}, nil
}

// Streaming search implementation
func (r *RAGExample) streamingSearch(ctx context.Context, queryEmbedding []float32) []core.ScoredEmbedding {
	opts := core.StreamingOptions{
		SearchOptions: core.SearchOptions{
			TopK: r.config.TopK,
		},
		BatchSize:      10,
		EarlyTerminate: true,
		QualityThreshold: 0.8,
		ProgressCallback: func(processed, total int) {
			log.Printf("Search progress: %d/%d", processed, total)
		},
	}

	stream, err := r.vectorStore.StreamSearch(ctx, queryEmbedding, opts)
	if err != nil {
		log.Printf("Streaming search failed: %v", err)
		return nil
	}

	// Collect top results
	results, _ := core.CollectTopKFromStream(ctx, stream, r.config.TopK)
	return results
}

// LSH search implementation
func (r *RAGExample) lshSearch(ctx context.Context, queryEmbedding []float32) []core.ScoredEmbedding {
	// Fast approximate search with LSH
	lshResults, err := r.lshIndex.SearchWithMultiProbe(queryEmbedding, r.config.TopK*2, 3)
	if err != nil {
		log.Printf("LSH search failed: %v", err)
		return nil
	}

	// Get full embeddings for top results
	var ids []string
	for _, result := range lshResults {
		ids = append(ids, result.ID)
		if len(ids) >= r.config.TopK {
			break
		}
	}

	// Fetch full embeddings with metadata
	var results []core.ScoredEmbedding
	for _, id := range ids {
		emb, err := r.vectorStore.GetByID(ctx, id)
		if err == nil && emb != nil {
			// Calculate exact score
			score := r.calculateSimilarity(queryEmbedding, emb.Vector)
			results = append(results, core.ScoredEmbedding{
				Embedding: *emb,
				Score:     score,
			})
		}
	}

	return results
}

// Helper functions

func (r *RAGExample) createChunks(content string) []string {
	words := strings.Fields(content)
	var chunks []string
	
	for i := 0; i < len(words); i += r.config.ChunkSize {
		end := i + r.config.ChunkSize
		if end > len(words) {
			end = len(words)
		}
		
		chunk := strings.Join(words[i:end], " ")
		chunks = append(chunks, chunk)
		
		// Add overlap for next chunk
		if r.config.ChunkOverlap > 0 && end < len(words) {
			i -= r.config.ChunkOverlap
		}
	}
	
	return chunks
}

func (r *RAGExample) mockEmbedding(text string) []float32 {
	// In production, call OpenAI/Claude/etc API
	// For demo, create a deterministic mock embedding
	embedding := make([]float32, r.config.VectorDim)
	
	// Simple hash-based mock embedding
	hash := 0
	for _, char := range text {
		hash = (hash*31 + int(char)) % 1000000
	}
	
	for i := range embedding {
		embedding[i] = float32((hash+i)%1000) / 1000.0
	}
	
	return embedding
}

func (r *RAGExample) generateAnswer(question string, contexts []string) string {
	// In production, call LLM API
	// For demo, create a simple response
	if len(contexts) == 0 {
		return "I couldn't find relevant information to answer your question."
	}
	
	context := strings.Join(contexts, "\n\n")
	
	// Mock LLM response
	return fmt.Sprintf(
		"Based on the available information:\n\n%s\n\n[This is a mock response. In production, this would be generated by an LLM using the context above.]",
		truncateString(context, 500),
	)
}

func (r *RAGExample) calculateSimilarity(a, b []float32) float64 {
	// Cosine similarity
	var dotProduct, normA, normB float64
	
	for i := range a {
		dotProduct += float64(a[i] * b[i])
		normA += float64(a[i] * a[i])
		normB += float64(b[i] * b[i])
	}
	
	if normA == 0 || normB == 0 {
		return 0
	}
	
	return dotProduct / (normA * normB)
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// Response types

type RAGResponse struct {
	Question     string   `json:"question"`
	Answer       string   `json:"answer"`
	Sources      []Source `json:"sources"`
	Filter       string   `json:"filter,omitempty"`
	SearchMethod string   `json:"search_method,omitempty"`
	NumResults   int      `json:"num_results"`
}

type Source struct {
	DocID    string  `json:"doc_id"`
	DocTitle string  `json:"doc_title"`
	Chunk    string  `json:"chunk"`
	Score    float64 `json:"score"`
}

// Main function demonstrates usage
func main() {
	// Initialize RAG system
	rag, err := NewRAGExample(RAGConfig{
		DBPath:       "/tmp/rag_demo.db",
		VectorDim:    384,  // Smaller for demo
		ChunkSize:    100,
		ChunkOverlap: 20,
		TopK:         3,
		UseStreaming: true,
		UseLSH:       true,
	})
	if err != nil {
		log.Fatal(err)
	}

	// Index sample documents
	documents := []Document{
		{
			ID:    "doc1",
			Title: "Introduction to RAG",
			Content: `Retrieval Augmented Generation (RAG) is a technique that combines 
			the power of large language models with external knowledge retrieval. 
			RAG systems first retrieve relevant documents or passages from a knowledge base, 
			then use these retrieved contexts to generate more accurate and grounded responses. 
			This approach helps reduce hallucinations and provides traceable sources for answers.`,
			Metadata: map[string]string{
				"category": "AI",
				"date":     "2024-01-15",
			},
		},
		{
			ID:    "doc2",
			Title: "Vector Databases Explained",
			Content: `Vector databases are specialized databases designed to store and search 
			high-dimensional vector embeddings. They use similarity metrics like cosine similarity 
			or Euclidean distance to find the most similar vectors to a query. Popular vector 
			databases include Pinecone, Weaviate, and Qdrant. sqvect is a lightweight, pure Go 
			implementation that can be embedded directly in applications.`,
			Metadata: map[string]string{
				"category": "Database",
				"date":     "2024-02-20",
			},
		},
		{
			ID:    "doc3",
			Title: "Building AI Applications",
			Content: `Modern AI applications often combine multiple components: LLMs for generation, 
			vector databases for retrieval, and traditional databases for structured data. 
			The key to successful AI applications is choosing the right architecture and tools. 
			Consider factors like latency, accuracy, cost, and scalability when designing your system.`,
			Metadata: map[string]string{
				"category": "AI",
				"date":     "2024-03-10",
			},
		},
	}

	// Index documents
	for _, doc := range documents {
		if err := rag.IndexDocument(doc); err != nil {
			log.Printf("Failed to index document %s: %v", doc.ID, err)
		}
	}

	// Example queries
	queries := []string{
		"What is RAG and how does it work?",
		"Tell me about vector databases",
		"How do I build AI applications?",
	}

	fmt.Println("\n=== Regular Queries ===")
	for _, query := range queries {
		response, err := rag.Query(query)
		if err != nil {
			log.Printf("Query failed: %v", err)
			continue
		}
		
		fmt.Printf("\nQ: %s\n", response.Question)
		fmt.Printf("A: %s\n", response.Answer)
		fmt.Printf("Sources: %d documents (search method: %s)\n", 
			response.NumResults, response.SearchMethod)
	}

	// Query with filter
	fmt.Println("\n=== Filtered Query ===")
	filteredQuery := "What should I know about AI?"
	filter := "category:AI AND date>'2024-02-01'"
	
	response, err := rag.QueryWithFilter(filteredQuery, filter)
	if err != nil {
		log.Printf("Filtered query failed: %v", err)
	} else {
		fmt.Printf("\nQ: %s\n", response.Question)
		fmt.Printf("Filter: %s\n", response.Filter)
		fmt.Printf("A: %s\n", response.Answer)
		fmt.Printf("Sources: %d documents\n", response.NumResults)
	}

	// Show statistics
	fmt.Println("\n=== System Statistics ===")
	stats, _ := json.MarshalIndent(map[string]interface{}{
		"total_chunks":    15, // approximate
		"vector_dim":      rag.config.VectorDim,
		"chunk_size":      rag.config.ChunkSize,
		"using_lsh":       rag.config.UseLSH,
		"using_streaming": rag.config.UseStreaming,
	}, "", "  ")
	fmt.Println(string(stats))

	// Cleanup
	_ = os.Remove("/tmp/rag_demo.db")
}