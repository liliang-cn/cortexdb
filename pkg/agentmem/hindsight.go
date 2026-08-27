package agentmem

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// MarkStale marks a memory as superseded by another. It sets ValidTo to now
// and appends a revision entry recording the supersession.
func (s *Store) MarkStale(ctx context.Context, id, supersededBy string) error {
	if id == "" {
		return fmt.Errorf("agentmem: empty id")
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := s.txExec(ctx, tx, `
		UPDATE agentmem_memories
		SET valid_to = ?, superseded_by = ?, updated_at = ?
		WHERE id = ?
	`, now, supersededBy, now, id)
	if err != nil {
		return fmt.Errorf("agentmem: mark stale: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return ErrNotFound
	}
	if err := s.appendRevisionTx(ctx, tx, id, "reflect", fmt.Sprintf("superseded by %s", supersededBy), now); err != nil {
		return err
	}
	return tx.Commit()
}

// Archive flags a memory as archived (excluded from default search) and
// records the reason.
func (s *Store) Archive(ctx context.Context, id, reason string) error {
	if id == "" {
		return fmt.Errorf("agentmem: empty id")
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := s.txExec(ctx, tx, `
		UPDATE agentmem_memories
		SET archived = 1, archived_at = ?, archive_reason = ?, updated_at = ?
		WHERE id = ?
	`, now, reason, now, id)
	if err != nil {
		return fmt.Errorf("agentmem: archive: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return ErrNotFound
	}
	if err := s.appendRevisionTx(ctx, tx, id, "archive", reason, now); err != nil {
		return err
	}
	return tx.Commit()
}

// Unarchive clears the archive flags.
func (s *Store) Unarchive(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("agentmem: empty id")
	}
	now := time.Now().UTC()
	res, err := s.exec(ctx, `
		UPDATE agentmem_memories
		SET archived = 0, archived_at = NULL, archive_reason = '', updated_at = ?
		WHERE id = ?
	`, now, id)
	if err != nil {
		return fmt.Errorf("agentmem: unarchive: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return ErrNotFound
	}
	return nil
}

// AddRevision appends a revision entry to a memory's history.
func (s *Store) AddRevision(ctx context.Context, id, by, summary string) error {
	if id == "" {
		return fmt.Errorf("agentmem: empty id")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.appendRevisionTx(ctx, tx, id, by, summary, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

// IsStale reports whether a memory has been superseded.
func IsStale(m *Memory) bool {
	return m != nil && (m.ValidTo != nil || m.SupersededBy != "")
}

func (s *Store) appendRevisionTx(ctx context.Context, tx *sql.Tx, id, by, summary string, at time.Time) error {
	var nextSeq int
	row := s.txQueryRow(ctx, tx, `SELECT COALESCE(MAX(seq), -1) + 1 FROM agentmem_revisions WHERE memory_id = ?`, id)
	if err := row.Scan(&nextSeq); err != nil {
		return fmt.Errorf("agentmem: revision seq: %w", err)
	}
	if _, err := s.txExec(ctx, tx, `INSERT INTO agentmem_revisions (memory_id, seq, at, by, summary) VALUES (?, ?, ?, ?, ?)`,
		id, nextSeq, at, by, summary); err != nil {
		return fmt.Errorf("agentmem: insert revision: %w", err)
	}
	return nil
}
