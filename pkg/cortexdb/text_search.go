package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"math"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"

	"github.com/liliang-cn/cortexdb/v2/internal/encoding"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// defaultSearchTopK is the page size used when a caller names none. top_k is optional throughout the
// tool surface, so every path that truncates by it has to settle on this first — a zero carried into a
// truncation returns an empty result set rather than the default page.
const defaultSearchTopK = 10

// TextSearchOptions defines options for text-only search.
//
// Threshold drops rows whose normalized bm25 is below it. With Keywords or
// AlternateQueries it applies to each phrasing's own bm25, before the lists
// are fused; the scores returned are then fused ranks, at most 1/61 for
// each list that holds the row, not of the threshold's scale.
type TextSearchOptions struct {
	Collection       string
	TopK             int
	Threshold        float64
	Keywords         []string
	AlternateQueries []string

	// Authorize, when set, is a retrieval-layer security gate: it is applied to
	// every candidate and only authorized rows count toward TopK. The search
	// over-fetches internally so the caller still receives up to TopK authorized
	// results. This pushes access control (RBAC/ABAC) into the retrieval boundary
	// instead of leaving it to post-filtering in application code.
	Authorize func(core.ScoredEmbedding) bool

	// Reranker, when set, re-scores and reorders the authorized candidate set
	// (typically a cross-encoder or LLM) before MinScore filtering and TopK
	// truncation — the standard recall→precision second stage.
	Reranker core.Reranker

	// MinScore, when > 0, drops candidates whose (possibly reranked) Score is
	// below the threshold — a relevance floor that prevents weak tail matches
	// from being surfaced.
	MinScore float64

	// skipCJKBigrams is set by callers that run several phrasings of one
	// question through SearchTextOnly and add the CJK bigram path once
	// themselves, so it is not repeated for every phrasing.
	skipCJKBigrams bool
	// reviewStatus is the review status the plan of a tool lane asks for,
	// so that the gate keeps it instead of hiding it first.
	reviewStatus string
}

// InsertText inserts text with automatic embedding generation.
func (db *DB) InsertText(ctx context.Context, id string, text string, metadata map[string]string) error {
	if db.embedder == nil {
		return ErrEmbedderNotConfigured
	}
	if text == "" {
		return ErrEmptyText
	}

	vec, err := db.embedder.Embed(ctx, text)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrEmbeddingFailed, err)
	}

	embedding := &core.Embedding{
		ID:       id,
		Vector:   vec,
		Content:  text,
		Metadata: signedCopy(ctx, metadata),
	}
	return db.store.Upsert(ctx, embedding)
}

// InsertTextBatch inserts multiple texts with automatic embedding generation.
func (db *DB) InsertTextBatch(ctx context.Context, texts map[string]string, metadata map[string]string) error {
	if db.embedder == nil {
		return ErrEmbedderNotConfigured
	}

	ids := make([]string, 0, len(texts))
	textList := make([]string, 0, len(texts))
	for id, text := range texts {
		if text == "" {
			continue
		}
		ids = append(ids, id)
		textList = append(textList, text)
	}
	if len(textList) == 0 {
		return nil
	}

	vectors, err := db.embedder.EmbedBatch(ctx, textList)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrEmbeddingFailed, err)
	}

	metadata = signedCopy(ctx, metadata)
	embeddings := make([]*core.Embedding, len(ids))
	for i, id := range ids {
		embeddings[i] = &core.Embedding{
			ID:       id,
			Vector:   vectors[i],
			Content:  textList[i],
			Metadata: metadata,
		}
	}
	return db.store.UpsertBatch(ctx, embeddings)
}

// InsertTextWithVector inserts text with a pre-computed vector.
func (db *DB) InsertTextWithVector(ctx context.Context, id string, text string, vector []float32, metadata map[string]string) error {
	if text == "" {
		return ErrEmptyText
	}
	if vector == nil {
		return ErrInvalidVector
	}

	embedding := &core.Embedding{
		ID:       id,
		Vector:   vector,
		Content:  text,
		Metadata: metadata,
	}
	return db.store.UpsertBatchWithAdapt(ctx, []*core.Embedding{embedding})
}

