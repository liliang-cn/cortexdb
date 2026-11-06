package liveview

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// leoBrain opens a real brain and writes into it what the Changes page exists
// to explain: a schema in which lives_in is single-valued, three entities, and
// Leo living in Beijing and then in Chengdu (examples/21_history does the
// same). Nothing is faked: the items under test are made from the events the
// store's own triggers wrote.
func leoBrain(t *testing.T, opts ...cortexdb.Option) *cortexdb.DB {
	t.Helper()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "leo.db")), opts...)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	str := cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}
	people := cortexdb.OntologySchema{
		SchemaID: "people", Name: "People",
		ObjectTypes: []cortexdb.OntologyObjectType{
			{APIName: "Person", PrimaryKey: "personId", Properties: []cortexdb.OntologyProperty{{APIName: "personId", DataType: str, Required: true}}},
			{APIName: "City", PrimaryKey: "cityId", Properties: []cortexdb.OntologyProperty{{APIName: "cityId", DataType: str, Required: true}}},
		},
		LinkTypes: []cortexdb.OntologyLinkType{{
			APIName: "lives_in",
			A:       cortexdb.OntologyLinkSide{APIName: "city", ObjectTypeAPIName: "Person", Cardinality: cortexdb.OntologyCardinalityOne},
			B:       cortexdb.OntologyLinkSide{APIName: "residents", ObjectTypeAPIName: "City", Cardinality: cortexdb.OntologyCardinalityMany},
		}},
	}
	if _, err := db.SaveOntologySchema(ctx, cortexdb.OntologySaveRequest{Schema: people, Activate: true}); err != nil {
		t.Fatalf("save schema: %v", err)
	}
	entity := func(name, typ, pk string) cortexdb.ToolEntityInput {
		return cortexdb.ToolEntityInput{Name: name, Type: typ, Metadata: map[string]string{pk: name}}
	}
	if _, err := db.GraphRAGTools().UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: []cortexdb.ToolEntityInput{
		entity("Leo", "Person", "personId"), entity("Beijing", "City", "cityId"), entity("Chengdu", "City", "cityId"),
	}}); err != nil {
		t.Fatalf("entities: %v", err)
	}
	livesIn(t, db, "Beijing")
	time.Sleep(20 * time.Millisecond)
	livesIn(t, db, "Chengdu")
	return db
}

func livesIn(t *testing.T, db *cortexdb.DB, city string) {
	t.Helper()
	if _, err := db.GraphRAGTools().UpsertRelations(context.Background(), cortexdb.ToolUpsertRelationsRequest{
		Relations: []cortexdb.ToolRelationInput{{From: "Leo", To: city, Type: "lives_in"}},
	}); err != nil {
		t.Fatalf("Leo lives_in %s: %v", city, err)
	}
}

// allItems reads the whole feed through the same ChangeLog hook the stream
// uses and returns the items oldest first.
func allItems(t *testing.T, db *cortexdb.DB) []ChangeItem {
	t.Helper()
	log := localChangeLog(db)
	var items []ChangeItem
	var after int64
	for {
		r, err := log(context.Background(), cortexdb.ChangesSinceRequest{After: after, Limit: 1000, Explain: true})
		if err != nil {
			t.Fatalf("changes_since: %v", err)
		}
		items = append(items, ChangeItems(r.Events, Snapshot{}, r.Labels)...)
		if !r.More {
			return items
		}
		after = r.NextCursor
	}
}

func find(t *testing.T, items []ChangeItem, kind, op, summary string) ChangeItem {
	t.Helper()
	for _, it := range items {
		if it.Kind == kind && it.Op == op && it.Summary == summary {
			return it
		}
	}
	t.Fatalf("no %s %s %q among %d items", kind, op, summary, len(items))
	return ChangeItem{}
}

