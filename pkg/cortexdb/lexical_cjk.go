package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// Lexical recall for CJK queries written as sentences.
//
// Chinese has no spaces, so the lexical path saw 微信机器人为什么从来不主动给我发提醒
// as one seventeen-character token and asked the trigram index for it as a
// phrase. Only a memory containing those seventeen characters in that order
// could match, which is none: on a snapshot of the shared brain, five of the six
// Chinese sentence queries in the golden set returned an empty list — not a
// wrong ranking, nothing at all — while the memory that answers them was there,
// containing 微信, 主动 and 提醒.
//
// The trigram index cannot rescue this by itself. Most Chinese words are two
// characters long, and a trigram index matches nothing shorter than three. So
// this path works on character bigrams — the classic unit for Chinese retrieval
// without a dictionary — found by substring match and ranked with BM25 in Go:
//
//   - The query is cut at function words (的, 了, 为什么, 怎么, 那个, …) and each
//     remaining fragment is broken into overlapping bigrams.
//   - Candidates are the rows containing any bigram. Because that set is every
//     row that contains any term, the document frequencies counted over it are
//     exact, not estimates.
//   - A row must match more than one word of a question to be returned at all
//     (see cjkMinCovered and cjkMinCoverage). Breaking a question into bigrams
//     makes it easy to match; without a floor, 北京明天会下雨吗 returns every
//     memory that mentions 北京.
//   - Coverage is counted in characters, not terms. Overlapping bigrams straddle
//     word boundaries — 微信机器人 yields 信机, which no row contains — and a
//     term count would hold that against every row. Counting the query's
//     characters that some matched bigram covers does not: 信 and 机 are covered
//     by 微信 and 机器. The first version instead dropped unseen bigrams from the
//     denominator, and that turned a query mostly about things the store has
//     never heard of into one "fully covered" by the single word it had: every
//     negative in the golden set returned five rows at 100% coverage.
//
// It is the same arithmetic on both backends: the SQL is a substring filter,
// and the ranking happens here.

// A row is returned only if what it covers of the query is more than one word:
// two separate stretches of it, or one stretch of at least cjkMinSpan
// characters, or the whole query when the query is shorter than that. It must
// also cover at least cjkMinCoverage of the query's content characters.
//
// Measured on the golden set against a snapshot of the shared brain. Every
// negative query that matched anything matched one word: 北京 for "will it rain
// in Beijing tomorrow", 推荐 for "recommend some films", 学会 for "how do I learn
// guitar" — at 20–50% coverage. The weakest true answers cover 40% of a long
// question, across several words. A first cut at "at least three characters"
// let 周末去哪里爬山 (where to hike at the weekend) return a note about going to a
// coffee shop at the weekend, on 周末去: one word and the commonest verb there
// is, and none of the question's point. Counting stretches rather than
// characters says what was meant: a single shared word is not evidence of an
// answer, unless that word is the whole question.
const (
	cjkMinSpan     = 4
	cjkMinCoverage = 0.3
)

// cjkMaxTerms bounds the substring filter. A paragraph pasted as a query would
// otherwise become hundreds of LIKE clauses; the first bigrams of a question are
// the ones it is about.
const cjkMaxTerms = 32

// cjkMaxCandidates bounds how many rows the substring filter may return before
// scoring. Document frequencies are exact below it and a lower bound above it,
// which only flattens the ranking of terms too common to matter.
const cjkMaxCandidates = 5000

// BM25 parameters, the usual ones.
const (
	cjkBM25K1 = 1.2
	cjkBM25B  = 0.75
)

