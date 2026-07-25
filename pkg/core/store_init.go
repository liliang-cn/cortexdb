package core

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // SQLite driver
)

// Init initializes the SQLite database and creates necessary tables
func (s *SQLiteStore) Init(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return wrapError("init", ErrStoreClosed)
	}

	// Open database connection. NOTE: pragmas must use modernc.org/sqlite's
	// `_pragma=name(value)` DSN syntax — the mattn/CGO `_journal_mode=...` form is
	// silently ignored by this pure-Go driver, which previously left WAL and the
	// busy timeout unset (causing SQLITE_BUSY under concurrent readers/writers).
	// Pragmas in the DSN apply to EVERY pooled connection (unlike a one-off
	// PRAGMA via Exec, which only affects a single connection).
	//   journal_mode(WAL): better read/write concurrency
	//   synchronous(NORMAL): good balance of safety and speed
	//   busy_timeout(5000): wait up to 5s for a lock instead of failing immediately
	//   cache_size(-2000): 2MB page cache (negative = KiB)
	//   foreign_keys(ON): enforce FK cascades on every connection
	// Ensure the parent directory exists for file-backed databases so nested
	// paths (e.g. ".cortexdb/cortexdb.db") open without the caller having to
	// pre-create the directory. In-memory databases have no directory.
	if dir := filepath.Dir(s.config.Path); dir != "" && dir != "." && !strings.HasPrefix(s.config.Path, ":memory:") && !strings.Contains(s.config.Path, ":memory:") {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return wrapError("init", fmt.Errorf("failed to create database directory %q: %w", dir, err))
		}
	}

	dsn := fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=cache_size(-2000)&_pragma=foreign_keys(ON)", s.config.Path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return wrapError("init", fmt.Errorf("failed to open database: %w", err))
	}

	// Configure connection pool with sensible defaults
	// Allow more open connections for read concurrency
	db.SetMaxOpenConns(25)
	// Keep enough idle connections to avoid reconnection overhead
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(2 * time.Hour)

	s.db = db

	// Enable Foreign Keys (Crucial for cascading deletes)
	if _, err := s.db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		return wrapError("init", fmt.Errorf("failed to enable foreign keys: %w", err))
	}

	// Create tables
	if err := s.createTables(ctx); err != nil {
		return wrapError("init", err)
	}

	// Initialize HNSW index if enabled
	if err := s.initHNSWIndex(ctx); err != nil {
		return wrapError("init", err)
	}

	// Initialize IVF index if enabled
	if err := s.initIVFIndex(ctx); err != nil {
		return wrapError("init", err)
	}

	s.logger.Info("database initialized", "path", s.config.Path)

	// Start auto-save if enabled
	if s.config.AutoSave.Enabled {
		s.startAutoSave()
	}

	return nil
}

