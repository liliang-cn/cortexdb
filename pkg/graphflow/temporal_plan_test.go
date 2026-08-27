package graphflow

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// What the query planner actually does, rather than what the index name suggests.
func TestAsOfUsesTheExpressionIndex(t *testing.T) {
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "plan.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 2000; i++ {
		at := base.AddDate(0, 0, i)
		if err := SaveTemporalFact(ctx, db, TemporalFact{
			From: fmt.Sprintf("subject-%d", i), To: "somewhere",
			Type: "lives_in", ValidFrom: &at,
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	if _, err := QueryFactsAsOf(ctx, db, base, TemporalFilter{}); err != nil {
		t.Fatalf("warm: %v", err)
	}

	vf := db.Dialect().JSONText("properties", factValidFromKey)
	q := `SELECT from_node_id FROM graph_edges WHERE ` + vf + ` IS NOT NULL AND ` + vf + ` <= ?`
	rows, err := db.SQL().QueryContext(ctx, `EXPLAIN QUERY PLAN `+q, base.Format(time.RFC3339))
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()

	plan := ""
	for rows.Next() {
		var a, b, c int
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatalf("scan: %v", err)
		}
		plan += detail + "\n"
	}
	t.Logf("plan:\n%s", plan)

	// Asserted, not logged. A test that only prints the plan passes just as
	// happily on the day the index stops being used, which is the day anyone
	// would want to hear about it.
	if !strings.Contains(plan, "idx_graph_edges_valid_from") {
		t.Errorf("the as-of filter is not using the expression index:\n%s", plan)
	}
	if strings.Contains(plan, "SCAN graph_edges") {
		t.Errorf("the as-of filter is still scanning every edge:\n%s", plan)
	}
}
