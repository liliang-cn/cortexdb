package connector

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func TestSQLiteCheckpointRoundTrip(t *testing.T) {
	dir := t.TempDir()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(dir, "kb.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	cp, err := NewSQLiteCheckpointStore(db)
	if err != nil {
		t.Fatal(err)
	}

	// missing -> zero Checkpoint, ok=false
	got, ok, err := cp.Load(ctx, "src1")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("expected no checkpoint yet, got %+v", got)
	}

	if err := cp.Save(ctx, "src1", Checkpoint{Cursor: `{"users":"100"}`, Position: ""}); err != nil {
		t.Fatal(err)
	}
	got, ok, err = cp.Load(ctx, "src1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.Cursor != `{"users":"100"}` {
		t.Fatalf("load mismatch: %+v ok=%v", got, ok)
	}

	// overwrite
	if err := cp.Save(ctx, "src1", Checkpoint{Cursor: `{"users":"200"}`}); err != nil {
		t.Fatal(err)
	}
	got, _, _ = cp.Load(ctx, "src1")
	if got.Cursor != `{"users":"200"}` {
		t.Fatalf("overwrite failed: %+v", got)
	}
}
