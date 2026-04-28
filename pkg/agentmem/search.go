package agentmem

import (
	"context"
	"database/sql"
	"fmt"
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
	IncludeStale    bool   // include rows whose ValidTo is set or SupersededBy non-empty
	Type            Type   // optional type filter
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
	matchExpr := buildFTSMatch(tokens)

	args := []any{matchExpr}
	conds := []string{"m.id = f.memory_id", "f.agentmem_fts MATCH ?"}
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
		placeholders := strings.Repeat("?,", len(opts.BankIDs))
		placeholders = strings.TrimRight(placeholders, ",")
		conds = append(conds, "m.bank_id IN ("+placeholders+")")
		for _, b := range opts.BankIDs {
			args = append(args, b)
		}
	}

	limitN := topK * 4
	args = append(args, limitN)

	q := `SELECT m.id, bm25(agentmem_fts) AS rank,
	             m.importance, m.created_at
	      FROM agentmem_fts AS f
	      JOIN agentmem_memories AS m ON ` + strings.Join(conds, " AND ") + `
	      ORDER BY rank
	      LIMIT ?`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("agentmem: fts query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type hit struct {
		id        string
		bm25      float64
		score     float64
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
		text := 1.0 / (1.0 + math.Abs(rank))
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
