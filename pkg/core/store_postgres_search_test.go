package core

// The three search methods PostgresStore gained, against a real PostgreSQL.
//
// Written from the questions a caller actually asks of them rather than from
// the code: can someone see a row they are not on the ACL of; does the keyword
// arm rescue a row the vectors rank badly and vice versa; does an AND/OR tree
// return exactly the rows it names; and does a filter this backend cannot
// compile say so instead of quietly matching everything.

import (
	"context"
	"os"
	"strings"
	"testing"

	"database/sql"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// The schema this file owns. `go test ./...` runs packages in parallel against
// one database and CREATE TABLE IF NOT EXISTS is not atomic, so sharing
// `public` produces failures that appear only in full runs.
const pgSearchTestSchema = "cortexdb_search_test"

func openPGSearchStore(t *testing.T) *PostgresStore {
	t.Helper()

	dsn := os.Getenv("CORTEXDB_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("CORTEXDB_TEST_POSTGRES unset — the PostgreSQL search surface " +
			"(SearchWithACL, HybridSearch, SearchWithAdvancedFilter) is NOT covered by this run")
	}

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open (admin): %v", err)
	}
	defer admin.Close()

	ctx := context.Background()
	// The extension is database-wide; creating it from the scoped connection
	// would try to put it in the test schema and lose it on the drop.
	if _, err := admin.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		t.Fatalf("create extension: %v", err)
	}
	if _, err := admin.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+pgSearchTestSchema+` CASCADE`); err != nil {
		t.Fatalf("drop schema: %v", err)
	}
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+pgSearchTestSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	// public stays on the path so the `vector` type still resolves.
	db, err := sql.Open("pgx", dsn+sep+"search_path="+pgSearchTestSchema+",public")
	if err != nil {
		t.Fatalf("open (scoped): %v", err)
	}
	if _, err := db.ExecContext(ctx, `SET search_path TO `+pgSearchTestSchema+`, public`); err != nil {
		t.Fatalf("search_path: %v", err)
	}

	cfg := DefaultConfig()
	cfg.VectorDim = 4
	store := NewPostgresStore(db, cfg)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	t.Cleanup(func() {
		db.Close()
		cleanup, err := sql.Open("pgx", dsn)
		if err != nil {
			return
		}
		defer cleanup.Close()
		if _, err := cleanup.Exec(`DROP SCHEMA IF EXISTS ` + pgSearchTestSchema + ` CASCADE`); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})
	return store
}

func seedSearch(t *testing.T, s *PostgresStore, embs ...*Embedding) {
	t.Helper()
	for _, e := range embs {
		if err := s.Upsert(context.Background(), e); err != nil {
			t.Fatalf("Upsert(%s): %v", e.ID, err)
		}
	}
}

func idsOf(results []ScoredEmbedding) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.ID
	}
	return out
}

func hasID(results []ScoredEmbedding, id string) bool {
	for _, r := range results {
		if r.ID == id {
			return true
		}
	}
	return false
}

// --- SearchWithACL -----------------------------------------------------------

