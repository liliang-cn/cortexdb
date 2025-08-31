package main

import (
	"fmt"

	"github.com/liliang-cn/sqvect"
)

func main() {
	fmt.Println("=== 🚀 Score超过1.0测试 (修改后) ===")
	fmt.Println()

	// 测试1: 默认配置（限制在1.0）
	fmt.Println("🔒 测试1: 默认配置（AllowScoreAboveOne = false）")
	
	normalOptions := sqvect.TextSimilarityOptions{
		AllowScoreAboveOne: false, // 限制在1.0
		BoostTerms: map[string]float64{
			"yinshu": 3.0,
			"音书":    3.0,
		},
		TermPairs: map[string][]string{
			"yinshu": {"音书"},
			"音书":    {"yinshu"},
		},
	}
	
	normalSim := sqvect.NewTextSimilarityWithOptions(normalOptions)
	score1 := normalSim.CalculateSimilarity("yinshu", "音书")
	fmt.Printf("   'yinshu' vs '音书' (boost=3.0, 限制=true): %.3f\n", score1)
	
	fmt.Println()

	// 测试2: 允许超过1.0
	fmt.Println("🔓 测试2: 允许超过1.0（AllowScoreAboveOne = true）")
	
	unlimitedOptions := sqvect.TextSimilarityOptions{
		AllowScoreAboveOne: true, // 允许超过1.0！
		BoostTerms: map[string]float64{
			"yinshu": 3.0,
			"音书":    3.0,
		},
		TermPairs: map[string][]string{
			"yinshu": {"音书"},
			"音书":    {"yinshu"},
		},
	}
	
	unlimitedSim := sqvect.NewTextSimilarityWithOptions(unlimitedOptions)
	score2 := unlimitedSim.CalculateSimilarity("yinshu", "音书")
	fmt.Printf("   'yinshu' vs '音书' (boost=3.0, 限制=false): %.3f", score2)
	if score2 > 1.0 {
		fmt.Printf(" 🔥 成功超过1.0！")
	}
	fmt.Println()
	
	fmt.Println()

	// 测试3: 不同boost级别的对比
	fmt.Println("🎚️ 测试3: 不同boost级别的效果对比")
	
	boostLevels := []float64{1.0, 2.0, 3.0, 5.0, 10.0}
	
	for _, boost := range boostLevels {
		// 限制版本
		limitedOptions := sqvect.TextSimilarityOptions{
			AllowScoreAboveOne: false,
			BoostTerms: map[string]float64{
				"yinshu": boost,
				"音书":    boost,
			},
			TermPairs: map[string][]string{
				"yinshu": {"音书"},
				"音书":    {"yinshu"},
			},
		}
		
		// 不限制版本
		unlimitedOptions := sqvect.TextSimilarityOptions{
			AllowScoreAboveOne: true,
			BoostTerms: map[string]float64{
				"yinshu": boost,
				"音书":    boost,
			},
			TermPairs: map[string][]string{
				"yinshu": {"音书"},
				"音书":    {"yinshu"},
			},
		}
		
		limitedSim := sqvect.NewTextSimilarityWithOptions(limitedOptions)
		unlimitedSim := sqvect.NewTextSimilarityWithOptions(unlimitedOptions)
		
		limitedScore := limitedSim.CalculateSimilarity("yinshu", "音书")
		unlimitedScore := unlimitedSim.CalculateSimilarity("yinshu", "音书")
		
		fmt.Printf("   Boost %.1f: 限制版=%.3f | 不限制版=%.3f", boost, limitedScore, unlimitedScore)
		if unlimitedScore > 1.0 {
			fmt.Printf(" 🔥")
		}
		fmt.Println()
	}

	fmt.Println()

	// 测试4: 复杂查询场景
	fmt.Println("🎯 测试4: 复杂查询中的boost效果")
	
	superBoostOptions := sqvect.TextSimilarityOptions{
		AllowScoreAboveOne: true,
		BoostTerms: map[string]float64{
			"yinshu": 5.0,
			"音书":    5.0,
			"beijing": 3.0,
			"北京":     3.0,
		},
		TermPairs: map[string][]string{
			"yinshu":  {"音书"},
			"音书":     {"yinshu"},
			"beijing": {"北京"},
			"北京":     {"beijing"},
		},
	}
	
	superBoostSim := sqvect.NewTextSimilarityWithOptions(superBoostOptions)
	
	complexCases := []struct {
		query   string
		content string
		desc    string
	}{
		{"yinshu", "音书", "单词完美匹配"},
		{"yinshu bar", "音书酒吧", "部分boost匹配"},
		{"beijing yinshu", "北京音书", "多个boost词"},
		{"find yinshu", "寻找音书", "句子中的boost词"},
	}

	for _, tc := range complexCases {
		score := superBoostSim.CalculateSimilarity(tc.query, tc.content)
		fmt.Printf("   '%s' vs '%s': %.3f", tc.query, tc.content, score)
		if score > 1.0 {
			fmt.Printf(" 🔥")
		}
		fmt.Printf(" (%s)\n", tc.desc)
	}

	fmt.Println()
	fmt.Println("=== 结论 ===")
	fmt.Println("✅ 现在可以通过 AllowScoreAboveOne=true 让分数超过1.0")
	fmt.Println("✅ Boost权重确实可以放大相似度分数")
	fmt.Println("✅ 用户可以根据需求选择是否限制分数上限")
	fmt.Println("🎯 这样既保持了向后兼容，又提供了灵活性")
}
