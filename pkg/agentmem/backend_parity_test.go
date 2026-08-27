package agentmem_test

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/liliang-cn/cortexdb/v2/pkg/agentmem"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// One suite, both backends.
//
// agentmem was the last package on the shared handle still speaking only
// SQLite: eight tables of its own and ~90 `?` placeholders, which means it
// worked perfectly on one backend and failed at the first query on the other.
// What keeps the port honest is not the dialect layer — that only guarantees
// the SQL parses — but a suite that runs the same assertions twice and
// compares behaviour.

type backendUnderTest struct {
	name  string
	store *agentmem.Store
}

func backendsUnderTest(t *testing.T) []backendUnderTest {
	t.Helper()
	out := []backendUnderTest{{name: "sqlite", store: openSQLiteStore(t)}}

	dsn := os.Getenv("CORTEXDB_TEST_POSTGRES")
	if dsn == "" {
		t.Log("CORTEXDB_TEST_POSTGRES unset — PostgreSQL is NOT covered by this run")
		return out
	}
	return append(out, backendUnderTest{name: "postgres", store: openPostgresStore(t, dsn)})
}

func openSQLiteStore(t *testing.T) *agentmem.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), fmt.Sprintf("parity_%d.db", time.Now().UnixNano()))
	cfg := cortexdb.DefaultConfig(path)
	cfg.Dimensions = 4
	db, err := cortexdb.Open(cfg)
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := agentmem.New(db)
	if err != nil {
		t.Fatalf("sqlite agentmem.New: %v", err)
	}
	if got := string(store.Dialect().Kind()); got != "sqlite" {
		t.Fatalf("dialect = %s, want sqlite", got)
	}
	return store
}

