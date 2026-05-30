package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

func main() {
	ctx := context.Background()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(".", "importflow_demo.db")))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	csv := "id,name,bio\n1,Ada,first programmer\n2,Alan,enigma codebreaker\n"
	src, err := importflow.NewCSVSource(strings.NewReader(csv), importflow.CSVOptions{Table: "people"})
	if err != nil {
		log.Fatal(err)
	}
	defer src.Close()

	plan := importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
		"people": {
			RAG: &importflow.RAGPlan{ContentTmpl: "{name}: {bio}", IDColumn: "id"},
			KG:  &importflow.KGPlan{Entities: []importflow.EntityMap{{Ref: "p", Type: "Person", IDTmpl: "{id}", LabelTmpl: "{name}"}}},
		},
	}}

	rep, err := importflow.New(db).Run(ctx, src, plan)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("rows=%d chunks=%d triples=%d unparsed=%d\n",
		rep.RowsRead, rep.ChunksIndexed, rep.TriplesCreated, len(rep.UnparsedStatements))

	// With a real graphflow.JSONGenerator you could instead call:
	//   im := importflow.New(db, importflow.WithMappingInferer(importflow.LLMInferer{Client: yourLLM}))
	//   rep, _ := im.AutoImport(ctx, src, importflow.Goal{BuildRAG: true, BuildKG: true})
}
