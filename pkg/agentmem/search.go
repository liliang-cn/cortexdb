package agentmem

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

// SearchOptions controls SearchByText/SearchByScope behavior.
type SearchOptions struct {
	TopK            int
	MinScore        float64
	IncludeArchived bool
	IncludeStale    bool     // include rows whose ValidTo is set or SupersededBy non-empty
	Type            Type     // optional type filter
	BankIDs         []string // pre-resolved bank ids (used by SearchByScope)
}

// SearchByText runs an FTS5 query, then re-ranks with importance + time decay.
func (s *Store) SearchByText(ctx context.Context, query string, opts SearchOptions) ([]ScoredMemory, error) {
	return s.searchInternal(ctx, query, opts)
}

// SearchByScope is SearchByText restricted to a list of scopes.
func (s *Store) SearchByScope(ctx context.Context, query string, scopes []Scope, opts SearchOptions) ([]ScoredMemory, error) {
	if len(scopes) == 0 {
		return s.searchInternal(ctx, query, opts)
	}
	bankIDs := make([]string, 0, len(scopes))
	seen := map[string]struct{}{}
	for _, sc := range scopes {
		id := BankID(sc)
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		bankIDs = append(bankIDs, id)
	}
	opts.BankIDs = bankIDs
	return s.searchInternal(ctx, query, opts)
}

// SearchByType returns top-N memories of a given type, ranked by importance +
// recency (no FTS query). Useful when callers want the latest preferences /
// observations.
func (s *Store) SearchByType(ctx context.Context, t Type, limit int) ([]*Memory, error) {
	return s.GetByType(ctx, t, limit)
}

func (s *Store) searchInternal(ctx context.Context, query string, opts SearchOptions) ([]ScoredMemory, error) {
	topK := opts.TopK
	if topK <= 0 {
		topK = 10
	}

	tokens := tokenizeForFTS(query)
	if len(tokens) == 0 {
		return nil, nil
	}
	q, args := s.buildSearchQuery(tokens, opts, topK*4)

	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("agentmem: fts query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type hit struct {
		id    string
		bm25  float64
		score float64
	}
	var hits []hit
	now := time.Now().UTC()
	for rows.Next() {
		var (
			id         string
			rank       float64
			importance float64
			createdAt  time.Time
		)
		if err := rows.Scan(&id, &rank, &importance, &createdAt); err != nil {
			return nil, err
		}
		// bm25() is negative and a better match is more negative, so relevance
		// has to grow as the rank falls. 1/(1+|rank|) did the opposite and the
		// descending sort below then put the weakest match on top.
		relevance := -rank
		if relevance < 0 {
			relevance = 0
		}
		text := relevance / (1 + relevance)
		score := applyBoosts(text, importance, createdAt, now)
		hits = append(hits, hit{id: id, bm25: rank, score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].score > hits[j].score })

	out := make([]ScoredMemory, 0, len(hits))
	for _, h := range hits {
		if h.score < opts.MinScore {
			continue
		}
		m, err := s.Get(ctx, h.id)
		if err != nil {
			if err == ErrNotFound {
				continue
			}
			return nil, err
		}
		out = append(out, ScoredMemory{Memory: m, Score: h.score})
		if len(out) >= topK {
			break
		}
	}
	return out, nil
}

// applyBoosts mirrors agent-go's applyMemoryBoosts: importance maps [0,1]→
// [0.5,1.0], time decay has ≈99-day half-life.
func applyBoosts(textScore, importance float64, createdAt, now time.Time) float64 {
	if importance <= 0 {
		importance = 0.5
	}
	importanceBoost := 0.5 + importance*0.5
	days := now.Sub(createdAt).Hours() / 24
	if days < 0 {
		days = 0
	}
	decay := math.Exp(-0.007 * days)
	return textScore * importanceBoost * decay
}

