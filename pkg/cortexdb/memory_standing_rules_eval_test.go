package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode"
)

// The standing-rules reach eval: does the rule that governs a task reach the
// agent when the task's words do not mention it?
//
// A fixture brain holds eleven standing rules (global preferences saved at
// 0.85 to 0.95), five memories that look like rules and are not (superseded,
// low importance, another project's, a decision, session scope) and 157
// ordinary facts, decisions, lessons and project notes, many of them about
// ports, servers, commits and UIs. Thirty task prompts are labelled with the
// rules that apply to them, most sharing no word with the rule; five apply
// none.
//
// Before is what main gives an agent: the per-prompt recall alone, asked the
// way the hooks ask without a planner (the prompt and its words, top 3). After
// adds the standing rules handed over at session start. The test asserts
// floors under the numbers measured when this was written, and logs the table.
//
// Optional, off by default and never in CI:
//
//	CORTEXDB_RULES_EVAL_OLLAMA=http://127.0.0.1:11434  also run with embeddinggemma
//	CORTEXDB_RULES_EVAL_SNAPSHOT=<copy of a brain>     run on that copy instead
//	CORTEXDB_RULES_EVAL_LABELS=<labels.json>           its labelled prompts
//	CORTEXDB_RULES_EVAL_OUT=<file.json>                write per-prompt results

type rulesEvalFixture struct {
	Rules []struct {
		ID         string  `json:"id"`
		Importance float64 `json:"importance"`
		Content    string  `json:"content"`
	} `json:"rules"`
	NotRules []struct {
		ID               string  `json:"id"`
		Type             string  `json:"type"`
		Importance       float64 `json:"importance"`
		Project          string  `json:"project"`
		Scope            string  `json:"scope"`
		SupersededByRule string  `json:"superseded_by_rule"`
		Content          string  `json:"content"`
	} `json:"not_rules"`
	Prompts []rulesEvalPrompt `json:"prompts"`
}

// rulesEvalPrompt is a task prompt and the rules that apply to it. Each rule
// is a set of memory ids any one of which carries it: a real brain holds the
// same rule in several memories.
type rulesEvalPrompt struct {
	ID      string     `json:"id"`
	Prompt  string     `json:"prompt"`
	Rules   []string   `json:"rules,omitempty"`
	AnyOf   [][]string `json:"any_of,omitempty"`
	Project string     `json:"project,omitempty"`
}

func (p rulesEvalPrompt) groups() [][]string {
	if len(p.AnyOf) > 0 {
		return p.AnyOf
	}
	out := make([][]string, 0, len(p.Rules))
	for _, id := range p.Rules {
		out = append(out, []string{id})
	}
	return out
}

type rulesEvalDistractor struct {
	ID         string  `json:"id"`
	Type       string  `json:"type"`
	Importance float64 `json:"importance"`
	Project    string  `json:"project"`
	Content    string  `json:"content"`
}

