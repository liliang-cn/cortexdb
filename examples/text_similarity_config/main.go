package main

import (
	"fmt"

	"github.com/liliang-cn/sqvect"
)

func main() {
	fmt.Println("=== Text Similarity Configuration Examples ===")
	fmt.Println()

	// Example 1: Default (no special terms)
	fmt.Println("1. Default text similarity (no special terms):")
	defaultSim := sqvect.NewTextSimilarity()
	score1 := defaultSim.CalculateSimilarity("yinshu", "音书")
	fmt.Printf("   'yinshu' vs '音书': %.3f\n", score1)

	// Example 2: With predefined Chinese options
	fmt.Println("\n2. With predefined Chinese term pairs:")
	chineseSim := sqvect.NewTextSimilarityWithOptions(sqvect.DefaultChineseOptions())
	score2 := chineseSim.CalculateSimilarity("yinshu", "音书")
	fmt.Printf("   'yinshu' vs '音书': %.3f\n", score2)

	// Example 3: Custom configuration
	fmt.Println("\n3. Custom configuration:")
	customOptions := sqvect.TextSimilarityOptions{
		BoostTerms: map[string]float64{
			"test":    1.5,
			"demo":    1.3,
			"example": 1.2,
		},
		TermPairs: map[string][]string{
			"ai":   {"artificial intelligence", "人工智能"},
			"ml":   {"machine learning", "机器学习"},
			"deep": {"deep learning", "深度学习"},
		},
	}

	customSim := sqvect.NewTextSimilarityWithOptions(customOptions)

	// Test custom term pairs
	score3 := customSim.CalculateSimilarity("ai", "artificial intelligence")
	fmt.Printf("   'ai' vs 'artificial intelligence': %.3f\n", score3)

	score4 := customSim.CalculateSimilarity("ml", "machine learning")
	fmt.Printf("   'ml' vs 'machine learning': %.3f\n", score4)

	// Example 4: Adding terms dynamically
	fmt.Println("\n4. Adding terms dynamically:")
	dynamicSim := sqvect.NewTextSimilarity()

	// Add boost terms
	dynamicSim.AddBoostTerm("important", 1.5)
	dynamicSim.AddBoostTerm("critical", 1.4)

	// Add term pairs
	dynamicSim.AddTermPair("db", []string{"database", "数据库"})
	dynamicSim.AddTermPair("api", []string{"interface", "接口"})

	score5 := dynamicSim.CalculateSimilarity("db", "database")
	fmt.Printf("   'db' vs 'database': %.3f\n", score5)

	score6 := dynamicSim.CalculateSimilarity("api", "interface")
	fmt.Printf("   'api' vs 'interface': %.3f\n", score6)

	fmt.Println("\n=== Benefits of Configuration Approach ===")
	fmt.Println("✓ No hardcoded business logic")
	fmt.Println("✓ Flexible configuration per use case")
	fmt.Println("✓ Easy to maintain and extend")
	fmt.Println("✓ Testable with different configurations")
	fmt.Println("✓ Can load configurations from external files")
}
