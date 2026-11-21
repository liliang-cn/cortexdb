package cortexdb

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
)

// trigramFloorDocs are chunks in two collections; the floor counts only the
// searched one.
var trigramFloorDocs = map[string]struct{ collection, body string }{
	"wal":    {"kb", "The SQLite WAL grew without bound because nothing ever ran wal_checkpoint."},
	"pool":   {"kb", "A thin pool that fills up fails every backup volume in it at once."},
	"growth": {"kb", "Unbounded growth of a log file is a checkpoint problem, Größe included."},
	"tagit":  {"kb", "TagIt is a self-hosted team assistant addressed with @agent, 100% local."},
	"far":    {"other", "Checkpoint, checkpoint, checkpoint: the WAL of another collection."},
}

func saveTrigramFloorDocs(t *testing.T, db *DB) {
	t.Helper()
	for id, d := range trigramFloorDocs {
		if _, err := db.SaveKnowledge(context.Background(), KnowledgeSaveRequest{KnowledgeID: id, Content: d.body, Collection: d.collection}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
}

type floorRun struct {
	ids           []string
	scores        []float64
	trigram, scan int
}

func searchCountingStages(t *testing.T, db *DB, query string) floorRun {
	t.Helper()
	ctx, stages := observability.WithStages(context.Background())
	resp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: query, Collection: "kb", TopK: 5, RetrievalMode: RetrievalModeLexical})
	if err != nil {
		t.Fatalf("search %q: %v", query, err)
	}
	var r floorRun
	for _, h := range resp.Results {
		r.ids = append(r.ids, h.KnowledgeID)
		r.scores = append(r.scores, h.Score)
	}
	r.trigram, r.scan = stageCount(stages, "df-trigram"), stageCount(stages, "df-scan")
	return r
}

// The keyword floor counts the documents holding each term of the question
// through the trigram index, which answers the same LIKE as the scan of the
// collection it replaces: the same rows come back with the same scores, with
// the index whole or emptied.
func TestTheKeywordFloorCountsTermsThroughTheTrigramIndex(t *testing.T) {
	db := openSQLiteLexical(t)
	saveTrigramFloorDocs(t, db)
	const query = "why does the WAL checkpoint file keep growing"

	indexed := searchCountingStages(t, db, query)
	if indexed.trigram != 1 || indexed.scan != 0 {
		t.Fatalf("whole index: df-trigram ran %d times, df-scan %d, want 1 and 0", indexed.trigram, indexed.scan)
	}
	if len(indexed.ids) == 0 || slices.Contains(indexed.ids, "far") {
		t.Fatalf("results %v, want hits from kb only", indexed.ids)
	}

	if _, err := db.SQL().Exec(`INSERT INTO chunks_fts_cjk(chunks_fts_cjk) VALUES('delete-all')`); err != nil {
		t.Fatal(err)
	}
	// The chunks have not changed, so the counts would be served from the
	// cache (lexical_rarity_cache.go); forgotten, the scan counts them.
	forgetChunkStats(db)
	scanned := searchCountingStages(t, db, query)
	if scanned.trigram != 0 || scanned.scan != 1 {
		t.Fatalf("emptied index: df-trigram ran %d times, df-scan %d, want 0 and 1", scanned.trigram, scanned.scan)
	}
	if !slices.Equal(indexed.ids, scanned.ids) || !slices.Equal(indexed.scores, scanned.scores) {
		t.Fatalf("index %v %v, scan %v %v: want the same rows and scores", indexed.ids, indexed.scores, scanned.ids, scanned.scores)
	}
}

// A question the trigram index cannot count exactly or cheaply is counted by
// the scan, as before: a term under three characters, more terms than
// trigramCountMax, and a store without the index.
func TestTheKeywordFloorScansWhatTheTrigramIndexCannotCount(t *testing.T) {
	many := "checkpoint"
	for i := 0; i < trigramCountMax; i++ {
		many += fmt.Sprintf(" growing%d", i)
	}
	for name, query := range map[string]string{
		"short term": "is WAL checkpoint ok",
		"many terms": many,
	} {
		t.Run(name, func(t *testing.T) {
			db := openSQLiteLexical(t)
			saveTrigramFloorDocs(t, db)
			if r := searchCountingStages(t, db, query); r.trigram != 0 || r.scan != 1 {
				t.Fatalf("%q: df-trigram ran %d times, df-scan %d, want 0 and 1", query, r.trigram, r.scan)
			}
		})
	}
	t.Run("no index", func(t *testing.T) {
		db := openSQLiteLexical(t)
		saveTrigramFloorDocs(t, db)
		if _, err := db.SQL().Exec(`DROP TABLE chunks_fts_cjk`); err != nil {
			t.Fatal(err)
		}
		r := searchCountingStages(t, db, "why does the WAL checkpoint file keep growing")
		if r.trigram != 0 || r.scan != 1 || len(r.ids) == 0 {
			t.Fatalf("df-trigram ran %d times, df-scan %d, results %v: want the scan and hits", r.trigram, r.scan, r.ids)
		}
	})
}

