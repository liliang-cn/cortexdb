// Demo: desensitize a CSV through the connector privacy gate, then import to RAG.
// go run ./examples/09_connector
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/connector"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

func main() {
	dbPath := "example_connector.db"
	vaultPath := "example_connector.vault.db"
	defer os.Remove(dbPath)
	defer os.Remove(vaultPath)

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	// CSVSource buffers the whole stream at construction, so one src is safe to
	// read for both planning (Schemas) and import (Schemas + Records).
	csv := "id,name,phone,notes\n1,张三,13812341234,VIP; reach at 13900000000\n"
	src, err := importflow.NewCSVSource(strings.NewReader(csv), importflow.CSVOptions{Table: "customers"})
	if err != nil {
		log.Fatal(err)
	}

	// Schema-first: classify PII and propose a plan. No data has moved yet.
	plan, err := connector.BuildMaskingPlan(ctx, src, connector.NewRuleClassifier(), connector.PlanOptions{ScanTextColumns: true})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("proposed MaskingPlan (review before signing):")
	for _, r := range plan.Columns {
		fmt.Printf("  %s.%s  kind=%s  action=%s  (%s)\n", r.Table, r.Column, r.PiiKind, r.Action, r.Reason)
	}
	// A human signs off. Run refuses an unsigned plan.
	plan.Sign("you", time.Now())

	// The token vault is a SEPARATE store; the RAG path never reads it.
	vault, err := connector.OpenSQLiteVault(vaultPath)
	if err != nil {
		log.Fatal(err)
	}
	defer vault.Close()
	d, err := connector.NewDesensitizer(plan, connector.DesensitizerOptions{
		Tenant: "demo", KeyProvider: connector.StaticKeyProvider([]byte("0123456789abcdef0123456789abcdef")), Vault: vault,
	})
	if err != nil {
		log.Fatal(err)
	}

	// Data-second: import through the desensitizing decorator into RAG.
	mapping := importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
		"customers": {RAG: &importflow.RAGPlan{ContentTmpl: "{name} {phone} {notes}"}},
	}}
	rep, err := importflow.New(db).Run(ctx, connector.Desensitized(src, d), mapping)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("imported: %d rows, %d chunks (PII desensitized before indexing)\n", rep.RowsRead, rep.ChunksIndexed)
}