// InsertTextBatchWithVectors inserts multiple texts with pre-computed vectors.
func (db *DB) InsertTextBatchWithVectors(ctx context.Context, texts map[string]string, vectors [][]float32, metadata map[string]string) error {
	if len(texts) == 0 {
		return nil
	}
	if len(vectors) == 0 {
		return fmt.Errorf("vectors cannot be empty")
	}

	ids := make([]string, 0, len(texts))
	textList := make([]string, 0, len(texts))
	for id, text := range texts {
		if text == "" {
			continue
		}
		ids = append(ids, id)
		textList = append(textList, text)
	}
	if len(textList) == 0 {
		return nil
	}
	if len(ids) != len(vectors) {
		return fmt.Errorf("text count (%d) must match vector count (%d)", len(ids), len(vectors))
	}

	embeddings := make([]*core.Embedding, len(ids))
	for i, id := range ids {
		embeddings[i] = &core.Embedding{
			ID:       id,
			Vector:   vectors[i],
			Content:  textList[i],
			Metadata: metadata,
		}
	}
	return db.store.UpsertBatchWithAdapt(ctx, embeddings)
}

// SearchText performs similarity search using text query.
func (db *DB) SearchText(ctx context.Context, query string, topK int) ([]core.ScoredEmbedding, error) {
	return db.SearchTextInCollection(ctx, "", query, topK)
}

// SearchTextInCollection performs similarity search using text query within a collection.
func (db *DB) SearchTextInCollection(ctx context.Context, collection string, query string, topK int) ([]core.ScoredEmbedding, error) {
	if query == "" {
		return nil, ErrEmptyText
	}
	if db.embedder == nil {
		return db.SearchTextOnly(ctx, query, TextSearchOptions{
			Collection: collection,
			TopK:       topK,
		})
	}

	vec, err := db.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEmbeddingFailed, err)
	}

	defer observability.Time(ctx, "vector")()
	return db.gatedSearch(ctx, topK, func(k int) ([]core.ScoredEmbedding, error) {
		return db.store.Search(ctx, vec, core.SearchOptions{Collection: collection, TopK: k, QueryText: query})
	})
}

// HybridSearchText performs hybrid search combining vector and keyword matching.
func (db *DB) HybridSearchText(ctx context.Context, query string, topK int) ([]core.ScoredEmbedding, error) {
	if query == "" {
		return nil, ErrEmptyText
	}
	var vec []float32
	if db.embedder != nil {
		embedded, err := db.embedder.Embed(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrEmbeddingFailed, err)
		}
		vec = embedded
	}
	return db.gatedSearch(ctx, topK, func(k int) ([]core.ScoredEmbedding, error) {
		return db.store.HybridSearch(ctx, vec, query, core.HybridSearchOptions{
			SearchOptions: core.SearchOptions{TopK: k},
		})
	})
}

