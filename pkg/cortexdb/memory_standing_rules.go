package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Standing rules: the global preferences that apply to every task, whatever
// the prompt says.
//
// A rule like "never use common ports such as 8080 for dev servers" is
// recalled only when a prompt or a planner happens to share its words. "Start
// a dev server for the new frontend" shares none, so the rule that governs the
// task never reaches the agent. Standing rules are therefore handed over at
// session start, independent of any prompt, and set apart at the head of a
// recall's context pack when a recall does find them.
//
// What counts as one is read off the metadata the memory was saved with, never
// off its words:
//
//   - scope global: it holds for everyone the brain serves, not one session;
//   - type preference or rule: what someone wants done (the capture writes a
//     standing preference as "preference"; "rule" is the explicit name). A
//     decision records a choice made inside one project and a lesson what was
//     learned; neither is an instruction for unrelated work;
//   - importance at least 0.8: the capture saves a standing preference at
//     0.85 or more and a passing one at 0.3 to 0.6;
//   - current: not superseded, not expired, not rejected, not hidden by the
//     review gate.
//
// A rule whose metadata names a project holds only in that project.

// Standing rule defaults.
const (
	// DefaultStandingRuleMinImportance is the importance from which a global
	// preference is a standing rule.
	DefaultStandingRuleMinImportance = 0.8
	defaultStandingRulesMax          = 16
	defaultStandingRulesMaxChars     = 2400
	// standingRuleLineRunes is how much of one rule a line keeps; the whole
	// memory is a memory_get away.
	standingRuleLineRunes = 160
)

// StandingRuleTypes are the memory types a standing rule may carry.
var StandingRuleTypes = []string{"preference", "rule"}

// StandingRulesRequest asks for the standing rules.
type StandingRulesRequest struct {
	// Project names the project the session works in. Rules tagged with a
	// project are given only when it matches, compared without case or
	// punctuation; untagged rules are always given.
	Project string `json:"project,omitempty"`
	// Types overrides StandingRuleTypes.
	Types []string `json:"types,omitempty"`
	// MinImportance overrides DefaultStandingRuleMinImportance.
	MinImportance *float64 `json:"min_importance,omitempty"`
	// MaxRules caps how many rules come back (0 = 16).
	MaxRules int `json:"max_rules,omitempty"`
	// MaxChars caps the rendered text, in characters (0 = 2400).
	MaxChars int `json:"max_chars,omitempty"`
}

// StandingRule is one rule as given: its first lines, cut.
type StandingRule struct {
	ID         string  `json:"id"`
	Content    string  `json:"content"`
	Type       string  `json:"type,omitempty"`
	Importance float64 `json:"importance"`
	Project    string  `json:"project,omitempty"`
	Date       string  `json:"date,omitempty"`
	// Truncated is true when Content is cut; memory_get has the rest.
	Truncated bool `json:"truncated,omitempty"`
}

// StandingRulesResponse lists the rules most important first, and the text an
// agent is handed.
type StandingRulesResponse struct {
	Rules []StandingRule `json:"rules"`
	// Total is how many current rules qualified before the caps.
	Total int `json:"total"`
	// Text is the rules as one block, empty when there are none.
	Text string `json:"text"`
}

// StandingRulesHeader starts StandingRulesResponse.Text, so a hook can tell
// the block is already in a session's context.
const StandingRulesHeader = "Standing rules from CortexDB"

// IsStandingRule reports whether a memory is a standing rule by its stored
// scope, type, importance and currency, with the default type set and
// threshold. Projects are not considered: a recall that found the rule
// already judged it relevant.
func IsStandingRule(m MemoryRecord) bool {
	return standingRuleMatch(m, StandingRuleTypes, DefaultStandingRuleMinImportance)
}

func standingRuleMatch(m MemoryRecord, types []string, minImportance float64) bool {
	scope := m.Scope
	if scope == "" {
		scope = scopeFromBucketID(m.SessionID)
	}
	if scope != MemoryScopeGlobal {
		return false
	}
	typ, _ := stringFromAny(m.Metadata["type"])
	if !standingTypeIn(types, typ) {
		return false
	}
	if m.Importance < minImportance {
		return false
	}
	return !memorySuperseded(m) && !memoryExpired(m) && m.ReviewStatus != ReviewRejected
}

func standingTypeIn(list []string, s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, v := range list {
		if strings.EqualFold(strings.TrimSpace(v), s) {
			return true
		}
	}
	return false
}

