package main

import (
	"fmt"

	"github.com/liliang-cn/sqvect"
)

func main() {
	fmt.Println("=== 文本相似度 Score 详细测试 ===")
	fmt.Println()

	// 测试1: 默认配置（无特殊词汇）
	fmt.Println("🔹 测试1: 默认配置（无特殊词汇）")
	defaultSim := sqvect.NewTextSimilarity()

	testCases := []struct {
		query   string
		content string
		desc    string
	}{
		{"yinshu", "音书", "英文vs中文"},
		{"yinshu", "yinshu", "完全相同"},
		{"beijing", "北京", "北京英文vs中文"},
		{"bar", "酒吧", "酒吧英文vs中文"},
		{"hello", "hello world", "部分匹配"},
		{"ai", "artificial intelligence", "缩写vs全称"},
		{"machine learning", "机器学习", "英文vs中文术语"},
		{"coffee", "咖啡", "咖啡英文vs中文"},
		{"", "test", "空查询"},
		{"test", "", "空内容"},
		{"completely different", "完全不同的内容", "完全不相关"},
	}

	for _, tc := range testCases {
		score := defaultSim.CalculateSimilarity(tc.query, tc.content)
		fmt.Printf("   '%s' vs '%s' (%s): %.3f\n", tc.query, tc.content, tc.desc, score)
	}

	fmt.Println()

	// 测试2: 使用中文预设配置
	fmt.Println("🔹 测试2: 使用中文预设配置")
	chineseSim := sqvect.NewTextSimilarityWithOptions(sqvect.DefaultChineseOptions())

	for _, tc := range testCases {
		score := chineseSim.CalculateSimilarity(tc.query, tc.content)
		fmt.Printf("   '%s' vs '%s' (%s): %.3f\n", tc.query, tc.content, tc.desc, score)
	}

	fmt.Println()

	// 测试3: 自定义高权重配置
	fmt.Println("🔹 测试3: 自定义高权重配置")
	highBoostOptions := sqvect.TextSimilarityOptions{
		BoostTerms: map[string]float64{
			"yinshu":  2.0, // 很高的权重
			"音书":      2.0,
			"beijing": 1.8,
			"北京":      1.8,
			"bar":     1.5,
			"酒吧":      1.5,
		},
		TermPairs: map[string][]string{
			"yinshu":  {"音书", "yin shu"},
			"音书":      {"yinshu", "yin shu"},
			"beijing": {"北京", "bei jing"},
			"北京":      {"beijing", "bei jing"},
			"bar":     {"酒吧", "pub"},
			"酒吧":      {"bar", "pub"},
		},
	}

	highBoostSim := sqvect.NewTextSimilarityWithOptions(highBoostOptions)

	for _, tc := range testCases {
		score := highBoostSim.CalculateSimilarity(tc.query, tc.content)
		fmt.Printf("   '%s' vs '%s' (%s): %.3f\n", tc.query, tc.content, tc.desc, score)
	}

	fmt.Println()

	// 测试4: 复杂场景测试
	fmt.Println("🔹 测试4: 复杂场景测试")
	complexCases := []struct {
		query   string
		content string
		desc    string
	}{
		{"yinshu bar", "音书酒吧", "多个特殊词汇"},
		{"beijing yinshu", "北京音书", "多个中英文混合"},
		{"sanlitun bar", "三里屯的酒吧很棒", "词汇在句子中"},
		{"yinshu", "音书是一个很好的地方", "目标词在句子开头"},
		{"找yinshu", "寻找音书", "中英文混合查询"},
		{"coffee shop", "咖啡店", "组合词匹配"},
		{"beijing coffee", "北京咖啡", "多词匹配"},
	}

	for _, tc := range complexCases {
		defaultScore := defaultSim.CalculateSimilarity(tc.query, tc.content)
		chineseScore := chineseSim.CalculateSimilarity(tc.query, tc.content)
		highBoostScore := highBoostSim.CalculateSimilarity(tc.query, tc.content)

		fmt.Printf("   '%s' vs '%s' (%s):\n", tc.query, tc.content, tc.desc)
		fmt.Printf("     默认: %.3f | 中文预设: %.3f | 高权重: %.3f\n",
			defaultScore, chineseScore, highBoostScore)
	}

	fmt.Println()

	// 测试5: 边界情况
	fmt.Println("🔹 测试5: 边界情况")
	boundaryCases := []struct {
		query   string
		content string
		desc    string
	}{
		{"a", "a", "单字符完全匹配"},
		{"a", "b", "单字符不匹配"},
		{"very long query with many words", "很长的内容包含很多单词", "长文本匹配"},
		{"YINSHU", "音书", "大小写测试"},
		{"yinshu!", "音书。", "标点符号测试"},
		{"  yinshu  ", "  音书  ", "空格测试"},
		{"yinshu123", "音书456", "数字混合"},
	}

	for _, tc := range boundaryCases {
		score := chineseSim.CalculateSimilarity(tc.query, tc.content)
		fmt.Printf("   '%s' vs '%s' (%s): %.3f\n", tc.query, tc.content, tc.desc, score)
	}

	fmt.Println()
	fmt.Println("=== 总结 ===")
	fmt.Println("- Score 范围: 0.0 到 1.0（可能超过1.0如果有boost）")
	fmt.Println("- 1.0 表示完美匹配")
	fmt.Println("- 0.0 表示完全不匹配")
	fmt.Println("- 配置的boost可以让特定词汇的匹配分数超过1.0")
	fmt.Println("- 特殊词对(TermPairs)可以建立跨语言的匹配关系")
}