// HybridSearchTextWithOptions is the full retrieval pipeline for production RAG:
// hybrid recall (vector + BM25 when an embedder is set, BM25-only otherwise),
// then the optional retrieval-layer stages in order:
//
//	recall(over-fetch) → Authorize(RBAC/ABAC) → Reranker → MinScore → TopK
//
// Keeping these stages inside the retrieval call makes access control and the
// relevance floor non-bypassable by callers.
func (db *DB) HybridSearchTextWithOptions(ctx context.Context, query string, opts TextSearchOptions) ([]core.ScoredEmbedding, error) {
	if query == "" {
		return nil, ErrEmptyText
	}
	wantK := opts.TopK
	if wantK <= 0 {
		wantK = 10
	}

	// Over-fetch so enough survive Authorize/Reranker to still fill TopK.
	fetchK := wantK
	if opts.Authorize != nil || opts.Reranker != nil {
		fetchK = overfetchK(wantK)
	}
	candidates, err := db.HybridSearchText(ctx, query, fetchK)
	if err != nil {
		return nil, err
	}

	// 1) Authorization gate.
	//
	// Widened until TopK candidates pass or the corpus runs out, because a
	// single fixed over-fetch only holds while the predicate is generous. A
	// selective one — a user who may read a small share of the corpus, or a
	// caller separating one source from another — sees every one of its rows
	// ranked below the first fetchK and gets back a near-empty result that
	// looks like "nothing matched". Observed on a store where one imported code
	// graph outnumbered the prose ten to one: the prose scored the same as the
	// code and sat past rank 50, so a fetch of 50 returned none of it.
	if opts.Authorize != nil {
		candidates, err = db.recallAuthorized(ctx, query, candidates, fetchK, wantK, opts.Authorize)
		if err != nil {
			return nil, err
		}
	}

	// 2) Rerank (cross-encoder / LLM second stage).
	if opts.Reranker != nil && len(candidates) > 0 {
		reranked, rerr := opts.Reranker.Rerank(ctx, query, candidates)
		if rerr != nil {
			return nil, fmt.Errorf("rerank: %w", rerr)
		}
		candidates = reranked
	}

	// 3) Relevance floor.
	if opts.MinScore > 0 {
		kept := candidates[:0]
		for _, c := range candidates {
			if c.Score >= opts.MinScore {
				kept = append(kept, c)
			}
		}
		candidates = kept
	}

	// 4) Final TopK.
	if len(candidates) > wantK {
		candidates = candidates[:wantK]
	}
	return candidates, nil
}

// SearchTextOnly performs pure FTS5 full-text search without embeddings.
func (db *DB) SearchTextOnly(ctx context.Context, query string, opts TextSearchOptions) ([]core.ScoredEmbedding, error) {
	if query == "" {
		return nil, ErrEmptyText
	}
	if opts.TopK <= 0 {
		opts.TopK = defaultSearchTopK
	}
	ctx = withVariantMemo(ctx)

	// Retrieval-layer authorization: over-fetch, apply the gate, then return up
	// to TopK authorized rows. Keeping this here (rather than in the caller)
	// makes the security predicate a non-bypassable part of retrieval.
	if opts.Authorize != nil {
		authorize := opts.Authorize
		wantK := opts.TopK
		inner := opts
		inner.Authorize = nil
		inner.TopK = overfetchK(wantK)
		candidates, err := db.SearchTextOnly(ctx, query, inner)
		if err != nil {
			return nil, err
		}
		out := make([]core.ScoredEmbedding, 0, wantK)
		for _, c := range candidates {
			if authorize(c) {
				out = append(out, c)
				if len(out) >= wantK {
					break
				}
			}
		}
		return out, nil
	}

	return db.gatedSearchFor(ctx, opts.TopK, opts.reviewStatus, func(k int) ([]core.ScoredEmbedding, error) {
		inner := opts
		inner.TopK = k
		return db.searchTextOnlyAll(ctx, query, inner)
	})
}

// searchTextOnlyAll is SearchTextOnly before the gate: the word indexes,
// plus the CJK bigrams when the query holds CJK text.
func (db *DB) searchTextOnlyAll(ctx context.Context, query string, opts TextSearchOptions) ([]core.ScoredEmbedding, error) {
	results, err := db.searchTextOnlyWords(ctx, query, opts)
	if err != nil || opts.skipCJKBigrams || !core.ContainsCJK(query) {
		return results, err
	}
	// A CJK sentence is one token to the word index; see lexical_cjk.go.
	cjk, cjkErr := db.searchTextCJK(ctx, query, opts)
	if cjkErr != nil {
		slog.Warn("cortexdb: cjk bigram search skipped", observability.LogArgs(ctx, "err", cjkErr)...)
		return results, nil
	}
	return mergeScoredEmbeddings(opts.TopK, results, cjk), nil
}

