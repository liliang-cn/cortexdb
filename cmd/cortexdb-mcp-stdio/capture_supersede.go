package main

// Retiring what a session made untrue.
//
// A capture only ever added. Say on Monday the deploy moved to node-b, and a
// memory from last month says node-a: both are recalled, both in the same
// confident voice, and nothing tells the reader which one is current. The
// brain grows more wrong the more it is used.
//
// So after a session's facts are drawn out, each is looked up against what the
// brain already holds, and one more model call is asked a narrow question:
// which of these existing memories does a new fact make untrue? Those are
// saved as superseded by the new one — kept, exported, linked forward, and no
// longer recalled as current. Superseding is the only thing it can do: a
// memory is often the only record that something was said, and "which older
// memory does this replace?" is the judgement a model makes confidently and
// wrongly, so the step it takes must be one a person can undo (clear
// superseded_by) and find (superseded_at).
//
// Time decides direction. A session imported months late can hold facts the
// brain has since moved past; those must not retire the newer memories.
// Instead they are dropped as already outdated. Either way, what is dated
// later wins.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

const (
	// supersedeCandidatesPerFact is how many existing memories each new fact
	// is compared with. Contradictions are about the same thing, so the top
	// few hits are where they are.
	supersedeCandidatesPerFact = 4
	// supersedeCandidateLimit bounds the prompt.
	supersedeCandidateLimit = 24
	// maxSupersedesPerSession caps the damage of one bad judgement.
	maxSupersedesPerSession = 5
)

// supersedeCandidate is an existing memory a new fact might contradict.
type supersedeCandidate struct {
	ID      string
	Content string
	Date    string // YYYY-MM-DD
}

// supersedePlan is what the model decided for one session's facts.
type supersedePlan struct {
	// Retire maps a new fact's slug to the older memories it makes untrue.
	Retire map[string][]string
	// Reason is the model's why, per slug.
	Reason map[string]string
	// Outdated are new facts that a later memory already contradicts.
	Outdated map[string]string // slug -> the later memory's id
}

const supersedeSystemPrompt = `You maintain an AI agent's long-term memory. A session just produced NEW memories. EXISTING memories are already stored, each with the date it was recorded.

Decide, for each NEW memory, whether it makes an EXISTING memory untrue: the same thing, with a different current value (a host moved, a version changed, a decision was reversed, a preference changed, a plan was dropped, something "not done yet" is now done). The memory dated later wins:
- "retire": a NEW memory replaces EXISTING memories dated on or before the session.
- "outdated": a NEW memory is itself contradicted by an EXISTING memory dated after the session; it should not be saved.

Be strict. Being about the same topic is not enough; adding detail is not a contradiction; two facts that can both be true are not one. When unsure, leave it out — an outdated memory left in place is a smaller harm than a true one retired.

Name memories exactly as listed: a NEW memory by its "new" name, an EXISTING one by its "old" id — never by a date.

Respond with JSON only: {"retire":[{"new":"<new name>","old":["<old id>"],"reason":"..."}],"outdated":[{"new":"<new name>","by":"<old id>"}]}`

// findCandidates looks each new fact up in the brain. ownPrefix is this
// session's id prefix: its own memories are rewritten in place by slug, not
// superseded.
func findCandidates(ctx context.Context, b brainClient, facts []capturedFact, ownPrefix string) []supersedeCandidate {
	seen := map[string]bool{}
	var out []supersedeCandidate
	for _, f := range facts {
		req := cortexdb.MemorySearchRequest{Query: clip(f.Content, 400), TopK: supersedeCandidatesPerFact}
		for _, e := range f.Entities {
			if name := strings.TrimSpace(e.Name); name != "" {
				req.EntityNames = append(req.EntityNames, name)
			}
		}
		hits, err := b.SearchMemory(ctx, req)
		if err != nil {
			// Finding nothing to retire is the safe failure: the capture
			// still saves, as it did before this step existed.
			continue
		}
		for _, h := range hits {
			id, date := h.Memory.ID, memoryDate(h.Memory)
			// Undated, it could be neither retired nor the later of two.
			if id == "" || date == "" || seen[id] || strings.HasPrefix(id, ownPrefix) {
				continue
			}
			seen[id] = true
			out = append(out, supersedeCandidate{ID: id, Content: h.Memory.Content, Date: date})
			if len(out) >= supersedeCandidateLimit {
				return out
			}
		}
	}
	return out
}

// memoryDate is the day a memory speaks for: the session it came from when it
// says, otherwise when it was stored.
func memoryDate(m cortexdb.MemoryRecord) string {
	if d, _ := m.Metadata["date"].(string); len(d) >= 10 {
		return d[:10]
	}
	if !m.CreatedAt.IsZero() {
		return m.CreatedAt.Format("2006-01-02")
	}
	return ""
}