// cjkFunctionWords are the words a question is made of rather than about.
// Matched longest first, so 为什么 is removed whole instead of leaving 为 and 什.
//
// Single characters are kept to those that almost never begin a content word.
// 上 is not here, because removing it breaks 上海 into 海; 会 is not here either
// (会议), nor 能 (性能), 说 (说明) or 在 (存在 is harmless, but 在线 is not).
var cjkFunctionWords = func() map[string]struct{} {
	words := []string{
		"为什么", "怎么样", "是不是", "有没有", "能不能", "要不要", "会不会", "是什么", "什么样", "怎么办", "为啥子",
		"什么", "怎么", "怎样", "如何", "为啥", "为何", "哪个", "哪些", "哪里", "哪儿", "那个", "这个", "那些", "这些",
		"我们", "你们", "他们", "它们", "咱们", "自己", "一个", "一下", "一直", "从来", "已经", "还是", "或者", "而且",
		"但是", "因为", "所以", "如果", "就是", "可以", "应该", "需要", "现在", "刚才", "刚刚", "之前", "之后", "时候",
		"问题", "情况", "东西", "事情", "那么", "这么", "居然", "竟然", "到底", "究竟", "是否", "能否", "没有", "不是",
		"的", "了", "吗", "呢", "吧", "啊", "呀", "嘛", "我", "你", "他", "她", "它", "是", "这", "那", "哪", "谁",
		"啥", "把", "被", "给", "么", "很", "都", "也", "不", "没", "又",
	}
	set := make(map[string]struct{}, len(words))
	for _, w := range words {
		set[w] = struct{}{}
	}
	return set
}()

// cjkFunctionWordMaxRunes is the longest entry in cjkFunctionWords.
const cjkFunctionWordMaxRunes = 3

func isCJKRune(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r)
}

// cjkQuery is a query broken into bigrams, with which of the query's content
// characters each bigram covers.
type cjkQuery struct {
	terms  []string
	covers [][]int // covers[t] are the character positions terms[t] spans
	chars  int     // content characters: those left after function words
	// fragmentStart marks the positions that begin a fragment, so a stretch of
	// covered characters never runs across a function word.
	fragmentStart []bool
}

// cjkQueryTerms breaks the CJK part of a query into the bigrams it is about.
// Non-CJK text is ignored here; the word index already handles it, and the two
// result sets are merged by the caller.
func cjkQueryTerms(query string) cjkQuery {
	var fragments [][]rune
	var current []rune
	flush := func() {
		if len(current) >= 2 {
			fragments = append(fragments, current)
		}
		current = nil
	}

	runes := []rune(query)
	for i := 0; i < len(runes); {
		if !isCJKRune(runes[i]) {
			flush()
			i++
			continue
		}
		matched := 0
		for n := cjkFunctionWordMaxRunes; n >= 1; n-- {
			if i+n > len(runes) {
				continue
			}
			if _, ok := cjkFunctionWords[string(runes[i:i+n])]; ok {
				matched = n
				break
			}
		}
		if matched > 0 {
			flush()
			i += matched
			continue
		}
		current = append(current, runes[i])
		i++
	}
	flush()

	var q cjkQuery
	index := make(map[string]int)
	offset := 0
	for _, fragment := range fragments {
		q.fragmentStart = append(q.fragmentStart, true)
		for range fragment[1:] {
			q.fragmentStart = append(q.fragmentStart, false)
		}
		for i := 0; i+1 < len(fragment); i++ {
			bigram := string(fragment[i : i+2])
			t, seen := index[bigram]
			if !seen {
				if len(q.terms) == cjkMaxTerms {
					continue
				}
				t = len(q.terms)
				index[bigram] = t
				q.terms = append(q.terms, bigram)
				q.covers = append(q.covers, nil)
			}
			q.covers[t] = append(q.covers[t], offset+i, offset+i+1)
		}
		offset += len(fragment)
	}
	q.chars = offset
	return q
}

// cjkScored is one candidate's BM25 score and the share of the query it covers.
type cjkScored struct {
	index    int
	score    float64
	coverage float64
}