func loadRulesEvalFixture(t *testing.T) (rulesEvalFixture, []rulesEvalDistractor) {
	t.Helper()
	var fx rulesEvalFixture
	var ds []rulesEvalDistractor
	for path, into := range map[string]any{"rules.json": &fx, "distractors.json": &ds} {
		raw, err := os.ReadFile(filepath.Join("testdata", "standing_rules", path))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	return fx, ds
}

func seedRulesEvalBrain(t *testing.T, db *DB, fx rulesEvalFixture, ds []rulesEvalDistractor) {
	t.Helper()
	ctx := context.Background()
	save := func(req MemorySaveRequest) {
		if _, err := db.SaveMemory(ctx, req); err != nil {
			t.Fatalf("save %s: %v", req.MemoryID, err)
		}
	}
	for _, d := range ds {
		req := MemorySaveRequest{MemoryID: d.ID, Scope: MemoryScopeGlobal, Content: d.Content, Importance: d.Importance,
			Metadata: map[string]any{"type": d.Type, "project": d.Project}}
		if d.Project != "" {
			req.Entities = []ToolEntityInput{{Name: d.Project, Type: "project"}}
		}
		save(req)
	}
	supersedes := map[string][]string{}
	for _, n := range fx.NotRules {
		meta := map[string]any{"type": n.Type}
		if n.Project != "" {
			meta["project"] = n.Project
		}
		req := MemorySaveRequest{MemoryID: n.ID, Scope: MemoryScopeGlobal, Content: n.Content, Importance: n.Importance, Metadata: meta}
		if n.Scope == MemoryScopeSession {
			req.Scope, req.SessionID = MemoryScopeSession, "eval-session"
		}
		save(req)
		if n.SupersededByRule != "" {
			supersedes[n.SupersededByRule] = append(supersedes[n.SupersededByRule], n.ID)
		}
	}
	for _, r := range fx.Rules {
		save(MemorySaveRequest{MemoryID: r.ID, Scope: MemoryScopeGlobal, Content: r.Content, Importance: r.Importance,
			Metadata: map[string]any{"type": "preference", "source": "CLAUDE.md"}, Supersedes: supersedes[r.ID]})
	}
}

// rulesEvalWords is the hooks' keyword split: letter and digit runs,
// lowercased, two characters or more.
func rulesEvalWords(prompt string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToLower(prompt), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(f)) >= 2 && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

type rulesEvalRow struct {
	ID           string   `json:"id"`
	Prompt       string   `json:"prompt"`
	Applicable   int      `json:"applicable"`
	RecallHits   []string `json:"recall_memory_ids"`
	RecallRules  int      `json:"recall_reached"`
	AfterRules   int      `json:"after_reached"`
	BeforeRanks  []int    `json:"before_ranks,omitempty"`
	RulesFirst   bool     `json:"rules_section_first"`
	RuleMemories int      `json:"recalled_rule_memories"`
	RecallMs     float64  `json:"recall_ms"`
}

type rulesEvalReport struct {
	Mode           string         `json:"mode"`
	Pairs          int            `json:"pairs"`
	BeforePairs    int            `json:"before_pairs"`
	AfterPairs     int            `json:"after_pairs"`
	Prompts        int            `json:"prompts"`
	BeforePrompts  int            `json:"before_prompts_all_rules"`
	AfterPrompts   int            `json:"after_prompts_all_rules"`
	BeforeRanks    []int          `json:"before_ranks"`
	BehindFacts    int            `json:"before_behind_graph_facts"`
	RulesFirst     int            `json:"recalls_with_rules_section_first"`
	RulesRecalled  int            `json:"recalls_with_rule_memories"`
	SessionRules   int            `json:"session_rules"`
	SessionTotal   int            `json:"session_rules_total"`
	SessionChars   int            `json:"session_chars"`
	SessionIDs     []string       `json:"session_rule_ids"`
	SessionMs      float64        `json:"session_ms"`
	RecallP50      float64        `json:"recall_p50_ms"`
	RecallP95      float64        `json:"recall_p95_ms"`
	NegPrompts     int            `json:"no_rule_prompts"`
	NegRecallRules int            `json:"no_rule_prompts_recall_rule_memories"`
	Rows           []rulesEvalRow `json:"rows"`
}

func (r rulesEvalReport) table() string {
	pct := func(n, d int) string {
		if d == 0 {
			return "-"
		}
		return fmt.Sprintf("%.1f%% (%d/%d)", 100*float64(n)/float64(d), n, d)
	}
	ranks := "-"
	if len(r.BeforeRanks) > 0 {
		sum := 0
		for _, k := range r.BeforeRanks {
			sum += k
		}
		ranks = fmt.Sprintf("mean %.2f %v", float64(sum)/float64(len(r.BeforeRanks)), r.BeforeRanks)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n[%s]\n", r.Mode)
	fmt.Fprintf(&b, "rule reach (prompt, rule) pairs   before %s   after %s\n", pct(r.BeforePairs, r.Pairs), pct(r.AfterPairs, r.Pairs))
	fmt.Fprintf(&b, "prompts with every rule reached   before %s   after %s\n", pct(r.BeforePrompts, r.Prompts), pct(r.AfterPrompts, r.Prompts))
	fmt.Fprintf(&b, "rule rank among recalled memories before %s, behind graph facts in %d   after: rules section first in %d of %d recalls holding a rule\n", ranks, r.BehindFacts, r.RulesFirst, r.RulesRecalled)
	fmt.Fprintf(&b, "session start                     %d of %d rules, %d chars (~%d tokens), %.1f ms\n", r.SessionRules, r.SessionTotal, r.SessionChars, (r.SessionChars+3)/4, r.SessionMs)
	fmt.Fprintf(&b, "recall latency                    p50 %.1f ms   p95 %.1f ms\n", r.RecallP50, r.RecallP95)
	fmt.Fprintf(&b, "no-rule prompts                   %d prompts, recall returned %d rule memories\n", r.NegPrompts, r.NegRecallRules)
	return b.String()
}

func percentile(ms []float64, p float64) float64 {
	if len(ms) == 0 {
		return 0
	}
	s := append([]float64(nil), ms...)
	sort.Float64s(s)
	i := int(p*float64(len(s)-1) + 0.5)
	return s[i]
}

func runRulesEval(t *testing.T, db *DB, mode string, prompts []rulesEvalPrompt) rulesEvalReport {
	t.Helper()
	ctx := context.Background()
	rep := rulesEvalReport{Mode: mode}

	sessions := map[string]map[string]bool{}
	sessionFor := func(project string) map[string]bool {
		if got, ok := sessions[project]; ok {
			return got
		}
		start := time.Now()
		resp, err := db.StandingRules(ctx, StandingRulesRequest{Project: project})
		if err != nil {
			t.Fatalf("standing rules: %v", err)
		}
		if project == "" {
			rep.SessionMs = float64(time.Since(start).Microseconds()) / 1000
			rep.SessionRules, rep.SessionTotal, rep.SessionChars = len(resp.Rules), resp.Total, len([]rune(resp.Text))
		}
		ids := map[string]bool{}
		for _, r := range resp.Rules {
			ids[r.ID] = true
			if project == "" {
				rep.SessionIDs = append(rep.SessionIDs, r.ID)
			}
		}
		sessions[project] = ids
		return ids
	}
	sessionFor("")

	var latencies []float64
	for _, p := range prompts {
		start := time.Now()
		resp, err := db.KnowledgeMemory().Recall(ctx, KnowledgeMemoryRecallRequest{
			Query:         p.Prompt,
			Keywords:      rulesEvalWords(p.Prompt),
			RetrievalMode: RetrievalModeAuto,
			GraphLight:    true,
			TopKMemories:  3,
			TopKKnowledge: 3,
		})
		if err != nil {
			t.Fatalf("%s: recall: %v", p.ID, err)
		}
		ms := float64(time.Since(start).Microseconds()) / 1000
		latencies = append(latencies, ms)

		row := rulesEvalRow{ID: p.ID, Prompt: p.Prompt, RecallMs: ms}
		rank := map[string]int{}
		for i, m := range resp.Memories {
			row.RecallHits = append(row.RecallHits, m.Memory.ID)
			rank[m.Memory.ID] = i + 1
			if IsStandingRule(m.Memory) {
				row.RuleMemories++
			}
		}
		if row.RuleMemories > 0 {
			rep.RulesRecalled++
			if s := resp.ContextPack.Sections; len(s) > 0 && s[0].Kind == "rules" {
				row.RulesFirst = true
				rep.RulesFirst++
			}
		}
		session := sessionFor(p.Project)
		groups := p.groups()
		row.Applicable = len(groups)
		for _, g := range groups {
			best, inSession := 0, false
			for _, id := range g {
				if k := rank[id]; k > 0 && (best == 0 || k < best) {
					best = k
				}
				inSession = inSession || session[id]
			}
			if best > 0 {
				if len(resp.GraphFacts) > 0 {
					// main's pack put graph facts before every memory.
					rep.BehindFacts++
				}
				row.RecallRules++
				row.BeforeRanks = append(row.BeforeRanks, best)
			}
			if best > 0 || inSession {
				row.AfterRules++
			}
		}
		if len(groups) == 0 {
			rep.NegPrompts++
			rep.NegRecallRules += row.RuleMemories
		} else {
			rep.Prompts++
			rep.Pairs += len(groups)
			rep.BeforePairs += row.RecallRules
			rep.AfterPairs += row.AfterRules
			rep.BeforeRanks = append(rep.BeforeRanks, row.BeforeRanks...)
			if row.RecallRules == len(groups) {
				rep.BeforePrompts++
			}
			if row.AfterRules == len(groups) {
				rep.AfterPrompts++
			}
		}
		rep.Rows = append(rep.Rows, row)
	}
	rep.RecallP50, rep.RecallP95 = percentile(latencies, 0.5), percentile(latencies, 0.95)
	return rep
}

func writeRulesEvalOut(t *testing.T, reports ...rulesEvalReport) {
	path := os.Getenv("CORTEXDB_RULES_EVAL_OUT")
	if path == "" {
		return
	}
	raw, _ := json.MarshalIndent(reports, "", "  ")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Errorf("write %s: %v", path, err)
	}
}

func TestStandingRulesReachEval(t *testing.T) {
	if os.Getenv("CORTEXDB_RULES_EVAL_SNAPSHOT") != "" {
		t.Skip("snapshot run: see TestStandingRulesReachEvalSnapshot")
	}
	fx, ds := loadRulesEvalFixture(t)
	open := func(t *testing.T, opts ...Option) *DB {
		db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "rules.db")), opts...)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		seedRulesEvalBrain(t, db, fx, ds)
		return db
	}

	db := open(t)
	rep := runRulesEval(t, db, "fixture, lexical", fx.Prompts)
	t.Log(rep.table())
	reports := []rulesEvalReport{rep}

	// Floors under what was measured when this was written: after 100% of
	// pairs and prompts; before 14.0% of pairs.
	if got := float64(rep.AfterPairs) / float64(rep.Pairs); got < 0.95 {
		t.Errorf("after reach %.3f, want >= 0.95", got)
	}
	if rep.AfterPrompts < rep.Prompts-1 {
		t.Errorf("prompts with every rule reached after: %d of %d", rep.AfterPrompts, rep.Prompts)
	}
	if rep.AfterPairs <= rep.BeforePairs {
		t.Errorf("session start added nothing: before %d, after %d", rep.BeforePairs, rep.AfterPairs)
	}
	if rep.RulesFirst != rep.RulesRecalled {
		t.Errorf("rules section first in %d of %d recalls holding a rule", rep.RulesFirst, rep.RulesRecalled)
	}
	if rep.SessionRules != len(fx.Rules) || rep.SessionChars > defaultStandingRulesMaxChars {
		t.Errorf("session start gave %d rules in %d chars, want %d within %d", rep.SessionRules, rep.SessionChars, len(fx.Rules), defaultStandingRulesMaxChars)
	}

	// What looks like a rule and is not stays out of the session block.
	ctx := context.Background()
	got, err := db.StandingRules(ctx, StandingRulesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	given := map[string]bool{}
	for _, r := range got.Rules {
		given[r.ID] = true
	}
	for _, n := range fx.NotRules {
		if given[n.ID] {
			t.Errorf("%s is not a standing rule but was given", n.ID)
		}
	}
	// Another project's rule is given in that project only.
	athanor, err := db.StandingRules(ctx, StandingRulesRequest{Project: "Athanor"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(athanor.Text, "pref-athanor-shadcn") || athanor.Total != got.Total+1 {
		t.Errorf("athanor session: total %d (global %d):\n%s", athanor.Total, got.Total, athanor.Text)
	}
	// The order is importance first, and the caps hold.
	for i := 1; i < len(got.Rules); i++ {
		if got.Rules[i].Importance > got.Rules[i-1].Importance {
			t.Fatalf("not ordered by importance: %+v", got.Rules)
		}
	}
	capped, err := db.StandingRules(ctx, StandingRulesRequest{MaxRules: 3, MaxChars: 400})
	if err != nil {
		t.Fatal(err)
	}
	if len(capped.Rules) > 3 || len([]rune(capped.Text)) > 400 || capped.Total != got.Total || !strings.Contains(capped.Text, "memory_standing_rules") {
		t.Errorf("caps not kept: %d rules, %d chars:\n%s", len(capped.Rules), len([]rune(capped.Text)), capped.Text)
	}

	if url := os.Getenv("CORTEXDB_RULES_EVAL_OLLAMA"); url != "" {
		emb := NewOpenAIEmbedder(OpenAIEmbedderConfig{BaseURL: strings.TrimRight(url, "/") + "/v1", Model: "embeddinggemma", Dim: 768})
		edb := open(t, WithEmbedder(emb))
		erep := runRulesEval(t, edb, "fixture, embeddinggemma", fx.Prompts)
		t.Log(erep.table())
		reports = append(reports, erep)
	}
	writeRulesEvalOut(t, reports...)
}

// TestStandingRulesReachEvalSnapshot runs the eval on a copy of a real brain
// with labels kept outside the repository. Recall writes recall counts, so it
// must be a copy.
func TestStandingRulesReachEvalSnapshot(t *testing.T) {
	path, labels := os.Getenv("CORTEXDB_RULES_EVAL_SNAPSHOT"), os.Getenv("CORTEXDB_RULES_EVAL_LABELS")
	if path == "" || labels == "" {
		t.Skip("set CORTEXDB_RULES_EVAL_SNAPSHOT and CORTEXDB_RULES_EVAL_LABELS")
	}
	raw, err := os.ReadFile(labels)
	if err != nil {
		t.Fatal(err)
	}
	var prompts []rulesEvalPrompt
	if err := json.Unmarshal(raw, &prompts); err != nil {
		t.Fatal(err)
	}
	var reports []rulesEvalReport
	run := func(mode string, opts ...Option) {
		db, err := Open(DefaultConfig(path), opts...)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		rep := runRulesEval(t, db, mode, prompts)
		t.Log(rep.table())
		reports = append(reports, rep)
	}
	run("snapshot, lexical")
	if url := os.Getenv("CORTEXDB_RULES_EVAL_OLLAMA"); url != "" {
		run("snapshot, embeddinggemma", WithEmbedder(NewOpenAIEmbedder(OpenAIEmbedderConfig{BaseURL: strings.TrimRight(url, "/") + "/v1", Model: "embeddinggemma", Dim: 768})))
	}
	writeRulesEvalOut(t, reports...)
}
