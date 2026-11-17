package liveview

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Standing rules on the Recalls page: its Rules tab (/recalls?tab=rules) and
// the rules group a recall's detail opens with.
//
// Both come through [Source.Call], as every recall route does, so the local
// file, a shared brain and a host's own Source answer them the same way, and
// a Source whose caller does not run memory_standing_rules — a brain older
// than the tool, a key confined to a scope, which the brain refuses it — is
// a failed read on the tab and no rules group on a recall, never an empty
// list of rules. No hook was added to Source for them.

// StandingRulesView is what the Rules tab draws: every standing rule in the
// order the session start considers them, each with where it stands against
// its caps, and the near misses.
type StandingRulesView struct {
	// Project is the session the injection was worked out for; "" is a
	// session in no project.
	Project string `json:"project"`
	// Projects are the projects the rules and near misses name, for the
	// tab's picker.
	Projects []string          `json:"projects"`
	Rules    []StandingRuleRow `json:"rules"`
	// Injected counts the rules in the block, and Chars its length.
	Injected int `json:"injected"`
	Chars    int `json:"chars"`
	MaxRules int `json:"max_rules"`
	MaxChars int `json:"max_chars"`
	// NearMisses are global memories one field short of a rule; NearMissTotal
	// counts them past the ones listed.
	NearMisses    []StandingRuleRow `json:"near_misses"`
	NearMissTotal int               `json:"near_miss_total"`
	// Partial is set when the brain answered without the full list (older
	// than the all option): Rules then holds only the injected ones and
	// NearMisses nothing.
	Partial bool `json:"partial,omitempty"`
}

// StandingRuleRow is one rule or near miss, with the graph node standing for
// it.
type StandingRuleRow struct {
	cortexdb.StandingRuleEntry
	NodeID string `json:"node_id"`
}

// handleRecallRules answers the Rules tab: memory_standing_rules with all.
func (s *Server) handleRecallRules(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	ctx, cancel := s.exploreContext(r)
	defer cancel()
	var out cortexdb.StandingRulesResponse
	if err := callInto(ctx, s.src.Call, "memory_standing_rules", cortexdb.StandingRulesRequest{Project: project, All: true}, &out); err != nil {
		writeExplore(w, nil, err)
		return
	}
	writeExplore(w, standingRulesView(project, out), nil)
}

// standingRulesView reshapes the tool's answer for the page.
func standingRulesView(project string, out cortexdb.StandingRulesResponse) StandingRulesView {
	v := StandingRulesView{
		Project:       project,
		Projects:      []string{},
		Rules:         []StandingRuleRow{},
		NearMisses:    []StandingRuleRow{},
		Injected:      len(out.Rules),
		Chars:         out.Chars,
		MaxRules:      out.MaxRules,
		MaxChars:      out.MaxChars,
		NearMissTotal: out.NearMissTotal,
	}
	// A brain older than the all option ignores it: the caps are the marker,
	// set whenever it was understood.
	if out.MaxChars == 0 {
		v.Partial = true
		v.Chars = len([]rune(out.Text))
		for _, r := range out.Rules {
			v.Rules = append(v.Rules, ruleRow(cortexdb.StandingRuleEntry{
				ID: r.ID, Content: r.Content, Truncated: r.Truncated, Type: r.Type, Importance: r.Importance,
				Project: r.Project, Date: r.Date, Status: cortexdb.StandingRuleInjected,
			}))
		}
	}
	for _, e := range out.Ranked {
		v.Rules = append(v.Rules, ruleRow(e))
	}
	for _, e := range out.NearMisses {
		v.NearMisses = append(v.NearMisses, ruleRow(e))
	}
	seen := map[string]bool{}
	add := func(p string) {
		if p = strings.TrimSpace(p); p != "" && !seen[strings.ToLower(p)] {
			seen[strings.ToLower(p)] = true
			v.Projects = append(v.Projects, p)
		}
	}
	add(project)
	for _, rows := range [][]StandingRuleRow{v.Rules, v.NearMisses} {
		for _, r := range rows {
			add(r.Project)
		}
	}
	slices.SortFunc(v.Projects, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return v
}

func ruleRow(e cortexdb.StandingRuleEntry) StandingRuleRow {
	return StandingRuleRow{StandingRuleEntry: e, NodeID: "memory:" + e.ID}
}

// RecallWithRules is a logged recall as the detail reads it.
type RecallWithRules struct {
	cortexdb.RecallLogEntry
	// RulesNow is set when the log predates rule marks and the memory hits
	// were marked by what their memories are now (see markRecallRules).
	RulesNow bool `json:"rules_now,omitempty"`
}

// markRecallRules makes sure each memory hit says whether it is a standing
// rule.
//
// A recall logged since the log kept it carries the mark itself: whether the
// memory was set apart in that recall's rules section, when it ran. That is
// the record, and it is left as it is. A recall logged before has no marks;
// its memories are read now (memory_get) and marked by their metadata now
// (cortexdb.IsStandingRule), and RulesNow says the marks are today's. When
// the memories cannot be read the recall is answered unmarked: the rules
// group is a reading of the recall, and the recall is still the answer.
func (s *Server) markRecallRules(ctx context.Context, e cortexdb.RecallLogEntry) RecallWithRules {
	out := RecallWithRules{RecallLogEntry: e}
	var unmarked []string
	for _, h := range e.Hits {
		if h.Kind == "memory" && h.Rule == nil {
			unmarked = append(unmarked, h.ID)
		}
	}
	if len(unmarked) == 0 {
		return out
	}
	if len(unmarked) > cortexdb.MaxListedIDs {
		unmarked = unmarked[:cortexdb.MaxListedIDs]
	}
	var got cortexdb.MemoryGetResponse
	if err := callInto(ctx, s.src.Call, "memory_get", cortexdb.MemoryGetRequest{MemoryIDs: unmarked}, &got); err != nil {
		return out
	}
	rules := map[string]bool{}
	for _, m := range got.Memories {
		rules[m.ID] = cortexdb.IsStandingRule(m)
	}
	hits := slices.Clone(e.Hits)
	for i := range hits {
		if hits[i].Kind != "memory" || hits[i].Rule != nil {
			continue
		}
		if rule, ok := rules[hits[i].ID]; ok {
			hits[i].Rule = &rule
		}
	}
	out.Hits = hits
	out.RulesNow = true
	return out
}
