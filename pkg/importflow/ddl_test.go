package importflow

import "testing"

func TestParseDDL(t *testing.T) {
	ddl := `
CREATE TABLE customers (
  id INT PRIMARY KEY,
  name TEXT,
  city VARCHAR(64)
);
CREATE TABLE IF NOT EXISTS public.orders (
  id INT PRIMARY KEY,
  customer_id INT REFERENCES customers(id),
  amount NUMERIC(10,2),
  status TEXT,
  FOREIGN KEY (status) REFERENCES statuses(code)
);
`
	tables, err := ParseDDL(ddl)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 2 {
		t.Fatalf("want 2 tables, got %d", len(tables))
	}
	byName := map[string]DDLTable{}
	for _, tb := range tables {
		byName[tb.Name] = tb
	}
	cust := byName["customers"]
	if len(cust.Columns) != 3 || cust.Columns[0].Name != "id" {
		t.Fatalf("customers columns: %+v", cust.Columns)
	}
	if len(cust.PrimaryKey) != 1 || cust.PrimaryKey[0] != "id" {
		t.Fatalf("customers pk: %+v", cust.PrimaryKey)
	}
	ord := byName["orders"] // schema prefix stripped
	if ord.Name != "orders" {
		t.Fatalf("orders name: %q", ord.Name)
	}
	cols := map[string]string{}
	for _, c := range ord.Columns {
		cols[c.Name] = c.Type
	}
	if _, ok := cols["amount"]; !ok || len(ord.Columns) != 4 {
		t.Fatalf("orders columns: %+v", ord.Columns)
	}
	if len(ord.ForeignKeys) != 2 {
		t.Fatalf("orders fks: %+v", ord.ForeignKeys)
	}
	fk := map[string]DDLForeignKey{}
	for _, f := range ord.ForeignKeys {
		fk[f.Column] = f
	}
	if fk["customer_id"].RefTable != "customers" || fk["customer_id"].RefColumn != "id" {
		t.Fatalf("customer_id fk: %+v", fk["customer_id"])
	}
	if fk["status"].RefTable != "statuses" || fk["status"].RefColumn != "code" {
		t.Fatalf("status fk: %+v", fk["status"])
	}
}

func TestParseDDLMySQLBackticksAndTableLevelPK(t *testing.T) {
	ddl := "CREATE TABLE `orders` (`id` INT, `customer_id` INT, PRIMARY KEY (`id`), FOREIGN KEY (`customer_id`) REFERENCES `customers`(`id`));"
	tables, err := ParseDDL(ddl)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || tables[0].Name != "orders" {
		t.Fatalf("tables: %+v", tables)
	}
	if len(tables[0].PrimaryKey) != 1 || tables[0].PrimaryKey[0] != "id" {
		t.Fatalf("pk: %+v", tables[0].PrimaryKey)
	}
	if len(tables[0].ForeignKeys) != 1 || tables[0].ForeignKeys[0].RefTable != "customers" {
		t.Fatalf("fk: %+v", tables[0].ForeignKeys)
	}
}

func TestParseDDLNoCreateTable(t *testing.T) {
	if _, err := ParseDDL("SELECT 1;"); err == nil {
		t.Fatal("expected error when no CREATE TABLE present")
	}
}

func TestParseDDLConstraintForm(t *testing.T) {
	ddl := `CREATE TABLE orders (
  id INT,
  customer_id INT,
  CONSTRAINT pk_orders PRIMARY KEY (id),
  CONSTRAINT fk_cust FOREIGN KEY (customer_id) REFERENCES customers(id)
);`
	tables, err := ParseDDL(ddl)
	if err != nil {
		t.Fatal(err)
	}
	tb := tables[0]
	if len(tb.PrimaryKey) != 1 || tb.PrimaryKey[0] != "id" {
		t.Fatalf("constraint pk: %+v", tb.PrimaryKey)
	}
	if len(tb.ForeignKeys) != 1 || tb.ForeignKeys[0].Column != "customer_id" ||
		tb.ForeignKeys[0].RefTable != "customers" || tb.ForeignKeys[0].RefColumn != "id" {
		t.Fatalf("constraint fk: %+v", tb.ForeignKeys)
	}
}

func TestMappingFromDDL(t *testing.T) {
	ddl := `
CREATE TABLE customers (id INT PRIMARY KEY, name TEXT, city TEXT);
CREATE TABLE orders (id INT PRIMARY KEY, customer_id INT REFERENCES customers(id), product TEXT, amount NUMERIC, status TEXT);
`
	plan, tables, err := MappingFromDDL(ddl, DDLMappingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 2 {
		t.Fatalf("tables: %d", len(tables))
	}

	cust := plan.Tables["customers"]
	if cust.RAG == nil || cust.RAG.IDColumn != "id" {
		t.Fatalf("customers RAG: %+v", cust.RAG)
	}
	if cust.KG == nil || len(cust.KG.Entities) != 1 ||
		cust.KG.Entities[0].Type != "Customer" || cust.KG.Entities[0].IDTmpl != "{id}" {
		t.Fatalf("customers KG entity: %+v", cust.KG)
	}

	ord := plan.Tables["orders"]
	if ord.RAG.IDColumn != "id" {
		t.Fatalf("orders RAG id: %+v", ord.RAG)
	}
	types := map[string]EntityMap{}
	for _, e := range ord.KG.Entities {
		types[e.Type] = e
	}
	if types["Order"].IDTmpl != "{id}" {
		t.Fatalf("Order entity: %+v", types["Order"])
	}
	if types["Customer"].IDTmpl != "{customer_id}" {
		t.Fatalf("ref Customer entity: %+v", types["Customer"])
	}
	if len(ord.KG.Relations) != 1 {
		t.Fatalf("orders relations: %+v", ord.KG.Relations)
	}
	rel := ord.KG.Relations[0]
	if rel.Subject != "orders" || rel.Predicate != "customer" || rel.Object != "customers" {
		t.Fatalf("relation: %+v", rel)
	}
	for _, p := range types["Order"].Props {
		if p == "customer_id" {
			t.Fatal("customer_id should not be an Order prop")
		}
	}
}

func TestMappingFromDDLRelationStyleReftable(t *testing.T) {
	ddl := `CREATE TABLE a (id INT PRIMARY KEY, b_ref INT REFERENCES bs(id));`
	plan, _, err := MappingFromDDL(ddl, DDLMappingOptions{RelationStyle: "reftable"})
	if err != nil {
		t.Fatal(err)
	}
	rel := plan.Tables["a"].KG.Relations[0]
	if rel.Predicate != "references_bs" {
		t.Fatalf("predicate: %q", rel.Predicate)
	}
}
