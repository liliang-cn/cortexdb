package cortexdb

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// The keyword floor's document frequencies, counted through the trigram index.
//
// scanCorpusStats reads every chunk of the searched collection with one
// LIKE per term, at every search: on a collection of 270,000 chunks that is
// most of a knowledge_search. chunks_fts_cjk, the trigram index CortexDB keeps
// over the same rows, answers the same LIKE from its index — the same rows,
// for accents and case too — when the pattern has three characters or more
// and no ESCAPE clause (with one, SQLite reads every row of the index). So a
// question whose terms all qualify is counted there, one indexed count per
// term; any other, or a store whose index does not hold every chunk, is
// scanned as before.

// trigramCountMax bounds the terms counted one by one: each count reads the
// rows its term matches, so past a handful of terms the single scan, which
// counts them all at once, costs less.
const trigramCountMax = 6

// A term held by more than one chunk in trigramCommonShare (and by more than
// trigramCommonFloor) is counted by the scan with the rest of the question:
// an indexed count reads every row its term matches, and past a few percent
// of the store that costs more than the scan (on 270,000 chunks: 0.16 s for a
// term in 5,900 of them, 0.28 s for one in 9,500, 1 s for one in 17,000,
// against about 0.3 s for the scan).
const (
	trigramCommonShare = 40
	trigramCommonFloor = 1000
)

// anyTrigramPatternCommon reports whether a pattern matches limit rows of the
// trigram index or more. Each probe stops at limit, so a common term costs
// no more than limit rows of the index.
func (db *DB) anyTrigramPatternCommon(ctx context.Context, patterns []string, limit int64) (bool, error) {
	columns := make([]string, 0, len(patterns))
	args := make([]any, 0, 2*len(patterns))
	for _, p := range patterns {
		columns = append(columns, `(SELECT count(*) FROM (SELECT rowid FROM chunks_fts_cjk WHERE content LIKE ? LIMIT ?))`)
		args = append(args, "%"+p+"%", limit)
	}
	counts := make([]int64, len(columns))
	dest := make([]any, len(counts))
	for i := range counts {
		dest[i] = &counts[i]
	}
	if err := db.queryRow(ctx, "SELECT "+strings.Join(columns, ", "), args...).Scan(dest...); err != nil {
		return false, fmt.Errorf("probe term frequencies: %w", err)
	}
	for _, c := range counts {
		if c >= limit {
			return true, nil
		}
	}
	return false, nil
}

// trigramCountInCollection counts the chunks of a collection holding a
// pattern. The unary plus keeps the planner on the rows the index names: on
// the collection index, it would read every chunk of the collection.
const trigramCountInCollection = `SELECT COUNT(*) FROM embeddings e JOIN collections c ON c.id = +e.collection_id WHERE c.name = ? AND e.rowid IN (SELECT rowid FROM chunks_fts_cjk WHERE content LIKE ?)`

// trigramCountable reports whether every pattern can be counted by the
// trigram index with the result the scan would give.
func trigramCountable(patterns []string) bool {
	if len(patterns) == 0 || len(patterns) > trigramCountMax {
		return false
	}
	for _, p := range patterns {
		if utf8.RuneCountInString(p) < 3 || strings.ContainsAny(p, `%_\`) {
			return false
		}
	}
	return true
}

// chunkStatsByTrigrams counts the chunks of collection (every chunk when it
// is empty) and those holding each pattern, through the trigram index. It
// reports false, and counts nothing, when the scan has to count instead: on
// PostgreSQL, for patterns the index cannot count, and when the index does
// not hold every chunk — a store without it, or one whose index was emptied.
func (db *DB) chunkStatsByTrigrams(ctx context.Context, collection string, patterns []string) (corpusStats, bool, error) {
	if d := db.Dialect(); (d != nil && d.Kind() == sqldialect.Postgres) || !trigramCountable(patterns) {
		return corpusStats{}, false, nil
	}
	var held, rows int64
	if err := db.queryRow(ctx, `SELECT (SELECT count(*) FROM chunks_fts_cjk_docsize), (SELECT count(*) FROM embeddings)`).Scan(&held, &rows); err != nil || held != rows {
		return corpusStats{}, false, nil
	}
	common, err := db.anyTrigramPatternCommon(ctx, patterns, max(rows/trigramCommonShare, trigramCommonFloor))
	if err != nil || common {
		return corpusStats{}, false, err
	}
	defer observability.Time(ctx, "df-trigram")()

	// One statement: the chunks of the collection, then those holding each
	// pattern, each read from the trigram index.
	total := `SELECT COUNT(*) FROM embeddings e`
	matching := `SELECT COUNT(*) FROM embeddings e WHERE e.rowid IN (SELECT rowid FROM chunks_fts_cjk WHERE content LIKE ?)`
	var scope []any
	if collection != "" {
		total += ` JOIN collections c ON c.id = e.collection_id WHERE c.name = ?`
		matching = trigramCountInCollection
		scope = []any{collection}
	}
	columns := []string{"(" + total + ")"}
	args := append([]any(nil), scope...)
	for _, p := range patterns {
		columns = append(columns, "("+matching+")")
		args = append(append(args, scope...), "%"+p+"%")
	}
	counts := make([]int64, len(columns))
	dest := make([]any, len(counts))
	for i := range counts {
		dest[i] = &counts[i]
	}
	if err := db.queryRow(ctx, "SELECT "+strings.Join(columns, ", "), args...).Scan(dest...); err != nil {
		return corpusStats{}, false, fmt.Errorf("count term frequencies: %w", err)
	}
	stats := corpusStats{n: int(counts[0]), df: make(map[string]int, len(patterns))}
	for i, p := range patterns {
		stats.df[p] = int(counts[i+1])
	}
	return stats, true, nil
}