// openPostgresStore gives this package its own schema.
//
// `go test ./...` runs packages in parallel against one database, and CREATE
// EXTENSION / CREATE TABLE IF NOT EXISTS are not atomic in PostgreSQL: two
// packages initialising at the same moment race and one loses with "duplicate
// key value violates unique constraint pg_type_typname_nsp_index", which names
// nothing a reader could act on. The failure only appears in a full run, never
// when this package is tested alone, which is the worst way for it to appear.
// public stays in the search_path so the vector type resolves.
func openPostgresStore(t *testing.T, dsn string) *agentmem.Store {
	t.Helper()
	ctx := context.Background()

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("postgres open admin: %v", err)
	}
	if _, err := admin.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		admin.Close()
		t.Fatalf("postgres extension: %v", err)
	}
	schema := fmt.Sprintf("agentmem_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatalf("postgres schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})

	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	cfg := cortexdb.DefaultConfig(dsn + sep + "search_path=" + schema + ",public")
	cfg.Dimensions = 4
	db, err := cortexdb.Open(cfg)
	if err != nil {
		t.Fatalf("postgres open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store, err := agentmem.New(db)
	if err != nil {
		t.Fatalf("postgres agentmem.New: %v", err)
	}
	if got := string(store.Dialect().Kind()); got != "postgres" {
		t.Fatalf("dialect = %s, want postgres", got)
	}
	if !store.SearchIsIndexed() {
		t.Log("pg_trgm unavailable: text search is correct but linear on this instance")
	}
	return store
}

// A memory and everything hanging off it survives the round trip on both.
//
// The side tables are the point: tags, keywords and evidence are written
// through a *prepared* statement executed once per value, which is the one
// call site a port like this forgets. Rebinding exec and query but not
// PrepareContext leaves single-row writes working and every batched write
// failing, so a memory with no tags saves and a memory with tags does not.
func TestAMemoryRoundTripsOnBothBackends(t *testing.T) {
	for _, b := range backendsUnderTest(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			scope := agentmem.Scope{Type: agentmem.ScopeUser, ID: "alice"}
			m := &agentmem.Memory{
				ID:          "round-trip",
				Scope:       scope,
				Type:        agentmem.TypeFact,
				Content:     "Apollo ships on Friday.",
				Importance:  0.8,
				Confidence:  0.25,
				Tags:        []string{"apollo", "deadline"},
				Keywords:    []string{"apollo", "friday", "ship"},
				EvidenceIDs: []string{"src-1", "src-2"},
				RevisionHistory: []agentmem.Revision{
					{At: time.Now().UTC().Add(-time.Hour), By: "leo", Summary: "first draft"},
				},
			}
			if err := b.store.Save(ctx, m); err != nil {
				t.Fatalf("Save: %v", err)
			}

			got, err := b.store.Get(ctx, "round-trip")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.Content != m.Content {
				t.Errorf("content = %q, want %q", got.Content, m.Content)
			}
			if got.Scope != scope {
				t.Errorf("scope = %+v, want %+v", got.Scope, scope)
			}
			if got.Type != agentmem.TypeFact || got.SourceType != agentmem.SourceUserInput {
				t.Errorf("type/source came back as %s/%s", got.Type, got.SourceType)
			}
			// A four-byte float would hand back 0.800000011920929 here, which
			// is the kind of divergence a threshold comparison finds in
			// production and a "does it round trip" test does not.
			if got.Importance != 0.8 {
				t.Errorf("importance = %.17g, want exactly 0.8 — the column is not double precision", got.Importance)
			}
			if got.Confidence != 0.25 {
				t.Errorf("confidence = %.17g, want 0.25", got.Confidence)
			}
			if !equalStrings(got.Tags, []string{"apollo", "deadline"}) {
				t.Errorf("tags = %v", got.Tags)
			}
			if !equalStrings(got.Keywords, []string{"apollo", "friday", "ship"}) {
				t.Errorf("keywords = %v", got.Keywords)
			}
			if !equalStrings(got.EvidenceIDs, []string{"src-1", "src-2"}) {
				t.Errorf("evidence = %v", got.EvidenceIDs)
			}
			if len(got.RevisionHistory) != 1 || got.RevisionHistory[0].By != "leo" {
				t.Errorf("revision history = %+v", got.RevisionHistory)
			}
			if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() || got.ValidFrom.IsZero() {
				t.Errorf("timestamps lost: created=%v updated=%v valid_from=%v",
					got.CreatedAt, got.UpdatedAt, got.ValidFrom)
			}

			// Saving the same id again replaces rather than duplicates, and
			// the side tables are rewritten rather than appended to. This is
			// also what exercises ON CONFLICT DO NOTHING on the tag insert,
			// which used to be INSERT OR IGNORE.
			m.Content = "Apollo slipped to Monday."
			m.Tags = []string{"apollo", "deadline", "slipped"}
			if err := b.store.Save(ctx, m); err != nil {
				t.Fatalf("second Save: %v", err)
			}
			got, err = b.store.Get(ctx, "round-trip")
			if err != nil {
				t.Fatalf("Get after re-save: %v", err)
			}
			if got.Content != "Apollo slipped to Monday." {
				t.Errorf("content = %q after re-save", got.Content)
			}
			if len(got.Tags) != 3 {
				t.Errorf("tags = %v, want 3 after re-save", got.Tags)
			}

			all, total, err := b.store.List(ctx, 50, 0)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if total != 1 || len(all) != 1 {
				t.Errorf("two saves of one id produced %d rows", total)
			}
		})
	}
}

