package importflow

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func TestDDLToKnowledgeGraphE2E(t *testing.T) {
	ctx := context.Background()
	ddl := `
CREATE TABLE customers (id INT PRIMARY KEY, name TEXT, city TEXT);
CREATE TABLE orders (id INT PRIMARY KEY, customer_id INT REFERENCES customers(id), product TEXT);
`
	plan, _, err := MappingFromDDL(ddl, DDLMappingOptions{})
	if err != nil {
		t.Fatal(err)
	}

	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "kb.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	custCSV := "id,name,city\n1,Alice,Chengdu\n"
	custSrc, _ := NewCSVSource(strings.NewReader(custCSV), CSVOptions{Table: "customers"})
	if _, err := New(db).Run(ctx, custSrc, MappingPlan{Tables: map[string]TablePlan{"customers": plan.Tables["customers"]}}); err != nil {
		t.Fatalf("import customers: %v", err)
	}
	custSrc.Close()

	ordCSV := "id,customer_id,product\n100,1,Pro Plan\n"
	ordSrc, _ := NewCSVSource(strings.NewReader(ordCSV), CSVOptions{Table: "orders"})
	if _, err := New(db).Run(ctx, ordSrc, MappingPlan{Tables: map[string]TablePlan{"orders": plan.Tables["orders"]}}); err != nil {
		t.Fatalf("import orders: %v", err)
	}
	ordSrc.Close()

	q := `ASK { <urn:cortexdb:Order:100> <urn:cortexdb:rel:customer> <urn:cortexdb:Customer:1> . }`
	res, err := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{Query: q})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Result.Boolean {
		t.Fatalf("expected the FK relation edge to exist; ASK was false (result: %+v)", res.Result)
	}
}