// standingProjectKey compares project names without case or punctuation:
// "cortex-db", "CortexDB" and "cortex_db" are one project.
func standingProjectKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// StandingRules returns the current standing rules, most important first,
// within the request's count and character caps.
func (db *DB) StandingRules(ctx context.Context, req StandingRulesRequest) (*StandingRulesResponse, error) {
	types := req.Types
	if len(types) == 0 {
		types = StandingRuleTypes
	}
	minImportance := DefaultStandingRuleMinImportance
	if req.MinImportance != nil {
		minImportance = *req.MinImportance
	}
	maxRules := req.MaxRules
	if maxRules <= 0 {
		maxRules = defaultStandingRulesMax
	}
	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = defaultStandingRulesMaxChars
	}

	lowered := make([]string, 0, len(types))
	placeholders := make([]string, 0, len(types))
	for _, t := range types {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
			lowered = append(lowered, t)
			placeholders = append(placeholders, "?")
		}
	}
	resp := &StandingRulesResponse{Rules: []StandingRule{}}
	if len(lowered) == 0 {
		return resp, nil
	}
	args := make([]any, 0, len(lowered))
	for _, t := range lowered {
		args = append(args, t)
	}
	typeExpr := db.Dialect().JSONTextGuarded("m.metadata", "type")
	rows, err := db.query(ctx, `
		SELECT m.id, m.session_id, s.user_id, m.role, m.content, m.metadata, m.created_at
		FROM messages m
		JOIN sessions s ON s.id = m.session_id
		WHERE m.session_id LIKE 'memory:global:%'
		  AND LOWER(`+typeExpr+`) IN (`+strings.Join(placeholders, ", ")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("standing rules: %w", err)
	}
	var candidates []MemoryRecord
	for rows.Next() {
		var record MemoryRecord
		var metadataJSON []byte
		var createdAt time.Time
		if err := rows.Scan(&record.ID, &record.SessionID, &record.UserID, &record.Role, &record.Content, &metadataJSON, &createdAt); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan standing rule: %w", err)
		}
		record.CreatedAt = createdAt
		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &record.Metadata); err != nil {
				continue
			}
		}
		applyMemoryMetadata(&record)
		candidates = append(candidates, record)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	project := standingProjectKey(req.Project)
	permitted := map[string]bool{}
	var kept []MemoryRecord
	for _, m := range candidates {
		if !standingRuleMatch(m, types, minImportance) || db.memoryFilteredBy(ctx, m) != "" {
			continue
		}
		if p, _ := stringFromAny(m.Metadata["project"]); strings.TrimSpace(p) != "" && standingProjectKey(p) != project {
			continue
		}
		ok, seen := permitted[m.SessionID]
		if !seen {
			if ok, err = db.memoryReadPermitted(ctx, m.SessionID); err != nil {
				return nil, err
			}
			permitted[m.SessionID] = ok
		}
		if ok {
			kept = append(kept, m)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].Importance != kept[j].Importance {
			return kept[i].Importance > kept[j].Importance
		}
		if !kept[i].CreatedAt.Equal(kept[j].CreatedAt) {
			return kept[i].CreatedAt.After(kept[j].CreatedAt)
		}
		return kept[i].ID < kept[j].ID
	})
	resp.Total = len(kept)

	header := func(shown int) string {
		if shown < resp.Total {
			return fmt.Sprintf("%s: they hold for every task in this session, whatever the prompt (%d of %d; the rest with memory_standing_rules):", StandingRulesHeader, shown, resp.Total)
		}
		return StandingRulesHeader + ": they hold for every task in this session, whatever the prompt:"
	}
	var lines []string
	// The header is counted in the budget at its longest.
	used := utf8.RuneCountInString(header(0))
	for _, m := range kept {
		if len(resp.Rules) >= maxRules {
			break
		}
		content, cut := standingRuleLine(m.Content)
		line := fmt.Sprintf("- [%s] %s", m.ID, content)
		size := utf8.RuneCountInString(line) + 1
		if used+size > maxChars {
			continue
		}
		used += size
		lines = append(lines, line)
		date, _ := stringFromAny(m.Metadata["date"])
		if date == "" && !m.CreatedAt.IsZero() {
			date = m.CreatedAt.UTC().Format("2006-01-02")
		}
		typ, _ := stringFromAny(m.Metadata["type"])
		proj, _ := stringFromAny(m.Metadata["project"])
		resp.Rules = append(resp.Rules, StandingRule{
			ID: m.ID, Content: content, Type: typ, Importance: m.Importance,
			Project: proj, Date: date, Truncated: cut,
		})
	}
	if len(lines) > 0 {
		resp.Text = header(len(resp.Rules)) + "\n" + strings.Join(lines, "\n")
	}
	return resp, nil
}

// standingRuleLine flattens a rule and cuts it to standingRuleLineRunes.
func standingRuleLine(content string) (string, bool) {
	flat := strings.Join(strings.Fields(content), " ")
	runes := []rune(flat)
	if len(runes) <= standingRuleLineRunes {
		return flat, false
	}
	return strings.TrimSpace(string(runes[:standingRuleLineRunes])) + "…", true
}