func TestEachKindOfChangeReadsAsOneSentence(t *testing.T) {
	db := leoBrain(t)
	ctx := context.Background()
	if _, err := db.SaveMemory(ctx, cortexdb.MemorySaveRequest{MemoryID: "m1", UserID: "u1", Content: "prefers light UI"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{KnowledgeID: "k1", Title: "Tea", Content: "Tea is a drink."}); err != nil {
		t.Fatal(err)
	}
	items := allItems(t, db)

	leo := find(t, items, graph.ChangeKindNode, OpAdded, "Person Leo")
	if leo.Node == nil || leo.Node.ID != "entity:person:leo" || leo.Node.Label != "Leo" || leo.Node.Type != "Person" {
		t.Errorf("node ref = %+v", leo.Node)
	}
	if len(leo.Entities) != 1 || leo.Entities[0] != "entity:person:leo" {
		t.Errorf("entities = %v, want the node itself", leo.Entities)
	}

	beijing := find(t, items, graph.ChangeKindEdge, OpAdded, "Leo lives_in Beijing")
	if beijing.Edge == nil || beijing.Edge.Predicate != "lives_in" || beijing.Edge.Source.Label != "Leo" || beijing.Edge.Target.Label != "Beijing" {
		t.Errorf("edge ref = %+v", beijing.Edge)
	}
	if len(beijing.Entities) != 2 {
		t.Errorf("an edge touches both its ends, got %v", beijing.Entities)
	}
	if beijing.Replaces != nil || beijing.ReplacedBy != nil {
		t.Errorf("the first value replaced nothing: %+v", beijing)
	}

	find(t, items, graph.ChangeKindMemory, OpAdded, `memory "prefers light UI"`)
	find(t, items, graph.ChangeKindKnowledge, OpAdded, `document "Tea"`)
	find(t, items, graph.ChangeKindOntologySchema, OpAdded, "schema People v1")
}

func TestASupersededEdgeNamesWhatReplacedItAndTheSchemaVersion(t *testing.T) {
	items := allItems(t, leoBrain(t))

	old := find(t, items, graph.ChangeKindEdge, OpSuperseded, "Leo lives_in Beijing")
	if old.Reason != "superseded" || old.Producer != "graphrag.upsert_relations" {
		t.Errorf("reason %q producer %q", old.Reason, old.Producer)
	}
	if old.ReplacedBy == nil || old.ReplacedBy.Summary != "Leo lives_in Chengdu" || old.ReplacedBy.ID == "" {
		t.Errorf("replaced_by = %+v, want the Chengdu edge", old.ReplacedBy)
	}
	if old.Schema == nil || old.Schema.ID != "people" || old.Schema.Version != 1 {
		t.Errorf("schema = %+v, want people v1", old.Schema)
	}
	raw, _ := json.Marshal(old)
	t.Logf("a superseded edge as /api/changes returns it:\n%s", raw)
}

func TestTheNewEdgeNamesTheOneItReplaced(t *testing.T) {
	items := allItems(t, leoBrain(t))

	added := find(t, items, graph.ChangeKindEdge, OpAdded, "Leo lives_in Chengdu")
	if added.Replaces == nil || added.Replaces.Summary != "Leo lives_in Beijing" {
		t.Fatalf("replaces = %+v, want the Beijing edge", added.Replaces)
	}
	if added.Schema == nil || added.Schema.ID != "people" || added.Schema.Version != 1 {
		t.Errorf("the closure was decided under people v1, the new row should say so: %+v", added.Schema)
	}
}

// The mapping is the contract's table, row by row.
func TestFeedOpsAndReasonsMapToTheContractsOps(t *testing.T) {
	for _, c := range []struct{ op, reason, want string }{
		{"insert", "", OpAdded},
		{"insert", "superseded", OpAdded},
		{"update", "", OpUpdated},
		{"update", "orphaned", OpUpdated},
		{"update", "superseded", OpSuperseded},
		{"update", "retracted", OpRetracted},
		{"update", "merged", OpMerged},
		{"supersede", "", OpSuperseded},
		{"merge", "", OpMerged},
		{"delete", "", OpDeleted},
		{"delete", "document_deleted", OpDeleted},
		{"delete", "superseded", OpSuperseded},
		{"delete", "retracted", OpRetracted},
		{"delete", "merged", OpMerged},
	} {
		if got := changeOp(c.op, c.reason); got != c.want {
			t.Errorf("%s with reason %q is %q, want %q", c.op, c.reason, got, c.want)
		}
	}
}

func TestADeletedNodeIsReadFromItsOwnRow(t *testing.T) {
	ev := graph.ChangeEvent{Seq: 9, Kind: graph.ChangeKindNode, Op: "delete", ID: "entity:person:gone", Reason: "retracted",
		Before: json.RawMessage(`{"content":"Gone Person","node_type":"Person","properties":{"_grade":"held"}}`)}
	items := ChangeItems([]graph.ChangeEvent{ev}, Snapshot{}, nil)
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	it := items[0]
	if it.Op != OpRetracted || it.Summary != "Person Gone Person" || it.Grade != "held" || it.Node.Label != "Gone Person" {
		t.Errorf("item = %+v", it)
	}
}

// The graph the view holds wins over the store's labels and the row's own:
// it is what the page is drawing.
func TestLabelsComeFromTheGraphThenTheStoreThenTheId(t *testing.T) {
	ev := graph.ChangeEvent{Seq: 1, Kind: graph.ChangeKindEdge, Op: "insert", ID: "e1",
		After: json.RawMessage(`{"from":"entity:a","to":"entity:b","edge_type":"knows"}`)}
	snap := Snapshot{Nodes: []Node{{ID: "entity:a", Label: "Alice"}}}
	it := ChangeItems([]graph.ChangeEvent{ev}, snap, map[string]string{"entity:a": "ignored", "entity:b": "Bob"})[0]
	if it.Summary != "Alice knows Bob" {
		t.Errorf("summary = %q", it.Summary)
	}
	it = ChangeItems([]graph.ChangeEvent{ev}, Snapshot{}, nil)[0]
	if it.Summary != "a knows b" {
		t.Errorf("with nothing known the id's last segment is the label, got %q", it.Summary)
	}
}

// A search rewrites recall_count and last_recalled_at on every memory it
// returns. Those are usage accounting, not changes.
func TestAMemoryThatOnlyWasRecalledIsNotAChange(t *testing.T) {
	db := leoBrain(t)
	ctx := context.Background()
	if _, err := db.SaveMemory(ctx, cortexdb.MemorySaveRequest{MemoryID: "m1", UserID: "u1", Content: "prefers light UI"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SearchMemory(ctx, cortexdb.MemorySearchRequest{UserID: "u1", Query: "light UI"}); err != nil {
		t.Fatal(err)
	}
	// The feed does hold the bump; only the reading drops it.
	raw, err := db.ChangesSince(ctx, cortexdb.ChangesSinceRequest{After: 0, Limit: 1000, Kinds: []string{graph.ChangeKindMemory}})
	if err != nil || len(raw.Events) != 2 {
		t.Fatalf("the feed holds %d memory events (%v), want the insert and the recall's update", len(raw.Events), err)
	}
	var memory []ChangeItem
	for _, it := range allItems(t, db) {
		if it.Kind == graph.ChangeKindMemory {
			memory = append(memory, it)
		}
	}
	if len(memory) != 1 || memory[0].Op != OpAdded {
		t.Fatalf("memory items = %+v, want only the save", memory)
	}

	// Editing the memory is a change, and stays one even though it is also
	// recalled after.
	if _, err := db.SaveMemory(ctx, cortexdb.MemorySaveRequest{MemoryID: "m1", UserID: "u1", Content: "prefers dark UI"}); err != nil {
		t.Fatal(err)
	}
	memory = memory[:0]
	for _, it := range allItems(t, db) {
		if it.Kind == graph.ChangeKindMemory {
			memory = append(memory, it)
		}
	}
	if len(memory) != 2 || memory[1].Op != OpUpdated || memory[1].Summary != `memory "prefers dark UI"` {
		t.Fatalf("memory items = %+v, want the save and the edit", memory)
	}
}

func TestHubCursorMovesWithTheChangesItPublishesUnderOneLock(t *testing.T) {
	h := NewHub()
	h.publishChanges([]ChangeItem{{Seq: 3}, {Seq: 4}}, 6)
	ch, _, _, cursor := h.subscribe()
	if cursor != 6 {
		t.Fatalf("a subscriber starts at the cursor %d, want 6: items up to it were published before it listened", cursor)
	}
	h.publishChanges([]ChangeItem{{Seq: 7}}, 9)
	if got := (<-ch).Change; got == nil || got.Seq != 7 {
		t.Fatalf("the change after the cursor did not reach the subscriber: %+v", got)
	}
	h.publishChanges(nil, 5)
	if h.changeCursor() != 9 {
		t.Errorf("the cursor went backwards to %d", h.changeCursor())
	}
}

/* ---- the stream ---- */

func liveServer(t *testing.T, db *cortexdb.DB) *Server {
	t.Helper()
	return serveSource(t, SourceFor(db, "leo"))
}

func changeSeqs(t *testing.T, frames <-chan sseFrame, n int) []int64 {
	t.Helper()
	var seqs []int64
	for len(seqs) < n {
		f := nextFrame(t, frames, "change")
		var it ChangeItem
		mustJSON(t, f.data, &it)
		if f.id != strconv.FormatInt(it.Seq, 10) {
			t.Fatalf("the SSE id %q is not the change's seq %d", f.id, it.Seq)
		}
		seqs = append(seqs, it.Seq)
	}
	return seqs
}

func TestTheSnapshotCarriesTheCursorAndSaysWhetherThereIsAFeed(t *testing.T) {
	db := leoBrain(t)
	head, err := db.ChangesHead(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sv := liveServer(t, db)
	frames, stop := openStream(t, sv.URL()+"/api/stream")
	defer stop()
	var open Payload
	mustJSON(t, nextFrame(t, frames, "snapshot").data, &open)
	if open.Cursor != head || !open.Changes {
		t.Errorf("cursor %d changes %v, want %d true", open.Cursor, open.Changes, head)
	}

	// A source with no feed says so, and its cursor is 0.
	f := &fakeSource{}
	plain := startTestServer(t, f, false)
	frames2, stop2 := openStream(t, plain.URL()+"/api/stream")
	defer stop2()
	var none Payload
	mustJSON(t, nextFrame(t, frames2, "snapshot").data, &none)
	if none.Cursor != 0 || none.Changes {
		t.Errorf("a source with no feed claims cursor %d changes %v", none.Cursor, none.Changes)
	}
}

func TestALiveWriteArrivesAsAChangeWithItsSeqAsTheEventID(t *testing.T) {
	db := leoBrain(t)
	sv := liveServer(t, db)
	frames, stop := openStream(t, sv.URL()+"/api/stream")
	defer stop()
	nextFrame(t, frames, "snapshot")

	if _, err := db.SaveMemory(context.Background(), cortexdb.MemorySaveRequest{MemoryID: "live", UserID: "u", Content: "written while watching"}); err != nil {
		t.Fatal(err)
	}
	f := nextFrame(t, frames, "change")
	var it ChangeItem
	mustJSON(t, f.data, &it)
	if it.Kind != graph.ChangeKindMemory || it.Op != OpAdded || it.Summary != `memory "written while watching"` {
		t.Errorf("change = %+v", it)
	}
	if f.id == "" || f.id != strconv.FormatInt(it.Seq, 10) {
		t.Errorf("id %q for seq %d", f.id, it.Seq)
	}
}

// The browser reconnects by itself with the id of the last change it saw. What
// was written while it was away comes back once each, in order, and the live
// tail joins on with neither a gap nor a repeat.
func TestAReconnectWithLastEventIDReplaysExactlyTheMissedChangesInOrder(t *testing.T) {
	db := leoBrain(t)
	sv := liveServer(t, db)
	ctx := context.Background()

	frames, stop := openStream(t, sv.URL()+"/api/stream")
	nextFrame(t, frames, "snapshot")
	save := func(id string) {
		t.Helper()
		if _, err := db.SaveMemory(ctx, cortexdb.MemorySaveRequest{MemoryID: id, UserID: "u", Content: "memory " + id}); err != nil {
			t.Fatal(err)
		}
	}
	save("seen")
	seen := changeSeqs(t, frames, 1)
	stop() // the connection drops

	for _, id := range []string{"missed1", "missed2", "missed3"} {
		save(id)
	}
	// The poller has to have read them for the replay to be bounded by its
	// cursor, but the replay must not depend on that: reconnect straight away.
	time.Sleep(150 * time.Millisecond)
	head, _ := db.ChangesHead(ctx)

	frames, stop = openStreamWith(t, sv.URL()+"/api/stream", map[string]string{"Last-Event-ID": strconv.FormatInt(seen[0], 10)})
	defer stop()
	first := nextFrame(t, frames, "snapshot")
	var open Payload
	mustJSON(t, first.data, &open)
	if first.id != "" {
		t.Errorf("the snapshot carries id %q; only changes do", first.id)
	}
	got := changeSeqs(t, frames, 3)
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("replayed out of order: %v", got)
		}
	}
	if got[0] <= seen[0] || got[2] != head {
		t.Fatalf("replayed %v after %d, want exactly the three written since, ending at head %d", got, seen[0], head)
	}

	// And now live: the next write is the next change, not a repeat.
	save("after")
	next := changeSeqs(t, frames, 1)
	if next[0] <= head {
		t.Fatalf("live change %d is not past the replayed head %d", next[0], head)
	}
	select {
	case f := <-frames:
		if f.event == "change" {
			t.Fatalf("an extra change followed: %+v", f)
		}
	case <-time.After(150 * time.Millisecond):
	}
}

func TestTheAfterParameterResumesLikeTheHeader(t *testing.T) {
	db := leoBrain(t)
	sv := liveServer(t, db)
	ctx := context.Background()
	all := allItems(t, db)
	cut := all[len(all)-3].Seq // the last two edges' events are after it

	frames, stop := openStream(t, sv.URL()+"/api/stream?after="+strconv.FormatInt(cut, 10))
	defer stop()
	nextFrame(t, frames, "snapshot")
	got := changeSeqs(t, frames, 2)
	if got[0] != all[len(all)-2].Seq || got[1] != all[len(all)-1].Seq {
		t.Errorf("replayed %v, want the last two seqs %d and %d", got, all[len(all)-2].Seq, all[len(all)-1].Seq)
	}
	_ = ctx
}

// A cursor older than retention cannot be replayed from. The page is told so,
// handed a fresh snapshot, and then follows the brain from its head.
func TestAPrunedCursorYieldsAResetThenASnapshotThenTheLiveTail(t *testing.T) {
	db := leoBrain(t, cortexdb.WithChangeFeed(cortexdb.ChangeFeedOptions{Retention: graph.ChangeFeedRetention{MaxRows: 2}}))
	ctx := context.Background()
	if _, err := db.PruneChanges(ctx); err != nil {
		t.Fatal(err)
	}
	sv := liveServer(t, db)

	frames, stop := openStreamWith(t, sv.URL()+"/api/stream", map[string]string{"Last-Event-ID": "1"})
	defer stop()
	first := <-frames
	if first.event != "reset" {
		t.Fatalf("first frame is %q, want reset", first.event)
	}
	var r ResetNotice
	mustJSON(t, first.data, &r)
	head, _ := db.ChangesHead(ctx)
	if r.Reason != "pruned" || r.PrunedThrough == 0 || r.Head != head {
		t.Errorf("reset = %+v, want pruned through something with head %d", r, head)
	}
	second := <-frames
	if second.event != "snapshot" {
		t.Fatalf("second frame is %q, want snapshot", second.event)
	}
	var open Payload
	mustJSON(t, second.data, &open)
	if open.Cursor != head {
		t.Errorf("the snapshot's cursor is %d, want the head %d", open.Cursor, head)
	}

	if _, err := db.SaveMemory(ctx, cortexdb.MemorySaveRequest{MemoryID: "after", UserID: "u", Content: "after the reset"}); err != nil {
		t.Fatal(err)
	}
	seqs := changeSeqs(t, frames, 1)
	if seqs[0] <= head {
		t.Errorf("the live tail started at %d, not past the head %d", seqs[0], head)
	}
}

func TestACursorFromABrainFurtherAlongIsResetNotReplayed(t *testing.T) {
	db := leoBrain(t)
	sv := liveServer(t, db)
	frames, stop := openStream(t, sv.URL()+"/api/stream?after=999999")
	defer stop()
	var r ResetNotice
	mustJSON(t, nextFrame(t, frames, "reset").data, &r)
	if r.Reason != "ahead" {
		t.Errorf("reason = %q", r.Reason)
	}
	nextFrame(t, frames, "snapshot")
}

/* ---- /api/changes ---- */

func getChanges(t *testing.T, sv *Server, query string) ChangesPage {
	t.Helper()
	var page ChangesPage
	decodeGet(t, sv.URL()+"/api/changes"+query, &page)
	return page
}

func seqsOf(items []ChangeItem) []int64 {
	out := make([]int64, len(items))
	for i, it := range items {
		out[i] = it.Seq
	}
	return out
}

func TestTheChangesPageListsNewestFirstAndPagesBackThroughTheWholeFeed(t *testing.T) {
	db := leoBrain(t)
	sv := liveServer(t, db)
	all := allItems(t, db)

	var got []ChangeItem
	query := "?limit=3"
	for pages := 0; ; pages++ {
		if pages > len(all) {
			t.Fatal("paging never ended")
		}
		page := getChanges(t, sv, query)
		if page.Head == 0 {
			t.Fatal("no head")
		}
		if len(page.Items) > 3 {
			t.Fatalf("a page of %d, limit was 3", len(page.Items))
		}
		got = append(got, page.Items...)
		if page.NextBefore == 0 {
			break
		}
		query = fmt.Sprintf("?limit=3&before=%d", page.NextBefore)
	}
	if len(got) != len(all) {
		t.Fatalf("paged %d items, the feed has %d", len(got), len(all))
	}
	for i, it := range got {
		if want := all[len(all)-1-i]; it.Seq != want.Seq {
			t.Fatalf("item %d is seq %d, want %d (newest first, none missing or repeated)", i, it.Seq, want.Seq)
		}
	}
}

func TestTheChangesPageFiltersByKindOpProducerAndEntityAndOrsRepeats(t *testing.T) {
	db := leoBrain(t)
	sv := liveServer(t, db)
	if _, err := db.SaveMemory(context.Background(), cortexdb.MemorySaveRequest{MemoryID: "m", UserID: "u", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	// Let the poller publish, so labels come from the graph too.
	time.Sleep(150 * time.Millisecond)

	for _, c := range []struct {
		name, query string
		check       func(ChangeItem) bool
		min         int
	}{
		{"kind", "?kind=edge", func(i ChangeItem) bool { return i.Kind == "edge" }, 3},
		{"kinds ORed", "?kind=edge&kind=memory", func(i ChangeItem) bool { return i.Kind == "edge" || i.Kind == "memory" }, 4},
		{"op", "?op=superseded", func(i ChangeItem) bool { return i.Op == OpSuperseded }, 1},
		{"ops ORed", "?op=superseded&op=updated", func(i ChangeItem) bool { return i.Op == OpSuperseded || i.Op == OpUpdated }, 1},
		{"producer", "?producer=graphrag.upsert_relations", func(i ChangeItem) bool { return i.Producer == "graphrag.upsert_relations" }, 1},
		{"entity", "?entity=entity:city:beijing", func(i ChangeItem) bool {
			for _, e := range i.Entities {
				if e == "entity:city:beijing" {
					return true
				}
			}
			return false
		}, 3},
		{"filters ANDed", "?kind=edge&op=superseded", func(i ChangeItem) bool { return i.Kind == "edge" && i.Op == OpSuperseded }, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			page := getChanges(t, sv, c.query)
			if len(page.Items) < c.min {
				t.Fatalf("%d items, want at least %d", len(page.Items), c.min)
			}
			for _, it := range page.Items {
				if !c.check(it) {
					t.Errorf("%s let through %+v", c.query, it)
				}
			}
		})
	}
	if got := getChanges(t, sv, "?kind=edge&op=superseded"); len(got.Items) != 1 {
		t.Errorf("one edge was superseded, got %d", len(got.Items))
	}

	// A filter pages too: the cursor it returns continues the same filter.
	p1 := getChanges(t, sv, "?kind=edge&limit=1")
	p2 := getChanges(t, sv, fmt.Sprintf("?kind=edge&limit=1&before=%d", p1.NextBefore))
	if len(p1.Items) != 1 || len(p2.Items) != 1 || p2.Items[0].Seq >= p1.Items[0].Seq || p2.Items[0].Kind != "edge" {
		t.Errorf("filtered paging: %v then %v", seqsOf(p1.Items), seqsOf(p2.Items))
	}
}

func TestTheChangesPageAfterReturnsOnlyNewerItemsStillNewestFirst(t *testing.T) {
	db := leoBrain(t)
	sv := liveServer(t, db)
	all := allItems(t, db)
	cut := all[len(all)-4].Seq

	page := getChanges(t, sv, fmt.Sprintf("?after=%d", cut))
	want := []int64{all[len(all)-1].Seq, all[len(all)-2].Seq, all[len(all)-3].Seq}
	got := seqsOf(page.Items)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("after %d: %v, want %v", cut, got, want)
	}
	if page.NextBefore != 0 {
		t.Errorf("nothing older than the cursor should be offered, next_before = %d", page.NextBefore)
	}
}

func TestTheChangesPageIsEmptyAndOKWhenTheSourceHasNoFeed(t *testing.T) {
	f := &fakeSource{}
	sv := startTestServer(t, f, false)
	resp, err := http.Get(sv.URL() + "/api/changes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || string(raw["items"]) != "[]" || string(raw["head"]) != "0" {
		t.Errorf("status %d body %v", resp.StatusCode, raw)
	}
}

func TestTheChangesPageRefusesBadParameters(t *testing.T) {
	sv := liveServer(t, leoBrain(t))
	for _, q := range []string{"?limit=x", "?before=-3", "?after=y"} {
		resp, err := http.Get(sv.URL() + "/api/changes" + q)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s answered %d", q, resp.StatusCode)
		}
	}
	page := getChanges(t, sv, "?limit=100000")
	if len(page.Items) > changesMaxLimit {
		t.Errorf("limit is capped at %d, got %d", changesMaxLimit, len(page.Items))
	}
}

/* ---- /api/entity ---- */

func TestTheEntityPageGroupsRelationsByPredicateLargestFirstBothWays(t *testing.T) {
	f := &fakeSource{}
	f.set(
		[]Node{{ID: "n:leo", Label: "Leo", Type: "Person"}, {ID: "n:b", Label: "Beijing", Type: "City"}, {ID: "n:c", Label: "Chengdu", Type: "City"},
			{ID: "n:x", Label: "Xi", Type: "Person"}, {ID: "n:y", Label: "Yu", Type: "Person"}},
		[]Edge{
			{ID: "e1", Source: "n:leo", Target: "n:b", Label: "visited"},
			{ID: "e2", Source: "n:leo", Target: "n:c", Label: "visited"},
			{ID: "e3", Source: "n:leo", Target: "n:c", Label: "lives_in", Grade: "verified"},
			{ID: "e4", Source: "n:leo", Target: "n:x", Label: "co_occurs"},
			{ID: "e5", Source: "n:x", Target: "n:leo", Label: "knows"},
			{ID: "e6", Source: "n:y", Target: "n:leo", Label: "knows"},
			{ID: "e7", Source: "n:x", Target: "n:y", Label: "knows"},
		})
	sv := startTestServer(t, f, false)

	var v EntityView
	decodeGet(t, sv.URL()+"/api/entity?id=n:leo", &v)
	if v.Node == nil || v.Node.Label != "Leo" {
		t.Fatalf("node = %+v", v.Node)
	}
	var out []string
	for _, g := range v.Out {
		out = append(out, fmt.Sprintf("%s:%d", g.Predicate, len(g.Edges)))
	}
	if fmt.Sprint(out) != "[visited:2 co_occurs:1 lives_in:1]" {
		t.Errorf("out groups = %v, want largest first, then by predicate", out)
	}
	if g := v.Out[0]; g.Edges[0].Other.Label != "Beijing" || g.Edges[0].EdgeID != "e1" || g.Edges[1].Other.Label != "Chengdu" {
		t.Errorf("visited edges = %+v", g.Edges)
	}
	if v.Out[2].Edges[0].Grade != "verified" {
		t.Errorf("the grade is lost: %+v", v.Out[2])
	}
	if len(v.In) != 1 || v.In[0].Predicate != "knows" || len(v.In[0].Edges) != 2 || v.In[0].Edges[0].Other.Label != "Xi" {
		t.Errorf("in groups = %+v", v.In)
	}

	// An id that is not in the graph now: null node, no relations, still 200.
	var gone EntityView
	decodeGet(t, sv.URL()+"/api/entity?id=n:nobody", &gone)
	if gone.Node != nil || len(gone.Out) != 0 || len(gone.In) != 0 {
		t.Errorf("unknown id = %+v", gone)
	}
	resp, _ := http.Get(sv.URL() + "/api/entity")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a missing id answered %d", resp.StatusCode)
	}
}

// The log can run ahead of the hub: a write committed after the subscription
// was taken is in the log and will also reach the page live. The replay stops
// at the cursor, so that write is delivered once.
func TestTheReplayStopsAtTheCursorSoTheLiveTailDoesNotRepeatIt(t *testing.T) {
	var events []graph.ChangeEvent
	for seq := int64(1); seq <= 5; seq++ {
		events = append(events, graph.ChangeEvent{Seq: seq, Kind: graph.ChangeKindMemory, Op: "insert", ID: fmt.Sprint("m", seq),
			After: json.RawMessage(`{"content":"x"}`)})
	}
	rec := httptest.NewRecorder()
	s := &Server{hub: NewHub()}
	if rn := s.replay(context.Background(), rec, rec, &cortexdb.ChangesSinceResponse{Events: events, NextCursor: 5, Head: 5}, 3); rn != nil {
		t.Fatalf("reset: %+v", rn)
	}
	if got := strings.Count(rec.Body.String(), "event: change"); got != 3 || strings.Contains(rec.Body.String(), "id: 4") {
		t.Errorf("replayed %d changes past a cursor of 3:\n%s", got, rec.Body.String())
	}
}