// The same query finds the same memory on both, and ranks it above the noise.
func TestTextSearchAgreesOnBothBackends(t *testing.T) {
	for _, b := range backendsUnderTest(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			scope := agentmem.Scope{Type: agentmem.ScopeUser, ID: "alice"}
			seed := []*agentmem.Memory{
				{ID: "s1", Scope: scope, Content: "The deployment pipeline runs on GitLab CI.", Importance: 0.9,
					Tags: []string{"ci"}, Keywords: []string{"gitlab", "pipeline"}},
				{ID: "s2", Scope: scope, Content: "Coffee is restocked on Tuesdays.", Importance: 0.5},
				{ID: "s3", Scope: scope, Content: "Backups are written to object storage nightly.", Importance: 0.5},
			}
			for _, m := range seed {
				if err := b.store.Save(ctx, m); err != nil {
					t.Fatalf("Save %s: %v", m.ID, err)
				}
			}

			hits, err := b.store.SearchByText(ctx, "gitlab pipeline", agentmem.SearchOptions{TopK: 5})
			if err != nil {
				t.Fatalf("SearchByText: %v", err)
			}
			if len(hits) == 0 {
				t.Fatal("no hits for a phrase that is in the corpus")
			}
			if hits[0].Memory.ID != "s1" {
				t.Errorf("top hit = %s, want s1 (%v)", hits[0].Memory.ID, hitIDs(hits))
			}
			for i := 1; i < len(hits); i++ {
				if hits[i].Score > hits[i-1].Score {
					t.Errorf("scores do not descend: %v", hits)
				}
			}
			// A score has to mean the same thing on both, or a MinScore tuned
			// on SQLite silently returns nothing on PostgreSQL.
			if hits[0].Score <= 0 || hits[0].Score > 1 {
				t.Errorf("score = %v, want it in (0,1] on every backend", hits[0].Score)
			}

			// Nothing matches: an empty result, not an error.
			none, err := b.store.SearchByText(ctx, "helicopter", agentmem.SearchOptions{TopK: 5})
			if err != nil {
				t.Fatalf("SearchByText (no match): %v", err)
			}
			if len(none) != 0 {
				t.Errorf("a word that is nowhere in the corpus returned %v", hitIDs(none))
			}

			// Type and scope filters bind their arguments after the query's,
			// which is exactly where a rebind gets the numbering wrong.
			typed, err := b.store.SearchByText(ctx, "gitlab pipeline", agentmem.SearchOptions{
				TopK: 5, Type: agentmem.TypeObservation,
			})
			if err != nil {
				t.Fatalf("SearchByText (typed): %v", err)
			}
			if len(typed) != 0 {
				t.Errorf("a fact came back under a type filter for observations: %v", hitIDs(typed))
			}

			scoped, err := b.store.SearchByScope(ctx, "gitlab pipeline",
				[]agentmem.Scope{{Type: agentmem.ScopeUser, ID: "bob"}}, agentmem.SearchOptions{TopK: 5})
			if err != nil {
				t.Fatalf("SearchByScope: %v", err)
			}
			if len(scoped) != 0 {
				t.Errorf("alice's memory came back in bob's scope: %v", hitIDs(scoped))
			}
			scoped, err = b.store.SearchByScope(ctx, "gitlab pipeline", []agentmem.Scope{scope},
				agentmem.SearchOptions{TopK: 5})
			if err != nil {
				t.Fatalf("SearchByScope (own): %v", err)
			}
			if len(scoped) == 0 || scoped[0].Memory.ID != "s1" {
				t.Errorf("scoped search = %v, want s1 first", hitIDs(scoped))
			}
		})
	}
}

// CJK on both. tokenizeForFTS emits two-character windows precisely because
// neither backend can tokenise Chinese into words, so this is the query shape
// where the SQLite trigram table and the PostgreSQL substring match have to
// agree or the port is only half done.
func TestCJKSearchAgreesOnBothBackends(t *testing.T) {
	for _, b := range backendsUnderTest(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			if b.name == "sqlite" && b.store.UsesFallbackTokenizer() {
				t.Skip("trigram tokenizer unavailable; CJK substring search not guaranteed")
			}
			scope := agentmem.Scope{Type: agentmem.ScopeUser, ID: "cjk"}
			if err := b.store.Save(ctx, &agentmem.Memory{
				ID: "c1", Scope: scope, Content: "乘法和除法是四则运算的一部分", Importance: 0.7,
			}); err != nil {
				t.Fatalf("Save: %v", err)
			}
			if err := b.store.Save(ctx, &agentmem.Memory{
				ID: "c2", Scope: scope, Content: "周长是围绕图形一周的长度", Importance: 0.7,
			}); err != nil {
				t.Fatalf("Save: %v", err)
			}

			// Above the trigram floor, so this test stays about the port.
			// Below it is its own test now — see
			// TestShortCJKQueriesFindTheirWords, which is where that gap was
			// closed after PostgreSQL disagreed with SQLite about 乘法.
			hits, err := b.store.SearchByText(ctx, "四则运算", agentmem.SearchOptions{TopK: 5})
			if err != nil {
				t.Fatalf("SearchByText: %v", err)
			}
			if len(hits) == 0 || hits[0].Memory.ID != "c1" {
				t.Fatalf("搜索 四则运算 = %v, want c1 first", hitIDs(hits))
			}
		})
	}
}

