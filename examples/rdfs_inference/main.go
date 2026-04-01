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
	dbPath := "rdfs_inference.db"
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	fmt.Println("=== RDFS Inference Example ===")

	_, err = db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{
		Triples: []cortexdb.KnowledgeGraphTriple{
			{
				Subject:   graph.NewIRI("https://example.com/Manager"),
				Predicate: graph.NewIRI("http://www.w3.org/2000/01/rdf-schema#subClassOf"),
				Object:    graph.NewIRI("https://example.com/Employee"),
			},
			{
				Subject:   graph.NewIRI("https://example.com/Employee"),
				Predicate: graph.NewIRI("http://www.w3.org/2000/01/rdf-schema#subClassOf"),
				Object:    graph.NewIRI("https://example.com/Person"),
			},
			{
				Subject:   graph.NewIRI("https://example.com/worksFor"),
				Predicate: graph.NewIRI("http://www.w3.org/2000/01/rdf-schema#domain"),
				Object:    graph.NewIRI("https://example.com/Employee"),
			},
			{
				Subject:   graph.NewIRI("https://example.com/alice"),
				Predicate: graph.NewIRI("http://www.w3.org/1999/02/22-rdf-syntax-ns#type"),
				Object:    graph.NewIRI("https://example.com/Manager"),
			},
			{
				Subject:   graph.NewIRI("https://example.com/alice"),
				Predicate: graph.NewIRI("https://example.com/worksFor"),
				Object:    graph.NewIRI("https://example.com/acme"),
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	refresh, err := db.RefreshKnowledgeGraphInference(ctx, cortexdb.KnowledgeGraphInferenceRefreshRequest{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Explicit=%d Inferred=%d\n", refresh.Result.ExplicitCount, refresh.Result.InferredCount)

	query, err := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
		Query: `
SELECT ?type WHERE {
	<https://example.com/alice> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> ?type .
}
ORDER BY ?type
`,
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("\nTypes for Alice:")
	var inferredTripleID string
	for _, row := range query.Result.Bindings {
		fmt.Printf("  %s\n", row["type"].Value)
	}

	found, err := db.FindKnowledgeGraph(ctx, cortexdb.KnowledgeGraphFindRequest{
		Pattern: cortexdb.KnowledgeGraphTriplePattern{
			Subject:   ptrTerm(graph.NewIRI("https://example.com/alice")),
			Predicate: ptrTerm(graph.NewIRI("http://www.w3.org/1999/02/22-rdf-syntax-ns#type")),
			Object:    ptrTerm(graph.NewIRI("https://example.com/Person")),
			Inferred:  ptrBool(true),
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	if len(found.Triples) > 0 {
		inferredTripleID = found.Triples[0].ID
	}

	if inferredTripleID != "" {
		explain, err := db.ExplainKnowledgeGraphInference(ctx, cortexdb.KnowledgeGraphInferenceExplainRequest{
			TripleID: inferredTripleID,
			Depth:    2,
		})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("\nExplanation rule: %s\n", explain.Explanation.Rule)
		fmt.Println("Trace:")
		for _, entry := range explain.Trace {
			fmt.Printf("  depth=%d triple=%s explicit=%v rule=%s\n",
				entry.Depth,
				entry.Explanation.Triple.ID,
				entry.Explanation.Explicit,
				entry.Explanation.Rule,
			)
		}
	}
}

func ptrTerm(term cortexdb.KnowledgeGraphTerm) *cortexdb.KnowledgeGraphTerm {
	return &term
}

func ptrBool(value bool) *bool {
	return &value
}