func TestPostgresSearchWithACLShowsOnlyWhatTheCallerMaySee(t *testing.T) {
	s := openPGSearchStore(t)
	ctx := context.Background()

	// All four sit on top of the query vector, so nothing here is decided by
	// ranking: whatever comes back came back because the ACL let it.
	seedSearch(t, s,
		&Embedding{ID: "public", Vector: []float32{1, 0, 0, 0}, Content: "everyone may read this"},
		&Embedding{ID: "alice-only", Vector: []float32{1, 0, 0, 0}, Content: "alice's notes", ACL: []string{"alice"}},
		&Embedding{ID: "eng-team", Vector: []float32{1, 0, 0, 0}, Content: "engineering", ACL: []string{"team:eng", "bob"}},
		&Embedding{ID: "secret", Vector: []float32{1, 0, 0, 0}, Content: "nobody here", ACL: []string{"carol"}},
	)

	query := []float32{1, 0, 0, 0}
	opts := SearchOptions{TopK: 10}

	t.Run("anonymous sees only public rows", func(t *testing.T) {
		got, err := s.SearchWithACL(ctx, query, nil, opts)
		if err != nil {
			t.Fatalf("SearchWithACL: %v", err)
		}
		if len(got) != 1 || got[0].ID != "public" {
			t.Fatalf("an empty ACL should see only the public row, got %v", idsOf(got))
		}
	})

	t.Run("a group grant is honoured and others are not", func(t *testing.T) {
		got, err := s.SearchWithACL(ctx, query, []string{"team:eng"}, opts)
		if err != nil {
			t.Fatalf("SearchWithACL: %v", err)
		}
		if !hasID(got, "public") || !hasID(got, "eng-team") {
			t.Errorf("team:eng should see the public row and its own, got %v", idsOf(got))
		}
		for _, forbidden := range []string{"alice-only", "secret"} {
			if hasID(got, forbidden) {
				t.Errorf("team:eng must not see %s, got %v", forbidden, idsOf(got))
			}
		}
	})

	t.Run("holding several entries unions them", func(t *testing.T) {
		got, err := s.SearchWithACL(ctx, query, []string{"alice", "carol"}, opts)
		if err != nil {
			t.Fatalf("SearchWithACL: %v", err)
		}
		if len(got) != 3 || !hasID(got, "public") || !hasID(got, "alice-only") || !hasID(got, "secret") {
			t.Fatalf("alice+carol should see public, alice-only and secret, got %v", idsOf(got))
		}
		if hasID(got, "eng-team") {
			t.Errorf("alice+carol must not see eng-team, got %v", idsOf(got))
		}
	})

	t.Run("an unknown identity is not a skeleton key", func(t *testing.T) {
		got, err := s.SearchWithACL(ctx, query, []string{"mallory"}, opts)
		if err != nil {
			t.Fatalf("SearchWithACL: %v", err)
		}
		if len(got) != 1 || got[0].ID != "public" {
			t.Fatalf("an unrecognised identity should see only public rows, got %v", idsOf(got))
		}
	})
}

