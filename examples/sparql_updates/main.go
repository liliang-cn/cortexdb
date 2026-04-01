package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func main() {
	dbPath := "sparql_updates.db"
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	fmt.Println("=== SPARQL Updates And Aggregates Example ===")

	for _, query := range []string{
		`
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

INSERT DATA {
	ex:alice schema:score 10 .
	ex:bob schema:score 20 .
	ex:bob schema:score 30 .
}
`,
		`
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

INSERT {
	ex:alice schema:label ?label .
}
WHERE {
	VALUES ?label { "Alice" }
}
`,
		`
PREFIX ex: <https://example.com/>
PREFIX schema: <https://schema.org/>

DELETE {
	ex:bob schema:score ?old .
}
INSERT {
	ex:bob schema:score 99 .
}
WHERE {
	ex:bob schema:score ?old .
	FILTER(?old > 25)
}
`,
	} {
		resp, err := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{Query: query})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Applied query type=%s count=%d\n", resp.Result.QueryType, resp.Result.Count)
	}

	aggregate, err := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
		Query: `
PREFIX schema: <https://schema.org/>

SELECT
	(SUM(?score) AS ?sum)
	(AVG(?score) AS ?avg)
	(MAX(?score) AS ?max)
	(GROUP_CONCAT(?score; SEPARATOR=",") AS ?scores)
WHERE {
	?person schema:score ?score .
}
`,
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("\nAggregate result:")
	for _, row := range aggregate.Result.Bindings {
		fmt.Printf("  sum=%s avg=%s max=%s scores=%s\n",
			row["sum"].Value,
			row["avg"].Value,
			row["max"].Value,
			row["scores"].Value,
		)
	}
}
