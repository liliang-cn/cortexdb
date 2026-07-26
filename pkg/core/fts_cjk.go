package core

import (
	"strings"
	"unicode"
)

// CJK lexical search needs a different index than English.
//
// FTS5's default unicode61 tokenizer splits on non-alphanumeric characters. Chinese,
// Japanese and Korean text has no spaces, so a whole run of CJK characters becomes a
// single token and no realistic query can ever match it — lexical search silently
// returns zero rows for a CJK corpus while English works fine. The trigram tokenizer
// indexes substrings, which does match CJK, but it is a poor word index for
// space-separated languages: BM25 over trigrams reorders English results and matches
// fragments across word boundaries.
//
// So both indexes exist, and the query picks one by script. Anything containing CJK
// goes to the trigram companion; everything else keeps the word index it always used.

// ContainsCJK reports whether the text holds any Han, Hiragana, Katakana or Hangul
// character — the scripts whose text is not space-delimited.
func ContainsCJK(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) ||
			unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) ||
			unicode.Is(unicode.Hangul, r) {
			return true
		}
	}
	return false
}

// CJKAwareIndex returns the FTS index to MATCH against for this query: the trigram
// companion for CJK text, otherwise the unicode61 word index.
//
// Note the trigram floor: a query of one or two CJK characters produces no trigrams and
// therefore matches nothing. Callers that may be handed such a query should route it past
// MATCH entirely — see BelowTrigramFloor.
func CJKAwareIndex(wordIndex, query string) string {
	if ContainsCJK(query) {
		return wordIndex + "_cjk"
	}
	return wordIndex
}

// TrigramFloor is the shortest run the trigram tokenizer can make a token from.
const TrigramFloor = 3

// BelowTrigramFloor reports whether a CJK query is too short for the trigram index to match it.
//
// Two characters is a whole word in Chinese, and a great many of the words a lesson is about are
// exactly two: 乘法, 除法, 分数, 面积, 周长. MATCH against a trigram index returns nothing for all
// of them, so a caller who does not notice reports "no results" for a term the corpus is full of.
// Falling back to a substring scan is slower than an index, but it is the difference between an
// answer and a silent zero.
func BelowTrigramFloor(query string) bool {
	trimmed := strings.TrimSpace(query)
	return ContainsCJK(trimmed) && len([]rune(trimmed)) < TrigramFloor
}

// SubstringPattern turns a literal into a LIKE pattern matching it anywhere, escaping the
// wildcards LIKE would otherwise read as syntax. Use it with ESCAPE '\'.
func SubstringPattern(literal string) string {
	var pattern strings.Builder
	pattern.WriteByte('%')
	for _, r := range literal {
		if r == '%' || r == '_' || r == '\\' {
			pattern.WriteByte('\\')
		}
		pattern.WriteRune(r)
	}
	pattern.WriteByte('%')
	return pattern.String()
}
