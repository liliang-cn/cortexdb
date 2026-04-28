package agentmem

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Store provides a SQL-backed agent memory layer mirroring agent-go's
// FileMemoryStore semantics (typed memories, Hindsight fields, banks, mental
// models, context slots) without requiring an embedder.
type Store struct {
	db        *sql.DB
	parent    *cortexdb.DB
	mu        sync.Mutex
	initOnce  sync.Once
	initErr   error
	useFTS    bool
	ftsBackup bool // true when falling back to unicode61
}

// New creates a Store on top of a cortexdb DB, ensuring schema exists.
func New(db *cortexdb.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("agentmem: nil cortexdb.DB")
	}
	s := &Store{db: db.SQL(), parent: db}
	if err := s.ensureSchema(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

// Parent returns the underlying cortexdb.DB so callers can compose other
// CortexDB layers.
func (s *Store) Parent() *cortexdb.DB { return s.parent }

func (s *Store) ensureSchema(ctx context.Context) error {
	s.initOnce.Do(func() {
		if _, err := s.db.ExecContext(ctx, schemaSQL); err != nil {
			s.initErr = fmt.Errorf("agentmem: create schema: %w", err)
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
