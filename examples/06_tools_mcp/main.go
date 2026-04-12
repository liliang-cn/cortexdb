package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

func main() {
	dbPath := "example_tools_mcp.db"
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	tools := db.GraphRAGTools()

	names := make([]string, 0, len(tools.Definitions()))
	for _, def := range tools.Definitions() {
		names = append(names, def.Name)
	}
	sort.Strings(names)
	fmt.Printf("tool definitions=%d\n", len(names))
	for _, name := range names {
		if name == "knowledge_graph_query" || name == "knowledge_graph_shacl_validate" || name == "memory_save" || name == "knowledge_search" {
			fmt.Printf("- %s\n", name)
		}
	}

	upsertPayload, _ := json.Marshal(cortexdb.KnowledgeGraphUpsertRequest{
		Triples: []cortexdb.KnowledgeGraphTriple{
			{Subject: graph.NewIRI("https://example.com/alice"), Predicate: graph.NewIRI("https://schema.org/name"), Object: graph.NewLiteral("Alice")},
		},
	})
	if _, err := tools.Call(ctx, "knowledge_graph_upsert", upsertPayload); err != nil {
		log.Fatal(err)
	}

	queryPayload, _ := json.Marshal(cortexdb.KnowledgeGraphQueryRequest{
		Query: `
PREFIX schema: <https://schema.org/>
SELECT ?name WHERE {
	<https://example.com/alice> schema:name ?name .
}
`,
	})
	result, err := tools.Call(ctx, "knowledge_graph_query", queryPayload)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nknowledge_graph_query result: %+v\n", result)

	fmt.Println("\nThe same definitions are exposed through db.NewMCPServer(...).")
}
