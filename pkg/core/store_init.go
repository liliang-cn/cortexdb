package core

import (
	"context"
	"database/sql"
	"fmt"
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

	// Open database connection
	// _journal_mode=WAL: Better concurrency
	// _synchronous=NORMAL: Good balance of safety and speed
	// _busy_timeout=5000: Wait up to 5s for lock instead of failing immediately
	// _cache_size=-2000: Use 2MB of memory for cache (negative value = kb)
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000&_cache_size=-2000", s.config.Path)
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

	-- FTS5 Virtual Table for Hybrid Search
	-- We use 'content' option to avoid duplicating data, referencing embeddings table
	-- Note: Triggers are needed to keep FTS index in sync
	CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(content, content='embeddings', content_rowid='rowid');

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
