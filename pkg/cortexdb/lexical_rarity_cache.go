package cortexdb

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// The keyword floor's document frequencies, kept until the chunks change.
//
// A question counts each of its terms over the searched collection. The
// trigram index (lexical_rarity_trigram.go) counts a rare term cheaply, but a
// term of one or two characters ("AI") or one held by many chunks ("act",
// "data") falls back to the scan of the whole collection, and a question
// nearly always has one: on a collection of 250,000 chunks, 0.9 s of a 1.1 s
// knowledge_search. The same terms come back question after question, over
// chunks that have not changed, so their counts are kept per collection and
// served until a write reaches the chunks.
//
// Staleness is read from the file, as the PageRank cache reads it
// (graph_pagerank_cache.go): the highest rowid of embeddings, which every
// insert raises, and a counter that triggers bump on a delete and on an
// update of a chunk's content or collection. The triggers are part of the
// file, so a write by another process or by an older binary is seen too. A
// count is exact: the cache only saves recounting what a fresh count would
// give. On PostgreSQL there are no triggers, and every question counts.

const (
	chunkStatsVersionTable = "embeddings_content_version"
	// chunkStatsCacheTerms bounds the terms kept per collection; past it the
	// collection's counts start over.
	chunkStatsCacheTerms = 4096
)

var chunkStatsSchema = []string{
	`CREATE TABLE IF NOT EXISTS ` + chunkStatsVersionTable + ` (id INTEGER PRIMARY KEY, version INTEGER NOT NULL)`,
	`INSERT OR IGNORE INTO ` + chunkStatsVersionTable + ` (id, version) VALUES (1, 0)`,
	`CREATE TRIGGER IF NOT EXISTS embeddings_content_version_ad AFTER DELETE ON embeddings BEGIN
		UPDATE ` + chunkStatsVersionTable + ` SET version = version + 1 WHERE id = 1;
	END`,
	`CREATE TRIGGER IF NOT EXISTS embeddings_content_version_au AFTER UPDATE OF content, collection_id ON embeddings BEGIN
		UPDATE ` + chunkStatsVersionTable + ` SET version = version + 1 WHERE id = 1;
	END`,
}

// chunkStatsCache is the counts of each collection, by collection id (0 for
// every collection), with the state of the chunks they were counted on.
type chunkStatsCache struct {
	mu    sync.Mutex
	ready bool
	sets  map[int64]*cachedChunkStats
}

type cachedChunkStats struct {
	version, maxRowID int64
	n                 int
	df                map[string]int
}

// chunkStatsCacheReady installs the version counter once per DB, latching on
// success only. It reports false when the store cannot hold it: PostgreSQL,
// or a file this process may not write.
func (db *DB) chunkStatsCacheReady(ctx context.Context) bool {
	c := &db.chunkStats
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := db.Dialect(); !c.ready && (d == nil || d.Kind() != sqldialect.Postgres) {
		c.ready = db.inTx(ctx, func(tx *sql.Tx) error {
			for _, stmt := range chunkStatsSchema {
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		}) == nil
		c.sets = map[int64]*cachedChunkStats{}
	}
	return c.ready
}

// cachedChunkStatsFor serves the counts of patterns over collection (every
// collection when it is empty) from the cache, and counts the patterns it
// lacks with count.
func (db *DB) cachedChunkStatsFor(ctx context.Context, collection string, patterns []string, count func(missing []string) (corpusStats, error)) (corpusStats, error) {
	if !db.chunkStatsCacheReady(ctx) {
		return count(patterns)
	}
	var version, maxRowID, collectionID int64
	var found bool
	if collection == "" {
		found = true
		if err := db.queryRow(ctx, `SELECT (SELECT version FROM `+chunkStatsVersionTable+` WHERE id = 1), (SELECT COALESCE(MAX(rowid), 0) FROM embeddings)`).Scan(&version, &maxRowID); err != nil {
			return corpusStats{}, fmt.Errorf("chunk counts: read version: %w", err)
		}
	} else {
		var id sql.NullInt64
		if err := db.queryRow(ctx, `SELECT (SELECT version FROM `+chunkStatsVersionTable+` WHERE id = 1), (SELECT COALESCE(MAX(rowid), 0) FROM embeddings), (SELECT id FROM collections WHERE name = ?)`, collection).Scan(&version, &maxRowID, &id); err != nil {
			return corpusStats{}, fmt.Errorf("chunk counts: read version: %w", err)
		}
		found, collectionID = id.Valid, id.Int64
	}
	if !found {
		return count(patterns)
	}

	c := &db.chunkStats
	c.mu.Lock()
	set := c.sets[collectionID]
	if set == nil || set.version != version || set.maxRowID != maxRowID || len(set.df) > chunkStatsCacheTerms {
		set = &cachedChunkStats{version: version, maxRowID: maxRowID, df: map[string]int{}}
		c.sets[collectionID] = set
	}
	var missing []string
	for _, p := range patterns {
		if _, ok := set.df[p]; !ok {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		stats := corpusStats{n: set.n, df: make(map[string]int, len(patterns))}
		for _, p := range patterns {
			stats.df[p] = set.df[p]
		}
		c.mu.Unlock()
		defer observability.Time(ctx, "df-cache")()
		return stats, nil
	}
	c.mu.Unlock()

	counted, err := count(missing)
	if err != nil {
		return corpusStats{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// Kept only if the chunks are still those the version named: a write
	// since then has made it stale, and the next question will see that.
	if c.sets[collectionID] == set {
		set.n = counted.n
		for _, p := range missing {
			set.df[p] = counted.df[p]
		}
	}
	stats := corpusStats{n: counted.n, df: make(map[string]int, len(patterns))}
	for _, p := range patterns {
		if v, ok := counted.df[p]; ok {
			stats.df[p] = v
		} else {
			stats.df[p] = set.df[p]
		}
	}
	return stats, nil
}
