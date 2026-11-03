package cortexdb

import "strings"

// English words are compared by their stem where coverage is counted, so a
// question and a row that use different forms of one word share it.
//
// The word index is FTS5 unicode61, which does not stem, and containsWord
// matched a question word in a row only whole, or for four letters or more as
// the start of a longer one. So "deploy" covered "deployed", but "own" did not
// cover "owns", nor "owned" "owns", nor "shipped" "ships", nor "box" "boxes":
// "what does Alice own?" found "Alice owns Apollo and Apollo ships Friday."
// through alice, and the floor then dropped it for holding one word of two,
// while "when does Apollo ship?" passed because ship is four letters long.
//
// stemWord strips the inflectional suffixes only — steps 1a, 1b, 1c and 5 of
// Porter's algorithm: plurals and third persons (-s, -es, -ies), past tenses
// and participles (-ed, -ing, with their doubled consonants and dropped e),
// and a final y or e. It leaves derivational suffixes (-ation, -ness, -ful)
// alone: those make another word, with another meaning, and the prefix rule
// of containsWord already covers the ones that only lengthen it. A word with
// a letter outside a–z (a digit, an accent that foldAccents keeps, any
// non-Latin script) is returned unchanged, so CJK text, which coverage reads
// by character bigrams, is not touched.

// stemMinLen is the shortest word stemWord changes, and the shortest stem it
// makes: "as" and "is" stay what they are, and so do "les" and "uses".
const stemMinLen = 3

// stemWord is the stem of a lowercased word.
func stemWord(w string) string {
	if len(w) < stemMinLen {
		return w
	}
	for i := 0; i < len(w); i++ {
		if w[i] < 'a' || w[i] > 'z' {
			return w
		}
	}
	b := []byte(w)
	b = stemStep1a(b)
	b = stemStep1b(b)
	b = stemStep1c(b)
	b = stemStep5(b)
	// A stem of two letters is not one: "les" and "des" are French words,
	// not plurals of "le" and "de", and "its" is not a form of "it".
	if len(b) < stemMinLen {
		return w
	}
	return string(b)
}

// stemRoot is the part of w's stem that every form of the word starts with,
// for finding them by prefix (see formatFTSKeywords): the stem without a
// final e or i, which some forms have and others do not ("hope" in "hoping",
// "compani" in "company"). It is w itself when that is shorter than
// stemMinLen.
func stemRoot(w string) string {
	root := stemWord(w)
	if n := len(root); n > stemMinLen && (root[n-1] == 'e' || root[n-1] == 'i') {
		root = root[:n-1]
	}
	if len(root) < stemMinLen {
		return w
	}
	return root
}

// stemmedWords are the stems of the words of folded text, lowercased and
// without accents.
func stemmedWords(folded string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, w := range strings.FieldsFunc(folded, func(r rune) bool { return !isWordRune(r) }) {
		out[stemWord(w)] = struct{}{}
	}
	return out
}

// isConsonant reports whether b[i] is a consonant in Porter's sense: a letter
// other than a, e, i, o, u, and other than a y that follows a consonant.
func isConsonant(b []byte, i int) bool {
	switch b[i] {
	case 'a', 'e', 'i', 'o', 'u':
		return false
	case 'y':
		return i == 0 || !isConsonant(b, i-1)
	}
	return true
}

// measure is Porter's m of b: the number of vowel-consonant sequences in it.
func measure(b []byte) int {
	m, i, n := 0, 0, len(b)
	for i < n && isConsonant(b, i) {
		i++
	}
	for i < n {
		for i < n && !isConsonant(b, i) {
			i++
		}
		if i == n {
			break
		}
		for i < n && isConsonant(b, i) {
			i++
		}
		m++
	}
	return m
}

func hasVowel(b []byte) bool {
	for i := range b {
		if !isConsonant(b, i) {
			return true
		}
	}
	return false
}

// endsDoubleConsonant is Porter's *d: b ends with two of one consonant.
func endsDoubleConsonant(b []byte) bool {
	n := len(b)
	return n >= 2 && b[n-1] == b[n-2] && isConsonant(b, n-1)
}

// endsCVC is Porter's *o: b ends consonant, vowel, consonant, the last not
// w, x or y ("hop", not "box").
func endsCVC(b []byte) bool {
	n := len(b)
	if n < 3 || !isConsonant(b, n-3) || isConsonant(b, n-2) || !isConsonant(b, n-1) {
		return false
	}
	switch b[n-1] {
	case 'w', 'x', 'y':
		return false
	}
	return true
}

func hasSuffix(b []byte, s string) bool {
	return len(b) >= len(s) && string(b[len(b)-len(s):]) == s
}

// stemStep1a strips plurals: sses → ss, ies → i, s → (but not ss). After a
// stem without a vowel, ies is y: "flies" is "fly", which step 1c leaves be.
func stemStep1a(b []byte) []byte {
	switch {
	case hasSuffix(b, "sses"):
		return b[:len(b)-2]
	case hasSuffix(b, "ies"):
		if !hasVowel(b[:len(b)-3]) {
			b = b[:len(b)-2]
			b[len(b)-1] = 'y'
			return b
		}
		return b[:len(b)-2]
	case hasSuffix(b, "ss"):
		return b
	case hasSuffix(b, "s"):
		return b[:len(b)-1]
	}
	return b
}

// stemStep1b strips -eed, -ed and -ing, then mends the stem: "hop(p)ing" is
// "hop", "hoping" "hope", "rated" "rate".
func stemStep1b(b []byte) []byte {
	if hasSuffix(b, "eed") {
		if measure(b[:len(b)-3]) > 0 {
			return b[:len(b)-1]
		}
		return b
	}
	var stem []byte
	switch {
	case hasSuffix(b, "ed") && hasVowel(b[:len(b)-2]):
		stem = b[:len(b)-2]
	case hasSuffix(b, "ing") && hasVowel(b[:len(b)-3]):
		stem = b[:len(b)-3]
	default:
		return b
	}
	switch {
	case hasSuffix(stem, "at"), hasSuffix(stem, "bl"), hasSuffix(stem, "iz"):
		return append(stem, 'e')
	case endsDoubleConsonant(stem):
		switch stem[len(stem)-1] {
		case 'l', 's', 'z':
			return stem
		}
		return stem[:len(stem)-1]
	case measure(stem) == 1 && endsCVC(stem):
		return append(stem, 'e')
	}
	return stem
}

// stemStep1c turns a final y after a vowel-holding stem into i, so "company"
// and "companies" meet at "compani".
func stemStep1c(b []byte) []byte {
	if n := len(b); n > 0 && b[n-1] == 'y' && hasVowel(b[:n-1]) {
		b[n-1] = 'i'
	}
	return b
}

// stemStep5 drops a final e (5a) and a doubled l (5b) where the stem is long
// enough: "boxe" is "box", "controll" "control"; "hope" stays.
func stemStep5(b []byte) []byte {
	if n := len(b); n > 0 && b[n-1] == 'e' {
		stem := b[:n-1]
		if m := measure(stem); m > 1 || (m == 1 && !endsCVC(stem)) {
			b = stem
		}
	}
	if n := len(b); n >= 2 && b[n-1] == 'l' && b[n-2] == 'l' && measure(b) > 1 {
		b = b[:n-1]
	}
	return b
}
