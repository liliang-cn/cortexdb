// Package main demonstrates the semantic-router usage.
// Note: This example requires the semantic-router package to be properly imported.
// Uncomment the imports when the module is published.
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
	semanticrouter "github.com/liliang-cn/sqvect/v2/pkg/semantic-router"
)

func main() {
	log.Println("Semantic Router Example")
	log.Println("======================")
	log.Println()
	log.Println("This example demonstrates the semantic-router usage.")
	log.Println()

	// Create a mock embedder (replace with real OpenAI embedder in production)
	embedder := semanticrouter.NewMockEmbedder(1536)

	// Create a new semantic router with custom threshold
	router, err := semanticrouter.NewRouter(
		embedder,
		semanticrouter.WithThreshold(0.82),
		semanticrouter.WithSimilarityFunc(core.CosineSimilarity),
	)
	if err != nil {
		log.Fatal(err)
	}

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

	if err := router.AddBatch(routes); err != nil {
		log.Fatal(err)
	}

	// Route a query
	ctx := context.Background()
	query := "我要退款"
	result, err := router.Route(ctx, query)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(strings.Repeat("-", 60))
	fmt.Printf("查询: %q\n", query)
	fmt.Printf("  匹配路由: %s\n", result.RouteName)
	fmt.Printf("  相似度: %.4f\n", result.Score)
	fmt.Printf("  是否命中: %v\n", result.Matched)

	if result.Matched && result.Handler != nil {
		response, err := result.Handler(ctx, query, result.Score)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  响应: %s\n", response)
	}
}
