package connector

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// scriptedSource emits a fixed list of events once, then returns.
type scriptedSource struct{ events []ChangeEvent }

func (s *scriptedSource) Changes(_ context.Context, fn func(ChangeEvent) error) error {
	for _, e := range s.events {
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}
func (s *scriptedSource) Close() error { return nil }

func cdcMapping() importflow.MappingPlan {
	return importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
		"users": {
			RAG: &importflow.RAGPlan{ContentTmpl: "{name} {phone}", IDColumn: "id"},
			KG: &importflow.KGPlan{Entities: []importflow.EntityMap{
				{Ref: "u", Type: "User", IDTmpl: "{id}", Props: []string{"name"}},
			}},
		},
	}}
}

func cdcSignedPlan() MaskingPlan {
	p := MaskingPlan{Columns: []ColumnRule{
		{Table: "users", Column: "id", Action: ActionKeep},
		{Table: "users", Column: "name", Action: ActionKeep},
		{Table: "users", Column: "phone", PiiKind: PiiPhone, Action: ActionMask},
	}}
	p.Sign("tester", time.Unix(1, 0))
	return p
}

func newWatcherForTest(t *testing.T, db *cortexdb.DB, src ChangeSource) *Watcher {
	t.Helper()
	d, err := NewDesensitizer(cdcSignedPlan(), DesensitizerOptions{Tenant: "t", KeyProvider: testKP(), Vault: testVault(t)})
	if err != nil {
		t.Fatal(err)
	}
	cp, err := NewSQLiteCheckpointStore(db)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWatcher(db, src, WatcherOptions{
		SourceKey:    "users-src",
		Desensitizer: d,
		Mapping:      cdcMapping(),
		Checkpoint:   cp,
		Columns:      map[string][]importflow.Column{"users": {{Name: "id"}, {Name: "name"}, {Name: "phone"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestWatcherInsertThenDelete(t *testing.T) {
	dir := t.TempDir()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "kb.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	src := &scriptedSource{events: []ChangeEvent{
		{Op: OpInsert, Table: "users", Key: map[string]string{"id": "1"},
			Row: importflow.Record{Table: "users", Values: map[string]string{"id": "1", "name": "Alice", "phone": "13812341234"}}},
	}}
	w := newWatcherForTest(t, db, src)
	if err := w.Run(ctx); err != nil {
		t.Fatalf("run insert: %v", err)
	}

	got, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
	if err != nil {
		t.Fatalf("get after insert: %v", err)
	}
	if indexOf(got.Knowledge.Content, "13812341234") >= 0 {
		t.Fatalf("raw PII leaked: %q", got.Knowledge.Content)
	}
	if indexOf(got.Knowledge.Content, "Alice") < 0 {
		t.Fatalf("content missing kept field: %q", got.Knowledge.Content)
	}

	src2 := &scriptedSource{events: []ChangeEvent{
		{Op: OpDelete, Table: "users", Key: map[string]string{"id": "1"}},
	}}
	w2 := newWatcherForTest(t, db, src2)
	if err := w2.Run(ctx); err != nil {
		t.Fatalf("run delete: %v", err)
	}
	if _, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"}); err == nil {
		t.Fatal("expected chunk gone after delete")
	}
}

func TestWatcherUpdateUpserts(t *testing.T) {
	dir := t.TempDir()
	db, _ := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "kb.db")))
	defer db.Close()
	ctx := context.Background()

	w := newWatcherForTest(t, db, &scriptedSource{events: []ChangeEvent{
		{Op: OpInsert, Table: "users", Key: map[string]string{"id": "1"},
			Row: importflow.Record{Table: "users", Values: map[string]string{"id": "1", "name": "Alice", "phone": "13812341234"}}},
		{Op: OpUpdate, Table: "users", Key: map[string]string{"id": "1"},
			Row: importflow.Record{Table: "users", Values: map[string]string{"id": "1", "name": "Alice Cooper", "phone": "13812341234"}}},
	}})
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "1"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if indexOf(got.Knowledge.Content, "Cooper") < 0 {
		t.Fatalf("update did not upsert content: %q", got.Knowledge.Content)
	}
}

func TestWatcherRejectsMissingIDColumn(t *testing.T) {
	dir := t.TempDir()
	db, _ := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "kb.db")))
	defer db.Close()
	d, _ := NewDesensitizer(cdcSignedPlan(), DesensitizerOptions{Tenant: "t", KeyProvider: testKP(), Vault: testVault(t)})
	cp, _ := NewSQLiteCheckpointStore(db)
	badMapping := importflow.MappingPlan{Tables: map[string]importflow.TablePlan{
		"users": {RAG: &importflow.RAGPlan{ContentTmpl: "{name}"}}, // no IDColumn
	}}
	_, err := NewWatcher(db, &scriptedSource{}, WatcherOptions{
		SourceKey: "k", Desensitizer: d, Mapping: badMapping, Checkpoint: cp,
	})
	if err == nil {
		t.Fatal("expected key-precondition error for RAG without IDColumn")
	}
}
