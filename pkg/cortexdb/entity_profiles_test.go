package cortexdb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func openProfileBrain(t *testing.T, opts ...Option) *DB {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "profiles.db")), opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func saveProfileDoc(t *testing.T, db *DB, id, text string, rels ...ToolRelationInput) {
	t.Helper()
	if _, err := db.SaveKnowledge(context.Background(), KnowledgeSaveRequest{KnowledgeID: id, Title: id, Content: text, Relations: rels}); err != nil {
		t.Fatalf("save %s: %v", id, err)
	}
}

// A profile says what the entity's relations are, with their times, and
// what each source says of it, citing the chunk; recall puts it ahead of
// the chunks, and keeps it until a source changes (#253).
func TestEntityProfileIsKeptAndRebuiltWhenItsSourcesChange(t *testing.T) {
	db := openProfileBrain(t)
	ctx := context.Background()
	saveProfileDoc(t, db, "ep1", "Ada Lovelace worked with Charles Babbage from 1833 until 1852 on the engine.",
		ToolRelationInput{From: "Ada Lovelace", To: "Charles Babbage", Type: "worked_with"})
	saveProfileDoc(t, db, "ep2", "Ada Lovelace wrote the first published program in 1843.")

	km := db.KnowledgeMemory()
	resp, err := km.Recall(ctx, KnowledgeMemoryRecallRequest{Query: "What did Ada Lovelace do?"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Profiles) == 0 || resp.Profiles[0].EntityID != "entity:ada_lovelace" {
		t.Fatalf("profiles %+v, want Ada Lovelace's", resp.Profiles)
	}
	p := resp.Profiles[0]
	for _, want := range []string{"mentioned in 2 documents", "Ada Lovelace —worked_with→ Charles Babbage (1833 until 1852)", "ep1: Ada Lovelace worked with Charles Babbage", "ep2: Ada Lovelace wrote the first published program", "[chunk:ep2:000]"} {
		if !strings.Contains(p.Content, want) {
			t.Errorf("the profile lacks %q:\n%s", want, p.Content)
		}
	}
	text := resp.ContextPack.Text
	if pi, ki := strings.Index(text, "[PROFILES]"), strings.Index(text, "[KNOWLEDGE]"); pi < 0 || ki >= 0 && pi > ki {
		t.Fatalf("the profile is not ahead of the knowledge:\n%s", text)
	}

	again, err := km.Recall(ctx, KnowledgeMemoryRecallRequest{Query: "Ada Lovelace"})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Profiles) == 0 || again.Profiles[0].Version != 1 || again.Profiles[0].BuiltAt != p.BuiltAt {
		t.Fatalf("an unchanged entity's profile was rebuilt: %+v", again.Profiles)
	}

	// A new source supersedes it; the old one is kept.
	saveProfileDoc(t, db, "ep3", "Ada Lovelace described the Analytical Engine as weaving algebraic patterns.")
	third, err := km.Recall(ctx, KnowledgeMemoryRecallRequest{Query: "Ada Lovelace"})
	if err != nil {
		t.Fatal(err)
	}
	if len(third.Profiles) == 0 || third.Profiles[0].Version != 2 || !strings.Contains(third.Profiles[0].Content, "mentioned in 3 documents") {
		t.Fatalf("after a new source: %+v", third.Profiles)
	}
	history, err := km.EntityProfileHistory(ctx, "entity:ada_lovelace")
	if err != nil || len(history) != 1 || history[0].Version != 1 || history[0].Content != p.Content {
		t.Fatalf("history %+v, %v", history, err)
	}

	// A retired source does too.
	if _, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "ep3"}); err != nil {
		t.Fatal(err)
	}
	fourth, err := km.EntityProfile(ctx, "entity:ada_lovelace", false, true)
	if err != nil || fourth == nil || fourth.Version != 3 || !strings.Contains(fourth.Content, "mentioned in 2 documents") {
		t.Fatalf("after a retired source: %+v, %v", fourth, err)
	}
}

