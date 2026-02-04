package semanticrouter

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
)

// SparseEncoder converts text into sparse vector representations (mostly zeros).
// Sparse vectors are efficient for keyword matching and term frequency analysis.
type SparseEncoder interface {
	// EncodeSparse converts text into a sparse vector representation.
	// Returns a map where keys are term identifiers and values are weights.
	EncodeSparse(text string) map[string]float64

	// EncodeSparseBatch converts multiple texts into sparse vectors.
	EncodeSparseBatch(texts []string) []map[string]float64

	// Vocabulary returns the encoder's vocabulary.
	Vocabulary() []string

	// Dimensions returns the size of the vocabulary (sparse vector dimensionality).
	Dimensions() int
}

// SparseSimilarity computes similarity between two sparse vectors.
// Uses cosine similarity for sparse representations.
func SparseSimilarity(a, b map[string]float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64

	// Compute dot product and norm for a
	for term, weightA := range a {
		dotProduct += weightA * b[term]
		normA += weightA * weightA
	}

	// Compute norm for b
	for _, weightB := range b {
		normB += weightB * weightB
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// tokenize splits text into terms (words).
func tokenize(text string) []string {
	// Convert to lowercase and split on non-alphanumeric characters
	text = strings.ToLower(text)
	words := strings.Fields(text)

	// Simple stop words list
	stopWords := map[string]bool{
		"a": true, "an": true, "the": true, "and": true, "or": true,
		"but": true, "in": true, "on": true, "at": true, "to": true,
		"for": true, "of": true, "with": true, "by": true, "is": true,
		"are": true, "was": true, "were": true, "be": true, "been": true,
		"this": true, "that": true, "these": true, "those": true,
		"我": true, "你": true, "他": true, "她": true, "它": true,
		"的": true, "了": true, "是": true, "在": true, "有": true,
		"和": true, "与": true, "或": true, "但": true, "不": true,
	}

	var terms []string
	for _, word := range words {
		if !stopWords[word] && len(word) > 1 {
			terms = append(terms, word)
		}
	}

	return terms
}

// BM25Encoder implements BM25 (Best Matching 25) sparse encoding.
// BM25 improves upon TF-IDF by incorporating document length normalization.
type BM25Encoder struct {
	// IDF (Inverse Document Frequency) values for each term
	idf     map[string]float64
	idfMu   sync.RWMutex

	// Document frequency for each term
	docFreq map[string]int

	// Total number of documents seen
	totalDocs int

	// BM25 parameters
	k1 float64 // Term frequency saturation parameter (default 1.2)
	b  float64 // Length normalization parameter (default 0.75)

	// Average document length
	avgDocLen float64

	// Vocabulary
	vocabulary []string
	vocabMu    sync.RWMutex
}

// NewBM25Encoder creates a new BM25 encoder with default parameters.
func NewBM25Encoder() *BM25Encoder {
	return &BM25Encoder{
		idf:       make(map[string]float64),
		docFreq:   make(map[string]int),
		totalDocs: 0,
		k1:        1.2,
		b:         0.75,
	}
}

// NewBM25EncoderWithParams creates a new BM25 encoder with custom parameters.
func NewBM25EncoderWithParams(k1, b float64) *BM25Encoder {
	return &BM25Encoder{
		idf:       make(map[string]float64),
		docFreq:   make(map[string]int),
		totalDocs: 0,
		k1:        k1,
		b:         b,
	}
}

// Fit trains the BM25 encoder on a corpus of documents.
// This computes IDF values based on the document frequency of terms.
func (e *BM25Encoder) Fit(ctx context.Context, documents []string) error {
	e.idfMu.Lock()
	defer e.idfMu.Unlock()

	e.totalDocs = len(documents)
	e.docFreq = make(map[string]int)
	termDocCount := make(map[string]map[int]bool) // term -> set of doc indices

	totalLen := 0.0

	for docIdx, doc := range documents {
		terms := tokenize(doc)
		totalLen += float64(len(terms))

		uniqueTerms := make(map[string]bool)
		for _, term := range terms {
			if !uniqueTerms[term] {
				uniqueTerms[term] = true
				if termDocCount[term] == nil {
					termDocCount[term] = make(map[int]bool)
				}
				termDocCount[term][docIdx] = true
			}

			// Add to vocabulary
			e.vocabMu.Lock()
			found := false
			for _, v := range e.vocabulary {
				if v == term {
					found = true
					break
				}
			}
			if !found {
				e.vocabulary = append(e.vocabulary, term)
			}
			e.vocabMu.Unlock()
		}
	}

	// Compute IDF and document frequency
	for term, docs := range termDocCount {
		e.docFreq[term] = len(docs)
		df := float64(len(docs))
		// IDF = log((N - df + 0.5) / (df + 0.5) + 1)
		idf := math.Log((float64(e.totalDocs)-df+0.5)/(df+0.5) + 1)
		e.idf[term] = idf
	}

	e.avgDocLen = totalLen / float64(e.totalDocs)

	return nil
}

// EncodeSparse converts a document into a BM25 sparse vector.
func (e *BM25Encoder) EncodeSparse(text string) map[string]float64 {
	e.idfMu.RLock()
	defer e.idfMu.RUnlock()

	terms := tokenize(text)
	docLen := float64(len(terms))

	if docLen == 0 {
		return make(map[string]float64)
	}

	result := make(map[string]float64)

	// Count term frequencies
	termFreq := make(map[string]int)
	for _, term := range terms {
		termFreq[term]++
	}

	// Compute BM25 score for each term
	for term, tf := range termFreq {
		idf, ok := e.idf[term]
		if !ok {
			// Unknown term - use a default IDF
			idf = 1.0
		}

		// BM25 formula: IDF * (tf * (k1 + 1)) / (tf + k1 * (1 - b + b * (docLen / avgDocLen)))
		numerator := float64(tf) * (e.k1 + 1)
		denominator := float64(tf) + e.k1*(1-e.b+e.b*(docLen/e.avgDocLen))
		bm25 := idf * (numerator / denominator)

		result[term] = bm25
	}

	return result
}

// EncodeSparseBatch converts multiple documents into BM25 sparse vectors.
func (e *BM25Encoder) EncodeSparseBatch(texts []string) []map[string]float64 {
	results := make([]map[string]float64, len(texts))
	for i, text := range texts {
		results[i] = e.EncodeSparse(text)
	}
	return results
}

// Vocabulary returns the encoder's vocabulary.
func (e *BM25Encoder) Vocabulary() []string {
	e.vocabMu.RLock()
	defer e.vocabMu.RUnlock()
	return e.vocabulary
}

// Dimensions returns the size of the vocabulary.
func (e *BM25Encoder) Dimensions() int {
	e.vocabMu.RLock()
	defer e.vocabMu.RUnlock()
	return len(e.vocabulary)
}

// TFIDFEncoder implements TF-IDF (Term Frequency-Inverse Document Frequency) sparse encoding.
type TFIDFEncoder struct {
	// IDF (Inverse Document Frequency) values
	idf     map[string]float64
	idfMu   sync.RWMutex

	// Document frequency for each term
	docFreq map[string]int

	// Total number of documents
	totalDocs int

	// Vocabulary
	vocabulary []string
	vocabMu    sync.RWMutex

	// Whether to use sublinear TF scaling (log(1 + tf))
	sublinearTF bool
}

// NewTFIDFEncoder creates a new TF-IDF encoder.
func NewTFIDFEncoder() *TFIDFEncoder {
	return &TFIDFEncoder{
		idf:         make(map[string]float64),
		docFreq:     make(map[string]int),
		sublinearTF: false,
	}
}

// NewTFIDFEncoderWithSublinearTF creates a new TF-IDF encoder with sublinear TF scaling.
func NewTFIDFEncoderWithSublinearTF() *TFIDFEncoder {
	return &TFIDFEncoder{
		idf:         make(map[string]float64),
		docFreq:     make(map[string]int),
		sublinearTF: true,
	}
}

// Fit trains the TF-IDF encoder on a corpus of documents.
func (e *TFIDFEncoder) Fit(ctx context.Context, documents []string) error {
	e.idfMu.Lock()
	defer e.idfMu.Unlock()

	e.totalDocs = len(documents)
	e.docFreq = make(map[string]int)
	termDocCount := make(map[string]map[int]bool)

	for docIdx, doc := range documents {
		terms := tokenize(doc)
		uniqueTerms := make(map[string]bool)

		for _, term := range terms {
			if !uniqueTerms[term] {
				uniqueTerms[term] = true
				if termDocCount[term] == nil {
					termDocCount[term] = make(map[int]bool)
				}
				termDocCount[term][docIdx] = true
			}

			// Add to vocabulary
			e.vocabMu.Lock()
			found := false
			for _, v := range e.vocabulary {
				if v == term {
					found = true
					break
				}
			}
			if !found {
				e.vocabulary = append(e.vocabulary, term)
			}
			e.vocabMu.Unlock()
		}
	}

	// Compute IDF: log(N / df)
	for term, docs := range termDocCount {
		df := float64(len(docs))
		e.docFreq[term] = len(docs)
		e.idf[term] = math.Log(float64(e.totalDocs) / df)
	}

	return nil
}

// EncodeSparse converts a document into a TF-IDF sparse vector.
func (e *TFIDFEncoder) EncodeSparse(text string) map[string]float64 {
	e.idfMu.RLock()
	defer e.idfMu.RUnlock()

	terms := tokenize(text)
	result := make(map[string]float64)

	// Count term frequencies
	termFreq := make(map[string]int)
	for _, term := range terms {
		termFreq[term]++
	}

	// Compute TF-IDF for each term
	for term, tf := range termFreq {
		idf, ok := e.idf[term]
		if !ok {
			// Unknown term - skip or use default
			continue
		}

		var tfVal float64
		if e.sublinearTF {
			tfVal = 1 + math.Log(float64(tf))
		} else {
			tfVal = float64(tf)
		}

		result[term] = tfVal * idf
	}

	return result
}

// EncodeSparseBatch converts multiple documents into TF-IDF sparse vectors.
func (e *TFIDFEncoder) EncodeSparseBatch(texts []string) []map[string]float64 {
	results := make([]map[string]float64, len(texts))
	for i, text := range texts {
		results[i] = e.EncodeSparse(text)
	}
	return results
}

// Vocabulary returns the encoder's vocabulary.
func (e *TFIDFEncoder) Vocabulary() []string {
	e.vocabMu.RLock()
	defer e.vocabMu.RUnlock()
	return e.vocabulary
}

// Dimensions returns the size of the vocabulary.
func (e *TFIDFEncoder) Dimensions() int {
	e.vocabMu.RLock()
	defer e.vocabMu.RUnlock()
	return len(e.vocabulary)
}

// HybridEmbedder combines a dense embedder with a sparse encoder.
type HybridEmbedder struct {
	dense  Embedder
	sparse SparseEncoder

	// Weight for combining dense and sparse similarities (default 0.5)
	// Final score = alpha * dense_similarity + (1-alpha) * sparse_similarity
	alpha float64
}

// NewHybridEmbedder creates a new hybrid embedder.
func NewHybridEmbedder(dense Embedder, sparse SparseEncoder) *HybridEmbedder {
	return &HybridEmbedder{
		dense:  dense,
		sparse: sparse,
		alpha:  0.5,
	}
}

// NewHybridEmbedderWithAlpha creates a new hybrid embedder with custom alpha.
func NewHybridEmbedderWithAlpha(dense Embedder, sparse SparseEncoder, alpha float64) *HybridEmbedder {
	return &HybridEmbedder{
		dense:  dense,
		sparse: sparse,
		alpha:  alpha,
	}
}

// Embed computes dense embedding (for compatibility with Embedder interface).
func (h *HybridEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return h.dense.Embed(ctx, text)
}

// EmbedBatch computes dense embeddings in batch.
func (h *HybridEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return h.dense.EmbedBatch(ctx, texts)
}

// Dimensions returns the dense embedding dimension.
func (h *HybridEmbedder) Dimensions() int {
	return h.dense.Dimensions()
}

// EncodeSparse computes sparse encoding.
func (h *HybridEmbedder) EncodeSparse(text string) map[string]float64 {
	return h.sparse.EncodeSparse(text)
}

// EncodeSparseBatch computes sparse encodings in batch.
func (h *HybridEmbedder) EncodeSparseBatch(texts []string) []map[string]float64 {
	return h.sparse.EncodeSparseBatch(texts)
}

// HybridSimilarity computes combined similarity using both dense and sparse.
func (h *HybridEmbedder) HybridSimilarity(
	queryDense []float32,
	querySparse map[string]float64,
	compareDense []float32,
	compareSparse map[string]float64,
	similarityFunc core.SimilarityFunc,
) float64 {
	// Compute dense similarity
	denseSim := float64(0)
	if similarityFunc != nil && len(queryDense) > 0 && len(compareDense) > 0 {
		denseSim = similarityFunc(queryDense, compareDense)
	}

	// Compute sparse similarity
	sparseSim := SparseSimilarity(querySparse, compareSparse)

	// Combine using alpha
	return h.alpha*denseSim + (1-h.alpha)*sparseSim
}

// SetAlpha updates the weight for combining dense and sparse similarities.
func (h *HybridEmbedder) SetAlpha(alpha float64) {
	h.alpha = alpha
}

// GetAlpha returns the current alpha value.
func (h *HybridEmbedder) GetAlpha() float64 {
	return h.alpha
}

// TopTerms returns the top-k terms from a sparse vector by weight.
func TopTerms(vec map[string]float64, k int) []TermScore {
	if len(vec) == 0 {
		return nil
	}

	scores := make([]TermScore, 0, len(vec))
	for term, score := range vec {
		scores = append(scores, TermScore{Term: term, Score: score})
	}

	// Sort by score descending
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].Score > scores[j].Score
	})

	if k > 0 && k < len(scores) {
		scores = scores[:k]
	}

	return scores
}

// TermScore represents a term with its score.
type TermScore struct {
	Term  string
	Score float64
}
