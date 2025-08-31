package sqvect

import (
	"math"
	"regexp"
	"strings"
	"unicode"
	
	"github.com/mozillazg/go-pinyin"
)

// TextSimilarity provides text-based similarity calculations
type TextSimilarity struct {
	pinyinArgs         pinyin.Args
	boostTerms         map[string]float64 // Configurable boost terms
	termPairs          map[string][]string // Configurable translation pairs
	allowScoreAboveOne bool                // Whether to allow scores above 1.0
}

// TextSimilarityOptions provides configuration for text similarity calculator
type TextSimilarityOptions struct {
	BoostTerms map[string]float64   // Terms that get similarity boost
	TermPairs  map[string][]string  // Translation pairs for special matching
	AllowScoreAboveOne bool         // Whether to allow scores above 1.0 with boost
}

// NewTextSimilarity creates a new text similarity calculator with default config
func NewTextSimilarity() *TextSimilarity {
	return NewTextSimilarityWithOptions(TextSimilarityOptions{})
}

// NewTextSimilarityWithOptions creates a new text similarity calculator with custom config
func NewTextSimilarityWithOptions(options TextSimilarityOptions) *TextSimilarity {
	args := pinyin.NewArgs()
	args.Style = pinyin.Normal    // No tone marks for better matching
	args.Heteronym = false        // Use most common pronunciation
	args.Separator = ""           // No separator between pinyin syllables
	args.Fallback = func(r rune, a pinyin.Args) []string {
		return []string{string(r)} // Keep non-Chinese characters as-is
	}
	
	// Initialize with provided config or empty maps
	boostTerms := make(map[string]float64)
	if options.BoostTerms != nil {
		for k, v := range options.BoostTerms {
			boostTerms[k] = v
		}
	}
	
	termPairs := make(map[string][]string)
	if options.TermPairs != nil {
		for k, v := range options.TermPairs {
			termPairs[k] = v
		}
	}
	
	return &TextSimilarity{
		pinyinArgs:         args,
		boostTerms:         boostTerms,
		termPairs:          termPairs,
		allowScoreAboveOne: options.AllowScoreAboveOne,
	}
}

// AddBoostTerm adds a boost term for similarity calculation
func (ts *TextSimilarity) AddBoostTerm(term string, boost float64) {
	ts.boostTerms[term] = boost
}

// AddTermPair adds a translation pair for special term matching
func (ts *TextSimilarity) AddTermPair(term string, translations []string) {
	ts.termPairs[term] = translations
}

// DefaultChineseConfig returns a config with common Chinese-English term pairs
// DefaultChineseOptions returns commonly used Chinese-English term configurations
func DefaultChineseOptions() TextSimilarityOptions {
	return TextSimilarityOptions{
		AllowScoreAboveOne: false, // Default: keep scores <= 1.0
		BoostTerms: map[string]float64{
			"yinshu":   1.0,
			"音书":      1.0,
			"beijing":  1.0,
			"北京":      1.0,
			"bar":      1.0,
			"酒吧":      1.0,
			"coffee":   1.0,
			"咖啡":      1.0,
		},
		TermPairs: map[string][]string{
			"yinshu":   {"音书"},
			"音书":      {"yinshu"},
			"beijing":  {"北京"},
			"北京":      {"beijing"},
			"bar":      {"酒吧", "pub"},
			"酒吧":      {"bar", "pub"},
			"咖啡":      {"coffee", "cafe"},
			"coffee":   {"咖啡", "cafe"},
		},
	}
}

// CalculateSimilarity computes text similarity between query and content
func (ts *TextSimilarity) CalculateSimilarity(query, content string) float64 {
	if query == "" || content == "" {
		return 0.0
	}
	
	// Step 1: Normalize both texts
	queryNorm := ts.normalizeText(query)
	contentNorm := ts.normalizeText(content)
	
	// Step 2: Convert to pinyin for Chinese parts
	queryPinyin := ts.convertToPinyin(queryNorm)
	contentPinyin := ts.convertToPinyin(contentNorm)
	
	// Step 3: Calculate multiple similarity scores
	scores := []float64{
		// Direct text matching
		ts.fuzzyMatch(queryNorm, contentNorm),
		ts.fuzzyMatch(queryNorm, contentPinyin),
		
		// Pinyin-based matching
		ts.fuzzyMatch(queryPinyin, contentNorm),
		ts.fuzzyMatch(queryPinyin, contentPinyin),
		
		// Word-level matching
		ts.wordLevelMatch(queryNorm, contentNorm),
		ts.wordLevelMatch(queryPinyin, contentPinyin),
		
		// Special term matching
		ts.specialTermMatch(queryNorm, contentNorm),
	}
	
	// Return the best score with boost factor
	bestScore := 0.0
	for _, score := range scores {
		if score > bestScore {
			bestScore = score
		}
	}
	
	// Apply boost for special terms
	boost := ts.calculateBoost(queryNorm, contentNorm)
	finalScore := bestScore * boost
	
	// Optionally clamp to [0, 1] range
	if ts.allowScoreAboveOne {
		return math.Max(finalScore, 0.0) // Only ensure it's not negative
	} else {
		return math.Min(math.Max(finalScore, 0.0), 1.0) // Clamp to [0, 1]
	}
}

// normalizeText performs text normalization
func (ts *TextSimilarity) normalizeText(text string) string {
	// Convert to lowercase
	text = strings.ToLower(text)
	
	// Remove extra whitespace
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)
	
	// Remove punctuation but keep Chinese characters and alphanumeric
	var normalized strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Han, r) || r == ' ' {
			normalized.WriteRune(r)
		}
	}
	
	return normalized.String()
}

