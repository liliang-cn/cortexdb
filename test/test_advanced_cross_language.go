package main

import (
	"context"
	"fmt"
	"os"
	"math"

	"github.com/liliang-cn/sqvect"
)

func main() {
	fmt.Println("🎯 Advanced Chinese-English Cross-Language Matching Tests")
	
	dbPath := "test_advanced_cross_lang.db"
	os.Remove(dbPath)
	defer os.Remove(dbPath)

	store, err := sqvect.New(dbPath, 0)
	if err != nil {
		panic(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		panic(err)
	}

	// 测试数据集：品牌名、地名、专有名词匹配
	testDatasets := []struct {
		name     string
		documents []DocumentPair
	}{
		{
			name: "品牌名测试 (Brand Names)",
			documents: []DocumentPair{
				{"brand_cn_1", "音书酒吧提供精致的鸡尾酒和舒适的环境", "Yinshu Bar offers exquisite cocktails and comfortable atmosphere"},
				{"brand_cn_2", "星巴克咖啡在中国市场表现出色", "Starbucks Coffee performs excellently in Chinese market"},
				{"brand_cn_3", "麦当劳快餐连锁遍布全球", "McDonald's fast food chain spreads globally"},
				{"brand_cn_4", "华为技术公司的创新能力很强", "Huawei Technology Company has strong innovation capabilities"},
				{"brand_cn_5", "小米手机在年轻人中很受欢迎", "Xiaomi phones are very popular among young people"},
			},
		},
		{
			name: "地名测试 (Place Names)",
			documents: []DocumentPair{
				{"place_cn_1", "北京是中国的首都和政治中心", "Beijing is the capital and political center of China"},
				{"place_cn_2", "上海是中国最大的经济中心", "Shanghai is China's largest economic center"},
				{"place_cn_3", "深圳是中国的科技创新之都", "Shenzhen is China's technology and innovation capital"},
				{"place_cn_4", "广州是华南地区的商贸中心", "Guangzhou is the commercial center of South China"},
				{"place_cn_5", "杭州以西湖美景而闻名", "Hangzhou is famous for the beautiful West Lake scenery"},
			},
		},
		{
			name: "行业术语测试 (Industry Terms)",
			documents: []DocumentPair{
				{"industry_cn_1", "人工智能技术正在快速发展", "Artificial Intelligence technology is rapidly developing"},
				{"industry_cn_2", "区块链应用前景广阔", "Blockchain applications have broad prospects"},
				{"industry_cn_3", "云计算服务越来越重要", "Cloud computing services are becoming increasingly important"},
				{"industry_cn_4", "大数据分析帮助企业决策", "Big data analysis helps enterprise decision-making"},
				{"industry_cn_5", "物联网连接万物互联", "Internet of Things connects everything"},
			},
		},
	}

	fmt.Println("\n📝 Inserting test documents...")
	docCount := 0
	for _, dataset := range testDatasets {
		fmt.Printf("\n--- %s ---\n", dataset.name)
		
		for i, docPair := range dataset.documents {
			// Insert Chinese version
			chineseVec := generateSemanticVector(docPair.chinese, 1024, "zh")
			chineseEmb := &sqvect.Embedding{
				ID:      fmt.Sprintf("%s_zh", docPair.id),
				Vector:  chineseVec,
				Content: docPair.chinese,
				Metadata: map[string]string{
					"lang":     "zh",
					"category": dataset.name,
					"pair_id":  docPair.id,
				},
			}
			
			// Insert English version
			englishVec := generateSemanticVector(docPair.english, 1024, "en")
			englishEmb := &sqvect.Embedding{
				ID:      fmt.Sprintf("%s_en", docPair.id),
				Vector:  englishVec,
				Content: docPair.english,
				Metadata: map[string]string{
					"lang":     "en",
					"category": dataset.name,
					"pair_id":  docPair.id,
				},
			}
			
			if err := store.Upsert(ctx, chineseEmb); err != nil {
				fmt.Printf("❌ Failed Chinese %d: %v\n", i+1, err)
			} else {
				fmt.Printf("✅ Chinese %d: %s...\n", i+1, docPair.chinese[:min(25, len(docPair.chinese))])
			}
			
			if err := store.Upsert(ctx, englishEmb); err != nil {
				fmt.Printf("❌ Failed English %d: %v\n", i+1, err)
			} else {
				fmt.Printf("✅ English %d: %s...\n", i+1, docPair.english[:min(25, len(docPair.english))])
			}
			
			docCount += 2
		}
	}

	fmt.Printf("\n📊 Total documents inserted: %d\n", docCount)

	// 跨语言测试查询
	crossLanguageTests := []TestQuery{
		// 品牌名跨语言匹配
		{"音书酒吧", "zh", "Should match Yinshu Bar in English", []string{"Yinshu", "Bar", "cocktails"}},
		{"Yinshu Bar", "en", "应该匹配中文音书酒吧", []string{"音书", "酒吧", "鸡尾酒"}},
		{"星巴克", "zh", "Should match Starbucks", []string{"Starbucks", "Coffee"}},
		{"McDonald's", "en", "应该匹配麦当劳", []string{"麦当劳", "快餐"}},
		
		// 地名跨语言匹配  
		{"北京", "zh", "Should match Beijing", []string{"Beijing", "capital"}},
		{"Shanghai", "en", "应该匹配上海", []string{"上海", "经济"}},
		{"深圳科技", "zh", "Should match Shenzhen technology", []string{"Shenzhen", "technology", "innovation"}},
		{"Guangzhou business", "en", "应该匹配广州商贸", []string{"广州", "商贸"}},
		
		// 技术术语匹配
		{"人工智能", "zh", "Should match AI", []string{"Artificial", "Intelligence"}},
		{"Blockchain technology", "en", "应该匹配区块链技术", []string{"区块链", "应用"}},
		{"云计算服务", "zh", "Should match cloud computing", []string{"Cloud", "computing", "services"}},
		{"Big Data", "en", "应该匹配大数据", []string{"大数据", "分析"}},
	}

	fmt.Println("\n🔍 Cross-Language Matching Tests...")
	
	successCount := 0
	for i, test := range crossLanguageTests {
		fmt.Printf("\n--- Test %d ---\n", i+1)
		fmt.Printf("Query: \"%s\" (%s)\n", test.query, test.language)
		fmt.Printf("Expected: %s\n", test.expected)
		
		queryVec := generateSemanticVector(test.query, 1024, test.language)
		
		results, err := store.Search(ctx, queryVec, sqvect.SearchOptions{TopK: 5})
		if err != nil {
			fmt.Printf("❌ Search failed: %v\n", err)
			continue
		}
		
		fmt.Printf("📊 Top Results:\n")
		for j, result := range results[:min(3, len(results))] {
			lang := result.Metadata["lang"]
			category := result.Metadata["category"]
			fmt.Printf("  %d. [%.4f] [%s] [%s] %s\n", 
				j+1, result.Score, lang, category, 
				truncate(result.Content, 45))
		}
		
		// 评估匹配质量
		if len(results) > 0 && evaluateMatch(results[0], test) {
			fmt.Printf("✅ Successful cross-language match!\n")
			successCount++
		} else {
			fmt.Printf("❌ Poor cross-language matching\n")
		}
	}

	// 同语言匹配测试
	fmt.Println("\n🔍 Same-Language Matching Tests...")
	
	sameLanguageTests := []TestQuery{
		// 中文同义词测试
		{"酒吧娱乐", "zh", "Should match Chinese bar content", []string{"音书酒吧", "舒适"}},
		{"咖啡店", "zh", "Should match coffee content", []string{"星巴克", "咖啡"}},
		{"快餐连锁", "zh", "Should match fast food", []string{"麦当劳", "快餐"}},
		
		// 英文同义词测试  
		{"bar entertainment", "en", "Should match bar content", []string{"Yinshu", "cocktails"}},
		{"coffee shop", "en", "Should match coffee content", []string{"Starbucks", "Coffee"}},
		{"fast food chain", "en", "Should match McDonald's", []string{"McDonald's", "fast", "food"}},
	}
	
	sameSuccessCount := 0
	for i, test := range sameLanguageTests {
		fmt.Printf("\n--- Same-Language Test %d ---\n", i+1)
		fmt.Printf("Query: \"%s\" (%s)\n", test.query, test.language)
		
		queryVec := generateSemanticVector(test.query, 1024, test.language)
		
		// 只搜索同语言文档
		results, err := store.SearchWithFilter(ctx, queryVec, 
			sqvect.SearchOptions{TopK: 3}, 
			map[string]interface{}{"lang": test.language})
		
		if err != nil {
			fmt.Printf("❌ Search failed: %v\n", err)
			continue
		}
		
		fmt.Printf("📊 Same-Language Results:\n")
		for j, result := range results {
			category := result.Metadata["category"]
			fmt.Printf("  %d. [%.4f] [%s] %s\n", 
				j+1, result.Score, category,
				truncate(result.Content, 40))
		}
		
		if len(results) > 0 && evaluateMatch(results[0], test) {
			fmt.Printf("✅ Good same-language match!\n")
			sameSuccessCount++
		} else {
			fmt.Printf("❌ Poor same-language matching\n")
		}
	}

	// 性能统计
	fmt.Printf("\n📈 Test Results Summary:\n")
	fmt.Printf("  📊 Cross-Language Tests: %d/%d successful (%.1f%%)\n", 
		successCount, len(crossLanguageTests), 
		float64(successCount)/float64(len(crossLanguageTests))*100)
	fmt.Printf("  📊 Same-Language Tests: %d/%d successful (%.1f%%)\n", 
		sameSuccessCount, len(sameLanguageTests),
		float64(sameSuccessCount)/float64(len(sameLanguageTests))*100)

	// Database stats
	stats, err := store.Stats(ctx)
	if err == nil {
		fmt.Printf("\n📊 Database Performance:\n")
		fmt.Printf("  - Documents: %d\n", stats.Count)
		fmt.Printf("  - Dimensions: %d\n", stats.Dimensions)
		fmt.Printf("  - Size: %d bytes\n", stats.Size)
		fmt.Printf("  - Avg bytes/doc: %.1f\n", float64(stats.Size)/float64(stats.Count))
	}

	fmt.Println("\n🎯 Key Findings:")
	if successCount < len(crossLanguageTests)/2 {
		fmt.Println("  ❌ Cross-language matching needs significant improvement")
		fmt.Println("  💡 Recommendation: Implement pinyin-based text similarity")
	} else {
		fmt.Println("  ✅ Cross-language matching shows promising results")
	}
	
	if sameSuccessCount >= len(sameLanguageTests)*3/4 {
		fmt.Println("  ✅ Same-language matching works well")
	} else {
		fmt.Println("  ⚠️ Same-language matching could be improved")
	}
	
	fmt.Println("  📋 Next steps: Add go-pinyin for Chinese-English phonetic matching")
}

type DocumentPair struct {
	id      string
	chinese string
	english string
}

type TestQuery struct {
	query     string
	language  string
	expected  string
	keywords  []string
}

// 改进的语义向量生成（模拟更好的嵌入）
func generateSemanticVector(text string, dim int, language string) []float32 {
	vector := make([]float32, dim)
	
	// 基础哈希
	hash := advancedHash(text)
	
	// 语言特征编码
	langBonus := uint32(0)
	if language == "zh" {
		langBonus = 123456
	} else if language == "en" {
		langBonus = 654321
	}
	
	// 语义特征提取（模拟）
	semanticFeatures := extractSemanticFeatures(text, language)
	
	for i := 0; i < dim; i++ {
		seed := hash + langBonus + semanticFeatures + uint32(i*17+31)
		
		// 添加一些"语义相关性"
		semantic := float32(0)
		for _, keyword := range getKeywords(text, language) {
			keywordHash := advancedHash(keyword)
			if (keywordHash+uint32(i))%100 < 20 { // 20% 概率影响
				semantic += float32((keywordHash%100)) / 500.0
			}
		}
		
		vector[i] = float32((seed%2000))/2000.0 - 0.5 + semantic
	}
	
	return normalizeVector(vector)
}

func advancedHash(text string) uint32 {
	hash := uint32(5381)
	for _, c := range text {
		hash = hash*33 + uint32(c)
	}
	return hash
}

func extractSemanticFeatures(text string, language string) uint32 {
	features := uint32(0)
	
	// 简单的"语义特征"提取
	keywords := getKeywords(text, language)
	for _, keyword := range keywords {
		features += advancedHash(keyword) % 10000
	}
	
	return features
}

func getKeywords(text string, language string) []string {
	// 超级简化的关键词提取
	if language == "zh" {
		keywords := []string{}
		// 中文关键词模拟
		chineseKeywords := map[string]bool{
			"酒吧": true, "咖啡": true, "技术": true, "公司": true,
			"北京": true, "上海": true, "深圳": true, "广州": true,
			"人工智能": true, "区块链": true, "云计算": true,
		}
		for word := range chineseKeywords {
			if contains(text, word) {
				keywords = append(keywords, word)
			}
		}
		return keywords
	} else {
		keywords := []string{}
		englishKeywords := map[string]bool{
			"Bar": true, "Coffee": true, "Technology": true, "Company": true,
			"Beijing": true, "Shanghai": true, "Shenzhen": true, "Guangzhou": true,
			"Intelligence": true, "Blockchain": true, "Cloud": true,
		}
		for word := range englishKeywords {
			if contains(text, word) {
				keywords = append(keywords, word)
			}
		}
		return keywords
	}
}

func evaluateMatch(result sqvect.ScoredEmbedding, test TestQuery) bool {
	// 简单的匹配评估
	if result.Score < 0.3 {
		return false
	}
	
	// 检查是否包含预期关键词
	content := result.Content
	matchCount := 0
	for _, keyword := range test.keywords {
		if contains(content, keyword) {
			matchCount++
		}
	}
	
	return matchCount > 0 || result.Score > 0.6
}

func contains(text, substr string) bool {
	return len(text) >= len(substr) && 
		(text == substr || 
		 len(text) > len(substr) && 
		 (text[:len(substr)] == substr || text[len(text)-len(substr):] == substr ||
		  findInString(text, substr)))
}

func findInString(text, substr string) bool {
	for i := 0; i <= len(text)-len(substr); i++ {
		if text[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func truncate(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "..."
}

func normalizeVector(vector []float32) []float32 {
	var norm float64
	for _, v := range vector {
		norm += float64(v * v)
	}
	if norm == 0 {
		return vector
	}
	
	norm = math.Sqrt(norm)
	for i := range vector {
		vector[i] = float32(float64(vector[i]) / norm)
	}
	return vector
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}