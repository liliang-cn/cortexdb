package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

const (
	ex             = "https://example.com/"
	rdfsSubClassOf = "http://www.w3.org/2000/01/rdf-schema#subClassOf"
)

func main() {
	dbPath := "example_kg.db"
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := seedKG(ctx, db); err != nil {
		log.Fatal(err)
	}
	if err := runInference(ctx, db); err != nil {
		log.Fatal(err)
	}
	if err := runSPARQL(ctx, db); err != nil {
		log.Fatal(err)
	}
	if err := runSHACL(ctx, db); err != nil {
		log.Fatal(err)
	}
}

func seedKG(ctx context.Context, db *cortexdb.DB) error {
	_, err := db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{
		Triples: []cortexdb.KnowledgeGraphTriple{
			{Subject: graph.NewIRI(ex + "Manager"), Predicate: graph.NewIRI(rdfsSubClassOf), Object: graph.NewIRI(ex + "Employee")},
			{Subject: graph.NewIRI(ex + "alice"), Predicate: graph.NewIRI(graph.RDFType), Object: graph.NewIRI(ex + "Manager")},
			{Subject: graph.NewIRI(ex + "alice"), Predicate: graph.NewIRI(ex + "age"), Object: graph.NewTypedLiteral("34", graph.XSDNamespace+"integer")},
			{Subject: graph.NewIRI(ex + "alice"), Predicate: graph.NewIRI(ex + "knows"), Object: graph.NewIRI(ex + "bob")},
			{Subject: graph.NewIRI(ex + "bob"), Predicate: graph.NewIRI(graph.RDFType), Object: graph.NewIRI(ex + "Person")},
			{Subject: graph.NewIRI(ex + "bob"), Predicate: graph.NewIRI(ex + "age"), Object: graph.NewTypedLiteral("-5", graph.XSDNamespace+"integer")},
			{Subject: graph.NewIRI(ex + "bob"), Predicate: graph.NewIRI(ex + "knows"), Object: graph.NewIRI(ex + "carol")},
			{Subject: graph.NewIRI(ex + "carol"), Predicate: graph.NewIRI(graph.RDFType), Object: graph.NewIRI(ex + "Person")},
		},
	})
	return err
}

func runInference(ctx context.Context, db *cortexdb.DB) error {
	seed := cortexdb.KnowledgeGraphTriple{
		Subject:   graph.NewIRI(ex + "Employee"),
		Predicate: graph.NewIRI(rdfsSubClassOf),
		Object:    graph.NewIRI(ex + "Person"),
	}
	if _, err := db.UpsertKnowledgeGraph(ctx, cortexdb.KnowledgeGraphUpsertRequest{Triples: []cortexdb.KnowledgeGraphTriple{seed}}); err != nil {
		return err
	}
	refresh, err := db.RefreshKnowledgeGraphInference(ctx, cortexdb.KnowledgeGraphInferenceRefreshRequest{
		Mode:    cortexdb.KnowledgeGraphInferenceRefreshModeIncremental,
		Triples: []cortexdb.KnowledgeGraphTriple{seed},
	})
	if err != nil {
		return err
	}
	fmt.Printf("incremental inference: explicit=%d affected=%d inferred=%d\n", refresh.Result.ExplicitCount, refresh.Result.AffectedExplicitCount, refresh.Result.InferredCount)
	return nil
}

func runSPARQL(ctx context.Context, db *cortexdb.DB) error {
	result, err := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
		Query: `
PREFIX ex: <https://example.com/>
PREFIX rdf: <http://www.w3.org/1999/02/22-rdf-syntax-ns#>

SELECT ?person ?friend_count WHERE {
	?person rdf:type ex:Person .
	ex:alice ex:knows+ ?reachable .
	{
		SELECT ?person (COUNT(?friend) AS ?friend_count) WHERE {
			?person ex:knows ?friend .
		}
		GROUP BY ?person
	}
	FILTER EXISTS { ?person ex:age ?age . }
}
ORDER BY ?person
`,
	})
	if err != nil {
		return err
	}
	fmt.Println("\nSPARQL results:")
	for _, row := range result.Result.Bindings {
		fmt.Printf("- %s friend_count=%s\n", row["person"].Value, row["friend_count"].Value)
	}
	return nil
}

func runSHACL(ctx context.Context, db *cortexdb.DB) error {
	resp, err := db.ValidateKnowledgeGraphSHACL(ctx, cortexdb.KnowledgeGraphSHACLValidateRequest{
		Shapes: []cortexdb.KnowledgeGraphTriple{
			{Subject: graph.NewIRI(ex + "PersonShape"), Predicate: graph.NewIRI(graph.RDFType), Object: graph.NewIRI(graph.SHACLNodeShape)},
			{Subject: graph.NewIRI(ex + "PersonShape"), Predicate: graph.NewIRI(graph.SHACLTargetClass), Object: graph.NewIRI(ex + "Person")},
			{Subject: graph.NewIRI(ex + "PersonShape"), Predicate: graph.NewIRI(graph.SHACLProperty), Object: graph.NewIRI(ex + "AgeShape")},
			{Subject: graph.NewIRI(ex + "AgeShape"), Predicate: graph.NewIRI(graph.SHACLPath), Object: graph.NewIRI(ex + "age")},
			{Subject: graph.NewIRI(ex + "AgeShape"), Predicate: graph.NewIRI(graph.SHACLMinCount), Object: graph.NewLiteral("1")},
			{Subject: graph.NewIRI(ex + "AgeShape"), Predicate: graph.NewIRI(graph.SHACLMinInclusive), Object: graph.NewLiteral("0")},
		},
	})
	if err != nil {
		return err
	}
	fmt.Printf("\nSHACL conforms=%v violations=%d\n", resp.Report.Conforms, len(resp.Report.Results))
	for _, item := range resp.Report.Results {
		fmt.Printf("- focus=%s path=%s message=%s\n", item.FocusNode.Value, item.Path.Value, item.Message)
	}
	return nil
}