// convertToPinyin converts Chinese characters to pinyin
func (ts *TextSimilarity) convertToPinyin(text string) string {
	var result strings.Builder
	var currentWord strings.Builder
	
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			// Chinese character - convert to pinyin
			if currentWord.Len() > 0 {
				// Process accumulated non-Chinese word
				result.WriteString(currentWord.String())
				currentWord.Reset()
			}
			
			// Convert single character to pinyin
			pinyinSlice := pinyin.Pinyin(string(r), ts.pinyinArgs)
			if len(pinyinSlice) > 0 && len(pinyinSlice[0]) > 0 {
				result.WriteString(pinyinSlice[0][0])
			}
		} else {
			// Non-Chinese character - accumulate
			currentWord.WriteRune(r)
		}
	}
	
	// Don't forget the last word
	if currentWord.Len() > 0 {
		result.WriteString(currentWord.String())
	}
	
	return strings.TrimSpace(result.String())
}

// fuzzyMatch calculates fuzzy string similarity using Levenshtein distance
func (ts *TextSimilarity) fuzzyMatch(str1, str2 string) float64 {
	if str1 == str2 {
		return 1.0
	}
	
	// Handle empty strings
	if len(str1) == 0 || len(str2) == 0 {
		return 0.0
	}
	
	// Calculate Levenshtein distance
	distance := levenshteinDistance(str1, str2)
	maxLen := math.Max(float64(len(str1)), float64(len(str2)))
	
	// Convert distance to similarity (0-1 range)
	similarity := 1.0 - (float64(distance) / maxLen)
	return math.Max(similarity, 0.0)
}

// wordLevelMatch performs word-level similarity matching
func (ts *TextSimilarity) wordLevelMatch(query, content string) float64 {
	queryWords := strings.Fields(query)
	contentWords := strings.Fields(content)
	
	if len(queryWords) == 0 || len(contentWords) == 0 {
		return 0.0
	}
	
	matchCount := 0
	for _, qWord := range queryWords {
		for _, cWord := range contentWords {
			if ts.fuzzyMatch(qWord, cWord) > 0.8 { // High threshold for word matching
				matchCount++
				break
			}
		}
	}
	
	// Return ratio of matched words
	return float64(matchCount) / float64(len(queryWords))
}

// specialTermMatch handles special terms with known translations
func (ts *TextSimilarity) specialTermMatch(query, content string) float64 {
	// Use configured term pairs if available
	if len(ts.termPairs) == 0 {
		return 0.0 // No special terms configured
	}
	
	score := 0.0
	queryWords := strings.Fields(query)
	
	for _, qWord := range queryWords {
		if translations, exists := ts.termPairs[qWord]; exists {
			for _, translation := range translations {
				if strings.Contains(content, translation) {
					score += 1.0 / float64(len(queryWords))
				}
			}
		}
	}
	
	return math.Min(score, 1.0)
}

// calculateBoost applies boost factors for special terms
func (ts *TextSimilarity) calculateBoost(query, content string) float64 {
	boost := 1.0
	
	// Check for direct term matches in boost terms
	for term, factor := range ts.boostTerms {
		if strings.Contains(query, term) && strings.Contains(content, term) {
			boost *= factor
		}
	}
	
	// Check for cross-language matches using term pairs
	for queryTerm, factor := range ts.boostTerms {
		if strings.Contains(query, queryTerm) {
			// Check if content contains any paired terms
			if pairedTerms, exists := ts.termPairs[queryTerm]; exists {
				for _, pairedTerm := range pairedTerms {
					if strings.Contains(content, pairedTerm) {
						// Check if the paired term also has a boost factor
						if pairedFactor, pairedExists := ts.boostTerms[pairedTerm]; pairedExists {
							// Use the maximum boost factor of the pair
							maxFactor := math.Max(factor, pairedFactor)
							boost *= maxFactor
							break // Only apply once per query term
						} else {
							// Use the current term's factor
							boost *= factor
							break
						}
					}
				}
			}
		}
	}
	
	return boost
}

// levenshteinDistance calculates the Levenshtein distance between two strings
func levenshteinDistance(str1, str2 string) int {
	runes1 := []rune(str1)
	runes2 := []rune(str2)
	
	if len(runes1) == 0 {
		return len(runes2)
	}
	if len(runes2) == 0 {
		return len(runes1)
	}
	
	// Create matrix
	matrix := make([][]int, len(runes1)+1)
	for i := range matrix {
		matrix[i] = make([]int, len(runes2)+1)
	}
	
	// Initialize first row and column
	for i := 0; i <= len(runes1); i++ {
		matrix[i][0] = i
	}
	for j := 0; j <= len(runes2); j++ {
		matrix[0][j] = j
	}
	
	// Fill matrix
	for i := 1; i <= len(runes1); i++ {
		for j := 1; j <= len(runes2); j++ {
			cost := 0
			if runes1[i-1] != runes2[j-1] {
				cost = 1
			}
			
			matrix[i][j] = min(
				matrix[i-1][j]+1,      // deletion
				matrix[i][j-1]+1,      // insertion
				matrix[i-1][j-1]+cost, // substitution
			)
		}
	}
	
	return matrix[len(runes1)][len(runes2)]
}

// min returns the minimum of three integers
func min(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}