// Archive, supersede and the revision sequence. The sequence is read back with
// COALESCE(MAX(seq), -1) + 1 inside the transaction that writes it, which is a
// different statement shape from everything else in the package.
func TestHindsightBehavesTheSameOnBothBackends(t *testing.T) {
	for _, b := range backendsUnderTest(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			scope := agentmem.Scope{Type: agentmem.ScopeAgent, ID: "planner"}
			for _, id := range []string{"old", "new"} {
				if err := b.store.Save(ctx, &agentmem.Memory{
					ID: id, Scope: scope, Content: "belief " + id, Importance: 0.6,
				}); err != nil {
					t.Fatalf("Save %s: %v", id, err)
				}
			}

			if err := b.store.MarkStale(ctx, "old", "new"); err != nil {
				t.Fatalf("MarkStale: %v", err)
			}
			stale, err := b.store.Get(ctx, "old")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if !agentmem.IsStale(stale) || stale.SupersededBy != "new" {
				t.Errorf("old is not stale: valid_to=%v superseded_by=%q", stale.ValidTo, stale.SupersededBy)
			}
			if len(stale.RevisionHistory) != 1 {
				t.Fatalf("MarkStale wrote %d revisions, want 1", len(stale.RevisionHistory))
			}

			if err := b.store.AddRevision(ctx, "old", "leo", "reviewed"); err != nil {
				t.Fatalf("AddRevision: %v", err)
			}
			stale, _ = b.store.Get(ctx, "old")
			if len(stale.RevisionHistory) != 2 {
				t.Fatalf("revision seq did not advance: %+v", stale.RevisionHistory)
			}
			if stale.RevisionHistory[1].By != "leo" || stale.RevisionHistory[1].Summary != "reviewed" {
				t.Errorf("second revision = %+v", stale.RevisionHistory[1])
			}

			if err := b.store.Archive(ctx, "new", "obsolete"); err != nil {
				t.Fatalf("Archive: %v", err)
			}
			archived, _ := b.store.Get(ctx, "new")
			if !archived.Archived || archived.ArchiveReason != "obsolete" || archived.ArchivedAt == nil {
				t.Errorf("archive flags = %+v", archived)
			}
			// ListByScope filters on `archived`, not on staleness, so the
			// superseded row is still listed and the archived one is not.
			// Both backends have to agree on that, not just on the flag.
			got, err := b.store.ListByScope(ctx, scope, 10)
			if err != nil {
				t.Fatalf("ListByScope: %v", err)
			}
			if len(got) != 1 || got[0].ID != "old" {
				t.Errorf("ListByScope = %v, want only the superseded-but-unarchived row", memIDs(got))
			}
			if err := b.store.Unarchive(ctx, "new"); err != nil {
				t.Fatalf("Unarchive: %v", err)
			}
			unarchived, _ := b.store.Get(ctx, "new")
			if unarchived.Archived || unarchived.ArchivedAt != nil || unarchived.ArchiveReason != "" {
				t.Errorf("unarchive left flags behind: %+v", unarchived)
			}

			// A missing id is ErrNotFound on both, not a silent no-op.
			if err := b.store.MarkStale(ctx, "no-such-id", "x"); err != agentmem.ErrNotFound {
				t.Errorf("MarkStale on a missing id = %v, want ErrNotFound", err)
			}
			if err := b.store.IncrementAccess(ctx, "no-such-id"); err != agentmem.ErrNotFound {
				t.Errorf("IncrementAccess on a missing id = %v, want ErrNotFound", err)
			}
			if err := b.store.IncrementAccess(ctx, "new"); err != nil {
				t.Fatalf("IncrementAccess: %v", err)
			}
			if bumped, _ := b.store.Get(ctx, "new"); bumped.AccessCount != 1 {
				t.Errorf("access_count = %d, want 1", bumped.AccessCount)
			}
		})
	}
}

