// Package main demonstrates the semantic-router usage.
// Note: This example requires the semantic-router package to be properly imported.
// Uncomment the imports when the module is published.
package main

import (
	"fmt"
	"log"
	"strings"

	// Uncomment when package is available:
	// "github.com/liliang-cn/sqvect/v2/pkg/core"
	// "github.com/liliang-cn/sqvect/v2/pkg/semanticrouter"
)

func main() {
	log.Println("Semantic Router Example")
	log.Println("======================")
	log.Println()
	log.Println("This example demonstrates the semantic-router usage.")
	log.Println("Uncomment the imports and code below when the package is available.")
	log.Println()
	log.Println("Example usage:")
	log.Println(`
	// Create a mock embedder (replace with real OpenAI embedder in production)
	embedder := semanticrouter.NewMockEmbedder(1536)

	// Create a new semantic router with custom threshold
	router, err := semanticrouter.NewRouter(
		embedder,
		semanticrouter.WithThreshold(0.82),
		semanticrouter.WithSimilarityFunc(core.CosineSimilarity),
	)

	// Define routes for different intents
	refundHandler := func(ctx context.Context, query string, score float64) (string, error) {
		return fmt.Sprintf("[退款处理] 您的退款请求已收到（置信度: %.2f）", score), nil
	}

	// Add routes with example utterances
	routes := []*semanticrouter.Route{
		{
			Name: "refund",
			Utterances: []string{
				"我要退款",
				"这东西坏了",
				"把钱还我",
				"申请退款",
				"退货",
			},
			Handler: refundHandler,
		},
		{
			Name: "chat",
			Utterances: []string{
				"你好",
				"在吗",
				"最近怎么样",
			},
		},
	}

	router.AddBatch(routes)

	// Route a query
	ctx := context.Background()
	result, _ := router.Route(ctx, "我要退款")
	fmt.Printf("Route: %s, Score: %.4f, Matched: %v\n",
		result.RouteName, result.Score, result.Matched)
	`)

	// Sample output simulation
	fmt.Println("\nSample output:")
	fmt.Println(strings.Repeat("-", 60))
	fmt.Println("已加载 2 个路由:")
	fmt.Println("  - refund")
	fmt.Println("  - chat")
	fmt.Println()
	fmt.Println("查询: \"我要退款\"")
	fmt.Println("  匹配路由: refund")
	fmt.Println("  相似度: 1.0000")
	fmt.Println("  是否命中: true")
	fmt.Println("  响应: [退款处理] 您的退款请求已收到（置信度: 1.00）")
}
