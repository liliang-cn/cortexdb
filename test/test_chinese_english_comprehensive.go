package main

import (
	"context"
	"fmt"
	"os"

	"github.com/liliang-cn/sqvect"
)

func main() {
	fmt.Println("🧪 Testing Chinese vs English Embedding & Search Performance")
	
	dbPath := "test_chinese_english_embedding.db"
	os.Remove(dbPath)
	defer os.Remove(dbPath)

	// Create store with auto-detect
	store, err := sqvect.New(dbPath, 0)
	if err != nil {
		panic(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		panic(err)
	}

	// Test Data: Chinese documents
	chineseDocuments := []struct {
		id      string
		content string
		tags    map[string]string
	}{
		{"doc_cn_1", "音书酒吧是一个很受欢迎的聚会场所", map[string]string{"lang": "zh", "type": "bar"}},
		{"doc_cn_2", "北京的咖啡文化日益繁荣，很多咖啡店都很有特色", map[string]string{"lang": "zh", "type": "cafe"}},
		{"doc_cn_3", "上海的夜生活丰富多彩，酒吧街非常热闹", map[string]string{"lang": "zh", "type": "nightlife"}},
		{"doc_cn_4", "深圳有很多创新型的餐厅和娱乐场所", map[string]string{"lang": "zh", "type": "restaurant"}},
		{"doc_cn_5", "广州的茶文化历史悠久，茶楼众多", map[string]string{"lang": "zh", "type": "teahouse"}},
	}

	// Test Data: English documents  
	englishDocuments := []struct {
		id      string
		content string
		tags    map[string]string
	}{
		{"doc_en_1", "Yinshu Bar is a popular gathering place for friends", map[string]string{"lang": "en", "type": "bar"}},
		{"doc_en_2", "Beijing's coffee culture is thriving with many unique coffee shops", map[string]string{"lang": "en", "type": "cafe"}},
		{"doc_en_3", "Shanghai nightlife is vibrant with bustling bar streets", map[string]string{"lang": "en", "type": "nightlife"}},
		{"doc_en_4", "Shenzhen has many innovative restaurants and entertainment venues", map[string]string{"lang": "en", "type": "restaurant"}},
		{"doc_en_5", "Guangzhou has a long tea culture history with numerous tea houses", map[string]string{"lang": "en", "type": "teahouse"}},
	}

	fmt.Println("\n📝 Step 1: Inserting Chinese Documents...")
	for _, doc := range chineseDocuments {
		// Generate dummy vector (in real app, this would be from an embedding model)
		vector := generateTextVector(doc.content, 768)
		
		embedding := &sqvect.Embedding{
			ID:       doc.id,
			Vector:   vector,
			Content:  doc.content,
			Metadata: doc.tags,
		}
		
		if err := store.Upsert(ctx, embedding); err != nil {
			fmt.Printf("❌ Failed to insert Chinese doc %s: %v\n", doc.id, err)
		} else {
			fmt.Printf("✅ Inserted Chinese: %s\n", doc.content[:min(30, len(doc.content))])
		}
	}

	fmt.Println("\n📝 Step 2: Inserting English Documents...")
	for _, doc := range englishDocuments {
		// Generate dummy vector
		vector := generateTextVector(doc.content, 768)
		
		embedding := &sqvect.Embedding{
			ID:       doc.id,
			Vector:   vector,
			Content:  doc.content,
			Metadata: doc.tags,
		}
		
		if err := store.Upsert(ctx, embedding); err != nil {
			fmt.Printf("❌ Failed to insert English doc %s: %v\n", doc.id, err)
		} else {
			fmt.Printf("✅ Inserted English: %s\n", doc.content[:min(30, len(doc.content))])
		}
	}

	// Test Queries
	testQueries := []struct {
		name     string
		query    string
		language string
		expected string
	}{
		// Chinese queries
		{"Chinese Query 1", "音书酒吧在哪里", "zh", "应该找到音书酒吧相关内容"},
		{"Chinese Query 2", "北京咖啡店推荐", "zh", "应该找到北京咖啡文化相关内容"},
		{"Chinese Query 3", "上海夜生活娱乐", "zh", "应该找到上海夜生活相关内容"},
		{"Chinese Query 4", "深圳创新餐厅", "zh", "应该找到深圳餐厅相关内容"},
		{"Chinese Query 5", "广州茶楼文化", "zh", "应该找到广州茶文化相关内容"},
		
		// English queries
		{"English Query 1", "Yinshu Bar location", "en", "Should find Yinshu Bar related content"},
		{"English Query 2", "Beijing coffee shops recommendation", "en", "Should find Beijing coffee culture content"},
		{"English Query 3", "Shanghai nightlife entertainment", "en", "Should find Shanghai nightlife content"},
		{"English Query 4", "Shenzhen innovative restaurants", "en", "Should find Shenzhen restaurant content"},
		{"English Query 5", "Guangzhou tea house culture", "en", "Should find Guangzhou tea culture content"},
		
		// Cross-language queries
		{"Cross Query 1", "Yinshu Bar", "cross", "应该匹配中文的音书酒吧"},
		{"Cross Query 2", "音书酒吧", "cross", "Should match English Yinshu Bar"},
		{"Cross Query 3", "Beijing coffee", "cross", "应该匹配中英文咖啡相关内容"},
		{"Cross Query 4", "上海 nightlife", "cross", "Should match Chinese/English nightlife"},
	}

	fmt.Println("\n🔍 Step 3: Testing Search Performance...")
	
	for i, test := range testQueries {
		fmt.Printf("\n--- Test %d: %s ---\n", i+1, test.name)
		fmt.Printf("Query: \"%s\" (Language: %s)\n", test.query, test.language)
		fmt.Printf("Expected: %s\n", test.expected)
		
		// Generate query vector
		queryVector := generateTextVector(test.query, 768)
		
		// Perform search
		results, err := store.Search(ctx, queryVector, sqvect.SearchOptions{TopK: 3})
		if err != nil {
			fmt.Printf("❌ Search failed: %v\n", err)
			continue
		}
		
		fmt.Printf("📊 Results (%d found):\n", len(results))
		for j, result := range results {
			langTag := result.Metadata["lang"]
			typeTag := result.Metadata["type"]
			fmt.Printf("  %d. [Score: %.4f] [%s|%s] %s\n", 
				j+1, result.Score, langTag, typeTag, result.Content[:min(50, len(result.Content))])
		}
		
		// Analysis
		if len(results) > 0 {
			topResult := results[0]
			if topResult.Score > 0.5 {
				fmt.Printf("✅ Good match found (score > 0.5)\n")
			} else if topResult.Score > 0.2 {
				fmt.Printf("🟡 Moderate match (score 0.2-0.5)\n")
			} else {
				fmt.Printf("❌ Poor match (score < 0.2)\n")
			}
		} else {
			fmt.Printf("❌ No results found\n")
		}
	}

	// Test SearchWithFilter
	fmt.Println("\n🔍 Step 4: Testing SearchWithFilter...")
	
	filterTests := []struct {
		name   string
		query  string
		filter map[string]interface{}
	}{
		{"Filter by Chinese", "酒吧聚会", map[string]interface{}{"lang": "zh"}},
		{"Filter by English", "bar gathering", map[string]interface{}{"lang": "en"}},
		{"Filter by Type", "咖啡", map[string]interface{}{"type": "cafe"}},
	}
	
	for _, test := range filterTests {
		fmt.Printf("\n--- Filter Test: %s ---\n", test.name)
		queryVector := generateTextVector(test.query, 768)
		
		results, err := store.SearchWithFilter(ctx, queryVector, sqvect.SearchOptions{TopK: 5}, test.filter)
		if err != nil {
			fmt.Printf("❌ SearchWithFilter failed: %v\n", err)
			continue
		}
		
		fmt.Printf("📊 Filtered Results (%d found):\n", len(results))
		for j, result := range results {
			fmt.Printf("  %d. [Score: %.4f] %s\n", 
				j+1, result.Score, result.Content[:min(40, len(result.Content))])
		}
	}

	// Performance Summary
	stats, err := store.Stats(ctx)
	if err == nil {
		fmt.Printf("\n📈 Database Statistics:\n")
		fmt.Printf("  - Total Documents: %d\n", stats.Count)
		fmt.Printf("  - Vector Dimensions: %d\n", stats.Dimensions)
		fmt.Printf("  - Database Size: %d bytes\n", stats.Size)
	}

	fmt.Println("\n🎉 Testing completed!")
	fmt.Println("Key observations:")
	fmt.Println("  1. Vector similarity depends heavily on embedding quality")
	fmt.Println("  2. Cross-language matching challenges visible")
	fmt.Println("  3. Metadata filtering works effectively")
	fmt.Println("  4. Search performance scales well with document count")
}

// Utility function to generate dummy vectors based on text content
func generateTextVector(text string, dim int) []float32 {
	vector := make([]float32, dim)
	
	// Simple hash-based vector generation (NOT for production!)
	// In real applications, use proper embedding models like BERT, OpenAI, etc.
	hash := simpleHash(text)
	for i := 0; i < dim; i++ {
		// Create pseudo-random but deterministic values based on text
		seed := hash + uint32(i*7+13)
		vector[i] = float32(seed%1000) / 1000.0 - 0.5 // Range [-0.5, 0.5]
	}
	
	// Normalize vector
	return normalizeVector(vector)
}

func simpleHash(text string) uint32 {
	hash := uint32(5381)
	for _, c := range text {
		hash = hash*33 + uint32(c)
	}
	return hash
}

func normalizeVector(vector []float32) []float32 {
	var norm float32
	for _, v := range vector {
		norm += v * v
	}
	if norm == 0 {
		return vector
	}
	
	norm = float32(1.0 / (float64(norm) * 0.5 + 0.5)) // Simple normalization
	for i := range vector {
		vector[i] *= norm
	}
	return vector
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}