package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// searchSnippetRunes and searchFactLimit keep a text search to a screen:
// each hit is one line of its content, and the graph facts stop after a few.
// --format json prints the whole result.
const (
	searchSnippetRunes = 160
	searchFactLimit    = 10
)

// printRecall writes a knowledge_memory_recall result as a list of hits. It
// reports false when text is not a recall result, for the caller to print it
// as it came.
func printRecall(w io.Writer, text string) (bool, error) {
	var res cortexdb.KnowledgeMemoryRecallResponse
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		return false, nil
	}
	var b strings.Builder
	if len(res.Profiles) > 0 {
		b.WriteString("Profiles\n")
		for _, p := range res.Profiles {
			fmt.Fprintf(&b, "  %s  %s\n", p.Name, p.EntityID)
			writeSnippet(&b, p.Content)
		}
	}
	if len(res.Memories) > 0 {
		b.WriteString("Memories\n")
		for _, h := range res.Memories {
			line := fmt.Sprintf("  %.3f  %s", h.Score, h.Memory.ID)
			if len(h.Via) > 0 {
				line += "  (" + strings.Join(h.Via, ", ") + ")"
			}
			if !h.Memory.CreatedAt.IsZero() {
				line += "  " + h.Memory.CreatedAt.Format("2006-01-02")
			}
			b.WriteString(line + "\n")
			writeSnippet(&b, h.Memory.Content)
		}
	}
	if len(res.Knowledge) > 0 {
		b.WriteString("Knowledge\n")
		for _, h := range res.Knowledge {
			line := fmt.Sprintf("  %.3f  %s", h.Score, h.KnowledgeID)
			if h.Title != "" {
				line += "  " + h.Title
			}
			b.WriteString(line + "\n")
			writeSnippet(&b, h.Snippet)
		}
	}
	if len(res.GraphFacts) > 0 {
		b.WriteString("Graph facts\n")
		for i, f := range res.GraphFacts {
			if i == searchFactLimit {
				fmt.Fprintf(&b, "  … %d more\n", len(res.GraphFacts)-searchFactLimit)
				break
			}
			fmt.Fprintf(&b, "  %s —%s→ %s\n", f.Subject, f.Predicate, f.Object)
		}
	}
	if b.Len() == 0 {
		b.WriteString("no results\n")
	}
	if len(res.Degraded) > 0 {
		fmt.Fprintf(&b, "degraded: %s\n", strings.Join(res.Degraded, ", "))
	}
	_, err := io.WriteString(w, b.String())
	return true, err
}

// writeSnippet writes the start of s on one indented line.
func writeSnippet(b *strings.Builder, s string) {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return
	}
	if utf8.RuneCountInString(s) > searchSnippetRunes {
		s = string([]rune(s)[:searchSnippetRunes]) + "…"
	}
	b.WriteString("         " + s + "\n")
}
