package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

func main() {
	dbPath := "rdf_knowledge_graph.db"
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	fmt.Println("=== RDF Knowledge Graph Example ===")

	if _, err := db.UpsertKnowledgeNamespace(ctx, cortexdb.KnowledgeGraphNamespaceUpsertRequest{
		Prefix: "ex",
		URI:    "https://example.com/",
	}); err != nil {
		log.Fatal(err)
	}

	upsert, err := db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{
		Triples: []cortexdb.KnowledgeGraphTriple{
			{
				Subject:   graph.NewIRI("ex:alice"),
				Predicate: graph.NewIRI("rdf:type"),
				Object:    graph.NewIRI("schema:Person"),
			},
			{
				Subject:   graph.NewIRI("ex:alice"),
				Predicate: graph.NewIRI("schema:name"),
				Object:    graph.NewLangLiteral("Alice", "en"),
			},
			{
				Subject:   graph.NewIRI("ex:alice"),
				Predicate: graph.NewIRI("schema:worksFor"),
				Object:    graph.NewIRI("ex:acme"),
			},
			{
				Subject:   graph.NewIRI("ex:acme"),
				Predicate: graph.NewIRI("schema:name"),
				Object:    graph.NewLiteral("Acme Inc."),
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Inserted %d triples\n", upsert.Count)

	query, err := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
		Query: `
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

SELECT ?company_name WHERE {
	ex:alice schema:worksFor ?company .
	?company schema:name ?company_name .
}
`,
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("\nSPARQL query result:")
	for _, row := range query.Result.Bindings {
		fmt.Printf("  company_name=%s\n", row["company_name"].Value)
	}

	exported, err := db.ExportKnowledgeGraph(ctx, cortexdb.KnowledgeGraphExportRequest{
		Format: cortexdb.KnowledgeGraphFormatTriG,
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("\nTriG export:")
	fmt.Print(exported.Content)
}