// The counts by the index are the counts of the scan, term by term, in the
// collection and across the store.
func TestTrigramCountsEqualTheScan(t *testing.T) {
	db := openSQLiteLexical(t)
	saveTrigramFloorDocs(t, db)
	ctx := context.Background()
	patterns := []string{"checkpoint", "größe", "GRÖSSE", "wal", "nowhere"}
	for _, collection := range []string{"kb", ""} {
		from, where, args := "embeddings e", "", []any(nil)
		if collection != "" {
			from, where, args = "embeddings e LEFT JOIN collections c ON e.collection_id = c.id", "c.name = ?", []any{collection}
		}
		want, err := db.scanCorpusStats(ctx, "e.content", from, where, args, patterns)
		if err != nil {
			t.Fatal(err)
		}
		got, ok, err := db.chunkStatsByTrigrams(ctx, collection, patterns)
		if err != nil || !ok {
			t.Fatalf("collection %q: ok %v, err %v", collection, ok, err)
		}
		if got.n != want.n || fmt.Sprint(got.df) != fmt.Sprint(want.df) {
			t.Errorf("collection %q: index n=%d %v, scan n=%d %v", collection, got.n, got.df, want.n, want.df)
		}
	}
}

// A pattern holding a LIKE wildcard would need an ESCAPE clause, which keeps
// SQLite off the index: it is left to the scan. A question's words never hold
// one (they are cut at punctuation); the guard is for any other caller.
func TestAWildcardPatternIsLeftToTheScan(t *testing.T) {
	db := openSQLiteLexical(t)
	saveTrigramFloorDocs(t, db)
	for _, p := range []string{"100%", "wal_checkpoint", `back\slash`} {
		if _, ok, err := db.chunkStatsByTrigrams(context.Background(), "kb", []string{"checkpoint", p}); ok || err != nil {
			t.Errorf("%q: counted by the index (ok %v, err %v)", p, ok, err)
		}
	}
	if trigramCountable(nil) {
		t.Error("no pattern: nothing to count by the index")
	}
}

// A store that cannot be read while counting fails the search with its error.
func TestTrigramCountsThatCannotBeReadFail(t *testing.T) {
	db := openSQLiteLexical(t)
	saveTrigramFloorDocs(t, db)
	if _, err := db.SQL().Exec(`ALTER TABLE collections RENAME TO collections_hidden`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.chunkStatsByTrigrams(context.Background(), "kb", []string{"checkpoint"}); err == nil || !strings.Contains(err.Error(), "count term frequencies") {
		t.Fatalf("err %v, want the count's error", err)
	}
}

// A term common enough that counting it through the index would cost more
// than the scan sends the whole question to the scan.
func TestACommonTermIsLeftToTheScan(t *testing.T) {
	db := openSQLiteLexical(t)
	saveTrigramFloorDocs(t, db)
	ctx := context.Background()
	if common, err := db.anyTrigramPatternCommon(ctx, []string{"checkpoint", "wal"}, 3); err != nil || !common {
		t.Fatalf("checkpoint in 3 chunks, limit 3: common %v, err %v", common, err)
	}
	if common, err := db.anyTrigramPatternCommon(ctx, []string{"checkpoint", "wal"}, 4); err != nil || common {
		t.Fatalf("limit 4: common %v, err %v", common, err)
	}
	// Past trigramCommonFloor rows, a term in every chunk is common.
	for i := 0; i < trigramCommonFloor; i++ {
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: fmt.Sprintf("bulk-%d", i), Content: fmt.Sprintf("checkpoint note %d", i), Collection: "kb"}); err != nil {
			t.Fatal(err)
		}
	}
	if r := searchCountingStages(t, db, "why does the WAL checkpoint file keep growing"); r.trigram != 0 || r.scan != 1 {
		t.Fatalf("df-trigram ran %d times, df-scan %d, want 0 and 1", r.trigram, r.scan)
	}
}

// A trigram index that cannot be read while probing fails the search with
// its error.
func TestTrigramProbesThatCannotBeReadFail(t *testing.T) {
	db := openSQLiteLexical(t)
	saveTrigramFloorDocs(t, db)
	if _, err := db.SQL().Exec(`ALTER TABLE embeddings RENAME COLUMN content TO content_hidden`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.chunkStatsByTrigrams(context.Background(), "kb", []string{"checkpoint"}); err == nil || !strings.Contains(err.Error(), "probe term frequencies") {
		t.Fatalf("err %v, want the probe's error", err)
	}
}

// A count in a collection reads the chunks the trigram index names, by rowid,
// not every chunk of the collection.
func TestTrigramCountInCollectionReadsTheIndexedRows(t *testing.T) {
	db := openSQLiteLexical(t)
	saveTrigramFloorDocs(t, db)
	rows, err := db.SQL().Query(`EXPLAIN QUERY PLAN `+trigramCountInCollection, "kb", "%checkpoint%")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail + "\n")
	}
	if p := plan.String(); strings.Contains(p, "idx_embeddings_collection_id") || !strings.Contains(p, "INTEGER PRIMARY KEY") {
		t.Fatalf("plan:\n%s\nwant embeddings read by rowid, not by the collection index", p)
	}
}
