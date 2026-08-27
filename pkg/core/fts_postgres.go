package core

// The same CJK lexical strategy, spoken to PostgreSQL.
//
// fts_cjk.go works out *what* to search for; this works out *how* on a
// database with no FTS5. The routing decisions are shared on purpose —
// ContainsCJK and BelowTrigramFloor are called from both — so the two backends
// can disagree about speed but never about which arm of the search a query
// belongs in.
//
// The mapping is closer than it looks, because the SQLite side already chose
// trigrams for CJK:
//
//	query               SQLite                     PostgreSQL
//	non-CJK             FTS5 unicode61 MATCH       tsvector @@ plainto_tsquery
//	CJK, 3+ characters  FTS5 trigram companion     LIKE, accelerated by pg_trgm
//	CJK, 1-2 characters LIKE, unindexed            LIKE, unindexed
//
// The last row is not a PostgreSQL limitation — a two-character word produces
// no trigrams on either side, so both degrade to a scan for 乘法 and 分数.
// Same weakness, same place, which is what makes it predictable.
//
// 'simple' rather than 'english' for the word index: unicode61 does not stem
// or drop stopwords, and a backend that quietly stemmed would return different
// rows for the same query depending on where it ran.

import (
	"fmt"
	"strings"
)

// PostgresLexicalDDL returns the statements that make lexical search on a
// column fast, in the order they must run.
//
// pg_trgm is a contrib extension: present in the standard images, but a
// managed instance may refuse CREATE EXTENSION to this account. The caller
// should treat a failure as "unindexed, still correct" rather than fatal —
// every query below works without any of these, just linearly.
func PostgresLexicalDDL(table, column string) []string {
	return []string{
		`CREATE EXTENSION IF NOT EXISTS pg_trgm`,
		// The word index, for text with spaces in it.
		fmt.Sprintf(
			`CREATE INDEX IF NOT EXISTS idx_%[1]s_%[2]s_tsv ON %[1]s USING gin (to_tsvector('simple', %[2]s))`,
			table, column),
		// The CJK companion. gin_trgm_ops is what turns LIKE '%…%' from a
		// scan into an index lookup — the counterpart of the FTS5 trigram
		// table, not a workaround for the lack of one.
		fmt.Sprintf(
			`CREATE INDEX IF NOT EXISTS idx_%[1]s_%[2]s_trgm ON %[1]s USING gin (%[2]s gin_trgm_ops)`,
			table, column),
	}
}

// PostgresLexicalCondition builds the WHERE fragment for one query, with `?`
// placeholders for the dialect to rebind, and the values to bind.
//
// indexed reports whether an index can serve it. False is not a failure — the
// row still gets found — but it is linear in table size, and a caller that
// wants to log or refuse that needs to be told rather than left to infer it
// from a slow query log.
//
// The CJK arm matches TERMS, not the whole string. That was wrong for a while
// and wrong in a way no unit test saw: every test passed a single word, while
// the real caller passes a whole question. `LIKE '%pkg/agentmem 那个 bug 是
// 什么？%'` matches nothing however much of the corpus is about it, so search
// returned an empty result and the model above it correctly answered "the
// material does not say" — a wrong answer that looks like an honest one.
// FTS5's MATCH tokenises, so this has to as well, or the two backends are
// answering different questions.
func PostgresLexicalCondition(column, query string) (sql string, args []any, indexed bool) {
	parsed := ParseFTS5(query)
	terms := parsed.Terms
	if len(terms) == 0 {
		terms = []string{query}
	}

	switch {
	case ContainsCJK(query):
		// Split each term into the runs FTS5 would tokenise, and match any of
		// them. A query whose terms are all below the trigram floor cannot use
		// the pg_trgm index; one long enough term is enough for it to help.
		var expanded []string
		for _, t := range terms {
			sub := indexableTerms(t)
			if len(sub) == 0 {
				continue
			}
			expanded = append(expanded, sub...)
		}
		if len(expanded) == 0 {
			expanded = []string{query}
		}
		conds := make([]string, 0, len(expanded))
		indexed = false
		for _, t := range expanded {
			conds = append(conds, fmt.Sprintf(`%s LIKE ? ESCAPE '\'`, column))
			args = append(args, SubstringPattern(t))
			if !BelowTrigramFloor(t) {
				indexed = true
			}
		}
		return "(" + strings.Join(conds, " OR ") + ")", args, indexed

	case parsed.Any:
		// An explicit OR. plainto_tsquery ANDs everything it is given, so the
		// disjunction has to be built out of one call per term — and it has to
		// be built at all, because the alternative is what used to happen:
		// `owner OR name` went in as a sentence and came back as
		// 'owner' & 'or' & 'name', an AND over a word that is in no document.
		conds := make([]string, 0, len(terms))
		for _, t := range terms {
			conds = append(conds, fmt.Sprintf(
				`to_tsvector('simple', %s) @@ plainto_tsquery('simple', ?)`, column))
			args = append(args, t)
		}
		return "(" + strings.Join(conds, " OR ") + ")", args, true

	default:
		// plainto_tsquery, not to_tsquery: it takes the user's words as words
		// and never reads a stray & or ! as an operator, which is the same
		// reason MatchExpression exists on the FTS5 side. It tokenises for us.
		return fmt.Sprintf(`to_tsvector('simple', %s) @@ plainto_tsquery('simple', ?)`, column),
			[]any{strings.Join(terms, " ")}, true
	}
}

