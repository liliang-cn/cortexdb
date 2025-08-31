package main

import (
	"fmt"
	"strings"

	"github.com/liliang-cn/sqvect"
)

func main() {
	fmt.Println("=== 🔍 深度调试 Boost 计算 ===")
	fmt.Println()

	// 手动模拟计算过程
	fmt.Println("🧮 手动模拟 calculateBoost 逻辑：")

	boostTerms := map[string]float64{
		"yinshu": 3.0,
		"音书":     3.0,
	}

	query := "yinshu"
	content := "音书"

	// 模拟 normalizeText (转小写)
	queryNorm := strings.ToLower(query)
	contentNorm := strings.ToLower(content)

	fmt.Printf("   原始查询: '%s' -> 标准化: '%s'\n", query, queryNorm)
	fmt.Printf("   原始内容: '%s' -> 标准化: '%s'\n", content, contentNorm)

	// 模拟 calculateBoost
	boost := 1.0
	fmt.Printf("   初始 boost: %.1f\n", boost)

	for term, factor := range boostTerms {
		queryContains := strings.Contains(queryNorm, term)
		contentContains := strings.Contains(contentNorm, term)

		fmt.Printf("   检查词 '%s' (factor=%.1f):\n", term, factor)
		fmt.Printf("     查询包含: %v\n", queryContains)
		fmt.Printf("     内容包含: %v\n", contentContains)

		if queryContains && contentContains {
			boost *= factor
			fmt.Printf("     ✅ 两边都包含，boost *= %.1f -> %.1f\n", factor, boost)
		} else {
			fmt.Printf("     ❌ 不满足条件，boost 不变\n")
		}
	}

	fmt.Printf("   最终 boost: %.1f\n", boost)
	fmt.Println()

	// 测试实际的API
	fmt.Println("🔬 测试实际的 API：")

	options := sqvect.TextSimilarityOptions{
		AllowScoreAboveOne: true,
		BoostTerms: map[string]float64{
			"yinshu": 3.0,
			"音书":     3.0,
		},
		TermPairs: map[string][]string{
			"yinshu": {"音书"},
			"音书":     {"yinshu"},
		},
	}

	sim := sqvect.NewTextSimilarityWithOptions(options)
	score := sim.CalculateSimilarity("yinshu", "音书")

	fmt.Printf("   实际结果: %.3f\n", score)
	fmt.Println()

	// 测试一些变体
	fmt.Println("🧪 测试不同情况：")

	testCases := []struct {
		query   string
		content string
		desc    string
	}{
		{"yinshu", "yinshu", "完全相同的英文"},
		{"音书", "音书", "完全相同的中文"},
		{"yinshu", "音书", "英文对中文"},
		{"音书", "yinshu", "中文对英文"},
		{"yinshu test", "yinshu test", "包含boost词的句子"},
		{"something yinshu", "其他 音书", "句子中包含对应词"},
	}

	for _, tc := range testCases {
		score := sim.CalculateSimilarity(tc.query, tc.content)
		fmt.Printf("   '%s' vs '%s': %.3f (%s)\n", tc.query, tc.content, score, tc.desc)
	}

	fmt.Println()
	fmt.Println("🤔 分析：")
	fmt.Printf("   如果 boost 计算正确，'yinshu' vs 'yinshu' 应该得到 1.0 * 3.0 = 3.0\n")
	fmt.Printf("   但实际结果显示分数仍然是 1.0 或更低\n")
	fmt.Printf("   这表明可能的问题：\n")
	fmt.Printf("   1. calculateBoost 的匹配逻辑有问题\n")
	fmt.Printf("   2. 基础相似度计算本身有限制\n")
	fmt.Printf("   3. 或者还有其他地方限制了分数\n")
}
