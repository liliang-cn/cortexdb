package authz

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// The ids a tool call names are read off the paths the allowlist lists for
// it, nested ones included: a promotion's memory ids, a plan's document ids,
// the memories a save supersedes.
func TestTheIDsAToolNamesAreReadOffTheAllowlistedPaths(t *testing.T) {
	ids, err := JSONPathValues([]byte(`{"memory_id":"a","MEMORY_IDS":["b","","c"],"other":"x"}`), "memory_ids")
	if err != nil || strings.Join(ids, ",") != "b,c" {
		t.Fatalf("ids %v %v", ids, err)
	}
	ids, err = JSONPathValues([]byte(`{"plan":{"filters":{"document_ids":["d2","d1"]}},"plans":[{"filters":{"document_ids":["d3"]}}]}`), "plan.filters.document_ids")
	if err != nil || strings.Join(ids, ",") != "d1,d2" {
		t.Fatalf("nested ids %v %v", ids, err)
	}
	ids, err = JSONPathValues([]byte(`{"entities":[{"chunk_ids":["c1"]},{"chunk_ids":["c2","c1"]},{"name":"x"}]}`), "entities[].chunk_ids")
	if err != nil || strings.Join(ids, ",") != "c1,c2" {
		t.Fatalf("ids inside an array %v %v", ids, err)
	}
	if _, err := JSONPathValues([]byte(`[1]`), "memory_id"); err == nil {
		t.Fatal("non-object arguments yielded ids")
	}
	// A value that is neither a string nor a list of strings names no row.
	if ids, err := JSONPathValues([]byte(`{"memory_id":5,"supersedes":{"x":"y"}}`), "memory_id"); err != nil || len(ids) != 0 {
		t.Fatalf("a number named a row: %v %v", ids, err)
	}
	// A key confined to its own memories may not retire another user's: the
	// supersede is a write to that row, and an existence oracle besides.
	hermes := Scope{UserID: "hermes"}
	err = hermes.GuardToolRows(context.Background(), fakeRows{}, "memory_save", []byte(`{"memory_id":"h-2","user_id":"hermes","content":"x","supersedes":["h-1","zeus-1"]}`))
	if !errors.Is(err, ErrWithheld) {
		t.Fatalf("hermes retired zeus's memory: %v", err)
	}
	// A search may pin any document id: the collection filter, forced by the
	// argument check, decides what the pin finds, and a document of another
	// collection matches nothing, exactly as a document that does not exist.
	// Refusing it here would tell the pin apart from a missing one.
	notes := Scope{Collection: "notes"}
	err = notes.GuardToolRows(context.Background(), fakeRows{}, "knowledge_search", []byte(`{"query":"q","collection":"notes","plan":{"filters":{"document_ids":["secret-doc"]}}}`))
	if err != nil {
		t.Fatalf("a pinned document id was guarded: %v", err)
	}
}

// On every field that names a row, the guard withholds a foreign id with the
// one wording, "not found", and nothing more (no kind, no id, no
// collection), and lets a missing id through to the tool. The ports then
// spell the tool's own "not found" the same way for a confined key, which
// their tests hold end to end. The fields are read off the allowlist, so a
// field added later is held to the rule without a test being added.
func TestTheGuardWithholdsAForeignIdAndPassesAMissingOneOnEveryRowIdField(t *testing.T) {
	ctx := context.Background()
	lf := Scope{Collection: "team-acme"}
	hermes := Scope{UserID: "hermes"}
	fields := 0
	for tool, rule := range confinedTools {
		for _, field := range rule.ids {
			fields++
			scope, foreign := lf, "secret-doc"
			if field.kind == idMemory {
				scope, foreign = hermes, "zeus-1"
			}
			for _, c := range []struct {
				id   string
				want error
			}{{foreign, ErrWithheld}, {"no-such-row", nil}} {
				err := scope.GuardToolRows(ctx, fakeRows{}, tool, argsAtPath(field.path, c.id))
				if !errors.Is(err, c.want) {
					t.Errorf("%s.%s with %s: %v, want %v", tool, field.path, c.id, err, c.want)
				}
				if err != nil && err.Error() != ErrWithheld.Error() {
					t.Errorf("%s.%s: the refusal says %q, which is more than %q", tool, field.path, err, ErrWithheld)
				}
			}
		}
	}
	// document_id twice (ingest_document, ingest_media), knowledge_id four times, memory_id five times,
	// supersedes once. A field added or lost changes the count, and the
	// body of the change says which.
	if fields != 12 {
		t.Fatalf("%d id fields were checked, want 12; the allowlist changed its row-id fields", fields)
	}
}