func TestPostgresSearchWithACLScoresLikeSearch(t *testing.T) {
	s := openPGSearchStore(t)
	ctx := context.Background()

	seedSearch(t, s,
		&Embedding{ID: "near", Vector: []float32{1, 0, 0, 0}, Content: "nearest"},
		&Embedding{ID: "far", Vector: []float32{0, 1, 0, 0}, Content: "orthogonal"},
	)

	query := []float32{1, 0, 0, 0}
	acl, err := s.SearchWithACL(ctx, query, nil, SearchOptions{TopK: 10})
	if err != nil {
		t.Fatalf("SearchWithACL: %v", err)
	}
	plain, err := s.Search(ctx, query, SearchOptions{TopK: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(acl) != len(plain) {
		t.Fatalf("ACL search over public-only rows returned %v, plain search %v", idsOf(acl), idsOf(plain))
	}
	// A caller comparing against a threshold must not have to know which
	// method answered.
	for i := range acl {
		if acl[i].ID != plain[i].ID {
			t.Fatalf("order differs: %v vs %v", idsOf(acl), idsOf(plain))
		}
		if diff := acl[i].Score - plain[i].Score; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("%s scored %v via ACL search and %v via Search", acl[i].ID, acl[i].Score, plain[i].Score)
		}
	}
}

// --- HybridSearch ------------------------------------------------------------

func TestPostgresHybridSearchRescuesWhatEachArmMisses(t *testing.T) {
	s := openPGSearchStore(t)
	ctx := context.Background()

	// "vector-only" sits on the query vector but never says the word.
	// "text-only" points the other way but is the only row that does.
	// Ordered, not merely distinct. Four mutually orthogonal vectors are all
	// exactly distance 1 from the query, so the second place in a top-2 is a
	// tie broken however the planner feels — and PostgreSQL promises no order
	// among equals. The premise below then failed roughly at random, which is
	// how a test that passes alone fails in a full run.
	seedSearch(t, s,
		&Embedding{ID: "vector-only", Vector: []float32{1, 0, 0, 0}, Content: "a chunk about entirely unrelated matters"},
		&Embedding{ID: "filler-a", Vector: []float32{0.9, 0.1, 0, 0}, Content: "nothing to see"},
		&Embedding{ID: "filler-b", Vector: []float32{0.6, 0.8, 0, 0}, Content: "nor here"},
		&Embedding{ID: "text-only", Vector: []float32{0, 0, 0, 1}, Content: "the mitochondrion is the powerhouse"},
	)

	query := []float32{1, 0, 0, 0}
	opts := HybridSearchOptions{SearchOptions: SearchOptions{TopK: 10}}

	t.Run("the keyword arm finds what the vectors rank last", func(t *testing.T) {
		vecOnly, err := s.Search(ctx, query, SearchOptions{TopK: 2})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if hasID(vecOnly, "text-only") {
			t.Fatalf("premise broken: text-only should be outside the vector top 2, got %v", idsOf(vecOnly))
		}

		got, err := s.HybridSearch(ctx, query, "mitochondrion", opts)
		if err != nil {
			t.Fatalf("HybridSearch: %v", err)
		}
		if !hasID(got, "text-only") {
			t.Errorf("the keyword arm should have pulled text-only in, got %v", idsOf(got))
		}
		if !hasID(got, "vector-only") {
			t.Errorf("the vector arm should still contribute vector-only, got %v", idsOf(got))
		}
		// Only text-only carries the term, so it wins its arm outright and
		// outranks the row that merely happens to point the right way.
		if got[0].ID != "text-only" {
			t.Errorf("text-only should lead on a query it alone matches, got %v", idsOf(got))
		}
	})

	t.Run("the vector arm finds what no keyword matches", func(t *testing.T) {
		got, err := s.HybridSearch(ctx, query, "mitochondrion", opts)
		if err != nil {
			t.Fatalf("HybridSearch: %v", err)
		}
		// vector-only shares not one word with the query text, so it is here
		// on the strength of the vector arm alone.
		if !hasID(got, "vector-only") {
			t.Fatalf("vector-only should survive on vector rank, got %v", idsOf(got))
		}
	})

	t.Run("text with no vector still answers", func(t *testing.T) {
		got, err := s.HybridSearch(ctx, nil, "powerhouse", opts)
		if err != nil {
			t.Fatalf("HybridSearch: %v", err)
		}
		if len(got) != 1 || got[0].ID != "text-only" {
			t.Fatalf("a keyword-only search should return just the matching row, got %v", idsOf(got))
		}
	})

	t.Run("neither arm is an empty search, not an error", func(t *testing.T) {
		got, err := s.HybridSearch(ctx, nil, "", opts)
		if err != nil {
			t.Fatalf("HybridSearch: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("no vector and no text should find nothing, got %v", idsOf(got))
		}
	})

	t.Run("TopK cuts the fused list", func(t *testing.T) {
		got, err := s.HybridSearch(ctx, query, "mitochondrion",
			HybridSearchOptions{SearchOptions: SearchOptions{TopK: 2}})
		if err != nil {
			t.Fatalf("HybridSearch: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("TopK=2 should return two rows, got %v", idsOf(got))
		}
	})
}

// Hyphenated identifiers are the case the FTS5 side needed MatchExpression for.
// plainto_tsquery takes the words as words, so the arm must not vanish here.
func TestPostgresHybridSearchSurvivesPunctuation(t *testing.T) {
	s := openPGSearchStore(t)
	ctx := context.Background()

	seedSearch(t, s,
		&Embedding{ID: "reactor", Vector: []float32{0, 0, 0, 1}, Content: "the on-drbd-demote-failure handler"},
		&Embedding{ID: "other", Vector: []float32{1, 0, 0, 0}, Content: "unrelated"},
	)

	got, err := s.HybridSearch(ctx, nil, "on-drbd-demote-failure",
		HybridSearchOptions{SearchOptions: SearchOptions{TopK: 5}})
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if !hasID(got, "reactor") {
		t.Fatalf("a hyphenated identifier should still match, got %v", idsOf(got))
	}
}

// CJK routes through LIKE rather than tsquery — see fts_postgres.go. Two
// characters is below the trigram floor and still has to find its row.
func TestPostgresHybridSearchFindsCJK(t *testing.T) {
	s := openPGSearchStore(t)
	ctx := context.Background()

	seedSearch(t, s,
		&Embedding{ID: "math", Vector: []float32{0, 0, 0, 1}, Content: "这一课讲的是乘法和除法"},
		&Embedding{ID: "other", Vector: []float32{1, 0, 0, 0}, Content: "unrelated english text"},
	)

	for _, q := range []string{"乘法", "乘法和除法"} {
		got, err := s.HybridSearch(ctx, nil, q, HybridSearchOptions{SearchOptions: SearchOptions{TopK: 5}})
		if err != nil {
			t.Fatalf("HybridSearch(%q): %v", q, err)
		}
		if !hasID(got, "math") {
			t.Errorf("HybridSearch(%q) should find the CJK row, got %v", q, idsOf(got))
		}
	}
}

// The keyword arm shares the FTS-free table with every collection, so it has to
// be restricted the same way the vector arm is.
func TestPostgresHybridSearchRespectsCollection(t *testing.T) {
	s := openPGSearchStore(t)
	ctx := context.Background()

	if _, err := s.CreateCollection(ctx, "other", 4); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	other, err := s.GetCollection(ctx, "other")
	if err != nil {
		t.Fatalf("GetCollection: %v", err)
	}

	seedSearch(t, s,
		&Embedding{ID: "in-default", Vector: []float32{0, 0, 0, 1}, Content: "shared keyword here"},
		&Embedding{ID: "in-other", CollectionID: other.ID, Vector: []float32{0, 0, 0, 1}, Content: "shared keyword here"},
	)

	got, err := s.HybridSearch(ctx, nil, "keyword", HybridSearchOptions{
		SearchOptions: SearchOptions{TopK: 10, Collection: "other"},
	})
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if len(got) != 1 || got[0].ID != "in-other" {
		t.Fatalf("a keyword-only search must stay inside its collection, got %v", idsOf(got))
	}
}

// --- SearchWithAdvancedFilter ------------------------------------------------

func advFilterCorpus(t *testing.T, s *PostgresStore) {
	t.Helper()
	// Same vector everywhere: what comes back is decided by the filter alone.
	seedSearch(t, s,
		&Embedding{ID: "a", Vector: []float32{1, 0, 0, 0}, Content: "a", Metadata: map[string]string{"lang": "go", "stars": "120", "kind": "lib"}},
		&Embedding{ID: "b", Vector: []float32{1, 0, 0, 0}, Content: "b", Metadata: map[string]string{"lang": "go", "stars": "3", "kind": "tool"}},
		&Embedding{ID: "c", Vector: []float32{1, 0, 0, 0}, Content: "c", Metadata: map[string]string{"lang": "rust", "stars": "900", "kind": "lib"}},
		&Embedding{ID: "d", Vector: []float32{1, 0, 0, 0}, Content: "d", Metadata: map[string]string{"lang": "rust", "stars": "n/a", "kind": "lib"}},
		&Embedding{ID: "e", Vector: []float32{1, 0, 0, 0}, Content: "e", Metadata: map[string]string{"kind": "lib"}},
	)
}

func TestPostgresAdvancedFilterReturnsExactlyTheMatchingRows(t *testing.T) {
	s := openPGSearchStore(t)
	ctx := context.Background()
	advFilterCorpus(t, s)

	query := []float32{1, 0, 0, 0}

	cases := []struct {
		name   string
		filter *FilterExpression
		want   []string
	}{
		{
			name: "AND of two equalities",
			filter: &FilterExpression{Operator: FilterAND, Children: []*FilterExpression{
				{Operator: FilterEQ, Field: "lang", Value: "go"},
				{Operator: FilterEQ, Field: "kind", Value: "lib"},
			}},
			want: []string{"a"},
		},
		{
			// (lang = go AND stars > 100) OR lang = rust
			name: "OR over an AND",
			filter: &FilterExpression{Operator: FilterOR, Children: []*FilterExpression{
				{Operator: FilterAND, Children: []*FilterExpression{
					{Operator: FilterEQ, Field: "lang", Value: "go"},
					{Operator: FilterGT, Field: "stars", Value: 100},
				}},
				{Operator: FilterEQ, Field: "lang", Value: "rust"},
			}},
			want: []string{"a", "c", "d"},
		},
		{
			name:   "IN",
			filter: &FilterExpression{Operator: FilterIN, Field: "lang", Value: []any{"rust", "zig"}},
			want:   []string{"c", "d"},
		},
		{
			name:   "BETWEEN skips the value that is not a number",
			filter: &FilterExpression{Operator: FilterBETWEEN, Field: "stars", Value: []any{float64(1), float64(200)}},
			want:   []string{"a", "b"},
		},
		{
			name: "NOT keeps the row that has no such key",
			// The row with no lang at all is not "lang = go", so a negation
			// must keep it. NOT NULL would drop it silently.
			filter: &FilterExpression{Operator: FilterNOT, Children: []*FilterExpression{
				{Operator: FilterEQ, Field: "lang", Value: "go"},
			}},
			want: []string{"c", "d", "e"},
		},
		{
			name:   "LIKE",
			filter: &FilterExpression{Operator: FilterLIKE, Field: "kind", Value: "li%"},
			want:   []string{"a", "c", "d", "e"},
		},
		{
			name: "a number written as a float still matches the stored text",
			// ParseFilterString turns every literal into a float64; stars is
			// the string "3", and 3.0 must not become "3.000000".
			filter: &FilterExpression{Operator: FilterEQ, Field: "stars", Value: float64(3)},
			want:   []string{"b"},
		},
	}

	for _, tc := range cases {
		t.Run("pre/"+tc.name, func(t *testing.T) {
			got, err := s.SearchWithAdvancedFilter(ctx, query, AdvancedSearchOptions{
				SearchOptions: SearchOptions{TopK: 10},
				PreFilter:     tc.filter,
			})
			if err != nil {
				t.Fatalf("SearchWithAdvancedFilter: %v", err)
			}
			assertSameIDs(t, idsOf(got), tc.want)
		})
	}
}

// The post filter runs in Go, on the same tree, and must not be a different
// language from the pre-filter.
func TestPostgresAdvancedFilterPostFilterAgreesWithPreFilter(t *testing.T) {
	s := openPGSearchStore(t)
	ctx := context.Background()
	advFilterCorpus(t, s)

	filter := &FilterExpression{Operator: FilterAND, Children: []*FilterExpression{
		{Operator: FilterEQ, Field: "kind", Value: "lib"},
		{Operator: FilterEQ, Field: "lang", Value: "rust"},
	}}

	pre, err := s.SearchWithAdvancedFilter(ctx, []float32{1, 0, 0, 0}, AdvancedSearchOptions{
		SearchOptions: SearchOptions{TopK: 10}, PreFilter: filter,
	})
	if err != nil {
		t.Fatalf("pre: %v", err)
	}
	post, err := s.SearchWithAdvancedFilter(ctx, []float32{1, 0, 0, 0}, AdvancedSearchOptions{
		SearchOptions: SearchOptions{TopK: 10}, PostFilter: filter,
	})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	assertSameIDs(t, idsOf(pre), []string{"c", "d"})
	assertSameIDs(t, idsOf(post), []string{"c", "d"})
}

// A LIMIT applied before the post filter would return fewer rows than asked
// for while matching rows sat just past the cut.
func TestPostgresAdvancedFilterFillsTopKPastThePostFilter(t *testing.T) {
	s := openPGSearchStore(t)
	ctx := context.Background()

	// The three nearest rows all fail the filter; the matches are behind them.
	seedSearch(t, s,
		&Embedding{ID: "near-1", Vector: []float32{1, 0, 0, 0}, Content: "x", Metadata: map[string]string{"keep": "no"}},
		&Embedding{ID: "near-2", Vector: []float32{0.99, 0.01, 0, 0}, Content: "x", Metadata: map[string]string{"keep": "no"}},
		&Embedding{ID: "near-3", Vector: []float32{0.98, 0.02, 0, 0}, Content: "x", Metadata: map[string]string{"keep": "no"}},
		&Embedding{ID: "far-1", Vector: []float32{0.5, 0.5, 0, 0}, Content: "x", Metadata: map[string]string{"keep": "yes"}},
		&Embedding{ID: "far-2", Vector: []float32{0.4, 0.6, 0, 0}, Content: "x", Metadata: map[string]string{"keep": "yes"}},
	)

	got, err := s.SearchWithAdvancedFilter(ctx, []float32{1, 0, 0, 0}, AdvancedSearchOptions{
		SearchOptions: SearchOptions{TopK: 2},
		PostFilter:    &FilterExpression{Operator: FilterEQ, Field: "keep", Value: "yes"},
	})
	if err != nil {
		t.Fatalf("SearchWithAdvancedFilter: %v", err)
	}
	if len(got) != 2 || got[0].ID != "far-1" || got[1].ID != "far-2" {
		t.Fatalf("the post filter should still fill TopK in distance order, got %v", idsOf(got))
	}
}

// The point of the whole exercise: an operator this backend cannot express
// stops the search and names itself, rather than compiling to nothing and
// handing back rows the caller excluded.
func TestPostgresAdvancedFilterRefusesWhatItCannotCompile(t *testing.T) {
	s := openPGSearchStore(t)
	ctx := context.Background()
	advFilterCorpus(t, s)

	query := []float32{1, 0, 0, 0}

	cases := []struct {
		name   string
		opts   AdvancedSearchOptions
		naming string
	}{
		{
			name: "REGEX as a pre-filter",
			opts: AdvancedSearchOptions{
				SearchOptions: SearchOptions{TopK: 10},
				PreFilter:     &FilterExpression{Operator: FilterREGEX, Field: "lang", Value: "^g"},
			},
			naming: "REGEX",
		},
		{
			name: "REGEX buried inside an AND",
			opts: AdvancedSearchOptions{
				SearchOptions: SearchOptions{TopK: 10},
				PreFilter: &FilterExpression{Operator: FilterAND, Children: []*FilterExpression{
					{Operator: FilterEQ, Field: "kind", Value: "lib"},
					{Operator: FilterREGEX, Field: "lang", Value: "^g"},
				}},
			},
			naming: "REGEX",
		},
		{
			name: "REGEX as a post-filter",
			opts: AdvancedSearchOptions{
				SearchOptions: SearchOptions{TopK: 10},
				PostFilter:    &FilterExpression{Operator: FilterREGEX, Field: "lang", Value: "^g"},
			},
			naming: "REGEX",
		},
		{
			name: "an operator nobody defined",
			opts: AdvancedSearchOptions{
				SearchOptions: SearchOptions{TopK: 10},
				PreFilter:     &FilterExpression{Operator: FilterOperator("NEAR"), Field: "lang", Value: "go"},
			},
			naming: "NEAR",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.SearchWithAdvancedFilter(ctx, query, tc.opts)
			if err == nil {
				t.Fatalf("expected an error, got %d rows: %v", len(got), idsOf(got))
			}
			if !strings.Contains(err.Error(), tc.naming) {
				t.Errorf("the error does not name %s: %v", tc.naming, err)
			}
			if len(got) != 0 {
				t.Errorf("a refused filter must return no rows, got %v", idsOf(got))
			}
		})
	}
}

// The advanced path scores the same way the plain one does.
func TestPostgresAdvancedFilterScoresLikeSearch(t *testing.T) {
	s := openPGSearchStore(t)
	ctx := context.Background()
	advFilterCorpus(t, s)

	query := []float32{1, 0, 0, 0}
	plain, err := s.Search(ctx, query, SearchOptions{TopK: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	adv, err := s.SearchWithAdvancedFilter(ctx, query, AdvancedSearchOptions{
		SearchOptions: SearchOptions{TopK: 1},
	})
	if err != nil {
		t.Fatalf("SearchWithAdvancedFilter: %v", err)
	}
	if len(plain) != 1 || len(adv) != 1 {
		t.Fatalf("expected one row from each, got %d and %d", len(plain), len(adv))
	}
	if diff := adv[0].Score - plain[0].Score; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("advanced search scored %v where Search scored %v", adv[0].Score, plain[0].Score)
	}
}

func assertSameIDs(t *testing.T, got, want []string) {
	t.Helper()
	seen := make(map[string]bool, len(got))
	for _, id := range got {
		seen[id] = true
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, id := range want {
		if !seen[id] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
