package cortexdb

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// The name index of find_nodes.
//
// find_nodes compares names folded (foldName): a node matches when its folded
// content contains the folded name. Without an index each call read and
// folded every node of the store: 1.3 s for one name on a store of 411,000
// nodes, most of it in the fold, and 0.3 s with skip_chunks.
//
// graph_node_folds_fts is a trigram index over the folded content of each
// node. SQL cannot fold, so Go fills it; triggers on graph_nodes take a node
// out of it whenever the node is written and mark it pending in
// graph_node_folds, and the next find_nodes folds the pending nodes back in
// before it reads. A trigram phrase query finds every node whose folded
// content contains the folded name (the tokenizer folds case alike on both
// sides), so its rows are a superset of the scan's matches, ranked by the
// same code. A name folding to fewer than three characters has no trigram:
// a call holding one scans, as before. SQLite only; the index holds no text
// (contentless), only its trigrams.
const (
	nodeFoldsTable = "graph_node_folds"
	nodeFoldsFTS   = "graph_node_folds_fts"
	// nodeFoldsBatch is how many pending nodes one transaction folds, so a
	// store written in bulk does not hold the write lock for the whole fold.
	nodeFoldsBatch = 1000
)

// nodeFoldsSchema creates the index and the triggers that keep it, and marks
// pending every node written before it existed. It runs in one transaction,
// so no node is written between the backfill and the triggers.
var nodeFoldsSchema = []string{
	`CREATE TABLE IF NOT EXISTS ` + nodeFoldsTable + ` (
		rid INTEGER PRIMARY KEY,
		node_id TEXT NOT NULL UNIQUE,
		pending INTEGER NOT NULL DEFAULT 1,
		-- Set by the fold: the length of the folded content and of the
		-- content, in characters, which rank the matches without the text.
		folded_len INTEGER NOT NULL DEFAULT 0,
		size INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS idx_graph_node_folds_pending ON ` + nodeFoldsTable + `(rid) WHERE pending = 1`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS ` + nodeFoldsFTS + ` USING fts5(folded, content='', contentless_delete=1, tokenize='trigram')`,
	// A node written is marked pending, and the fold replaces its trigrams.
	// INSERT OR REPLACE on graph_nodes fires the insert trigger alone, which
	// is enough for that reason.
	`CREATE TRIGGER IF NOT EXISTS graph_node_folds_ai AFTER INSERT ON graph_nodes BEGIN
		INSERT INTO ` + nodeFoldsTable + `(node_id) VALUES (new.id) ON CONFLICT(node_id) DO UPDATE SET pending = 1;
	END`,
	`CREATE TRIGGER IF NOT EXISTS graph_node_folds_au AFTER UPDATE OF id, content ON graph_nodes BEGIN
		UPDATE ` + nodeFoldsTable + ` SET node_id = new.id, pending = 1 WHERE node_id = old.id;
	END`,
	// A node deleted takes its trigrams with it: nothing would fold them out.
	`CREATE TRIGGER IF NOT EXISTS graph_node_folds_ad AFTER DELETE ON graph_nodes BEGIN
		DELETE FROM ` + nodeFoldsFTS + ` WHERE rowid = (SELECT rid FROM ` + nodeFoldsTable + ` WHERE node_id = old.id);
		DELETE FROM ` + nodeFoldsTable + ` WHERE node_id = old.id;
	END`,
	`INSERT OR IGNORE INTO ` + nodeFoldsTable + `(node_id) SELECT id FROM graph_nodes`,
}

// nodeFoldIndexReady creates the name index on first use and folds into it
// every node written since the last call.
func (db *DB) nodeFoldIndexReady(ctx context.Context) error {
	db.nodeFoldMu.Lock()
	defer db.nodeFoldMu.Unlock()
	if !db.nodeFoldReady {
		if err := db.inTx(ctx, func(tx *sql.Tx) error {
			for _, stmt := range nodeFoldsSchema {
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return fmt.Errorf("find_nodes name index: %w", err)
		}
		db.nodeFoldReady = true
	}
	for {
		folded, err := db.foldPendingNodes(ctx)
		if err != nil {
			return fmt.Errorf("find_nodes name index: fold written nodes: %w", err)
		}
		if folded < nodeFoldsBatch {
			return nil
		}
	}
}

// foldPendingNodes folds up to nodeFoldsBatch pending nodes into the index
// and reports how many it took. An insert under a rowid the index holds
// replaces its trigrams (contentless_delete), so a node written again, or
// folded first by another process, is folded right.
func (db *DB) foldPendingNodes(ctx context.Context) (int, error) {
	n := 0
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		// The rowid travels as the id of a candidate, folded with it. Read
		// in the transaction, so a node written meanwhile by another process
		// is not marked folded with its old text.
		batch, err := readNodeCandidates(ctx, tx.QueryContext, nil, `SELECT CAST(f.rid AS TEXT), COALESCE(g.content, ''), '' FROM `+nodeFoldsTable+` f
			JOIN graph_nodes g ON g.id = f.node_id WHERE f.pending = 1 LIMIT ?`, nodeFoldsBatch)
		if err != nil {
			return err
		}
		for _, p := range batch {
			if _, err := tx.ExecContext(ctx, `INSERT INTO `+nodeFoldsFTS+`(rowid, folded) VALUES (CAST(? AS INTEGER), ?)`, p.id, p.folded); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE `+nodeFoldsTable+` SET pending = 0, folded_len = ?, size = ? WHERE rid = CAST(? AS INTEGER)`,
				utf8.RuneCountInString(p.folded), utf8.RuneCountInString(p.content), p.id); err != nil {
				return err
			}
		}
		n = len(batch)
		return nil
	})
	return n, err
}

