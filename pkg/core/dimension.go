package core

import (
	"fmt"
	"log"
	"math"
	"math/rand"
)

// DimensionAnalysis contains information about vector dimensions in the store
type DimensionAnalysis struct {
	PrimaryDim    int            `json:"primaryDim"`    // Most common dimension
	PrimaryCount  int            `json:"primaryCount"`  // Count of primary dimension
	Dimensions    map[int]int    `json:"dimensions"`    // Map of dimension -> count
	TotalVectors  int            `json:"totalVectors"`  // Total number of vectors
	NeedsMigration bool          `json:"needsMigration"` // Whether migration is recommended
}

// DimensionAdapter handles vector dimension adaptation
type DimensionAdapter struct {
	policy AdaptPolicy
}

// NewDimensionAdapter creates a new dimension adapter with the given policy
func NewDimensionAdapter(policy AdaptPolicy) *DimensionAdapter {
	return &DimensionAdapter{policy: policy}
}

// AdaptVector adapts a vector from source dimension to target dimension
func (da *DimensionAdapter) AdaptVector(vector []float32, sourceDim, targetDim int) ([]float32, error) {
	if len(vector) != sourceDim {
		return nil, fmt.Errorf("vector length %d doesn't match source dimension %d", len(vector), sourceDim)
	}

	if sourceDim == targetDim {
		return vector, nil // No adaptation needed
	}

	switch da.policy {
	case SmartAdapt:
		return da.smartAdapt(vector, sourceDim, targetDim), nil
	case AutoTruncate:
		return da.truncateVector(vector, targetDim), nil
	case AutoPad:
		return da.padVector(vector, targetDim), nil
	case WarnOnly:
		return vector, fmt.Errorf("dimension mismatch: expected %d, got %d (WarnOnly policy)", targetDim, sourceDim)
	default:
		return vector, fmt.Errorf("unknown adaptation policy: %v", da.policy)
	}
}

// smartAdapt intelligently adapts vector based on dimension difference
func (da *DimensionAdapter) smartAdapt(vector []float32, sourceDim, targetDim int) []float32 {
	if sourceDim > targetDim {
		// Truncate intelligently - keep most important dimensions
		return da.truncateWithImportance(vector, targetDim)
	} else {
		// Pad with small noise
		return da.padWithNoise(vector, targetDim)
	}
}

// truncateVector truncates vector to target dimension, or pads if smaller
func (da *DimensionAdapter) truncateVector(vector []float32, targetDim int) []float32 {
	if targetDim >= len(vector) {
		// If target is larger, pad with zeros
		result := make([]float32, targetDim)
		copy(result, vector)
		return normalizeVector(result)
	}
	
	result := make([]float32, targetDim)
	copy(result, vector[:targetDim])
	return normalizeVector(result)
}

// truncateWithImportance truncates vector keeping most important dimensions
func (da *DimensionAdapter) truncateWithImportance(vector []float32, targetDim int) []float32 {
	if targetDim >= len(vector) {
		return vector
	}

	// For simplicity, use magnitude-based importance
	// In practice, you might use more sophisticated methods
	type dimValue struct {
		index int
		value float32
		abs   float32
	}

	// Create dimension value pairs
	dims := make([]dimValue, len(vector))
	for i, v := range vector {
		dims[i] = dimValue{index: i, value: v, abs: float32(math.Abs(float64(v)))}
	}

	// Sort by absolute value (descending)
	for i := 0; i < len(dims)-1; i++ {
		for j := i + 1; j < len(dims); j++ {
			if dims[i].abs < dims[j].abs {
				dims[i], dims[j] = dims[j], dims[i]
			}
		}
	}

	// Take top targetDim dimensions and sort by original index
	selected := dims[:targetDim]
	for i := 0; i < len(selected)-1; i++ {
		for j := i + 1; j < len(selected); j++ {
			if selected[i].index > selected[j].index {
				selected[i], selected[j] = selected[j], selected[i]
			}
		}
	}

	// Build result vector
	result := make([]float32, targetDim)
	for i, dim := range selected {
		result[i] = dim.value
	}

	return normalizeVector(result)
}

