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

import "fmt"

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

// PostgresLexicalCondition builds the WHERE fragment for one query, with a
// single `?` placeholder for the dialect to rebind.
//
// indexed reports whether an index can serve it. False is not a failure — the
// row still gets found — but it is linear in table size, and a caller that
// wants to log or refuse that needs to be told rather than left to infer it
// from a slow query log.
func PostgresLexicalCondition(column, query string) (sql string, arg string, indexed bool) {
	switch {
	case BelowTrigramFloor(query):
		// One or two CJK characters: no trigram exists to look up, on either
		// backend. A substring scan is slower than an index and infinitely
		// faster than the zero rows MATCH would return.
		return fmt.Sprintf(`%s LIKE ? ESCAPE '\'`, column), SubstringPattern(query), false

	case ContainsCJK(query):
		// Long enough to have trigrams, so the pg_trgm index serves the same
		// LIKE that would otherwise scan.
		return fmt.Sprintf(`%s LIKE ? ESCAPE '\'`, column), SubstringPattern(query), true

	default:
		// plainto_tsquery, not to_tsquery: it takes the user's words as words
		// and never reads a stray & or ! as an operator, which is the same
		// reason MatchExpression exists on the FTS5 side.
		return fmt.Sprintf(`to_tsvector('simple', %s) @@ plainto_tsquery('simple', ?)`, column), query, true
	}
}
