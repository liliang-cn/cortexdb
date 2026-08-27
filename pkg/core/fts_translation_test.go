package core

import (
	"strings"
	"testing"
)

// The retrieval layer builds FTS5 *syntax*, not text. sanitizeFTSQuery quotes
// every token and the keyword expansion emits `owner OR name` and
// `owner* OR name*`. PostgreSQL was handed those strings as if a person had
// typed them, so the quotes joined the words, the star joined the word, and OR
// became a word — a search for documents containing the English word "or",
// which no document contains. Every one of the unit tests that covered this
// passed a single bare word, which is the one shape where the two spellings
// coincide.

func TestParseFTS5ReadsTheSyntaxItIsGiven(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		terms []string
		any   bool
	}{
		{"bare word", "owner", []string{"owner"}, false},
		{"quoted tokens", `"what's" "the" "owner's" "name?"`,
			[]string{"what's", "the", "owner's", "name?"}, false},
		{"explicit or", "owner OR name", []string{"owner", "name"}, true},
		{"prefix stars", "owner* OR name*", []string{"owner", "name"}, true},
		{"and is the default", "owner AND name", []string{"owner", "name"}, false},
		{"operators that are not generated here are dropped",
			"owner NOT name NEAR title", []string{"owner", "name", "title"}, false},
		{"escaped quote inside a phrase", `"say ""hi"""`, []string{`say "hi"`}, false},
		{"cjk phrase", `"风控组" "值班"`, []string{"风控组", "值班"}, false},
		{"empty", "", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseFTS5(tc.query)
			if got.Any != tc.any {
				t.Errorf("Any = %v, want %v", got.Any, tc.any)
			}
			if strings.Join(got.Terms, "|") != strings.Join(tc.terms, "|") {
				t.Errorf("Terms = %q, want %q", got.Terms, tc.terms)
			}
		})
	}
}

func TestPostgresLexicalConditionDoesNotSearchForTheWordOR(t *testing.T) {
	_, args, _ := PostgresLexicalCondition("content", "settle OR job OR 触发者")
	for _, a := range args {
		if s, ok := a.(string); ok && strings.Contains(s, "OR") {
			t.Fatalf("bound %q — the connective became a search term", s)
		}
	}
}

func TestPostgresLexicalConditionHonoursAnExplicitOR(t *testing.T) {
	sql, args, _ := PostgresLexicalCondition("content", "owner OR name")
	if !strings.Contains(sql, " OR ") {
		t.Errorf("condition is not a disjunction: %s", sql)
	}
	if len(args) != 2 {
		t.Errorf("bound %d values, want one per term: %v", len(args), args)
	}
	// plainto_tsquery ANDs whatever it is handed, so a single call over
	// "owner OR name" yields 'owner' & 'or' & 'name' and matches nothing.
	if strings.Count(sql, "plainto_tsquery") != 2 {
		t.Errorf("want one plainto_tsquery per term: %s", sql)
	}
}

// A rank expression with one placeholder against a condition with three is not
// a worse ranking — it is a bind mismatch, and the query fails outright at the
// driver. The two are built from the same parse, so they have to be checked
// together.
func TestLexicalConditionAndRankBindWhatTheyPlaceholder(t *testing.T) {
	for _, query := range []string{
		"owner",
		"owner OR name",
		"owner* OR name*",
		`"what's" "the" "owner's" "name?"`,
		"风控",
		`"settle-job" "触发者"`,
		"settle OR job OR 触发者",
		"ledger_entries",
	} {
		t.Run(query, func(t *testing.T) {
			cond, condArgs, _ := PostgresLexicalCondition("content", query)
			if n := strings.Count(cond, "?"); n != len(condArgs) {
				t.Errorf("condition has %d placeholders and %d args:\n%s\n%v",
					n, len(condArgs), cond, condArgs)
			}
			rank, rankArgs := PostgresLexicalRank("content", query)
			if n := strings.Count(rank, "?"); n != len(rankArgs) {
				t.Errorf("rank has %d placeholders and %d args:\n%s\n%v",
					n, len(rankArgs), rank, rankArgs)
			}
		})
	}
}

// Ordering by id was worse than no ranking at all: it HAD one, an arbitrary
// one, so the wrong chunk came first and the model above answered from it.
func TestPostgresLexicalRankIsARankAndNotAnID(t *testing.T) {
	for _, query := range []string{"owner OR name", "风控组 值班"} {
		rank, _ := PostgresLexicalRank("content", query)
		if rank == "id" || rank == "0" || !strings.HasPrefix(rank, "-(") {
			t.Errorf("%q ranks by %q, which is not a relevance signal", query, rank)
		}
	}
}