// padVector pads vector to target dimension with zeros
func (da *DimensionAdapter) padVector(vector []float32, targetDim int) []float32 {
	if targetDim <= len(vector) {
		return normalizeVector(vector[:targetDim])
	}

	result := make([]float32, targetDim)
	copy(result, vector)
	// Remaining elements are already zero-initialized
	return normalizeVector(result)
}

// padWithNoise pads vector with small random noise
func (da *DimensionAdapter) padWithNoise(vector []float32, targetDim int) []float32 {
	if targetDim <= len(vector) {
		return vector[:targetDim]
	}

	result := make([]float32, targetDim)
	copy(result, vector)

	// Calculate noise level as 1% of vector standard deviation
	stddev := calculateVectorStddev(vector)
	noiseLevel := stddev * 0.01

	// Fill remaining dimensions with small random noise
	for i := len(vector); i < targetDim; i++ {
		result[i] = float32(rand.NormFloat64()) * noiseLevel
	}

	return normalizeVector(result)
}

// normalizeVector normalizes vector to unit length
func normalizeVector(vector []float32) []float32 {
	var sumSquares float64
	for _, v := range vector {
		sumSquares += float64(v) * float64(v)
	}

	if sumSquares == 0 {
		return vector // Avoid division by zero
	}

	norm := math.Sqrt(sumSquares)
	result := make([]float32, len(vector))
	for i, v := range vector {
		result[i] = float32(float64(v) / norm)
	}

	return result
}

// calculateVectorStddev calculates standard deviation of vector elements
func calculateVectorStddev(vector []float32) float32 {
	if len(vector) <= 1 {
		return 0.0
	}

	// Calculate mean
	var sum float64
	for _, v := range vector {
		sum += float64(v)
	}
	mean := sum / float64(len(vector))

	// Calculate variance
	var variance float64
	for _, v := range vector {
		diff := float64(v) - mean
		variance += diff * diff
	}
	variance /= float64(len(vector) - 1)

	return float32(math.Sqrt(variance))
}

// logDimensionEvent logs dimension-related events
func (da *DimensionAdapter) logDimensionEvent(event string, sourceDim, targetDim int, vectorID string) {
	switch da.policy {
	case SmartAdapt:
		if sourceDim != targetDim {
			if sourceDim > targetDim {
				log.Printf("[DIMENSION] Smart truncation: %d → %d for vector %s", sourceDim, targetDim, vectorID)
			} else {
				log.Printf("[DIMENSION] Smart padding: %d → %d for vector %s", sourceDim, targetDim, vectorID)
			}
		}
	case AutoTruncate:
		if sourceDim > targetDim {
			log.Printf("[DIMENSION] Auto truncation: %d → %d for vector %s", sourceDim, targetDim, vectorID)
		}
	case AutoPad:
		if sourceDim < targetDim {
			log.Printf("[DIMENSION] Auto padding: %d → %d for vector %s", sourceDim, targetDim, vectorID)
		}
	case WarnOnly:
		log.Printf("[DIMENSION] WARNING: Dimension mismatch %d ≠ %d for vector %s (no adaptation)", sourceDim, targetDim, vectorID)
	}
}

// AnalyzeDimensions analyzes dimension distribution in the given vectors
func AnalyzeDimensions(vectors [][]float32) *DimensionAnalysis {
	if len(vectors) == 0 {
		return &DimensionAnalysis{
			Dimensions:   make(map[int]int),
			TotalVectors: 0,
		}
	}

	dimensions := make(map[int]int)
	totalVectors := len(vectors)

	// Count dimensions
	for _, vector := range vectors {
		dim := len(vector)
		dimensions[dim]++
	}

	// Find primary dimension (most common)
	primaryDim := 0
	primaryCount := 0
	for dim, count := range dimensions {
		if count > primaryCount {
			primaryDim = dim
			primaryCount = count
		}
	}

	// Check if migration is needed (less than 80% are primary dimension)
	needsMigration := float64(primaryCount)/float64(totalVectors) < 0.8 && len(dimensions) > 1

	return &DimensionAnalysis{
		PrimaryDim:     primaryDim,
		PrimaryCount:   primaryCount,
		Dimensions:     dimensions,
		TotalVectors:   totalVectors,
		NeedsMigration: needsMigration,
	}
}