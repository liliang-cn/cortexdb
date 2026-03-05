package main

import (
	"context"
	"fmt"
	"log"
	"math"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
	"github.com/liliang-cn/sqvect/v2/pkg/graph"
	"github.com/liliang-cn/sqvect/v2/pkg/hindsight"
	"github.com/liliang-cn/sqvect/v2/pkg/sqvect"
)

// DummyEmbedder for demonstration
type DummyEmbedder struct {
	dim int
}

func (d *DummyEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vec := make([]float32, d.dim)
	for i := 0; i < d.dim; i++ {
		if len(text) > 0 {
			vec[i] = float32(text[i%len(text)]) / 255.0
		} else {
			vec[i] = 0.0
		}
	}
	var sum float32
	for _, v := range vec {
		sum += v * v
	}
	if sum > 0 {
		norm := float32(math.Sqrt(float64(sum)))
		for i := range vec {
			vec[i] /= norm
		}
	}
	return vec, nil
}
func (d *DummyEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, nil // Not used in this example
}
func (d *DummyEmbedder) Dim() int { return d.dim }

func main() {
	fmt.Println("=== Working with Structured Data (SQL/CSV) in sqvect ===")
	ctx := context.Background()
	embedder := &DummyEmbedder{dim: 128}

	// Strategy 1: Textification & Advanced Filtering (RAG Context)
	demoTextification(ctx, embedder)

	// Strategy 2: Agent Memory with SQL Entities
	demoHindsightMemory(ctx, embedder)

	// Strategy 3: Relational Data to Knowledge Graph
	demoGraphRAG(ctx, embedder)
}

// ---------------------------------------------------------
// Strategy 1: Flattening CSV/SQL rows into text + Metadata
// ---------------------------------------------------------
func demoTextification(ctx context.Context, embedder sqvect.Embedder) {
	fmt.Println("\n--- Strategy 1: Textification + Advanced Filtering ---")
	db, err := sqvect.Open(sqvect.DefaultConfig("structured_rag.db"), sqvect.WithEmbedder(embedder))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// 1. Simulate reading rows from a CSV or SQL Database
	type Product struct {
		ID       string
		Name     string
		Category string
		Price    float64
		Stock    int
	}

	products := []Product{
		{"p1", "MacBook Pro M3", "laptop", 1999.99, 15},
		{"p2", "iPhone 15 Pro", "phone", 999.99, 50},
		{"p3", "Pixel 8", "phone", 799.99, 0}, // out of stock
		{"p4", "Dell XPS 15", "laptop", 1599.99, 5},
	}

	// 2. Insert as Text + Metadata
	fmt.Println("Indexing products...")
	for _, p := range products {
		// Convert structured row to natural language for the AI/Vector engine
		content := fmt.Sprintf("Product %s is a %s. It costs $%.2f. Current stock: %d units.", p.Name, p.Category, p.Price, p.Stock)
		
		// Store exact values in metadata for strict SQL-like filtering
		err := db.InsertText(ctx, p.ID, content, map[string]string{
			"name":     p.Name,
			"category": p.Category,
			"price":    fmt.Sprintf("%f", p.Price), // Metadata is string-based, filter handles parsing
			"stock":    fmt.Sprintf("%d", p.Stock),
		})
		if err != nil {
			log.Printf("Failed to insert %s: %v", p.ID, err)
		}
	}

	// 3. Search: Find "powerful computer" but STRICTLY require in-stock laptops under $2000
	query := "I need a powerful computer for programming"
	queryVec, _ := embedder.Embed(ctx, query)

	// Build an advanced SQL-like filter
	filter := core.NewMetadataFilter().
		And(core.NewMetadataFilter().Equal("category", "laptop")).
		And(core.NewMetadataFilter().LessThan("price", 2000.0)).
		And(core.NewMetadataFilter().GreaterThan("stock", 0)).
		Build()

	opts := core.AdvancedSearchOptions{
		SearchOptions: core.SearchOptions{TopK: 2},
		PreFilter:     filter, // Filters happen BEFORE vector math!
	}

	results, _ := db.Vector().SearchWithAdvancedFilter(ctx, queryVec, opts)
	
	fmt.Println("Search Results (Filtered: Laptop, In-stock, <$2000):")
	for _, res := range results {
		fmt.Printf("- [%s] %s (Price: %s, Stock: %s)\n", res.ID, res.Metadata["name"], res.Metadata["price"], res.Metadata["stock"])
	}
}

