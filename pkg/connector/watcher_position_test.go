package connector

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// fakePositionSource streams scripted events (each carrying a Position) and
// records the position it was seeded with. It is a positionCheckpointer.
type fakePositionSource struct {
	events []ChangeEvent
	loaded string
}

func (f *fakePositionSource) Changes(_ context.Context, fn func(ChangeEvent) error) error {
	for _, e := range f.events {
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}
func (f *fakePositionSource) Close() error                  { return nil }
func (f *fakePositionSource) LoadPosition(pos string) error { f.loaded = pos; return nil }

func TestWatcherSeedsAndSavesPosition(t *testing.T) {
	dir := t.TempDir()
	db, _ := cortexdbOpen(t, filepath.Join(dir, "kb.db"))
	defer db.Close()
	ctx := context.Background()
	cp, _ := NewSQLiteCheckpointStore(db)
	if err := cp.Save(ctx, "users-src", Checkpoint{Position: "0/16B3748"}); err != nil {
		t.Fatal(err)
	}
	src := &fakePositionSource{events: []ChangeEvent{
		{Op: OpInsert, Table: "users", Key: map[string]string{"id": "1"}, Position: "0/16B37A0",
			Row: importflow.Record{Table: "users", Values: map[string]string{"id": "1", "name": "Alice", "phone": "13812341234"}}},
	}}
	w := newWatcherForTestWithSource(t, db, src, cp)
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if src.loaded != "0/16B3748" {
		t.Fatalf("position not seeded from checkpoint: %q", src.loaded)
	}
	got, _, _ := cp.Load(ctx, "users-src")
	if got.Position != "0/16B37A0" {
		t.Fatalf("applied position not saved: %q", got.Position)
	}
}
