package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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
	// standingEntryRunes is how much of a memory a Ranked or NearMisses
	// entry keeps.
	standingEntryRunes = 2000
	// standingNearMissMax caps NearMisses; NearMissTotal counts them all.
	standingNearMissMax = 100
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
	// All also fills the response's Ranked, NearMisses and budget fields:
	// every rule that qualifies, whether or not it fit the caps, and the
	// global memories one field short of a rule. For a page that shows them;
	// a session start leaves it off.
	All bool `json:"all,omitempty"`
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

// Where a rule stands against a session start (StandingRuleEntry.Status).
const (
	// StandingRuleInjected: the rule is in the block.
	StandingRuleInjected = "injected"
	// StandingRuleOverBudget: the rule qualifies and the caps left it out.
	StandingRuleOverBudget = "over_budget"
	// StandingRuleOtherProject: the rule names a project other than the
	// request's, so this session is not given it.
	StandingRuleOtherProject = "other_project"
)

// What a near miss lacks (StandingRuleEntry.Missing).
const (
	// StandingRuleMissingType: no type metadata.
	StandingRuleMissingType = "type"
	// StandingRuleMissingImportance: importance under the minimum.
	StandingRuleMissingImportance = "importance"
)

// StandingRuleEntry is one memory as an All request lists it: a rule with
// where it stands, or a near miss with what it lacks.
type StandingRuleEntry struct {
	ID string `json:"id"`
	// Content is the memory's text, cut at 2000 characters.
	Content    string  `json:"content"`
	Truncated  bool    `json:"truncated,omitempty"`
	Type       string  `json:"type,omitempty"`
	Importance float64 `json:"importance"`
	Project    string  `json:"project,omitempty"`
	Date       string  `json:"date,omitempty"`
	// CreatedAt is when the memory was saved, RFC 3339.
	CreatedAt   string `json:"created_at,omitempty"`
	RecallCount int    `json:"recall_count"`
	// Status is StandingRuleInjected, StandingRuleOverBudget or
	// StandingRuleOtherProject for a rule, empty for a near miss.
	Status string `json:"status,omitempty"`
	// Missing names what a near miss lacks: StandingRuleMissingType or
	// StandingRuleMissingImportance.
	Missing []string `json:"missing,omitempty"`
}

// StandingRulesResponse lists the rules most important first, and the text an
// agent is handed.
type StandingRulesResponse struct {
	Rules []StandingRule `json:"rules"`
	// Total is how many current rules qualified before the caps.
	Total int `json:"total"`
	// Text is the rules as one block, empty when there are none.
	Text string `json:"text"`

	// Filled when All is asked, and absent otherwise.

	// Ranked is every rule that qualifies, in the order the caps consider
	// them, each with its Status; the rules of other projects come last.
	Ranked []StandingRuleEntry `json:"ranked,omitempty"`
	// NearMisses are current global memories one field short of a rule: a
	// rule's type under the minimum importance. Most important first, at
	// most 100.
	NearMisses    []StandingRuleEntry `json:"near_misses,omitempty"`
	NearMissTotal int                 `json:"near_miss_total,omitempty"`
	// Chars is the length of Text in characters; MaxRules and MaxChars are
	// the caps it was built under.
	Chars    int `json:"chars,omitempty"`
	MaxRules int `json:"max_rules,omitempty"`
	MaxChars int `json:"max_chars,omitempty"`
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
	if req.All {
		resp.MaxRules, resp.MaxChars = maxRules, maxChars
		resp.Ranked, resp.NearMisses = []StandingRuleEntry{}, []StandingRuleEntry{}
	}
	if len(lowered) == 0 {
		return resp, nil
	}
	args := make([]any, 0, len(lowered))
	for _, t := range lowered {
		args = append(args, t)
	}
	typeExpr := db.Dialect().JSONTextGuarded("m.metadata", "type")
	candidates, err := db.globalMemories(ctx, `LOWER(`+typeExpr+`) IN (`+strings.Join(placeholders, ", ")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("standing rules: %w", err)
	}

	permitted := map[string]bool{}
	readable := func(m MemoryRecord) (bool, error) {
		ok, seen := permitted[m.SessionID]
		if !seen {
			var err error
			if ok, err = db.memoryReadPermitted(ctx, m.SessionID); err != nil {
				return false, err
			}
			permitted[m.SessionID] = ok
		}
		return ok, nil
	}
	project := standingProjectKey(req.Project)
	// kept is what this session is given, elsewhere the rules of other
	// projects, low the current rules saved under the threshold.
	var kept, elsewhere, low []MemoryRecord
	for _, m := range candidates {
		if !standingRuleMatch(m, types, math.Inf(-1)) || db.memoryFilteredBy(ctx, m) != "" {
			continue
		}
		if m.Importance < minImportance && !req.All {
			continue
		}
		ok, err := readable(m)
		if err != nil {
			return nil, err
		}
		switch {
		case !ok:
		case m.Importance < minImportance:
			low = append(low, m)
		case standingProjectElsewhere(m, project):
			if req.All {
				elsewhere = append(elsewhere, m)
			}
		default:
			kept = append(kept, m)
		}
	}
	sortStanding(kept)
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
		status := StandingRuleOverBudget
		content, cut := standingRuleLine(m.Content)
		line := fmt.Sprintf("- [%s] %s", m.ID, content)
		size := utf8.RuneCountInString(line) + 1
		if len(resp.Rules) < maxRules && used+size <= maxChars {
			status = StandingRuleInjected
			used += size
			lines = append(lines, line)
			typ, _ := stringFromAny(m.Metadata["type"])
			proj, _ := stringFromAny(m.Metadata["project"])
			resp.Rules = append(resp.Rules, StandingRule{
				ID: m.ID, Content: content, Type: typ, Importance: m.Importance,
				Project: proj, Date: standingDate(m), Truncated: cut,
			})
		}
		if req.All {
			resp.Ranked = append(resp.Ranked, standingEntry(m, status))
		}
	}
	if len(lines) > 0 {
		resp.Text = header(len(resp.Rules)) + "\n" + strings.Join(lines, "\n")
	}
	if !req.All {
		return resp, nil
	}
	resp.Chars = utf8.RuneCountInString(resp.Text)
	sortStanding(elsewhere)
	for _, m := range elsewhere {
		resp.Ranked = append(resp.Ranked, standingEntry(m, StandingRuleOtherProject))
	}

	// The near misses: a rule's type under the threshold, read off the
	// metadata as the rules are. A memory with no type is not one: on a real
	// brain nearly all of those are facts some writer left untyped.
	var near []StandingRuleEntry
	for _, m := range low {
		e := standingEntry(m, "")
		e.Missing = []string{StandingRuleMissingImportance}
		near = append(near, e)
	}
	sort.SliceStable(near, func(i, j int) bool {
		if near[i].Importance != near[j].Importance {
			return near[i].Importance > near[j].Importance
		}
		if near[i].CreatedAt != near[j].CreatedAt {
			return near[i].CreatedAt > near[j].CreatedAt
		}
		return near[i].ID < near[j].ID
	})
	resp.NearMissTotal = len(near)
	if len(near) > standingNearMissMax {
		near = near[:standingNearMissMax]
	}
	resp.NearMisses = append(resp.NearMisses, near...)
	return resp, nil
}

