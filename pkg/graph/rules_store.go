package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Rules live in a table of their own, not as nodes in the graph they reason
// about.
//
// The alternative — a node per rule, id "rule:<id>" — was tempting because it
// needs no DDL. It was rejected because a rule is not a fact about the world the
// graph describes, and every reader of that graph would have had to learn to
// ignore it: find_nodes would return rules, expand_graph would walk into them,
// an export would carry them, a SHACL shape over the node types would have to
// exempt them, and a node needs a vector this store would have had to invent.
// A rule is configuration for the engine, so it is stored where configuration
// goes.
//
// The table is created lazily by the rule paths rather than in createGraphSchema
// so that a brain that never declares a rule never grows the table.

// StoredRule is a rule as persisted, with the bookkeeping the store adds.
type StoredRule struct {
	Rule
	// Text is the rule rendered into the textual form, stored alongside the
	// structured body so a human reading the table sees the rule.
	Text      string    `json:"text"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ensureRuleSchema creates the rule table once per store.
func (g *GraphStore) ensureRuleSchema(ctx context.Context) error {
	if err := g.InitGraphSchema(ctx); err != nil {
		return fmt.Errorf("init graph schema: %w", err)
	}
	g.ruleSchemaMu.Lock()
	defer g.ruleSchemaMu.Unlock()
	if g.ruleSchemaReady {
		return nil
	}
	const schema = `
	CREATE TABLE IF NOT EXISTS kg_rules (
		id TEXT PRIMARY KEY,
		name TEXT,
		rule_text TEXT NOT NULL,
		body TEXT NOT NULL,
		confidence REAL NOT NULL DEFAULT 1.0,
		note TEXT,
		enabled INTEGER NOT NULL DEFAULT 1,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_kg_rules_enabled ON kg_rules(enabled);
	`
	if _, err := g.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create rule schema: %w", err)
	}
	g.ruleSchemaReady = true
	return nil
}

// SaveRule stores a rule under its id, replacing any rule already there.
func (g *GraphStore) SaveRule(ctx context.Context, rule Rule, enabled bool) (*StoredRule, error) {
	if err := rule.Validate(); err != nil {
		return nil, err
	}
	if err := g.ensureRuleSchema(ctx); err != nil {
		return nil, err
	}
	body, err := json.Marshal(rule)
	if err != nil {
		return nil, fmt.Errorf("encode rule %s: %w", rule.ID, err)
	}
	enabledValue := 0
	if enabled {
		enabledValue = 1
	}
	_, err = g.exec(ctx, `
		INSERT INTO kg_rules (id, name, rule_text, body, confidence, note, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			rule_text = excluded.rule_text,
			body = excluded.body,
			confidence = excluded.confidence,
			note = excluded.note,
			enabled = excluded.enabled,
			updated_at = CURRENT_TIMESTAMP
	`, rule.ID, rule.Name, rule.Text(), string(body), rule.effectiveConfidence(), rule.Note, enabledValue)
	if err != nil {
		return nil, fmt.Errorf("save rule %s: %w", rule.ID, err)
	}
	return g.GetRule(ctx, rule.ID)
}

// GetRule returns one stored rule, or nil when there is no such rule.
func (g *GraphStore) GetRule(ctx context.Context, id string) (*StoredRule, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("rule id is required")
	}
	if err := g.ensureRuleSchema(ctx); err != nil {
		return nil, err
	}
	row := g.queryRow(ctx, `
		SELECT id, rule_text, body, enabled, created_at, updated_at
		FROM kg_rules
		WHERE id = ?
		ORDER BY id ASC
	`, id)
	stored, err := scanStoredRule(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get rule %s: %w", id, err)
	}
	return stored, nil
}

// ListRules returns the stored rules in id order. Passing onlyEnabled skips the
// ones somebody has switched off without deleting.
func (g *GraphStore) ListRules(ctx context.Context, onlyEnabled bool) ([]StoredRule, error) {
	if err := g.ensureRuleSchema(ctx); err != nil {
		return nil, err
	}
	query := `
		SELECT id, rule_text, body, enabled, created_at, updated_at
		FROM kg_rules
	`
	if onlyEnabled {
		query += ` WHERE enabled = 1`
	}
	query += ` ORDER BY id ASC`

	rows, err := g.query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]StoredRule, 0)
	for rows.Next() {
		stored, err := scanStoredRule(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("list rules: %w", err)
		}
		out = append(out, *stored)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rules: %w", err)
	}
	return out, nil
}

// DeleteRule removes a stored rule and reports whether there was one.
//
// It does not touch the edges that rule derived. Deleting a rule is a statement
// about what will be derived next time, not a retraction of what is already in
// the graph — and the derived edges carry the rule text with them, so they
// remain explicable after the rule is gone.
func (g *GraphStore) DeleteRule(ctx context.Context, id string) (bool, error) {
	if strings.TrimSpace(id) == "" {
		return false, fmt.Errorf("rule id is required")
	}
	if err := g.ensureRuleSchema(ctx); err != nil {
		return false, err
	}
	result, err := g.exec(ctx, `DELETE FROM kg_rules WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete rule %s: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		// Not every driver reports this; the row is gone either way.
		return true, nil
	}
	return affected > 0, nil
}

func scanStoredRule(scan func(...any) error) (*StoredRule, error) {
	var (
		id, text, body string
		enabled        int
		createdAt      time.Time
		updatedAt      time.Time
	)
	if err := scan(&id, &text, &body, &enabled, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	var rule Rule
	if err := json.Unmarshal([]byte(body), &rule); err != nil {
		return nil, fmt.Errorf("decode rule %s: %w", id, err)
	}
	rule.ID = id
	return &StoredRule{
		Rule:      rule,
		Text:      text,
		Enabled:   enabled != 0,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
}
