package sqvect

import "math"

// SimilarityFunc defines a function that calculates similarity between two vectors
type SimilarityFunc func(a, b []float32) float64

// GetCosineSimilarity returns the cosine similarity function
func GetCosineSimilarity() SimilarityFunc {
	return cosineSimilarity
}

// GetDotProduct returns the dot product function
func GetDotProduct() SimilarityFunc {
	return dotProduct
}

// GetEuclideanDist returns the euclidean distance function
func GetEuclideanDist() SimilarityFunc {
	return euclideanDistance
}

// Predefined similarity functions for backward compatibility
var (
	// CosineSimilarity calculates cosine similarity between two vectors
	CosineSimilarity = cosineSimilarity
	
	// DotProduct calculates dot product between two vectors
	DotProduct = dotProduct
	
	// EuclideanDist calculates negative Euclidean distance (higher = more similar)
	EuclideanDist = euclideanDistance
)

// cosineSimilarity calculates cosine similarity between two vectors.
// Returns a value between -1 and 1, where 1 means identical direction.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0.0
	}
	
	var dotProduct, normA, normB float64
	
	for i := 0; i < len(a); i++ {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	
	// Handle zero vectors
	if normA == 0.0 || normB == 0.0 {
		return 0.0
	}
	
	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// dotProduct calculates the dot product between two vectors.
func dotProduct(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0.0
	}
	
	var result float64
	for i := 0; i < len(a); i++ {
		result += float64(a[i]) * float64(b[i])
	}
	
	return result
}

// euclideanDistance calculates negative Euclidean distance for similarity ranking.
// Returns negative distance so higher values indicate more similarity.
func euclideanDistance(a, b []float32) float64 {
	if len(a) != len(b) {
		return -math.Inf(1)
	}
	
	var sum float64
	for i := 0; i < len(a); i++ {
		diff := float64(a[i]) - float64(b[i])
		sum += diff * diff
	}
	
	return -math.Sqrt(sum)
}