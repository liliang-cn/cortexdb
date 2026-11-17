package cortexdb

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Recall detail levels.
//
// A full recall returns every hit whole, which is right when the caller will
// read them all and wasteful when it is choosing which to read: a dozen
// memories and chunks can be several thousand tokens, most of them for hits
// the caller then ignores. The index level returns the same hits in the same
// order, each cut to its first line, so the caller can pick by id and fetch
// only those with memory_get (memory_ids) or knowledge_get (knowledge_ids).
const (
	RecallDetailFull  = "full"
	RecallDetailIndex = "index"
)

// indexLineRunes is how much of a hit an index line keeps.
const indexLineRunes = 120

// firstLine is the first non-empty line of s, cut to max runes.
func firstLine(s string, max int) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		if utf8.RuneCountInString(line) <= max {
			return line
		}
		runes := []rune(line)
		return strings.TrimSpace(string(runes[:max])) + "…"
	}
	return ""
}

// indexRecall cuts a recall response down to an index of what it found.
func indexRecall(resp *KnowledgeMemoryRecallResponse) {
	resp.Detail = RecallDetailIndex
	var b strings.Builder
	// Standing rules first, under their own heading, as in the full pack.
	var rules, others []int
	for i := range resp.Memories {
		if IsStandingRule(resp.Memories[i].Memory) {
			rules = append(rules, i)
		} else {
			others = append(others, i)
		}
	}
	var ruleIDs []string
	line := func(i int) {
		m := &resp.Memories[i].Memory
		m.Content = firstLine(m.Content, indexLineRunes)
		m.Metadata = nil
		date := ""
		if !m.CreatedAt.IsZero() {
			date = m.CreatedAt.Format("2006-01-02") + " "
		}
		fmt.Fprintf(&b, "- %s %s%s\n", m.ID, date, m.Content)
	}
	if len(rules) > 0 {
		b.WriteString("Standing rules (fetch with memory_get memory_ids):\n")
	}
	for _, i := range rules {
		ruleIDs = append(ruleIDs, resp.Memories[i].Memory.ID)
		line(i)
	}
	if len(others) > 0 {
		b.WriteString("Memories (fetch with memory_get memory_ids):\n")
	}
	for _, i := range others {
		line(i)
	}
	if len(resp.Knowledge) > 0 {
		b.WriteString("Knowledge (fetch with knowledge_get knowledge_ids):\n")
	}
	for i := range resp.Knowledge {
		k := &resp.Knowledge[i]
		k.Snippet = firstLine(k.Snippet, indexLineRunes)
		title := k.Title
		if title == "" {
			title = k.Snippet
		}
		fmt.Fprintf(&b, "- %s %s\n", k.KnowledgeID, firstLine(title, indexLineRunes))
	}
	for i := range resp.Chunks {
		resp.Chunks[i].Content = firstLine(resp.Chunks[i].Content, indexLineRunes)
	}
	if len(resp.GraphFacts) > 0 {
		b.WriteString("Graph facts:\n")
	}
	for _, f := range resp.GraphFacts {
		fmt.Fprintf(&b, "- %s %s %s\n", f.Subject, f.Predicate, f.Object)
	}
	resp.ContextPack.Text = strings.TrimRight(b.String(), "\n")
	resp.ContextPack.Sections = nil
	// The rules section is kept, ids only, so a client can still set the
	// rules apart without the metadata the index dropped.
	if len(ruleIDs) > 0 {
		resp.ContextPack.Sections = []KnowledgeMemoryContextSection{{
			Kind: "rules", Title: "Standing rules", Text: strings.Join(ruleIDs, "\n"), SourceIDs: ruleIDs,
		}}
	}
}
