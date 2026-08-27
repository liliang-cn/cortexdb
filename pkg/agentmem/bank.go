package agentmem

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ConfigureBank stores the disposition for a memory bank (per scope).
func (s *Store) ConfigureBank(ctx context.Context, scope Scope, cfg *BankConfig) error {
	if cfg == nil {
		return fmt.Errorf("agentmem: nil BankConfig")
	}
	directives, err := json.Marshal(cfg.Directives)
	if err != nil {
		return fmt.Errorf("agentmem: marshal directives: %w", err)
	}
	bank := BankID(normalizeScope(scope))
	_, err = s.exec(ctx, `
		INSERT INTO agentmem_bank_config (bank_id, mission, directives, skepticism, literalism, empathy, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(bank_id) DO UPDATE SET
			mission = excluded.mission,
			directives = excluded.directives,
			skepticism = excluded.skepticism,
			literalism = excluded.literalism,
			empathy = excluded.empathy,
			updated_at = excluded.updated_at
	`, bank, cfg.Mission, string(directives), cfg.Skepticism, cfg.Literalism, cfg.Empathy, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("agentmem: configure bank: %w", err)
	}
	return nil
}

// GetBankConfig returns the bank's stored disposition. Returns ErrNotFound
// when the bank has not been configured yet.
func (s *Store) GetBankConfig(ctx context.Context, scope Scope) (*BankConfig, error) {
	bank := BankID(normalizeScope(scope))
	row := s.queryRow(ctx, `
		SELECT mission, directives, skepticism, literalism, empathy
		FROM agentmem_bank_config WHERE bank_id = ?
	`, bank)
	var (
		cfg          BankConfig
		directivesJS string
	)
	if err := row.Scan(&cfg.Mission, &directivesJS, &cfg.Skepticism, &cfg.Literalism, &cfg.Empathy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("agentmem: load bank: %w", err)
	}
	if directivesJS != "" {
		if err := json.Unmarshal([]byte(directivesJS), &cfg.Directives); err != nil {
			return nil, fmt.Errorf("agentmem: decode directives: %w", err)
		}
	}
	return &cfg, nil
}

// AddMentalModel inserts or updates a curated rule.
func (s *Store) AddMentalModel(ctx context.Context, m *MentalModel) error {
	if m == nil || m.Content == "" {
		return fmt.Errorf("agentmem: empty mental model")
	}
	if m.ID == "" {
		return fmt.Errorf("agentmem: mental model id required")
	}
	tags, err := json.Marshal(m.Tags)
	if err != nil {
		return fmt.Errorf("agentmem: marshal tags: %w", err)
	}
	now := time.Now().UTC()
	if _, err := s.exec(ctx, `
		INSERT INTO agentmem_mental_models (id, name, description, content, tags, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			description = excluded.description,
			content = excluded.content,
			tags = excluded.tags,
			updated_at = excluded.updated_at
	`, m.ID, m.Name, m.Description, m.Content, string(tags), now); err != nil {
		return fmt.Errorf("agentmem: upsert mental model: %w", err)
	}
	m.UpdatedAt = now
	return nil
}

// ListMentalModels returns all curated rules ordered by most recently updated.
func (s *Store) ListMentalModels(ctx context.Context) ([]MentalModel, error) {
	rows, err := s.query(ctx, `
		SELECT id, name, description, content, tags, updated_at
		FROM agentmem_mental_models
		ORDER BY updated_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("agentmem: list mental models: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []MentalModel
	for rows.Next() {
		var (
			m      MentalModel
			tagsJS string
		)
		if err := rows.Scan(&m.ID, &m.Name, &m.Description, &m.Content, &tagsJS, &m.UpdatedAt); err != nil {
			return nil, err
		}
		if tagsJS != "" {
			_ = json.Unmarshal([]byte(tagsJS), &m.Tags)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteMentalModel removes a rule by id.
func (s *Store) DeleteMentalModel(ctx context.Context, id string) error {
	_, err := s.exec(ctx, `DELETE FROM agentmem_mental_models WHERE id = ?`, id)
	return err
}