// argsAtPath is a JSON object with one string value at an allowlist path,
// through every "[]" as a one-element array.
func argsAtPath(path, value string) []byte {
	segments := strings.Split(path, ".")
	var build func(i int) any
	build = func(i int) any {
		if i == len(segments) {
			return value
		}
		name, each := strings.CutSuffix(segments[i], "[]")
		inner := build(i + 1)
		if each {
			inner = []any{inner}
		}
		return map[string]any{name: inner}
	}
	out, _ := json.Marshal(build(0))
	return out
}

// The guard reads rows through the facade it protects. If the facade hid a
// foreign row from a read by id (as it hides foreign buckets from a search
// since v2.135.0), the guard would take the row for missing and let a write
// through: knowledge_update on a foreign id would move the document. So the
// single-row reads stay unscoped, and this test holds it with a real brain
// under the caller scope a port sets.
func TestTheRowGuardSeesForeignRowsUnderACallerScope(t *testing.T) {
	ctx := context.Background()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "guard.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.SaveMemory(ctx, cortexdb.MemorySaveRequest{MemoryID: "zeus-1", UserID: "zeus", Scope: "user", Content: "the drawer key"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{KnowledgeID: "secret-doc", Collection: "secrets", Content: "the retainer"}); err != nil {
		t.Fatal(err)
	}
	hermes := Key{ID: "hermes", Scope: Scope{UserID: "hermes"}}
	notes := Key{ID: "notes", Scope: Scope{Collection: "notes"}}
	for _, c := range []struct {
		key  Key
		tool string
		args string
	}{
		{hermes, "memory_save", `{"memory_id":"zeus-1","user_id":"hermes","content":"overwritten"}`},
		{hermes, "memory_delete", `{"memory_id":"zeus-1"}`},
		{notes, "knowledge_update", `{"knowledge_id":"secret-doc","collection":"notes","content":"overwritten"}`},
		{notes, "knowledge_delete", `{"knowledge_id":"secret-doc"}`},
	} {
		err := c.key.Scope.GuardToolRows(c.key.CallerContext(ctx), db, c.tool, []byte(c.args))
		if !errors.Is(err, ErrWithheld) {
			t.Errorf("%s under %s's caller scope: %v, want the row withheld", c.tool, c.key.ID, err)
		}
	}
	// The facade's "not found" for a row that does not exist is the
	// sentinel the guard reads, on both kinds.
	for _, c := range []struct {
		key  Key
		tool string
		args string
	}{
		{hermes, "memory_save", `{"memory_id":"no-such","user_id":"hermes","content":"new"}`},
		{notes, "knowledge_update", `{"knowledge_id":"no-such","collection":"notes","content":"new"}`},
	} {
		if err := c.key.Scope.GuardToolRows(c.key.CallerContext(ctx), db, c.tool, []byte(c.args)); err != nil {
			t.Errorf("%s of a missing row under %s's caller scope: %v, want it let through", c.tool, c.key.ID, err)
		}
	}
}

// The guard decides a document by its collection, which the facade reads
// off the document's chunks. A document whose chunks are gone has no
// collection to read (GetKnowledge then names the default one), so the
// guard cannot place it and withholds it, whatever key asks.
func TestTheRowGuardWithholdsADocumentItCannotPlace(t *testing.T) {
	ctx := context.Background()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "chunkless.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{KnowledgeID: "secret-doc", Collection: "secrets", Content: "the retainer"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Vector().DeleteByDocID(ctx, "secret-doc"); err != nil {
		t.Fatal(err)
	}
	for _, collection := range []string{"graphrag_chunks", "secrets"} {
		key := Key{ID: "k", Scope: Scope{Collection: collection}}
		err := key.Scope.GuardToolRows(key.CallerContext(ctx), db, "knowledge_get", []byte(`{"knowledge_id":"secret-doc"}`))
		if !errors.Is(err, ErrWithheld) {
			t.Errorf("a document with no chunks under a key confined to %s: %v, want it withheld", collection, err)
		}
	}
}

