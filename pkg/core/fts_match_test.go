package core

import "testing"

// FTS5's MATCH argument is a query language. Hyphens are NOT, colons filter
// columns — so ordinary technical vocabulary was a syntax error, and in a
// hybrid search the failure was invisible because the vector arm still
// answered.
func TestMatchExpressionQuotesTechnicalTerms(t *testing.T) {
	for query, want := range map[string]string{
		// The parts are ANDed, not phrased: that is what a bareword meant
		// where it happened to parse, and phrasing them instead demands
		// adjacency and collapses recall.
		"on-drbd-demote-failure": `"on" "drbd" "demote" "failure"`,
		"al-extents":             `"al" "extents"`,
		"peer-disk no-quorum":    `"peer" "disk" "no" "quorum"`,
		// A colon would otherwise be read as a column filter.
		"error:timeout": `"error" "timeout"`,
		// Plain words are returned untouched — see needsEscaping.
		"promoter status": "promoter status",
	} {
		if got := MatchExpression(query); got != want {
			t.Errorf("MatchExpression(%q) = %q, want %q", query, got, want)
		}
	}
}

// A quote in the query is punctuation like any other: it separates terms and
// can never escape into the expression.
func TestMatchExpressionNeutralisesQuotes(t *testing.T) {
	if got, want := MatchExpression(`say"hi`), `"say" "hi"`; got != want {
		t.Errorf("MatchExpression = %q, want %q", got, want)
	}
}

// Nothing indexable means there is no keyword arm to run; MATCH ” is a syntax
// error of its own, so callers need an empty result to skip on.
func TestMatchExpressionIsEmptyWhenNothingIsIndexable(t *testing.T) {
	for _, query := range []string{"", "   ", "-", "--", "* ( ) :", "\t\n"} {
		if got := MatchExpression(query); got != "" {
			t.Errorf("MatchExpression(%q) = %q, want empty", query, got)
		}
	}
}

// Punctuation-only words are dropped, the rest survive.
func TestMatchExpressionDropsOnlyTheUnindexableWords(t *testing.T) {
	if got, want := MatchExpression("- al-extents *"), `"al" "extents"`; got != want {
		t.Errorf("MatchExpression = %q, want %q", got, want)
	}
}

func TestMatchExpressionKeepsCJK(t *testing.T) {
	if got, want := MatchExpression("存储池 状态"), "存储池 状态"; got != want {
		t.Errorf("MatchExpression = %q, want %q", got, want)
	}
}

// The half that matters as much as the fix: a query FTS5 already understands is
// not touched. Rewriting those cost recall@10 0.85 -> 0.27 on
// cortexdb-retrieval-v2, because the rewrite is not as equivalent as it looks.
func TestMatchExpressionLeavesPlainQueriesAlone(t *testing.T) {
	for _, q := range []string{
		"concurrent programming with goroutines and channels",
		"approximate nearest neighbor vector search index",
		"存储池 状态",
		"bm25",
	} {
		if got := MatchExpression(q); got != q {
			t.Errorf("MatchExpression(%q) = %q, want it unchanged", q, got)
		}
	}
}

// The end-to-end proof: against a real FTS5 table, a hyphenated identifier is a
// syntax error raw and a hit once quoted. This is the failure that made the
// keyword arm of every hybrid search silently useless for exactly the terms it
// is best at — DRBD's al-extents, on-drbd-demote-failure, peer-disk.
func TestMatchExpressionMakesHyphenatedTermsSearchable(t *testing.T) {
	store, ctx := newCJKStore(t)
	if err := createDummyDoc(ctx, store, "doc-hyphen"); err != nil {
		t.Fatalf("create doc: %v", err)
	}
	if err := store.Upsert(ctx, &Embedding{
		ID:      "chunk-hyphen",
		Vector:  []float32{1, 2, 3},
		Content: "SDS sets on-drbd-demote-failure to reboot-immediate; al-extents stays at the default.",
		DocID:   "doc-hyphen",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	db := store.GetDB()
	const q = "SELECT count(*) FROM chunks_fts WHERE chunks_fts MATCH ?"

	// Raw: FTS5 reads the hyphens as syntax and refuses the query.
	var n int
	if err := db.QueryRow(q, "on-drbd-demote-failure").Scan(&n); err == nil {
		t.Fatal("expected raw hyphenated text to be an FTS5 syntax error; it was accepted, " +
			"so this test no longer proves anything")
	}

	// Sanitized: it finds the document.
	if err := db.QueryRow(q, MatchExpression("on-drbd-demote-failure")).Scan(&n); err != nil {
		t.Fatalf("sanitized query failed: %v", err)
	}
	if n != 1 {
		t.Errorf("sanitized query matched %d chunks, want 1", n)
	}

	// A term that is genuinely absent still returns nothing — the fix must not
	// turn every query into a match.
	if err := db.QueryRow(q, MatchExpression("no-such-parameter")).Scan(&n); err != nil {
		t.Fatalf("absent term query failed: %v", err)
	}
	if n != 0 {
		t.Errorf("absent term matched %d chunks, want 0", n)
	}
}