// globalMemories reads the memories of the global buckets that match where.
func (db *DB) globalMemories(ctx context.Context, where string, args ...any) ([]MemoryRecord, error) {
	rows, err := db.query(ctx, `
		SELECT m.id, m.session_id, s.user_id, m.role, m.content, m.metadata, m.created_at
		FROM messages m
		JOIN sessions s ON s.id = m.session_id
		WHERE m.session_id LIKE 'memory:global:%'
		  AND `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []MemoryRecord
	for rows.Next() {
		var record MemoryRecord
		var metadataJSON []byte
		var createdAt time.Time
		if err := rows.Scan(&record.ID, &record.SessionID, &record.UserID, &record.Role, &record.Content, &metadataJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("scan standing rule: %w", err)
		}
		record.CreatedAt = createdAt
		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &record.Metadata); err != nil {
				continue
			}
		}
		applyMemoryMetadata(&record)
		out = append(out, record)
	}
	return out, rows.Err()
}

// standingCurrentGlobal is standingRuleMatch without the type and the
// importance: global, and current.
func standingCurrentGlobal(m MemoryRecord) bool {
	scope := m.Scope
	if scope == "" {
		scope = scopeFromBucketID(m.SessionID)
	}
	return scope == MemoryScopeGlobal && !memorySuperseded(m) && !memoryExpired(m) && m.ReviewStatus != ReviewRejected
}

// standingProjectElsewhere reports whether a rule names a project other than
// the session's (project is a standingProjectKey).
func standingProjectElsewhere(m MemoryRecord, project string) bool {
	p, _ := stringFromAny(m.Metadata["project"])
	return strings.TrimSpace(p) != "" && standingProjectKey(p) != project
}

// sortStanding puts rules in the order the session start considers them: most
// important first, then newest, then by id.
func sortStanding(rules []MemoryRecord) {
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Importance != rules[j].Importance {
			return rules[i].Importance > rules[j].Importance
		}
		if !rules[i].CreatedAt.Equal(rules[j].CreatedAt) {
			return rules[i].CreatedAt.After(rules[j].CreatedAt)
		}
		return rules[i].ID < rules[j].ID
	})
}

// standingDate is the memory's own date, else the day it was saved.
func standingDate(m MemoryRecord) string {
	date, _ := stringFromAny(m.Metadata["date"])
	if date == "" && !m.CreatedAt.IsZero() {
		date = m.CreatedAt.UTC().Format("2006-01-02")
	}
	return date
}

// standingEntry is one memory as StandingRulesResponse.Ranked and NearMisses
// list it.
func standingEntry(m MemoryRecord, status string) StandingRuleEntry {
	typ, _ := stringFromAny(m.Metadata["type"])
	proj, _ := stringFromAny(m.Metadata["project"])
	content, cut := m.Content, false
	if runes := []rune(content); len(runes) > standingEntryRunes {
		content, cut = string(runes[:standingEntryRunes])+"…", true
	}
	e := StandingRuleEntry{
		ID: m.ID, Content: content, Truncated: cut, Type: typ, Importance: m.Importance,
		Project: proj, Date: standingDate(m), RecallCount: memoryRecallCount(m.Metadata), Status: status,
	}
	if !m.CreatedAt.IsZero() {
		e.CreatedAt = m.CreatedAt.UTC().Format(time.RFC3339)
	}
	return e
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