// tokenizeForFTS splits a query into FTS-safe tokens. ASCII words are kept
// whole; CJK runs are emitted both as the full run and as 2-character sliding
// windows so that the trigram tokenizer (which works on any substring) and the
// unicode61 fallback (which indexes whole "words") both have something to
// match.
func tokenizeForFTS(query string) []string {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	add := func(t string) {
		t = strings.TrimSpace(t)
		if t == "" {
			return
		}
		if _, dup := seen[t]; dup {
			return
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}

	runes := []rune(query)
	var word strings.Builder
	flushWord := func() {
		if word.Len() > 0 {
			for _, w := range strings.Fields(word.String()) {
				add(strings.ToLower(w))
			}
			word.Reset()
		}
	}
	var cjkRun []rune
	flushCJK := func() {
		if len(cjkRun) > 0 {
			add(string(cjkRun))
			for i := 0; i+1 < len(cjkRun); i++ {
				add(string(cjkRun[i : i+2]))
			}
			cjkRun = cjkRun[:0]
		}
	}

	for _, r := range runes {
		switch {
		case isCJK(r):
			flushWord()
			cjkRun = append(cjkRun, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushCJK()
			word.WriteRune(r)
		default:
			flushWord()
			flushCJK()
			if !unicode.IsSpace(r) {
				// drop punctuation; they confuse FTS5 grammar
			}
		}
	}
	flushWord()
	flushCJK()
	return out
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x20000 && r <= 0x2A6DF) ||
		(r >= 0x3040 && r <= 0x30FF) || // Hiragana + Katakana
		(r >= 0xAC00 && r <= 0xD7AF) // Hangul syllables
}

// buildFTSMatch produces an FTS5 MATCH expression: each token quoted, joined
// with OR.  Quoting protects against tokens that include punctuation residues
// or that look like FTS5 reserved words.
func buildFTSMatch(tokens []string) string {
	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		parts = append(parts, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
	}
	return strings.Join(parts, " OR ")
}

// (sql.ErrNoRows alias to keep imports tidy)
var _ = sql.ErrNoRows

// buildSearchQuery is where the two backends genuinely part company.
//
// Everything else in agentmem is one body of SQL with the placeholders
// rebound. Text search is not: FTS5 is a SQLite feature and there is no
// PostgreSQL spelling of `MATCH` or `bm25()`. What is shared is the shape —
// the same tokens, the same haystack, the same filters, and a rank column
// where lower is better on both — so the caller and the re-ranking below never
// learn which one ran.
func (s *Store) buildSearchQuery(tokens []string, opts SearchOptions, limitN int) (string, []any) {
	if s.isPostgres() {
		return s.buildPostgresSearch(tokens, opts, limitN)
	}
	return buildSQLiteSearch(tokens, opts, limitN)
}

// searchFilters are the conditions both backends share, in the order their
// arguments must be bound.
func searchFilters(opts SearchOptions) ([]string, []any) {
	var conds []string
	var args []any
	if !opts.IncludeArchived {
		conds = append(conds, "m.archived = 0")
	}
	if !opts.IncludeStale {
		conds = append(conds, "(m.valid_to IS NULL AND (m.superseded_by IS NULL OR m.superseded_by = ''))")
	}
	if opts.Type != "" {
		conds = append(conds, "m.type = ?")
		args = append(args, string(opts.Type))
	}
	if len(opts.BankIDs) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(opts.BankIDs)), ",")
		conds = append(conds, "m.bank_id IN ("+placeholders+")")
		for _, b := range opts.BankIDs {
			args = append(args, b)
		}
	}
	return conds, args
}

// belowFloor reports that no token is long enough for the trigram tokenizer to
// index, so MATCH would return nothing whatever the corpus holds.
func belowFloor(tokens []string) bool {
	for _, t := range tokens {
		if !core.BelowTrigramFloor(t) {
			return false
		}
	}
	return len(tokens) > 0
}

// buildSQLiteSubstringSearch is the unindexed fallback for a query the trigram
// index cannot serve. Slower than an index and infinitely faster than the zero
// rows MATCH would return — the same trade pkg/core makes.
//
// Rank is the negated count of matched columns so that lower stays better,
// matching bm25's direction; there is no bm25 to call when MATCH is not used.
func buildSQLiteSubstringSearch(tokens []string, opts SearchOptions, limitN int) (string, []any) {
	var (
		args  []any
		likes []string
		rank  []string
	)
	for _, t := range tokens {
		pattern := core.SubstringPattern(strings.ToLower(t))
		for _, col := range []string{"f.content", "f.tags", "f.keywords"} {
			likes = append(likes, "lower("+col+") LIKE ? ESCAPE '\\'")
			rank = append(rank, "(CASE WHEN lower("+col+") LIKE ? ESCAPE '\\' THEN 1 ELSE 0 END)")
			args = append(args, pattern)
		}
	}
	// The rank arm repeats the same patterns, so its arguments follow the
	// WHERE arm's in the same order.
	rankArgs := append([]any(nil), args...)

	conds := []string{"m.id = f.memory_id", "(" + strings.Join(likes, " OR ") + ")"}
	rest, restArgs := searchFilters(opts)
	conds = append(conds, rest...)

	all := append(append([]any(nil), rankArgs...), args...)
	all = append(all, restArgs...)
	all = append(all, limitN)

	return `SELECT m.id, -(` + strings.Join(rank, " + ") + `) AS rank,
	             m.importance, m.created_at
	      FROM agentmem_fts AS f
	      JOIN agentmem_memories AS m ON ` + strings.Join(conds, " AND ") + `
	      ORDER BY rank
	      LIMIT ?`, all
}