// createTables creates the necessary database tables
func (s *SQLiteStore) createTables(ctx context.Context) error {
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS collections (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		dimensions INTEGER NOT NULL DEFAULT 0,
		description TEXT,
		metadata TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS documents (
		id TEXT PRIMARY KEY,
		title TEXT,
		content TEXT,
		source_url TEXT,
		version INTEGER DEFAULT 1,
		author TEXT,
		metadata TEXT,
		acl TEXT, -- JSON list of allowed users/groups
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS embeddings (
		id TEXT PRIMARY KEY,
		collection_id INTEGER DEFAULT 1,
		vector BLOB NOT NULL,
		content TEXT NOT NULL,
		doc_id TEXT,
		metadata TEXT,
		acl TEXT, -- JSON list of allowed users/groups (inherits from doc if null)
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (collection_id) REFERENCES collections(id) ON DELETE CASCADE,
		FOREIGN KEY (doc_id) REFERENCES documents(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_embeddings_collection_id ON embeddings(collection_id);
	CREATE INDEX IF NOT EXISTS idx_embeddings_doc_id ON embeddings(doc_id);
	CREATE INDEX IF NOT EXISTS idx_embeddings_created_at ON embeddings(created_at);
	CREATE INDEX IF NOT EXISTS idx_collections_name ON collections(name);

	CREATE TABLE IF NOT EXISTS index_snapshots (
		type TEXT PRIMARY KEY,
		data BLOB NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		user_id TEXT,
		metadata TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS messages (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		role TEXT NOT NULL, -- 'user', 'assistant', 'system'
		content TEXT NOT NULL,
		vector BLOB, -- Optional embedding for long-term memory
		metadata TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id);
	CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at);
	CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);

	-- FTS5 Virtual Table for message keyword search (BM25 via SQLite FTS5)
	-- content='messages' keeps a shadow copy synced via triggers below.
	CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(content, content='messages', content_rowid='rowid');

	-- Companion CJK index. unicode61 (above) does not segment Han/Hiragana/Hangul, so a
	-- run of CJK characters becomes one token and no Chinese query can ever match. The
	-- trigram tokenizer indexes substrings instead, which does work for CJK — but it is
	-- a poor word index for space-separated languages, so it lives beside the unicode61
	-- index rather than replacing it, and callers route by script (see CJKAwareIndex).
	CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts_cjk USING fts5(content, content='messages', content_rowid='rowid', tokenize='trigram');

	CREATE TRIGGER IF NOT EXISTS messages_cjk_ai AFTER INSERT ON messages BEGIN
	  INSERT INTO messages_fts_cjk(rowid, content) VALUES (new.rowid, new.content);
	END;
	CREATE TRIGGER IF NOT EXISTS messages_cjk_ad AFTER DELETE ON messages BEGIN
	  INSERT INTO messages_fts_cjk(messages_fts_cjk, rowid, content) VALUES('delete', old.rowid, old.content);
	END;
	CREATE TRIGGER IF NOT EXISTS messages_cjk_au AFTER UPDATE ON messages BEGIN
	  INSERT INTO messages_fts_cjk(messages_fts_cjk, rowid, content) VALUES('delete', old.rowid, old.content);
	  INSERT INTO messages_fts_cjk(rowid, content) VALUES (new.rowid, new.content);
	END;

	-- Triggers to keep messages_fts in sync with messages table
	CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
	  INSERT INTO messages_fts(rowid, content) VALUES (new.rowid, new.content);
	END;
	CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
	  INSERT INTO messages_fts(messages_fts, rowid, content) VALUES('delete', old.rowid, old.content);
	END;
	CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
	  INSERT INTO messages_fts(messages_fts, rowid, content) VALUES('delete', old.rowid, old.content);
	  INSERT INTO messages_fts(rowid, content) VALUES (new.rowid, new.content);
	END;

	-- FTS5 Virtual Table for Hybrid Search
	-- We use 'content' option to avoid duplicating data, referencing embeddings table
	-- Note: Triggers are needed to keep FTS index in sync
	CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(content, content='embeddings', content_rowid='rowid');

	-- Companion CJK index for chunks — see the note on messages_fts_cjk.
	CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts_cjk USING fts5(content, content='embeddings', content_rowid='rowid', tokenize='trigram');

	CREATE TRIGGER IF NOT EXISTS embeddings_cjk_ai AFTER INSERT ON embeddings BEGIN
	  INSERT INTO chunks_fts_cjk(rowid, content) VALUES (new.rowid, new.content);
	END;
	CREATE TRIGGER IF NOT EXISTS embeddings_cjk_ad AFTER DELETE ON embeddings BEGIN
	  INSERT INTO chunks_fts_cjk(chunks_fts_cjk, rowid, content) VALUES('delete', old.rowid, old.content);
	END;
	CREATE TRIGGER IF NOT EXISTS embeddings_cjk_au AFTER UPDATE ON embeddings BEGIN
	  INSERT INTO chunks_fts_cjk(chunks_fts_cjk, rowid, content) VALUES('delete', old.rowid, old.content);
	  INSERT INTO chunks_fts_cjk(rowid, content) VALUES (new.rowid, new.content);
	END;

	-- Triggers to keep FTS index in sync
	CREATE TRIGGER IF NOT EXISTS embeddings_ai AFTER INSERT ON embeddings BEGIN
	  INSERT INTO chunks_fts(rowid, content) VALUES (new.rowid, new.content);
	END;
	CREATE TRIGGER IF NOT EXISTS embeddings_ad AFTER DELETE ON embeddings BEGIN
	  INSERT INTO chunks_fts(chunks_fts, rowid, content) VALUES('delete', old.rowid, old.content);
	END;
	CREATE TRIGGER IF NOT EXISTS embeddings_au AFTER UPDATE ON embeddings BEGIN
	  INSERT INTO chunks_fts(chunks_fts, rowid, content) VALUES('delete', old.rowid, old.content);
	  INSERT INTO chunks_fts(rowid, content) VALUES (new.rowid, new.content);
	END;
	`

	_, err := s.db.ExecContext(ctx, createTableSQL)
	if err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}

	if err := s.backfillCJKIndexes(ctx); err != nil {
		return err
	}

	// Create default collection if it doesn't exist
	_, err = s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO collections (id, name, dimensions, description, created_at, updated_at)
		VALUES (1, 'default', ?, 'Default collection', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, s.config.VectorDim)
	if err != nil {
		return fmt.Errorf("failed to create default collection: %w", err)
	}

	return nil
}

// cjkIndexes pairs each CJK companion index with the table it shadows.
var cjkIndexes = []struct{ index, source string }{
	{index: "chunks_fts_cjk", source: "embeddings"},
	{index: "messages_fts_cjk", source: "messages"},
}

// schemaVersionCJKIndexes marks the schema revision that introduced the trigram
// companion indexes. `PRAGMA user_version` is unused elsewhere in CortexDB, so it
// serves as the bookkeeping for one-time index builds.
const schemaVersionCJKIndexes = 1

// backfillCJKIndexes populates the trigram companion indexes for a database whose rows
// predate them. `CREATE VIRTUAL TABLE IF NOT EXISTS` creates an empty index and the
// triggers only fire on new writes, so without this pass existing content stays
// invisible to CJK lexical search.
//
// Emptiness cannot be detected by counting: on an external-content FTS5 table
// `count(*)` reads the *source* table and so reports rows the index does not hold.
// The build therefore runs once, gated on the schema version.
func (s *SQLiteStore) backfillCJKIndexes(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("failed to read schema version: %w", err)
	}
	if version >= schemaVersionCJKIndexes {
		return nil
	}
	for _, table := range cjkIndexes {
		var rows int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table.source).Scan(&rows); err != nil {
			return fmt.Errorf("failed to measure %s: %w", table.source, err)
		}
		if rows == 0 {
			continue
		}
		rebuild := fmt.Sprintf(`INSERT INTO %s(%s) VALUES('rebuild')`, table.index, table.index)
		if _, err := s.db.ExecContext(ctx, rebuild); err != nil {
			return fmt.Errorf("failed to build %s: %w", table.index, err)
		}
		log.Printf("cortexdb: built %s over %d rows so CJK text is searchable", table.index, rows)
	}
	if _, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersionCJKIndexes)); err != nil {
		return fmt.Errorf("failed to record schema version: %w", err)
	}
	return nil
}
