package cortexdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// What the change feed adds to a write through the public API: memories,
// knowledge and entities saved into a brain with the feed and one without
// it, alternating in rounds so drift in the machine's load lands on both.
// Opt-in, since it is a measurement (pkg/graph's TestChangeFeedWriteOverhead
// is the same at the graph layer).
func TestChangeFeedFacadeWriteOverhead(t *testing.T) {
	if os.Getenv("CORTEXDB_FEED_BENCH") == "" {
		t.Skip("set CORTEXDB_FEED_BENCH=1 to measure")
	}
	ctx := context.Background()
	brains := map[bool]*DB{}
	for _, on := range []bool{false, true} {
		// The feed on is the default: the change log and the inference
		// maintainer that reads it. Off is both turned off.
		db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), fmt.Sprintf("feed_%v.db", on))),
			WithChangeFeed(ChangeFeedOptions{Disabled: !on}))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		brains[on] = db
	}
	kinds := []string{"memory", "knowledge", "entities"}
	samples := map[string][]time.Duration{}
	n := 0
	for round := 0; round < 10; round++ {
		for _, on := range []bool{round%2 == 0, round%2 != 0} {
			db := brains[on]
			for i := 0; i < 90; i++ {
				n++
				kind := kinds[i%3]
				start := time.Now()
				var err error
				switch kind {
				case "memory":
					_, err = db.SaveMemory(ctx, MemorySaveRequest{MemoryID: fmt.Sprintf("m%d", n), Scope: "global",
						Content: fmt.Sprintf("Note %d: the Harbour Board met Quorvane on day %d.", n, n)})
				case "knowledge":
					_, err = db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: fmt.Sprintf("k%d", n), Title: fmt.Sprintf("Ledger %d", n),
						Content: fmt.Sprintf("Ledger %d records that Mira Holt shipped crate %d to Port Aster.", n, n)})
				default:
					_, err = db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
						{Name: fmt.Sprintf("Vessel %d", n), Type: "ship"}, {Name: fmt.Sprintf("Captain %d", n), Type: "person"}}})
				}
				if err != nil {
					t.Fatal(err)
				}
				key := fmt.Sprintf("%v/%s", on, kind)
				samples[key] = append(samples[key], time.Since(start))
			}
		}
	}
	for on, db := range brains {
		// Read directly: ChangesHead would install the feed it reads.
		var logged int
		if err := db.queryRow(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name LIKE 'change_log_%'`).Scan(&logged); err != nil {
			t.Fatal(err)
		}
		if (logged > 0) != on {
			t.Fatalf("feed on=%v but %d change-log triggers are installed", on, logged)
		}
	}
	p50 := func(d []time.Duration) time.Duration {
		sorted := append([]time.Duration(nil), d...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		return sorted[len(sorted)/2]
	}
	for _, kind := range kinds {
		off, on := p50(samples["false/"+kind]), p50(samples["true/"+kind])
		t.Logf("%-9s off p50=%v on p50=%v overhead %.1f%%", kind, off, on, 100*(float64(on)/float64(off)-1))
	}
}
