package sqvect

// TextSimilarity interface for text-based similarity calculations
type TextSimilarity interface {
	CalculateSimilarity(query, text string) float64
}