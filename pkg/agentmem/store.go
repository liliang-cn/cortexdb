package agentmem

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// Store provides a SQL-backed agent memory layer mirroring agent-go's
// FileMemoryStore semantics (typed memories, Hindsight fields, banks, mental
// models, context slots) without requiring an embedder.
type Store struct {
	db     *sql.DB
	parent *cortexdb.DB

	// dialect is the SQL this store speaks. It comes from the parent rather
	// than from the DSN or a guess, so agentmem cannot end up disagreeing with
	// the handle it shares with pkg/graph and pkg/cortexdb.
	dialect sqldialect.Dialect

	mu           sync.Mutex
	initOnce     sync.Once
	initErr      error
	useFTS       bool
	ftsBackup    bool // true when falling back to unicode61
	ftsUnindexed bool // postgres only: pg_trgm unavailable, search scans
}

// New creates a Store on top of a cortexdb DB, ensuring schema exists.
func New(db *cortexdb.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("agentmem: nil cortexdb.DB")
	}
	s := &Store{db: db.SQL(), parent: db, dialect: db.Dialect()}
	if err := s.ensureSchema(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

// Parent returns the underlying cortexdb.DB so callers can compose other
// CortexDB layers.
func (s *Store) Parent() *cortexdb.DB { return s.parent }

// Dialect reports which SQL this store is speaking.
func (s *Store) Dialect() sqldialect.Dialect { return s.dialect }

func (s *Store) isPostgres() bool { return s.dialect.Kind() == sqldialect.Postgres }

func (s *Store) ensureSchema(ctx context.Context) error {
	s.initOnce.Do(func() {
		if _, err := s.db.ExecContext(ctx, schemaSQL(s.dialect)); err != nil {
			s.initErr = fmt.Errorf("agentmem: create schema: %w", err)
			return
		}
		if s.isPostgres() {
			s.initErr = s.ensurePostgresFTS(ctx)
			return
		}
		// Try trigram tokenizer first; some sqlite builds lack it.
		if _, err := s.db.ExecContext(ctx, ftsTrigram); err == nil {
			s.useFTS = true
			return
		}
		// Drop any half-created table and fall back.
		_, _ = s.db.ExecContext(ctx, `DROP TABLE IF EXISTS agentmem_fts`)
		if _, err := s.db.ExecContext(ctx, ftsUnicode61); err != nil {
			s.initErr = fmt.Errorf("agentmem: create fts table: %w", err)
			return
		}
		s.useFTS = true
		s.ftsBackup = true
	})
	return s.initErr
}

// ensurePostgresFTS creates the plain-table stand-in for FTS5 and, if the
// account is allowed to, the trigram indexes that keep the search off a scan.
func (s *Store) ensurePostgresFTS(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, ftsPostgres); err != nil {
		return fmt.Errorf("agentmem: create fts table: %w", err)
	}
	s.useFTS = true
	for _, stmt := range ftsPostgresIndexes {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			// Correct without them, just linear. Reported once rather than
			// fatally, because a managed instance that refuses CREATE
			// EXTENSION should not cost the caller the whole package.
			log.Printf("cortexdb/agentmem: lexical search is unindexed on this instance — %v", err)
			s.ftsUnindexed = true
			return nil
		}
	}
	return nil
}

// SearchIsIndexed reports whether the backend can serve a text search from an
// index. False on PostgreSQL instances that refuse pg_trgm: the same rows come
// back, linearly.
func (s *Store) SearchIsIndexed() bool { return !s.ftsUnindexed }

// UsesFallbackTokenizer reports whether the FTS table fell back from trigram
// to unicode61. Callers running CJK-heavy workloads on older SQLite builds may
// want to surface this to users.
func (s *Store) UsesFallbackTokenizer() bool { return s.ftsBackup }

// BankID returns the canonical bank id for a scope, used as the partition key
// for memories and context slots.
func BankID(scope Scope) string {
	t := normalizeScopeType(scope.Type)
	id := strings.TrimSpace(scope.ID)
	if t == ScopeGlobal {
		return "global"
	}
	if id == "" {
		return string(t)
	}
	return fmt.Sprintf("%s:%s", t, id)
}

func normalizeScopeType(t ScopeType) ScopeType {
	switch t {
	case "":
		return ScopeGlobal
	case ScopeGlobal, ScopeAgent, ScopeTeam, ScopeUser, ScopeSession:
		return t
	default:
		return ScopeGlobal
	}
}

func normalizeScope(scope Scope) Scope {
	scope.Type = normalizeScopeType(scope.Type)
	return scope
}