// Banks, mental models and context slots: three upserts, three different
// conflict targets, one of them composite.
func TestBanksModelsAndSlotsUpsertTheSameOnBothBackends(t *testing.T) {
	for _, b := range backendsUnderTest(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			scope := agentmem.Scope{Type: agentmem.ScopeTeam, ID: "infra"}

			if _, err := b.store.GetBankConfig(ctx, scope); err != agentmem.ErrNotFound {
				t.Errorf("an unconfigured bank returned %v, want ErrNotFound", err)
			}
			cfg := &agentmem.BankConfig{
				Mission:    "keep the fleet up",
				Directives: []string{"page early", "write it down"},
				Skepticism: 3, Literalism: 2, Empathy: 1,
			}
			if err := b.store.ConfigureBank(ctx, scope, cfg); err != nil {
				t.Fatalf("ConfigureBank: %v", err)
			}
			cfg.Mission = "keep the fleet up, quietly"
			if err := b.store.ConfigureBank(ctx, scope, cfg); err != nil {
				t.Fatalf("ConfigureBank (update): %v", err)
			}
			back, err := b.store.GetBankConfig(ctx, scope)
			if err != nil {
				t.Fatalf("GetBankConfig: %v", err)
			}
			if back.Mission != cfg.Mission || len(back.Directives) != 2 || back.Skepticism != 3 {
				t.Errorf("bank config = %+v", back)
			}

			mm := &agentmem.MentalModel{
				ID: "mm-1", Name: "blast radius", Description: "before changing anything",
				Content: "ask what breaks", Tags: []string{"ops"},
			}
			if err := b.store.AddMentalModel(ctx, mm); err != nil {
				t.Fatalf("AddMentalModel: %v", err)
			}
			mm.Content = "ask what breaks, then who notices"
			if err := b.store.AddMentalModel(ctx, mm); err != nil {
				t.Fatalf("AddMentalModel (update): %v", err)
			}
			models, err := b.store.ListMentalModels(ctx)
			if err != nil {
				t.Fatalf("ListMentalModels: %v", err)
			}
			if len(models) != 1 || models[0].Content != mm.Content || len(models[0].Tags) != 1 {
				t.Errorf("mental models = %+v", models)
			}
			if err := b.store.DeleteMentalModel(ctx, "mm-1"); err != nil {
				t.Fatalf("DeleteMentalModel: %v", err)
			}
			if models, _ = b.store.ListMentalModels(ctx); len(models) != 0 {
				t.Errorf("%d mental models survived the delete", len(models))
			}

			if _, err := b.store.GetContext(ctx, scope, agentmem.SlotSoul); err != agentmem.ErrNotFound {
				t.Errorf("an unwritten slot returned %v, want ErrNotFound", err)
			}
			if err := b.store.SetContext(ctx, scope, agentmem.SlotSoul, "on-call for storage"); err != nil {
				t.Fatalf("SetContext: %v", err)
			}
			if err := b.store.AppendContext(ctx, scope, agentmem.SlotSoul, "and for the GUI"); err != nil {
				t.Fatalf("AppendContext: %v", err)
			}
			slot, err := b.store.GetContext(ctx, scope, agentmem.SlotSoul)
			if err != nil {
				t.Fatalf("GetContext: %v", err)
			}
			if !strings.Contains(slot, "on-call for storage") || !strings.Contains(slot, "and for the GUI") {
				t.Errorf("slot = %q", slot)
			}
			if err := b.store.DeleteContext(ctx, scope, agentmem.SlotSoul); err != nil {
				t.Fatalf("DeleteContext: %v", err)
			}
			if _, err := b.store.GetContext(ctx, scope, agentmem.SlotSoul); err != agentmem.ErrNotFound {
				t.Errorf("a deleted slot returned %v, want ErrNotFound", err)
			}
		})
	}
}

