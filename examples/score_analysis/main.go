package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/liliang-cn/sqvect"
)

func main() {
	fmt.Println("=== 分析 Score 被限制的问题 ===")
	fmt.Println()

	// 创建一个临时的测试函数来验证boost计算
	testBoostCalculation := func() {
		fmt.Println("🔍 验证 boost 计算逻辑")
		
		// 模拟calculateBoost的逻辑
		boostTerms := map[string]float64{
			"yinshu": 3.0,
			"音书":    3.0,
		}
		
		query := "yinshu"
		content := "音书"
		
		boost := 1.0
		for term, factor := range boostTerms {
			if strings.Contains(query, term) && strings.Contains(content, term) {
				boost *= factor
				fmt.Printf("   找到boost词 '%s'，factor = %.1f，累积boost = %.1f\n", term, factor, boost)
			}
		}
		
		// 假设baseScore = 1.0（完美匹配）
		baseScore := 1.0
		finalScore := baseScore * boost
		clampedScore := math.Min(finalScore, 1.0)
		
		fmt.Printf("   基础分数: %.1f\n", baseScore)
		fmt.Printf("   Boost倍数: %.1f\n", boost)
		fmt.Printf("   计算后分数: %.1f\n", finalScore)
		fmt.Printf("   限制后分数: %.1f ← 这里被限制了！\n", clampedScore)
		fmt.Println()
	}
	
	testBoostCalculation()

	// 实际测试当前的实现
	fmt.Println("🔹 当前实现的实际表现")
	
	options := sqvect.TextSimilarityOptions{
		BoostTerms: map[string]float64{
			"yinshu": 5.0,
			"音书":    5.0,
		},
		TermPairs: map[string][]string{
			"yinshu": {"音书"},
			"音书":    {"yinshu"},
		},
	}
	
	sim := sqvect.NewTextSimilarityWithOptions(options)
	score := sim.CalculateSimilarity("yinshu", "音书")
	
	fmt.Printf("   'yinshu' vs '音书' (boost=5.0): %.3f\n", score)
	fmt.Printf("   结果：即使设置了5倍boost，分数仍然被限制在1.0\n")
	
	fmt.Println()
	fmt.Println("💡 建议的改进方案:")
	fmt.Println("   1. 移除 math.Min(finalScore, 1.0) 的限制")
	fmt.Println("   2. 或者提供配置选项来控制是否限制最大值")
	fmt.Println("   3. 或者使用不同的boost应用策略")
}
