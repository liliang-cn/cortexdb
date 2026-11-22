package cortexdb

import (
	"context"
	"path/filepath"
	"testing"
)

// With the recall log on, its tables exist once Open returns, so no recall
// changes the schema under a search running beside it (#244).
func TestRecallLogTablesExistAtOpen(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "rl.db")), WithRecallLog(RecallLogOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"recall_log", "recall_log_hits", "recall_feedback"} {
		var n int
		if err := db.SQL().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s at open: %d %v", table, n, err)
		}
	}
}