// inTx runs fn in a transaction, committed when fn returns nil.
func (db *DB) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := db.SQL().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// indexedHits serves the matches of a name from the name index. The index
// gives every node whose folded content may hold the folded name, with the
// lengths the ranking needs, so the candidates are ordered before any text
// is read. Those whose fold is as long as the name's can only be exact or
// folded matches: their text is read at once, since they outrank the rest.
// The others can only contain the name, and are read in order, a window at a
// time, until the caller has the matches it asked for; on a large store a
// common name is held by thousands of chunks, whose text is most of what a
// call would otherwise read.
type indexedHits struct {
	db           *DB
	name, folded string
	types        map[string]struct{}
	skip         string    // the alias entity, already served
	ready        []nodeHit // ranked, to serve
	rest         []nodeHit // ordered by size and id, text not yet read
}

func (db *DB) indexedNodeHits(ctx context.Context, name, folded string, skipChunks bool, types map[string]struct{}, aliasOf string, isAlias bool) (nodeHits, error) {
	q := `SELECT node_id, folded_len, size FROM ` + nodeFoldsTable + `
		WHERE rid IN (SELECT rowid FROM ` + nodeFoldsFTS + ` WHERE ` + nodeFoldsFTS + ` MATCH ?)`
	if skipChunks {
		q += ` AND substr(node_id, 1, 6) != 'chunk:'`
	}
	rows, err := db.query(ctx, q, `"`+folded+`"`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	k := utf8.RuneCountInString(folded)
	s := &indexedHits{db: db, name: name, folded: folded, types: types}
	var same []nodeHit
	for rows.Next() {
		var h nodeHit
		var foldedLen int
		if err := rows.Scan(&h.id, &foldedLen, &h.size); err != nil {
			return nil, err
		}
		if foldedLen == k {
			same = append(same, h)
		} else {
			s.rest = append(s.rest, h)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if s.ready, err = s.ranked(ctx, same); err != nil {
		return nil, err
	}
	if isAlias {
		// The entity that carries the name as an alias is served once,
		// under the stronger of its two matches.
		if !slices.ContainsFunc(s.ready, func(h nodeHit) bool { return h.id == aliasOf }) {
			s.ready = append(s.ready, nodeHit{id: aliasOf, rank: 2})
		}
		s.skip = aliasOf
	}
	slices.SortFunc(s.ready, compareHits)
	slices.SortFunc(s.rest, compareHits)
	return s, nil
}

func (s *indexedHits) next(ctx context.Context, n int) ([]nodeHit, error) {
	for len(s.ready) < n && len(s.rest) > 0 {
		batch := s.rest[:min(len(s.rest), n)]
		s.rest = s.rest[len(batch):]
		batch = slices.DeleteFunc(slices.Clone(batch), func(h nodeHit) bool { return h.id == s.skip })
		hits, err := s.ranked(ctx, batch)
		if err != nil {
			return nil, err
		}
		s.ready = append(s.ready, hits...)
	}
	window := s.ready[:min(n, len(s.ready))]
	s.ready = s.ready[len(window):]
	return window, nil
}

// ranked reads the text of candidates and keeps, in their order, those of the
// wanted types that match the name, ranked as the scan ranks them.
func (s *indexedHits) ranked(ctx context.Context, candidates []nodeHit) ([]nodeHit, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	ids := make([]any, len(candidates))
	for i, c := range candidates {
		ids[i] = c.id
	}
	read, err := readNodeCandidates(ctx, s.db.query, s.types, `SELECT id, COALESCE(content,''), COALESCE(node_type,'') FROM graph_nodes WHERE id IN (?`+strings.Repeat(",?", len(ids)-1)+`)`, ids...)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]nodeCandidate, len(read))
	for _, c := range read {
		byID[c.id] = c
	}
	hits := make([]nodeHit, 0, len(candidates))
	for _, h := range candidates {
		// A node gone since the index was read has no text, and matches
		// nothing.
		c := byID[h.id]
		if h.rank = rankNode(c.content, c.folded, s.name, s.folded); h.rank >= 0 {
			hits = append(hits, h)
		}
	}
	return hits, nil
}