// Listing, the entrypoint render and the two deletes.
func TestListingAndDeletionAgreeOnBothBackends(t *testing.T) {
	for _, b := range backendsUnderTest(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			scope := agentmem.Scope{Type: agentmem.ScopeSession, ID: "s-1"}
			for i := 0; i < 5; i++ {
				if err := b.store.Save(ctx, &agentmem.Memory{
					ID:         fmt.Sprintf("l%d", i),
					Scope:      scope,
					Type:       agentmem.TypeFact,
					Content:    fmt.Sprintf("fact number %d", i),
					Importance: 0.1 * float64(i+1),
					CreatedAt:  time.Now().UTC().Add(-time.Duration(i) * time.Minute),
				}); err != nil {
					t.Fatalf("Save l%d: %v", i, err)
				}
			}

			rows, total, err := b.store.List(ctx, 3, 0)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if total != 5 || len(rows) != 3 {
				t.Fatalf("List(3,0) = %d rows of %d total, want 3 of 5", len(rows), total)
			}
			// Newest first, on both.
			if rows[0].ID != "l0" {
				t.Errorf("first row = %s, want the newest (l0): %v", rows[0].ID, memIDs(rows))
			}
			page2, _, err := b.store.List(ctx, 3, 3)
			if err != nil {
				t.Fatalf("List page 2: %v", err)
			}
			if len(page2) != 2 {
				t.Errorf("page 2 = %d rows, want 2", len(page2))
			}

			byScope, err := b.store.ListByScope(ctx, scope, 10)
			if err != nil {
				t.Fatalf("ListByScope: %v", err)
			}
			if len(byScope) != 5 || byScope[0].ID != "l4" {
				t.Errorf("ListByScope = %v, want 5 rows most-important first", memIDs(byScope))
			}
			byType, err := b.store.GetByType(ctx, agentmem.TypeFact, 10)
			if err != nil {
				t.Fatalf("GetByType: %v", err)
			}
			if len(byType) != 5 {
				t.Errorf("GetByType = %d rows, want 5", len(byType))
			}

			entry, err := b.store.BuildEntrypoint(ctx, scope, agentmem.EntrypointOptions{TopN: 2})
			if err != nil {
				t.Fatalf("BuildEntrypoint: %v", err)
			}
			if !strings.Contains(entry, "fact number 4") {
				t.Errorf("entrypoint missing the most important memory:\n%s", entry)
			}
			if strings.Count(entry, "\n## ") != 2 {
				t.Errorf("entrypoint rendered %d sections, want 2:\n%s", strings.Count(entry, "\n## "), entry)
			}

			if err := b.store.Delete(ctx, "l0"); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if _, err := b.store.Get(ctx, "l0"); err != agentmem.ErrNotFound {
				t.Errorf("a deleted memory returned %v, want ErrNotFound", err)
			}
			if _, total, _ = b.store.List(ctx, 10, 0); total != 4 {
				t.Errorf("after one delete, total = %d, want 4", total)
			}
			// The FTS row goes with it, on both — otherwise a deleted memory
			// keeps turning up in search until the id lookup drops it.
			if hits, err := b.store.SearchByText(ctx, "fact number 0", agentmem.SearchOptions{TopK: 5}); err != nil {
				t.Fatalf("SearchByText after delete: %v", err)
			} else {
				for _, h := range hits {
					if h.Memory.ID == "l0" {
						t.Error("a deleted memory is still searchable")
					}
				}
			}

			if err := b.store.Clear(ctx); err != nil {
				t.Fatalf("Clear: %v", err)
			}
			if _, total, _ = b.store.List(ctx, 10, 0); total != 0 {
				t.Errorf("after Clear, total = %d, want 0", total)
			}
			if hits, _ := b.store.SearchByText(ctx, "fact number", agentmem.SearchOptions{TopK: 5}); len(hits) != 0 {
				t.Errorf("Clear left %d rows in the search index", len(hits))
			}
		})
	}
}

