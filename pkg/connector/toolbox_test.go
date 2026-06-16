package connector

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func TestToolboxIntrospectAndUnmask(t *testing.T) {
	dir := t.TempDir()
	db, _ := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "kb.db")))
	defer db.Close()
	v, _ := OpenSQLiteVault(filepath.Join(dir, "v.db"))
	defer v.Close()
	tb := NewToolbox(db, ToolboxOptions{Vault: v, KeyProvider: testKP(), Tenant: "t"})

	// definitions present
	names := map[string]bool{}
	for _, d := range tb.Definitions() {
		names[d.Name] = true
	}
	for _, want := range []string{"connector_introspect", "connector_plan", "connector_run", "connector_unmask"} {
		if !names[want] {
			t.Fatalf("missing tool %s", want)
		}
	}

	// unmask round-trips a token created via the vault
	tok, _ := v.Put(context.Background(), "t", PiiName, "张三", testKP())
	out, err := tb.Call(context.Background(), "connector_unmask", json.RawMessage(`{"tokens":["`+tok+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	if indexOf(string(b), "张三") < 0 {
		t.Fatalf("unmask did not return original: %s", b)
	}
}