// An entity one document mentions has a profile only when the caller names
// it (#253).
func TestEntityProfileOfALittleMentionedEntityNeedsItsName(t *testing.T) {
	db := openProfileBrain(t)
	ctx := context.Background()
	saveProfileDoc(t, db, "one", "Grace Hopper found a moth in the Mark II.")
	km := db.KnowledgeMemory()
	resp, err := km.Recall(ctx, KnowledgeMemoryRecallRequest{Query: "Grace Hopper"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range resp.Profiles {
		if p.EntityID == "entity:grace_hopper" {
			t.Fatalf("an entity one document mentions got a profile unasked: %+v", p)
		}
	}
	named, err := km.Recall(ctx, KnowledgeMemoryRecallRequest{Query: "Grace Hopper", EntityNames: []string{"Grace Hopper"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(named.Profiles) == 0 || named.Profiles[0].EntityID != "entity:grace_hopper" {
		t.Fatalf("named, profiles %+v", named.Profiles)
	}
	exp, err := km.ExpandEntityContext(ctx, KnowledgeMemoryExpandEntityContextRequest{EntityNames: []string{"Grace Hopper"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(exp.Profiles) != 1 || !strings.Contains(exp.ContextPack.Text, "[PROFILES]") {
		t.Fatalf("expand_entity_context: %+v\n%s", exp.Profiles, exp.ContextPack.Text)
	}
}

type profileReflector struct{ calls int }

func (r *profileReflector) Reflect(_ context.Context, req KnowledgeMemoryReflectRequest, in KnowledgeMemoryReflectInput) (*KnowledgeMemoryReflection, error) {
	r.calls++
	ids := make([]string, 0, len(in.Recall.Chunks))
	for _, c := range in.Recall.Chunks {
		ids = append(ids, c.ID)
	}
	return &KnowledgeMemoryReflection{Summary: "Profile of " + in.Recall.Query + " from " + strings.Join(ids, ","), SourceChunkIDs: ids}, nil
}

// With a reflector the profile is the model's, and a recall rebuilds one
// stale profile, serving the others as they were (#253).
func TestEntityProfileWrittenByTheReflector(t *testing.T) {
	r := &profileReflector{}
	db := openProfileBrain(t, WithKnowledgeMemoryReflector(r))
	ctx := context.Background()
	saveProfileDoc(t, db, "r1", "Alan Turing met Claude Shannon at Bell Labs.")
	saveProfileDoc(t, db, "r2", "Alan Turing and Claude Shannon talked about thinking machines.")
	km := db.KnowledgeMemory()
	resp, err := km.Recall(ctx, KnowledgeMemoryRecallRequest{Query: "Alan Turing and Claude Shannon"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Profiles) != 2 || resp.Profiles[0].BuiltBy != "reflector" || !strings.HasPrefix(resp.Profiles[0].Content, "Profile of Alan Turing") {
		t.Fatalf("profiles %+v", resp.Profiles)
	}
	saveProfileDoc(t, db, "r3", "Alan Turing and Claude Shannon both wrote about chess.")
	calls := r.calls
	stale, err := km.Recall(ctx, KnowledgeMemoryRecallRequest{Query: "Alan Turing and Claude Shannon"})
	if err != nil {
		t.Fatal(err)
	}
	if r.calls-calls != 1 {
		t.Fatalf("a recall made %d model calls for profiles, want 1", r.calls-calls)
	}
	var fresh, served int
	for _, p := range stale.Profiles {
		if p.Stale {
			served++
		} else {
			fresh++
		}
	}
	if fresh != 1 || served != 1 {
		t.Fatalf("fresh %d stale %d, want 1 and 1: %+v", fresh, served, stale.Profiles)
	}
}

// A profile is what documents say: a memory naming the entity, someone's
// own, neither appears in it nor rebuilds it (#253).
func TestEntityProfileIsNotFedByMemories(t *testing.T) {
	db := openProfileBrain(t)
	ctx := context.Background()
	saveProfileDoc(t, db, "m1", "Ada Lovelace worked with Charles Babbage.")
	saveProfileDoc(t, db, "m2", "Ada Lovelace wrote notes on the engine.")
	km := db.KnowledgeMemory()
	before, err := km.EntityProfile(ctx, "entity:ada_lovelace", false, true)
	if err != nil || before == nil {
		t.Fatalf("%+v %v", before, err)
	}
	if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "zeus", UserID: "zeus", Content: "ZEUSPRIVATE: Ada Lovelace owes Zeus money.",
		Entities: []ToolEntityInput{{Name: "Ada Lovelace"}, {Name: "Zeus"}}, Relations: []ToolRelationInput{{From: "Ada Lovelace", To: "Zeus", Type: "owes"}}}); err != nil {
		t.Fatal(err)
	}
	after, err := km.EntityProfile(ctx, "entity:ada_lovelace", false, true)
	if err != nil || after == nil {
		t.Fatalf("%+v %v", after, err)
	}
	if after.Version != before.Version || strings.Contains(after.Content, "ZEUS") || strings.Contains(after.Content, "owes") {
		t.Fatalf("a memory fed the profile: %+v", after)
	}
}

// A brain whose profile tables could not be made at open (opened read-only)
// still gets profiles, built on read and not kept (#253).
func TestEntityProfileServedWhenItCannotBeKept(t *testing.T) {
	db := openProfileBrain(t)
	ctx := context.Background()
	saveProfileDoc(t, db, "k1", "Ada Lovelace worked with Charles Babbage.")
	saveProfileDoc(t, db, "k2", "Ada Lovelace wrote notes on the engine.")
	db.entityProfilesKept = false
	resp, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{Query: "Ada Lovelace"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Profiles) == 0 || resp.Profiles[0].EntityID != "entity:ada_lovelace" {
		t.Fatalf("profiles %+v", resp.Profiles)
	}
	var n int
	if err := db.queryRow(ctx, `SELECT COUNT(*) FROM entity_profiles`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("kept %d profiles, %v", n, err)
	}
}

// Profiles have a budget of their own in the pack, over all of them, and a
// source line whose chunk the pack already shows is left out (#260).
func TestEntityProfilesKeepToTheirBudget(t *testing.T) {
	profiles := []EntityProfile{
		{EntityID: "entity:a", Content: "A, mentioned in 2 documents.\nRelations:\n- A —knows→ B\nSources:\n- d1: A said hello. [chunk:d1:000]\n- d2: A said goodbye. [chunk:d2:000]", ChunkIDs: []string{"chunk:d1:000", "chunk:d2:000"}},
		{EntityID: "entity:b", Content: "B, mentioned in 2 documents.\nRelations:\n- B —knows→ A\nSources:\n- d3: " + strings.Repeat("B talks. ", 40) + "[chunk:d3:000]", ChunkIDs: []string{"chunk:d3:000"}},
	}
	text, ids := renderProfiles(profiles, 0, map[string]bool{"chunk:d1:000": true})
	if strings.Contains(text, "hello") || !strings.Contains(text, "goodbye") {
		t.Fatalf("the source line of a chunk in the pack was kept, or another dropped:\n%s", text)
	}
	if strings.Contains(strings.Join(ids, ","), "chunk:d1:000") {
		t.Fatalf("cites a chunk whose line was left out: %v", ids)
	}
	if len(text) > DefaultMaxProfileChars {
		t.Fatalf("%d chars over the default budget %d", len(text), DefaultMaxProfileChars)
	}
	small, _ := renderProfiles(profiles, 120, nil)
	if len(small) > 120 || !strings.Contains(small, "A —knows→ B") || strings.Contains(small, "B —knows→ A") {
		t.Fatalf("a 120-char budget gave:\n%s", small)
	}
	if none, _ := renderProfiles(profiles, -1, nil); none != "" {
		t.Fatalf("-1 still rendered:\n%s", none)
	}
}

// A recall's profiles stay within max_profile_chars, -1 leaves them out, and
// the relation most documents state leads the profile (#260).
func TestRecallProfilesBudgetAndRanking(t *testing.T) {
	db := openProfileBrain(t)
	ctx := context.Background()
	saveProfileDoc(t, db, "b1", "Ada Lovelace worked with Charles Babbage on the Analytical Engine.",
		ToolRelationInput{From: "Ada Lovelace", To: "Charles Babbage", Type: "worked_with"},
		ToolRelationInput{From: "Ada Lovelace", To: "Analytical Engine", Type: "described"})
	saveProfileDoc(t, db, "b2", "Ada Lovelace and Charles Babbage wrote letters.",
		ToolRelationInput{From: "Ada Lovelace", To: "Charles Babbage", Type: "worked_with"})
	saveProfileDoc(t, db, "b3", "Ada Lovelace admired Mary Somerville.",
		ToolRelationInput{From: "Ada Lovelace", To: "Mary Somerville", Type: "admired"})
	km := db.KnowledgeMemory()
	p, err := km.EntityProfile(ctx, "entity:ada_lovelace", false, true)
	if err != nil || p == nil {
		t.Fatalf("%+v %v", p, err)
	}
	rel := p.Content[strings.Index(p.Content, "Relations:"):]
	if first := strings.SplitN(rel, "\n", 3)[1]; !strings.Contains(first, "worked_with→ Charles Babbage") {
		t.Fatalf("the relation two documents state does not lead:\n%s", p.Content)
	}
	resp, err := km.Recall(ctx, KnowledgeMemoryRecallRequest{Query: "Ada Lovelace", MaxProfileChars: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range resp.ContextPack.Sections {
		if s.Kind == "profiles" && len(s.Text) > 100 {
			t.Fatalf("profiles take %d chars of a 100-char budget", len(s.Text))
		}
	}
	off, err := km.Recall(ctx, KnowledgeMemoryRecallRequest{Query: "Ada Lovelace", MaxProfileChars: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(off.Profiles) != 0 || strings.Contains(off.ContextPack.Text, "[PROFILES]") {
		t.Fatalf("max_profile_chars -1 still gave profiles: %+v", off.Profiles)
	}
}