// plan.filters.document_ids is data, not a row id: every search that takes
// it stays inside the key's collection, so a pin to another collection's
// document finds nothing, as a pin to a document that does not exist does.
// Held on a real brain, through the tools, under the key's caller scope.
func TestAPinToAForeignDocumentFindsNothingOnARealBrain(t *testing.T) {
	ctx := context.Background()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "pin.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{KnowledgeID: "secret-doc", Collection: "secrets", Content: "Orpheus earns a secret salary."}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{KnowledgeID: "notes-doc", Collection: "notes", Content: "Orpheus plays the lyre for a salary of songs."}); err != nil {
		t.Fatal(err)
	}
	notes := Key{ID: "notes", Scope: Scope{Collection: "notes"}}
	for _, tool := range []string{"knowledge_search", "search_text", "search_graphrag_lexical"} {
		for _, pin := range []string{"secret-doc", "no-such-doc"} {
			args := []byte(`{"query":"Orpheus salary","collection":"notes","plan":{"query":"Orpheus salary","filters":{"collection":"notes","document_ids":["` + pin + `"]}}}`)
			if err := notes.AuthorizeToolArgs(tool, args); err != nil {
				t.Fatalf("%s pinned to %s was refused: %v", tool, pin, err)
			}
			got, err := db.GraphRAGTools().Call(notes.CallerContext(ctx), tool, args)
			if err != nil {
				t.Fatalf("%s pinned to %s: %v", tool, pin, err)
			}
			out, _ := json.Marshal(got)
			// The answer echoes the plan, pin included; a hit would carry the
			// text or the id under its own key.
			if strings.Contains(string(out), "secret salary") || strings.Contains(string(out), `"document_id":"secret-doc"`) || strings.Contains(string(out), `"id":"secret-doc"`) || strings.Contains(string(out), `"knowledge_id":"secret-doc"`) || strings.Contains(string(out), `"DocumentID":"secret-doc"`) {
				t.Errorf("%s pinned to %s read the secret: %s", tool, pin, out)
			}
		}
	}
	// Pinned to the key's own document, each search finds it, so the pin,
	// not an empty answer, is what keeps the secret out.
	for _, tool := range []string{"knowledge_search", "search_text", "search_graphrag_lexical"} {
		got, err := db.GraphRAGTools().Call(notes.CallerContext(ctx), tool, []byte(`{"query":"Orpheus salary","collection":"notes","plan":{"query":"Orpheus salary","filters":{"collection":"notes","document_ids":["notes-doc"]}}}`))
		if err != nil {
			t.Fatal(err)
		}
		if out, _ := json.Marshal(got); !strings.Contains(string(out), "salary of songs") {
			t.Errorf("%s pinned to the key's own document found nothing: %s", tool, out)
		}
	}
}

// fakeRows is a RowLoader with four rows: a document in `secrets` and one in
// `team-acme`, a memory of zeus and one of hermes. Everything else is not
// found.
type fakeRows struct{}

func (fakeRows) GetKnowledge(_ context.Context, req cortexdb.KnowledgeGetRequest) (*cortexdb.KnowledgeGetResponse, error) {
	switch req.KnowledgeID {
	case "secret-doc":
		return &cortexdb.KnowledgeGetResponse{Knowledge: cortexdb.KnowledgeRecord{ID: "secret-doc", Collection: "secrets", ChunkIDs: []string{"chunk:secret-doc:000"}}}, nil
	case "acme-doc":
		return &cortexdb.KnowledgeGetResponse{Knowledge: cortexdb.KnowledgeRecord{ID: "acme-doc", Collection: "team-acme", ChunkIDs: []string{"chunk:acme-doc:000"}}}, nil
	}
	return nil, core.ErrNotFound
}

func (fakeRows) GetMemory(_ context.Context, req cortexdb.MemoryGetRequest) (*cortexdb.MemoryGetResponse, error) {
	switch req.MemoryID {
	case "zeus-1":
		return &cortexdb.MemoryGetResponse{Memory: cortexdb.MemoryRecord{ID: "zeus-1", UserID: "zeus", Scope: "user", Namespace: "default"}}, nil
	case "hermes-1":
		return &cortexdb.MemoryGetResponse{Memory: cortexdb.MemoryRecord{ID: "hermes-1", UserID: "hermes", Scope: "user", Namespace: "default"}}, nil
	}
	return nil, core.ErrNotFound
}

