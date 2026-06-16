package connector

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// fakeCursorSource is a ChangeSource that also exposes watermark load/save, like
// the polling source, but emits scripted events and records the cursor it was
// seeded with.
type fakeCursorSource struct {
	events    []ChangeEvent
	loaded    string
	saveValue string
}

func (f *fakeCursorSource) Changes(_ context.Context, fn func(ChangeEvent) error) error {
	for _, e := range f.events {
		if err := fn(e); err != nil {
			return err
		}
	}
	f.saveValue = `{"users":"200"}`
	return nil
}
func (f *fakeCursorSource) Close() error                       { return nil }
func (f *fakeCursorSource) LoadWatermarks(cursor string) error { f.loaded = cursor; return nil }
func (f *fakeCursorSource) WatermarkJSON() (string, error)     { return f.saveValue, nil }

func cortexdbOpen(t *testing.T, path string) (*cortexdb.DB, error) {
	t.Helper()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	return db, nil
}

func newWatcherForTestWithSource(t *testing.T, db *cortexdb.DB, src ChangeSource, cp CheckpointStore) *Watcher {
	t.Helper()
	d, err := NewDesensitizer(cdcSignedPlan(), DesensitizerOptions{Tenant: "t", KeyProvider: testKP(), Vault: testVault(t)})
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

func TestWatcherSeedsAndSavesCheckpoint(t *testing.T) {
	dir := t.TempDir()
	db, _ := cortexdbOpen(t, filepath.Join(dir, "kb.db"))
	defer db.Close()
	ctx := context.Background()
	cp, _ := NewSQLiteCheckpointStore(db)
	if err := cp.Save(ctx, "users-src", Checkpoint{Cursor: `{"users":"100"}`}); err != nil {
		t.Fatal(err)
	}
	src := &fakeCursorSource{events: []ChangeEvent{
		{Op: OpInsert, Table: "users", Key: map[string]string{"id": "1"},
			Row: importflow.Record{Table: "users", Values: map[string]string{"id": "1", "name": "Alice", "phone": "13812341234"}}},
	}}
	w := newWatcherForTestWithSource(t, db, src, cp)
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if src.loaded != `{"users":"100"}` {
		t.Fatalf("watermark not seeded from checkpoint: %q", src.loaded)
	}
	got, _, _ := cp.Load(ctx, "users-src")
	if got.Cursor != `{"users":"200"}` {
		t.Fatalf("watermark not saved after drain: %q", got.Cursor)
	}
}
