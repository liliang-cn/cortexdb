// Example 21_history reads the history of a brain the way git log reads a
// repository: the versions of an entity's relations and which one is in effect,
// a deleted triple read as of an earlier instant, and the versions of an
// ontology schema. No embedder and no model are involved.
//
//	go run ./examples/21_history
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func main() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "cortexdb-history-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "history.db")))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// A schema in which a person lives in one city. Saving it again later adds
	// a version; the first one stays readable.
	str := cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}
	people := cortexdb.OntologySchema{
		SchemaID: "people",
		Name:     "People",
		ObjectTypes: []cortexdb.OntologyObjectType{
			{APIName: "Person", PrimaryKey: "personId", Properties: []cortexdb.OntologyProperty{{APIName: "personId", DataType: str, Required: true}}},
			{APIName: "City", PrimaryKey: "cityId", Properties: []cortexdb.OntologyProperty{{APIName: "cityId", DataType: str, Required: true}}},
		},
		LinkTypes: []cortexdb.OntologyLinkType{{
			APIName: "lives_in",
			A:       cortexdb.OntologyLinkSide{APIName: "city", ObjectTypeAPIName: "Person", Cardinality: cortexdb.OntologyCardinalityOne},
			B:       cortexdb.OntologyLinkSide{APIName: "residents", ObjectTypeAPIName: "City", Cardinality: cortexdb.OntologyCardinalityMany},
		}},
	}
	must(db.SaveOntologySchema(ctx, cortexdb.OntologySaveRequest{Schema: people, Activate: true}))
	people.ObjectTypes[0].Properties = append(people.ObjectTypes[0].Properties, cortexdb.OntologyProperty{APIName: "nickname", DataType: str})
	must(db.SaveOntologySchema(ctx, cortexdb.OntologySaveRequest{Schema: people}))

	tools := db.GraphRAGTools()
	entity := func(name, typ, pk string) cortexdb.ToolEntityInput {
		return cortexdb.ToolEntityInput{Name: name, Type: typ, Metadata: map[string]string{pk: name}}
	}
	must(tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: []cortexdb.ToolEntityInput{
		entity("Leo", "Person", "personId"), entity("Beijing", "City", "cityId"), entity("Chengdu", "City", "cityId"),
	}}))
	relate := func(to string) {
		must(tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{
			Relations: []cortexdb.ToolRelationInput{{From: "Leo", To: to, Type: "lives_in"}},
		}))
	}
	relate("Beijing")
	time.Sleep(20 * time.Millisecond)
	relate("Chengdu")

	// The log of Leo: one line, two versions, and why Chengdu is the one in effect.
	h, err := db.GraphHistory(ctx, cortexdb.GraphHistoryRequest{Entity: "Leo", Relation: "lives_in"})
	if err != nil {
		log.Fatal(err)
	}
	for _, line := range h.Lines {
		fmt.Printf("%s -%s-> (%d versions)\n", line.SubjectName, line.Predicate, len(line.Versions))
		for _, v := range line.Versions {
			fmt.Printf("  %-10s %-11s", v.ObjectName, v.Status)
			if v.InEffect != nil {
				fmt.Printf(" in effect: %s", v.InEffect.Summary)
			}
			fmt.Println()
		}
	}

	// A triple that was deleted is still readable as of an instant before.
	const iri = "<urn:leo> <urn:city> <urn:beijing>"
	must(db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{Query: "INSERT DATA { " + iri + " }"}))
	time.Sleep(20 * time.Millisecond)
	then := time.Now().UTC().Format(time.RFC3339Nano)
	time.Sleep(20 * time.Millisecond)
	must(db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{Query: "DELETE DATA { " + iri + " }"}))
	ask := "SELECT ?o WHERE { <urn:leo> <urn:city> ?o }"
	now, _ := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{Query: ask})
	past, _ := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{Query: ask, AsOf: then})
	fmt.Printf("\ntriples now: %d, as of %s: %d\n", now.Result.Count, then, past.Result.Count)

	// The ontology: every saved version and when each was active.
	oh, err := db.OntologyHistory(ctx, cortexdb.OntologyHistoryRequest{SchemaID: "people"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println()
	for _, v := range oh.Versions {
		changes := 0
		if v.Summary != nil {
			changes = v.Summary.Changes
		}
		fmt.Printf("people v%d saved %s, active now: %v, %d change(s)\n",
			v.Version, v.SavedAt.Format(time.RFC3339), v.Active, changes)
	}
	d, err := db.DiffOntologySchema(ctx, cortexdb.OntologyDiffRequest{SchemaID: "people", FromVersion: 1, ToVersion: 2})
	if err != nil {
		log.Fatal(err)
	}
	for _, c := range d.Diff.Changes {
		fmt.Printf("v1 -> v2: %s %s\n", c.Kind, c.Target)
	}
}

// must stops on an error and drops the value, which the example does not read.
func must[T any](_ T, err error) {
	if err != nil {
		log.Fatal(err)
	}
}
