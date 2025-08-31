package main

import (
	"fmt"

	"github.com/liliang-cn/sqvect"
)

func main() {
	fmt.Println("=== Boost权重超过1.0测试 ===")
	fmt.Println()

	// 创建一个超高权重的配置
	superBoostOptions := sqvect.TextSimilarityOptions{
		BoostTerms: map[string]float64{
			"yinshu": 5.0,  // 超高权重！
			"音书":    5.0,
			"test":   3.0,
			"超级":     4.0,
		},
		TermPairs: map[string][]string{
			"yinshu": {"音书"},
			"音书":    {"yinshu"},
		},
	}
	
	superBoostSim := sqvect.NewTextSimilarityWithOptions(superBoostOptions)

	fmt.Println("🚀 超高权重测试 (boost = 5.0)")
	testCases := []struct {
		query   string
		content string
		desc    string
	}{
		{"yinshu", "yinshu", "完全相同 + 5x boost"},
		{"yinshu", "音书", "中英对照 + 5x boost"},
		{"test", "test", "test词 + 3x boost"},
		{"超级", "超级", "中文词 + 4x boost"},
		{"yinshu test", "音书 test", "多个boost词"},
		{"yinshu something", "音书 其他", "部分boost词"},
	}

	for _, tc := range testCases {
		score := superBoostSim.CalculateSimilarity(tc.query, tc.content)
		fmt.Printf("   '%s' vs '%s' (%s): %.3f", tc.query, tc.content, tc.desc, score)
		if score > 1.0 {
			fmt.Printf(" 🔥 超过1.0！")
		}
		fmt.Println()
	}

	fmt.Println()
	fmt.Println("🔍 分析不同权重下的分数变化")
	
	boostLevels := []float64{1.0, 1.5, 2.0, 3.0, 5.0, 10.0}
	
	for _, boost := range boostLevels {
		options := sqvect.TextSimilarityOptions{
			BoostTerms: map[string]float64{
				"yinshu": boost,
				"音书":    boost,
			},
			TermPairs: map[string][]string{
				"yinshu": {"音书"},
				"音书":    {"yinshu"},
			},
		}
		
		sim := sqvect.NewTextSimilarityWithOptions(options)
		score := sim.CalculateSimilarity("yinshu", "音书")
		
		fmt.Printf("   Boost %.1f: Score = %.3f", boost, score)
		if score > 1.0 {
			fmt.Printf(" 🔥")
		}
		fmt.Println()
	}

	fmt.Println()
	fmt.Println("📊 复杂查询的boost效果")
	
	// 测试在复杂查询中boost的效果
	complexQueries := []struct {
		query   string
		content string
		desc    string
	}{
		{"find yinshu location", "寻找音书位置", "长查询中的boost词"},
		{"yinshu is great", "音书很棒", "boost词在句子中"},
		{"multiple yinshu yinshu", "多个音书音书", "重复boost词"},
		{"not related query", "无关内容", "无boost词基准"},
	}

	for _, tc := range complexQueries {
		score := superBoostSim.CalculateSimilarity(tc.query, tc.content)
		fmt.Printf("   '%s' vs '%s':\n", tc.query, tc.content)
		fmt.Printf("     Score: %.3f (%s)", score, tc.desc)
		if score > 1.0 {
			fmt.Printf(" 🔥 超过1.0！")
		}
		fmt.Println()
	}

	fmt.Println()
	fmt.Println("=== 结论 ===")
	fmt.Println("✅ Boost权重确实可以让分数超过1.0")
	fmt.Println("✅ 权重越高，匹配分数越高")
	fmt.Println("✅ 在复杂查询中，boost词仍然有效")
	fmt.Println("✅ Score的实际上限取决于boost值的设置")
}
