package agentmem

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SetContext writes (replaces) the content of a context slot for a given scope.
func (s *Store) SetContext(ctx context.Context, scope Scope, slot ContextSlot, content string) error {
	if slot == "" {
		return fmt.Errorf("agentmem: empty slot")
	}
	bank := BankID(normalizeScope(scope))
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agentmem_context_slots (bank_id, slot, content, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(bank_id, slot) DO UPDATE SET
			content = excluded.content,
			updated_at = excluded.updated_at
	`, bank, string(slot), content, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("agentmem: set context: %w", err)
	}
	return nil
}

// GetContext reads a single context slot. Returns "" with ErrNotFound when the
// slot has not been written.
func (s *Store) GetContext(ctx context.Context, scope Scope, slot ContextSlot) (string, error) {
	bank := BankID(normalizeScope(scope))
	row := s.db.QueryRowContext(ctx, `SELECT content FROM agentmem_context_slots WHERE bank_id = ? AND slot = ?`, bank, string(slot))
	var content string
	if err := row.Scan(&content); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("agentmem: get context: %w", err)
	}
	return content, nil
}

// AppendContext appends content to a slot, creating it if missing. A newline
// separator is inserted between the existing content and the new section.
func (s *Store) AppendContext(ctx context.Context, scope Scope, slot ContextSlot, content string) error {
	current, err := s.GetContext(ctx, scope, slot)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if current != "" {
		if !strings.HasSuffix(current, "\n") {
			current += "\n"
		}
		current += content
	} else {
		current = content
	}
	return s.SetContext(ctx, scope, slot, current)
}

// DeleteContext removes a slot.
func (s *Store) DeleteContext(ctx context.Context, scope Scope, slot ContextSlot) error {
	bank := BankID(normalizeScope(scope))
	_, err := s.db.ExecContext(ctx, `DELETE FROM agentmem_context_slots WHERE bank_id = ? AND slot = ?`, bank, string(slot))
	return err
}

// BuildContextString concatenates the slots for a scope in the given order
// (or DefaultContextOrder when nil), emitting `# <SLOT>` headings between
// non-empty sections. Empty/missing slots are skipped silently.
func (s *Store) BuildContextString(ctx context.Context, scope Scope, order []ContextSlot) (string, error) {
	if len(order) == 0 {
		order = DefaultContextOrder
	}
	var sb strings.Builder
	for _, slot := range order {
		content, err := s.GetContext(ctx, scope, slot)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return "", err
		}
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("# ")
		sb.WriteString(string(slot))
		sb.WriteString("\n\n")
		sb.WriteString(content)
	}
	return sb.String(), nil
}

// EntrypointOptions controls BuildEntrypoint.
type EntrypointOptions struct {
	TopN            int
	IncludeArchived bool
	IncludeStale    bool
	Title           string // optional heading; defaults to "Memory Entrypoint"
}

// BuildEntrypoint renders a Markdown summary of the top-N memories for a scope,
// ranked by importance (then recency). This replaces agent-go's auto-generated
// MEMORY.md entrypoint with an in-memory string.
func (s *Store) BuildEntrypoint(ctx context.Context, scope Scope, opts EntrypointOptions) (string, error) {
	if opts.TopN <= 0 {
		opts.TopN = 20
	}
	scope = normalizeScope(scope)

	conds := []string{"bank_id = ?"}
	args := []any{BankID(scope)}
	if !opts.IncludeArchived {
		conds = append(conds, "archived = 0")
	}
	if !opts.IncludeStale {
		conds = append(conds, "valid_to IS NULL", "(superseded_by IS NULL OR superseded_by = '')")
	}
	args = append(args, opts.TopN)

	rows, err := s.db.QueryContext(ctx, selectMemoryColumns+`
		FROM agentmem_memories
		WHERE `+strings.Join(conds, " AND ")+`
		ORDER BY importance DESC, created_at DESC
		LIMIT ?
	`, args...)
	if err != nil {
		return "", fmt.Errorf("agentmem: entrypoint query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var sb strings.Builder
	title := opts.Title
	if title == "" {
		title = "Memory Entrypoint"
	}
	sb.WriteString("# ")
	sb.WriteString(title)
	sb.WriteString("\n\n")
	sb.WriteString("- Scope: `")
	sb.WriteString(BankID(scope))
	sb.WriteString("`\n")
	sb.WriteString("- Generated: ")
	sb.WriteString(time.Now().UTC().Format(time.RFC3339))
	sb.WriteString("\n\n")

	count := 0
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return "", err
		}
		count++
		fmt.Fprintf(&sb, "## %d. [%s] %.2f\n\n%s\n\n",
			count, m.Type, m.Importance, strings.TrimSpace(m.Content))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if count == 0 {
		sb.WriteString("_No memories yet._\n")
	}
	return sb.String(), nil
}
