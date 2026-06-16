package connector

import (
	"context"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

type fakeSource struct {
	schema  importflow.Schema
	records []importflow.Record
}

func (f *fakeSource) Schemas(context.Context) ([]importflow.Schema, error) {
	return []importflow.Schema{f.schema}, nil
}
func (f *fakeSource) Records(_ context.Context, fn func(importflow.Record) error) error {
	for _, r := range f.records {
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}
func (f *fakeSource) Close() error { return nil }

func newFake() *fakeSource {
	return &fakeSource{
		schema: importflow.Schema{Table: "users", Columns: []importflow.Column{
			{Name: "id"}, {Name: "name"}, {Name: "phone"}, {Name: "ssn"}, {Name: "notes"},
		}},
		records: []importflow.Record{
			{Table: "users", Values: map[string]string{"id": "1", "name": "张三", "phone": "13812341234", "ssn": "110101199003078888", "notes": "vip, call 13900000000"}},
		},
	}
}

func signedPlan() MaskingPlan {
	p := MaskingPlan{
		Columns: []ColumnRule{
			{Table: "users", Column: "id", Action: ActionKeep},
			{Table: "users", Column: "name", PiiKind: PiiName, Action: ActionPseudonymize},
			{Table: "users", Column: "phone", PiiKind: PiiPhone, Action: ActionMask},
			{Table: "users", Column: "ssn", PiiKind: PiiNationalID, Action: ActionDrop},
		},
		TextScan: []TextScanRule{{Table: "users", Column: "notes"}},
	}
	p.Sign("tester", time.Unix(1, 0))
	return p
}

func TestDesensitizedSchemaDropsColumns(t *testing.T) {
	d := mustDesensitizer(t, signedPlan())
	src := Desensitized(newFake(), d)
	schemas, err := src.Schemas(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range schemas[0].Columns {
		if c.Name == "ssn" {
			t.Fatal("dropped column must not appear in schema")
		}
	}
}

func TestDesensitizedRecordsMasked(t *testing.T) {
	d := mustDesensitizer(t, signedPlan())
	src := Desensitized(newFake(), d)
	var got importflow.Record
	_ = src.Records(context.Background(), func(r importflow.Record) error { got = r; return nil })

	if _, ok := got.Values["ssn"]; ok {
		t.Fatal("dropped column leaked into record")
	}
	if got.Values["phone"] != "138****1234" {
		t.Fatalf("phone not masked: %q", got.Values["phone"])
	}
	if got.Values["name"] == "张三" || got.Values["name"] == "" {
		t.Fatalf("name not pseudonymized: %q", got.Values["name"])
	}
	if got.Values["notes"] == "" || contains(got.Values["notes"], "13900000000") {
		t.Fatalf("free-text PII not redacted: %q", got.Values["notes"])
	}
	if got.Values["id"] != "1" {
		t.Fatalf("kept column altered: %q", got.Values["id"])
	}
}

func TestDesensitizedRefusesUnsignedPlan(t *testing.T) {
	p := signedPlan()
	p.SignedBy = "" // unsign
	_, err := NewDesensitizer(p, DesensitizerOptions{Tenant: "t", KeyProvider: testKP(), Vault: testVault(t)})
	if err == nil {
		t.Fatal("expected refusal of unsigned plan")
	}
}

func contains(s, sub string) bool { return indexOf(s, sub) >= 0 }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func testKP() KeyProvider { return StaticKeyProvider([]byte("0123456789abcdef0123456789abcdef")) }
func testVault(t *testing.T) Vault {
	v, err := OpenSQLiteVault(t.TempDir() + "/v.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = v.Close() })
	return v
}
func mustDesensitizer(t *testing.T, p MaskingPlan) *Desensitizer {
	d, err := NewDesensitizer(p, DesensitizerOptions{Tenant: "t", KeyProvider: testKP(), Vault: testVault(t)})
	if err != nil {
		t.Fatal(err)
	}
	return d
}
