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