// FTS5Query is an FTS5 MATCH expression taken apart far enough that a database
// without FTS5 can answer it.
type FTS5Query struct {
	// Terms are the words and phrases to match, quotes and prefix stars gone.
	Terms []string
	// Any is true when the expression joined its terms with OR.
	Any bool
}

// ParseFTS5 reads an FTS5 MATCH expression as terms plus a connective.
//
// It exists because the query the retrieval layer builds is FTS5 *syntax*, not
// text: sanitizeFTSQuery double-quotes every token, and the keyword expansion
// emits `owner OR name` and `owner* OR name*`. SQLite reads those as a query.
// PostgreSQL was handed the same string as if a person had typed it, so the
// quotes became part of the words, the star became part of the word, and OR
// became a word — a search for a document containing the English word "or".
//
// The result is an approximation on purpose. NOT and NEAR are dropped rather
// than translated: they never appear in what this codebase generates, and
// silently mistranslating them would be worse than matching a little too much.
func ParseFTS5(query string) FTS5Query {
	var out FTS5Query
	var cur strings.Builder
	inQuote := false

	flush := func() {
		term := cur.String()
		cur.Reset()
		if term == "" {
			return
		}
		switch term {
		case "OR":
			out.Any = true
			return
		case "AND", "NOT", "NEAR":
			// The implicit connective is already AND, and the other two are
			// not generated here; dropping them keeps their operands.
			return
		}
		// A trailing star is FTS5 prefix matching. plainto_tsquery has no
		// prefix form, so the term matches whole; the caller always emits a
		// non-prefix variant of the same query alongside it.
		term = strings.TrimSuffix(term, "*")
		if term == "" {
			return
		}
		out.Terms = append(out.Terms, term)
	}

	runes := []rune(query)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '"':
			// "" inside a quoted phrase is one escaped quote.
			if inQuote && i+1 < len(runes) && runes[i+1] == '"' {
				cur.WriteRune('"')
				i++
				continue
			}
			if inQuote {
				// End of a phrase: flush even if the next character is not a
				// space, so `"a""b"` does not merge into one term.
				inQuote = false
				term := cur.String()
				cur.Reset()
				if term != "" {
					out.Terms = append(out.Terms, term)
				}
				continue
			}
			inQuote = true
		case !inQuote && (r == ' ' || r == '\t' || r == '\n' || r == '\r'):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// PostgresLexicalRank is the ORDER BY expression that goes with
// PostgresLexicalCondition, negated so that lower is better — the convention
// bm25 already sets on the FTS5 side and what every caller's ORDER BY expects.
//
// It has to be built from the same parse as the condition, and it has to bind
// exactly as many values. Ordering by id was worse than no ranking at all: it
// HAD one, an arbitrary one, so the wrong chunk came first and the model above
// answered from it — which reads as a retrieval that found nothing useful
// rather than as a sort order nobody chose.
func PostgresLexicalRank(column, query string) (expr string, args []any) {
	parsed := ParseFTS5(query)
	terms := parsed.Terms
	if len(terms) == 0 {
		terms = []string{query}
	}

	if ContainsCJK(query) {
		// No bm25 counterpart here, but "matched three terms of four" is a
		// real signal, and it is the only one a LIKE scan can offer.
		var expanded []string
		for _, t := range terms {
			expanded = append(expanded, indexableTerms(t)...)
		}
		if len(expanded) == 0 {
			expanded = []string{query}
		}
		parts := make([]string, 0, len(expanded))
		for _, t := range expanded {
			parts = append(parts, fmt.Sprintf(`(CASE WHEN %s LIKE ? ESCAPE '\' THEN 1 ELSE 0 END)`, column))
			args = append(args, SubstringPattern(t))
		}
		return "-(" + strings.Join(parts, " + ") + ")", args
	}

	// Summed per term rather than one call over the whole string: the
	// condition is a disjunction when the query said OR, and a rank with one
	// placeholder against a condition with three is not a worse ranking, it is
	// a bind mismatch that fails the query outright.
	parts := make([]string, 0, len(terms))
	for _, t := range terms {
		parts = append(parts, fmt.Sprintf(
			`ts_rank_cd(to_tsvector('simple', %s), plainto_tsquery('simple', ?))`, column))
		args = append(args, t)
	}
	return "-(" + strings.Join(parts, " + ") + ")", args
}
