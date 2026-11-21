package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// openChangedFact holds one fact saved three times: the budget was 30k,
// then 38k, then 45k, each version superseding the one before.
func openChangedFact(t *testing.T, opts ...Option) (*DB, [3]time.Time) {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "change.db")), opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	days := [3]time.Time{
		time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC),
	}
	versions := []struct{ id, text string }{
		{"budget-v1", "The Orion project budget is 30k EUR."},
		{"budget-v2", "The Orion project budget is 38k EUR after the March review."},
		{"budget-v3", "The Orion project budget is 45k EUR, approved by finance."},
	}
	for i, v := range versions {
		req := MemorySaveRequest{MemoryID: v.id, Scope: MemoryScopeGlobal, Content: v.text,
			Metadata: map[string]any{"source": "planning-notes"}}
		if i > 0 {
			req.Supersedes = []string{versions[i-1].id}
		}
		if _, err := db.SaveMemory(WithWriter(ctx, "writer-"+v.id), req); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "other", Scope: MemoryScopeGlobal,
		Content: "The Vega project budget is 12k EUR."}); err != nil {
		t.Fatal(err)
	}
	backdateMemory(t, db, "budget-v1", days[0], days[1])
	backdateMemory(t, db, "budget-v2", days[1], days[2])
	backdateMemory(t, db, "budget-v3", days[2], time.Time{})
	backdateMemory(t, db, "other", days[0], time.Time{})
	return db, days
}

func memoryHitIDList(resp *MemorySearchResponse) []string {
	var ids []string
	for _, h := range resp.Results {
		ids = append(ids, h.Memory.ID)
	}
	return ids
}