// Reflect writes observations through Save and marks their predecessors stale,
// so it is the one path that exercises a transaction opened inside a
// transaction-free method on top of another.
func TestReflectAgreesOnBothBackends(t *testing.T) {
	for _, b := range backendsUnderTest(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			scope := agentmem.Scope{Type: agentmem.ScopeUser, ID: "reflect"}
			for i := 0; i < 3; i++ {
				if err := b.store.Save(ctx, &agentmem.Memory{
					ID: fmt.Sprintf("f%d", i), Scope: scope, Type: agentmem.TypeFact,
					Content: fmt.Sprintf("fact %d", i), Importance: 0.5,
				}); err != nil {
					t.Fatalf("Save: %v", err)
				}
			}
			res, err := b.store.Reflect(ctx, scope, reflectorFunc(func(facts, existing []*agentmem.Memory) ([]agentmem.Observation, error) {
				ids := make([]string, 0, len(facts))
				for _, f := range facts {
					ids = append(ids, f.ID)
				}
				return []agentmem.Observation{{
					Content: "three facts, one pattern", Confidence: 0.75, EvidenceIDs: ids,
				}}, nil
			}))
			if err != nil {
				t.Fatalf("Reflect: %v", err)
			}
			if res.Reviewed != 3 || res.Created != 1 || len(res.NewIDs) != 1 {
				t.Fatalf("Reflect result = %+v", res)
			}
			obs, err := b.store.Get(ctx, res.NewIDs[0])
			if err != nil {
				t.Fatalf("Get observation: %v", err)
			}
			if obs.Type != agentmem.TypeObservation || len(obs.EvidenceIDs) != 3 {
				t.Errorf("observation = %+v", obs)
			}
			if math.Abs(obs.Confidence-0.75) > 1e-12 {
				t.Errorf("confidence = %v, want 0.75", obs.Confidence)
			}

			// A second run sees no unevidenced facts left and says so instead
			// of consolidating the same three again.
			res2, err := b.store.Reflect(ctx, scope, reflectorFunc(func([]*agentmem.Memory, []*agentmem.Memory) ([]agentmem.Observation, error) {
				t.Error("Reflect consolidated already-evidenced facts")
				return nil, nil
			}))
			if err != nil {
				t.Fatalf("second Reflect: %v", err)
			}
			if res2.Created != 0 || res2.Note == "" {
				t.Errorf("second Reflect = %+v, want a no-op with a note", res2)
			}
		})
	}
}

type reflectorFunc func(facts, existing []*agentmem.Memory) ([]agentmem.Observation, error)

func (f reflectorFunc) Consolidate(_ context.Context, facts, existing []*agentmem.Memory) ([]agentmem.Observation, error) {
	return f(facts, existing)
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, g := range got {
		seen[g]++
	}
	for _, w := range want {
		if seen[w] == 0 {
			return false
		}
		seen[w]--
	}
	return true
}

func hitIDs(hits []agentmem.ScoredMemory) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Memory.ID)
	}
	return out
}

func memIDs(ms []*agentmem.Memory) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

// Two-character Chinese words, which is most of them: 乘法, 分数, 面积, 周长.
//
// The trigram tokenizer makes no token out of a run shorter than three
// characters, so FTS5 MATCH returned nothing for any of these — silently,
// because zero rows and no matches look identical. This package answered "no
// results" for terms its corpus is full of, until PostgreSQL matched them by
// substring and the two backends disagreed. SQLite now routes a below-floor
// query past MATCH, the way pkg/core has always done.
func TestShortCJKQueriesFindTheirWords(t *testing.T) {
	for _, b := range backendsUnderTest(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			scope := agentmem.Scope{Type: agentmem.ScopeUser, ID: "short-cjk"}
			if err := b.store.Save(ctx, &agentmem.Memory{
				ID: "s1", Scope: scope, Importance: 0.7,
				Content: "乘法和除法是四则运算的一部分，分数的加减法需要先通分",
			}); err != nil {
				t.Fatalf("Save: %v", err)
			}

			for _, q := range []string{"乘法", "分数"} {
				hits, err := b.store.SearchByText(ctx, q, agentmem.SearchOptions{TopK: 5})
				if err != nil {
					t.Fatalf("SearchByText(%q): %v", q, err)
				}
				if len(hits) == 0 {
					t.Errorf("SearchByText(%q) found nothing — the corpus contains it", q)
				}
			}
		})
	}
}