// ---------------------------------------------------------
// Strategy 2: Using Hindsight for CRM / Agent State
// ---------------------------------------------------------
func demoHindsightMemory(ctx context.Context, embedder sqvect.Embedder) {
	fmt.Println("\n--- Strategy 2: Agent Memory with SQL Entities ---")
	sys, err := hindsight.New(&hindsight.Config{
		DBPath:    "structured_memory.db",
		VectorDim: 128,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer sys.Close()

	sys.CreateBank(ctx, hindsight.NewBank("sales_agent_01", "Sales Assistant"))

	// Simulate a wide SQL record from a CRM system
	vec, _ := embedder.Embed(ctx, "Customer Alice from TechCorp is interested in AI solutions.")
	
	mem := &hindsight.Memory{
		ID:       "crm_record_1024",
		BankID:   "sales_agent_01",
		Type:     hindsight.WorldMemory,
		Content:  "Customer Alice from TechCorp is interested in AI solutions.",
		Vector:   vec,
		// Map SQL Foreign Keys to Hindsight Entities for the 'E' in TEMPR recall
		Entities: []string{"user:alice", "company:techcorp", "status:lead"},
		// Store the raw JSON/SQL payload here
		Metadata: map[string]any{
			"sql_id":      1024,
			"revenue_ytd": 50000.0,
			"is_vip":      true,
			"tags":        []string{"ai", "enterprise"},
		},
	}
	sys.Retain(ctx, mem)

	// Recall strictly by Entity (like a SQL JOIN / Foreign Key lookup)
	req := &hindsight.RecallRequest{
		BankID: "sales_agent_01",
		Strategy: &hindsight.RecallStrategy{
			Entity: []string{"company:techcorp"}, // Only fetch memories linked to this specific foreign key
		},
	}
	results, _ := sys.Recall(ctx, req)
	
	fmt.Println("Recalled CRM Memories for 'company:techcorp':")
	for _, res := range results {
		fmt.Printf("- %s (SQL ID: %v, VIP: %v)\n", res.Memory.Content, res.Memory.Metadata["sql_id"], res.Memory.Metadata["is_vip"])
	}
}

// ---------------------------------------------------------
// Strategy 3: Relational Data to Knowledge Graph (GraphRAG)
// ---------------------------------------------------------
func demoGraphRAG(ctx context.Context, embedder sqvect.Embedder) {
	fmt.Println("\n--- Strategy 3: Relational Data to Graph (GraphRAG) ---")
	db, err := sqvect.Open(sqvect.DefaultConfig("structured_graph.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	db.Graph().InitGraphSchema(ctx)

	// Simulate SQL Tables: Employee (id, name, dept_id) and Department (id, name)
	
	// 1. Convert SQL Rows to Graph Nodes
	deptVec, _ := embedder.Embed(ctx, "Engineering Department handles core product development")
	db.Graph().UpsertNode(ctx, &graph.GraphNode{
		ID:       "dept_eng",
		NodeType: "department",
		Content:  "Engineering Department handles core product development",
		Vector:   deptVec,
	})

	empVec, _ := embedder.Embed(ctx, "Bob is a Senior Go Developer")
	db.Graph().UpsertNode(ctx, &graph.GraphNode{
		ID:       "emp_bob",
		NodeType: "employee",
		Content:  "Bob is a Senior Go Developer",
		Vector:   empVec,
	})

	// 2. Convert SQL Foreign Keys (emp_bob.dept_id = 'dept_eng') to Graph Edges
	db.Graph().UpsertEdge(ctx, &graph.GraphEdge{
		FromNodeID: "emp_bob",
		ToNodeID:   "dept_eng",
		EdgeType:   "BELONGS_TO",
		Weight:     1.0,
	})

	fmt.Println("Constructed Graph: Bob [BELONGS_TO] -> Engineering")

	// 3. Multi-hop Retrieval (Find Bob, then find his department automatically)
	// In standard RAG, finding Bob's department might require two LLM calls or complex SQL JOINs.
	// In GraphRAG, the database traverses the relationships for you.
	neighbors, _ := db.Graph().Neighbors(ctx, "emp_bob", graph.TraversalOptions{
		EdgeTypes: []string{"BELONGS_TO"},
		MaxDepth:  1,
	})

	for _, neighbor := range neighbors {
		fmt.Printf("Graph Traversal Result: Bob's department is [%s] -> %s\n", neighbor.ID, neighbor.Content)
	}
}