// planSupersedes asks the model which candidates the new facts make untrue,
// and keeps only answers the dates allow.
func planSupersedes(ctx context.Context, llm interface {
	GenerateJSON(ctx context.Context, systemPrompt, userPrompt string) ([]byte, error)
}, facts []capturedFact, candidates []supersedeCandidate, asOf time.Time) (supersedePlan, error) {
	plan := supersedePlan{Retire: map[string][]string{}, Reason: map[string]string{}, Outdated: map[string]string{}}
	if len(facts) == 0 || len(candidates) == 0 {
		return plan, nil
	}
	session := asOf.Format("2006-01-02")
	slugs := map[string]bool{}
	for _, f := range facts {
		slugs[f.Slug] = true
	}
	byID := map[string]supersedeCandidate{}
	for _, c := range candidates {
		byID[c.ID] = c
	}

	raw, err := llm.GenerateJSON(ctx, supersedeSystemPrompt, supersedeUserPrompt(facts, candidates, session))
	if err != nil {
		return plan, fmt.Errorf("llm: %w", err)
	}
	var out struct {
		Retire []struct {
			New    string   `json:"new"`
			Old    []string `json:"old"`
			Reason string   `json:"reason"`
		} `json:"retire"`
		Outdated []struct {
			New string `json:"new"`
			By  string `json:"by"`
		} `json:"outdated"`
	}
	if err := json.Unmarshal(repairJSON(raw), &out); err != nil {
		return plan, fmt.Errorf("decode llm output: %w (raw: %s)", err, clip(string(raw), 200))
	}
	// Models embellish names ("later-deploy (2026-11-02)"); read back only
	// the names that were given, never one made up.
	slugList := keys(slugs)
	idList := keys(byID)

	// A memory dated after the session cannot be retired by it, and an
	// undated one is not retired at all: there is no telling which is later.
	for _, o := range out.Outdated {
		slug, id := nameIn(o.New, slugList), nameIn(o.By, idList)
		if slug != "" && id != "" && byID[id].Date > session {
			plan.Outdated[slug] = id
		}
	}
	budget := maxSupersedesPerSession
	retired := map[string]bool{}
	for _, r := range out.Retire {
		slug := nameIn(r.New, slugList)
		if slug == "" || plan.Outdated[slug] != "" {
			continue
		}
		for _, raw := range r.Old {
			id := nameIn(raw, idList)
			c, ok := byID[id]
			if !ok || retired[id] || c.Date == "" || c.Date > session || budget == 0 {
				continue
			}
			retired[id] = true
			budget--
			plan.Retire[slug] = append(plan.Retire[slug], id)
		}
		if len(plan.Retire[slug]) > 0 {
			plan.Reason[slug] = clip(strings.TrimSpace(r.Reason), 300)
		}
	}
	return plan, nil
}

// supersedeUserPrompt lists both sides so that names and dates cannot be
// confused: the name first, the date in words after it.
func supersedeUserPrompt(facts []capturedFact, candidates []supersedeCandidate, session string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The session is dated %s.\n\nNEW memories:\n", session)
	for _, f := range facts {
		fmt.Fprintf(&b, "- new %q: %s\n", f.Slug, clip(strings.TrimSpace(f.Content), 400))
	}
	b.WriteString("\nEXISTING memories:\n")
	for _, c := range candidates {
		fmt.Fprintf(&b, "- old %q, recorded %s: %s\n", c.ID, c.Date, clip(strings.TrimSpace(c.Content), 400))
	}
	return b.String()
}

// nameIn finds which of names s means: the name itself, or the longest name
// s starts with or quotes. Empty when it names none of them.
func nameIn(s string, names []string) string {
	s = strings.TrimSpace(s)
	best := ""
	for _, n := range names {
		switch {
		case s == n:
			return n
		case len(n) > len(best) && (strings.HasPrefix(s, n+" ") || strings.HasPrefix(s, n+"(") || strings.Contains(s, `"`+n+`"`)):
			best = n
		}
	}
	return best
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// applyPlan sets what the plan decided on a fact's save request. It reports
// false for a fact that should not be saved at all.
func (p supersedePlan) apply(slug string, req *cortexdb.MemorySaveRequest) bool {
	if _, outdated := p.Outdated[slug]; outdated {
		return false
	}
	if ids := p.Retire[slug]; len(ids) > 0 {
		req.Supersedes = append([]string(nil), ids...)
		sort.Strings(req.Supersedes)
		if req.Metadata == nil {
			req.Metadata = map[string]any{}
		}
		if reason := p.Reason[slug]; reason != "" {
			req.Metadata["supersede_reason"] = reason
		}
	}
	return true
}

// reconcileFacts is the whole step: look the facts up, ask, and return the
// plan. Any failure returns an empty plan — the capture then saves everything
// and retires nothing, which is what it did before.
func reconcileFacts(ctx context.Context, llm interface {
	GenerateJSON(ctx context.Context, systemPrompt, userPrompt string) ([]byte, error)
}, b brainClient, facts []capturedFact, ownPrefix string, asOf time.Time) (supersedePlan, error) {
	candidates := findCandidates(ctx, b, facts, ownPrefix)
	return planSupersedes(ctx, llm, facts, candidates, asOf)
}
