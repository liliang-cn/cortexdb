package agentmem

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a memory id has no row.
var ErrNotFound = errors.New("agentmem: memory not found")

// Save upserts a memory. If m.ID is empty it is generated; CreatedAt / UpdatedAt
// / ValidFrom are populated when zero.
func (s *Store) Save(ctx context.Context, m *Memory) error {
	if m == nil {
		return fmt.Errorf("agentmem: nil memory")
	}
	if strings.TrimSpace(m.Content) == "" {
		return fmt.Errorf("agentmem: empty content")
	}
	now := time.Now().UTC()
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	if m.Type == "" {
		m.Type = TypeFact
	}
	if m.Importance == 0 {
		m.Importance = 0.5
	}
	if m.SourceType == "" {
		m.SourceType = SourceUserInput
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	if m.ValidFrom.IsZero() {
		m.ValidFrom = m.CreatedAt
	}
	m.UpdatedAt = now
	m.Scope = normalizeScope(m.Scope)
	bank := BankID(m.Scope)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("agentmem: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agentmem_memories
			(id, scope_type, scope_id, bank_id, type, content, importance, source_type,
			 confidence, valid_from, valid_to, superseded_by, conflicting,
			 archived, archived_at, archive_reason,
			 access_count, last_accessed, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			scope_type = excluded.scope_type,
			scope_id = excluded.scope_id,
			bank_id = excluded.bank_id,
			type = excluded.type,
			content = excluded.content,
			importance = excluded.importance,
			source_type = excluded.source_type,
			confidence = excluded.confidence,
			valid_from = excluded.valid_from,
			valid_to = excluded.valid_to,
			superseded_by = excluded.superseded_by,
			conflicting = excluded.conflicting,
			archived = excluded.archived,
			archived_at = excluded.archived_at,
			archive_reason = excluded.archive_reason,
			updated_at = excluded.updated_at
	`,
		m.ID, string(m.Scope.Type), m.Scope.ID, bank, string(m.Type), m.Content, m.Importance, string(m.SourceType),
		m.Confidence, nullTime(m.ValidFrom), nullTimePtr(m.ValidTo), m.SupersededBy, boolToInt(m.Conflicting),
		boolToInt(m.Archived), nullTimePtr(m.ArchivedAt), m.ArchiveReason,
		m.AccessCount, nullTime(m.LastAccessed), m.CreatedAt, m.UpdatedAt,
	); err != nil {
		return fmt.Errorf("agentmem: upsert memory: %w", err)
	}

	if err := replaceStringSet(ctx, tx, "agentmem_tags", "tag", m.ID, m.Tags); err != nil {
		return err
	}
	if err := replaceStringSet(ctx, tx, "agentmem_keywords", "keyword", m.ID, m.Keywords); err != nil {
		return err
	}
	if err := replaceStringSet(ctx, tx, "agentmem_evidence", "evidence_id", m.ID, m.EvidenceIDs); err != nil {
		return err
	}
	if err := replaceRevisions(ctx, tx, m.ID, m.RevisionHistory); err != nil {
		return err
	}
	if err := upsertFTS(ctx, tx, m); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("agentmem: commit: %w", err)
	}
	return nil
}

// Get loads a single memory by id, including side tables.
func (s *Store) Get(ctx context.Context, id string) (*Memory, error) {
	if id == "" {
		return nil, fmt.Errorf("agentmem: empty id")
	}
	row := s.db.QueryRowContext(ctx, selectMemoryColumns+` FROM agentmem_memories WHERE id = ?`, id)
	m, err := scanMemory(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := s.attachSides(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

// Delete removes a memory and its side rows.
func (s *Store) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("agentmem: empty id")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, table := range []string{
		"agentmem_tags", "agentmem_keywords", "agentmem_evidence", "agentmem_revisions",
	} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE memory_id = ?", id); err != nil {
			return fmt.Errorf("agentmem: delete %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM agentmem_fts WHERE memory_id = ?", id); err != nil {
		return fmt.Errorf("agentmem: delete fts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM agentmem_memories WHERE id = ?", id); err != nil {
		return fmt.Errorf("agentmem: delete memory: %w", err)
	}
	return tx.Commit()
}

// Clear removes all memories (and side tables) but preserves bank config /
// mental models / context slots, mirroring agent-go behavior.
func (s *Store) Clear(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range []string{
		"DELETE FROM agentmem_tags",
		"DELETE FROM agentmem_keywords",
		"DELETE FROM agentmem_evidence",
		"DELETE FROM agentmem_revisions",
		"DELETE FROM agentmem_fts",
		"DELETE FROM agentmem_memories",
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("agentmem: clear: %w", err)
		}
	}
	return tx.Commit()
}

// IncrementAccess bumps access_count and last_accessed.
func (s *Store) IncrementAccess(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE agentmem_memories
		SET access_count = access_count + 1,
		    last_accessed = ?
		WHERE id = ?
	`, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("agentmem: increment access: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns memories with simple pagination, newest first, including the
// total count.
func (s *Store) List(ctx context.Context, limit, offset int) ([]*Memory, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agentmem_memories`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("agentmem: count: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, selectMemoryColumns+`
		FROM agentmem_memories
		ORDER BY created_at DESC, id ASC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("agentmem: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out, err := s.scanAndAttach(ctx, rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ListByScope returns active memories within a scope, newest first.
func (s *Store) ListByScope(ctx context.Context, scope Scope, limit int) ([]*Memory, error) {
	if limit <= 0 {
		limit = 50
	}
	scope = normalizeScope(scope)
	rows, err := s.db.QueryContext(ctx, selectMemoryColumns+`
		FROM agentmem_memories
		WHERE bank_id = ? AND archived = 0
		ORDER BY importance DESC, created_at DESC
		LIMIT ?
	`, BankID(scope), limit)
	if err != nil {
		return nil, fmt.Errorf("agentmem: list by scope: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return s.scanAndAttach(ctx, rows)
}

// GetByType returns active memories matching a type.
func (s *Store) GetByType(ctx context.Context, t Type, limit int) ([]*Memory, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, selectMemoryColumns+`
		FROM agentmem_memories
		WHERE type = ? AND archived = 0
		ORDER BY importance DESC, created_at DESC
		LIMIT ?
	`, string(t), limit)
	if err != nil {
		return nil, fmt.Errorf("agentmem: list by type: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return s.scanAndAttach(ctx, rows)
}

// ----- helpers -----

const selectMemoryColumns = `
SELECT id, scope_type, scope_id, type, content, importance, source_type,
       confidence, valid_from, valid_to, superseded_by, conflicting,
       archived, archived_at, archive_reason,
       access_count, last_accessed, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMemory(r rowScanner) (*Memory, error) {
	var (
		m            Memory
		scopeType    string
		typ          string
		source       string
		validFrom    sql.NullTime
		validTo      sql.NullTime
		archivedAt   sql.NullTime
		lastAccessed sql.NullTime
		conflicting  int
		archived     int
	)
	if err := r.Scan(
		&m.ID, &scopeType, &m.Scope.ID, &typ, &m.Content, &m.Importance, &source,
		&m.Confidence, &validFrom, &validTo, &m.SupersededBy, &conflicting,
		&archived, &archivedAt, &m.ArchiveReason,
		&m.AccessCount, &lastAccessed, &m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		return nil, err
	}
	m.Scope.Type = ScopeType(scopeType)
	m.Type = Type(typ)
	m.SourceType = SourceType(source)
	m.Conflicting = conflicting != 0
	m.Archived = archived != 0
	if validFrom.Valid {
		m.ValidFrom = validFrom.Time
	}
	if validTo.Valid {
		t := validTo.Time
		m.ValidTo = &t
	}
	if archivedAt.Valid {
		t := archivedAt.Time
		m.ArchivedAt = &t
	}
	if lastAccessed.Valid {
		m.LastAccessed = lastAccessed.Time
	}
	return &m, nil
}

func (s *Store) scanAndAttach(ctx context.Context, rows *sql.Rows) ([]*Memory, error) {
	var out []*Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, fmt.Errorf("agentmem: scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, m := range out {
		if err := s.attachSides(ctx, m); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) attachSides(ctx context.Context, m *Memory) error {
	tags, err := loadStringSet(ctx, s.db, "agentmem_tags", "tag", m.ID)
	if err != nil {
		return err
	}
	m.Tags = tags
	kws, err := loadStringSet(ctx, s.db, "agentmem_keywords", "keyword", m.ID)
	if err != nil {
		return err
	}
	m.Keywords = kws
	ev, err := loadStringSet(ctx, s.db, "agentmem_evidence", "evidence_id", m.ID)
	if err != nil {
		return err
	}
	m.EvidenceIDs = ev
	revs, err := loadRevisions(ctx, s.db, m.ID)
	if err != nil {
		return err
	}
	m.RevisionHistory = revs
	return nil
}

func replaceStringSet(ctx context.Context, tx *sql.Tx, table, col, id string, values []string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE memory_id = ?", id); err != nil {
		return fmt.Errorf("agentmem: clear %s: %w", table, err)
	}
	if len(values) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT OR IGNORE INTO "+table+" (memory_id, "+col+") VALUES (?, ?)")
	if err != nil {
		return fmt.Errorf("agentmem: prepare %s: %w", table, err)
	}
	defer func() { _ = stmt.Close() }()
	seen := map[string]struct{}{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		if _, err := stmt.ExecContext(ctx, id, v); err != nil {
			return fmt.Errorf("agentmem: insert %s: %w", table, err)
		}
	}
	return nil
}

func replaceRevisions(ctx context.Context, tx *sql.Tx, id string, revs []Revision) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM agentmem_revisions WHERE memory_id = ?", id); err != nil {
		return fmt.Errorf("agentmem: clear revisions: %w", err)
	}
	if len(revs) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO agentmem_revisions (memory_id, seq, at, by, summary) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for i, r := range revs {
		if _, err := stmt.ExecContext(ctx, id, i, r.At, r.By, r.Summary); err != nil {
			return fmt.Errorf("agentmem: insert revision: %w", err)
		}
	}
	return nil
}

func loadStringSet(ctx context.Context, db *sql.DB, table, col, id string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+col+" FROM "+table+" WHERE memory_id = ? ORDER BY "+col, id)
	if err != nil {
		return nil, fmt.Errorf("agentmem: load %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func loadRevisions(ctx context.Context, db *sql.DB, id string) ([]Revision, error) {
	rows, err := db.QueryContext(ctx, `SELECT at, by, summary FROM agentmem_revisions WHERE memory_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, fmt.Errorf("agentmem: load revisions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Revision
	for rows.Next() {
		var r Revision
		if err := rows.Scan(&r.At, &r.By, &r.Summary); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func upsertFTS(ctx context.Context, tx *sql.Tx, m *Memory) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM agentmem_fts WHERE memory_id = ?`, m.ID); err != nil {
		return fmt.Errorf("agentmem: clear fts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agentmem_fts (memory_id, content, tags, keywords) VALUES (?, ?, ?, ?)`,
		m.ID, m.Content, strings.Join(m.Tags, " "), strings.Join(m.Keywords, " "),
	); err != nil {
		return fmt.Errorf("agentmem: insert fts: %w", err)
	}
	return nil
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func nullTimePtr(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return *t
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