func TestMemorySearchHitCarriesWhatItReplaced(t *testing.T) {
	db, days := openChangedFact(t)
	ctx := context.Background()
	resp, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "Orion project budget", Scope: MemoryScopeGlobal, TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 || resp.Results[0].Memory.ID != "budget-v3" {
		t.Fatalf("top hit %v, want budget-v3", memoryHitIDList(resp))
	}
	chain := resp.Results[0].Replaces
	if len(chain) != 2 || chain[0].ID != "budget-v1" || chain[1].ID != "budget-v2" {
		t.Fatalf("replaces %+v, want budget-v1 then budget-v2", chain)
	}
	if !chain[0].SavedAt.Equal(days[0]) || !chain[0].SupersededAt.Equal(days[1]) || chain[0].SupersededBy != "budget-v2" {
		t.Errorf("v1 entry %+v", chain[0])
	}
	if !chain[1].SupersededAt.Equal(days[2]) || chain[1].SupersededBy != "budget-v3" {
		t.Errorf("v2 entry %+v", chain[1])
	}
	if !strings.Contains(chain[0].Excerpt, "30k") || chain[0].WrittenBy != "writer-budget-v1" || chain[0].Source != "planning-notes" {
		t.Errorf("v1 entry %+v", chain[0])
	}
	for _, h := range resp.Results[1:] {
		if len(h.Replaces) > 0 {
			t.Errorf("%s replaced nothing, got %+v", h.Memory.ID, h.Replaces)
		}
	}

	// The tool answer carries it in JSON.
	out, err := db.GraphRAGTools().Call(ctx, "memory_search", json.RawMessage(`{"query":"Orion project budget","scope":"global"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), `"replaces":[{"id":"budget-v1"`) || !strings.Contains(string(raw), `"superseded_at":"2026-05-10T09:00:00Z"`) {
		t.Errorf("tool JSON lacks the chain: %s", raw)
	}
}

func TestContextPackSaysAFactChanged(t *testing.T) {
	db, _ := openChangedFact(t)
	ctx := context.Background()
	resp, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: "Orion project budget", DisableKnowledge: true})
	if err != nil {
		t.Fatal(err)
	}
	want := `[budget-v3] The Orion project budget is 45k EUR, approved by finance. [changed 2026-08-20; before: "The Orion project budget is 38k EUR after the March review."; 1 earlier]`
	if !strings.Contains(resp.ContextPack.Text, want) {
		t.Fatalf("pack:\n%s\nwant line:\n%s", resp.ContextPack.Text, want)
	}
	if len(resp.Memories) == 0 || len(resp.Memories[0].Replaces) != 2 {
		t.Fatalf("recall hits lack the chain: %+v", resp.Memories)
	}

	// Under a budget too small for the memory's text, the text is cut and
	// the change still said.
	tight, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: "Orion project budget", DisableKnowledge: true, MaxMemoryItems: 1, MaxMemoryChars: 150})
	if err != nil {
		t.Fatal(err)
	}
	if len(tight.ContextPack.Text) > 150+len("[MEMORIES]\n") || !strings.Contains(tight.ContextPack.Text, "[changed 2026-08-20; before:") {
		t.Errorf("tight pack (%d chars):\n%s", len(tight.ContextPack.Text), tight.ContextPack.Text)
	}
}

func TestMemoryGetOfAReplacedMemoryPointsAtTheCurrentOne(t *testing.T) {
	db, _ := openChangedFact(t)
	ctx := context.Background()
	got, err := db.GetMemory(ctx, MemoryGetRequest{MemoryID: "budget-v1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Memory.SupersededBy != "budget-v2" || got.Memory.CurrentID != "budget-v3" {
		t.Errorf("v1: superseded_by %q current_id %q, want budget-v2 and budget-v3", got.Memory.SupersededBy, got.Memory.CurrentID)
	}
	list, err := db.GetMemory(ctx, MemoryGetRequest{MemoryIDs: []string{"budget-v2", "budget-v3"}})
	if err != nil {
		t.Fatal(err)
	}
	if list.Memories[0].CurrentID != "budget-v3" || list.Memories[1].CurrentID != "" || list.Memories[1].SupersededBy != "" {
		t.Errorf("list: %+v", list.Memories)
	}
}

func TestMemorySearchAsOf(t *testing.T) {
	db, days := openChangedFact(t)
	ctx := context.Background()
	at := func(asOf string) *MemorySearchResponse {
		t.Helper()
		resp, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "Orion project budget", Scope: MemoryScopeGlobal, TopK: 3, AsOf: asOf})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	cases := []struct {
		asOf  string
		top   string
		chain int
	}{
		{days[0].Add(24 * time.Hour).Format(time.RFC3339), "budget-v1", 0},
		{days[1].Add(-time.Second).Format(time.RFC3339), "budget-v1", 0},
		{days[1].Format(time.RFC3339), "budget-v2", 1},
		{days[2].Add(-24 * time.Hour).Format(time.RFC3339Nano), "budget-v2", 1},
		{days[2].Add(time.Hour).Format(time.RFC3339), "budget-v3", 2},
		{"", "budget-v3", 2},
	}
	for _, c := range cases {
		resp := at(c.asOf)
		ids := memoryHitIDList(resp)
		if len(ids) == 0 || ids[0] != c.top || len(resp.Results[0].Replaces) != c.chain {
			t.Errorf("as_of %q: hits %v (top chain %d), want %s with %d replaced", c.asOf, ids, len(resp.Results[0].Replaces), c.top, c.chain)
		}
		for _, id := range ids[1:] {
			if strings.HasPrefix(id, "budget-") {
				t.Errorf("as_of %q: a second version %s came back: %v", c.asOf, id, ids)
			}
		}
	}
	// Before anything about Orion was saved there is nothing to recall.
	if ids := memoryHitIDList(at(days[0].Add(-time.Hour).Format(time.RFC3339))); len(ids) != 0 {
		t.Errorf("before the first save: %v", ids)
	}
	if _, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "Orion", AsOf: "last tuesday"}); err == nil || !strings.Contains(err.Error(), "as_of") {
		t.Errorf("a malformed as_of was not refused: %v", err)
	}
	// Recall passes it to the memory half.
	rec, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: "Orion project budget", DisableKnowledge: true,
		AsOf: days[1].Add(time.Hour).Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Memories) == 0 || rec.Memories[0].Memory.ID != "budget-v2" || !strings.Contains(rec.ContextPack.Text, "[changed 2026-05-10; before: \"The Orion project budget is 30k EUR.\"]") {
		t.Errorf("recall as_of: %v\n%s", rec.Memories, rec.ContextPack.Text)
	}
}

// A replaced memory in a bucket the caller may not read is not shown to it,
// as a read by id would not show it.
func TestReplacesKeepsToTheCallerScope(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "scope.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "zeus-old", UserID: "zeus", Scope: MemoryScopeUser, Content: "The shared deploy window is Friday."}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "hermes-new", UserID: "hermes", Scope: MemoryScopeUser, Content: "The shared deploy window is Tuesday.", Supersedes: []string{"zeus-old"}}); err != nil {
		t.Fatal(err)
	}
	hermes := WithCallerScope(ctx, CallerScope{Key: "hermes", Memory: func(b MemoryBucket) bool { return b.UserID == "hermes" }})
	resp, err := db.SearchMemory(hermes, MemorySearchRequest{Query: "shared deploy window", UserID: "hermes", Scope: MemoryScopeUser})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 || len(resp.Results[0].Replaces) != 0 {
		t.Fatalf("hermes sees %+v", resp.Results)
	}
	open, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "shared deploy window", UserID: "hermes", Scope: MemoryScopeUser})
	if err != nil {
		t.Fatal(err)
	}
	if len(open.Results) != 1 || len(open.Results[0].Replaces) != 1 {
		t.Fatalf("unconfined: %+v", open.Results)
	}
}

// A long history is followed four steps back, and a wide one keeps the six
// latest replaced memories, oldest first.
func TestReplacesStopsAtItsDepthAndWidth(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "caps.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	save := func(id, text string, supersedes ...string) {
		t.Helper()
		if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: id, Scope: MemoryScopeGlobal, Content: text, Supersedes: supersedes}); err != nil {
			t.Fatal(err)
		}
	}
	day := func(n int) time.Time { return time.Date(2026, 1, n, 9, 0, 0, 0, time.UTC) }

	// Long: seven versions in a line.
	for i := 1; i <= 7; i++ {
		id := fmt.Sprintf("kiln-v%d", i)
		if i == 1 {
			save(id, "The kiln firing temperature is 1000 C.")
		} else {
			save(id, fmt.Sprintf("The kiln firing temperature is %d C.", 1000+i*10), fmt.Sprintf("kiln-v%d", i-1))
		}
	}
	for i := 1; i <= 7; i++ {
		superseded := time.Time{}
		if i < 7 {
			superseded = day(i + 1)
		}
		backdateMemory(t, db, fmt.Sprintf("kiln-v%d", i), day(i), superseded)
	}
	resp, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "kiln firing temperature", Scope: MemoryScopeGlobal, TopK: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Memory.ID != "kiln-v7" {
		t.Fatalf("top hit %v, want kiln-v7", memoryHitIDList(resp))
	}
	var got []string
	for _, r := range resp.Results[0].Replaces {
		got = append(got, r.ID)
	}
	if want := []string{"kiln-v3", "kiln-v4", "kiln-v5", "kiln-v6"}; !slices.Equal(got, want) {
		t.Errorf("long history replaces %v, want %v", got, want)
	}

	// Wide: one memory replaced three, each of which replaced two.
	var firsts []string
	for i := 0; i < 3; i++ {
		var parents []string
		for j := 0; j < 2; j++ {
			id := fmt.Sprintf("glaze-%d%d", i, j)
			save(id, fmt.Sprintf("Glaze recipe note %d%d for the celadon glaze.", i, j))
			backdateMemory(t, db, id, day(1), time.Time{})
			parents = append(parents, id)
		}
		id := fmt.Sprintf("glaze-%d", i)
		save(id, fmt.Sprintf("Glaze recipe merged %d for the celadon glaze.", i), parents...)
		firsts = append(firsts, id)
	}
	save("glaze-final", "The celadon glaze recipe is settled.", firsts...)
	for i := 0; i < 3; i++ {
		for j := 0; j < 2; j++ {
			backdateMemory(t, db, fmt.Sprintf("glaze-%d%d", i, j), day(1), day(2+i*2+j))
		}
		backdateMemory(t, db, fmt.Sprintf("glaze-%d", i), day(2), day(20+i))
	}
	backdateMemory(t, db, "glaze-final", day(25), time.Time{})
	resp, err = db.SearchMemory(ctx, MemorySearchRequest{Query: "celadon glaze recipe settled", Scope: MemoryScopeGlobal, TopK: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Memory.ID != "glaze-final" {
		t.Fatalf("top hit %v, want glaze-final", memoryHitIDList(resp))
	}
	got = got[:0]
	for _, r := range resp.Results[0].Replaces {
		got = append(got, r.ID)
	}
	if want := []string{"glaze-11", "glaze-20", "glaze-21", "glaze-0", "glaze-1", "glaze-2"}; !slices.Equal(got, want) {
		t.Errorf("wide history replaces %v, want %v", got, want)
	}
}

// A replaced memory whose superseded_at cannot be read still counts as
// replaced at every instant, and its entry carries no change time; the
// change note falls back to when the current memory was saved.
func TestReplacesWithAnUnreadableSupersededAt(t *testing.T) {
	db, days := openChangedFact(t)
	ctx := context.Background()
	row, err := db.loadMemoryRow(ctx, "budget-v2")
	if err != nil {
		t.Fatal(err)
	}
	md := cloneAnyMap(row.record.Metadata)
	md["superseded_at"] = "last Tuesday"
	raw, _ := json.Marshal(md)
	if _, err := db.exec(ctx, `UPDATE messages SET metadata = ? WHERE id = ?`, raw, "budget-v2"); err != nil {
		t.Fatal(err)
	}
	resp, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "Orion project budget", Scope: MemoryScopeGlobal, TopK: 3,
		AsOf: days[1].Add(24 * time.Hour).Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range resp.Results {
		if h.Memory.ID == "budget-v2" {
			t.Fatalf("budget-v2 returned as current at an instant with an unreadable superseded_at: %v", memoryHitIDList(resp))
		}
	}
	resp, err = db.SearchMemory(ctx, MemorySearchRequest{Query: "Orion project budget", Scope: MemoryScopeGlobal, TopK: 1})
	if err != nil {
		t.Fatal(err)
	}
	chain := resp.Results[0].Replaces
	if len(chain) != 2 || chain[1].ID != "budget-v2" || !chain[1].SupersededAt.IsZero() {
		t.Fatalf("replaces %+v, want budget-v2 last with no change time", chain)
	}
	note := memoryChangeNote(resp.Results[0])
	if !strings.Contains(note, days[2].Format("2006-01-02")) {
		t.Errorf("change note %q, want the current memory's saved date %s", note, days[2].Format("2006-01-02"))
	}
}

// An unknown change time is left out of the JSON, not printed as year 1.
func TestReplacedWithoutAChangeTimeLeavesItOut(t *testing.T) {
	raw, err := json.Marshal(MemoryReplaced{ID: "a", SavedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "superseded_at") {
		t.Errorf("unknown change time serialized: %s", raw)
	}
	raw, _ = json.Marshal(MemoryReplaced{ID: "a", SupersededAt: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)})
	if !strings.Contains(string(raw), `"superseded_at":"2026-01-03T00:00:00Z"`) {
		t.Errorf("known change time missing: %s", raw)
	}
}