// A confined key may name a row that does not exist yet (the write creates it
// inside the scope) or a row inside the scope, and is answered "not found" for
// a row outside it — with no hint that the row exists.
func TestAToolCallMayOnlyNameRowsWithinTheScope(t *testing.T) {
	ctx := context.Background()
	lf := Scope{Collections: []string{"team-acme", "user-lf"}}
	hermes := Scope{UserID: "hermes"}

	if err := lf.GuardToolRows(ctx, fakeRows{}, "knowledge_update", []byte(`{"knowledge_id":"new-doc","collection":"team-acme"}`)); err != nil {
		t.Fatalf("a new row was refused: %v", err)
	}
	// A row inside the scope passes, whichever store it is in.
	if err := lf.GuardToolRows(ctx, fakeRows{}, "knowledge_update", []byte(`{"knowledge_id":"acme-doc","collection":"team-acme"}`)); err != nil {
		t.Fatalf("a row of the key's own collection was refused: %v", err)
	}
	if err := hermes.GuardToolRows(ctx, fakeRows{}, "memory_update", []byte(`{"memory_id":"hermes-1","content":"y"}`)); err != nil {
		t.Fatalf("a memory of the key's own user was refused: %v", err)
	}
	for _, tool := range []string{"knowledge_update", "knowledge_save", "ingest_document", "knowledge_get", "knowledge_delete"} {
		field := "knowledge_id"
		if tool == "ingest_document" {
			field = "document_id"
		}
		err := lf.GuardToolRows(ctx, fakeRows{}, tool, []byte(`{"`+field+`":"secret-doc","collection":"team-acme"}`))
		if !errors.Is(err, ErrWithheld) {
			t.Fatalf("%s reached another collection's row: %v", tool, err)
		}
		if strings.Contains(err.Error(), "secrets") {
			t.Fatalf("%s: the refusal names the other collection: %v", tool, err)
		}
	}
	if err := hermes.GuardToolRows(ctx, fakeRows{}, "memory_save", []byte(`{"memory_id":"zeus-1","user_id":"hermes","content":"x"}`)); !errors.Is(err, ErrWithheld) {
		t.Fatalf("hermes reached zeus's memory: %v", err)
	}
	// A list of memory ids is the facade's to answer: it lists a foreign id
	// under missing, in the order asked, exactly as an id that does not
	// exist (cortexdb.DB.GetMemory). Refusing the call here would tell them
	// apart.
	if err := hermes.GuardToolRows(ctx, fakeRows{}, "memory_get", []byte(`{"memory_ids":["h-1","zeus-1"]}`)); err != nil {
		t.Fatalf("a list of memory ids was guarded: %v", err)
	}
	if err := hermes.GuardToolRows(ctx, fakeRows{}, "memory_get", []byte(`{"memory_id":"zeus-1"}`)); !errors.Is(err, ErrWithheld) {
		t.Fatalf("hermes reached zeus's memory by id: %v", err)
	}
	if err := hermes.GuardToolRows(ctx, fakeRows{}, "memory_save", []byte(`{"memory_id":"h-1","user_id":"hermes"}`)); err != nil {
		t.Fatalf("hermes refused its own new memory: %v", err)
	}
	// A collection-confined key reaches no memory row, and a user-confined key
	// no knowledge row: an uncheckable confinement is a refusal.
	if lf.PermitsMemory("hermes", "user", "default") || hermes.PermitsKnowledge("team-acme") {
		t.Fatal("an uncheckable confinement passed")
	}
	// Unconfined: no check at all.
	if err := (Scope{}).GuardToolRows(ctx, fakeRows{}, "knowledge_update", []byte(`{"knowledge_id":"secret-doc"}`)); err != nil {
		t.Fatalf("an unconfined scope was checked: %v", err)
	}
}

// errRows is a RowLoader whose reads fail. A database error is not "not
// found": it must neither let a row through nor pass for a refusal.
type errRows struct{}

func (errRows) GetKnowledge(context.Context, cortexdb.KnowledgeGetRequest) (*cortexdb.KnowledgeGetResponse, error) {
	return nil, errors.New("read failed")
}

func (errRows) GetMemory(context.Context, cortexdb.MemoryGetRequest) (*cortexdb.MemoryGetResponse, error) {
	return nil, errors.New("read failed")
}

