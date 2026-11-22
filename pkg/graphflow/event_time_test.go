package graphflow

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// A time a document states is the temporal fact QueryFactsAsOf reads: one
// model of event time, whoever wrote it (#252).
func TestQueryFactsAsOfReadsTimesExtractedFromText(t *testing.T) {
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "facts.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if _, err := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
		KnowledgeID: "khan", Content: "Sal Khan worked at Oracle from 2005 until 2008.",
		Entities:  []cortexdb.ToolEntityInput{{Name: "Sal Khan"}, {Name: "Oracle"}},
		Relations: []cortexdb.ToolRelationInput{{From: "Sal Khan", To: "Oracle", Type: "worked_at"}},
	}); err != nil {
		t.Fatal(err)
	}
	at := func(year int) time.Time { return time.Date(year, 6, 1, 0, 0, 0, 0, time.UTC) }
	during, err := QueryFactsAsOf(ctx, db, at(2006), TemporalFilter{Type: "worked_at"})
	if err != nil {
		t.Fatal(err)
	}
	if len(during) != 1 || during[0].To != "Oracle" {
		t.Fatalf("as of 2006: %+v, want Sal Khan worked_at Oracle", during)
	}
	after, err := QueryFactsAsOf(ctx, db, at(2010), TemporalFilter{Type: "worked_at"})
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Fatalf("as of 2010 the fact had ended: %+v", after)
	}
}