// searchTextOnlyWords is SearchTextOnly over the FTS indexes alone.
func (db *DB) searchTextOnlyWords(ctx context.Context, query string, opts TextSearchOptions) ([]core.ScoredEmbedding, error) {
	if len(opts.Keywords) > 0 || len(opts.AlternateQueries) > 0 {
		return db.searchTextOnlyExpanded(ctx, query, opts)
	}

	results, err := db.store.HybridSearch(ctx, nil, query, core.HybridSearchOptions{
		SearchOptions: core.SearchOptions{
			Collection: opts.Collection,
			TopK:       opts.TopK,
			Threshold:  opts.Threshold,
		},
	})
	if err == nil {
		return results, nil
	}
	return db.ftsSearch(ctx, query, opts)
}

// overfetchK returns how many candidates to pull when an Authorize gate is set,
// so enough rows survive filtering to still fill TopK.
func overfetchK(wantK int) int {
	k := wantK * 5
	if k < 50 {
		k = 50
	}
	return k
}

// maxAuthorizedFetch bounds how far recallAuthorized will widen its recall. A
// predicate selective enough to survive this is one the caller should express
// as a filter on the query, not as a post-recall gate.
const maxAuthorizedFetch = 4096

// recallAuthorized applies the authorization predicate, re-fetching with a wider
// recall until TopK candidates pass or the corpus is exhausted.
//
// The widening is what makes the documented contract true: callers are promised
// up to TopK *authorized* results, and a fixed over-fetch silently breaks that
// promise exactly when the predicate matters most. Each round doubles, so a
// generous predicate costs one pass (the common case) and a selective one costs
// a handful.
func (db *DB) recallAuthorized(
	ctx context.Context,
	query string,
	candidates []core.ScoredEmbedding,
	fetchK, wantK int,
	authorize func(core.ScoredEmbedding) bool,
) ([]core.ScoredEmbedding, error) {
	filter := func(in []core.ScoredEmbedding) []core.ScoredEmbedding {
		kept := make([]core.ScoredEmbedding, 0, len(in))
		for _, c := range in {
			if authorize(c) {
				kept = append(kept, c)
			}
		}
		return kept
	}

	fetched := len(candidates)
	kept := filter(candidates)
	for len(kept) < wantK && fetchK < maxAuthorizedFetch {
		// Deliberately not stopping because a round returned fewer rows than it
		// asked for: hybrid recall fuses two result sets and dedupes, so it is
		// routinely short of fetchK while a wider fetch would still reach more.
		// Only a round that adds nothing new (below) means the corpus is spent.
		fetchK *= 2
		if fetchK > maxAuthorizedFetch {
			fetchK = maxAuthorizedFetch
		}
		wider, err := db.HybridSearchText(ctx, query, fetchK)
		if err != nil {
			return nil, err
		}
		if len(wider) <= fetched {
			// No new rows: the corpus, not the fetch size, is the limit.
			kept = filter(wider)
			break
		}
		fetched = len(wider)
		kept = filter(wider)
	}
	return kept, nil
}