// textNotFoundRows fails every read with an error whose text says "not
// found" without being the not-found sentinel: a lost table, a missing
// index. The guard cannot tell that from a row that exists.
type textNotFoundRows struct{}

func (textNotFoundRows) GetKnowledge(context.Context, cortexdb.KnowledgeGetRequest) (*cortexdb.KnowledgeGetResponse, error) {
	return nil, errors.New("read failed: index not found")
}

func (textNotFoundRows) GetMemory(context.Context, cortexdb.MemoryGetRequest) (*cortexdb.MemoryGetResponse, error) {
	return nil, errors.New("read failed: bucket table not found")
}

// The row guard fails closed on everything it cannot decide: arguments that
// are not an object, a tool the toolbox does not define, a read that fails.
// A tool that names no row has nothing to guard and passes.
func TestTheRowGuardFailsClosedOnWhatItCannotDecide(t *testing.T) {
	ctx := context.Background()
	lf := Scope{Collection: "team-acme"}
	hermes := Scope{UserID: "hermes"}

	if err := lf.GuardToolRows(ctx, fakeRows{}, "knowledge_update", []byte(`[1]`)); !errors.Is(err, ErrDenied) {
		t.Fatalf("non-object arguments passed the knowledge row guard: %v", err)
	}
	if err := hermes.GuardToolRows(ctx, fakeRows{}, "memory_save", []byte(`"text"`)); !errors.Is(err, ErrDenied) {
		t.Fatalf("non-object arguments passed the memory row guard: %v", err)
	}
	if err := lf.GuardToolRows(ctx, fakeRows{}, "no_such_tool", []byte(`{"knowledge_id":"x"}`)); !errors.Is(err, ErrDenied) {
		t.Fatalf("an unknown tool passed the row guard: %v", err)
	}
	if err := lf.GuardToolRows(ctx, fakeRows{}, "knowledge_search", []byte(`{"query":"q","collection":"team-acme"}`)); err != nil {
		t.Fatalf("a tool that names no row was guarded: %v", err)
	}
	for _, c := range []struct {
		scope Scope
		tool  string
		args  string
	}{
		{lf, "knowledge_update", `{"knowledge_id":"any","collection":"team-acme"}`},
		{hermes, "memory_save", `{"memory_id":"any","user_id":"hermes"}`},
	} {
		for _, loader := range []RowLoader{errRows{}, textNotFoundRows{}} {
			err := c.scope.GuardToolRows(ctx, loader, c.tool, []byte(c.args))
			if err == nil || errors.Is(err, ErrWithheld) || errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), "read failed") {
				t.Fatalf("%s with %T: a failed read was not reported as one: %v", c.tool, loader, err)
			}
		}
	}
}

// Each of the four confinable fields holds on both row rules: a memory row
// passes only with the confined value, and a knowledge row, which carries no
// memory field, never passes a key confined on one.
func TestTheRowRulesHoldEveryConfinedField(t *testing.T) {
	cases := []struct {
		name       string
		scope      Scope
		own, alien [3]string // user_id, scope, namespace of a memory row
		knowledge  bool      // whether a knowledge row in "notes" passes
	}{
		{"user_id", Scope{UserID: "hermes"}, [3]string{"hermes", "user", "default"}, [3]string{"zeus", "user", "default"}, false},
		{"scope", Scope{MemoryScope: "user"}, [3]string{"hermes", "user", "default"}, [3]string{"hermes", "session", "default"}, false},
		{"namespace", Scope{Namespace: "ops"}, [3]string{"hermes", "user", "ops"}, [3]string{"hermes", "user", "dev"}, false},
	}
	for _, c := range cases {
		if !c.scope.PermitsMemory(c.own[0], c.own[1], c.own[2]) {
			t.Errorf("%s: its own memory row was refused", c.name)
		}
		if c.scope.PermitsMemory(c.alien[0], c.alien[1], c.alien[2]) {
			t.Errorf("%s: a memory row outside the confinement passed", c.name)
		}
		if c.scope.PermitsKnowledge("notes") != c.knowledge {
			t.Errorf("%s: a knowledge row, which has no %s, passed", c.name, c.name)
		}
	}
	notes := Scope{Collection: "notes"}
	if !notes.PermitsKnowledge("notes") || notes.PermitsKnowledge("secrets") {
		t.Error("collection: the knowledge rule does not follow the collection")
	}
}
