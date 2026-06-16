package connector

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// leakStrings are the raw PII values from newFake() that must NEVER survive
// desensitization into the RAG content: the phone, the in-notes phone, the
// national id, and the name.
var leakStrings = []string{"13812341234", "13900000000", "110101199003078888", "张三"}

// assertNoLeak fails if any raw PII value appears in body. This is the whole
// point of the test and must run over REAL imported content.
func assertNoLeak(t *testing.T, body string) {
	t.Helper()
	for _, leak := range leakStrings {
		if indexOf(body, leak) >= 0 {
			t.Fatalf("PII leaked into RAG content: %q in %q", leak, body)
		}
	}
}

func TestEndToEndDesensitizedImport(t *testing.T) {
	dir := t.TempDir()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "kb.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	src := newFake() // users: id,name,phone,ssn,notes (from desensitize_test.go)

	plan, err := BuildMaskingPlan(ctx, src, NewRuleClassifier(), PlanOptions{ScanTextColumns: true})
	if err != nil {
		t.Fatal(err)
	}
	plan.Sign("reviewer", time.Unix(1, 0))
	d, err := NewDesensitizer(plan, DesensitizerOptions{
		Tenant: "t", KeyProvider: testKP(),
		Vault: func() Vault { v, _ := OpenSQLiteVault(filepath.Join(dir, "v.db")); return v }(),
	})
	if err != nil {
		t.Fatal(err)
	}
	safe := Desensitized(src, d)

	// importflow uses {col} placeholder syntax (see pkg/importflow/plan.go
	// renderTemplate); unknown/dropped columns render as the empty string.
	mapping := importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
		"users": {RAG: &importflow.RAGPlan{ContentTmpl: "{name} {phone} {notes}"}},
	}}
	rep, err := importflow.New(db).Run(ctx, safe, mapping)
	if err != nil {
		t.Fatal(err)
	}
	// Sanity: the row really was imported as a non-empty RAG chunk. Without this
	// the leak-check below could pass vacuously.
	if rep.ChunksIndexed != 1 {
		t.Fatalf("expected 1 indexed chunk, got %d (rows read=%d)", rep.ChunksIndexed, rep.RowsRead)
	}

	// Run the leak-check over the ACTUAL imported content. The RAG sink keys each
	// chunk by "<table>:<row>" when RAGPlan.IDColumn is empty (mapRAG in
	// pkg/importflow/mapper.go), so the single row lands at KnowledgeID "users:0".
	// Fetching it directly guarantees the assertion runs over real content rather
	// than depending on whether a lexical query happens to match the masked text.
	full, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "users:0"})
	if err != nil {
		t.Fatalf("GetKnowledge(users:0): %v", err)
	}
	body := full.Knowledge.Content
	if body == "" {
		t.Fatal("imported knowledge content is empty")
	}
	assertNoLeak(t, body)

	// Also exercise the search path (lexical mode, no embedder). The masked phone
	// "138****1234" survives into the content, so a "138" query is a real hit. Any
	// result that comes back is also checked for leaks.
	res, err := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{Query: "138 vip customer", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range res.Results {
		hit, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: h.KnowledgeID})
		if err != nil {
			t.Fatalf("GetKnowledge(%s): %v", h.KnowledgeID, err)
		}
		assertNoLeak(t, hit.Knowledge.Content)
	}
}

// kgFakeSource is a re-readable in-memory source modelling an "orders" table
// whose join key (customer_phone) is itself PII. Two rows share the same
// customer so the test can prove the pseudonymized token preserves the edge.
type kgFakeSource struct{}

func (kgFakeSource) Schemas(context.Context) ([]importflow.Schema, error) {
	return []importflow.Schema{{
		Table: "orders",
		Columns: []importflow.Column{
			{Name: "customer_phone", Type: "text"},
			{Name: "product_id", Type: "text"},
			{Name: "city", Type: "text"},
		},
	}}, nil
}

