package cortexdb

import (
	"context"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// Keyword hits scored by how much of the question they cover, weighted by how
// rare each covered term is.
//
// bm25 scores a row against one query string, and the lexical paths run
// several — the question, its keywords OR-ed together, each alternate query —
// keeping each row's best. So one matched term was enough for a high score,
// and a common one did as well as any: on the shared brain "CortexDB
// 的使用场景是？" returned a DRBD lecture note that shares only 使用场景 and a
// knowledge note that mentions CortexDB in passing ("原计划存 CortexDB"), and
// in "CortexDB 是什么，能用来做什么" a memory matching only 是什么 scored 0.82
// and outranked one about CortexDB. Nothing in the score said how much of the
// question a row answers, or whether the words it shares are words every
// seventh memory contains.
//
// The score here does. A question is broken into units — its words, and for
// CJK text the characters its bigrams cover (see lexical_cjk.go) — and each
// unit weighs its inverse document frequency in the store being searched:
//
//	coverage = Σ idf(units the row contains) / Σ idf(units of the question)
//
// A row that shares only the term a third of the store contains covers next
// to nothing; one that holds the question's rare terms covers most of it.
// Document frequencies are counted with one substring scan of the searched
// set — a bucket of memories, a collection of chunks — which is the same SQL
// on SQLite and PostgreSQL (ILIKE there for LIKE) and needs no index of its
// own. A CJK term of two characters, which the trigram index cannot see, is
// counted the same way, or over the bigram path's candidates when it ran.
//
// Keywords and alternate queries a caller supplies are synonyms, not more
// question: a row that contains one is credited at half its weight, without
// enlarging what the question asks for.
//
// Words are compared without their accents, as the word index reads them
// (see foldAccents): the index finds "Universität" for "universitat", and the
// score must not then drop the row for holding none of the question. English
// words are also compared by their stem (see stemWord): "own" is in a row
// that says "owns", and "shipped" in one that says "ships".

// rarityExtraWeight is the credit a supplied keyword earns, as a share of
// what the same term would earn as a word of the question.
const rarityExtraWeight = 0.5

// rarityCJKCharWeight scales a CJK character's weight so that a two-character
// word, which covers two characters, weighs about as much as one English word.
const rarityCJKCharWeight = 0.5

// rarityMaxWords bounds the word units taken from the question and from the
// supplied keywords; CJK bigrams are already bounded by cjkMaxTerms.
const rarityMaxWords = 24

// rarityBM25Share is how much of a keyword hit's relevance the row's own
// bm25 (or bigram BM25) decides: enough to order rows that cover the same
// terms, too little to lift a row over one that covers more of the question.
const rarityBM25Share = 0.2

// rarityQuery is a question broken into the units coverage is counted in.
type rarityQuery struct {
	words []string // non-CJK words of the question, lowercased
	cjk   cjkQuery // CJK bigrams of the question and the characters they cover
	// extras are keyword and alternate-query terms the question itself does
	// not contain: lowercased words and CJK bigrams.
	extras []string
	// common are the words of the alternate queries that are common in their
	// match sets (see variantWords), without their accents. As extras they
	// add their weight, but they are not separate words of a row.
	common map[string]bool
}

// newRarityQuery breaks a question, and the synonyms supplied with it, into
// coverage units. A word typed twice with other accents ("universität
// universitat"), or in two forms ("own owns"), is one unit, the first
// spelling kept: coverage reads both the same, and counted twice it would
// pass the "more than one word" rule alone.
func newRarityQuery(query string, synonyms []string) rarityQuery {
	q := rarityQuery{cjk: cjkQueryTerms(query)}
	words := rarityWords(query, rarityMaxWords)
	have := make(map[string]struct{}, len(words)+len(q.cjk.terms))
	stems := make(map[string]struct{}, len(words))
	for _, w := range words {
		key := foldAccents(w)
		if _, ok := stems[stemWord(key)]; ok {
			continue
		}
		stems[stemWord(key)] = struct{}{}
		have[key] = struct{}{}
		q.words = append(q.words, w)
	}
	for _, t := range q.cjk.terms {
		have[t] = struct{}{}
	}
	// A synonym that is another form of a question word ("owned" for "own")
	// stays a synonym, as the OR query searches for it; inflects keeps it
	// from counting as a second word of a row that holds both.
	//
	// The synonyms' words are capped after the question's own are taken out,
	// as the keyword OR query caps them (see lexicalORSynonyms), so a word
	// that query searches for is credited when a row holds it. Their CJK
	// bigrams are bounded by cjkMaxTerms, apart from the words.
	joined := strings.Join(synonyms, " ")
	add := func(term string, budget *int) {
		key := foldAccents(term)
		if _, ok := have[key]; ok || *budget <= 0 {
			return
		}
		have[key] = struct{}{}
		q.extras = append(q.extras, term)
		*budget--
	}
	wordBudget := rarityMaxWords
	for _, w := range rarityWords(joined, math.MaxInt) {
		add(w, &wordBudget)
	}
	bigrams := cjkMaxTerms
	for _, t := range cjkQueryTerms(joined).terms {
		add(t, &bigrams)
	}
	return q
}

// rarityWords are the non-CJK words of text, lowercased, without stopwords
// and single letters — the same tokens the keyword expansion searches for.
func rarityWords(text string, limit int) []string {
	text = strings.Map(func(r rune) rune {
		if isCJKRune(r) {
			return ' '
		}
		return r
	}, text)
	words := lexicalQueryKeywords(text)
	if len(words) > limit {
		words = words[:limit]
	}
	return words
}

// empty reports a question with nothing to count coverage in: function words
// and stopwords only. Every row then covers all of it.
func (q rarityQuery) empty() bool {
	return len(q.words) == 0 && len(q.cjk.terms) == 0 && len(q.extras) == 0
}

// patterns are the distinct strings whose document frequency the score needs.
// They keep the accents they were typed with: the count is a LIKE, which
// compares them, so a word typed without its accents is counted only in rows
// that spell it so, and weighs as a rare word.
//
// A word is counted as typed, not by its stem: coverage matches "owns" for
// "own", but how rare a word is stays the rarity of the form asked. Counted by
// the stem's root, "Summers" weighed what "summer" does, and on the
// benchmarks (2WikiMultiHopQA, LongMemEval) a name or a plural that is rare as
// typed lost the rows that hold it to rows holding the common root.
func (q rarityQuery) patterns() []string {
	out := make([]string, 0, len(q.words)+len(q.cjk.terms)+len(q.extras))
	out = append(out, q.words...)
	out = append(out, q.cjk.terms...)
	out = append(out, q.extras...)
	return out
}

// patternsBesideBigrams are the patterns other than the question's own CJK
// bigrams, which a caller that runs the bigram path counts there.
func (q rarityQuery) patternsBesideBigrams() []string {
	out := make([]string, 0, len(q.words)+len(q.extras))
	out = append(out, q.words...)
	out = append(out, q.extras...)
	return out
}

// rarityScorer is a rarityQuery weighed against one searched set.
type rarityScorer struct {
	q      rarityQuery
	folded []string  // q.words then q.extras, without their accents
	stems  []string  // stemWord of each folded word
	wordW  []float64 // weight of q.words[i]
	charW  []float64 // weight of each CJK content character of the question
	extraW []float64 // weight of q.extras[i]
	// inflects[i] are the question words q.extras[i] is a form of (see
	// inflects): in a row that holds one of them, the extra is not another
	// word of the row.
	inflects [][]int
	total    float64 // what the whole question weighs
	words    int     // separate words in the question, counted as rarityMatch.words
}

// rarityPriorRows are rows assumed to exist beside the searched set, holding
// none of the question's terms. Inverse document frequency means nothing in a
// store of three memories, where every word that occurs at all occurs in a
// third of it and weighs a fifth of a word that does not; a new user's store
// is exactly that size. Against a few thousand rows the prior changes next to
// nothing.
const rarityPriorRows = 100

// rarityIDF is BM25's inverse document frequency over the searched set and
// the prior rows: always positive, low for a term most rows contain, highest
// for one that none does.
func rarityIDF(df, n int) float64 {
	if df < 0 {
		df = 0
	}
	if n < df {
		n = df
	}
	n += rarityPriorRows
	return math.Log(1 + (float64(n)-float64(df)+0.5)/(float64(df)+0.5))
}

// newRarityScorer weighs q's units by their document frequency df in a set
// of n rows.
//
// A CJK character takes the lowest idf among the bigrams that cover it and
// occur in the set at all. The lowest, because a character belongs to one
// word, and a bigram that straddles two words (用场 in 使用场景) is rare for
// being nonsense, not for being specific. Only those that occur, for the same
// reason: a straddling bigram no row contains says nothing. A character whose
// every bigram is absent from the set belongs to a word the store has never
// seen, and weighs what such a word does — it is part of the question that no
// row can answer, which is what keeps a question about something the store
// does not hold from being "covered" by the one word it shares.
func newRarityScorer(q rarityQuery, df map[string]int, n int) *rarityScorer {
	s := &rarityScorer{q: q}
	for _, w := range append(append([]string{}, q.words...), q.extras...) {
		f := foldAccents(w)
		s.folded = append(s.folded, f)
		s.stems = append(s.stems, stemWord(f))
	}
	for _, w := range q.words {
		weight := rarityIDF(df[w], n)
		s.wordW = append(s.wordW, weight)
		s.total += weight
	}
	if q.cjk.chars > 0 {
		s.charW = make([]float64, q.cjk.chars)
		seen := make([]bool, q.cjk.chars)
		for t, term := range q.cjk.terms {
			if df[term] == 0 {
				continue
			}
			weight := rarityIDF(df[term], n)
			for _, pos := range q.cjk.covers[t] {
				if !seen[pos] || weight < s.charW[pos] {
					s.charW[pos] = weight
					seen[pos] = true
				}
			}
		}
		unseen := rarityIDF(0, n)
		for pos := range s.charW {
			if !seen[pos] {
				s.charW[pos] = unseen
			}
			s.charW[pos] *= rarityCJKCharWeight
			s.total += s.charW[pos]
		}
	}
	for i, e := range q.extras {
		s.extraW = append(s.extraW, rarityExtraWeight*rarityIDF(df[e], n))
		var of []int
		for j := range q.words {
			if inflects(s.folded[j], s.folded[len(q.words)+i]) {
				of = append(of, j)
			}
		}
		s.inflects = append(s.inflects, of)
	}
	s.words = len(q.words)
	for pos, start := range q.cjk.fragmentStart {
		if start {
			s.words++
		} else if pos > 0 && runLength(q.cjk.fragmentStart, pos) == cjkMinSpan {
			s.words++
		}
	}
	// A question of function words only, asked with keywords: the keywords
	// are what it asks, at full weight.
	if s.total == 0 {
		for i := range s.extraW {
			s.extraW[i] /= rarityExtraWeight
			s.total += s.extraW[i]
		}
		s.words = len(s.extraW)
	}
	return s
}

// runLength is how many characters of its fragment end at pos, pos included.
func runLength(fragmentStart []bool, pos int) int {
	n := 1
	for i := pos; i > 0 && !fragmentStart[i]; i-- {
		n++
	}
	return n
}

// rarityMatch is what a row covers of a question.
type rarityMatch struct {
	// share is the part of the question, by weight, the row contains, in [0, 1].
	share float64
	// words counts the separate words of the question it contains, the way
	// the bigram path counts them (see cjkMinSpan): each question word or
	// keyword, each run of covered CJK characters, and a run of cjkMinSpan
	// characters or more as two.
	words int
}

// coverage is what content covers of the question. A question with nothing
// to weigh is covered whole by every row.
func (s *rarityScorer) coverage(content string) rarityMatch {
	if s.total <= 0 {
		return rarityMatch{share: 1, words: s.words}
	}
	lower := strings.ToLower(content)
	folded := foldAccents(lower)
	var stems map[string]struct{}
	holds := func(i int) bool {
		if containsWord(folded, s.folded[i]) {
			return true
		}
		if stems == nil {
			stems = stemmedWords(folded)
		}
		_, ok := stems[s.stems[i]]
		return ok
	}
	var m rarityMatch
	var got float64
	held := make([]bool, len(s.q.words))
	for i := range s.q.words {
		if holds(i) {
			got += s.wordW[i]
			m.words++
			held[i] = true
		}
	}
	if len(s.charW) > 0 {
		covered := make([]bool, len(s.charW))
		for t, term := range s.q.cjk.terms {
			if !strings.Contains(content, term) {
				continue
			}
			for _, pos := range s.q.cjk.covers[t] {
				covered[pos] = true
			}
		}
		run := 0
		for pos, c := range covered {
			if s.q.cjk.fragmentStart[pos] {
				run = 0
			}
			if !c {
				run = 0
				continue
			}
			got += s.charW[pos]
			run++
			if run == 1 || run == cjkMinSpan {
				m.words++
			}
		}
	}
	for i, e := range s.q.extras {
		var hit bool
		if isCJKTerm(e) {
			hit = strings.Contains(content, e)
		} else {
			hit = holds(len(s.q.words) + i)
		}
		if hit {
			got += s.extraW[i]
			if !s.q.common[foldAccents(e)] && !heldInflection(held, s.inflects[i]) {
				m.words++
			}
		}
	}
	m.share = min(1, got/s.total)
	return m
}

// answers reports whether a match is evidence of an answer: it covers at
// least floor of the question, and more than one of its words — unless the
// question is one word, or the row covers all of it.
//
// One shared word is not an answer, however rare. 如何学会弹吉他 shares 会弹
// with every memory about a dialog that 会弹出, and the coverage of that one
// bigram is real; that it is one word, and not the one asked about, is what
// gives it away. The bigram path has always held CJK rows to this (see
// cjkMinSpan); it now holds for words and keywords too.
func (s *rarityScorer) answers(m rarityMatch, floor float64) bool {
	if m.share <= 0 || m.share < floor {
		return false
	}
	return m.words >= 2 || s.words <= 1 || m.share >= rarityWhole
}

// rarityWhole is the share above which a row covers the whole question, up to
// floating-point error.
const rarityWhole = 0.999

func isCJKTerm(term string) bool {
	r, _ := utf8.DecodeRuneInString(term)
	return isCJKRune(r)
}

// containsWord reports whether lower contains word as a word, or for a word
// of four letters or more as the start of one — "deploy" in "deployed", as
// the prefix forms of the keyword search match — but never inside one: "ha"
// is not in "that". Both sides are already lowercased.
func containsWord(lower, word string) bool {
	if word == "" {
		return false
	}
	prefixOK := utf8.RuneCountInString(word) >= 4
	for from := 0; from < len(lower); {
		i := strings.Index(lower[from:], word)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(word)
		before, _ := utf8.DecodeLastRuneInString(lower[:start])
		after, _ := utf8.DecodeRuneInString(lower[end:])
		if (start == 0 || !isWordRune(before)) && (prefixOK || end == len(lower) || !isWordRune(after)) {
			return true
		}
		from = start + 1
	}
	return false
}

// foldAccents removes the accents of lowercased text as the word index's
// tokenizer does: FTS5 unicode61, with its default remove_diacritics 1. A
// Latin letter with one diacritic is the letter itself ("é" is "e", "ș" is
// "s", "ơ" is "o", "ſ" is "s"), and the combining marks that tokenizer
// removes go. What it keeps stays: letters that are not an accented form of
// another ("ß", "æ", "ø", "ł"), and letters with two diacritics ("ế", "ǖ").
// TestFoldAccentsMatchesTheWordIndex reads each rune of latinFolds back from
// the index.
func foldAccents(lower string) string {
	for i := 0; i < len(lower); i++ {
		if lower[i] >= utf8.RuneSelf {
			return strings.Map(foldAccent, lower)
		}
	}
	return lower
}

func foldAccent(r rune) rune {
	for _, block := range latinFolds {
		if i := int(r - block.first); i >= 0 && i < len(block.to) {
			switch c := block.to[i]; c {
			case '.':
				return r
			case '-':
				return -1
			default:
				return rune(c)
			}
		}
	}
	return r
}

// latinFolds maps runes from first on to the letter the word index reads
// them as, '-' for a mark it removes, '.' for a rune it keeps.
var latinFolds = []struct {
	first rune
	to    string
}{
	{0x00e0, "" + // Latin-1 Supplement, Latin Extended-A and B
		"aaaaaa.ceeeeiiii.nooooo..uuuuy.y" + // U+00E0
		"aaaaaaccccccccdd..eeeeeeeeeegggg" + // U+0100
		"gggghh..iiiiiiiii...jjkk.llllll." + // U+0120
		"...nnnnnn...oooooo..rrrrrrssssss" + // U+0140
		"sstttt..uuuuuuuuuuuuwwyyyzzzzzzs" + // U+0160
		"................................" + // U+0180
		"oo.............uu..............." + // U+01A0
		".............aaiioouu..........." + // U+01C0
		"......ggkkoo....j...gg..nn......" + // U+01E0
		"aaaaeeeeiiiioooorrrruuuusstt..hh" + // U+0200
		"......aaee....oo..yy"}, //             U+0220
	{0x0300, "" + // combining diacritical marks
		"-----.-------..-.-.........-...." + // U+0300
		"...------....--.--"}, //               U+0320
	{0x1e00, "" + // Latin Extended Additional
		"aabbbbbb..dddddddddd....eeee..ff" + // U+1E00
		"gghhhhhhhhhhii..kkkkkkll..llllmm" + // U+1E20
		"mmmmnnnnnnnn........pppprrrr..rr" + // U+1E40
		"ssss......ttttttttuuuuuu....vvvv" + // U+1E60
		"wwwwwwwwwwxxxxyyzzzzzzhtwy.s...." + // U+1E80
		"aaaa....................eeeeee.." + // U+1EA0
		"........iiiioooo................" + // U+1EC0
		"....uuuu..........yyyyyyyy"}, //       U+1EE0
}

// inflects reports whether two words are forms of one as coverage matches
// them: they have one stem ("ship", "shipped"), or the longer starts with the
// shorter, of four letters or more ("recommend", "recommendation"). A row
// that holds "recommendations" holds both, and that is one word of the
// question, not two.
func inflects(a, b string) bool {
	if a == b {
		return false
	}
	if stemWord(a) == stemWord(b) {
		return true
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	return utf8.RuneCountInString(a) >= 4 && strings.HasPrefix(b, a)
}

// heldInflection reports whether a row holds one of the question words of.
func heldInflection(held []bool, of []int) bool {
	for _, j := range of {
		if held[j] {
			return true
		}
	}
	return false
}

// isWordRune is a rune that continues a word for containsWord: a letter or
// digit outside the CJK scripts, which run into Latin text unspaced
// ("CortexDB的使用场景") and do not make it one word.
func isWordRune(r rune) bool {
	return (unicode.IsLetter(r) || unicode.IsDigit(r)) && !isCJKRune(r)
}

// corpusStats describes a searched set for the rarity score: how many rows it
// has, their mean length in characters, and how many rows contain each term.
type corpusStats struct {
	n      int
	avgLen float64
	df     map[string]int
}

// memoryBucketStats is corpusStats for one memory bucket.
type memoryBucketStats = corpusStats

// scanCorpusStats measures the rows that from/where select in one scan,
// counting the rows that contain each pattern.
//
// It is a substring count, so a short word's count includes rows where it is
// part of a longer one ("ha" in "that"). That only ever overstates how common
// a term is, which errs toward weighing it less.
func (db *DB) scanCorpusStats(ctx context.Context, column, from, where string, whereArgs []any, patterns []string) (corpusStats, error) {
	like := column + ` LIKE ? ESCAPE '\'`
	if d := db.Dialect(); d != nil && d.Kind() == sqldialect.Postgres {
		// LIKE is case-sensitive there and case-insensitive (for ASCII) in
		// SQLite; ILIKE makes the two count the same rows.
		like = column + ` ILIKE ? ESCAPE '\'`
	}
	var b strings.Builder
	b.WriteString("SELECT COUNT(*), COALESCE(AVG(LENGTH(" + column + ")), 0)")
	args := make([]any, 0, len(patterns)+len(whereArgs))
	for _, p := range patterns {
		b.WriteString(", COALESCE(SUM(CASE WHEN " + like + " THEN 1 ELSE 0 END), 0)")
		args = append(args, core.SubstringPattern(p))
	}
	b.WriteString(" FROM " + from)
	if where != "" {
		b.WriteString(" WHERE " + where)
	}
	args = append(args, whereArgs...)

	var n int64
	var avgLen float64
	counts := make([]int64, len(patterns))
	dest := make([]any, 0, len(counts)+2)
	dest = append(dest, &n, &avgLen)
	for i := range counts {
		dest = append(dest, &counts[i])
	}
	if err := db.queryRow(ctx, b.String(), args...).Scan(dest...); err != nil {
		return corpusStats{}, fmt.Errorf("count term frequencies: %w", err)
	}
	stats := corpusStats{n: int(n), avgLen: avgLen, df: make(map[string]int, len(patterns))}
	for i, p := range patterns {
		stats.df[p] = int(counts[i])
	}
	return stats, nil
}

// memoryBucketStatsFor measures one memory bucket, counting the patterns
// given. The CJK bigrams of a question are not among them: the bigram path
// counts those over its candidates (see searchMemoryCJK).
func (db *DB) memoryBucketStatsFor(ctx context.Context, bucketID string, patterns []string) (memoryBucketStats, error) {
	return db.scanCorpusStats(ctx, "content", "messages", "session_id = ?", []any{bucketID}, patterns)
}

// chunkRarityScorer weighs q against the knowledge chunks of a collection, or
// of every collection when it is empty.
func (db *DB) chunkRarityScorer(ctx context.Context, collection string, q rarityQuery) (*rarityScorer, error) {
	if q.empty() {
		return newRarityScorer(q, nil, 0), nil
	}
	from := "embeddings e"
	where := ""
	var args []any
	if collection != "" {
		from = "embeddings e LEFT JOIN collections c ON e.collection_id = c.id"
		where = "c.name = ?"
		args = append(args, collection)
	}
	stats, err := db.scanCorpusStats(ctx, "e.content", from, where, args, q.patterns())
	if err != nil {
		return nil, err
	}
	return newRarityScorer(q, stats.df, stats.n), nil
}

// rarityRelevance is a keyword hit's relevance: its coverage of the question,
// with the row's own bm25 relevance (in [0,1)) deciding among rows that cover
// the same.
func rarityRelevance(coverage, bm25 float64) float64 {
	return coverage * (1 - rarityBM25Share + rarityBM25Share*bm25)
}