func buildSQLiteSearch(tokens []string, opts SearchOptions, limitN int) (string, []any) {
	// Below the trigram floor, MATCH cannot answer and must be routed past.
	//
	// The trigram tokenizer makes no token out of a run shorter than three
	// characters, and a great many Chinese words are exactly two: 乘法, 分数,
	// 面积, 周长. MATCH returns nothing for all of them, so this package has
	// been answering "no results" for terms its corpus is full of — silently,
	// because zero rows and no matches look identical. pkg/core solved this
	// for its own search long ago (BelowTrigramFloor in fts_cjk.go); agentmem
	// never adopted it. Found by running the same assertions on PostgreSQL,
	// where substring matching finds those words and the two backends
	// therefore disagreed.
	if belowFloor(tokens) {
		return buildSQLiteSubstringSearch(tokens, opts, limitN)
	}

	args := []any{buildFTSMatch(tokens)}
	conds := []string{"m.id = f.memory_id", "f.agentmem_fts MATCH ?"}

	rest, restArgs := searchFilters(opts)
	conds = append(conds, rest...)
	args = append(args, restArgs...)
	args = append(args, limitN)

	return `SELECT m.id, bm25(agentmem_fts) AS rank,
	             m.importance, m.created_at
	      FROM agentmem_fts AS f
	      JOIN agentmem_memories AS m ON ` + strings.Join(conds, " AND ") + `
	      ORDER BY rank
	      LIMIT ?`, args
}

// buildPostgresSearch answers the same question with substrings.
//
// The SQLite side already prefers the trigram tokenizer, which matches any
// substring rather than any word, so a case-insensitive LIKE over the same
// denormalised haystack is not an approximation of FTS5 here — it is the same
// rule, and pg_trgm indexes it the same way. That also keeps the CJK
// behaviour: tokenizeForFTS emits two-character windows precisely because
// neither backend can tokenise Chinese into words.
//
// bm25 has no counterpart, so rank is the number of the query's tokens the row
// contains, negated to keep "lower is better" true on both. Ties are broken by
// nothing here on purpose — the re-rank in searchInternal applies importance
// and time decay to every candidate before anything is dropped, and it pulls
// limitN = 4×topK rows so a tie at the cut cannot silently decide the answer.
func (s *Store) buildPostgresSearch(tokens []string, opts SearchOptions, limitN int) (string, []any) {
	const haystack = "(f.content || ' ' || f.tags || ' ' || f.keywords)"

	var rankArgs, matchArgs []any
	rankParts := make([]string, 0, len(tokens))
	matchParts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		pattern := "%" + escapeLike(t) + "%"
		rankParts = append(rankParts, "(CASE WHEN "+haystack+" ILIKE ? ESCAPE '\\' THEN 1 ELSE 0 END)")
		matchParts = append(matchParts, haystack+" ILIKE ? ESCAPE '\\'")
		rankArgs = append(rankArgs, pattern)
		matchArgs = append(matchArgs, pattern)
	}

	// CAST to double precision: the sum is an integer expression, and a driver
	// handing back an int64 for a column the scanner reads as float64 is a
	// failure that depends on the driver rather than on the query.
	rankExpr := "CAST(-(" + strings.Join(rankParts, " + ") + ") AS DOUBLE PRECISION)"

	conds := []string{"m.id = f.memory_id", "(" + strings.Join(matchParts, " OR ") + ")"}
	rest, restArgs := searchFilters(opts)
	conds = append(conds, rest...)

	args := make([]any, 0, len(rankArgs)+len(matchArgs)+len(restArgs)+1)
	args = append(args, rankArgs...)
	args = append(args, matchArgs...)
	args = append(args, restArgs...)
	args = append(args, limitN)

	return `SELECT m.id, ` + rankExpr + ` AS rank,
	             m.importance, m.created_at
	      FROM agentmem_fts AS f
	      JOIN agentmem_memories AS m ON ` + strings.Join(conds, " AND ") + `
	      ORDER BY rank
	      LIMIT ?`, args
}

// escapeLike neutralises the two LIKE wildcards and the escape character
// itself. Every token here is already stripped of punctuation, which is
// exactly the condition under which this is easy to leave out and impossible
// to notice until a caller searches for something with an underscore in it.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)
	return r.Replace(s)
}