// scoreCJKCandidates ranks candidates against the query with BM25 and drops
// those that do not cover enough of it (see cjkMinCovered). corpusSize and
// avgLen describe the whole searchable set, not just the candidates.
func scoreCJKCandidates(q cjkQuery, contents []string, corpusSize int, avgLen float64) []cjkScored {
	if len(q.terms) == 0 || len(contents) == 0 || q.chars == 0 {
		return nil
	}
	if corpusSize < len(contents) {
		corpusSize = len(contents)
	}
	if avgLen <= 0 {
		avgLen = 1
	}

	df := make([]int, len(q.terms))
	tf := make([][]int, len(contents))
	for d, content := range contents {
		tf[d] = make([]int, len(q.terms))
		for t, term := range q.terms {
			if n := strings.Count(content, term); n > 0 {
				tf[d][t] = n
				df[t]++
			}
		}
	}

	var out []cjkScored
	covered := make([]bool, q.chars)
	for d, content := range contents {
		length := float64(utf8.RuneCountInString(content))
		for i := range covered {
			covered[i] = false
		}
		var score float64
		for t := range q.terms {
			n := float64(tf[d][t])
			if n == 0 {
				continue
			}
			idf := math.Log(1 + (float64(corpusSize)-float64(df[t])+0.5)/(float64(df[t])+0.5))
			score += idf * n * (cjkBM25K1 + 1) / (n + cjkBM25K1*(1-cjkBM25B+cjkBM25B*length/avgLen))
			for _, pos := range q.covers[t] {
				covered[pos] = true
			}
		}
		hit, spans, longest, run := 0, 0, 0, 0
		for i, c := range covered {
			if q.fragmentStart[i] {
				run = 0
			}
			if !c {
				run = 0
				continue
			}
			hit++
			if run == 0 {
				spans++
			}
			run++
			longest = max(longest, run)
		}
		coverage := float64(hit) / float64(q.chars)
		moreThanOneWord := spans >= 2 || longest >= cjkMinSpan || hit == q.chars
		if coverage < cjkMinCoverage || !moreThanOneWord {
			continue
		}
		out = append(out, cjkScored{index: d, score: score, coverage: coverage})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	return out
}

// cjkRelevance maps a BM25 score onto [0,1) the way the other lexical paths map
// theirs, weighted by coverage so that a row matching every idea in the question
// outranks one that repeats a single term many times.
func cjkRelevance(s cjkScored) float64 {
	weighted := s.score * s.coverage
	return weighted / (1 + weighted)
}

// cjkLikeClause builds "(col LIKE ? ESCAPE '\' OR ...)" with one pattern per term.
func cjkLikeClause(column string, terms []string) (string, []any) {
	parts := make([]string, len(terms))
	args := make([]any, len(terms))
	for i, term := range terms {
		parts[i] = column + ` LIKE ? ESCAPE '\'`
		args[i] = core.SubstringPattern(term)
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

// searchMemoryCJK is the bigram path for memories in one bucket.
func (db *DB) searchMemoryCJK(ctx context.Context, bucketID, query string, topK int) ([]MemorySearchHit, error) {
	q := cjkQueryTerms(query)
	if len(q.terms) == 0 {
		return nil, nil
	}

	var corpusSize int
	var avgLen float64
	if err := db.queryRow(ctx,
		`SELECT COUNT(*), COALESCE(AVG(LENGTH(content)), 0) FROM messages WHERE session_id = ?`,
		bucketID).Scan(&corpusSize, &avgLen); err != nil {
		return nil, fmt.Errorf("measure memory bucket: %w", err)
	}
	if corpusSize == 0 {
		return nil, nil
	}

	like, likeArgs := cjkLikeClause("m.content", q.terms)
	args := append([]any{bucketID}, likeArgs...)
	args = append(args, cjkMaxCandidates)
	rows, err := db.query(ctx, `
		SELECT m.id, m.session_id, s.user_id, m.role, m.content, m.metadata, m.created_at
		FROM messages m
		JOIN sessions s ON s.id = m.session_id
		WHERE m.session_id = ? AND `+like+`
		ORDER BY m.id
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("search memory cjk: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var records []MemoryRecord
	var contents []string
	for rows.Next() {
		var record MemoryRecord
		var metadataJSON []byte
		if err := rows.Scan(&record.ID, &record.SessionID, &record.UserID, &record.Role, &record.Content, &metadataJSON, &record.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan cjk memory: %w", err)
		}
		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &record.Metadata); err != nil {
				return nil, fmt.Errorf("decode cjk memory metadata: %w", err)
			}
		}
		applyMemoryMetadata(&record)
		if memoryExpired(record) || memorySuperseded(record) {
			continue
		}
		records = append(records, record)
		contents = append(contents, record.Content)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cjk memory rows: %w", err)
	}

	scored := scoreCJKCandidates(q, contents, corpusSize, avgLen)
	if len(scored) > topK*4 {
		scored = scored[:topK*4]
	}
	now := time.Now().UTC()
	hits := make([]MemorySearchHit, 0, len(scored))
	for _, s := range scored {
		record := records[s.index]
		hits = append(hits, MemorySearchHit{Memory: record, Score: applyMemoryRecallBoosts(cjkRelevance(s), record, now)})
	}
	return hits, nil
}

// searchTextCJK is the bigram path for knowledge chunks. It returns rows in the
// same shape as the FTS path, so authorization, filtering and merging downstream
// treat them alike.
func (db *DB) searchTextCJK(ctx context.Context, query string, opts TextSearchOptions) ([]core.ScoredEmbedding, error) {
	q := cjkQueryTerms(query)
	if len(q.terms) == 0 {
		return nil, nil
	}
	topK := opts.TopK
	if topK <= 0 {
		topK = defaultSearchTopK
	}

	collectionClause := ""
	var collectionArgs []any
	if opts.Collection != "" {
		collectionClause = " AND c.name = ?"
		collectionArgs = append(collectionArgs, opts.Collection)
	}

	var corpusSize int
	var avgLen float64
	if err := db.queryRow(ctx, `
		SELECT COUNT(*), COALESCE(AVG(LENGTH(e.content)), 0)
		FROM embeddings e
		LEFT JOIN collections c ON e.collection_id = c.id
		WHERE 1 = 1`+collectionClause, collectionArgs...).Scan(&corpusSize, &avgLen); err != nil {
		return nil, fmt.Errorf("measure chunks: %w", err)
	}
	if corpusSize == 0 {
		return nil, nil
	}

	// Two passes: ids and text to score, then full rows for the winners only.
	// The vector column is the bulk of a row and most candidates are discarded.
	like, likeArgs := cjkLikeClause("e.content", q.terms)
	args := append(append([]any{}, likeArgs...), collectionArgs...)
	args = append(args, cjkMaxCandidates)
	rows, err := db.query(ctx, `
		SELECT e.id, e.content
		FROM embeddings e
		LEFT JOIN collections c ON e.collection_id = c.id
		WHERE `+like+collectionClause+`
		ORDER BY e.id
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("search chunks cjk: %w", err)
	}
	var ids, contents []string
	for rows.Next() {
		var id, content string
		if err := rows.Scan(&id, &content); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan cjk chunk: %w", err)
		}
		ids = append(ids, id)
		contents = append(contents, content)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate cjk chunks: %w", err)
	}
	_ = rows.Close()

	scored := scoreCJKCandidates(q, contents, corpusSize, avgLen)
	if len(scored) > topK*4 {
		scored = scored[:topK*4]
	}
	if len(scored) == 0 {
		return nil, nil
	}

	relevance := make(map[string]float64, len(scored))
	placeholders := make([]string, len(scored))
	fetchArgs := make([]any, len(scored))
	for i, s := range scored {
		relevance[ids[s.index]] = cjkRelevance(s)
		placeholders[i] = "?"
		fetchArgs[i] = ids[s.index]
	}
	full, err := db.query(ctx, `
		SELECT e.id, e.collection_id, c.name, e.vector, e.content, e.doc_id, e.metadata, e.acl, 0 as score
		FROM embeddings e
		LEFT JOIN collections c ON e.collection_id = c.id
		WHERE e.id IN (`+strings.Join(placeholders, ", ")+`)`, fetchArgs...)
	if err != nil {
		return nil, fmt.Errorf("fetch cjk chunks: %w", err)
	}
	results, err := scanFTSRows(full, 0)
	if err != nil {
		return nil, err
	}
	out := results[:0]
	for _, r := range results {
		r.Score = relevance[r.ID]
		if opts.Threshold > 0 && r.Score < opts.Threshold {
			continue
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].ID < out[j].ID
		}
		return out[i].Score > out[j].Score
	})
	return out, nil
}

// mergeScoredEmbeddings keeps the higher score for a row found by both paths
// and returns the union ranked, cut to topK.
func mergeScoredEmbeddings(topK int, sets ...[]core.ScoredEmbedding) []core.ScoredEmbedding {
	best := make(map[string]core.ScoredEmbedding)
	for _, set := range sets {
		for _, r := range set {
			if existing, ok := best[r.ID]; !ok || r.Score > existing.Score {
				best[r.ID] = r
			}
		}
	}
	out := make([]core.ScoredEmbedding, 0, len(best))
	for _, r := range best {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].ID < out[j].ID
		}
		return out[i].Score > out[j].Score
	})
	if topK > 0 && len(out) > topK {
		out = out[:topK]
	}
	return out
}
