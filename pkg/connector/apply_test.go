package connector

import (
	"context"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

func TestSingleRowSource(t *testing.T) {
	rec := importflow.Record{Table: "users", Values: map[string]string{"id": "1", "name": "Bob"}}
	src := singleRowSource("users", []importflow.Column{{Name: "id"}, {Name: "name"}}, rec)
	schemas, err := src.Schemas(context.Background())
	if err != nil || len(schemas) != 1 || schemas[0].Table != "users" {
		t.Fatalf("schemas: %+v %v", schemas, err)
	}
	var got []importflow.Record
	_ = src.Records(context.Background(), func(r importflow.Record) error { got = append(got, r); return nil })
	if len(got) != 1 || got[0].Values["name"] != "Bob" {
		t.Fatalf("records: %+v", got)
	}
}

func TestRAGChunkID(t *testing.T) {
	rag := &importflow.RAGPlan{IDColumn: "id"}
	if id := ragChunkID("users", rag, map[string]string{"id": "42"}); id != "42" {
		t.Fatalf("rag id = %q want 42", id)
	}
}

func TestEntityIRIsForKey(t *testing.T) {
	kg := &importflow.KGPlan{Entities: []importflow.EntityMap{
		{Ref: "customer", Type: "Customer", IDTmpl: "{id}"},
		{Ref: "product", Type: "Product", IDTmpl: "{product_id}"}, // not built from key -> skipped
	}}
	iris := entityIRIsForKey(kg, map[string]string{"id": "42"})
	if len(iris) != 1 || iris[0] != "urn:cortexdb:Customer:42" {
		t.Fatalf("iris = %+v", iris)
	}
}