func (kgFakeSource) Records(_ context.Context, fn func(importflow.Record) error) error {
	rows := []importflow.Record{
		{Table: "orders", Row: 0, Values: map[string]string{"customer_phone": "13812341234", "product_id": "P1", "city": "Chengdu"}},
		{Table: "orders", Row: 1, Values: map[string]string{"customer_phone": "13812341234", "product_id": "P2", "city": "Chengdu"}},
	}
	for _, r := range rows {
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

func (kgFakeSource) Close() error { return nil }

// TestEndToEndDesensitizedKnowledgeGraph proves the connector feeds the
// KNOWLEDGE GRAPH sink (not just RAG): external rows whose identity column is
// PII are pseudonymized, and the resulting entity IRIs carry the deterministic
// token — so the "customer bought product" edges survive (joins preserved) while
// the raw phone number never appears anywhere in the graph.
func TestEndToEndDesensitizedKnowledgeGraph(t *testing.T) {
	dir := t.TempDir()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "kb.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	src := kgFakeSource{}

	// The join key (customer_phone) is PII → pseudonymize so the same customer
	// maps to the same token (and thus the same entity IRI) across rows, while
	// the original phone goes only to the vault. product_id / city are kept.
	plan := MaskingPlan{Columns: []ColumnRule{
		{Table: "orders", Column: "customer_phone", PiiKind: PiiPhone, Action: ActionPseudonymize},
		{Table: "orders", Column: "product_id", Action: ActionKeep},
		{Table: "orders", Column: "city", Action: ActionKeep},
	}}
	plan.Sign("reviewer", time.Unix(1, 0))

	vault, err := OpenSQLiteVault(filepath.Join(dir, "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	d, err := NewDesensitizer(plan, DesensitizerOptions{Tenant: "t", KeyProvider: testKP(), Vault: vault})
	if err != nil {
		t.Fatal(err)
	}
	safe := Desensitized(src, d)

	// Build the graph: a Customer entity keyed by the (now tokenized) phone, a
	// Product entity keyed by product_id, connected by a "bought" relation.
	mapping := importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
		"orders": {KG: &importflow.KGPlan{
			Entities: []importflow.EntityMap{
				{Ref: "customer", Type: "Customer", IDTmpl: "{customer_phone}", Props: []string{"city"}},
				{Ref: "product", Type: "Product", IDTmpl: "{product_id}"},
			},
			Relations: []importflow.RelationMap{
				{Subject: "customer", Predicate: "bought", Object: "product"},
			},
		}},
	}}
	rep, err := importflow.New(db).Run(ctx, safe, mapping)
	if err != nil {
		t.Fatal(err)
	}
	// Sanity: triples really were created, so the assertions below aren't vacuous.
	// 2 rows × (rdf:type + city prop + product rdf:type + bought) — at minimum the
	// two relation edges plus entity-type triples must exist.
	if rep.TriplesCreated == 0 {
		t.Fatalf("no triples created (rows read=%d)", rep.RowsRead)
	}

	// Dump the whole graph and inspect it.
	exp, err := db.ExportKnowledgeGraph(ctx, cortexdb.KnowledgeGraphExportRequest{Format: "ntriples"})
	if err != nil {
		t.Fatal(err)
	}
	g := exp.Content

	// 1. The raw phone must NOT appear anywhere in the graph.
	if indexOf(g, "13812341234") >= 0 {
		t.Fatalf("raw PII leaked into the knowledge graph:\n%s", g)
	}
	// 2. The customer entity IRI must carry the deterministic token.
	tok, err := vault.Put(ctx, "t", PiiPhone, "13812341234", testKP())
	if err != nil {
		t.Fatal(err)
	}
	customerIRI := "urn:cortexdb:Customer:" + tok
	if indexOf(g, customerIRI) < 0 {
		t.Fatalf("tokenized customer entity %q not found in graph:\n%s", customerIRI, g)
	}
	// 3. The edges survived: both products are linked to the SAME customer token,
	//    proving the pseudonym preserved the join across the two rows.
	for _, pid := range []string{"P1", "P2"} {
		edge := "<" + customerIRI + "> <urn:cortexdb:rel:bought> <urn:cortexdb:Product:" + pid + ">"
		if indexOf(g, edge) < 0 {
			t.Fatalf("expected preserved edge %s in graph:\n%s", edge, g)
		}
	}

	// 4. The vault still reverses the token to the original (operational un-mask),
	//    even though the graph itself never exposes it.
	got, err := Unmask(ctx, vault, "t", []string{tok}, testKP())
	if err != nil {
		t.Fatal(err)
	}
	if got[tok] != "13812341234" {
		t.Fatalf("vault un-mask failed: %v", got)
	}
}