// searchTextOnlyExpanded is SearchTextOnly with keywords or alternate
// queries: every phrasing runs, and their lists are fused by rank.
func (db *DB) searchTextOnlyExpanded(ctx context.Context, query string, opts TextSearchOptions) ([]core.ScoredEmbedding, error) {
	phrasings := fusedPhrasings(query, opts.AlternateQueries, lexicalSearchQueries(query, opts.Keywords, opts.AlternateQueries, db.chunkVariantWords(ctx, opts.Collection, opts.AlternateQueries).kept))
	if len(phrasings) == 0 {
		return nil, ErrEmptyText
	}

	var lists []lexicalList
	var firstErr error
	for _, phrasing := range phrasings {
		results, err := db.ftsSearch(ctx, phrasing.query, TextSearchOptions{
			Collection: opts.Collection,
			TopK:       opts.TopK * 4,
			Threshold:  opts.Threshold,
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		lists = append(lists, lexicalList{rows: results, own: phrasing.own})
	}

	ordered := fuseLexicalLists(opts.TopK, lists...)
	if len(ordered) == 0 {
		return nil, firstErr
	}
	return ordered, nil
}

// lexicalPhrasing is one full-text query of a search whose lists
// fuseLexicalLists merges. own marks the question's own phrasings: the
// question as typed, and its words OR-ed when that query runs.
type lexicalPhrasing struct {
	query string
	own   bool
}

// fusedPhrasings are the queries of a search whose lists fuseLexicalLists
// merges, from lexicalSearchQueries(question, …, alternateQueries, …).
//
// A phrasing that holds nothing the question's own lists do not is left out:
// fused beside them, a row that holds words of the question would be counted
// twice for them, above a row found through a keyword or a variant alone.
//
//   - The question's own OR query, when a keyword OR query holds its words
//     and more: every row it finds, that one finds too.
//   - The prefix form of the keyword OR query ("conseil* OR cantons*"): it
//     finds the rows the plain form finds, in nearly the same order. (On the
//     tool path it finds nothing besides: SearchTextOnly reads "*" as
//     punctuation and the OR as a word.)
//   - An alternate query whose every word is in the question ("网站 备案号"
//     for "网站的工信部备案号…"): the question's OR query and its CJK bigram
//     list search for those words already.
func fusedPhrasings(question string, alternateQueries, queries []string) []lexicalPhrasing {
	trimmed := strings.TrimSpace(question)
	phrase := sanitizeFTSQuery(trimmed)
	own := strings.Join(lexicalQueryKeywords(trimmed), " OR ")
	redundant := map[string]bool{}
	for _, alternate := range alternateQueries {
		if q := sanitizeFTSQuery(strings.TrimSpace(alternate)); q != phrase && wordsOfQuestion(trimmed, alternate) {
			redundant[q] = true
		}
	}
	wider := false
	for _, q := range queries {
		if q != own && strings.Contains(q, " OR ") && !strings.Contains(q, "*") {
			wider = true
		}
	}
	kept := make([]lexicalPhrasing, 0, len(queries))
	for _, q := range queries {
		prefixOR := strings.Contains(q, " OR ") && strings.Contains(q, "*")
		if prefixOR || redundant[q] || (wider && q == own) {
			continue
		}
		kept = append(kept, lexicalPhrasing{query: q, own: q == phrase || q == own})
	}
	return kept
}

// wordsOfQuestion reports whether every word of text is a word of question,
// compared without accents; a word in CJK script, which the question runs
// into its neighbours, is one when the question contains it.
func wordsOfQuestion(question, text string) bool {
	lower := strings.ToLower(question)
	words := map[string]bool{}
	for _, w := range lexicalQueryKeywords(question) {
		words[foldAccents(w)] = true
	}
	for _, w := range lexicalQueryKeywords(text) {
		if !words[foldAccents(w)] && !(core.ContainsCJK(w) && strings.Contains(lower, w)) {
			return false
		}
	}
	return true
}

// lexicalFusionK is the constant of the reciprocal rank fusion of a search's
// phrasings: the usual 60, which SearchTextOnly's own ranking uses too (1/61
// for a first row).
const lexicalFusionK = 60

// The weight of a list in the fusion. The question's own lists (the question
// as typed, its own OR query, its CJK bigrams) count in full; a list of an
// alternate query or of the keyword OR query counts half. They are synonyms
// of the question, not more of it, as the coverage floor credits them (see
// rarityExtraWeight): with equal weights a row that the question's bigram
// list put first lost to one that two variants found low, as a variant's word
// is in many rows the question is not about.
const (
	lexicalOwnListWeight     = 1.0
	lexicalVariantListWeight = 0.5
)

// lexicalList is the result list of one phrasing, and whether it is one of
// the question's own (see lexicalPhrasing). share, when above 0, is the part
// of the question an own list searches for (see cjkShareOfQuestion), and
// scales its weight.
type lexicalList struct {
	rows  []core.ScoredEmbedding
	own   bool
	share float64
}

// cjkShareOfQuestion is the part of question its CJK text is, in the units
// the coverage floor weighs it in (see rarityCJKCharWeight): each content
// character half a word, each other word one. The CJK bigram list searches
// for that part only; of "harness 那个 Rust 仓库发新版本到 crates.io 要怎么操作"
// it is half, and its bigrams of function words put rows that hold none of
// harness, Rust or crates.io first.
func cjkShareOfQuestion(question string) float64 {
	cjk := float64(cjkQueryTerms(question).chars) * rarityCJKCharWeight
	words := float64(len(rarityWords(question, math.MaxInt)))
	if cjk+words == 0 {
		return 0
	}
	return cjk / (cjk + words)
}

// fuseLexicalLists merges the result lists of a search's phrasings (the
// question, each alternate query, the keyword OR query, the CJK bigrams) by
// weighted reciprocal rank: a row scores the sum of weight/(lexicalFusionK +
// rank) over the lists that hold it, and the topK best are returned (all when
// topK is 0).
//
// Each query's bm25 is measured against its own terms, so the scores of two
// queries do not compare: a short row that holds two words of the OR query
// outscored rows that hold every word of the question. Ranks compare. A row
// that several phrasings find ranks above one that a single phrasing finds,
// so a row that only a variant finds comes after the rows the question finds
// whole.
//
// Rows that score the same are ordered by their best rank in the question's
// own lists, a row none of them holds after those, and then by id.
func fuseLexicalLists(topK int, lists ...lexicalList) []core.ScoredEmbedding {
	type fusedRow struct {
		row     core.ScoredEmbedding
		score   float64
		ownRank int // best 1-based rank in an own list; 0 when none holds it
	}
	fused := make(map[string]*fusedRow)
	for _, list := range lists {
		weight := lexicalVariantListWeight
		if list.own {
			weight = lexicalOwnListWeight
			if list.share > 0 {
				weight *= list.share
			}
		}
		for rank, result := range list.rows {
			f, ok := fused[result.ID]
			if !ok {
				f = &fusedRow{row: result}
				fused[result.ID] = f
			}
			f.score += weight / float64(lexicalFusionK+rank+1)
			if list.own && (f.ownRank == 0 || rank+1 < f.ownRank) {
				f.ownRank = rank + 1
			}
		}
	}
	rows := make([]*fusedRow, 0, len(fused))
	for _, f := range fused {
		rows = append(rows, f)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.ownRank != b.ownRank {
			return b.ownRank == 0 || (a.ownRank != 0 && a.ownRank < b.ownRank)
		}
		return a.row.ID < b.row.ID
	})
	if topK > 0 && len(rows) > topK {
		rows = rows[:topK]
	}
	out := make([]core.ScoredEmbedding, len(rows))
	for i, f := range rows {
		out[i] = f.row
		out[i].Score = f.score
	}
	return out
}

// ftsSearchQuery builds the lexical lookup for one query, returning the same columns either way.
//
// Normally that is a MATCH against the FTS index. A CJK query of one or two characters produces no
// trigram, so MATCH would return nothing however much of the corpus contains the term — those fall
// back to a substring scan, ordered by rowid since there is no BM25 score to rank by.
func ftsSearchQuery(d sqldialect.Dialect, query string, opts TextSearchOptions) (string, []any) {
	columns := `e.id, e.collection_id, c.name, e.vector, e.content, e.doc_id, e.metadata, e.acl`
	collectionClause := ""
	var collectionArgs []any
	if opts.Collection != "" {
		collectionClause = " AND c.name = ?"
		collectionArgs = append(collectionArgs, opts.Collection)
	}

	// PostgreSQL has no FTS5 and no rowid, so the whole shape differs — not
	// just the spelling. The routing does not: PostgresLexicalCondition calls
	// the same ContainsCJK and BelowTrigramFloor this function does, so a
	// query goes down the same arm on both backends and only the index behind
	// it changes.
	if d != nil && d.Kind() == sqldialect.Postgres {
		cond, lexArgs, _ := core.PostgresLexicalCondition("e.content", query)
		rank, rankArgs := core.PostgresLexicalRank("e.content", query)

		args := append([]any{}, rankArgs...)
		args = append(args, lexArgs...)
		args = append(args, collectionArgs...)
		return `
			SELECT ` + columns + `, ` + rank + ` as score
			FROM embeddings e
			LEFT JOIN collections c ON e.collection_id = c.id
			WHERE ` + cond + collectionClause + `
			ORDER BY score, e.id LIMIT ?
		`, append(args, opts.TopK)
	}

	if core.BelowTrigramFloor(query) {
		args := append([]any{core.SubstringPattern(query)}, collectionArgs...)
		return `
			SELECT ` + columns + `, 0 as score
			FROM embeddings e
			LEFT JOIN collections c ON e.collection_id = c.id
			WHERE e.content LIKE ? ESCAPE '\'` + collectionClause + `
			ORDER BY e.rowid LIMIT ?
		`, append(args, opts.TopK)
	}

	// CJK text needs the trigram companion index; see core.CJKAwareIndex.
	index := core.CJKAwareIndex("chunks_fts", query)
	args := append([]any{query}, collectionArgs...)
	return `
		SELECT ` + columns + `, bm25(` + index + `) as score
		FROM ` + index + `
		JOIN embeddings e ON ` + index + `.rowid = e.rowid
		LEFT JOIN collections c ON e.collection_id = c.id
		WHERE ` + index + ` MATCH ?` + collectionClause + `
		ORDER BY score LIMIT ?
	`, append(args, opts.TopK)
}

func (db *DB) ftsSearch(ctx context.Context, query string, opts TextSearchOptions) ([]core.ScoredEmbedding, error) {
	defer observability.Time(ctx, "lexical")()
	querySQL, args := ftsSearchQuery(db.Dialect(), query, opts)
	rows, err := db.query(ctx, querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("FTS search failed: %w", err)
	}
	return scanFTSRows(rows, opts.Threshold)
}

// scanFTSRows reads rows shaped like ftsSearchQuery's result — the chunk columns
// then a raw lexical rank — into scored embeddings, and closes them. Shared with
// the CJK bigram path so both return rows authorization and filtering treat alike.
func scanFTSRows(rows *sql.Rows, threshold float64) ([]core.ScoredEmbedding, error) {
	defer func() { _ = rows.Close() }()

	var results []core.ScoredEmbedding
	for rows.Next() {
		var id, content string
		var collectionID int
		var collectionName, docID sql.NullString
		var vectorBytes []byte
		var metadataJSON string
		var aclJSON sql.NullString
		var score float64

		if err := rows.Scan(&id, &collectionID, &collectionName, &vectorBytes, &content, &docID, &metadataJSON, &aclJSON, &score); err != nil {
			// Dropped rows are how a search that matched plenty answers "nothing found": one column that
			// will not scan — a NULL metadata, a NULL collection_id — silently costs every row it affects.
			log.Printf("[FTS] skipped a chunk that would not scan: %v", err)
			continue
		}
		// bm25() is negative and lower for a better match; both backends'
		// lexical rank keep that convention. 1/(1+(-bm25)) was highest for the
		// weakest match, so the SQL ORDER BY returned rows best first and every
		// caller that sorted by Score put them worst first. Same fix as the
		// memory path: relevance rises as the raw rank falls.
		relevance := -score
		if relevance < 0 {
			relevance = 0
		}
		normalizedScore := relevance / (1 + relevance)
		if threshold > 0 && normalizedScore < threshold {
			continue
		}
		metadata, _ := encoding.DecodeMetadata(metadataJSON)
		var acl []string
		if aclJSON.Valid && aclJSON.String != "" {
			_ = json.Unmarshal([]byte(aclJSON.String), &acl)
		}
		results = append(results, core.ScoredEmbedding{
			Embedding: core.Embedding{
				ID:         id,
				Collection: collectionName.String,
				Content:    content,
				DocID:      docID.String,
				Metadata:   metadata,
				ACL:        acl,
			},
			Score: normalizedScore,
		})
	}

	return results, rows.Err()
}
